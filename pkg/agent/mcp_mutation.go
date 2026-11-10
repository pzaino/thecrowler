package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// WP-04C1 is proposal-only. A mutation descriptor can produce an immutable
// inert proposal; it cannot execute an MCP tools/call for the mutation.
type MCPMutationProposalState string

const (
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

// MCPMutationPolicy is trusted operator/configuration policy. It is
// deliberately a Go contract in WP-04C1; it is not yet exposed as an agent
// manifest field because production durable proposal persistence has not been
// approved.
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

func (p *MCPMutationPolicy) Validate() error {
	if p == nil || !p.Enabled {
		return ErrMCPMutationDisabled
	}
	if strings.TrimSpace(p.PolicyID) == "" || strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("%w: policy_id and version are required", ErrMCPMutationPolicyInvalid)
	}
	if len(p.Operations) == 0 || len(p.Operations) > 32 {
		return fmt.Errorf("%w: operations must contain 1..32 entries", ErrMCPMutationPolicyInvalid)
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
	if MCPToolAlias(operation.ServerID, operation.ToolName) != operation.ToolAlias {
		return fmt.Errorf("tool_alias does not match the exact server/tool identity")
	}
	if len(operation.AllowedAgents) == 0 {
		return fmt.Errorf("allowed_agents must not be empty")
	}
	if len(operation.RequiredCapabilities) == 0 || !containsString(operation.RequiredCapabilities, "tool_execution") {
		return fmt.Errorf("required_capabilities must include tool_execution")
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
	for _, sourceID := range operation.AllowedSources {
		if sourceID == 0 {
			return fmt.Errorf("allowed_sources contains zero")
		}
	}
	for _, resource := range operation.AllowedResources {
		if strings.TrimSpace(resource) == "" || len(resource) > 256 || containsMCPControl(resource) {
			return fmt.Errorf("allowed_resources contains an invalid value")
		}
	}
	if operation.ProposalTTL <= 0 || operation.ProposalTTL > time.Hour {
		return fmt.Errorf("proposal_ttl must be within 1s..1h")
	}
	if operation.MaxProposalsPerRun <= 0 || operation.MaxProposalsPerRun > 16 {
		return fmt.Errorf("max_proposals_per_run must be 1..16")
	}
	if operation.MaxPendingProposals <= 0 || operation.MaxPendingProposals > 100 {
		return fmt.Errorf("max_pending_proposals must be 1..100")
	}
	if operation.MaxInputBytes <= 0 || operation.MaxInputBytes > 64<<10 {
		return fmt.Errorf("max_input_bytes must be 1..65536")
	}
	if operation.MaxOutputBytes <= 0 || operation.MaxOutputBytes > 64<<10 {
		return fmt.Errorf("max_output_bytes must be 1..65536")
	}
	if !operation.RequireApproval {
		return fmt.Errorf("require_approval must be true in WP-04C1")
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
	ProposalID        string
	State             MCPMutationProposalState
	RunID             string
	TraceID           string
	EffectiveCallID   string
	AgentID           string
	Owner             string
	Principal         MCPMutationPrincipal
	ServerID          string
	RawToolName       string
	ToolAlias         string
	Transport         string
	TransportIdentity string
	SchemaSHA256      string
	Arguments         map[string]any
	ArgumentsSHA256   string
	ProposalSHA256    string
	PolicyID          string
	PolicyVersion     string
	PolicySHA256      string
	SourceIDs         []uint64
	ExternalResource  string
	Destructive       bool
	CreatedAt         time.Time
	ExpiresAt         time.Time
	ApprovalRequired  bool
	ExecutionBackend  string
	AuditRequired     bool
}

// MCPMutationProposalStore is the narrow persistence boundary. The only
// implementation in WP-04C1 is test-only/in-memory; production durability
// requires the separate schema 1.17 review described in the design document.
type MCPMutationProposalStore interface {
	Create(ctx context.Context, proposal MCPMutationProposal) (MCPMutationProposal, error)
	Get(ctx context.Context, proposalID string) (MCPMutationProposal, bool, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (MCPMutationProposal, bool, error)
	CountByRun(ctx context.Context, runID string) (int, error)
	CountPendingByPrincipal(ctx context.Context, userID uint64) (int, error)
}

type InMemoryMCPMutationProposalStore struct {
	mu       sync.Mutex
	byID     map[string]MCPMutationProposal
	byFinger map[string]string
}

func NewInMemoryMCPMutationProposalStore() *InMemoryMCPMutationProposalStore {
	return &InMemoryMCPMutationProposalStore{
		byID: map[string]MCPMutationProposal{}, byFinger: map[string]string{},
	}
}

func (s *InMemoryMCPMutationProposalStore) Create(ctx context.Context, proposal MCPMutationProposal) (MCPMutationProposal, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return MCPMutationProposal{}, err
		}
	}
	if s == nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID, ok := s.byFinger[proposal.ProposalSHA256]; ok {
		existing := s.byID[existingID]
		if existing.ProposalSHA256 != proposal.ProposalSHA256 {
			return MCPMutationProposal{}, ErrMCPMutationProposalConflict
		}
		return existing, nil
	}
	if _, exists := s.byID[proposal.ProposalID]; exists {
		return MCPMutationProposal{}, ErrMCPMutationProposalConflict
	}
	s.byID[proposal.ProposalID] = cloneMCPMutationProposal(proposal)
	s.byFinger[proposal.ProposalSHA256] = proposal.ProposalID
	return cloneMCPMutationProposal(proposal), nil
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
	var descriptor *mcpMutationTool
	for _, candidate := range manager.mutations {
		if _, ok := policy.Operation(candidate.alias, candidate.server.config.ID, candidate.approved.Name); ok {
			descriptor = candidate
			break
		}
	}
	if descriptor == nil {
		return nil, fmt.Errorf("%w: no policy operation matches a frozen mutation descriptor", ErrMCPMutationNotApproved)
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

func (p *MCPMutationProposer) Propose(ctx context.Context, tool AgentTool, descriptor *mcpMutationTool, args map[string]any) (MCPMutationProposal, error) {
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
	canonical, err := canonicalMCPArguments(args, operation.MaxInputBytes)
	if err != nil {
		return MCPMutationProposal{}, err
	}
	sourceIDs, resource, err := mcpMutationScope(operation, args)
	if err != nil {
		return MCPMutationProposal{}, err
	}
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
	proposal := MCPMutationProposal{
		State:             MCPProposalApprovalRequired,
		RunID:             runtime.Auth.RunID,
		TraceID:           runtime.Auth.TraceID,
		EffectiveCallID:   runtime.CallID,
		AgentID:           runtime.Auth.Identity.AgentID,
		Owner:             runtime.Auth.Identity.Owner,
		Principal:         runtime.Principal,
		ServerID:          descriptor.server.config.ID,
		RawToolName:       descriptor.approved.Name,
		ToolAlias:         descriptor.alias,
		Transport:         descriptor.server.config.Transport,
		TransportIdentity: mcpTransportIdentity(descriptor.server.config),
		SchemaSHA256:      schemaHash,
		Arguments:         cloneAnyMap(args),
		ArgumentsSHA256:   argumentsHash,
		PolicyID:          p.Policy.PolicyID,
		PolicyVersion:     p.Policy.Version,
		PolicySHA256:      p.Policy.SHA256(),
		SourceIDs:         sourceIDs,
		ExternalResource:  resource,
		Destructive:       operation.Destructive,
		CreatedAt:         now,
		ExpiresAt:         expiresAt,
		ApprovalRequired:  operation.RequireApproval,
		ExecutionBackend:  "none",
		AuditRequired:     operation.AuditRequired,
	}
	proposal.ProposalSHA256 = proposalFingerprint(proposal)
	if existing, found, err := p.Store.GetByFingerprint(ctx, proposal.ProposalSHA256); err != nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	} else if found {
		if existing.RunID != proposal.RunID || existing.AgentID != proposal.AgentID ||
			existing.EffectiveCallID != proposal.EffectiveCallID {
			return MCPMutationProposal{}, ErrMCPMutationProposalConflict
		}
		return existing, nil
	}
	runCount, err := p.Store.CountByRun(ctx, runtime.Auth.RunID)
	if err != nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	if runCount >= operation.MaxProposalsPerRun {
		return MCPMutationProposal{}, fmt.Errorf("%w: per-run proposal limit reached", ErrMCPMutationPolicyInvalid)
	}
	pendingCount, err := p.Store.CountPendingByPrincipal(ctx, runtime.Principal.UserID)
	if err != nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	if pendingCount >= operation.MaxPendingProposals {
		return MCPMutationProposal{}, fmt.Errorf("%w: pending proposal limit reached", ErrMCPMutationPolicyInvalid)
	}
	proposalID, err := newMCPProposalID()
	if err != nil {
		return MCPMutationProposal{}, ErrMCPMutationStoreUnavailable
	}
	proposal.ProposalID = proposalID
	created, err := p.Store.Create(ctx, proposal)
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

func canonicalMCPArguments(args map[string]any, maxBytes int) ([]byte, error) {
	normalized, err := canonicalMCPValue(args, 0)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: arguments are not serializable", ErrMCPMutationArguments)
	}
	if len(encoded) > maxBytes {
		return nil, fmt.Errorf("%w: arguments exceed the configured size", ErrMCPMutationArguments)
	}
	return encoded, nil
}

func canonicalMCPValue(value any, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("%w: arguments are nested too deeply", ErrMCPMutationArguments)
	}
	switch v := value.(type) {
	case nil, string, bool:
		return v, nil
	case json.Number:
		return normalizeMCPNumber(string(v))
	case float64:
		if v != v || v > 1e308 || v < -1e308 {
			return nil, fmt.Errorf("%w: non-finite number", ErrMCPMutationArguments)
		}
		if v == float64(int64(v)) {
			return int64(v), nil
		}
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case float32:
		return canonicalMCPValue(float64(v), depth)
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return nil, fmt.Errorf("%w: integer is too large", ErrMCPMutationArguments)
		}
		return int64(v), nil
	case []any:
		if len(v) > 1024 {
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
		if len(v) > 256 {
			return nil, fmt.Errorf("%w: object has too many fields", ErrMCPMutationArguments)
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			if len(key) > 256 || containsMCPControl(key) {
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

func normalizeMCPNumber(raw string) (any, error) {
	if integer, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return integer, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value != value || value > 1e308 || value < -1e308 {
		return nil, fmt.Errorf("%w: invalid number", ErrMCPMutationArguments)
	}
	if value == float64(int64(value)) {
		return int64(value), nil
	}
	return strconv.FormatFloat(value, 'g', -1, 64), nil
}

func proposalFingerprint(p MCPMutationProposal) string {
	payload := struct {
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
		p.RunID, p.EffectiveCallID, p.AgentID, p.Owner,
		p.Principal.UserID,
		p.Principal.Username, p.Principal.Issuer,
		p.ServerID, p.RawToolName, p.ToolAlias,
		p.Transport, p.TransportIdentity,
		p.SchemaSHA256, p.ArgumentsSHA256,
		p.PolicyID, p.PolicyVersion, p.PolicySHA256,
		p.SourceIDs, p.ExternalResource,
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
// WP-04C1 only defines and verifies the binding; it never dispatches.
type MCPMutationApproval struct {
	ApprovalID       string
	ProposalSHA256   string
	RunID            string
	AgentID          string
	ServerID         string
	ToolAlias        string
	ArgumentsSHA256  string
	PolicySHA256     string
	ApprovedByUserID uint64
	ApprovedBy       string
	Issuer           string
	ApprovedAt       time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	MaxExecutions    int
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
	if !approval.ExpiresAt.After(now) || approval.RevokedAt != nil || approval.MaxExecutions <= 0 {
		return MCPMutationApproval{}, ErrMCPMutationApproval
	}
	return approval, nil
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
