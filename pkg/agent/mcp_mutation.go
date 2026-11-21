package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// WP-04C1 is proposal-only. A mutation descriptor can produce an immutable
// inert proposal; it cannot execute an MCP tools/call for the mutation.
type MCPMutationProposalState string

const (
	// MCPMutationIdentityVersion identifies the canonicalization and binding
	// contract stored with durable proposals.
	MCPMutationIdentityVersion = 1

	MCPProposalProposed               MCPMutationProposalState = "proposed"
	MCPProposalApprovalRequired       MCPMutationProposalState = "approval_required"
	MCPProposalApproved               MCPMutationProposalState = "approved"
	MCPProposalDenied                 MCPMutationProposalState = "denied"
	MCPProposalExpired                MCPMutationProposalState = "expired"
	MCPProposalDispatching            MCPMutationProposalState = "dispatching"
	MCPProposalSucceeded              MCPMutationProposalState = "succeeded"
	MCPProposalFailed                 MCPMutationProposalState = "failed"
	MCPProposalUncertain              MCPMutationProposalState = "uncertain"
	MCPProposalReconciliationRequired MCPMutationProposalState = "reconciliation_required"
)

var (
	ErrMCPMutationDisabled         = errors.New("MCP mutation proposals are disabled")
	ErrMCPMutationPolicyInvalid    = errors.New("MCP mutation policy is invalid")
	ErrMCPMutationNotApproved      = errors.New("MCP mutation tool is not approved")
	ErrMCPMutationArguments        = errors.New("MCP mutation arguments are invalid")
	ErrMCPMutationRunIdentity      = errors.New("trusted run identity is missing")
	ErrMCPMutationPrincipal        = errors.New("trusted mutation principal is required")
	ErrMCPMutationStoreUnavailable = errors.New("MCP mutation proposal store is unavailable")
	ErrMCPMutationProposalConflict = errors.New("MCP mutation proposal conflicts with an existing logical proposal")
	ErrMCPMutationQuota            = errors.New("MCP mutation proposal quota reached")
	ErrMCPMutationProposalExpired  = errors.New("MCP mutation proposal has expired")
	ErrMCPMutationProposalCorrupt  = errors.New("MCP mutation proposal record is corrupt")
	ErrMCPMutationApprovalCorrupt  = errors.New("MCP mutation approval record is corrupt")
	ErrMCPMutationApproval         = errors.New("MCP mutation approval is invalid")
)

// MCPMutationPrincipal is an authenticated operator identity supplied by the
// trusted runtime. It is never populated from model arguments.
type MCPMutationPrincipal struct {
	UserID   uint64
	Username string
	Issuer   string
}

func (p MCPMutationPrincipal) Validate() error {
	if p.UserID == 0 || strings.TrimSpace(p.Username) == "" || strings.TrimSpace(p.Issuer) == "" {
		return ErrMCPMutationPrincipal
	}
	return nil
}

// MCPMutationOperationPolicy binds one exact server/tool pair to proposal
// authority. It intentionally does not grant execution authority.
type MCPMutationOperationPolicy struct {
	ServerID               string
	ToolName               string
	ToolAlias              string
	AllowedAgents          []string
	RequiredCapabilities   []string
	MinTrustLevel          string
	AllowedSources         []uint64
	AllowedResources       []string
	RequiredArgumentFields []string
	AllowedArgumentFields  []string
	Destructive            bool
	ProposalTTL            time.Duration
	MaxProposalsPerRun     int
	MaxPendingProposals    int
	MaxInputBytes          int
	MaxOutputBytes         int
	RequireApproval        bool
	ExecutionBackend       string
	AllowedTransports      []string
	AuditRequired          bool
}

// MCPMutationPolicy is trusted operator/configuration policy. It remains a Go
// contract; it is not exposed as an agent manifest field because agent-facing
// mutation proposal creation has not been approved.
type MCPMutationPolicy struct {
	Enabled    bool
	PolicyID   string
	Version    string
	Operations []MCPMutationOperationPolicy
}

func (p *MCPMutationPolicy) Operation(alias, serverID, toolName string) (MCPMutationOperationPolicy, bool) {
	if p == nil || !p.Enabled {
		return MCPMutationOperationPolicy{}, false
	}
	for _, operation := range p.Operations {
		if operation.ToolAlias == alias && operation.ServerID == serverID && operation.ToolName == toolName {
			return operation, true
		}
	}
	return MCPMutationOperationPolicy{}, false
}

// OperationByAlias reports whether the policy contains one exact derived
// alias. It is used only for fail-closed background-origin checks.
func (p *MCPMutationPolicy) OperationByAlias(alias string) (MCPMutationOperationPolicy, bool) {
	if p == nil || !p.Enabled {
		return MCPMutationOperationPolicy{}, false
	}
	for _, operation := range p.Operations {
		if operation.ToolAlias == alias {
			return operation, true
		}
	}
	return MCPMutationOperationPolicy{}, false
}

func (p *MCPMutationPolicy) Validate() error {
	if p == nil || !p.Enabled {
		return ErrMCPMutationDisabled
	}
	if strings.TrimSpace(p.PolicyID) == "" || strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("%w: policy_id and version are required", ErrMCPMutationPolicyInvalid)
	}
	if len(p.Operations) == 0 || len(p.Operations) > cfg.MCPMutationMaxOperations {
		return fmt.Errorf("%w: operations must contain 1..%d entries", ErrMCPMutationPolicyInvalid, cfg.MCPMutationMaxOperations)
	}
	seen := map[string]bool{}
	for index, operation := range p.Operations {
		if err := validateMCPMutationOperation(operation); err != nil {
			return fmt.Errorf("%w: operations[%d]: %v", ErrMCPMutationPolicyInvalid, index, err)
		}
		if seen[operation.ToolAlias] {
			return fmt.Errorf("%w: duplicate operation alias %q", ErrMCPMutationPolicyInvalid, operation.ToolAlias)
		}
		seen[operation.ToolAlias] = true
	}
	return nil
}

func validateMCPMutationOperation(operation MCPMutationOperationPolicy) error {
	if strings.TrimSpace(operation.ServerID) == "" || strings.TrimSpace(operation.ToolName) == "" ||
		strings.TrimSpace(operation.ToolAlias) == "" {
		return fmt.Errorf("server_id, tool_name and tool_alias are required")
	}
	// Reuse the production server/tool grammar rather than maintaining a
	// second, weaker mutation-policy grammar. An operation that the operator
	// inventory cannot name can never be authorized.
	if !cfg.ValidMCPServerID(operation.ServerID) {
		return fmt.Errorf("server_id must match %s", cfg.MCPServerIDGrammar)
	}
	if !cfg.ValidMCPToolName(operation.ToolName) {
		return fmt.Errorf("tool_name is invalid")
	}
	if MCPToolAlias(operation.ServerID, operation.ToolName) != operation.ToolAlias {
		return fmt.Errorf("tool_alias does not match the exact server/tool identity")
	}
	if len(operation.AllowedAgents) == 0 || len(operation.AllowedAgents) > cfg.MCPMutationMaxAgents {
		return fmt.Errorf("allowed_agents must contain 1..%d entries", cfg.MCPMutationMaxAgents)
	}
	if len(operation.RequiredCapabilities) == 0 || len(operation.RequiredCapabilities) > cfg.MCPMutationMaxCapabilities ||
		!containsString(operation.RequiredCapabilities, "tool_execution") {
		return fmt.Errorf("required_capabilities must contain 1..%d entries including tool_execution", cfg.MCPMutationMaxCapabilities)
	}
	switch operation.MinTrustLevel {
	case "trusted", "medium", "privileged", "high", "internal", "system":
	default:
		return fmt.Errorf("min_trust_level must be a trusted-or-higher level")
	}
	if len(operation.AllowedSources) == 0 && len(operation.AllowedResources) == 0 {
		return fmt.Errorf("exactly one of allowed_sources or allowed_resources must be configured")
	}
	if len(operation.AllowedSources) > 0 && len(operation.AllowedResources) > 0 {
		return fmt.Errorf("allowed_sources and allowed_resources are mutually exclusive")
	}
	if len(operation.AllowedSources) > cfg.MCPMutationMaxSources {
		return fmt.Errorf("allowed_sources must contain at most %d entries", cfg.MCPMutationMaxSources)
	}
	if len(operation.AllowedResources) > cfg.MCPMutationMaxResources {
		return fmt.Errorf("allowed_resources must contain at most %d entries", cfg.MCPMutationMaxResources)
	}
	for _, sourceID := range operation.AllowedSources {
		if sourceID == 0 {
			return fmt.Errorf("allowed_sources contains zero")
		}
	}
	for _, resource := range operation.AllowedResources {
		if strings.TrimSpace(resource) == "" || len(resource) > cfg.MCPMaxResourceBytes || containsMCPControl(resource) {
			return fmt.Errorf("allowed_resources contains an invalid value")
		}
	}
	if len(operation.RequiredArgumentFields) > cfg.MCPMutationMaxArgumentFields ||
		len(operation.AllowedArgumentFields) > cfg.MCPMutationMaxArgumentFields {
		return fmt.Errorf("argument field lists must contain at most %d entries", cfg.MCPMutationMaxArgumentFields)
	}
	if operation.ProposalTTL <= 0 || operation.ProposalTTL > time.Hour {
		return fmt.Errorf("proposal_ttl must be within 1s..1h")
	}
	if operation.MaxProposalsPerRun <= 0 || operation.MaxProposalsPerRun > cfg.MCPMutationMaxProposalsPerRun {
		return fmt.Errorf("max_proposals_per_run must be 1..%d", cfg.MCPMutationMaxProposalsPerRun)
	}
	if operation.MaxPendingProposals <= 0 || operation.MaxPendingProposals > cfg.MCPMutationMaxPendingProposals {
		return fmt.Errorf("max_pending_proposals must be 1..%d", cfg.MCPMutationMaxPendingProposals)
	}
	if operation.MaxInputBytes <= 0 || operation.MaxInputBytes > cfg.MCPMutationMaxInputBytes {
		return fmt.Errorf("max_input_bytes must be 1..%d", cfg.MCPMutationMaxInputBytes)
	}
	if operation.MaxOutputBytes <= 0 || operation.MaxOutputBytes > cfg.MCPMutationMaxOutputBytes {
		return fmt.Errorf("max_output_bytes must be 1..%d", cfg.MCPMutationMaxOutputBytes)
	}
	if !operation.RequireApproval {
		return fmt.Errorf("require_approval must be true in WP-04C1")
	}
	if !operation.AuditRequired {
		return fmt.Errorf("audit_required must be true in WP-04C1")
	}
	if operation.ExecutionBackend != "" && operation.ExecutionBackend != "none" {
		return fmt.Errorf("execution_backend is not available in WP-04C1")
	}
	if len(operation.AllowedTransports) == 0 {
		return fmt.Errorf("allowed_transports must not be empty")
	}
	for _, transport := range operation.AllowedTransports {
		if transport != mcpTransportStdio && transport != mcpTransportStreamableHTTP {
			return fmt.Errorf("unsupported allowed transport %q", transport)
		}
	}
	return nil
}

func (p *MCPMutationPolicy) SHA256() string {
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// MCPMutationProposal is immutable trusted proposal state. It is not evidence
// that any external operation occurred.
type MCPMutationProposal struct {
	ProposalID         string
	IdentityVersion    int
	State              MCPMutationProposalState
	RunID              string
	TraceID            string
	EffectiveCallID    string
	AgentID            string
	Owner              string
	Principal          MCPMutationPrincipal
	ServerID           string
	RawToolName        string
	ToolAlias          string
	Transport          string
	TransportIdentity  string
	SchemaSHA256       string
	Arguments          map[string]any
	CanonicalArguments []byte
	ArgumentsSHA256    string
	ProposalSHA256     string
	PolicyID           string
	PolicyVersion      string
	PolicySHA256       string
	PolicySnapshot     []byte
	SourceIDs          []uint64
	ExternalResource   string
	Destructive        bool
	CreatedAt          time.Time
	LastUpdatedAt      time.Time
	ExpiresAt          time.Time
	ApprovedAt         time.Time
	TerminalAt         time.Time
	TerminalReason     string
	ApprovalRequired   bool
	ExecutionBackend   string
	AuditRequired      bool
}

// MCPMutationProposalStore is the narrow persistence boundary. The in-memory
// implementation is test-only; DatabaseMCPMutationProposalStore provides the
// additive schema-1.17 durable implementation without enabling dispatch.
type MCPMutationProposalStore interface {
	// Admit performs fingerprint lookup, run-call conflict detection, quota
	// checks and immutable creation as one atomic operation.
	Admit(ctx context.Context, proposal MCPMutationProposal, maxPerRun, maxPendingPerPrincipal int) (MCPMutationProposal, error)
	Get(ctx context.Context, proposalID string) (MCPMutationProposal, bool, error)
}

type InMemoryMCPMutationProposalStore struct {
	mu        sync.Mutex
	byID      map[string]MCPMutationProposal
	byFinger  map[string]string
	byRunCall map[string]string
	Now       func() time.Time
}

func NewInMemoryMCPMutationProposalStore() *InMemoryMCPMutationProposalStore {
	return &InMemoryMCPMutationProposalStore{
		byID: map[string]MCPMutationProposal{}, byFinger: map[string]string{},
		byRunCall: map[string]string{},
	}
}

// admitNow is test-seam friendly: tests may set Now.
func (s *InMemoryMCPMutationProposalStore) admitNow() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func mcpProposalConsumesQuota(state MCPMutationProposalState) bool {
	switch state {
	case MCPProposalProposed, MCPProposalApprovalRequired, MCPProposalApproved,
		MCPProposalDispatching, MCPProposalUncertain, MCPProposalReconciliationRequired:
		return true
	default:
		return false
	}
}

func (s *InMemoryMCPMutationProposalStore) Admit(ctx context.Context, proposal MCPMutationProposal, maxPerRun, maxPendingPerPrincipal int) (MCPMutationProposal, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return MCPMutationProposal{}, err
		}
	}
	if s == nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	// Take ownership of the authoritative bytes before hashing or storing them
	// so a caller cannot change the dispatch identity after admission.
	proposal.CanonicalArguments = append([]byte(nil), proposal.CanonicalArguments...)
	proposal.PolicySnapshot = append([]byte(nil), proposal.PolicySnapshot...)
	proposal.SourceIDs = normalizeMCPSourceIDs(proposal.SourceIDs)
	if err := validateMCPMutationProposalForAdmission(proposal); err != nil {
		return MCPMutationProposal{}, err
	}
	if maxPerRun <= 0 || maxPendingPerPrincipal <= 0 {
		return MCPMutationProposal{}, ErrMCPMutationQuota
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.admitNow()
	if !proposal.ExpiresAt.After(now) {
		return MCPMutationProposal{}, ErrMCPMutationProposalExpired
	}
	// Match the SQL admission sweep: expire records for the exact run and
	// principal before quota counting. Other expired records stay strict until
	// their own admission or an explicit transition.
	for id, existing := range s.byID {
		if existing.RunID != proposal.RunID || existing.Principal.UserID != proposal.Principal.UserID {
			continue
		}
		if !existing.ExpiresAt.After(now) && mcpProposalConsumesQuota(existing.State) {
			existing.State = MCPProposalExpired
			existing.TerminalAt = now
			existing.TerminalReason = "expired"
			existing.LastUpdatedAt = now
			s.byID[id] = existing
		}
	}
	runCallKey := proposal.RunID + "\x00" + proposal.EffectiveCallID
	if existingID, ok := s.byFinger[proposal.ProposalSHA256]; ok {
		existing := s.byID[existingID]
		if !existing.ExpiresAt.After(now) {
			return MCPMutationProposal{}, ErrMCPMutationProposalExpired
		}
		return cloneMCPMutationProposal(existing), nil
	}
	if existingID, ok := s.byRunCall[runCallKey]; ok {
		existing := s.byID[existingID]
		if existing.ProposalSHA256 != proposal.ProposalSHA256 {
			return MCPMutationProposal{}, ErrMCPMutationProposalConflict
		}
		if !existing.ExpiresAt.After(now) {
			return MCPMutationProposal{}, ErrMCPMutationProposalExpired
		}
		return cloneMCPMutationProposal(existing), nil
	}
	runCount := 0
	pendingCount := 0
	for _, existing := range s.byID {
		if !mcpProposalConsumesQuota(existing.State) {
			continue
		}
		if existing.RunID == proposal.RunID {
			runCount++
		}
		if proposal.Principal.UserID != 0 && existing.Principal.UserID == proposal.Principal.UserID &&
			(existing.State == MCPProposalProposed || existing.State == MCPProposalApprovalRequired ||
				existing.State == MCPProposalApproved || existing.State == MCPProposalDispatching) {
			pendingCount++
		}
	}
	if runCount >= maxPerRun {
		return MCPMutationProposal{}, ErrMCPMutationQuota
	}
	if pendingCount >= maxPendingPerPrincipal {
		return MCPMutationProposal{}, ErrMCPMutationQuota
	}
	if existing, ok := s.byID[proposal.ProposalID]; ok && existing.ProposalSHA256 != proposal.ProposalSHA256 {
		return MCPMutationProposal{}, ErrMCPMutationProposalConflict
	}
	s.byID[proposal.ProposalID] = cloneMCPMutationProposal(proposal)
	s.byFinger[proposal.ProposalSHA256] = proposal.ProposalID
	s.byRunCall[runCallKey] = proposal.ProposalID
	return cloneMCPMutationProposal(proposal), nil
}

func validateMCPMutationProposalForAdmission(proposal MCPMutationProposal) error {
	if proposal.IdentityVersion <= 0 {
		return ErrMCPMutationProposalConflict
	}
	if proposal.ProposalID == "" || proposal.ProposalSHA256 == "" ||
		proposal.RunID == "" || proposal.EffectiveCallID == "" ||
		proposal.AgentID == "" || proposal.Owner == "" ||
		proposal.ServerID == "" || proposal.ToolAlias == "" {
		return ErrMCPMutationProposalConflict
	}
	if proposal.ProposalSHA256 != proposalFingerprint(proposal) {
		return fmt.Errorf("%w: proposal fingerprint does not match its content", ErrMCPMutationProposalConflict)
	}
	if proposal.State != MCPProposalApprovalRequired && proposal.State != MCPProposalProposed {
		return ErrMCPMutationProposalConflict
	}
	if proposal.ApprovalRequired && proposal.Principal.Validate() != nil {
		return ErrMCPMutationPrincipal
	}
	if len(proposal.CanonicalArguments) == 0 || proposal.ArgumentsSHA256 == "" ||
		sha256Hex(proposal.CanonicalArguments) != proposal.ArgumentsSHA256 {
		return fmt.Errorf("%w: canonical arguments do not match their fingerprint", ErrMCPMutationArguments)
	}
	if len(proposal.PolicySnapshot) > 0 && sha256Hex(proposal.PolicySnapshot) != proposal.PolicySHA256 {
		return fmt.Errorf("%w: policy snapshot does not match its hash", ErrMCPMutationPolicyInvalid)
	}
	return nil
}

func (s *InMemoryMCPMutationProposalStore) GetByFingerprint(ctx context.Context, fingerprint string) (MCPMutationProposal, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return MCPMutationProposal{}, false, err
		}
	}
	if s == nil {
		return MCPMutationProposal{}, false, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byFinger[fingerprint]
	if !ok {
		return MCPMutationProposal{}, false, nil
	}
	return cloneMCPMutationProposal(s.byID[id]), true, nil
}

func (s *InMemoryMCPMutationProposalStore) CountByRun(ctx context.Context, runID string) (int, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if s == nil {
		return 0, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, proposal := range s.byID {
		if proposal.RunID == runID {
			count++
		}
	}
	return count, nil
}

func (s *InMemoryMCPMutationProposalStore) CountPendingByPrincipal(ctx context.Context, userID uint64) (int, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if s == nil {
		return 0, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, proposal := range s.byID {
		if proposal.Principal.UserID == userID && proposal.State == MCPProposalApprovalRequired {
			count++
		}
	}
	return count, nil
}

func (s *InMemoryMCPMutationProposalStore) Get(ctx context.Context, proposalID string) (MCPMutationProposal, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return MCPMutationProposal{}, false, err
		}
	}
	if s == nil {
		return MCPMutationProposal{}, false, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.byID[proposalID]
	return cloneMCPMutationProposal(proposal), ok, nil
}

func cloneMCPMutationProposal(p MCPMutationProposal) MCPMutationProposal {
	p.Arguments = cloneAnyMap(p.Arguments)
	p.CanonicalArguments = append([]byte(nil), p.CanonicalArguments...)
	p.PolicySnapshot = append([]byte(nil), p.PolicySnapshot...)
	p.SourceIDs = append([]uint64(nil), p.SourceIDs...)
	return p
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = deepCloneStepValue(value)
	}
	return out
}

// MCPMutationProposalTool is a mutation-classified AgentTool that can only
// create an inert proposal. Its Execute method never contacts the MCP server.
type MCPMutationProposalTool struct {
	descriptor *mcpMutationTool
	proposer   *MCPMutationProposer
}

func NewMCPMutationProposalTool(manager *MCPToolManager, policy MCPMutationPolicy, store MCPMutationProposalStore) (*MCPMutationProposalTool, error) {
	if manager == nil || store == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	var matches []*mcpMutationTool
	for _, candidate := range manager.mutations {
		if _, ok := policy.Operation(candidate.alias, candidate.server.config.ID, candidate.approved.Name); ok {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: no policy operation matches a frozen mutation descriptor", ErrMCPMutationNotApproved)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: multiple policy operations match frozen descriptors; use the exact alias constructor", ErrMCPMutationPolicyInvalid)
	}
	return NewMCPMutationProposalToolForOperation(manager, policy, store, matches[0].alias)
}

// NewMCPMutationProposalToolForOperation binds one exact policy alias to one
// frozen mutation descriptor. It never selects the first matching operation.
func NewMCPMutationProposalToolForOperation(manager *MCPToolManager, policy MCPMutationPolicy, store MCPMutationProposalStore, alias string) (*MCPMutationProposalTool, error) {
	if manager == nil || store == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if alias == "" || alias != strings.TrimSpace(alias) {
		return nil, fmt.Errorf("%w: exact MCP tool alias is required", ErrMCPMutationPolicyInvalid)
	}
	descriptor, ok := manager.MutationTool(alias)
	if !ok || descriptor == nil {
		return nil, fmt.Errorf("%w: frozen mutation descriptor %q is unavailable", ErrMCPMutationNotApproved, alias)
	}
	operation, ok := policy.Operation(alias, descriptor.server.config.ID, descriptor.approved.Name)
	if !ok {
		return nil, fmt.Errorf("%w: policy operation %q does not match the frozen descriptor", ErrMCPMutationNotApproved, alias)
	}
	if !containsString(operation.AllowedTransports, descriptor.server.config.Transport) {
		return nil, fmt.Errorf("%w: MCP transport is not approved for %q", ErrMCPMutationNotApproved, alias)
	}
	if operation.ExecutionBackend != "" && operation.ExecutionBackend != "none" {
		return nil, fmt.Errorf("%w: MCP execution backend is unavailable", ErrMCPMutationDisabled)
	}
	return &MCPMutationProposalTool{
		descriptor: descriptor,
		proposer:   &MCPMutationProposer{Manager: manager, Policy: policy, Store: store},
	}, nil
}

func (t *MCPMutationProposalTool) Name() string        { return t.descriptor.alias }
func (t *MCPMutationProposalTool) Description() string { return t.descriptor.description }
func (t *MCPMutationProposalTool) InputSchema() map[string]any {
	return t.descriptor.InputSchema()
}
func (t *MCPMutationProposalTool) RequiredCapabilities() []string {
	return t.descriptor.RequiredCapabilities()
}
func (t *MCPMutationProposalTool) Effect() ToolEffect { return ToolEffectMutation }

// IsMCPMutationProposal marks the adapter for the independent MCP authority
// branch in AuthorizeToolCall. It does not grant native crawl mutation rights.
func (t *MCPMutationProposalTool) IsMCPMutationProposal() bool { return true }

// MCPMutationAuditIdentity exposes only the frozen descriptor identity needed
// for a denied-admission audit. It never exposes arguments or transport data.
func (t *MCPMutationProposalTool) MCPMutationAuditIdentity() MCPMutationAuditIdentity {
	if t == nil || t.descriptor == nil || t.descriptor.server == nil {
		return MCPMutationAuditIdentity{}
	}
	policyID := ""
	if t.proposer != nil {
		policyID = t.proposer.Policy.PolicyID
	}
	return MCPMutationAuditIdentity{
		ServerID:  t.descriptor.server.config.ID,
		ToolAlias: t.descriptor.alias,
		PolicyID:  policyID,
	}
}

func (t *MCPMutationProposalTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	if t == nil || t.descriptor == nil || t.proposer == nil {
		return nil, ErrMCPMutationDisabled
	}
	proposal, err := t.proposer.Propose(ctx, t, t.descriptor, args)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":            string(proposal.State),
		"approval_required": proposal.ApprovalRequired,
		"proposal_id":       proposal.ProposalID,
		"proposal_sha256":   proposal.ProposalSHA256,
		"arguments_sha256":  proposal.ArgumentsSHA256,
		"server_id":         proposal.ServerID,
		"tool":              proposal.RawToolName,
		"tool_alias":        proposal.ToolAlias,
		"expires_at":        proposal.ExpiresAt,
	}, nil
}

// MCPMutationProposer validates and records an inert proposal. It never calls
// tools/call on the MCP transport.
type MCPMutationProposer struct {
	Manager *MCPToolManager
	Policy  MCPMutationPolicy
	Store   MCPMutationProposalStore
	Now     func() time.Time
}

func (p *MCPMutationProposer) Propose(ctx context.Context, tool AgentTool, descriptor *mcpMutationTool, args map[string]any) (proposal MCPMutationProposal, err error) {
	var runtime ToolRuntime
	haveRuntime := false
	defer func() {
		if err == nil || descriptor == nil {
			return
		}
		event := MCPAdmissionDeniedEvent{
			EventType:  MCPAuditEventAdmissionDenied,
			Stage:      MCPAuditStageProposal,
			ReasonCode: mcpAdmissionReasonCode(err),
			ServerID:   mcpMutationDescriptorServerID(descriptor),
			ToolAlias:  mcpMutationDescriptorAlias(descriptor),
			PolicyID:   p.Policy.PolicyID,
		}
		if haveRuntime {
			event.RunID = runtime.Auth.RunID
			event.TraceID = runtime.Auth.TraceID
			event.CallID = runtime.CallID
			event.Identity = runtime.Auth.Identity
			event.Principal = runtime.Principal
		}
		recordMCPAdmissionDenied(ctx, p.Store, event)
	}()
	if err := p.Policy.Validate(); err != nil {
		return MCPMutationProposal{}, err
	}
	if p.Store == nil || descriptor == nil || descriptor.server == nil || descriptor.manager == nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || !runtime.Auth.Enforcement || strings.TrimSpace(runtime.Auth.RunID) == "" ||
		strings.TrimSpace(runtime.Auth.Identity.AgentID) == "" {
		return MCPMutationProposal{}, ErrMCPMutationRunIdentity
	}
	haveRuntime = true
	if err := AuthorizeToolCall(runtime.Auth, tool, args); err != nil {
		return MCPMutationProposal{}, err
	}
	operation, ok := p.Policy.Operation(descriptor.alias, descriptor.server.config.ID, descriptor.approved.Name)
	if !ok {
		return MCPMutationProposal{}, ErrMCPMutationNotApproved
	}
	if !containsString(operation.AllowedAgents, runtime.Auth.Identity.AgentID) {
		return MCPMutationProposal{}, denyTool(ToolDenyNotAllowlisted, "agent %q is not approved for this MCP mutation", runtime.Auth.Identity.AgentID)
	}
	for _, capability := range operation.RequiredCapabilities {
		if !identityGrants(runtime.Auth.Identity, capability) {
			return MCPMutationProposal{}, denyTool(ToolDenyCapability, "agent lacks approved mutation capability %q", capability)
		}
	}
	if trustLevelRank(runtime.Auth.Identity.TrustLevel) < trustLevelRank(operation.MinTrustLevel) {
		return MCPMutationProposal{}, denyTool(ToolDenyTrust, "agent trust level is below the approved mutation policy")
	}
	if !containsString(operation.AllowedTransports, descriptor.server.config.Transport) {
		return MCPMutationProposal{}, denyTool(ToolDenyEffect, "MCP transport is not approved for this mutation")
	}
	if operation.ExecutionBackend != "" && operation.ExecutionBackend != "none" {
		return MCPMutationProposal{}, fmt.Errorf("%w: execution backend is not available", ErrMCPMutationDisabled)
	}
	if err := validateMCPMutationArguments(operation, args); err != nil {
		return MCPMutationProposal{}, err
	}
	canonical, canonicalArgs, err := canonicalMCPArguments(args, operation.MaxInputBytes)
	if err != nil {
		return MCPMutationProposal{}, err
	}
	sourceIDs, resource, err := mcpMutationScope(operation, args)
	if err != nil {
		return MCPMutationProposal{}, err
	}
	sourceIDs = normalizeMCPSourceIDs(sourceIDs)
	if len(operation.AllowedSources) > 0 {
		approvedScope := effectiveMCPSources(descriptor.server.config, descriptor.approved)
		for _, sourceID := range sourceIDs {
			if err := CheckSourceScope(runtime.Auth.AllowedSources, sourceID); err != nil {
				return MCPMutationProposal{}, err
			}
			if len(approvedScope) > 0 && !approvedScope[sourceID] {
				return MCPMutationProposal{}, denyTool(ToolDenyScope, "source %d is outside the approved mutation tool scope", sourceID)
			}
		}
	}
	if operation.RequireApproval && runtime.Principal.Validate() != nil {
		return MCPMutationProposal{}, ErrMCPMutationPrincipal
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	expiresAt := now.Add(operation.ProposalTTL)
	if !expiresAt.After(now) {
		return MCPMutationProposal{}, fmt.Errorf("%w: proposal expiry is invalid", ErrMCPMutationPolicyInvalid)
	}
	if strings.TrimSpace(runtime.CallID) == "" {
		return MCPMutationProposal{}, denyTool(ToolDenyNoOptIn, "effective tool-call ID is required")
	}
	argumentsHash := sha256Hex(canonical)
	schemaJSON, _ := json.Marshal(descriptor.schema)
	schemaHash := sha256Hex(schemaJSON)
	policySnapshot, err := json.Marshal(p.Policy)
	if err != nil {
		return MCPMutationProposal{}, fmt.Errorf("%w: policy snapshot is not serializable", ErrMCPMutationPolicyInvalid)
	}
	proposal = MCPMutationProposal{
		IdentityVersion:    MCPMutationIdentityVersion,
		State:              MCPProposalApprovalRequired,
		RunID:              runtime.Auth.RunID,
		TraceID:            runtime.Auth.TraceID,
		EffectiveCallID:    runtime.CallID,
		AgentID:            runtime.Auth.Identity.AgentID,
		Owner:              runtime.Auth.Identity.Owner,
		Principal:          runtime.Principal,
		ServerID:           descriptor.server.config.ID,
		RawToolName:        descriptor.approved.Name,
		ToolAlias:          descriptor.alias,
		Transport:          descriptor.server.config.Transport,
		TransportIdentity:  mcpTransportIdentity(descriptor.server.config),
		SchemaSHA256:       schemaHash,
		Arguments:          canonicalArgs,
		CanonicalArguments: append([]byte(nil), canonical...),
		ArgumentsSHA256:    argumentsHash,
		PolicyID:           p.Policy.PolicyID,
		PolicyVersion:      p.Policy.Version,
		PolicySHA256:       p.Policy.SHA256(),
		PolicySnapshot:     append([]byte(nil), policySnapshot...),
		SourceIDs:          sourceIDs,
		ExternalResource:   resource,
		Destructive:        operation.Destructive,
		CreatedAt:          now,
		ExpiresAt:          expiresAt,
		ApprovalRequired:   operation.RequireApproval,
		ExecutionBackend:   "none",
		AuditRequired:      operation.AuditRequired,
	}
	proposal.ProposalSHA256 = proposalFingerprint(proposal)
	proposalID, err := newMCPProposalID()
	if err != nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	proposal.ProposalID = proposalID
	created, err := p.Store.Admit(ctx, proposal, operation.MaxProposalsPerRun, operation.MaxPendingProposals)
	if err != nil {
		return MCPMutationProposal{}, err
	}
	emitMCPMutationAudit(runtime, descriptor, created, "approval_required", "mcp_mutation_proposal_created")
	return created, nil
}

func validateMCPMutationArguments(operation MCPMutationOperationPolicy, args map[string]any) error {
	if args == nil {
		return fmt.Errorf("%w: arguments must be an object", ErrMCPMutationArguments)
	}
	if len(operation.AllowedArgumentFields) > 0 {
		allowed := map[string]bool{}
		for _, name := range operation.AllowedArgumentFields {
			allowed[name] = true
		}
		for name := range args {
			if !allowed[name] {
				return fmt.Errorf("%w: unexpected argument %q", ErrMCPMutationArguments, name)
			}
		}
	}
	for _, name := range operation.RequiredArgumentFields {
		if _, ok := args[name]; !ok {
			return fmt.Errorf("%w: required argument %q is missing", ErrMCPMutationArguments, name)
		}
	}
	return nil
}

func mcpMutationScope(operation MCPMutationOperationPolicy, args map[string]any) ([]uint64, string, error) {
	if len(operation.AllowedSources) > 0 {
		// A source-scoped operation must not also carry an external-resource
		// selector. Accepting both would let the canonical fingerprint bind one
		// scope while the remote tool could interpret the other.
		if _, ok := args["resource"]; ok {
			return nil, "", fmt.Errorf("%w: resource is not allowed for a source-scoped mutation", ErrMCPMutationArguments)
		}
		if _, ok := args["resource_id"]; ok {
			return nil, "", fmt.Errorf("%w: resource_id is not allowed for a source-scoped mutation", ErrMCPMutationArguments)
		}
		raw, ok := args["source_id"]
		if !ok {
			return nil, "", fmt.Errorf("%w: source_id is required", ErrMCPMutationArguments)
		}
		sourceID, err := toSourceIDArg(raw)
		if err != nil || !containsSource(operation.AllowedSources, sourceID) {
			return nil, "", fmt.Errorf("%w: source is outside the approved mutation scope", ErrMCPMutationArguments)
		}
		return []uint64{sourceID}, "", nil
	}
	if _, ok := args["source_id"]; ok {
		return nil, "", fmt.Errorf("%w: source_id is not allowed for an external-resource mutation", ErrMCPMutationArguments)
	}
	// Two external-resource selector names must never disagree: the canonical
	// fingerprint would bind one value while a future transport could read the
	// other.
	_, hasResource := args["resource"]
	_, hasResourceID := args["resource_id"]
	if hasResource && hasResourceID {
		return nil, "", fmt.Errorf("%w: resource and resource_id are ambiguous together", ErrMCPMutationArguments)
	}
	rawResource, ok := args["resource"]
	if !ok {
		rawResource, ok = args["resource_id"]
	}
	resource := strings.TrimSpace(fmt.Sprint(rawResource))
	if !ok || resource == "" || resource == "<nil>" {
		return nil, "", fmt.Errorf("%w: an exact external resource is required", ErrMCPMutationArguments)
	}
	for _, allowed := range operation.AllowedResources {
		if resource == allowed {
			return nil, resource, nil
		}
	}
	return nil, "", fmt.Errorf("%w: external resource is outside the approved mutation scope", ErrMCPMutationArguments)
}

func mcpTransportIdentity(server cfg.MCPServerConfig) string {
	if server.Transport == mcpTransportStreamableHTTP {
		return server.Endpoint
	}
	return server.Command
}

var mcpJSONNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

const (
	mcpCanonicalMaxDepth     = 32
	mcpCanonicalMaxFields    = 256
	mcpCanonicalMaxItems     = 1024
	mcpCanonicalMaxNumberLen = 1024
	mcpCanonicalMaxExponent  = 1024
	mcpCanonicalMaxDigits    = 4096
)

// canonicalMCPArguments returns the exact canonical JSON bytes and a decoded
// map containing json.Number values. JSON numbers remain numbers; JSON strings
// remain strings. The canonical bytes are the value that must be retained for
// any future dispatch.
func canonicalMCPArguments(args map[string]any, maxBytes int) ([]byte, map[string]any, error) {
	normalized, err := canonicalMCPValue(args, 0)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: arguments are not serializable", ErrMCPMutationArguments)
	}
	if len(encoded) > maxBytes {
		return nil, nil, fmt.Errorf("%w: arguments exceed the configured size", ErrMCPMutationArguments)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, nil, fmt.Errorf("%w: canonical arguments are invalid", ErrMCPMutationArguments)
	}
	if decoder.More() {
		return nil, nil, fmt.Errorf("%w: trailing canonical arguments", ErrMCPMutationArguments)
	}
	return encoded, decoded, nil
}

func canonicalMCPValue(value any, depth int) (any, error) {
	if depth > mcpCanonicalMaxDepth {
		return nil, fmt.Errorf("%w: arguments are nested too deeply", ErrMCPMutationArguments)
	}
	switch v := value.(type) {
	case nil, bool:
		return v, nil
	case string:
		if !utf8.ValidString(v) {
			return nil, fmt.Errorf("%w: invalid UTF-8 string", ErrMCPMutationArguments)
		}
		return v, nil
	case json.Number:
		return canonicalMCPNumber(string(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%w: non-finite number", ErrMCPMutationArguments)
		}
		if v == 0 {
			return json.Number("0"), nil
		}
		if v == math.Trunc(v) {
			if v > 9007199254740991 || v < -9007199254740991 {
				return nil, fmt.Errorf("%w: integer exceeds the exact float64 range", ErrMCPMutationArguments)
			}
			return json.Number(strconv.FormatInt(int64(v), 10)), nil
		}
		return canonicalMCPNumber(strconv.FormatFloat(v, 'g', -1, 64))
	case float32:
		return canonicalMCPValue(float64(v), depth)
	case int:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int8:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int16:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int32:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int64:
		return json.Number(strconv.FormatInt(v, 10)), nil
	case uint:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint8:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint16:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint32:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(v, 10)), nil
	case []any:
		if len(v) > mcpCanonicalMaxItems {
			return nil, fmt.Errorf("%w: array is too large", ErrMCPMutationArguments)
		}
		out := make([]any, len(v))
		for i, item := range v {
			normalized, err := canonicalMCPValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = normalized
		}
		return out, nil
	case map[string]any:
		if len(v) > mcpCanonicalMaxFields {
			return nil, fmt.Errorf("%w: object has too many fields", ErrMCPMutationArguments)
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			if !utf8.ValidString(key) || len(key) > 256 || containsMCPControl(key) {
				return nil, fmt.Errorf("%w: invalid argument name", ErrMCPMutationArguments)
			}
			normalized, err := canonicalMCPValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = normalized
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unsupported argument type %T", ErrMCPMutationArguments, value)
	}
}

func canonicalMCPNumber(raw string) (json.Number, error) {
	if len(raw) == 0 || len(raw) > mcpCanonicalMaxNumberLen || !mcpJSONNumberPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: invalid JSON number", ErrMCPMutationArguments)
	}
	// Bound exponent magnitude before arbitrary-precision expansion.
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		exp, err := strconv.Atoi(raw[index+1:])
		if err != nil || exp > mcpCanonicalMaxExponent || exp < -mcpCanonicalMaxExponent {
			return "", fmt.Errorf("%w: numeric exponent is out of bounds", ErrMCPMutationArguments)
		}
	}
	rat, ok := new(big.Rat).SetString(raw)
	if !ok {
		return "", fmt.Errorf("%w: invalid numeric value", ErrMCPMutationArguments)
	}
	if rat.Sign() == 0 {
		return json.Number("0"), nil
	}
	den := new(big.Int).Set(rat.Denom())
	twos, fives := 0, 0
	two := big.NewInt(2)
	five := big.NewInt(5)
	zero := big.NewInt(0)
	for new(big.Int).Mod(den, two).Cmp(zero) == 0 {
		den.Div(den, two)
		twos++
	}
	for new(big.Int).Mod(den, five).Cmp(zero) == 0 {
		den.Div(den, five)
		fives++
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("%w: non-decimal numeric value", ErrMCPMutationArguments)
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	if scale > mcpCanonicalMaxDigits {
		return "", fmt.Errorf("%w: numeric precision is too large", ErrMCPMutationArguments)
	}
	scaled := new(big.Int).Mul(rat.Num(), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil))
	scaled.Quo(scaled, rat.Denom())
	digits := scaled.String()
	if len(digits) > mcpCanonicalMaxDigits {
		return "", fmt.Errorf("%w: numeric value is too large", ErrMCPMutationArguments)
	}
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = strings.TrimPrefix(digits, "-")
	}
	if scale > 0 {
		if len(digits) <= scale {
			digits = "0." + strings.Repeat("0", scale-len(digits)) + digits
		} else {
			digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
		}
		digits = strings.TrimRight(digits, "0")
		digits = strings.TrimRight(digits, ".")
	}
	if digits == "" || digits == "0" {
		return json.Number("0"), nil
	}
	if negative {
		digits = "-" + digits
	}
	return json.Number(digits), nil
}

// normalizeMCPSourceIDs keeps a nil source list and an empty SQL array
// representation identical for fingerprinting. PostgreSQL/SQLite round-trips
// commonly return an empty non-nil slice for a resource-scoped proposal.
func normalizeMCPSourceIDs(ids []uint64) []uint64 {
	if ids == nil {
		return []uint64{}
	}
	return ids
}

func proposalFingerprint(p MCPMutationProposal) string {
	payload := struct {
		IdentityVersion                        int
		RunID, EffectiveCallID, AgentID, Owner string
		PrincipalUserID                        uint64
		PrincipalUsername, PrincipalIssuer     string
		ServerID, RawToolName, ToolAlias       string
		Transport, TransportIdentity           string
		SchemaSHA256, ArgumentsSHA256          string
		PolicyID, PolicyVersion, PolicySHA256  string
		SourceIDs                              []uint64
		ExternalResource                       string
	}{
		p.IdentityVersion,
		p.RunID, p.EffectiveCallID, p.AgentID, p.Owner,
		p.Principal.UserID,
		p.Principal.Username, p.Principal.Issuer,
		p.ServerID, p.RawToolName, p.ToolAlias,
		p.Transport, p.TransportIdentity,
		p.SchemaSHA256, p.ArgumentsSHA256,
		p.PolicyID, p.PolicyVersion, p.PolicySHA256,
		normalizeMCPSourceIDs(p.SourceIDs), p.ExternalResource,
	}
	encoded, _ := json.Marshal(payload)
	return sha256Hex(encoded)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newMCPProposalID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// MCPMutationApproval is an approval contract for a future WP-04C2 dispatch.
// WP-04C2-B durably issues and revokes approvals and the verifier checks their
// binding; neither path dispatches an external operation.
type MCPMutationApproval struct {
	ApprovalID        string
	ProposalID        string
	ProposalSHA256    string
	RunID             string
	AgentID           string
	ServerID          string
	ToolAlias         string
	ArgumentsSHA256   string
	PolicySHA256      string
	ApprovedByUserID  uint64
	ApprovedBy        string
	Issuer            string
	ApprovedAt        time.Time
	ExpiresAt         time.Time
	RevokedAt         *time.Time
	MaxExecutions     int
	ExecutionsStarted int
	State             string
}

type MCPMutationApprovalStore interface {
	Lookup(ctx context.Context, approvalID string) (MCPMutationApproval, bool, error)
}

type InMemoryMCPMutationApprovalStore struct {
	mu     sync.Mutex
	values map[string]MCPMutationApproval
}

func NewInMemoryMCPMutationApprovalStore() *InMemoryMCPMutationApprovalStore {
	return &InMemoryMCPMutationApprovalStore{values: map[string]MCPMutationApproval{}}
}

func (s *InMemoryMCPMutationApprovalStore) Put(ctx context.Context, approval MCPMutationApproval) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if s == nil {
		return ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.values[approval.ApprovalID]; ok && existing.ProposalSHA256 != approval.ProposalSHA256 {
		return ErrMCPMutationProposalConflict
	}
	s.values[approval.ApprovalID] = approval
	return nil
}

func (s *InMemoryMCPMutationApprovalStore) Lookup(ctx context.Context, approvalID string) (MCPMutationApproval, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return MCPMutationApproval{}, false, err
		}
	}
	if s == nil {
		return MCPMutationApproval{}, false, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.values[approvalID]
	return approval, ok, nil
}

type MCPMutationApprovalVerifier struct {
	Store MCPMutationApprovalStore
	Now   func() time.Time
}

func (v MCPMutationApprovalVerifier) Verify(ctx context.Context, proposal MCPMutationProposal, approvalID string, approver MCPMutationPrincipal) (MCPMutationApproval, error) {
	if v.Store == nil {
		return MCPMutationApproval{}, ErrMCPMutationStoreUnavailable
	}
	if err := approver.Validate(); err != nil {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	// The verifier must not trust a caller-assembled proposal struct. Recompute
	// the fingerprint and check the canonical argument bytes so a changed
	// scope, resource, tool or argument cannot be matched against an approval
	// for the original content.
	if proposal.ProposalSHA256 == "" || proposalFingerprint(proposal) != proposal.ProposalSHA256 ||
		len(proposal.CanonicalArguments) == 0 ||
		sha256Hex(proposal.CanonicalArguments) != proposal.ArgumentsSHA256 {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	approval, ok, err := v.Store.Lookup(ctx, approvalID)
	if err != nil {
		return MCPMutationApproval{}, ErrMCPMutationStoreUnavailable
	}
	if !ok {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	if approval.ProposalSHA256 != proposal.ProposalSHA256 ||
		approval.RunID != proposal.RunID || approval.AgentID != proposal.AgentID ||
		approval.ServerID != proposal.ServerID || approval.ToolAlias != proposal.ToolAlias ||
		approval.ArgumentsSHA256 != proposal.ArgumentsSHA256 ||
		approval.PolicySHA256 != proposal.PolicySHA256 {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	if approval.ApprovedByUserID != approver.UserID || approval.ApprovedBy != approver.Username || approval.Issuer != approver.Issuer {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	// A binding check for an inert future dispatcher. It deliberately rejects
	// approvals that predate the proposal, are future-dated, or authorize an
	// already expired proposal. It still cannot check account status, current
	// policy or a durable execution counter; dispatch must revalidate those
	// atomically.
	if approval.ApprovedAt.IsZero() || approval.ApprovedAt.After(now) ||
		approval.ApprovedAt.Before(proposal.CreatedAt) ||
		!proposal.ExpiresAt.After(now) {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	if !approval.ExpiresAt.After(now) || approval.RevokedAt != nil || approval.MaxExecutions <= 0 {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	if approval.State != "" && approval.State != "active" {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	if approval.ExecutionsStarted >= approval.MaxExecutions {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	return approval, nil
}

func mcpMutationDescriptorServerID(descriptor *mcpMutationTool) string {
	if descriptor == nil || descriptor.server == nil {
		return ""
	}
	return descriptor.server.config.ID
}

func mcpMutationDescriptorAlias(descriptor *mcpMutationTool) string {
	if descriptor == nil {
		return ""
	}
	return descriptor.alias
}

func emitMCPMutationAudit(runtime ToolRuntime, descriptor *mcpMutationTool, proposal MCPMutationProposal, outcome, reason string) {
	if AgentsEngine == nil || descriptor == nil {
		return
	}
	AgentsEngine.appendAudit(AuditEvent{
		RunID: runtime.Auth.RunID, TraceID: runtime.Auth.TraceID,
		AgentID: runtime.Auth.Identity.AgentID, AgentName: runtime.Auth.Identity.Name,
		Owner: runtime.Auth.Identity.Owner, Action: descriptor.alias,
		RequiredCapability: "tool_execution", CapabilitiesUsed: []string{"tool_execution"},
		Outcome: outcome, Reason: reason, ReasonCode: reason,
		MCPServerID: descriptor.server.config.ID, MCPToolName: descriptor.approved.Name,
		MCPProposalID: proposal.ProposalID, MCPProposalSHA256: proposal.ProposalSHA256,
		MCPPolicyID: proposal.PolicyID,
	})
}

// Ensure the proposal tool is explicitly mutation-classified and cannot be
// registered through the read-only path.
var _ AgentTool = (*MCPMutationProposalTool)(nil)
var _ AgentToolEffect = (*MCPMutationProposalTool)(nil)
