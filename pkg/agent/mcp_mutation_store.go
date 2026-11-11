package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// DatabaseMCPMutationProposalStore is the durable proposal adapter. It
// implements the same Admit/Get contract as the in-memory test store; it never
// dispatches an external operation.
type DatabaseMCPMutationProposalStore struct {
	DB *cdb.Handler
}

// NewDatabaseMCPMutationProposalStore verifies the additive schema-1.17
// capability before returning a store. A database that advertises 1.17 but
// lacks the capability fails closed.
func NewDatabaseMCPMutationProposalStore(db *cdb.Handler) (*DatabaseMCPMutationProposalStore, error) {
	if db == nil || *db == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cdb.CheckMCPMutationCapability(ctx, db); err != nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	return &DatabaseMCPMutationProposalStore{DB: db}, nil
}

func mcpProposalDBRow(proposal MCPMutationProposal) cdb.MCPMutationProposalRow {
	row := cdb.MCPMutationProposalRow{
		ProposalID:         proposal.ProposalID,
		ProposalSHA256:     proposal.ProposalSHA256,
		IdentityVersion:    proposal.IdentityVersion,
		RunID:              proposal.RunID,
		TraceID:            proposal.TraceID,
		EffectiveCallID:    proposal.EffectiveCallID,
		AgentID:            proposal.AgentID,
		Owner:              proposal.Owner,
		PrincipalUserID:    proposal.Principal.UserID,
		PrincipalUsername:  proposal.Principal.Username,
		PrincipalIssuer:    proposal.Principal.Issuer,
		ServerID:           proposal.ServerID,
		RawToolName:        proposal.RawToolName,
		ToolAlias:          proposal.ToolAlias,
		Transport:          proposal.Transport,
		TransportIdentity:  proposal.TransportIdentity,
		SchemaSHA256:       proposal.SchemaSHA256,
		CanonicalArguments: append([]byte(nil), proposal.CanonicalArguments...),
		ArgumentsSHA256:    proposal.ArgumentsSHA256,
		PolicyID:           proposal.PolicyID,
		PolicyVersion:      proposal.PolicyVersion,
		PolicySHA256:       proposal.PolicySHA256,
		PolicySnapshot:     append([]byte(nil), proposal.PolicySnapshot...),
		SourceIDs:          normalizeMCPSourceIDs(proposal.SourceIDs),
		ExternalResource:   proposal.ExternalResource,
		Destructive:        proposal.Destructive,
		ApprovalRequired:   proposal.ApprovalRequired,
		ExecutionBackend:   proposal.ExecutionBackend,
		State:              string(proposal.State),
		CreatedAt:          proposal.CreatedAt.UTC(),
		LastUpdatedAt:      proposal.LastUpdatedAt.UTC(),
		ExpiresAt:          proposal.ExpiresAt.UTC(),
	}
	if !proposal.ApprovedAt.IsZero() {
		row.ApprovedAt = sql.NullTime{Time: proposal.ApprovedAt.UTC(), Valid: true}
	}
	if !proposal.TerminalAt.IsZero() {
		row.TerminalAt = sql.NullTime{Time: proposal.TerminalAt.UTC(), Valid: true}
	}
	if strings.TrimSpace(proposal.TerminalReason) != "" {
		row.TerminalReason = sql.NullString{String: proposal.TerminalReason, Valid: true}
	}
	return row
}

func mcpProposalFromDBRow(row cdb.MCPMutationProposalRow) MCPMutationProposal {
	proposal := MCPMutationProposal{
		ProposalID:         row.ProposalID,
		ProposalSHA256:     row.ProposalSHA256,
		IdentityVersion:    row.IdentityVersion,
		State:              MCPMutationProposalState(row.State),
		RunID:              row.RunID,
		TraceID:            row.TraceID,
		EffectiveCallID:    row.EffectiveCallID,
		AgentID:            row.AgentID,
		Owner:              row.Owner,
		Principal:          MCPMutationPrincipal{UserID: row.PrincipalUserID, Username: row.PrincipalUsername, Issuer: row.PrincipalIssuer},
		ServerID:           row.ServerID,
		RawToolName:        row.RawToolName,
		ToolAlias:          row.ToolAlias,
		Transport:          row.Transport,
		TransportIdentity:  row.TransportIdentity,
		SchemaSHA256:       row.SchemaSHA256,
		CanonicalArguments: append([]byte(nil), row.CanonicalArguments...),
		ArgumentsSHA256:    row.ArgumentsSHA256,
		PolicyID:           row.PolicyID,
		PolicyVersion:      row.PolicyVersion,
		PolicySHA256:       row.PolicySHA256,
		PolicySnapshot:     append([]byte(nil), row.PolicySnapshot...),
		SourceIDs:          normalizeMCPSourceIDs(row.SourceIDs),
		ExternalResource:   row.ExternalResource,
		Destructive:        row.Destructive,
		ApprovalRequired:   row.ApprovalRequired,
		ExecutionBackend:   row.ExecutionBackend,
		CreatedAt:          row.CreatedAt.UTC(),
		LastUpdatedAt:      row.LastUpdatedAt.UTC(),
		ExpiresAt:          row.ExpiresAt.UTC(),
	}
	// The canonical bytes are authoritative; decoding them for callers keeps
	// the stored map and bytes aligned without retaining caller-owned memory.
	if decoded, err := canonicalMCPArgumentsFromBytes(row.CanonicalArguments); err == nil {
		proposal.Arguments = decoded
	}
	if row.ApprovedAt.Valid {
		proposal.ApprovedAt = row.ApprovedAt.Time.UTC()
	}
	if row.TerminalAt.Valid {
		proposal.TerminalAt = row.TerminalAt.Time.UTC()
	}
	if row.TerminalReason.Valid {
		proposal.TerminalReason = row.TerminalReason.String
	}
	return proposal
}

func mapMCPMutationDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, cdb.ErrMCPMutationProposalConflict):
		return ErrMCPMutationProposalConflict
	case errors.Is(err, cdb.ErrMCPMutationProposalExpired):
		return ErrMCPMutationProposalExpired
	case errors.Is(err, cdb.ErrMCPMutationQuotaReached):
		return ErrMCPMutationQuota
	case errors.Is(err, cdb.ErrMCPMutationApprovalConflict):
		return ErrMCPMutationApproval
	case errors.Is(err, cdb.ErrMCPMutationApprovalExpired), errors.Is(err, cdb.ErrMCPMutationApprovalRevoked):
		return ErrMCPMutationApproval
	case errors.Is(err, cdb.ErrMCPMutationSchemaUnavailable), errors.Is(err, cdb.ErrMCPMutationStoreUnsupported):
		return ErrMCPMutationStoreUnavailable
	case errors.Is(err, cdb.ErrMCPMutationProposalNotFound):
		return ErrMCPMutationProposalConflict
	case errors.Is(err, cdb.ErrMCPMutationApprovalNotFound):
		return ErrMCPMutationApproval
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %v", ErrMCPMutationStoreUnavailable, err)
	}
}

// Admit implements MCPMutationProposalStore using the durable schema.
func (s *DatabaseMCPMutationProposalStore) Admit(ctx context.Context, proposal MCPMutationProposal, maxPerRun, maxPendingPerPrincipal int) (MCPMutationProposal, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	proposal.CanonicalArguments = append([]byte(nil), proposal.CanonicalArguments...)
	proposal.PolicySnapshot = append([]byte(nil), proposal.PolicySnapshot...)
	proposal.SourceIDs = normalizeMCPSourceIDs(proposal.SourceIDs)
	if err := validateMCPMutationProposalForAdmission(proposal); err != nil {
		return MCPMutationProposal{}, err
	}
	if len(proposal.PolicySnapshot) == 0 {
		return MCPMutationProposal{}, fmt.Errorf("%w: policy snapshot is required for durable persistence", ErrMCPMutationPolicyInvalid)
	}
	row, _, err := cdb.AdmitMCPMutationProposal(ctx, s.DB, mcpProposalDBRow(proposal), maxPerRun, maxPendingPerPrincipal)
	if err != nil {
		return MCPMutationProposal{}, mapMCPMutationDatabaseError(err)
	}
	return mcpProposalFromDBRow(row), nil
}

// Get implements MCPMutationProposalStore using the durable schema.
func (s *DatabaseMCPMutationProposalStore) Get(ctx context.Context, proposalID string) (MCPMutationProposal, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationProposal{}, false, ErrMCPMutationStoreUnavailable
	}
	row, found, err := cdb.GetMCPMutationProposal(ctx, s.DB, proposalID)
	if err != nil {
		return MCPMutationProposal{}, false, mapMCPMutationDatabaseError(err)
	}
	if !found {
		return MCPMutationProposal{}, false, nil
	}
	return mcpProposalFromDBRow(row), true, nil
}

// MCPMutationApprovalRequest is one trusted operator approval request.
type MCPMutationApprovalRequest struct {
	ProposalID     string
	ProposalSHA256 string
	Approver       MCPMutationPrincipal
	ExpiresAt      time.Time
	MaxExecutions  int
	Reason         string
}

// DatabaseMCPMutationApprovalStore is the durable approval adapter. It
// implements the verifier's Lookup contract and the operator issuance and
// revocation operations. It never dispatches.
type DatabaseMCPMutationApprovalStore struct {
	DB          *cdb.Handler
	MaxLifetime time.Duration
	Now         func() time.Time
}

func NewDatabaseMCPMutationApprovalStore(db *cdb.Handler, maxLifetime time.Duration) (*DatabaseMCPMutationApprovalStore, error) {
	if db == nil || *db == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cdb.CheckMCPMutationCapability(ctx, db); err != nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	if maxLifetime <= 0 {
		maxLifetime = time.Hour
	}
	return &DatabaseMCPMutationApprovalStore{DB: db, MaxLifetime: maxLifetime}, nil
}

func (s *DatabaseMCPMutationApprovalStore) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func mcpApprovalFromDBRow(row cdb.MCPMutationApprovalRow) MCPMutationApproval {
	approval := MCPMutationApproval{
		ApprovalID:        row.ApprovalID,
		ProposalID:        row.ProposalID,
		ProposalSHA256:    row.ProposalSHA256,
		RunID:             row.RunID,
		AgentID:           row.AgentID,
		ServerID:          row.ServerID,
		ToolAlias:         row.ToolAlias,
		ArgumentsSHA256:   row.ArgumentsSHA256,
		PolicySHA256:      row.PolicySHA256,
		ApprovedByUserID:  row.ApprovedByUserID,
		ApprovedBy:        row.ApprovedBy,
		Issuer:            row.ApprovedByIssuer,
		ApprovedAt:        row.ApprovedAt.UTC(),
		ExpiresAt:         row.ExpiresAt.UTC(),
		MaxExecutions:     row.MaxExecutions,
		ExecutionsStarted: row.ExecutionsStarted,
		State:             row.State,
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time.UTC()
		approval.RevokedAt = &t
	}
	return approval
}

// Lookup implements MCPMutationApprovalStore for the verifier.
func (s *DatabaseMCPMutationApprovalStore) Lookup(ctx context.Context, approvalID string) (MCPMutationApproval, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	row, found, err := cdb.GetMCPMutationApproval(ctx, s.DB, approvalID)
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	if !found {
		return MCPMutationApproval{}, false, nil
	}
	return mcpApprovalFromDBRow(row), true, nil
}

// Issue persists one operator approval and moves the proposal to approved.
func (s *DatabaseMCPMutationApprovalStore) Issue(ctx context.Context, request MCPMutationApprovalRequest) (MCPMutationApproval, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	if err := request.Approver.Validate(); err != nil {
		return MCPMutationApproval{}, false, ErrMCPMutationPrincipal
	}
	proposalID := strings.TrimSpace(request.ProposalID)
	fingerprint := strings.TrimSpace(request.ProposalSHA256)
	if proposalID == "" || fingerprint == "" {
		return MCPMutationApproval{}, false, ErrMCPMutationProposalConflict
	}
	maxExecutions := request.MaxExecutions
	if maxExecutions == 0 {
		maxExecutions = 1
	}
	if maxExecutions != 1 {
		return MCPMutationApproval{}, false, ErrMCPMutationApproval
	}
	proposalRow, found, err := cdb.GetMCPMutationProposal(ctx, s.DB, proposalID)
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	if !found {
		return MCPMutationApproval{}, false, ErrMCPMutationProposalConflict
	}
	if proposalRow.ProposalSHA256 != fingerprint {
		return MCPMutationApproval{}, false, ErrMCPMutationProposalConflict
	}
	now := s.now()
	if !proposalRow.ExpiresAt.After(now) {
		return MCPMutationApproval{}, false, ErrMCPMutationProposalExpired
	}
	approvedAt := now.UTC().Truncate(time.Microsecond)
	expiresAt := request.ExpiresAt.UTC()
	if expiresAt.IsZero() {
		expiresAt = approvedAt.Add(s.MaxLifetime)
	}
	if expiresAt.After(proposalRow.ExpiresAt) {
		expiresAt = proposalRow.ExpiresAt.UTC()
	}
	if !expiresAt.After(approvedAt) || expiresAt.Sub(approvedAt) > s.MaxLifetime {
		return MCPMutationApproval{}, false, ErrMCPMutationApproval
	}
	approvalID, err := newMCPProposalID()
	if err != nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	row := cdb.MCPMutationApprovalRow{
		ApprovalID:       approvalID,
		ProposalID:       proposalID,
		ProposalSHA256:   fingerprint,
		ApprovedByUserID: request.Approver.UserID,
		ApprovedBy:       request.Approver.Username,
		ApprovedByIssuer: request.Approver.Issuer,
		ApprovedAt:       approvedAt,
		ExpiresAt:        expiresAt,
		MaxExecutions:    maxExecutions,
		LastUpdatedAt:    now.UTC(),
	}
	stored, inserted, err := cdb.IssueMCPMutationApproval(ctx, s.DB, row, fingerprint, now)
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	return mcpApprovalFromDBRow(stored), inserted, nil
}

// Revoke revokes one active approval without deleting history.
func (s *DatabaseMCPMutationApprovalStore) Revoke(ctx context.Context, approvalID string, principal MCPMutationPrincipal, reason string) (MCPMutationApproval, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	if err := principal.Validate(); err != nil {
		return MCPMutationApproval{}, false, ErrMCPMutationPrincipal
	}
	row, revoked, err := cdb.RevokeMCPMutationApproval(ctx, s.DB, strings.TrimSpace(approvalID),
		principal.UserID, principal.Username, reason, s.now())
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	return mcpApprovalFromDBRow(row), revoked, nil
}

// GetActiveForProposal returns the current active approval for a proposal.
func (s *DatabaseMCPMutationApprovalStore) GetActiveForProposal(ctx context.Context, proposalSHA256 string) (MCPMutationApproval, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	row, found, err := cdb.GetActiveMCPMutationApprovalByProposal(ctx, s.DB, proposalSHA256)
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	if !found {
		return MCPMutationApproval{}, false, nil
	}
	return mcpApprovalFromDBRow(row), true, nil
}

// canonicalMCPArgumentsFromBytes decodes retained canonical bytes without
// accepting a different representation.
func canonicalMCPArgumentsFromBytes(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, ErrMCPMutationArguments
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, ErrMCPMutationArguments
	}
	return decoded, nil
}
