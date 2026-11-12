package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// DatabaseMCPMutationProposalStore is the durable proposal adapter. It
// implements the same Admit/Get contract as the in-memory test store; it never
// dispatches an external operation.
type DatabaseMCPMutationProposalStore struct {
	DB  *cdb.Handler
	Now func() time.Time
}

func (s *DatabaseMCPMutationProposalStore) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
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

func isMCPMutationHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func isMCPMutationState(value string) bool {
	switch value {
	case cdb.MCPMutationStateProposed, cdb.MCPMutationStateApprovalRequired,
		cdb.MCPMutationStateApproved, cdb.MCPMutationStateDenied,
		cdb.MCPMutationStateExpired, cdb.MCPMutationStateDispatching,
		cdb.MCPMutationStateSucceeded, cdb.MCPMutationStateFailed,
		cdb.MCPMutationStateUncertain, cdb.MCPMutationStateReconciliationRequired:
		return true
	default:
		return false
	}
}

func mcpProposalFromDBRow(row cdb.MCPMutationProposalRow) (MCPMutationProposal, error) {
	corrupt := func(reason string) (MCPMutationProposal, error) {
		return MCPMutationProposal{}, fmt.Errorf("%w: %s", ErrMCPMutationProposalCorrupt, reason)
	}
	if row.IdentityVersion != MCPMutationIdentityVersion {
		return corrupt("unsupported identity version")
	}
	if !isMCPMutationHash(row.ProposalID) || !isMCPMutationHash(row.ProposalSHA256) ||
		!isMCPMutationHash(row.SchemaSHA256) || !isMCPMutationHash(row.ArgumentsSHA256) ||
		!isMCPMutationHash(row.PolicySHA256) {
		return corrupt("invalid stored hash or identifier")
	}
	if !isMCPMutationState(row.State) {
		return corrupt("invalid stored state")
	}
	if strings.TrimSpace(row.RunID) == "" || strings.TrimSpace(row.EffectiveCallID) == "" ||
		strings.TrimSpace(row.AgentID) == "" || strings.TrimSpace(row.Owner) == "" ||
		row.PrincipalUserID == 0 || strings.TrimSpace(row.PrincipalUsername) == "" ||
		strings.TrimSpace(row.PrincipalIssuer) == "" || strings.TrimSpace(row.ServerID) == "" ||
		strings.TrimSpace(row.RawToolName) == "" || strings.TrimSpace(row.ToolAlias) == "" ||
		strings.TrimSpace(row.Transport) == "" || strings.TrimSpace(row.TransportIdentity) == "" {
		return corrupt("missing trusted proposal identity")
	}
	decoded, err := canonicalMCPArgumentsFromBytes(row.CanonicalArguments)
	if err != nil {
		return corrupt("canonical arguments are invalid")
	}
	if sha256Hex(row.CanonicalArguments) != row.ArgumentsSHA256 {
		return corrupt("canonical argument hash mismatch")
	}
	if len(row.PolicySnapshot) == 0 {
		return corrupt("policy snapshot is missing")
	}
	var policy MCPMutationPolicy
	if err := json.Unmarshal(row.PolicySnapshot, &policy); err != nil {
		return corrupt("policy snapshot is invalid")
	}
	policyBytes, err := json.Marshal(policy)
	if err != nil || sha256Hex(policyBytes) != row.PolicySHA256 {
		return corrupt("policy snapshot hash mismatch")
	}
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
		Arguments:          decoded,
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
	if row.ApprovedAt.Valid {
		proposal.ApprovedAt = row.ApprovedAt.Time.UTC()
	}
	if row.TerminalAt.Valid {
		proposal.TerminalAt = row.TerminalAt.Time.UTC()
	}
	if row.TerminalReason.Valid {
		proposal.TerminalReason = row.TerminalReason.String
	}
	if row.CreatedAt.IsZero() || !row.ExpiresAt.After(row.CreatedAt) {
		return corrupt("invalid proposal lifetime")
	}
	if row.ApprovedAt.Valid && row.ApprovedAt.Time.Before(row.CreatedAt) {
		return corrupt("approval predates proposal")
	}
	terminal := row.State == cdb.MCPMutationStateDenied || row.State == cdb.MCPMutationStateExpired ||
		row.State == cdb.MCPMutationStateSucceeded || row.State == cdb.MCPMutationStateFailed
	if terminal != row.TerminalAt.Valid {
		return corrupt("terminal timestamp inconsistent with state")
	}
	if len(row.SourceIDs) > 0 && strings.TrimSpace(row.ExternalResource) != "" {
		return corrupt("proposal has both source and external-resource scope")
	}
	if len(row.SourceIDs) == 0 && strings.TrimSpace(row.ExternalResource) == "" {
		return corrupt("proposal has no scope")
	}
	if proposalFingerprint(proposal) != row.ProposalSHA256 {
		return corrupt("proposal fingerprint mismatch")
	}
	return proposal, nil
}

// ValidateStoredMCPMutationProposal validates a durable row before the
// operator API or a future dispatcher treats it as authoritative. It is the
// single conversion/validation boundary for stored proposals.
func ValidateStoredMCPMutationProposal(row cdb.MCPMutationProposalRow) (MCPMutationProposal, error) {
	return mcpProposalFromDBRow(row)
}

// MCPMutationProposalFingerprint exposes the stable proposal identity for
// trusted stores and tests; it does not grant authority.
func MCPMutationProposalFingerprint(proposal MCPMutationProposal) string {
	return proposalFingerprint(proposal)
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
	row, _, err := cdb.AdmitMCPMutationProposalAt(ctx, s.DB, mcpProposalDBRow(proposal), maxPerRun, maxPendingPerPrincipal, s.now())
	if err != nil {
		return MCPMutationProposal{}, mapMCPMutationDatabaseError(err)
	}
	converted, err := mcpProposalFromDBRow(row)
	if err != nil {
		return MCPMutationProposal{}, err
	}
	return converted, nil
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
	converted, err := mcpProposalFromDBRow(row)
	if err != nil {
		return MCPMutationProposal{}, true, err
	}
	return converted, true, nil
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

func mcpApprovalFromDBRow(row cdb.MCPMutationApprovalRow) (MCPMutationApproval, error) {
	corrupt := func(reason string) (MCPMutationApproval, error) {
		return MCPMutationApproval{}, fmt.Errorf("%w: %s", ErrMCPMutationApprovalCorrupt, reason)
	}
	if !isMCPMutationHash(row.ApprovalID) || !isMCPMutationHash(row.ProposalID) ||
		!isMCPMutationHash(row.ProposalSHA256) || !isMCPMutationHash(row.ArgumentsSHA256) ||
		!isMCPMutationHash(row.PolicySHA256) {
		return corrupt("invalid stored hash or identifier")
	}
	if row.State != "active" && row.State != "revoked" && row.State != "expired" && row.State != "exhausted" {
		return corrupt("invalid approval state")
	}
	if row.ApprovedByUserID == 0 || strings.TrimSpace(row.ApprovedBy) == "" ||
		strings.TrimSpace(row.ApprovedByIssuer) == "" {
		return corrupt("missing approver identity")
	}
	if row.MaxExecutions <= 0 || row.ExecutionsStarted < 0 || row.ExecutionsStarted > row.MaxExecutions {
		return corrupt("invalid execution allowance")
	}
	if row.ApprovedAt.IsZero() || !row.ExpiresAt.After(row.ApprovedAt) {
		return corrupt("invalid approval lifetime")
	}
	if (row.State == "revoked") != row.RevokedAt.Valid {
		return corrupt("revocation state inconsistent with timestamp")
	}
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
	return approval, nil
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
	converted, err := mcpApprovalFromDBRow(row)
	if err != nil {
		return MCPMutationApproval{}, true, err
	}
	return converted, true, nil
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
	if _, err := mcpProposalFromDBRow(proposalRow); err != nil {
		return MCPMutationApproval{}, false, err
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
	converted, err := mcpApprovalFromDBRow(stored)
	if err != nil {
		return MCPMutationApproval{}, inserted, err
	}
	return converted, inserted, nil
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
	converted, err := mcpApprovalFromDBRow(row)
	if err != nil {
		return MCPMutationApproval{}, revoked, err
	}
	return converted, revoked, nil
}

// GetActiveForProposal returns the current active approval for a proposal.
func (s *DatabaseMCPMutationApprovalStore) GetActiveForProposal(ctx context.Context, proposalSHA256 string) (MCPMutationApproval, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	row, found, err := cdb.GetActiveMCPMutationApprovalByProposal(ctx, s.DB, proposalSHA256, s.now())
	if err != nil {
		return MCPMutationApproval{}, false, mapMCPMutationDatabaseError(err)
	}
	if !found {
		return MCPMutationApproval{}, false, nil
	}
	converted, err := mcpApprovalFromDBRow(row)
	if err != nil {
		return MCPMutationApproval{}, true, err
	}
	return converted, true, nil
}

// canonicalMCPArgumentsFromBytes decodes retained canonical bytes without
// accepting a different representation.
func canonicalMCPArgumentsFromBytes(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, ErrMCPMutationArguments
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, ErrMCPMutationArguments
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrMCPMutationArguments
	}
	canonical, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrMCPMutationArguments
	}
	return decoded, nil
}
