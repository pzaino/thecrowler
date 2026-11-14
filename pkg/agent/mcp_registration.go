package agent

import (
	"context"
	"fmt"
	"strings"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// registerMCPMutationProposalTools admits one inert proposal adapter per exact
// operator-approved MCP operation. Native crawl MutationAllowed and
// AllowedOperations are deliberately not consulted.
func registerMCPMutationProposalTools(
	ctx context.Context,
	manager *MCPToolManager,
	registry *AgentToolRegistry,
	policy MCPMutationPolicy,
	identity AgentIdentity,
	principal MCPMutationPrincipal,
	allowlist []string,
	runSources []uint64,
	db cdb.Handler,
) (map[string]bool, error) {
	if !policy.Enabled || len(policy.Operations) == 0 {
		return nil, nil
	}
	var requested []MCPMutationOperationPolicy
	for _, operation := range policy.Operations {
		if containsString(allowlist, operation.ToolAlias) {
			requested = append(requested, operation)
		}
	}
	if len(requested) == 0 {
		return nil, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if manager == nil || registry == nil {
		return nil, fmt.Errorf("%w: MCP manager or registry is unavailable", ErrMCPMutationNotApproved)
	}
	if principal.Validate() != nil {
		return nil, fmt.Errorf("%w: an authenticated principal is required for MCP mutation proposals", ErrMCPMutationPrincipal)
	}
	if strings.TrimSpace(identity.Owner) == "" {
		return nil, fmt.Errorf("%w: agent identity owner audit label is required for durable MCP proposals", ErrMCPMutationNotApproved)
	}
	if db == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	store, err := NewDatabaseMCPMutationProposalStore(&db)
	if err != nil {
		return nil, err
	}
	runScope := sliceToSourceSet(runSources)
	tools := make([]*MCPMutationProposalTool, 0, len(requested))
	for _, operation := range requested {
		descriptor, ok := manager.MutationTool(operation.ToolAlias)
		if !ok || descriptor == nil || descriptor.server == nil {
			return nil, fmt.Errorf("%w: frozen mutation descriptor %q is unavailable", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if descriptor.server.config.ID != operation.ServerID || descriptor.approved.Name != operation.ToolName {
			return nil, fmt.Errorf("%w: operation %q does not match the frozen server/tool identity", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if !containsString(operation.AllowedAgents, identity.AgentID) {
			return nil, fmt.Errorf("%w: agent %q is not approved for MCP operation %q", ErrMCPMutationNotApproved, identity.AgentID, operation.ToolAlias)
		}
		for _, capability := range operation.RequiredCapabilities {
			if !identityGrants(identity, capability) {
				return nil, fmt.Errorf("%w: agent lacks capability %q for MCP operation %q", ErrMCPMutationNotApproved, capability, operation.ToolAlias)
			}
		}
		if trustLevelRank(identity.TrustLevel) < trustLevelRank(operation.MinTrustLevel) {
			return nil, fmt.Errorf("%w: agent trust is below MCP operation %q", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if toolForbiddenByContract(identity.Contract, operation.ToolAlias) {
			return nil, fmt.Errorf("%w: MCP operation %q is forbidden by contract", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if !containsString(operation.AllowedTransports, descriptor.server.config.Transport) {
			return nil, fmt.Errorf("%w: MCP transport is not approved for %q", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		effective := effectiveMCPSources(descriptor.server.config, descriptor.approved)
		if len(operation.AllowedSources) > 0 {
			if len(effective) == 0 {
				return nil, fmt.Errorf("%w: source-scoped operation %q has no approved inventory scope", ErrMCPMutationNotApproved, operation.ToolAlias)
			}
			for _, sourceID := range operation.AllowedSources {
				if !effective[sourceID] {
					return nil, fmt.Errorf("%w: source %d is outside the inventory scope for %q", ErrMCPMutationNotApproved, sourceID, operation.ToolAlias)
				}
				if len(runScope) > 0 && !runScope[sourceID] {
					return nil, fmt.Errorf("%w: source %d is outside the run scope for %q", ErrMCPMutationNotApproved, sourceID, operation.ToolAlias)
				}
			}
		}
		if len(operation.AllowedResources) > 0 && descriptor.sourceScoped {
			return nil, fmt.Errorf("%w: resource-scoped operation %q conflicts with the source-scoped tool", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		proposalTool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, operation.ToolAlias)
		if err != nil {
			return nil, err
		}
		tools = append(tools, proposalTool)
	}
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if err := registry.RegisterMutation(tool); err != nil {
			return nil, err
		}
		allowed[tool.Name()] = true
	}
	return allowed, nil
}
