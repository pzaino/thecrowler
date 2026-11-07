package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// crawlRequestToolName is the prospective WP-03B2 tool name used only for
// contract matching and audit correlation. It is deliberately not registered
// in AgentToolRegistry and cannot be called by a model in WP-03B1.
const crawlRequestToolName = "request_source_crawl"

const maxCrawlRequestIDLen = 128

// Crawl request policy reason codes.
const (
	CrawlReasonApproved            = "crawl_request_approved"
	CrawlReasonInvalidProposal     = "crawl_proposal_invalid"
	CrawlReasonProposalExpired     = "crawl_proposal_expired"
	CrawlReasonMissingIdentity     = "crawl_missing_identity"
	CrawlReasonRunRequired         = "crawl_run_required"
	CrawlReasonTrustRequired       = "crawl_trust_required"
	CrawlReasonCapabilityRequired  = "crawl_capability_required"
	CrawlReasonContractForbidden   = "crawl_contract_forbidden"
	CrawlReasonSourceScopeRequired = "crawl_source_scope_required"
	CrawlReasonSourceOutOfScope    = "crawl_source_out_of_scope"
	CrawlReasonApprovalRequired    = "crawl_approval_required"
	CrawlReasonApprovalMismatch    = "crawl_approval_mismatch"
	CrawlReasonApprovalExpired     = "crawl_approval_expired"
	CrawlReasonRequestBuildFailed  = "crawl_request_build_failed"
)

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

// CrawlRequestApproval is an explicit trusted-operator decision bound to the
// exact proposal, agent, owner, source and operation.
type CrawlRequestApproval struct {
	ApprovalID     string
	ProposalSHA256 string
	AgentID        string
	Owner          string
	SourceID       uint64
	OperationType  string
	ApprovedBy     string
	ApprovedAt     time.Time
	ExpiresAt      time.Time
}

// Fingerprint returns a deterministic SHA-256 binding for the approval.
func (a CrawlRequestApproval) Fingerprint() string {
	return crawlHashJSON(struct {
		ApprovalID     string
		ProposalSHA256 string
		AgentID        string
		Owner          string
		SourceID       uint64
		OperationType  string
		ApprovedBy     string
		ApprovedAt     time.Time
		ExpiresAt      time.Time
	}{
		a.ApprovalID, a.ProposalSHA256, a.AgentID, a.Owner, a.SourceID,
		a.OperationType, a.ApprovedBy, a.ApprovedAt.UTC(), a.ExpiresAt.UTC(),
	})
}

// CrawlRequestDecision is the deterministic policy result for one proposal.
type CrawlRequestDecision struct {
	Allowed              bool
	ReasonCode           string
	ProposalSHA256       string
	ApprovalSHA256       string
	RequiredCapabilities []string
}

func crawlHashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// AuthorizeCrawlRequest is the internal WP-03B1 approval boundary. It does not
// touch the database and cannot be reached from model output. A future
// WP-03B2 tool may call it only after parsing a validated proposal.
func AuthorizeCrawlRequest(identity AgentIdentity, allowedSources map[uint64]bool, proposal CrawlRequestProposal, approval *CrawlRequestApproval, runID, traceID string, now time.Time) CrawlRequestDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision := CrawlRequestDecision{
		ReasonCode:           CrawlReasonApproved,
		RequiredCapabilities: []string{"tool_execution", "db_write"},
	}
	if err := validateCrawlProposal(proposal, now); err != nil {
		decision.ReasonCode = CrawlReasonInvalidProposal
		return decision
	}
	decision.ProposalSHA256 = proposal.Fingerprint()
	if proposal.ExpiresAt.Before(now) || proposal.ExpiresAt.Equal(now) {
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
	if toolForbiddenByContract(identity.Contract, crawlRequestToolName) {
		decision.ReasonCode = CrawlReasonContractForbidden
		return decision
	}
	if len(allowedSources) == 0 {
		decision.ReasonCode = CrawlReasonSourceScopeRequired
		return decision
	}
	if !allowedSources[proposal.SourceID] {
		decision.ReasonCode = CrawlReasonSourceOutOfScope
		return decision
	}
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
	decision.Allowed = true
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
	return nil
}

func validateCrawlApproval(approval CrawlRequestApproval, proposal CrawlRequestProposal, identity AgentIdentity, runID string, now time.Time) error {
	if err := validateCrawlRequestField("approval_id", approval.ApprovalID); err != nil {
		return err
	}
	if err := validateCrawlRequestField("approved_by", approval.ApprovedBy); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("run_id is required")
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
	if approval.ApprovedAt.IsZero() || approval.ApprovedAt.After(now) {
		return fmt.Errorf("approved_at must be a valid past timestamp")
	}
	if approval.ExpiresAt.IsZero() {
		return fmt.Errorf("approval expires_at is required")
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

// BuildCrawlScheduleRequest creates the durable scheduling request only from
// an allowed decision. A denied or mismatched decision cannot produce a
// submittable request.
func BuildCrawlScheduleRequest(identity AgentIdentity, proposal CrawlRequestProposal, approval CrawlRequestApproval, decision CrawlRequestDecision, now time.Time) (cdb.CrawlScheduleRequest, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !decision.Allowed ||
		decision.ProposalSHA256 == "" || decision.ApprovalSHA256 == "" ||
		decision.ProposalSHA256 != proposal.Fingerprint() ||
		decision.ApprovalSHA256 != approval.Fingerprint() ||
		approval.ProposalSHA256 != decision.ProposalSHA256 ||
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
		RequestID: proposal.RequestID, AgentID: identity.AgentID, Owner: identity.Owner,
		SourceID: proposal.SourceID, AllowedSources: []uint64{proposal.SourceID},
		OperationType:    proposal.OperationType,
		ParametersSHA256: decision.ProposalSHA256,
		ApprovalID:       approval.ApprovalID, ApprovalSHA256: decision.ApprovalSHA256,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt,
		ExpiresAt: expiresAt, Now: now,
	}, nil
}
