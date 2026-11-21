package agent

import (
	"context"
	"errors"
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
	runID, traceID string,
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
	auditAll := func(reasonCode string) {
		recordMCPAdmissionDeniedDB(ctx, db, MCPAdmissionDeniedEvent{
			EventType:      MCPAuditEventRegistrationDenied,
			Stage:          MCPAuditStageRegistration,
			RunID:          runID,
			TraceID:        traceID,
			Identity:       identity,
			Principal:      principal,
			PolicyID:       policy.PolicyID,
			ReasonCode:     reasonCode,
			OperationCount: len(requested),
		})
	}
	auditOperation := func(operation MCPMutationOperationPolicy, reasonCode string) {
		recordMCPAdmissionDeniedDB(ctx, db, MCPAdmissionDeniedEvent{
			EventType:  MCPAuditEventRegistrationDenied,
			Stage:      MCPAuditStageRegistration,
			RunID:      runID,
			TraceID:    traceID,
			Identity:   identity,
			Principal:  principal,
			ServerID:   operation.ServerID,
			ToolAlias:  operation.ToolAlias,
			PolicyID:   policy.PolicyID,
			ReasonCode: reasonCode,
		})
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			auditAll(ToolDenyContext)
			return nil, err
		}
	}
	if manager == nil || registry == nil {
		auditAll("manager_unavailable")
		return nil, fmt.Errorf("%w: MCP manager or registry is unavailable", ErrMCPMutationNotApproved)
	}
	if principal.Validate() != nil {
		auditAll("missing_principal")
		return nil, fmt.Errorf("%w: an authenticated principal is required for MCP mutation proposals", ErrMCPMutationPrincipal)
	}
	if strings.TrimSpace(identity.Owner) == "" {
		auditAll("missing_owner")
		return nil, fmt.Errorf("%w: agent identity owner audit label is required for durable MCP proposals", ErrMCPMutationNotApproved)
	}
	if db == nil {
		return nil, ErrMCPMutationStoreUnavailable
	}
	store, err := NewDatabaseMCPMutationProposalStore(&db)
	if err != nil {
		auditAll("store_unavailable")
		return nil, err
	}
	runScope := sliceToSourceSet(runSources)
	tools := make([]*MCPMutationProposalTool, 0, len(requested))
	for _, operation := range requested {
		descriptor, ok := manager.MutationTool(operation.ToolAlias)
		if !ok || descriptor == nil || descriptor.server == nil {
			auditOperation(operation, "descriptor_unavailable")
			return nil, fmt.Errorf("%w: frozen mutation descriptor %q is unavailable", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if descriptor.server.config.ID != operation.ServerID || descriptor.approved.Name != operation.ToolName {
			auditOperation(operation, "descriptor_mismatch")
			return nil, fmt.Errorf("%w: operation %q does not match the frozen server/tool identity", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if !containsString(operation.AllowedAgents, identity.AgentID) {
			auditOperation(operation, "agent_not_approved")
			return nil, fmt.Errorf("%w: agent %q is not approved for MCP operation %q", ErrMCPMutationNotApproved, identity.AgentID, operation.ToolAlias)
		}
		for _, capability := range operation.RequiredCapabilities {
			if !identityGrants(identity, capability) {
				auditOperation(operation, "capability_missing")
				return nil, fmt.Errorf("%w: agent lacks capability %q for MCP operation %q", ErrMCPMutationNotApproved, capability, operation.ToolAlias)
			}
		}
		if trustLevelRank(identity.TrustLevel) < trustLevelRank(operation.MinTrustLevel) {
			auditOperation(operation, "trust_insufficient")
			return nil, fmt.Errorf("%w: agent trust is below MCP operation %q", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if toolForbiddenByContract(identity.Contract, operation.ToolAlias) {
			auditOperation(operation, "contract_forbidden")
			return nil, fmt.Errorf("%w: MCP operation %q is forbidden by contract", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		if !containsString(operation.AllowedTransports, descriptor.server.config.Transport) {
			auditOperation(operation, "transport_not_approved")
			return nil, fmt.Errorf("%w: MCP transport is not approved for %q", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		effective := effectiveMCPSources(descriptor.server.config, descriptor.approved)
		if len(operation.AllowedSources) > 0 {
			if len(effective) == 0 {
				auditOperation(operation, "source_scope_denied")
				return nil, fmt.Errorf("%w: source-scoped operation %q has no approved inventory scope", ErrMCPMutationNotApproved, operation.ToolAlias)
			}
			for _, sourceID := range operation.AllowedSources {
				if !effective[sourceID] {
					auditOperation(operation, "source_scope_denied")
					return nil, fmt.Errorf("%w: source %d is outside the inventory scope for %q", ErrMCPMutationNotApproved, sourceID, operation.ToolAlias)
				}
				if len(runScope) > 0 && !runScope[sourceID] {
					auditOperation(operation, "source_scope_denied")
					return nil, fmt.Errorf("%w: source %d is outside the run scope for %q", ErrMCPMutationNotApproved, sourceID, operation.ToolAlias)
				}
			}
		}
		if len(operation.AllowedResources) > 0 && descriptor.sourceScoped {
			auditOperation(operation, "resource_scope_conflict")
			return nil, fmt.Errorf("%w: resource-scoped operation %q conflicts with the source-scoped tool", ErrMCPMutationNotApproved, operation.ToolAlias)
		}
		proposalTool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, operation.ToolAlias)
		if err != nil {
			reason := "not_approved"
			switch {
			case errors.Is(err, ErrMCPMutationPolicyInvalid):
				reason = "policy_invalid"
			case errors.Is(err, ErrMCPMutationDisabled):
				reason = ToolDenyMutationDisabled
			case errors.Is(err, ErrMCPMutationStoreUnavailable):
				reason = "store_unavailable"
			}
			auditOperation(operation, reason)
			return nil, err
		}
		tools = append(tools, proposalTool)
	}
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if err := registry.RegisterMutation(tool); err != nil {
			auditOperationForTool(policy, tool, "registry_denied", runID, traceID, identity, principal, db, ctx)
			return nil, err
		}
		allowed[tool.Name()] = true
	}
	return allowed, nil
}

func auditOperationForTool(policy MCPMutationPolicy, tool *MCPMutationProposalTool, reasonCode, runID, traceID string, identity AgentIdentity, principal MCPMutationPrincipal, db cdb.Handler, ctx context.Context) {
	if tool == nil {
		return
	}
	info := tool.MCPMutationAuditIdentity()
	recordMCPAdmissionDeniedDB(ctx, db, MCPAdmissionDeniedEvent{
		EventType:  MCPAuditEventRegistrationDenied,
		Stage:      MCPAuditStageRegistration,
		RunID:      runID,
		TraceID:    traceID,
		Identity:   identity,
		Principal:  principal,
		ServerID:   info.ServerID,
		ToolAlias:  info.ToolAlias,
		PolicyID:   policy.PolicyID,
		ReasonCode: reasonCode,
	})
}
