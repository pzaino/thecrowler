package agent

import (
	"errors"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	// ErrMCPMutationPolicyUnavailable means the current trusted operator
	// policy or the current agent identity cannot be loaded. Approval must fail
	// closed; it is never a permission grant.
	ErrMCPMutationPolicyUnavailable = errors.New("current MCP mutation policy is unavailable")
	// ErrMCPMutationPolicyMismatch means the stored proposal was created under
	// a different policy ID, version or canonical hash.
	ErrMCPMutationPolicyMismatch = errors.New("current MCP mutation policy identity does not match the proposal")
	// ErrMCPMutationOperationUnauthorized means the current policy or inventory
	// no longer authorizes the exact proposed operation and scope.
	ErrMCPMutationOperationUnauthorized = errors.New("current MCP mutation policy no longer authorizes the proposal")
)

// MCPMutationApprovalPolicy is the trusted current-policy view used at the
// approval boundary. Agent is the current manifest identity for the proposal's
// agent, re-read from the trusted registry; it is never taken from the
// proposal or the approval request.
type MCPMutationApprovalPolicy struct {
	Policy    MCPMutationPolicy
	Inventory cfg.MCPConfig
	Agent     *AgentIdentity
}

// CurrentMCPMutationApprovalPolicy snapshots the current process policy and
// inventory. The caller supplies the current agent identity separately because
// it is looked up by the proposal's agent ID.
func CurrentMCPMutationApprovalPolicy() MCPMutationApprovalPolicy {
	return MCPMutationApprovalPolicy{
		Policy:    CurrentMCPMutationPolicy(),
		Inventory: CurrentMCPOperatorConfig(),
	}
}

// CurrentAgentIdentityForMCPApproval resolves the current agent manifest
// identity from the trusted in-process registry. A missing registry or agent
// fails closed.
func CurrentAgentIdentityForMCPApproval(agentID string) (AgentIdentity, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || AgentsRegistry == nil {
		return AgentIdentity{}, false
	}
	registry := AgentsRegistry.Registry()
	if registry == nil {
		return AgentIdentity{}, false
	}
	def, ok := registry.GetByID(agentID)
	if !ok || def == nil {
		return AgentIdentity{}, false
	}
	return cloneAgentIdentityValue(def.Identity), true
}

func cloneAgentIdentityValue(identity AgentIdentity) AgentIdentity {
	return *cloneAgentIdentity(&identity)
}

// EvaluateMCPMutationApproval is the authoritative current-policy check for a
// stored proposal. It validates policy identity, the exact operation, current
// agent authority, source/resource scope, transport, argument constraints and
// the frozen inventory binding. It never trusts the stored policy snapshot as
// proof that the operation remains authorized.
func EvaluateMCPMutationApproval(proposal MCPMutationProposal, current MCPMutationApprovalPolicy) error {
	if err := current.Policy.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrMCPMutationPolicyUnavailable, err)
	}
	if proposal.PolicyID != current.Policy.PolicyID ||
		proposal.PolicyVersion != current.Policy.Version ||
		proposal.PolicySHA256 != current.Policy.SHA256() {
		return ErrMCPMutationPolicyMismatch
	}
	operation, ok := current.Policy.Operation(proposal.ToolAlias, proposal.ServerID, proposal.RawToolName)
	if !ok {
		return fmt.Errorf("%w: operation %q is no longer in the policy", ErrMCPMutationOperationUnauthorized, proposal.ToolAlias)
	}
	// The durable row persists approval_required and execution_backend but not
	// the redundant audit_required flag; the current operation's audit flag is
	// checked here and the policy hash binds it to the stored snapshot.
	if !proposal.ApprovalRequired || proposal.ExecutionBackend != "none" ||
		!operation.RequireApproval || !operation.AuditRequired ||
		(operation.ExecutionBackend != "" && operation.ExecutionBackend != "none") {
		return fmt.Errorf("%w: approval, audit or execution-backend contract changed", ErrMCPMutationOperationUnauthorized)
	}
	if proposal.Destructive != operation.Destructive {
		return fmt.Errorf("%w: destructive classification changed", ErrMCPMutationOperationUnauthorized)
	}
	if !containsString(operation.AllowedAgents, proposal.AgentID) {
		return fmt.Errorf("%w: agent %q is no longer allowed", ErrMCPMutationOperationUnauthorized, proposal.AgentID)
	}
	if current.Agent == nil {
		return fmt.Errorf("%w: current agent identity is unavailable", ErrMCPMutationPolicyUnavailable)
	}
	agent := *current.Agent
	if agent.AgentID != proposal.AgentID || strings.TrimSpace(agent.Owner) != proposal.Owner {
		return fmt.Errorf("%w: current agent identity changed", ErrMCPMutationOperationUnauthorized)
	}
	for _, capability := range operation.RequiredCapabilities {
		if !identityGrants(agent, capability) {
			return fmt.Errorf("%w: current agent lacks capability %q", ErrMCPMutationOperationUnauthorized, capability)
		}
	}
	if trustLevelRank(agent.TrustLevel) < trustLevelRank(operation.MinTrustLevel) {
		return fmt.Errorf("%w: current agent trust is insufficient", ErrMCPMutationOperationUnauthorized)
	}
	if toolForbiddenByContract(agent.Contract, proposal.ToolAlias) {
		return fmt.Errorf("%w: current agent contract forbids the tool", ErrMCPMutationOperationUnauthorized)
	}

	server, ok := mcpApprovalServerByID(current.Inventory, proposal.ServerID)
	if !ok || !server.Enabled {
		return fmt.Errorf("%w: current inventory no longer enables the server", ErrMCPMutationOperationUnauthorized)
	}
	if server.Transport != proposal.Transport || mcpTransportIdentity(server) != proposal.TransportIdentity {
		return fmt.Errorf("%w: current inventory transport identity changed", ErrMCPMutationOperationUnauthorized)
	}
	if len(server.AllowedAgents) > 0 && !containsString(server.AllowedAgents, proposal.AgentID) {
		return fmt.Errorf("%w: current inventory no longer allows the agent", ErrMCPMutationOperationUnauthorized)
	}
	tool, ok := mcpApprovalToolByName(server, proposal.RawToolName)
	if !ok || tool.Effect != "mutation" || !tool.AllowMutationProposal ||
		MCPToolAlias(server.ID, tool.Name) != proposal.ToolAlias {
		return fmt.Errorf("%w: current inventory no longer approves the exact mutation tool", ErrMCPMutationOperationUnauthorized)
	}
	if !containsString(operation.AllowedTransports, proposal.Transport) {
		return fmt.Errorf("%w: current policy no longer allows the transport", ErrMCPMutationOperationUnauthorized)
	}

	switch {
	case len(operation.AllowedSources) > 0:
		if len(proposal.SourceIDs) == 0 || strings.TrimSpace(proposal.ExternalResource) != "" {
			return fmt.Errorf("%w: source scope changed", ErrMCPMutationOperationUnauthorized)
		}
		effective := effectiveMCPSources(server, tool)
		if len(effective) == 0 {
			return fmt.Errorf("%w: inventory source scope is empty", ErrMCPMutationOperationUnauthorized)
		}
		for _, sourceID := range proposal.SourceIDs {
			if !containsSource(operation.AllowedSources, sourceID) || !effective[sourceID] {
				return fmt.Errorf("%w: source %d is outside the current scope", ErrMCPMutationOperationUnauthorized, sourceID)
			}
		}
	case len(operation.AllowedResources) > 0:
		if len(proposal.SourceIDs) != 0 || strings.TrimSpace(proposal.ExternalResource) == "" ||
			!containsString(operation.AllowedResources, proposal.ExternalResource) {
			return fmt.Errorf("%w: resource scope changed", ErrMCPMutationOperationUnauthorized)
		}
	default:
		return fmt.Errorf("%w: operation has no approved scope", ErrMCPMutationOperationUnauthorized)
	}

	if len(proposal.CanonicalArguments) == 0 ||
		sha256Hex(proposal.CanonicalArguments) != proposal.ArgumentsSHA256 {
		return fmt.Errorf("%w: stored arguments are corrupt", ErrMCPMutationOperationUnauthorized)
	}
	canonical, args, err := canonicalMCPArguments(proposal.Arguments, operation.MaxInputBytes)
	if err != nil {
		return fmt.Errorf("%w: current argument bounds reject the proposal", ErrMCPMutationOperationUnauthorized)
	}
	if string(canonical) != string(proposal.CanonicalArguments) ||
		sha256Hex(canonical) != proposal.ArgumentsSHA256 {
		return fmt.Errorf("%w: stored arguments no longer canonicalize identically", ErrMCPMutationOperationUnauthorized)
	}
	if err := validateMCPMutationArguments(operation, args); err != nil {
		return fmt.Errorf("%w: current argument constraints reject the proposal", ErrMCPMutationOperationUnauthorized)
	}
	sourceIDs, resource, err := mcpMutationScope(operation, args)
	if err != nil {
		return fmt.Errorf("%w: current scope constraints reject the proposal", ErrMCPMutationOperationUnauthorized)
	}
	if !sameMCPMutationSourceIDs(normalizeMCPSourceIDs(sourceIDs), normalizeMCPSourceIDs(proposal.SourceIDs)) ||
		strings.TrimSpace(resource) != strings.TrimSpace(proposal.ExternalResource) {
		return fmt.Errorf("%w: stored scope does not match current constraints", ErrMCPMutationOperationUnauthorized)
	}
	if proposal.ProposalSHA256 == "" || proposalFingerprint(proposal) != proposal.ProposalSHA256 {
		return fmt.Errorf("%w: proposal fingerprint is invalid", ErrMCPMutationOperationUnauthorized)
	}
	return nil
}

func mcpApprovalServerByID(inventory cfg.MCPConfig, serverID string) (cfg.MCPServerConfig, bool) {
	for _, server := range inventory.Servers {
		if server.ID == serverID {
			return server, true
		}
	}
	return cfg.MCPServerConfig{}, false
}

func mcpApprovalToolByName(server cfg.MCPServerConfig, toolName string) (cfg.MCPApprovedTool, bool) {
	for _, tool := range server.ApprovedTools {
		if tool.Name == toolName {
			return tool, true
		}
	}
	return cfg.MCPApprovedTool{}, false
}

func sameMCPMutationSourceIDs(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	left = normalizeMCPSourceIDs(left)
	right = normalizeMCPSourceIDs(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
