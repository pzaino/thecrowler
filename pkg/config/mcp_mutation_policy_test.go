package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qri-io/jsonschema"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const validMCPMutationPolicyYAML = `crawler:
  workers: 1
mcp:
  servers:
    - id: approved-server
      enabled: true
      transport: stdio
      command: /bin/echo
      allowed_agents: [agent-id]
      allowed_sources: [42]
      approved_tools:
        - name: approved_tool
          effect: mutation
          allow_mutation_proposal: true
          required_capabilities: [tool_execution]
          allowed_sources: [42]
  mutation_policy:
    enabled: true
    policy_id: operator-approved-mutations
    version: "1"
    operations:
      - server_id: approved-server
        tool_name: approved_tool
        allowed_agents: [agent-id]
        required_capabilities: [tool_execution]
        min_trust_level: trusted
        allowed_sources: [42]
        allowed_resources: []
        required_argument_fields: [source_id]
        allowed_argument_fields: [source_id]
        destructive: false
        proposal_ttl: 5m
        max_proposals_per_run: 4
        max_pending_proposals: 8
        max_input_bytes: 65536
        max_output_bytes: 65536
        require_approval: true
        execution_backend: none
        allowed_transports: [stdio]
        audit_required: true
`

func mcpPolicyRemoteBaseConfig() Config {
	return Config{Remote: Remote{
		Host: testURL, Path: "config.yaml", Timeout: 10, SSLMode: cmn.DisableStr,
	}}
}

func assertMCPPolicyLoadsEverywhere(t *testing.T, payload string) *Config {
	t.Helper()
	parsed, err := ParseConfig([]byte(payload))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fileConfig, err := getConfigFile(path)
	if err != nil {
		t.Fatalf("getConfigFile: %v", err)
	}
	if err := fileConfig.Validate(); err != nil {
		t.Fatalf("getConfigFile Validate: %v", err)
	}
	remoteConfig, err := LoadRemoteConfig(mcpPolicyRemoteBaseConfig(), &MockFetcher{Body: payload})
	if err != nil {
		t.Fatalf("LoadRemoteConfig: %v", err)
	}
	if parsed.MCP.MutationPolicy.Enabled != fileConfig.MCP.MutationPolicy.Enabled ||
		parsed.MCP.MutationPolicy.PolicyID != remoteConfig.MCP.MutationPolicy.PolicyID {
		t.Fatalf("loader policies diverged: parse=%+v file=%+v remote=%+v",
			parsed.MCP.MutationPolicy, fileConfig.MCP.MutationPolicy, remoteConfig.MCP.MutationPolicy)
	}
	return parsed
}

func assertMCPPolicyRejectedEverywhere(t *testing.T, payload string) {
	t.Helper()
	if _, err := ParseConfig([]byte(payload)); err == nil {
		t.Fatalf("ParseConfig accepted invalid policy:\n%s", payload)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := getConfigFile(path); err == nil {
		t.Fatalf("getConfigFile accepted invalid policy:\n%s", payload)
	}
	if _, err := LoadRemoteConfig(mcpPolicyRemoteBaseConfig(), &MockFetcher{Body: payload}); err == nil {
		t.Fatalf("LoadRemoteConfig accepted invalid policy:\n%s", payload)
	}
}

func TestMCPMutationPolicyValidLoadPaths(t *testing.T) {
	parsed := assertMCPPolicyLoadsEverywhere(t, validMCPMutationPolicyYAML)
	policy := parsed.MCP.MutationPolicy
	if !policy.Enabled || policy.PolicyID != "operator-approved-mutations" || policy.Version != "1" {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if len(policy.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(policy.Operations))
	}
	operation := policy.Operations[0]
	if operation.ServerID != "approved-server" || operation.ToolName != "approved_tool" ||
		operation.ProposalTTL != "5m" || operation.ExecutionBackend != "none" {
		t.Fatalf("unexpected operation: %+v", operation)
	}

	// Read-only MCP configuration without a policy remains valid and disabled.
	readOnly := `crawler:
  workers: 1
mcp:
  servers:
    - id: read-only
      enabled: true
      transport: stdio
      command: /bin/echo
      approved_tools:
        - name: echo
          read_only: true
`
	parsedReadOnly, err := ParseConfig([]byte(readOnly))
	if err != nil {
		t.Fatalf("read-only MCP config rejected: %v", err)
	}
	if parsedReadOnly.MCP.MutationPolicy.Enabled {
		t.Fatalf("read-only MCP config unexpectedly enabled a mutation policy")
	}
}

func TestMCPMutationPolicyStrictRejections(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{
			name: "unknown policy key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"require_approval: true", "require_aproval: true", 1),
		},
		{
			name: "unknown mcp key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"  servers:", "  serverz:", 1),
		},
		{
			name: "duplicate mcp key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"mcp:\n", "mcp:\n  mutation_policy: {enabled: false}\nmcp:\n", 1),
		},
		{
			name: "duplicate mutation policy key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"mcp:\n", "mcp:\n  mutation_policy: {enabled: false}\n", 1),
		},
		{
			name: "duplicate nested key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"        require_approval: true", "        require_approval: false\n        require_approval: true", 1),
		},
		{
			name: "unicode format control in policy id",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"policy_id: operator-approved-mutations",
				`policy_id: "operator\u202Eapproved"`, 1),
		},
		{
			name: "missing policy id",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"    policy_id: operator-approved-mutations\n", "", 1),
		},
		{
			name: "missing policy version",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"    version: \"1\"\n", "", 1),
		},
		{
			name: "invalid duration",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"proposal_ttl: 5m", "proposal_ttl: not-a-duration", 1),
		},
		{
			name: "null scalar",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"policy_id: operator-approved-mutations", "policy_id: null", 1),
		},
		{
			name: "bool as string",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"require_approval: true", `require_approval: "true"`, 1),
		},
		{
			name: "float as integer",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"max_proposals_per_run: 4", "max_proposals_per_run: 1.5", 1),
		},
		{
			name: "integer as string",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"policy_id: operator-approved-mutations", "policy_id: 123", 1),
		},
		{
			name: "unsupported transport",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"allowed_transports: [stdio]", "allowed_transports: [websocket]", 1),
		},
		{
			name: "positive execution backend",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"execution_backend: none", "execution_backend: remote", 1),
		},
		{
			name: "approval false",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"require_approval: true", "require_approval: false", 1),
		},
		{
			name: "audit false",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"audit_required: true", "audit_required: false", 1),
		},
		{
			name: "mixed scope",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"allowed_resources: []", "allowed_resources: [fixture-resource]", 1),
		},
		{
			name: "source outside inventory scope",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"        allowed_sources: [42]\n        allowed_resources: []",
				"        allowed_sources: [43]\n        allowed_resources: []", 1),
		},
		{
			name: "missing server",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"server_id: approved-server", "server_id: other-server", 1),
		},
		{
			name: "disabled server",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"id: approved-server\n      enabled: true", "id: approved-server\n      enabled: false", 1),
		},
		{
			name: "missing tool",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"tool_name: approved_tool", "tool_name: other_tool", 1),
		},
		{
			name: "read-only tool",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"effect: mutation", "effect: read_only", 1),
		},
		{
			name: "missing mutation proposal opt-in",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"          allow_mutation_proposal: true\n", "", 1),
		},
		{
			name: "tool capability missing from policy",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"          required_capabilities: [tool_execution]",
				"          required_capabilities: [tool_execution, admin_write]", 1),
		},
		{
			name: "agent outside server allowlist",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"allowed_agents: [agent-id]", "allowed_agents: [other-agent]", 1),
		},
		{
			name: "empty enabled operations",
			payload: `crawler:
  workers: 1
mcp:
  servers:
    - id: approved-server
      enabled: true
      transport: stdio
      command: /bin/echo
      approved_tools:
        - name: approved_tool
          effect: mutation
          allow_mutation_proposal: true
  mutation_policy:
    enabled: true
    policy_id: operator-approved-mutations
    version: "1"
    operations: []
`,
		},
		{
			name: "invalid server id",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"server_id: approved-server", "server_id: approved_server", 1),
		},
		{
			name: "yaml anchor",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"policy_id: operator-approved-mutations", "policy_id: &policy operator-approved-mutations", 1),
		},
		{
			name: "yaml merge key",
			payload: strings.Replace(validMCPMutationPolicyYAML,
				"        min_trust_level: trusted", "        <<: {min_trust_level: trusted}", 1),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertMCPPolicyRejectedEverywhere(t, test.payload)
		})
	}
}

func TestMCPMutationPolicyJSONStrictness(t *testing.T) {
	validJSON := `{"crawler":{"workers":1},"mcp":{"servers":[{"id":"approved-server","enabled":true,"transport":"stdio","command":"/bin/echo","allowed_agents":["agent-id"],"allowed_sources":[42],"approved_tools":[{"name":"approved_tool","effect":"mutation","allow_mutation_proposal":true,"required_capabilities":["tool_execution"],"allowed_sources":[42]}]}],"mutation_policy":{"enabled":true,"policy_id":"operator-approved-mutations","version":"1","operations":[{"server_id":"approved-server","tool_name":"approved_tool","allowed_agents":["agent-id"],"required_capabilities":["tool_execution"],"min_trust_level":"trusted","allowed_sources":[42],"allowed_resources":[],"required_argument_fields":["source_id"],"allowed_argument_fields":["source_id"],"destructive":false,"proposal_ttl":"5m","max_proposals_per_run":4,"max_pending_proposals":8,"max_input_bytes":65536,"max_output_bytes":65536,"require_approval":true,"execution_backend":"none","allowed_transports":["stdio"],"audit_required":true}]}}}`
	parsed := assertMCPPolicyLoadsEverywhere(t, validJSON)
	if !parsed.MCP.MutationPolicy.Enabled {
		t.Fatalf("valid JSON policy was not enabled")
	}

	for _, test := range []struct {
		name    string
		payload string
	}{
		{"duplicate policy key", `{"mcp":{"mutation_policy":{"enabled":true,"enabled":false,"operations":[]}}}`},
		{"duplicate outer mcp", `{"mcp":{"mutation_policy":{"enabled":false}},"mcp":{"mutation_policy":{"enabled":true}}}`},
		{"unknown key", `{"mcp":{"mutation_policy":{"enabled":true,"require_aproval":false,"operations":[]}}}`},
		{"wrong type", `{"mcp":{"mutation_policy":{"enabled":"true"}}}`},
		{"invalid duration", `{"mcp":{"mutation_policy":{"enabled":true,"policy_id":"p","version":"1","operations":[{"server_id":"approved-server","tool_name":"approved_tool","allowed_agents":["a"],"required_capabilities":["tool_execution"],"min_trust_level":"trusted","allowed_sources":[42],"proposal_ttl":"nope","max_proposals_per_run":1,"max_pending_proposals":1,"max_input_bytes":1,"max_output_bytes":1,"require_approval":true,"execution_backend":"none","allowed_transports":["stdio"],"audit_required":true}]}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertMCPPolicyRejectedEverywhere(t, test.payload)
		})
	}
}

func TestMCPMutationPolicyIncludeExpansionStrict(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte("mcp:\n"+strings.SplitN(validMCPMutationPolicyYAML, "mcp:\n", 2)[1]), 0o600); err != nil {
		t.Fatalf("write included policy: %v", err)
	}
	mainPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(mainPath, []byte("include: policy.yaml\ncrawler:\n  workers: 1\n"), 0o600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	parsed, err := getConfigFile(mainPath)
	if err != nil {
		t.Fatalf("included valid policy rejected: %v", err)
	}
	if !parsed.MCP.MutationPolicy.Enabled {
		t.Fatalf("included policy was not enabled")
	}

	invalidPolicy := strings.Replace(validMCPMutationPolicyYAML, "require_approval: true", "require_aproval: true", 1)
	if err := os.WriteFile(policyPath, []byte("mcp:\n"+strings.SplitN(invalidPolicy, "mcp:\n", 2)[1]), 0o600); err != nil {
		t.Fatalf("write invalid included policy: %v", err)
	}
	if _, err := getConfigFile(mainPath); err == nil {
		t.Fatalf("included invalid policy was accepted")
	}
}

func TestMCPMutationPolicyUnrelatedLegacyParserStillPermissive(t *testing.T) {
	payload := "crawler:\n  workers: 1\n  workers: 2\n  future_option: ignored\n"
	parsed, err := ParseConfig([]byte(payload))
	if err != nil {
		t.Fatalf("legacy unrelated config rejected: %v", err)
	}
	if parsed.Crawler.Workers != 2 {
		t.Fatalf("duplicate legacy key last value = %d, want 2", parsed.Crawler.Workers)
	}
}

func TestMCPMutationPolicyValidationStablePaths(t *testing.T) {
	policy := MCPMutationPolicyConfig{
		Enabled: true, PolicyID: "p", Version: "1",
		Operations: []MCPMutationOperationConfig{{
			ServerID: "approved-server", ToolName: "approved_tool",
			AllowedAgents: []string{"agent-id"}, RequiredCapabilities: []string{"tool_execution"},
			MinTrustLevel: "trusted", AllowedSources: []uint64{42},
			ProposalTTL: "5m", MaxProposalsPerRun: 1, MaxPendingProposals: 1,
			MaxInputBytes: 1, MaxOutputBytes: 1, RequireApproval: true,
			ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
		}},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	clone := policy.Clone()
	clone.Operations[0].AllowedAgents[0] = "mutated"
	if policy.Operations[0].AllowedAgents[0] != "agent-id" {
		t.Fatalf("policy clone shares caller slices")
	}
	if got := fmt.Sprintf("%+v", policy); strings.Contains(got, "fixture-secret") {
		t.Fatalf("policy formatting leaked a resource")
	}
}

func TestMCPMutationPolicySchemaMatchesRuntime(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/crowler-config-schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	properties, ok := document["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	mcp, ok := properties["mcp"].(map[string]any)
	if !ok {
		t.Fatal("mcp schema is missing")
	}
	mcpProperties, ok := mcp["properties"].(map[string]any)
	if !ok {
		t.Fatal("mcp schema has no properties")
	}
	if _, ok := mcpProperties["mutation_policy"]; !ok {
		t.Fatal("mutation_policy schema is missing")
	}
	// The policy schema references operation definitions. Embed the document's
	// definitions and compile the referenced policy definition directly so the
	// sub-schema can be validated in isolation.
	definitions, ok := document["definitions"].(map[string]any)
	if !ok {
		t.Fatal("schema has no definitions")
	}
	policyRaw, ok := definitions["mcpMutationPolicy"].(map[string]any)
	if !ok {
		t.Fatal("mcpMutationPolicy definition is missing")
	}
	policyRaw = cloneJSONMap(policyRaw)
	policyProperties := policyRaw["properties"].(map[string]any)
	operations := policyProperties["operations"].(map[string]any)
	operationDefinition, ok := definitions["mcpMutationPolicyOperation"].(map[string]any)
	if !ok {
		t.Fatal("mcpMutationPolicyOperation definition is missing")
	}
	// Inline the operation definition for this isolated sub-schema test. The
	// production schema keeps the $ref and is validated by the full-document
	// tooling.
	operations["items"] = operationDefinition
	encoded, err := json.Marshal(policyRaw)
	if err != nil {
		t.Fatalf("marshal policy schema: %v", err)
	}
	schema := &jsonschema.Schema{}
	if err := schema.UnmarshalJSON(encoded); err != nil {
		t.Fatalf("compile policy schema: %v", err)
	}

	operation := map[string]any{
		"server_id": "approved-server", "tool_name": "approved_tool",
		"allowed_agents": []string{"agent-id"}, "required_capabilities": []string{"tool_execution"},
		"min_trust_level": "trusted", "allowed_sources": []uint64{42},
		"proposal_ttl": "5m", "max_proposals_per_run": 4, "max_pending_proposals": 8,
		"max_input_bytes": 65536, "max_output_bytes": 65536,
		"require_approval": true, "execution_backend": "none",
		"allowed_transports": []string{"stdio"}, "audit_required": true,
	}
	valid := map[string]any{
		"enabled": true, "policy_id": "p", "version": "1",
		"operations": []any{operation},
	}
	validate := func(value any) []jsonschema.KeyError {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		issues, err := schema.ValidateBytes(context.Background(), payload)
		if err != nil {
			t.Fatalf("schema validation: %v", err)
		}
		return issues
	}
	if issues := validate(valid); len(issues) != 0 {
		t.Fatalf("valid policy failed schema: %v", issues)
	}

	negative := []struct {
		name  string
		value any
	}{
		{"unknown key", map[string]any{"enabled": false, "require_aproval": false}},
		{"approval false", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "require_approval", false)}}},
		{"audit false", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "audit_required", false)}}},
		{"backend remote", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "execution_backend", "remote")}}},
		{"bad transport", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "allowed_transports", []string{"websocket"})}}},
		{"bad server id", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "server_id", "approved_server")}}},
		{"mixed scope", map[string]any{"enabled": true, "policy_id": "p", "version": "1", "operations": []any{mergePolicyOperation(operation, "allowed_resources", []string{"fixture-resource"})}}},
	}
	for _, test := range negative {
		t.Run(test.name, func(t *testing.T) {
			if issues := validate(test.value); len(issues) == 0 {
				t.Fatalf("invalid policy passed schema: %+v", test.value)
			}
		})
	}
}

func mergePolicyOperation(base map[string]any, key string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

func cloneJSONMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestMCPToolAliasMatchesRuntimeContract(t *testing.T) {
	if got := MCPToolAlias("approved-server", "approved_tool"); got != "mcp_approved-server_approved_tool" {
		t.Fatalf("alias = %q", got)
	}
	long := MCPToolAlias("alpha", strings.Repeat("tool.", 40))
	if len(long) > MCPMaxToolAliasBytes || long == "" {
		t.Fatalf("long alias is invalid: %q", long)
	}
	if long != MCPToolAlias("alpha", strings.Repeat("tool.", 40)) {
		t.Fatalf("long alias is not deterministic")
	}
}

func TestMCPMutationPolicyRejectsDuplicateDerivedAliases(t *testing.T) {
	operation := MCPMutationOperationConfig{
		ServerID: "approved-server", ToolName: "a.b",
		AllowedAgents: []string{"agent-id"}, RequiredCapabilities: []string{"tool_execution"},
		MinTrustLevel: "trusted", AllowedSources: []uint64{42},
		ProposalTTL: "5m", MaxProposalsPerRun: 1, MaxPendingProposals: 1,
		MaxInputBytes: 1, MaxOutputBytes: 1, RequireApproval: true,
		ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
	}
	second := operation
	second.ToolName = "a_b"
	policy := MCPMutationPolicyConfig{Enabled: true, PolicyID: "p", Version: "1", Operations: []MCPMutationOperationConfig{operation, second}}
	if err := policy.Validate(); err == nil {
		t.Fatalf("policy with duplicate derived aliases was accepted")
	}
}

func TestMCPMutationPolicyInventoryAliasCollision(t *testing.T) {
	servers := []MCPServerConfig{{
		ID: "approved-server", Enabled: true, Transport: "stdio",
		ApprovedTools: []MCPApprovedTool{
			{Name: "a.b", Effect: "mutation", AllowMutationProposal: true},
			{Name: "a_b", Effect: "mutation", AllowMutationProposal: true},
		},
	}}
	policy := MCPMutationPolicyConfig{
		Enabled: true, PolicyID: "p", Version: "1",
		Operations: []MCPMutationOperationConfig{{
			ServerID: "approved-server", ToolName: "a.b",
			AllowedAgents: []string{"agent-id"}, RequiredCapabilities: []string{"tool_execution"},
			MinTrustLevel: "trusted", AllowedSources: []uint64{42},
			ProposalTTL: "5m", MaxProposalsPerRun: 1, MaxPendingProposals: 1,
			MaxInputBytes: 1, MaxOutputBytes: 1, RequireApproval: true,
			ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
		}},
	}
	if err := policy.ValidateInventory(servers); err == nil {
		t.Fatalf("inventory with a derived alias collision was accepted")
	}
}

func TestMCPMutationPolicyExampleLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/mcp-mutation-policy.yaml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	parsed, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("example rejected: %v", err)
	}
	policy := parsed.MCP.MutationPolicy
	if !policy.Enabled || len(policy.Operations) != 1 {
		t.Fatalf("example policy missing: %+v", policy)
	}
	if got := MCPToolAlias(policy.Operations[0].ServerID, policy.Operations[0].ToolName); got != "mcp_approved-server_approved_tool" {
		t.Fatalf("example alias = %q", got)
	}
}
