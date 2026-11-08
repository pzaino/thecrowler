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
// AgentToolRegistry and cannot be called by a model in WP-03B1.
const crawlRequestToolName = "request_source_crawl"

const maxCrawlRequestIDLen = 128

const (
	maxCrawlProposalLifetime = 24 * time.Hour
	maxCrawlApprovalLifetime = 24 * time.Hour
	maxCrawlPolicyRequests   = 100
	maxCrawlPolicyBudget     = 24 * time.Hour
)

// Crawl request policy reason codes.
const (
	CrawlReasonApproved                    = "crawl_request_approved"
	CrawlReasonInvalidProposal             = "crawl_proposal_invalid"
	CrawlReasonProposalExpired             = "crawl_proposal_expired"
	CrawlReasonMissingIdentity             = "crawl_missing_identity"
	CrawlReasonRunRequired                 = "crawl_run_required"
	CrawlReasonTrustRequired               = "crawl_trust_required"
	CrawlReasonCapabilityRequired          = "crawl_capability_required"
	CrawlReasonContractForbidden           = "crawl_contract_forbidden"
	CrawlReasonOperationNotAllowlisted     = "crawl_operation_not_allowlisted"
	CrawlReasonSourceScopeRequired         = "crawl_source_scope_required"
	CrawlReasonSourceOutOfScope            = "crawl_source_out_of_scope"
	CrawlReasonBudgetRequired              = "crawl_budget_required"
	CrawlReasonApprovalRequired            = "crawl_approval_required"
	CrawlReasonApprovalMismatch            = "crawl_approval_mismatch"
	CrawlReasonApprovalExpired             = "crawl_approval_expired"
	CrawlReasonApprovalVerifierUnavailable = "crawl_approval_verifier_unavailable"
	CrawlReasonApprovalUnverified          = "crawl_approval_unverified"
	CrawlReasonApprovalRevoked             = "crawl_approval_revoked"
	CrawlReasonRequestBuildFailed          = "crawl_request_build_failed"
)

var (
	// ErrCrawlApprovalUnverified means the trusted approval authority did not
	// verify the presented approval record.
	ErrCrawlApprovalUnverified = errors.New("crawl approval was not verified by a trusted authority")
	// ErrCrawlApprovalRevoked means the trusted authority revoked the approval.
	ErrCrawlApprovalRevoked = errors.New("crawl approval was revoked")
)

// CrawlApprovalVerifier is the narrow trusted-authority boundary. A nil
// verifier fails closed. Implementations may consult an operator-controlled
// approval registry or signing service; WP-03B1 does not invent a durable
// approval database or UI.
type CrawlApprovalVerifier interface {
	VerifyCrawlApproval(ctx context.Context, approval CrawlRequestApproval, proposal CrawlRequestProposal, identity AgentIdentity, runID string, now time.Time) error
}

// CrawlRequestPolicy is trusted run configuration. It is never populated from
// model arguments.
type CrawlRequestPolicy struct {
	AllowedOperations map[string]bool
	AllowedSources    map[uint64]bool
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
// CrawlApprovalVerifier must accept it.
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
		ApprovedAt     time.Time
		ExpiresAt      time.Time
	}{
		a.ApprovalID, a.ProposalSHA256, a.RunID, a.AgentID, a.Owner,
		a.SourceID, a.OperationType, a.Issuer, a.ApprovedBy,
		a.ApprovedAt.UTC(), a.ExpiresAt.UTC(),
	})
}

// CrawlRequestDecision is the deterministic policy result for one proposal.
// verified is unexported so an external caller cannot fabricate an allowed
// decision; only AuthorizeCrawlRequest with a trusted verifier sets it.
type CrawlRequestDecision struct {
	Allowed              bool
	ReasonCode           string
	ProposalSHA256       string
	ApprovalSHA256       string
	RequiredCapabilities []string
	RunID                string
	maxRequests          int
	budgetWindow         time.Duration
	verified             bool
}

// Quota returns the trusted operator quota bound into this decision. The
// fields are unexported so an external caller cannot widen the quota after
// authorization.
func (d CrawlRequestDecision) Quota() (int, time.Duration) {
	return d.maxRequests, d.budgetWindow
}

func crawlHashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// AuthorizeCrawlRequest is the internal WP-03B1 approval boundary. It does not
// touch the database and cannot be reached from model output. It reads the
// trusted runtime clock; tests use the package-internal clock-injected path.
func AuthorizeCrawlRequest(ctx context.Context, identity AgentIdentity, policy CrawlRequestPolicy, proposal CrawlRequestProposal, approval *CrawlRequestApproval, runID, traceID string) CrawlRequestDecision {
	return authorizeCrawlRequestAt(ctx, identity, policy, proposal, approval, runID, traceID, time.Now().UTC())
}

func authorizeCrawlRequestAt(ctx context.Context, identity AgentIdentity, policy CrawlRequestPolicy, proposal CrawlRequestProposal, approval *CrawlRequestApproval, runID, traceID string, now time.Time) CrawlRequestDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision := CrawlRequestDecision{
		ReasonCode:           CrawlReasonApproved,
		RequiredCapabilities: []string{"tool_execution", "db_write"},
		RunID:                runID,
	}
	if err := validateCrawlProposal(proposal, now); err != nil {
		decision.ReasonCode = CrawlReasonInvalidProposal
		return decision
	}
	decision.ProposalSHA256 = proposal.Fingerprint()
	if !proposal.ExpiresAt.After(now) {
		decision.ReasonCode = CrawlReasonProposalExpired
		return decision
	}
	if strings.TrimSpace(identity.AgentID) == "" || strings.TrimSpace(identity.Owner) == "" {
		decision.ReasonCode = CrawlReasonMissingIdentity
		return decision
	}
	if strings.TrimSpace(runID) == "" {
		decision.ReasonCode = CrawlReasonRunRequired
		return decision
	}
	if trustLevelRank(identity.TrustLevel) < minToolTrustRank {
		decision.ReasonCode = CrawlReasonTrustRequired
		return decision
	}
	for _, capability := range decision.RequiredCapabilities {
		if !identityGrants(identity, capability) {
			decision.ReasonCode = CrawlReasonCapabilityRequired
			return decision
		}
	}
	// Contract prohibition overrides an allowlist entry.
	if toolForbiddenByContract(identity.Contract, crawlRequestToolName) {
		decision.ReasonCode = CrawlReasonContractForbidden
		return decision
	}
	if len(policy.AllowedOperations) == 0 || !policy.AllowedOperations[crawlRequestToolName] {
		decision.ReasonCode = CrawlReasonOperationNotAllowlisted
		return decision
	}
	if len(policy.AllowedSources) == 0 {
		decision.ReasonCode = CrawlReasonSourceScopeRequired
		return decision
	}
	if !policy.AllowedSources[proposal.SourceID] {
		decision.ReasonCode = CrawlReasonSourceOutOfScope
		return decision
	}
	if policy.MaxRequests <= 0 || policy.MaxRequests > maxCrawlPolicyRequests ||
		policy.BudgetWindow <= 0 || policy.BudgetWindow > maxCrawlPolicyBudget {
		decision.ReasonCode = CrawlReasonBudgetRequired
		return decision
	}
	decision.maxRequests = policy.MaxRequests
	decision.budgetWindow = policy.BudgetWindow
	if approval == nil {
		decision.ReasonCode = CrawlReasonApprovalRequired
		return decision
	}
	decision.ApprovalSHA256 = approval.Fingerprint()
	if err := validateCrawlApproval(*approval, proposal, identity, runID, now); err != nil {
		decision.ReasonCode = CrawlReasonApprovalMismatch
		return decision
	}
	if !approval.ExpiresAt.After(now) {
		decision.ReasonCode = CrawlReasonApprovalExpired
		return decision
	}
	if policy.ApprovalVerifier == nil {
		decision.ReasonCode = CrawlReasonApprovalVerifierUnavailable
		return decision
	}
	if err := policy.ApprovalVerifier.VerifyCrawlApproval(ctx, *approval, proposal, identity, runID, now); err != nil {
		switch {
		case errors.Is(err, ErrCrawlApprovalRevoked):
			decision.ReasonCode = CrawlReasonApprovalRevoked
		default:
			decision.ReasonCode = CrawlReasonApprovalUnverified
		}
		return decision
	}
	decision.Allowed = true
	decision.verified = true
	return decision
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
	if !decision.Allowed || !decision.verified || decision.RunID != runID ||
		decision.ProposalSHA256 == "" || decision.ApprovalSHA256 == "" ||
		decision.maxRequests <= 0 || decision.budgetWindow <= 0 ||
		decision.ProposalSHA256 != proposal.Fingerprint() ||
		decision.ApprovalSHA256 != approval.Fingerprint() ||
		approval.ProposalSHA256 != decision.ProposalSHA256 ||
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
	return cdb.CrawlScheduleRequest{
		RequestID: proposal.RequestID, RunID: runID,
		AgentID: identity.AgentID, Owner: identity.Owner,
		SourceID: proposal.SourceID, AllowedSources: []uint64{proposal.SourceID},
		OperationType:    proposal.OperationType,
		ParametersSHA256: decision.ProposalSHA256,
		ApprovalID:       approval.ApprovalID, ApprovalSHA256: decision.ApprovalSHA256,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt,
		ExpiresAt:   expiresAt,
		MaxRequests: decision.maxRequests, BudgetWindow: decision.budgetWindow,
	}, nil
}
