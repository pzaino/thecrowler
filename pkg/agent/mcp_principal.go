package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pzaino/thecrowler/pkg/auth"
)

// cfgKeyMCPMutationPrincipal is a typed, trusted runtime value. It is never
// parsed from event payloads, agent manifests, model output or tool arguments.
const cfgKeyMCPMutationPrincipal = "mcp_mutation_principal"

// MCPMutationPrincipalFromAuthIdentity adapts a freshly resolved authenticated
// identity to the proposal principal. The caller must have re-fetched current
// account state before invoking this helper.
func MCPMutationPrincipalFromAuthIdentity(identity auth.Identity) (MCPMutationPrincipal, error) {
	userID, err := strconv.ParseUint(strings.TrimSpace(identity.Subject), 10, 64)
	if err != nil || userID == 0 {
		return MCPMutationPrincipal{}, fmt.Errorf("%w: authenticated subject is not a valid user ID", ErrMCPMutationPrincipal)
	}
	principal := MCPMutationPrincipal{
		UserID:   userID,
		Username: strings.TrimSpace(identity.Username),
		Issuer:   strings.TrimSpace(identity.Issuer),
	}
	if err := principal.Validate(); err != nil {
		return MCPMutationPrincipal{}, err
	}
	return principal, nil
}

// WithMCPMutationPrincipal binds a validated principal to a trusted invocation
// context. Invalid or zero principals are removed rather than persisted.
func WithMCPMutationPrincipal(input map[string]any, principal MCPMutationPrincipal) map[string]any {
	if input == nil {
		input = map[string]any{}
	}
	if principal.Validate() != nil {
		delete(input, cfgKeyMCPMutationPrincipal)
		return input
	}
	input[cfgKeyMCPMutationPrincipal] = principal
	return input
}

func mcpMutationPrincipalFromInput(input map[string]any) MCPMutationPrincipal {
	if input == nil {
		return MCPMutationPrincipal{}
	}
	principal, ok := input[cfgKeyMCPMutationPrincipal].(MCPMutationPrincipal)
	if !ok || principal.Validate() != nil {
		return MCPMutationPrincipal{}
	}
	return principal
}

func mcpMutationPrincipalFromRuntimeConfig(config map[string]any) MCPMutationPrincipal {
	runtime := mapStringAny(config[cfgKeyAgentRuntime])
	if runtime == nil {
		return MCPMutationPrincipal{}
	}
	principal, ok := runtime[cfgKeyMCPMutationPrincipal].(MCPMutationPrincipal)
	if !ok || principal.Validate() != nil {
		return MCPMutationPrincipal{}
	}
	return principal
}

// denyMCPMutationAliasesWithoutPrincipal keeps background/event origins
// fail-closed even if a future registry path accidentally exposes a mutation
// proposal adapter. B2 itself does not register that adapter.
func denyMCPMutationAliasesWithoutPrincipal(allowlist []string, principal MCPMutationPrincipal) error {
	if principal.Validate() == nil {
		return nil
	}
	policy := CurrentMCPMutationPolicy()
	if !policy.Enabled {
		return nil
	}
	for _, alias := range allowlist {
		if _, ok := policy.OperationByAlias(alias); ok {
			return fmt.Errorf("%w: MCP mutation alias %q requires an authenticated principal", ErrMCPMutationPrincipal, alias)
		}
	}
	return nil
}
