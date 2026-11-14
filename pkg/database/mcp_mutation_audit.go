package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MCPMutationAuditEventAdmissionDenied is the durable event for a proposal
	// or tool-call admission that was denied before any proposal row existed.
	MCPMutationAuditEventAdmissionDenied = "mcp_mutation_admission_denied"
	// MCPMutationAuditEventRegistrationDenied is the durable event for a
	// production proposal-tool registration that failed a trusted gate.
	MCPMutationAuditEventRegistrationDenied = "mcp_mutation_registration_denied"

	// MCPMutationAuditCleanupMaxBatch bounds one retention cleanup pass. The
	// Events janitor calls the cleanup hourly so an attacker cannot force an
	// unbounded delete or a long table lock.
	MCPMutationAuditCleanupMaxBatch = 1000

	mcpMutationAuditMaxTextBytes   = 256
	mcpMutationAuditMaxReasonBytes = 64
	mcpMutationAuditMaxEventBytes  = 128
	mcpMutationAuditMaxDetailBytes = 4096
	mcpMutationAuditMaxDetailKeys  = 32
	mcpMutationAuditMaxDetailItems = 16
)

var (
	mcpMutationAuditEventPattern  = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9_:-]{1,%d}$`, mcpMutationAuditMaxEventBytes))
	mcpMutationAuditReasonPattern = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9_:-]{1,%d}$`, mcpMutationAuditMaxReasonBytes))
	mcpMutationAuditKeyPattern    = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	mcpMutationAuditHashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// mcpMutationAuditRetainedEventTypes lists every event type this writer
// produces. Retention deliberately targets this bounded allowlist: an unknown
// event type is retained rather than silently deleted by a future writer.
var mcpMutationAuditRetainedEventTypes = []string{
	"mcp_mutation_proposal_created",
	"mcp_mutation_proposal_expired",
	"mcp_mutation_proposal_reopened",
	"mcp_mutation_proposal_denied",
	"mcp_mutation_approval_issued",
	"mcp_mutation_approval_expired",
	"mcp_mutation_approval_exhausted",
	"mcp_mutation_approval_revoked",
	MCPMutationAuditEventAdmissionDenied,
	MCPMutationAuditEventRegistrationDenied,
}

// InsertMCPMutationAudit appends one already-redacted audit event outside a
// proposal or approval transaction. Denied admissions never reach a mutation
// transaction, so this is their durable append path. A database failure is
// returned to the caller, which must preserve the original denial and must not
// turn the audit failure into an admission or a success.
func InsertMCPMutationAudit(ctx context.Context, db *Handler, event MCPMutationAuditEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return ErrMCPMutationStoreUnsupported
	}
	normalized, err := normalizeMCPMutationAuditEvent(event, time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = (*db).ExecContext(ctx, mcpMutationAuditInsertSQL(*db), mcpMutationAuditInsertArgs(normalized)...)
	return mcpMutationStoreError(err)
}

// CleanupMCPMutationAudit removes at most limit expired audit rows. It uses
// the existing (event_type, occurred_at) index and the bounded event-type
// allowlist, so retention never scans or deletes an unknown event family.
func CleanupMCPMutationAudit(ctx context.Context, db *Handler, cutoff time.Time, limit int) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return 0, fmt.Errorf("database handler is nil")
	}
	if !supportsMCPMutationStore(*db) {
		return 0, ErrMCPMutationStoreUnsupported
	}
	if cutoff.IsZero() {
		return 0, fmt.Errorf("MCP mutation audit retention cutoff is required")
	}
	if limit <= 0 || limit > MCPMutationAuditCleanupMaxBatch {
		limit = MCPMutationAuditCleanupMaxBatch
	}
	p := func(n int) string { return mcpMutationPlaceholder(*db, n) }
	placeholders := make([]string, len(mcpMutationAuditRetainedEventTypes))
	args := make([]any, 0, len(mcpMutationAuditRetainedEventTypes)+2)
	for i, eventType := range mcpMutationAuditRetainedEventTypes {
		placeholders[i] = p(i + 1)
		args = append(args, eventType)
	}
	args = append(args, cutoff.UTC(), limit)
	query := `DELETE FROM MCPMutationAudit
		WHERE event_id IN (
			SELECT event_id FROM MCPMutationAudit
			WHERE event_type IN (` + strings.Join(placeholders, ", ") + `)
			  AND occurred_at < ` + p(len(mcpMutationAuditRetainedEventTypes)+1) + `
			ORDER BY event_id
			LIMIT ` + p(len(mcpMutationAuditRetainedEventTypes)+2) + `
		)`
	res, err := (*db).ExecContext(ctx, query, args...)
	if err != nil {
		return 0, mcpMutationStoreError(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func normalizeMCPMutationAuditEvent(event MCPMutationAuditEvent, now time.Time) (MCPMutationAuditEvent, error) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now.UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	event.EventType = strings.TrimSpace(event.EventType)
	if !mcpMutationAuditEventPattern.MatchString(event.EventType) {
		return MCPMutationAuditEvent{}, fmt.Errorf("invalid MCP mutation audit event type")
	}
	event.ActorType = strings.TrimSpace(event.ActorType)
	switch event.ActorType {
	case "agent", "operator", "system":
	default:
		return MCPMutationAuditEvent{}, fmt.Errorf("invalid MCP mutation audit actor type")
	}
	event.ActorID = mcpMutationAuditText(event.ActorID, mcpMutationAuditMaxTextBytes)
	if event.ActorID == "" {
		return MCPMutationAuditEvent{}, fmt.Errorf("MCP mutation audit actor ID is required")
	}
	event.ActorIssuer = mcpMutationAuditText(event.ActorIssuer, mcpMutationAuditMaxTextBytes)
	event.RunID = mcpMutationAuditText(event.RunID, mcpMutationAuditMaxTextBytes)
	event.TraceID = mcpMutationAuditText(event.TraceID, mcpMutationAuditMaxTextBytes)
	event.AgentID = mcpMutationAuditText(event.AgentID, mcpMutationAuditMaxTextBytes)
	event.ServerID = mcpMutationAuditText(event.ServerID, mcpMutationAuditMaxTextBytes)
	event.ToolAlias = mcpMutationAuditText(event.ToolAlias, mcpMutationAuditMaxTextBytes)
	event.ReasonCode = strings.TrimSpace(event.ReasonCode)
	if event.ReasonCode != "" && !mcpMutationAuditReasonPattern.MatchString(event.ReasonCode) {
		return MCPMutationAuditEvent{}, fmt.Errorf("invalid MCP mutation audit reason code")
	}
	for _, hash := range []string{event.ProposalID, event.ProposalSHA256, event.ApprovalID, event.AttemptID} {
		if hash != "" && !mcpMutationAuditHashPattern.MatchString(hash) {
			return MCPMutationAuditEvent{}, fmt.Errorf("invalid MCP mutation audit identifier hash")
		}
	}
	if len(event.RedactedDetail) == 0 {
		event.RedactedDetail = []byte(`{}`)
	}
	if len(event.RedactedDetail) > mcpMutationAuditMaxDetailBytes {
		return MCPMutationAuditEvent{}, fmt.Errorf("MCP mutation audit detail is too large")
	}
	if err := validateMCPMutationAuditDetail(event.RedactedDetail); err != nil {
		return MCPMutationAuditEvent{}, err
	}
	return event, nil
}

func validateMCPMutationAuditDetail(raw []byte) error {
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(raw, &detail); err != nil {
		return fmt.Errorf("MCP mutation audit detail is not a JSON object")
	}
	if detail == nil {
		return fmt.Errorf("MCP mutation audit detail is not a JSON object")
	}
	if len(detail) > mcpMutationAuditMaxDetailKeys {
		return fmt.Errorf("MCP mutation audit detail has too many keys")
	}
	for key, value := range detail {
		if !mcpMutationAuditKeyPattern.MatchString(key) {
			return fmt.Errorf("MCP mutation audit detail has an invalid key")
		}
		var probe any
		if err := json.Unmarshal(value, &probe); err != nil {
			return fmt.Errorf("MCP mutation audit detail has an invalid value")
		}
		switch typed := probe.(type) {
		case nil, bool, float64:
		case string:
			if len(typed) > mcpMutationAuditMaxTextBytes {
				return fmt.Errorf("MCP mutation audit detail string is too long")
			}
		case []any:
			if len(typed) > mcpMutationAuditMaxDetailItems {
				return fmt.Errorf("MCP mutation audit detail list is too long")
			}
			for _, item := range typed {
				text, ok := item.(string)
				if !ok || len(text) > mcpMutationAuditMaxTextBytes {
					return fmt.Errorf("MCP mutation audit detail list must contain bounded strings")
				}
			}
		default:
			return fmt.Errorf("MCP mutation audit detail must not contain nested objects")
		}
	}
	return nil
}

func mcpMutationAuditText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out strings.Builder
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

func mcpMutationAuditInsertSQL(db Handler) string {
	p := func(n int) string { return mcpMutationPlaceholder(db, n) }
	return `INSERT INTO MCPMutationAudit
		(occurred_at, event_type, actor_type, actor_id, actor_issuer,
		 proposal_id, proposal_sha256, approval_id, attempt_id, run_id,
		 trace_id, agent_id, principal_user_id, server_id, tool_alias,
		 reason_code, redacted_detail)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `,
		 ` + p(6) + `, ` + p(7) + `, ` + p(8) + `, ` + p(9) + `, ` + p(10) + `,
		 ` + p(11) + `, ` + p(12) + `, ` + p(13) + `, ` + p(14) + `, ` + p(15) + `,
		 ` + p(16) + `, ` + p(17) + `)`
}

func mcpMutationAuditInsertArgs(event MCPMutationAuditEvent) []any {
	nullable := func(value string) any {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return value
	}
	var principal any
	if event.PrincipalUserID != 0 {
		principal = event.PrincipalUserID
	}
	return []any{
		event.OccurredAt.UTC(), event.EventType, event.ActorType, event.ActorID,
		nullable(event.ActorIssuer), nullable(event.ProposalID),
		nullable(event.ProposalSHA256), nullable(event.ApprovalID),
		nullable(event.AttemptID), nullable(event.RunID), nullable(event.TraceID),
		nullable(event.AgentID), principal, nullable(event.ServerID),
		nullable(event.ToolAlias), nullable(event.ReasonCode), string(event.RedactedDetail),
	}
}

// insertMCPMutationAuditExec is shared by the transaction-bound writer and the
// standalone writer so both enforce the same bounded audit contract.
func insertMCPMutationAuditExec(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, db Handler, event MCPMutationAuditEvent) error {
	normalized, err := normalizeMCPMutationAuditEvent(event, time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = exec.ExecContext(ctx, mcpMutationAuditInsertSQL(db), mcpMutationAuditInsertArgs(normalized)...)
	return mcpMutationStoreError(err)
}
