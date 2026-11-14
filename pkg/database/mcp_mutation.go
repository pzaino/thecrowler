package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	// MCPMutationCapability is the additive schema-1.17 capability marker.
	MCPMutationCapability = "mcp-mutation-authorization-v1"
	// MCPMutationCapabilityChecksum binds the marker to this implementation's
	// schema contract. It is deliberately not a schema version.
	MCPMutationCapabilityChecksum = "00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed"

	MCPMutationStateProposed               = "proposed"
	MCPMutationStateApprovalRequired       = "approval_required"
	MCPMutationStateApproved               = "approved"
	MCPMutationStateDenied                 = "denied"
	MCPMutationStateExpired                = "expired"
	MCPMutationStateDispatching            = "dispatching"
	MCPMutationStateSucceeded              = "succeeded"
	MCPMutationStateFailed                 = "failed"
	MCPMutationStateUncertain              = "uncertain"
	MCPMutationStateReconciliationRequired = "reconciliation_required"
)

var (
	ErrMCPMutationStoreUnsupported  = errors.New("durable MCP mutation persistence is unsupported by this database backend")
	ErrMCPMutationSchemaUnavailable = errors.New("MCP mutation schema capability is unavailable")
	ErrMCPMutationProposalNotFound  = errors.New("MCP mutation proposal was not found")
	ErrMCPMutationProposalConflict  = errors.New("MCP mutation proposal conflicts with an existing logical proposal")
	ErrMCPMutationProposalExpired   = errors.New("MCP mutation proposal has expired")
	ErrMCPMutationQuotaReached      = errors.New("MCP mutation proposal quota reached")
	ErrMCPMutationApprovalNotFound  = errors.New("MCP mutation approval was not found")
	ErrMCPMutationApprovalConflict  = errors.New("MCP mutation approval conflicts with an existing approval")
	ErrMCPMutationApprovalExpired   = errors.New("MCP mutation approval has expired")
	ErrMCPMutationApprovalRevoked   = errors.New("MCP mutation approval has been revoked")
)

// MCPMutationProposalRow is the durable representation of one inert proposal.
type MCPMutationProposalRow struct {
	ProposalID      string
	ProposalSHA256  string
	IdentityVersion int

	RunID             string
	TraceID           string
	EffectiveCallID   string
	AgentID           string
	Owner             string
	PrincipalUserID   uint64
	PrincipalUsername string
	PrincipalIssuer   string

	ServerID          string
	RawToolName       string
	ToolAlias         string
	Transport         string
	TransportIdentity string
	SchemaSHA256      string

	CanonicalArguments []byte
	ArgumentsSHA256    string
	PolicyID           string
	PolicyVersion      string
	PolicySHA256       string
	PolicySnapshot     []byte

	SourceIDs        []uint64
	ExternalResource string
	Destructive      bool
	ApprovalRequired bool

	ExecutionBackend string
	State            string
	CreatedAt        time.Time
	LastUpdatedAt    time.Time
	ExpiresAt        time.Time
	ApprovedAt       sql.NullTime
	TerminalAt       sql.NullTime
	TerminalReason   sql.NullString
}

// MCPMutationApprovalRow is one durable operator approval.
type MCPMutationApprovalRow struct {
	ApprovalID      string
	ProposalID      string
	ProposalSHA256  string
	RunID           string
	AgentID         string
	ServerID        string
	ToolAlias       string
	ArgumentsSHA256 string
	PolicySHA256    string

	ApprovedByUserID uint64
	ApprovedBy       string
	ApprovedByIssuer string

	ApprovedAt time.Time
	ExpiresAt  time.Time
	State      string

	RevokedAt        sql.NullTime
	RevokedByUserID  sql.NullInt64
	RevokedBy        sql.NullString
	RevocationReason sql.NullString

	MaxExecutions     int
	ExecutionsStarted int
	LastUpdatedAt     time.Time
}

// MCPMutationProposalFilter is a bounded operator listing filter.
type MCPMutationProposalFilter struct {
	State         string
	ServerID      string
	RunID         string
	CreatedBefore time.Time
	BeforeID      string
	Limit         int
	// ViewerUserID and ViewerAdmin apply the caller's trusted visibility
	// predicate before LIMIT. Non-admin viewers see only source-scoped
	// proposals where every source has verified ownership by ViewerUserID.
	// External-resource proposals are administrator-only.
	ViewerUserID uint64
	ViewerAdmin  bool
}

// MCPMutationAuditEvent is one append-only, already-redacted audit record.
type MCPMutationAuditEvent struct {
	OccurredAt      time.Time
	EventType       string
	ActorType       string
	ActorID         string
	ActorIssuer     string
	ProposalID      string
	ProposalSHA256  string
	ApprovalID      string
	AttemptID       string
	RunID           string
	TraceID         string
	AgentID         string
	PrincipalUserID uint64
	ServerID        string
	ToolAlias       string
	ReasonCode      string
	RedactedDetail  []byte
}

func supportsMCPMutationStore(db Handler) bool {
	switch normalizeInformationSeedDBMS(db.DBMS()) {
	case DBPostgresStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

func mcpMutationPlaceholder(db Handler, position int) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func mcpProposalColumns() string {
	return `proposal_id, proposal_sha256, identity_version, run_id, trace_id,
		effective_call_id, agent_id, owner, principal_user_id,
		principal_username, principal_issuer, server_id, raw_tool_name,
		tool_alias, transport, transport_identity, schema_sha256,
		canonical_arguments, arguments_sha256, policy_id, policy_version,
		policy_sha256, policy_snapshot, source_ids, external_resource,
		destructive, approval_required, execution_backend, state,
		created_at, last_updated_at, expires_at, approved_at, terminal_at,
		terminal_reason`
}

func scanMCPMutationProposalRow(scanner interface {
	Scan(dest ...any) error
}, dbms string) (MCPMutationProposalRow, error) {
	var row MCPMutationProposalRow
	var externalResource sql.NullString
	if normalizeInformationSeedDBMS(dbms) == DBPostgresStr {
		var ids pq.Int64Array
		err := scanner.Scan(
			&row.ProposalID, &row.ProposalSHA256, &row.IdentityVersion,
			&row.RunID, &row.TraceID, &row.EffectiveCallID, &row.AgentID,
			&row.Owner, &row.PrincipalUserID, &row.PrincipalUsername,
			&row.PrincipalIssuer, &row.ServerID, &row.RawToolName,
			&row.ToolAlias, &row.Transport, &row.TransportIdentity,
			&row.SchemaSHA256, &row.CanonicalArguments, &row.ArgumentsSHA256,
			&row.PolicyID, &row.PolicyVersion, &row.PolicySHA256,
			&row.PolicySnapshot, &ids, &externalResource,
			&row.Destructive, &row.ApprovalRequired, &row.ExecutionBackend,
			&row.State, &row.CreatedAt, &row.LastUpdatedAt, &row.ExpiresAt,
			&row.ApprovedAt, &row.TerminalAt, &row.TerminalReason,
		)
		if err != nil {
			return MCPMutationProposalRow{}, err
		}
		for _, id := range ids {
			if id <= 0 {
				return MCPMutationProposalRow{}, fmt.Errorf("invalid negative source id %d", id)
			}
			row.SourceIDs = append(row.SourceIDs, uint64(id))
		}
		row.ExternalResource = externalResource.String
		return row, nil
	}

	var idsText string
	err := scanner.Scan(
		&row.ProposalID, &row.ProposalSHA256, &row.IdentityVersion,
		&row.RunID, &row.TraceID, &row.EffectiveCallID, &row.AgentID,
		&row.Owner, &row.PrincipalUserID, &row.PrincipalUsername,
		&row.PrincipalIssuer, &row.ServerID, &row.RawToolName,
		&row.ToolAlias, &row.Transport, &row.TransportIdentity,
		&row.SchemaSHA256, &row.CanonicalArguments, &row.ArgumentsSHA256,
		&row.PolicyID, &row.PolicyVersion, &row.PolicySHA256,
		&row.PolicySnapshot, &idsText, &externalResource,
		&row.Destructive, &row.ApprovalRequired, &row.ExecutionBackend,
		&row.State, &row.CreatedAt, &row.LastUpdatedAt, &row.ExpiresAt,
		&row.ApprovedAt, &row.TerminalAt, &row.TerminalReason,
	)
	if err != nil {
		return MCPMutationProposalRow{}, err
	}
	if err := json.Unmarshal([]byte(idsText), &row.SourceIDs); err != nil {
		return MCPMutationProposalRow{}, fmt.Errorf("decode source_ids: %w", err)
	}
	row.ExternalResource = externalResource.String
	return row, nil
}

func mcpProposalInsertSQL(db Handler) string {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	return `INSERT INTO MCPMutationProposals
		(proposal_id, proposal_sha256, identity_version, run_id, trace_id,
		 effective_call_id, agent_id, owner, principal_user_id,
		 principal_username, principal_issuer, server_id, raw_tool_name,
		 tool_alias, transport, transport_identity, schema_sha256,
		 canonical_arguments, arguments_sha256, policy_id, policy_version,
		 policy_sha256, policy_snapshot, source_ids, external_resource,
		 destructive, approval_required, execution_backend, state,
		 created_at, last_updated_at, expires_at, approved_at, terminal_at,
		 terminal_reason)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `,
		 ` + p(6) + `, ` + p(7) + `, ` + p(8) + `, ` + p(9) + `, ` + p(10) + `,
		 ` + p(11) + `, ` + p(12) + `, ` + p(13) + `, ` + p(14) + `, ` + p(15) + `,
		 ` + p(16) + `, ` + p(17) + `, ` + p(18) + `, ` + p(19) + `, ` + p(20) + `,
		 ` + p(21) + `, ` + p(22) + `, ` + p(23) + `, ` + p(24) + `, ` + p(25) + `,
		 ` + p(26) + `, ` + p(27) + `, ` + p(28) + `, ` + p(29) + `, ` + p(30) + `,
		 ` + p(31) + `, ` + p(32) + `, ` + p(33) + `, ` + p(34) + `, ` + p(35) + `)`
}

func mcpProposalInsertArgs(db Handler, row MCPMutationProposalRow) []any {
	if row.SourceIDs == nil {
		row.SourceIDs = []uint64{}
	}
	var sourceIDs any
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		ids := make([]int64, len(row.SourceIDs))
		for i, id := range row.SourceIDs {
			ids[i] = int64(id)
		}
		sourceIDs = pq.Array(ids)
	} else {
		encoded, _ := json.Marshal(row.SourceIDs)
		sourceIDs = string(encoded)
	}
	var externalResource any
	if strings.TrimSpace(row.ExternalResource) != "" {
		externalResource = strings.TrimSpace(row.ExternalResource)
	}
	var approvedAt any
	if row.ApprovedAt.Valid {
		approvedAt = row.ApprovedAt.Time
	}
	var terminalAt any
	if row.TerminalAt.Valid {
		terminalAt = row.TerminalAt.Time
	}
	var terminalReason any
	if row.TerminalReason.Valid {
		terminalReason = row.TerminalReason.String
	}
	return []any{
		row.ProposalID, row.ProposalSHA256, row.IdentityVersion, row.RunID,
		row.TraceID, row.EffectiveCallID, row.AgentID, row.Owner,
		row.PrincipalUserID, row.PrincipalUsername, row.PrincipalIssuer,
		row.ServerID, row.RawToolName, row.ToolAlias, row.Transport,
		row.TransportIdentity, row.SchemaSHA256, row.CanonicalArguments,
		row.ArgumentsSHA256, row.PolicyID, row.PolicyVersion,
		row.PolicySHA256, string(row.PolicySnapshot), sourceIDs,
		externalResource, row.Destructive, row.ApprovalRequired,
		row.ExecutionBackend, row.State, row.CreatedAt, row.LastUpdatedAt,
		row.ExpiresAt, approvedAt, terminalAt, terminalReason,
	}
}

type mcpMutationQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

func getMCPMutationProposalWhere(ctx context.Context, q mcpMutationQuerier, dbms, where string, args ...any) (MCPMutationProposalRow, bool, error) {
	query := `SELECT ` + mcpProposalColumns() + ` FROM MCPMutationProposals WHERE ` + where + ` LIMIT 1`
	row, err := scanMCPMutationProposalRow(q.QueryRowContext(ctx, query, args...), dbms)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPMutationProposalRow{}, false, nil
	}
	if err != nil {
		return MCPMutationProposalRow{}, false, mcpMutationStoreError(err)
	}
	return row, true, nil
}

// GetMCPMutationProposal returns one durable proposal by ID.
func GetMCPMutationProposal(ctx context.Context, db *Handler, proposalID string) (MCPMutationProposalRow, bool, error) {
	if db == nil || *db == nil {
		return MCPMutationProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationProposalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	return getMCPMutationProposalWhere(ctx, *db, (*db).DBMS(),
		`proposal_id = `+mcpMutationPlaceholder(*db, 1), proposalID)
}

// GetMCPMutationProposalByFingerprint returns one durable proposal by fingerprint.
func GetMCPMutationProposalByFingerprint(ctx context.Context, db *Handler, fingerprint string) (MCPMutationProposalRow, bool, error) {
	if db == nil || *db == nil {
		return MCPMutationProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationProposalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	return getMCPMutationProposalWhere(ctx, *db, (*db).DBMS(),
		`proposal_sha256 = `+mcpMutationPlaceholder(*db, 1), fingerprint)
}

// ListMCPMutationProposals returns a bounded operator listing.
func ListMCPMutationProposals(ctx context.Context, db *Handler, filter MCPMutationProposalFilter) ([]MCPMutationProposalRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return nil, ErrMCPMutationStoreUnsupported
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if !filter.ViewerAdmin && filter.ViewerUserID == 0 {
		return []MCPMutationProposalRow{}, nil
	}
	conditions := make([]string, 0, 7)
	args := make([]any, 0, 9)
	placeholder := func(value any) string {
		args = append(args, value)
		return mcpMutationPlaceholder(*db, len(args))
	}
	if strings.TrimSpace(filter.State) != "" {
		conditions = append(conditions, `state = `+placeholder(strings.TrimSpace(filter.State)))
	}
	if strings.TrimSpace(filter.ServerID) != "" {
		conditions = append(conditions, `server_id = `+placeholder(strings.TrimSpace(filter.ServerID)))
	}
	if strings.TrimSpace(filter.RunID) != "" {
		conditions = append(conditions, `run_id = `+placeholder(strings.TrimSpace(filter.RunID)))
	}
	if !filter.ViewerAdmin {
		switch normalizeInformationSeedDBMS((*db).DBMS()) {
		case DBPostgresStr:
			conditions = append(conditions, `cardinality(p.source_ids) > 0 AND NOT EXISTS (
				SELECT 1 FROM unnest(p.source_ids) AS sid(source_id)
				 WHERE NOT EXISTS (
				   SELECT 1 FROM SourceOwnership so
				    WHERE so.source_id = sid.source_id AND so.user_id = `+placeholder(filter.ViewerUserID)+`))`)
		case DBSQLiteStr:
			conditions = append(conditions, `json_array_length(p.source_ids) > 0 AND NOT EXISTS (
				SELECT 1 FROM json_each(p.source_ids) AS sid
				 WHERE NOT EXISTS (
				   SELECT 1 FROM SourceOwnership so
				    WHERE so.source_id = CAST(sid.value AS INTEGER) AND so.user_id = `+placeholder(filter.ViewerUserID)+`))`)
		default:
			return nil, ErrMCPMutationStoreUnsupported
		}
	}
	if !filter.CreatedBefore.IsZero() {
		before := filter.CreatedBefore.UTC()
		if strings.TrimSpace(filter.BeforeID) != "" {
			conditions = append(conditions, `(created_at < `+placeholder(before)+` OR (created_at = `+placeholder(before)+` AND proposal_id < `+placeholder(filter.BeforeID)+`))`)
		} else {
			conditions = append(conditions, `created_at < `+placeholder(before))
		}
	}
	query := `SELECT ` + mcpProposalColumns() + ` FROM MCPMutationProposals p`
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC, proposal_id DESC LIMIT ` + placeholder(limit)
	rows, err := (*db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mcpMutationStoreError(err)
	}
	defer rows.Close()
	out := make([]MCPMutationProposalRow, 0, limit)
	for rows.Next() {
		row, err := scanMCPMutationProposalRow(rows, (*db).DBMS())
		if err != nil {
			return nil, mcpMutationStoreError(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mcpMutationStoreError(err)
	}
	return out, nil
}

func mcpMutationStoreError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such table") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "undefined table") ||
		strings.Contains(message, "no such column") {
		return fmt.Errorf("%w: %v", ErrMCPMutationSchemaUnavailable, err)
	}
	return err
}

func isMCPMutationUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique failed") ||
		strings.Contains(message, "constraint failed: unique")
}

// CheckMCPMutationCapability verifies the additive schema-1.17 objects and the
// capability marker. It fails closed: a database that advertises 1.17 but
// lacks this capability is not silently treated as ready.
func CheckMCPMutationCapability(ctx context.Context, db *Handler) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return ErrMCPMutationStoreUnsupported
	}
	required := map[string][]string{
		"MCPMutationProposals": {
			"proposal_id", "proposal_sha256", "identity_version", "run_id",
			"trace_id", "effective_call_id", "agent_id", "owner",
			"principal_user_id", "principal_username", "principal_issuer",
			"server_id", "raw_tool_name", "tool_alias", "transport",
			"transport_identity", "schema_sha256", "canonical_arguments",
			"arguments_sha256", "policy_id", "policy_version", "policy_sha256",
			"policy_snapshot", "source_ids", "external_resource", "destructive",
			"approval_required", "execution_backend", "state", "created_at",
			"expires_at", "approved_at", "terminal_at", "terminal_reason",
		},
		"MCPMutationApprovals": {
			"approval_id", "proposal_id", "proposal_sha256", "run_id",
			"agent_id", "server_id", "tool_alias", "arguments_sha256",
			"policy_sha256", "approved_by_user_id", "approved_by",
			"approved_by_issuer", "approved_at", "expires_at", "state",
			"revoked_at", "max_executions", "executions_started",
		},
		"MCPMutationDispatchAttempts": {
			"attempt_id", "proposal_id", "proposal_sha256", "approval_id",
			"submission_identity", "claimed_by", "claimed_at",
			"lease_expires_at", "state",
		},
		"MCPMutationAudit": {
			"event_id", "occurred_at", "event_type", "actor_type", "actor_id",
			"proposal_id", "proposal_sha256", "approval_id", "redacted_detail",
		},
		"MCPMutationRunQuota":       {"run_id", "active_count"},
		"MCPMutationPrincipalQuota": {"principal_user_id", "pending_count"},
		"SchemaCapabilities":        {"capability", "applied_at", "applied_by", "checksum"},
	}
	missing := make([]string, 0)
	for table, columns := range required {
		if err := probeColumns(ctx, *db, table, columns); err != nil {
			missing = append(missing, table+"("+strings.Join(columns, ",")+")")
		}
	}
	for _, index := range []string{
		"idx_mcp_proposals_pending",
		"idx_mcp_proposals_principal",
		"idx_mcp_proposals_run",
		"idx_mcp_approvals_pending",
		"uq_mcp_approvals_one_active",
		"uq_mcp_attempts_one_unresolved",
		"idx_mcp_audit_proposal",
	} {
		if !metadataObjectExists(ctx, *db, "index", index) {
			missing = append(missing, "index "+index)
		}
	}
	for _, table := range []string{
		"MCPMutationProposals", "MCPMutationApprovals",
		"MCPMutationDispatchAttempts", "MCPMutationAudit",
		"MCPMutationRunQuota", "MCPMutationPrincipalQuota", "SchemaCapabilities",
	} {
		if !tableConstraintExists(ctx, *db, table, "PRIMARY KEY") {
			missing = append(missing, "primary key on "+table)
		}
	}
	if !tableUniqueColumnsExist(ctx, *db, "MCPMutationProposals", []string{"run_id", "effective_call_id"}) {
		missing = append(missing, "unique (run_id,effective_call_id) on MCPMutationProposals")
	}
	if !tableUniqueColumnsExist(ctx, *db, "MCPMutationProposals", []string{"proposal_sha256"}) {
		missing = append(missing, "unique proposal_sha256 on MCPMutationProposals")
	}
	if !tableUniqueColumnsExist(ctx, *db, "MCPMutationDispatchAttempts", []string{"proposal_sha256", "submission_identity"}) {
		missing = append(missing, "unique (proposal_sha256,submission_identity) on MCPMutationDispatchAttempts")
	}
	for _, table := range []string{"MCPMutationApprovals", "MCPMutationDispatchAttempts"} {
		if !tableConstraintExists(ctx, *db, table, "FOREIGN KEY") {
			missing = append(missing, "foreign key on "+table)
		}
	}
	if normalizeDBMS((*db).DBMS()) == "postgres" {
		expectedTypes := map[string]map[string]string{
			"MCPMutationProposals": {
				"canonical_arguments": "bytea",
				"policy_snapshot":     "jsonb",
				"source_ids":          "array",
				"principal_user_id":   "bigint",
				"created_at":          "timestamp with time zone",
			},
			"MCPMutationApprovals": {
				"max_executions": "integer",
				"expires_at":     "timestamp with time zone",
			},
		}
		for table, columns := range expectedTypes {
			for column, expected := range columns {
				var dataType, udtName string
				err := (*db).QueryRowContext(ctx, `SELECT data_type, udt_name
					FROM information_schema.columns
					WHERE table_schema = current_schema()
					  AND lower(table_name) = lower($1)
					  AND lower(column_name) = lower($2)`, table, column).Scan(&dataType, &udtName)
				actual := strings.ToLower(dataType)
				if actual == "array" && strings.HasPrefix(strings.ToLower(udtName), "_") {
					actual = "array"
				}
				if err != nil || actual != expected {
					missing = append(missing, "type "+table+"."+column)
				}
			}
		}
	}
	var checksum string
	query := `SELECT checksum FROM SchemaCapabilities WHERE capability = ` + mcpMutationPlaceholder(*db, 1)
	if err := (*db).QueryRowContext(ctx, query, MCPMutationCapability).Scan(&checksum); err != nil {
		missing = append(missing, "SchemaCapabilities("+MCPMutationCapability+")")
	} else if checksum != MCPMutationCapabilityChecksum {
		missing = append(missing, "SchemaCapabilities checksum")
	}
	if len(missing) != 0 {
		return fmt.Errorf("%w: missing or invalid: %s", ErrMCPMutationSchemaUnavailable, strings.Join(missing, ", "))
	}
	return nil
}

func ensureAndLockMCPMutationQuotaTx(ctx context.Context, tx *sql.Tx, db Handler, runID string, principalUserID uint64) (int, int, error) {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO MCPMutationRunQuota (run_id, active_count) VALUES (`+p(1)+`, 0) ON CONFLICT (run_id) DO NOTHING`,
		runID); err != nil {
		return 0, 0, mcpMutationStoreError(err)
	}
	if principalUserID != 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO MCPMutationPrincipalQuota (principal_user_id, pending_count) VALUES (`+p(1)+`, 0) ON CONFLICT (principal_user_id) DO NOTHING`,
			principalUserID); err != nil {
			return 0, 0, mcpMutationStoreError(err)
		}
	}
	// The UPDATE is an explicit write lock. It serializes admission and state
	// transitions across processes and is also a no-op write in SQLite.
	if _, err := tx.ExecContext(ctx,
		`UPDATE MCPMutationRunQuota SET last_updated_at = CURRENT_TIMESTAMP WHERE run_id = `+p(1),
		runID); err != nil {
		return 0, 0, mcpMutationStoreError(err)
	}
	if principalUserID != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE MCPMutationPrincipalQuota SET last_updated_at = CURRENT_TIMESTAMP WHERE principal_user_id = `+p(1),
			principalUserID); err != nil {
			return 0, 0, mcpMutationStoreError(err)
		}
	}
	var active, pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT active_count FROM MCPMutationRunQuota WHERE run_id = `+p(1), runID).Scan(&active); err != nil {
		return 0, 0, mcpMutationStoreError(err)
	}
	if principalUserID != 0 {
		if err := tx.QueryRowContext(ctx,
			`SELECT pending_count FROM MCPMutationPrincipalQuota WHERE principal_user_id = `+p(1), principalUserID).Scan(&pending); err != nil {
			return 0, 0, mcpMutationStoreError(err)
		}
	}
	return active, pending, nil
}

func readMCPMutationQuotaTx(ctx context.Context, tx *sql.Tx, db Handler, runID string, principalUserID uint64) (int, int, error) {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	var active, pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT active_count FROM MCPMutationRunQuota WHERE run_id = `+p(1), runID).Scan(&active); err != nil {
		return 0, 0, mcpMutationStoreError(err)
	}
	if principalUserID != 0 {
		if err := tx.QueryRowContext(ctx,
			`SELECT pending_count FROM MCPMutationPrincipalQuota WHERE principal_user_id = `+p(1), principalUserID).Scan(&pending); err != nil {
			return 0, 0, mcpMutationStoreError(err)
		}
	}
	return active, pending, nil
}

// expireDueMCPMutationProposalsTx releases stale quota before a new admission.
// It deliberately expires only proposals for the exact run/principal pair whose
// quota rows are already locked. Expiring a proposal that shared only one of
// those rows would require locking the other row after the first, creating a
// lock-order inversion with concurrent admissions. Proposals outside this pair
// remain strict until their own admission or an explicit maintenance pass.
func expireDueMCPMutationProposalsTx(ctx context.Context, tx *sql.Tx, db Handler, runID string, principalUserID uint64, now time.Time) error {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	query := `SELECT ` + mcpProposalColumns() + ` FROM MCPMutationProposals
		WHERE expires_at <= ` + p(1) + `
		  AND state IN ('proposed', 'approval_required', 'approved', 'dispatching',
		                'uncertain', 'reconciliation_required')
		  AND run_id = ` + p(2) + `
		  AND principal_user_id = ` + p(3) + `
		ORDER BY proposal_id`
	rows, err := tx.QueryContext(ctx, query, now.UTC(), runID, principalUserID)
	if err != nil {
		return mcpMutationStoreError(err)
	}
	expired := make([]MCPMutationProposalRow, 0, 4)
	for rows.Next() {
		row, err := scanMCPMutationProposalRow(rows, db.DBMS())
		if err != nil {
			rows.Close()
			return mcpMutationStoreError(err)
		}
		expired = append(expired, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mcpMutationStoreError(err)
	}
	rows.Close()
	for _, row := range expired {
		if err := expireMCPMutationProposalTx(ctx, tx, db, row, now); err != nil {
			return err
		}
	}
	return nil
}

func adjustMCPMutationQuotaTx(ctx context.Context, tx *sql.Tx, db Handler, runID string, principalUserID uint64, activeDelta, pendingDelta int) error {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	if activeDelta != 0 {
		op := "+"
		value := activeDelta
		if value < 0 {
			op = "-"
			value = -value
		}
		query := `UPDATE MCPMutationRunQuota SET active_count = active_count ` + op + ` ` + p(1) + `, last_updated_at = CURRENT_TIMESTAMP WHERE run_id = ` + p(2)
		result, err := tx.ExecContext(ctx, query, value, runID)
		if err != nil {
			return mcpMutationStoreError(err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return fmt.Errorf("MCP mutation run quota row is missing")
		}
	}
	if pendingDelta != 0 && principalUserID != 0 {
		op := "+"
		value := pendingDelta
		if value < 0 {
			op = "-"
			value = -value
		}
		query := `UPDATE MCPMutationPrincipalQuota SET pending_count = pending_count ` + op + ` ` + p(1) + `, last_updated_at = CURRENT_TIMESTAMP WHERE principal_user_id = ` + p(2)
		result, err := tx.ExecContext(ctx, query, value, principalUserID)
		if err != nil {
			return mcpMutationStoreError(err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return fmt.Errorf("MCP mutation principal quota row is missing")
		}
	}
	return nil
}

func mcpStateConsumesActive(state string) bool {
	switch state {
	case MCPMutationStateProposed, MCPMutationStateApprovalRequired,
		MCPMutationStateApproved, MCPMutationStateDispatching,
		MCPMutationStateUncertain, MCPMutationStateReconciliationRequired:
		return true
	default:
		return false
	}
}

func mcpStateConsumesPending(state string) bool {
	switch state {
	case MCPMutationStateProposed, MCPMutationStateApprovalRequired,
		MCPMutationStateApproved, MCPMutationStateDispatching:
		return true
	default:
		return false
	}
}

func insertMCPMutationAuditTx(ctx context.Context, tx *sql.Tx, db Handler, event MCPMutationAuditEvent) error {
	return insertMCPMutationAuditExec(ctx, tx, db, event)
}

func expireMCPMutationProposalTx(ctx context.Context, tx *sql.Tx, db Handler, row MCPMutationProposalRow, now time.Time) error {
	if !mcpStateConsumesActive(row.State) {
		return nil
	}
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	result, err := tx.ExecContext(ctx, `UPDATE MCPMutationProposals
		SET state = 'expired', terminal_at = `+p(1)+`, terminal_reason = 'expired',
		    last_updated_at = `+p(2)+`
		WHERE proposal_id = `+p(3)+` AND state = `+p(4),
		now.UTC(), now.UTC(), row.ProposalID, row.State)
	if err != nil {
		return mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil
	}
	activeDelta, pendingDelta := -1, 0
	if mcpStateConsumesPending(row.State) {
		pendingDelta = -1
	}
	if err := adjustMCPMutationQuotaTx(ctx, tx, db, row.RunID, row.PrincipalUserID, activeDelta, pendingDelta); err != nil {
		return err
	}
	return insertMCPMutationAuditTx(ctx, tx, db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_proposal_expired",
		ActorType: "system", ActorID: "system", ProposalID: row.ProposalID,
		ProposalSHA256: row.ProposalSHA256, RunID: row.RunID,
		TraceID: row.TraceID, AgentID: row.AgentID,
		PrincipalUserID: row.PrincipalUserID, ServerID: row.ServerID,
		ToolAlias: row.ToolAlias, ReasonCode: "expired",
	})
}

// AdmitMCPMutationProposal atomically checks fingerprint replay, logical-call
// conflicts, quota counters and immutable creation.
func AdmitMCPMutationProposal(ctx context.Context, db *Handler, row MCPMutationProposalRow, maxPerRun, maxPendingPerPrincipal int) (MCPMutationProposalRow, bool, error) {
	return AdmitMCPMutationProposalAt(ctx, db, row, maxPerRun, maxPendingPerPrincipal, time.Now().UTC())
}

// AdmitMCPMutationProposalAt is the deterministic-clock admission boundary.
func AdmitMCPMutationProposalAt(ctx context.Context, db *Handler, row MCPMutationProposalRow, maxPerRun, maxPendingPerPrincipal int, now time.Time) (MCPMutationProposalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return MCPMutationProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationProposalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	if maxPerRun <= 0 || maxPendingPerPrincipal <= 0 {
		return MCPMutationProposalRow{}, false, ErrMCPMutationQuotaReached
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	if row.SourceIDs == nil {
		row.SourceIDs = []uint64{}
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.LastUpdatedAt.IsZero() {
		row.LastUpdatedAt = now
	}
	if !row.ExpiresAt.After(now) {
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalExpired
	}
	if _, _, err := ensureAndLockMCPMutationQuotaTx(txCtx, tx, *db, row.RunID, row.PrincipalUserID); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if err := expireDueMCPMutationProposalsTx(txCtx, tx, *db, row.RunID, row.PrincipalUserID, now); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	active, pending, err := readMCPMutationQuotaTx(txCtx, tx, *db, row.RunID, row.PrincipalUserID)
	if err != nil {
		return MCPMutationProposalRow{}, false, err
	}

	existing, found, err := getMCPMutationProposalWhere(txCtx, tx, (*db).DBMS(),
		`proposal_sha256 = `+mcpMutationPlaceholder(*db, 1), row.ProposalSHA256)
	if err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if found {
		if !existing.ExpiresAt.After(now) {
			if err := expireMCPMutationProposalTx(txCtx, tx, *db, existing, now); err != nil {
				return MCPMutationProposalRow{}, false, err
			}
			if err := tx.Commit(); err != nil {
				return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
			}
			return MCPMutationProposalRow{}, false, ErrMCPMutationProposalExpired
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return existing, false, nil
	}
	existing, found, err = getMCPMutationProposalWhere(txCtx, tx, (*db).DBMS(),
		`run_id = `+mcpMutationPlaceholder(*db, 1)+` AND effective_call_id = `+mcpMutationPlaceholder(*db, 2),
		row.RunID, row.EffectiveCallID)
	if err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if found {
		if existing.ProposalSHA256 != row.ProposalSHA256 {
			return MCPMutationProposalRow{}, false, ErrMCPMutationProposalConflict
		}
		if !existing.ExpiresAt.After(now) {
			if err := expireMCPMutationProposalTx(txCtx, tx, *db, existing, now); err != nil {
				return MCPMutationProposalRow{}, false, err
			}
			if err := tx.Commit(); err != nil {
				return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
			}
			return MCPMutationProposalRow{}, false, ErrMCPMutationProposalExpired
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return existing, false, nil
	}
	if active >= maxPerRun || pending >= maxPendingPerPrincipal {
		return MCPMutationProposalRow{}, false, ErrMCPMutationQuotaReached
	}
	if _, err := tx.ExecContext(txCtx, mcpProposalInsertSQL(*db), mcpProposalInsertArgs(*db, row)...); err != nil {
		if isMCPMutationUniqueViolation(err) {
			return MCPMutationProposalRow{}, false, ErrMCPMutationProposalConflict
		}
		return MCPMutationProposalRow{}, false, mcpMutationStoreError(err)
	}
	if err := adjustMCPMutationQuotaTx(txCtx, tx, *db, row.RunID, row.PrincipalUserID, 1, 1); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if err := insertMCPMutationAuditTx(txCtx, tx, *db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_proposal_created",
		ActorType: "agent", ActorID: row.AgentID, ProposalID: row.ProposalID,
		ProposalSHA256: row.ProposalSHA256, RunID: row.RunID,
		TraceID: row.TraceID, AgentID: row.AgentID,
		PrincipalUserID: row.PrincipalUserID, ServerID: row.ServerID,
		ToolAlias: row.ToolAlias,
	}); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	row.LastUpdatedAt = row.CreatedAt
	return row, true, nil
}

func mcpApprovalColumns() string {
	return `approval_id, proposal_id, proposal_sha256, run_id, agent_id,
		server_id, tool_alias, arguments_sha256, policy_sha256,
		approved_by_user_id, approved_by, approved_by_issuer, approved_at,
		expires_at, state, revoked_at, revoked_by_user_id, revoked_by,
		revocation_reason, max_executions, executions_started, last_updated_at`
}

func scanMCPMutationApprovalRow(scanner interface {
	Scan(dest ...any) error
}) (MCPMutationApprovalRow, error) {
	var row MCPMutationApprovalRow
	err := scanner.Scan(
		&row.ApprovalID, &row.ProposalID, &row.ProposalSHA256, &row.RunID,
		&row.AgentID, &row.ServerID, &row.ToolAlias, &row.ArgumentsSHA256,
		&row.PolicySHA256, &row.ApprovedByUserID, &row.ApprovedBy,
		&row.ApprovedByIssuer, &row.ApprovedAt, &row.ExpiresAt, &row.State,
		&row.RevokedAt, &row.RevokedByUserID, &row.RevokedBy,
		&row.RevocationReason, &row.MaxExecutions, &row.ExecutionsStarted,
		&row.LastUpdatedAt,
	)
	return row, err
}

func getMCPMutationApprovalWhere(ctx context.Context, q mcpMutationQuerier, dbms, where string, args ...any) (MCPMutationApprovalRow, bool, error) {
	query := `SELECT ` + mcpApprovalColumns() + ` FROM MCPMutationApprovals WHERE ` + where + ` LIMIT 1`
	row, err := scanMCPMutationApprovalRow(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return MCPMutationApprovalRow{}, false, nil
	}
	if err != nil {
		return MCPMutationApprovalRow{}, false, mcpMutationStoreError(err)
	}
	return row, true, nil
}

// GetMCPMutationApproval returns one durable approval by ID.
func GetMCPMutationApproval(ctx context.Context, db *Handler, approvalID string) (MCPMutationApprovalRow, bool, error) {
	if db == nil || *db == nil {
		return MCPMutationApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	return getMCPMutationApprovalWhere(ctx, *db, (*db).DBMS(),
		`approval_id = `+mcpMutationPlaceholder(*db, 1), approvalID)
}

// GetActiveMCPMutationApprovalByProposal returns an approval that is usable at
// the supplied trusted time. The SQL predicate is deliberately named "active
// at now": a row whose state is still active but whose expiry has passed is not
// returned. State reconciliation happens in the approval/revocation
// transactions, not on this read path.
func GetActiveMCPMutationApprovalByProposal(ctx context.Context, db *Handler, proposalSHA256 string, now time.Time) (MCPMutationApprovalRow, bool, error) {
	if db == nil || *db == nil {
		return MCPMutationApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return getMCPMutationApprovalWhere(ctx, *db, (*db).DBMS(),
		`proposal_sha256 = `+mcpMutationPlaceholder(*db, 1)+`
		 AND state = 'active'
		 AND expires_at > `+mcpMutationPlaceholder(*db, 2)+`
		 AND executions_started < max_executions
		 AND EXISTS (
		   SELECT 1 FROM MCPMutationProposals p
		    WHERE p.proposal_sha256 = MCPMutationApprovals.proposal_sha256
		      AND p.state = 'approved'
		      AND p.expires_at > `+mcpMutationPlaceholder(*db, 3)+`)`,
		proposalSHA256, now.UTC(), now.UTC())
}

func mcpApprovalIsUsable(approval MCPMutationApprovalRow, proposal MCPMutationProposalRow, now time.Time) bool {
	return approval.State == "active" && !approval.RevokedAt.Valid &&
		approval.ExpiresAt.After(now) && approval.ExecutionsStarted < approval.MaxExecutions &&
		proposal.State == MCPMutationStateApproved && proposal.ExpiresAt.After(now)
}

func getMCPMutationActiveApprovalTx(ctx context.Context, tx *sql.Tx, db Handler, proposalSHA256 string) (MCPMutationApprovalRow, bool, error) {
	return getMCPMutationApprovalWhere(ctx, tx, db.DBMS(),
		`proposal_sha256 = `+mcpMutationPlaceholder(db, 1)+` AND state = 'active'`,
		proposalSHA256)
}

func expireMCPMutationApprovalTx(ctx context.Context, tx *sql.Tx, db Handler, approval MCPMutationApprovalRow, now time.Time, reason string) error {
	state := "expired"
	eventType := "mcp_mutation_approval_expired"
	if reason == "exhausted" {
		state = "exhausted"
		eventType = "mcp_mutation_approval_exhausted"
	}
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	result, err := tx.ExecContext(ctx, `UPDATE MCPMutationApprovals
		SET state = `+p(1)+`, last_updated_at = `+p(2)+`
		WHERE approval_id = `+p(3)+` AND state = 'active'`,
		state, now.UTC(), approval.ApprovalID)
	if err != nil {
		return mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrMCPMutationApprovalConflict
	}
	return insertMCPMutationAuditTx(ctx, tx, db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: eventType, ActorType: "system",
		ActorID: "system", ProposalID: approval.ProposalID,
		ProposalSHA256: approval.ProposalSHA256, ApprovalID: approval.ApprovalID,
		RunID: approval.RunID, AgentID: approval.AgentID,
		PrincipalUserID: 0, ServerID: approval.ServerID, ToolAlias: approval.ToolAlias,
		ReasonCode: reason,
	})
}

func reopenMCPMutationProposalTx(ctx context.Context, tx *sql.Tx, db Handler, proposal MCPMutationProposalRow, now time.Time, reason string) error {
	if proposal.State != MCPMutationStateApproved {
		return nil
	}
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	result, err := tx.ExecContext(ctx, `UPDATE MCPMutationProposals
		SET state = 'approval_required', approved_at = NULL, last_updated_at = `+p(1)+`
		WHERE proposal_id = `+p(2)+` AND state = 'approved'`,
		now.UTC(), proposal.ProposalID)
	if err != nil {
		return mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrMCPMutationProposalConflict
	}
	return insertMCPMutationAuditTx(ctx, tx, db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_proposal_reopened",
		ActorType: "system", ActorID: "system", ProposalID: proposal.ProposalID,
		ProposalSHA256: proposal.ProposalSHA256, RunID: proposal.RunID,
		TraceID: proposal.TraceID, AgentID: proposal.AgentID,
		PrincipalUserID: proposal.PrincipalUserID, ServerID: proposal.ServerID,
		ToolAlias: proposal.ToolAlias, ReasonCode: reason,
	})
}

func insertMCPMutationApprovalTx(ctx context.Context, tx *sql.Tx, db Handler, row MCPMutationApprovalRow) error {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	_, err := tx.ExecContext(ctx, `INSERT INTO MCPMutationApprovals
		(approval_id, proposal_id, proposal_sha256, run_id, agent_id,
		 server_id, tool_alias, arguments_sha256, policy_sha256,
		 approved_by_user_id, approved_by, approved_by_issuer, approved_at,
		 expires_at, state, max_executions, executions_started, last_updated_at)
		VALUES (`+p(1)+`, `+p(2)+`, `+p(3)+`, `+p(4)+`, `+p(5)+`,
		 `+p(6)+`, `+p(7)+`, `+p(8)+`, `+p(9)+`, `+p(10)+`,
		 `+p(11)+`, `+p(12)+`, `+p(13)+`, `+p(14)+`, 'active',
		 `+p(15)+`, 0, `+p(16)+`)`,
		row.ApprovalID, row.ProposalID, row.ProposalSHA256, row.RunID,
		row.AgentID, row.ServerID, row.ToolAlias, row.ArgumentsSHA256,
		row.PolicySHA256, row.ApprovedByUserID, row.ApprovedBy,
		row.ApprovedByIssuer, row.ApprovedAt.UTC(), row.ExpiresAt.UTC(),
		row.MaxExecutions, row.LastUpdatedAt.UTC())
	if err != nil {
		if isMCPMutationUniqueViolation(err) {
			return ErrMCPMutationApprovalConflict
		}
		return mcpMutationStoreError(err)
	}
	return nil
}

func lockMCPMutationProposalTx(ctx context.Context, tx *sql.Tx, db Handler, proposalID string) (MCPMutationProposalRow, bool, error) {
	query := `SELECT ` + mcpProposalColumns() + ` FROM MCPMutationProposals WHERE proposal_id = ` + mcpMutationPlaceholder(db, 1)
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	row, err := scanMCPMutationProposalRow(tx.QueryRowContext(ctx, query, proposalID), db.DBMS())
	if errors.Is(err, sql.ErrNoRows) {
		return MCPMutationProposalRow{}, false, nil
	}
	if err != nil {
		return MCPMutationProposalRow{}, false, mcpMutationStoreError(err)
	}
	return row, true, nil
}

// IssueMCPMutationApproval persists one operator approval and moves the
// proposal to approved. It never dispatches an external operation.
func IssueMCPMutationApproval(ctx context.Context, db *Handler, approval MCPMutationApprovalRow, expectedProposalSHA256 string, now time.Time) (MCPMutationApprovalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return MCPMutationApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	proposal, found, err := getMCPMutationProposalWhere(txCtx, tx, (*db).DBMS(),
		`proposal_id = `+mcpMutationPlaceholder(*db, 1), approval.ProposalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalNotFound
	}
	if _, _, err := ensureAndLockMCPMutationQuotaTx(txCtx, tx, *db, proposal.RunID, proposal.PrincipalUserID); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	proposal, found, err = lockMCPMutationProposalTx(txCtx, tx, *db, approval.ProposalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalNotFound
	}
	if expectedProposalSHA256 != "" && proposal.ProposalSHA256 != expectedProposalSHA256 {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalConflict
	}
	if !proposal.ExpiresAt.After(now) {
		if err := expireMCPMutationProposalTx(txCtx, tx, *db, proposal, now); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalExpired
	}
	switch proposal.State {
	case MCPMutationStateApprovalRequired, MCPMutationStateApproved:
	case MCPMutationStateDenied, MCPMutationStateExpired:
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalExpired
	default:
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalConflict
	}
	existing, found, err := getMCPMutationActiveApprovalTx(txCtx, tx, *db, proposal.ProposalSHA256)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if found && mcpApprovalIsUsable(existing, proposal, now) {
		if existing.ApprovedByUserID != approval.ApprovedByUserID {
			return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalConflict
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return existing, false, nil
	}
	if found {
		reason := "expired"
		if existing.ExecutionsStarted >= existing.MaxExecutions {
			reason = "exhausted"
		} else if proposal.State != MCPMutationStateApproved || !existing.ExpiresAt.After(now) {
			reason = "expired"
		}
		if err := expireMCPMutationApprovalTx(txCtx, tx, *db, existing, now, reason); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		if proposal.State == MCPMutationStateApproved {
			if err := reopenMCPMutationProposalTx(txCtx, tx, *db, proposal, now, "approval_"+reason); err != nil {
				return MCPMutationApprovalRow{}, false, err
			}
			proposal.State = MCPMutationStateApprovalRequired
			proposal.ApprovedAt = sql.NullTime{}
		}
	} else if proposal.State == MCPMutationStateApproved {
		if err := reopenMCPMutationProposalTx(txCtx, tx, *db, proposal, now, "approval_missing"); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		proposal.State = MCPMutationStateApprovalRequired
		proposal.ApprovedAt = sql.NullTime{}
	}
	if approval.MaxExecutions <= 0 {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalConflict
	}
	if !approval.ApprovedAt.After(now.Add(-time.Minute)) || !approval.ExpiresAt.After(now) ||
		!approval.ExpiresAt.After(approval.ApprovedAt) || approval.ExpiresAt.After(proposal.ExpiresAt) {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalExpired
	}
	approval.ProposalSHA256 = proposal.ProposalSHA256
	approval.RunID = proposal.RunID
	approval.AgentID = proposal.AgentID
	approval.ServerID = proposal.ServerID
	approval.ToolAlias = proposal.ToolAlias
	approval.ArgumentsSHA256 = proposal.ArgumentsSHA256
	approval.PolicySHA256 = proposal.PolicySHA256
	approval.State = "active"
	approval.ExecutionsStarted = 0
	approval.LastUpdatedAt = now
	if err := insertMCPMutationApprovalTx(txCtx, tx, *db, approval); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	p := func(n int) string { return mcpMutationPlaceholder(*db, n) }
	result, err := tx.ExecContext(txCtx, `UPDATE MCPMutationProposals
		SET state = 'approved', approved_at = `+p(1)+`, last_updated_at = `+p(2)+`
		WHERE proposal_id = `+p(3)+` AND state IN ('approval_required', 'approved')`,
		approval.ApprovedAt.UTC(), now.UTC(), proposal.ProposalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalConflict
	}
	if err := insertMCPMutationAuditTx(txCtx, tx, *db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_approval_issued",
		ActorType: "operator", ActorID: approval.ApprovedBy,
		ActorIssuer: approval.ApprovedByIssuer, ProposalID: proposal.ProposalID,
		ProposalSHA256: proposal.ProposalSHA256, ApprovalID: approval.ApprovalID,
		RunID: proposal.RunID, TraceID: proposal.TraceID, AgentID: proposal.AgentID,
		PrincipalUserID: proposal.PrincipalUserID, ServerID: proposal.ServerID,
		ToolAlias: proposal.ToolAlias, ReasonCode: "operator_approved",
	}); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	return approval, true, nil
}

// RevokeMCPMutationApproval revokes one usable active approval without
// deleting history. If it was the proposal's only usable approval, the proposal
// returns to approval_required; an expired or exhausted approval is classified
// rather than being reported as revoked.
func RevokeMCPMutationApproval(ctx context.Context, db *Handler, approvalID string, principalUserID uint64, revokedBy, reason string, now time.Time) (MCPMutationApprovalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return MCPMutationApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	if principalUserID == 0 || strings.TrimSpace(revokedBy) == "" {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalConflict
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	approval, found, err := getMCPMutationApprovalWhere(txCtx, tx, (*db).DBMS(),
		`approval_id = `+mcpMutationPlaceholder(*db, 1), approvalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalNotFound
	}
	proposal, found, err := getMCPMutationProposalWhere(txCtx, tx, (*db).DBMS(),
		`proposal_id = `+mcpMutationPlaceholder(*db, 1), approval.ProposalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalNotFound
	}
	if _, _, err := ensureAndLockMCPMutationQuotaTx(txCtx, tx, *db, proposal.RunID, proposal.PrincipalUserID); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	proposal, found, err = lockMCPMutationProposalTx(txCtx, tx, *db, proposal.ProposalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationProposalNotFound
	}
	approval, found, err = getMCPMutationApprovalWhere(txCtx, tx, (*db).DBMS(),
		`approval_id = `+mcpMutationPlaceholder(*db, 1), approvalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if !found {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalNotFound
	}
	p := func(n int) string { return mcpMutationPlaceholder(*db, n) }

	reopenIfNeeded := func(reasonCode string) error {
		active, activeFound, err := getMCPMutationActiveApprovalTx(txCtx, tx, *db, proposal.ProposalSHA256)
		if err != nil {
			return err
		}
		if activeFound && mcpApprovalIsUsable(active, proposal, now) {
			return nil
		}
		if activeFound && !mcpApprovalIsUsable(active, proposal, now) {
			activeReason := "expired"
			if active.ExecutionsStarted >= active.MaxExecutions {
				activeReason = "exhausted"
			}
			if err := expireMCPMutationApprovalTx(txCtx, tx, *db, active, now, activeReason); err != nil {
				return err
			}
		}
		if proposal.State == MCPMutationStateApproved {
			return reopenMCPMutationProposalTx(txCtx, tx, *db, proposal, now, reasonCode)
		}
		return nil
	}

	if approval.State == "revoked" {
		if err := reopenIfNeeded("approval_revoked"); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return approval, false, nil
	}
	if approval.State != "active" {
		if err := reopenIfNeeded("approval_" + approval.State); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return approval, false, nil
	}
	if !mcpApprovalIsUsable(approval, proposal, now) {
		reasonCode := "expired"
		if approval.ExecutionsStarted >= approval.MaxExecutions {
			reasonCode = "exhausted"
		}
		if err := expireMCPMutationApprovalTx(txCtx, tx, *db, approval, now, reasonCode); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		approval.State = reasonCode
		if reasonCode == "exhausted" {
			approval.State = "exhausted"
		} else {
			approval.State = "expired"
		}
		if err := reopenIfNeeded("approval_" + reasonCode); err != nil {
			return MCPMutationApprovalRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return approval, false, nil
	}
	result, err := tx.ExecContext(txCtx, `UPDATE MCPMutationApprovals
		SET state = 'revoked', revoked_at = `+p(1)+`, revoked_by_user_id = `+p(2)+`,
		    revoked_by = `+p(3)+`, revocation_reason = `+p(4)+`,
		    last_updated_at = `+p(5)+`
		WHERE approval_id = `+p(6)+` AND state = 'active'`,
		now.UTC(), principalUserID, strings.TrimSpace(revokedBy),
		strings.TrimSpace(reason), now.UTC(), approvalID)
	if err != nil {
		return MCPMutationApprovalRow{}, false, mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return MCPMutationApprovalRow{}, false, ErrMCPMutationApprovalConflict
	}
	approval.State = "revoked"
	approval.RevokedAt = sql.NullTime{Time: now.UTC(), Valid: true}
	approval.RevokedByUserID = sql.NullInt64{Int64: int64(principalUserID), Valid: true}
	approval.RevokedBy = sql.NullString{String: strings.TrimSpace(revokedBy), Valid: true}
	approval.RevocationReason = sql.NullString{String: strings.TrimSpace(reason), Valid: true}
	approval.LastUpdatedAt = now.UTC()
	if err := reopenIfNeeded("approval_revoked"); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if err := insertMCPMutationAuditTx(txCtx, tx, *db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_approval_revoked",
		ActorType: "operator", ActorID: strings.TrimSpace(revokedBy),
		ProposalID: approval.ProposalID, ProposalSHA256: approval.ProposalSHA256,
		ApprovalID: approval.ApprovalID, RunID: approval.RunID, AgentID: approval.AgentID,
		PrincipalUserID: principalUserID, ServerID: approval.ServerID,
		ToolAlias: approval.ToolAlias, ReasonCode: "operator_revoked",
	}); err != nil {
		return MCPMutationApprovalRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return MCPMutationApprovalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	return approval, true, nil
}

// DenyMCPMutationProposal terminally denies one proposal.
func DenyMCPMutationProposal(ctx context.Context, db *Handler, proposalID string, actorUserID uint64, actorID, reason string, now time.Time) (MCPMutationProposalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return MCPMutationProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return MCPMutationProposalRow{}, false, ErrMCPMutationStoreUnsupported
	}
	if actorUserID == 0 || strings.TrimSpace(actorID) == "" {
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalConflict
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()
	proposal, found, err := getMCPMutationProposalWhere(txCtx, tx, (*db).DBMS(),
		`proposal_id = `+mcpMutationPlaceholder(*db, 1), proposalID)
	if err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if !found {
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalNotFound
	}
	if _, _, err := ensureAndLockMCPMutationQuotaTx(txCtx, tx, *db, proposal.RunID, proposal.PrincipalUserID); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	proposal, found, err = lockMCPMutationProposalTx(txCtx, tx, *db, proposalID)
	if err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if !found {
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalNotFound
	}
	if proposal.State == MCPMutationStateDenied {
		if err := tx.Commit(); err != nil {
			return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return proposal, false, nil
	}
	if !proposal.ExpiresAt.After(now) || proposal.State == MCPMutationStateExpired {
		if err := expireMCPMutationProposalTx(txCtx, tx, *db, proposal, now); err != nil {
			return MCPMutationProposalRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
		}
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalExpired
	}
	switch proposal.State {
	case MCPMutationStateProposed, MCPMutationStateApprovalRequired, MCPMutationStateApproved:
	default:
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalConflict
	}
	// Classify a stale active approval before denial. A row that is already
	// expired must become expired, not be mislabeled as operator-revoked.
	var deniedApproval MCPMutationApprovalRow
	deniedApprovalFound := false
	if active, activeFound, activeErr := getMCPMutationActiveApprovalTx(txCtx, tx, *db, proposal.ProposalSHA256); activeErr != nil {
		return MCPMutationProposalRow{}, false, activeErr
	} else if activeFound && !mcpApprovalIsUsable(active, proposal, now) {
		reasonCode := "expired"
		if active.ExecutionsStarted >= active.MaxExecutions {
			reasonCode = "exhausted"
		}
		if err := expireMCPMutationApprovalTx(txCtx, tx, *db, active, now, reasonCode); err != nil {
			return MCPMutationProposalRow{}, false, err
		}
	} else if activeFound {
		deniedApproval = active
		deniedApprovalFound = true
	}
	p := func(n int) string { return mcpMutationPlaceholder(*db, n) }
	result, err := tx.ExecContext(txCtx, `UPDATE MCPMutationProposals
		SET state = 'denied', terminal_at = `+p(1)+`, terminal_reason = `+p(2)+`,
		    last_updated_at = `+p(3)+`
		WHERE proposal_id = `+p(4)+` AND state = `+p(5),
		now.UTC(), strings.TrimSpace(reason), now.UTC(), proposalID, proposal.State)
	if err != nil {
		return MCPMutationProposalRow{}, false, mcpMutationStoreError(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return MCPMutationProposalRow{}, false, ErrMCPMutationProposalConflict
	}
	activeDelta, pendingDelta := -1, -1
	if !mcpStateConsumesPending(proposal.State) {
		pendingDelta = 0
	}
	if err := adjustMCPMutationQuotaTx(txCtx, tx, *db, proposal.RunID, proposal.PrincipalUserID, activeDelta, pendingDelta); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	// Denial invalidates any active approval in the same transaction. A denied
	// proposal must never leave a usable approval behind.
	if _, err := tx.ExecContext(txCtx, `UPDATE MCPMutationApprovals
		SET state = 'revoked', revoked_at = `+p(1)+`, revoked_by_user_id = `+p(2)+`,
		    revoked_by = `+p(3)+`, revocation_reason = 'proposal_denied',
		    last_updated_at = `+p(4)+`
		WHERE proposal_sha256 = `+p(5)+` AND state = 'active'`,
		now.UTC(), actorUserID, strings.TrimSpace(actorID), now.UTC(),
		proposal.ProposalSHA256); err != nil {
		return MCPMutationProposalRow{}, false, mcpMutationStoreError(err)
	}
	if deniedApprovalFound {
		if err := insertMCPMutationAuditTx(txCtx, tx, *db, MCPMutationAuditEvent{
			OccurredAt: now, EventType: "mcp_mutation_approval_revoked",
			ActorType: "operator", ActorID: strings.TrimSpace(actorID),
			ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
			ApprovalID: deniedApproval.ApprovalID, RunID: proposal.RunID,
			AgentID: proposal.AgentID, PrincipalUserID: proposal.PrincipalUserID,
			ServerID: proposal.ServerID, ToolAlias: proposal.ToolAlias,
			ReasonCode: "proposal_denied",
		}); err != nil {
			return MCPMutationProposalRow{}, false, err
		}
	}
	proposal.State = MCPMutationStateDenied
	proposal.TerminalAt = sql.NullTime{Time: now.UTC(), Valid: true}
	proposal.TerminalReason = sql.NullString{String: strings.TrimSpace(reason), Valid: true}
	proposal.LastUpdatedAt = now.UTC()
	if err := insertMCPMutationAuditTx(txCtx, tx, *db, MCPMutationAuditEvent{
		OccurredAt: now, EventType: "mcp_mutation_proposal_denied",
		ActorType: "operator", ActorID: strings.TrimSpace(actorID),
		ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
		RunID: proposal.RunID, TraceID: proposal.TraceID,
		AgentID: proposal.AgentID, PrincipalUserID: proposal.PrincipalUserID,
		ServerID: proposal.ServerID, ToolAlias: proposal.ToolAlias,
		ReasonCode: "operator_denied",
	}); err != nil {
		return MCPMutationProposalRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return MCPMutationProposalRow{}, false, NormalizeTransactionError(txCtx, err)
	}
	return proposal, true, nil
}

// ListMCPMutationApprovalsByProposal returns bounded approval history for one
// proposal. It never returns credentials or raw arguments.
func ListMCPMutationApprovalsByProposal(ctx context.Context, db *Handler, proposalID string) ([]MCPMutationApprovalRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return nil, ErrMCPMutationStoreUnsupported
	}
	query := `SELECT ` + mcpApprovalColumns() + ` FROM MCPMutationApprovals
		WHERE proposal_id = ` + mcpMutationPlaceholder(*db, 1) + `
		ORDER BY approved_at DESC LIMIT 50`
	rows, err := (*db).QueryContext(ctx, query, proposalID)
	if err != nil {
		return nil, mcpMutationStoreError(err)
	}
	defer rows.Close()
	out := make([]MCPMutationApprovalRow, 0, 4)
	for rows.Next() {
		row, err := scanMCPMutationApprovalRow(rows)
		if err != nil {
			return nil, mcpMutationStoreError(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mcpMutationStoreError(err)
	}
	return out, nil
}

// tableUniqueColumnsExist verifies an exact unique column set, not merely the
// presence of some unique constraint. This catches a dropped or widened
// logical-call key that would otherwise pass a table-only probe.
func tableUniqueColumnsExist(ctx context.Context, db Handler, table string, columns []string) bool {
	expected := make(map[string]bool, len(columns))
	for _, column := range columns {
		expected[strings.ToLower(strings.TrimSpace(column))] = true
	}
	same := func(actual []string) bool {
		if len(actual) != len(expected) {
			return false
		}
		seen := make(map[string]bool, len(actual))
		for _, column := range actual {
			column = strings.ToLower(strings.TrimSpace(column))
			if !expected[column] || seen[column] {
				return false
			}
			seen[column] = true
		}
		return true
	}
	switch normalizeDBMS(db.DBMS()) {
	case "sqlite":
		rows, err := db.QueryContext(ctx, `SELECT name, "unique" FROM pragma_index_list(?)`, table)
		if err != nil {
			return false
		}
		type index struct {
			name   string
			unique int
		}
		var indexes []index
		for rows.Next() {
			var item index
			if err := rows.Scan(&item.name, &item.unique); err != nil {
				_ = rows.Close()
				return false
			}
			indexes = append(indexes, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return false
		}
		_ = rows.Close()
		for _, item := range indexes {
			if item.unique == 0 {
				continue
			}
			columnRows, err := db.QueryContext(ctx, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, item.name)
			if err != nil {
				return false
			}
			var actual []string
			for columnRows.Next() {
				var name sql.NullString
				if err := columnRows.Scan(&name); err != nil {
					_ = columnRows.Close()
					return false
				}
				if name.Valid {
					actual = append(actual, name.String)
				}
			}
			if err := columnRows.Err(); err != nil {
				_ = columnRows.Close()
				return false
			}
			_ = columnRows.Close()
			if same(actual) {
				return true
			}
		}
		return false
	case "postgres":
		rows, err := db.QueryContext(ctx, `SELECT i.relname, a.attname
			FROM pg_index ix
			JOIN pg_class t ON t.oid = ix.indrelid
			JOIN pg_namespace n ON n.oid = t.relnamespace
			JOIN pg_class i ON i.oid = ix.indexrelid
			JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
			WHERE n.nspname = current_schema()
			  AND lower(t.relname) = lower($1)
			  AND ix.indisunique
			ORDER BY i.relname, a.attnum`, table)
		if err != nil {
			return false
		}
		defer rows.Close()
		indexes := make(map[string][]string)
		for rows.Next() {
			var indexName, columnName string
			if err := rows.Scan(&indexName, &columnName); err != nil {
				return false
			}
			indexes[indexName] = append(indexes[indexName], columnName)
		}
		if err := rows.Err(); err != nil {
			return false
		}
		for _, actual := range indexes {
			if same(actual) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
