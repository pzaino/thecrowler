package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// crawlRequestToolName is the prospective WP-03B2 tool/operation name used for
// allowlist and contract matching. It is deliberately not registered in
// AgentToolRegistry and cannot be called by a model in WP-03B2A.
const crawlRequestToolName = "request_source_crawl"

const maxCrawlRequestIDLen = 128

const (
	maxCrawlProposalLifetime = 24 * time.Hour
	maxCrawlApprovalLifetime = 24 * time.Hour
	maxCrawlRequestLifetime  = 24 * time.Hour
	maxCrawlPolicyRequests   = 100
	maxCrawlPolicyBudget     = 24 * time.Hour
)

// Stable authorization stages for audit correlation.
const (
	CrawlStageProposalReceived = "proposal_received"
	CrawlStagePolicyChecked    = "policy_checked"
	CrawlStageSourceVerified   = "source_verified"
	CrawlStageSourceDenied     = "source_denied"
	CrawlStageApprovalRequired = "approval_required"
	CrawlStageApprovalVerified = "approval_verified"
	CrawlStageApprovalRejected = "approval_rejected"
	CrawlStageAuthorized       = "authorized"
	CrawlStageSubmitted        = "submitted"
	CrawlStageReused           = "reused"
	CrawlStageSubmissionDenied = "submission_denied"
	CrawlStageSubmissionFailed = "submission_failed"
)

// Stable approval and source-ownership outcomes for audit correlation.
const (
	CrawlApprovalOutcomeNotRequested = "not_requested"
	CrawlApprovalOutcomeVerified     = "verified"
	CrawlApprovalOutcomeRejected     = "rejected"
	CrawlApprovalOutcomeRevoked      = "revoked"
	CrawlApprovalOutcomeExpired      = "expired"
	CrawlApprovalOutcomeUnavailable  = "unavailable"

	CrawlSourceOutcomeNotChecked  = "not_checked"
	CrawlSourceOutcomeVerified    = "verified"
	CrawlSourceOutcomeMissing     = "missing"
	CrawlSourceOutcomeDenied      = "denied"
	CrawlSourceOutcomeUnavailable = "unavailable"
)

// Crawl request policy reason codes.
const (
	CrawlReasonApproved                     = "crawl_request_approved"
	CrawlReasonInvalidProposal              = "crawl_proposal_invalid"
	CrawlReasonProposalExpired              = "crawl_proposal_expired"
	CrawlReasonMissingIdentity              = "crawl_missing_identity"
	CrawlReasonRunRequired                  = "crawl_run_required"
	CrawlReasonPrincipalRequired            = "crawl_principal_required"
	CrawlReasonPrincipalMismatch            = "crawl_principal_mismatch"
	CrawlReasonTrustRequired                = "crawl_trust_required"
	CrawlReasonCapabilityRequired           = "crawl_capability_required"
	CrawlReasonContractForbidden            = "crawl_contract_forbidden"
	CrawlReasonOperationNotAllowlisted      = "crawl_operation_not_allowlisted"
	CrawlReasonSourceScopeRequired          = "crawl_source_scope_required"
	CrawlReasonSourceOutOfScope             = "crawl_source_out_of_scope"
	CrawlReasonSourceAuthorizerUnavailable  = "crawl_source_authorizer_unavailable"
	CrawlReasonSourceOwnershipMissing       = "crawl_source_ownership_missing"
	CrawlReasonSourceForbidden              = "crawl_source_forbidden"
	CrawlReasonSourceAuthorizationError     = "crawl_source_authorization_error"
	CrawlReasonBudgetRequired               = "crawl_budget_required"
	CrawlReasonApprovalRequired             = "crawl_approval_required"
	CrawlReasonApprovalMismatch             = "crawl_approval_mismatch"
	CrawlReasonApprovalExpired              = "crawl_approval_expired"
	CrawlReasonApprovalVerifierUnavailable  = "crawl_approval_verifier_unavailable"
	CrawlReasonApprovalUnverified           = "crawl_approval_unverified"
	CrawlReasonApprovalRevoked              = "crawl_approval_revoked"
	CrawlReasonApprovalUnknown              = "crawl_approval_unknown"
	CrawlReasonApprovalIssuerUntrusted      = "crawl_approval_issuer_untrusted"
	CrawlReasonApprovalApproverUnauthorized = "crawl_approval_approver_unauthorized"
	CrawlReasonApprovalStoreUnavailable     = "crawl_approval_store_unavailable"
	CrawlReasonApprovalLifetimeExceeded     = "crawl_approval_lifetime_exceeded"
	CrawlReasonAuthorizationCancelled       = "crawl_authorization_cancelled"
	CrawlReasonAuthorizationTimeout         = "crawl_authorization_timeout"
	CrawlReasonRequestBuildFailed           = "crawl_request_build_failed"
)

var (
	// ErrCrawlApprovalUnverified means the trusted approval authority did not
	// verify the presented approval record.
	ErrCrawlApprovalUnverified = errors.New("crawl approval was not verified by a trusted authority")
	// ErrCrawlApprovalRevoked means the trusted authority revoked the approval.
	ErrCrawlApprovalRevoked = errors.New("crawl approval was revoked")
)

// CrawlApprovalVerifier is the trusted approval-authority boundary. A nil
// verifier fails closed. Implementations must resolve the approval in an
// authoritative store; matching caller-provided fields are not sufficient.
type CrawlApprovalVerifier interface {
	VerifyCrawlApproval(ctx context.Context, verification CrawlApprovalVerification) error
}

// CrawlRequestPolicy is trusted run configuration. It is never populated from
// model arguments.
type CrawlRequestPolicy struct {
	AllowedOperations map[string]bool
	AllowedSources    map[uint64]bool
	Principal         CrawlPrincipal
	SourceAuthorizer  CrawlSourceAuthorizer
	ApprovalVerifier  CrawlApprovalVerifier
	MaxRequests       int
	BudgetWindow      time.Duration
}

// CrawlRequestProposal is bounded, non-executable operator-facing data. It
// never mutates crawler state.
type CrawlRequestProposal struct {
	RequestID     string
	SourceID      uint64
	OperationType string
	ExpiresAt     time.Time
}

// Fingerprint returns a deterministic SHA-256 binding for the proposal.
func (p CrawlRequestProposal) Fingerprint() string {
	return crawlHashJSON(struct {
		RequestID     string
		SourceID      uint64
		OperationType string
		ExpiresAt     time.Time
	}{p.RequestID, p.SourceID, p.OperationType, p.ExpiresAt.UTC()})
}

// CrawlRequestApproval is an approval record presented for verification. The
// record fields alone are not an authorization credential; a trusted
// CrawlApprovalVerifier must resolve it in an authoritative store.
type CrawlRequestApproval struct {
	ApprovalID     string
	ProposalSHA256 string
	RunID          string
	AgentID        string
	Owner          string
	SourceID       uint64
	OperationType  string
	Issuer         string
	ApprovedBy     string
	ApproverUserID uint64
	ApprovedAt     time.Time
	ExpiresAt      time.Time
}

// Fingerprint returns a deterministic SHA-256 binding for the approval record.
func (a CrawlRequestApproval) Fingerprint() string {
	return crawlHashJSON(struct {
		ApprovalID     string
		ProposalSHA256 string
		RunID          string
		AgentID        string
		Owner          string
		SourceID       uint64
		OperationType  string
		Issuer         string
		ApprovedBy     string
		ApproverUserID uint64
		ApprovedAt     time.Time
		ExpiresAt      time.Time
	}{
		a.ApprovalID, a.ProposalSHA256, a.RunID, a.AgentID, a.Owner,
		a.SourceID, a.OperationType, a.Issuer, a.ApprovedBy, a.ApproverUserID,
		a.ApprovedAt.UTC(), a.ExpiresAt.UTC(),
	})
}

// CrawlRequestDecision is the deterministic policy result for one proposal.
// All authorization-relevant fields are unexported so an external caller
// cannot fabricate or mutate a verified decision. Read-only accessors expose
// the information needed for audit and future tool results.
type CrawlRequestDecision struct {
	allowed              bool
	reasonCode           string
	proposalSHA256       string
	approvalSHA256       string
	requiredCapabilities []string
	runID                string
	principalUserID      uint64
	maxRequests          int
	budgetWindow         time.Duration
	approvalOutcome      string
	sourceOutcome        string
	stage                string
	verified             bool
}

// Allowed reports whether the decision authorized the operation.
func (d CrawlRequestDecision) Allowed() bool { return d.allowed }

// ReasonCode returns the stable policy reason code.
func (d CrawlRequestDecision) ReasonCode() string { return d.reasonCode }

// ProposalSHA256 returns the authorized proposal fingerprint.
func (d CrawlRequestDecision) ProposalSHA256() string { return d.proposalSHA256 }

// ApprovalSHA256 returns the authorized approval fingerprint.
func (d CrawlRequestDecision) ApprovalSHA256() string { return d.approvalSHA256 }

// RequiredCapabilities returns a copy of the capability set checked by policy.
func (d CrawlRequestDecision) RequiredCapabilities() []string {
	if len(d.requiredCapabilities) == 0 {
		return nil
	}
	return append([]string(nil), d.requiredCapabilities...)
}

// RunID returns the trusted run bound into the decision.
func (d CrawlRequestDecision) RunID() string { return d.runID }

// PrincipalUserID returns the authenticated principal bound into the decision.
func (d CrawlRequestDecision) PrincipalUserID() uint64 { return d.principalUserID }

// Quota returns the trusted operator quota bound into this decision.
func (d CrawlRequestDecision) Quota() (int, time.Duration) {
	return d.maxRequests, d.budgetWindow
}

// ApprovalOutcome returns the stable approval verification outcome.
func (d CrawlRequestDecision) ApprovalOutcome() string { return d.approvalOutcome }

// SourceOwnershipOutcome returns the stable source-authorization outcome.
func (d CrawlRequestDecision) SourceOwnershipOutcome() string { return d.sourceOutcome }

// Stage returns the stable authorization stage reached by the decision.
func (d CrawlRequestDecision) Stage() string { return d.stage }

func crawlHashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// AuthorizeCrawlRequest is the internal WP-03B2A authorization boundary. It
// does not touch the database and cannot be reached from model output. It reads
// the trusted runtime clock; tests use the package-internal clock-injected
// path.
func AuthorizeCrawlRequest(ctx context.Context, identity AgentIdentity, policy CrawlRequestPolicy, proposal CrawlRequestProposal, approval *CrawlRequestApproval, runID, traceID string) CrawlRequestDecision {
	return authorizeCrawlRequestAt(ctx, identity, policy, proposal, approval, runID, traceID, time.Now().UTC())
}

func authorizeCrawlRequestAt(ctx context.Context, identity AgentIdentity, policy CrawlRequestPolicy, proposal CrawlRequestProposal, approval *CrawlRequestApproval, runID, traceID string, now time.Time) CrawlRequestDecision {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision := CrawlRequestDecision{
		reasonCode:           CrawlReasonApproved,
		requiredCapabilities: []string{"tool_execution", "db_write"},
		runID:                runID,
		approvalOutcome:      CrawlApprovalOutcomeNotRequested,
		sourceOutcome:        CrawlSourceOutcomeNotChecked,
		stage:                CrawlStageProposalReceived,
	}
	if err := ctx.Err(); err != nil {
		decision.reasonCode = crawlContextReason(err)
		return decision
	}
	if err := validateCrawlProposal(proposal, now); err != nil {
		decision.reasonCode = CrawlReasonInvalidProposal
		return decision
	}
	decision.proposalSHA256 = proposal.Fingerprint()
	if !proposal.ExpiresAt.After(now) {
		decision.reasonCode = CrawlReasonProposalExpired
		return decision
	}
	if strings.TrimSpace(identity.AgentID) == "" || strings.TrimSpace(identity.Owner) == "" {
		decision.reasonCode = CrawlReasonMissingIdentity
		return decision
	}
	if strings.TrimSpace(runID) == "" {
		decision.reasonCode = CrawlReasonRunRequired
		return decision
	}
	if err := policy.Principal.Validate(); err != nil {
		decision.reasonCode = CrawlReasonPrincipalRequired
		return decision
	}
	if policy.Principal.Username != strings.TrimSpace(identity.Owner) {
		decision.reasonCode = CrawlReasonPrincipalMismatch
		return decision
	}
	decision.principalUserID = policy.Principal.UserID
	if trustLevelRank(identity.TrustLevel) < minToolTrustRank {
		decision.reasonCode = CrawlReasonTrustRequired
		return decision
	}
	for _, capability := range decision.requiredCapabilities {
		if !identityGrants(identity, capability) {
			decision.reasonCode = CrawlReasonCapabilityRequired
			return decision
		}
	}
	// Contract prohibition overrides an allowlist entry.
	if toolForbiddenByContract(identity.Contract, crawlRequestToolName) {
		decision.reasonCode = CrawlReasonContractForbidden
		return decision
	}
	if len(policy.AllowedOperations) == 0 || !policy.AllowedOperations[crawlRequestToolName] {
		decision.reasonCode = CrawlReasonOperationNotAllowlisted
		return decision
	}
	if len(policy.AllowedSources) == 0 {
		decision.reasonCode = CrawlReasonSourceScopeRequired
		return decision
	}
	if !policy.AllowedSources[proposal.SourceID] {
		decision.reasonCode = CrawlReasonSourceOutOfScope
		return decision
	}
	if policy.MaxRequests <= 0 || policy.MaxRequests > maxCrawlPolicyRequests ||
		policy.BudgetWindow <= 0 || policy.BudgetWindow > maxCrawlPolicyBudget {
		decision.reasonCode = CrawlReasonBudgetRequired
		return decision
	}
	decision.maxRequests = policy.MaxRequests
	decision.budgetWindow = policy.BudgetWindow
	decision.stage = CrawlStagePolicyChecked

	if policy.SourceAuthorizer == nil {
		decision.reasonCode = CrawlReasonSourceAuthorizerUnavailable
		decision.sourceOutcome = CrawlSourceOutcomeUnavailable
		decision.stage = CrawlStageSourceDenied
		return decision
	}
	if err := policy.SourceAuthorizer.AuthorizeCrawlSource(ctx, policy.Principal, proposal.SourceID); err != nil {
		decision.reasonCode = crawlSourceAuthorizationReason(err)
		decision.sourceOutcome = crawlSourceAuthorizationOutcome(err)
		decision.stage = CrawlStageSourceDenied
		return decision
	}
	decision.sourceOutcome = CrawlSourceOutcomeVerified
	decision.stage = CrawlStageSourceVerified

	if approval == nil {
		decision.reasonCode = CrawlReasonApprovalRequired
		decision.approvalOutcome = CrawlApprovalOutcomeNotRequested
		decision.stage = CrawlStageApprovalRequired
		return decision
	}
	decision.approvalSHA256 = approval.Fingerprint()
	if err := validateCrawlApproval(*approval, proposal, identity, runID, now); err != nil {
		decision.reasonCode = CrawlReasonApprovalMismatch
		if errors.Is(err, ErrCrawlApprovalLifetime) {
			decision.reasonCode = CrawlReasonApprovalLifetimeExceeded
		}
		decision.approvalOutcome = CrawlApprovalOutcomeRejected
		decision.stage = CrawlStageApprovalRejected
		return decision
	}
	if !approval.ExpiresAt.After(now) {
		decision.reasonCode = CrawlReasonApprovalExpired
		decision.approvalOutcome = CrawlApprovalOutcomeExpired
		decision.stage = CrawlStageApprovalRejected
		return decision
	}
	if policy.ApprovalVerifier == nil {
		decision.reasonCode = CrawlReasonApprovalVerifierUnavailable
		decision.approvalOutcome = CrawlApprovalOutcomeUnavailable
		decision.stage = CrawlStageApprovalRejected
		return decision
	}
	verification := CrawlApprovalVerification{
		Approval:  *approval,
		Proposal:  proposal,
		Identity:  identity,
		Principal: policy.Principal,
		RunID:     runID,
		Now:       now,
	}
	if err := policy.ApprovalVerifier.VerifyCrawlApproval(ctx, verification); err != nil {
		decision.reasonCode = crawlApprovalFailureReason(err)
		decision.approvalOutcome = crawlApprovalFailureOutcome(err)
		decision.stage = CrawlStageApprovalRejected
		return decision
	}
	decision.approvalOutcome = CrawlApprovalOutcomeVerified
	decision.stage = CrawlStageAuthorized
	decision.allowed = true
	decision.verified = true
	return decision
}

func crawlContextReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return CrawlReasonAuthorizationTimeout
	}
	return CrawlReasonAuthorizationCancelled
}

func crawlSourceAuthorizationReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return CrawlReasonAuthorizationCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return CrawlReasonAuthorizationTimeout
	case errors.Is(err, ErrCrawlSourceOwnershipMissing):
		return CrawlReasonSourceOwnershipMissing
	case errors.Is(err, ErrCrawlSourceForbidden):
		return CrawlReasonSourceForbidden
	case errors.Is(err, ErrCrawlSourceAuthorizationUnavailable):
		return CrawlReasonSourceAuthorizerUnavailable
	default:
		return CrawlReasonSourceAuthorizationError
	}
}

func crawlSourceAuthorizationOutcome(err error) string {
	switch {
	case errors.Is(err, ErrCrawlSourceOwnershipMissing):
		return CrawlSourceOutcomeMissing
	case errors.Is(err, ErrCrawlSourceForbidden):
		return CrawlSourceOutcomeDenied
	case errors.Is(err, ErrCrawlSourceAuthorizationUnavailable):
		return CrawlSourceOutcomeUnavailable
	default:
		return CrawlSourceOutcomeUnavailable
	}
}

func crawlApprovalFailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return CrawlReasonAuthorizationCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return CrawlReasonAuthorizationTimeout
	case errors.Is(err, ErrCrawlApprovalRevoked):
		return CrawlReasonApprovalRevoked
	case errors.Is(err, ErrCrawlApprovalUnknown):
		return CrawlReasonApprovalUnknown
	case errors.Is(err, ErrCrawlApprovalIssuerUntrusted):
		return CrawlReasonApprovalIssuerUntrusted
	case errors.Is(err, ErrCrawlApprovalApproverUnauthorized):
		return CrawlReasonApprovalApproverUnauthorized
	case errors.Is(err, ErrCrawlApprovalStoreUnavailable),
		errors.Is(err, ErrCrawlApprovalApproverUnavailable):
		return CrawlReasonApprovalStoreUnavailable
	case errors.Is(err, ErrCrawlApprovalLifetime):
		return CrawlReasonApprovalLifetimeExceeded
	default:
		return CrawlReasonApprovalUnverified
	}
}

func crawlApprovalFailureOutcome(err error) string {
	switch {
	case errors.Is(err, ErrCrawlApprovalRevoked):
		return CrawlApprovalOutcomeRevoked
	case errors.Is(err, ErrCrawlApprovalStoreUnavailable),
		errors.Is(err, ErrCrawlApprovalApproverUnavailable):
		return CrawlApprovalOutcomeUnavailable
	default:
		return CrawlApprovalOutcomeRejected
	}
}

func validateCrawlProposal(proposal CrawlRequestProposal, now time.Time) error {
	if err := validateCrawlRequestField("request_id", proposal.RequestID); err != nil {
		return err
	}
	if proposal.SourceID == 0 {
		return fmt.Errorf("source_id is required")
	}
	if proposal.OperationType != cdb.CrawlOperationType {
		return fmt.Errorf("operation_type must be %q", cdb.CrawlOperationType)
	}
	if proposal.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	if proposal.ExpiresAt.After(now.Add(maxCrawlProposalLifetime)) {
		return fmt.Errorf("expires_at exceeds supported lifetime")
	}
	return nil
}

func validateCrawlApproval(approval CrawlRequestApproval, proposal CrawlRequestProposal, identity AgentIdentity, runID string, now time.Time) error {
	if err := validateCrawlRequestField("approval_id", approval.ApprovalID); err != nil {
		return err
	}
	if err := validateCrawlRequestField("issuer", approval.Issuer); err != nil {
		return err
	}
	if err := validateCrawlRequestField("approved_by", approval.ApprovedBy); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" || approval.RunID != runID {
		return fmt.Errorf("approval run binding mismatch")
	}
	if approval.ProposalSHA256 != proposal.Fingerprint() {
		return fmt.Errorf("approval proposal binding mismatch")
	}
	if approval.AgentID != identity.AgentID || approval.Owner != identity.Owner {
		return fmt.Errorf("approval identity binding mismatch")
	}
	if approval.SourceID != proposal.SourceID || approval.OperationType != proposal.OperationType {
		return fmt.Errorf("approval operation binding mismatch")
	}
	if approval.ApprovedAt.IsZero() || approval.ApprovedAt.After(now) ||
		approval.ApprovedAt.Before(now.Add(-maxCrawlApprovalLifetime)) {
		return fmt.Errorf("approved_at is outside the supported approval lifetime")
	}
	if approval.ExpiresAt.IsZero() || approval.ExpiresAt.After(now.Add(maxCrawlApprovalLifetime)) {
		return fmt.Errorf("approval expires_at is outside the supported lifetime")
	}
	if !approval.ExpiresAt.After(approval.ApprovedAt) ||
		approval.ExpiresAt.Sub(approval.ApprovedAt) > maxCrawlApprovalLifetime {
		return fmt.Errorf("%w: approval duration is outside the supported lifetime", ErrCrawlApprovalLifetime)
	}
	return nil
}

func validateCrawlRequestField(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxCrawlRequestIDLen {
		return fmt.Errorf("%s must be 1..%d characters", name, maxCrawlRequestIDLen)
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains an invalid character", name)
		}
	}
	return nil
}

// BuildCrawlScheduleRequest creates the durable scheduling request only from a
// verified, run-bound decision. A fabricated struct, modified decision or
// approval from another run cannot produce a submittable request. It reads the
// trusted runtime clock; tests use the package-internal clock-injected path.
func BuildCrawlScheduleRequest(identity AgentIdentity, proposal CrawlRequestProposal, approval CrawlRequestApproval, decision CrawlRequestDecision, runID string) (cdb.CrawlScheduleRequest, error) {
	return buildCrawlScheduleRequestAt(identity, proposal, approval, decision, runID, time.Now().UTC())
}

func buildCrawlScheduleRequestAt(identity AgentIdentity, proposal CrawlRequestProposal, approval CrawlRequestApproval, decision CrawlRequestDecision, runID string, now time.Time) (cdb.CrawlScheduleRequest, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !decision.allowed || !decision.verified || decision.runID != runID ||
		decision.proposalSHA256 == "" || decision.approvalSHA256 == "" ||
		decision.principalUserID == 0 ||
		decision.maxRequests <= 0 || decision.budgetWindow <= 0 ||
		decision.proposalSHA256 != proposal.Fingerprint() ||
		decision.approvalSHA256 != approval.Fingerprint() ||
		approval.ProposalSHA256 != decision.proposalSHA256 ||
		approval.RunID != runID ||
		approval.AgentID != identity.AgentID || approval.Owner != identity.Owner ||
		approval.SourceID != proposal.SourceID || approval.OperationType != proposal.OperationType {
		return cdb.CrawlScheduleRequest{}, fmt.Errorf("approved crawl request binding is invalid")
	}
	expiresAt := proposal.ExpiresAt
	if approval.ExpiresAt.Before(expiresAt) {
		expiresAt = approval.ExpiresAt
	}
	if !expiresAt.After(now) {
		return cdb.CrawlScheduleRequest{}, fmt.Errorf("approved crawl request has expired")
	}
	if !approval.ExpiresAt.After(approval.ApprovedAt) ||
		expiresAt.After(approval.ApprovedAt.Add(maxCrawlRequestLifetime)) {
		return cdb.CrawlScheduleRequest{}, fmt.Errorf("approved crawl request lifetime is invalid")
	}
	return cdb.CrawlScheduleRequest{
		RequestID: proposal.RequestID, RunID: runID,
		PrincipalUserID: decision.principalUserID,
		AgentID:         identity.AgentID, Owner: identity.Owner,
		SourceID: proposal.SourceID, AllowedSources: []uint64{proposal.SourceID},
		OperationType:    proposal.OperationType,
		ParametersSHA256: decision.proposalSHA256,
		ApprovalID:       approval.ApprovalID, ApprovalSHA256: decision.approvalSHA256,
		ApprovedBy: approval.ApprovedBy, ApprovedByUserID: approval.ApproverUserID, ApprovedAt: approval.ApprovedAt,
		ExpiresAt:   expiresAt,
		MaxRequests: decision.maxRequests, BudgetWindow: decision.budgetWindow,
	}, nil
}
