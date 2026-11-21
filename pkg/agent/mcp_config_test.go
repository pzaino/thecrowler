package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func validMCPConfig(command string) cfg.MCPConfig {
	return cfg.MCPConfig{Servers: []cfg.MCPServerConfig{{
		ID: "fixture", Enabled: true, Transport: "stdio", Command: command,
		Args:          []string{"-test.run=^TestMCPFixtureProcess$"},
		ApprovedTools: []cfg.MCPApprovedTool{{Name: "echo", ReadOnly: true}},
	}}}
}

func TestMCPToolAliasIsStableAndBounded(t *testing.T) {
	alias := MCPToolAlias("alpha", "echo")
	if alias != "mcp_alpha_echo" {
		t.Fatalf("alias = %q", alias)
	}
	long := MCPToolAlias("alpha", strings.Repeat("tool.", 40))
	if len(long) > maxToolNameLen || !toolNamePattern.MatchString(long) {
		t.Fatalf("long alias is invalid: %q", long)
	}
	if long != MCPToolAlias("alpha", strings.Repeat("tool.", 40)) {
		t.Fatalf("alias is not deterministic")
	}
}

func TestSetMCPOperatorConfigForApprovalDoesNotRequireCommand(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-mcp-command")
	config := validMCPConfig(missing)
	if err := SetMCPOperatorConfig(config); err == nil {
		t.Fatalf("set operator config accepted a missing command")
	}
	if err := SetMCPOperatorConfigForApproval(config); err != nil {
		t.Fatalf("approval policy rejected a missing command: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	if approved, ok := CurrentMCPApprovedTool(MCPToolAlias("fixture", "echo")); !ok || approved.ServerID != "fixture" {
		t.Fatalf("approval approved-tool lookup failed: %+v ok=%v", approved, ok)
	}
}

func TestMCPOperatorConfigValidation(t *testing.T) {
	command := os.Args[0]
	valid := validMCPConfig(command)
	if err := SetMCPOperatorConfig(valid); err != nil {
		t.Fatalf("valid MCP config rejected: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	if approved, ok := CurrentMCPApprovedTool(MCPToolAlias("fixture", "echo")); !ok || approved.ServerID != "fixture" {
		t.Fatalf("approved tool lookup failed: %+v ok=%v", approved, ok)
	}

	cases := []struct {
		name   string
		mutate func(*cfg.MCPConfig)
	}{
		{"invalid server id", func(c *cfg.MCPConfig) { c.Servers[0].ID = "Bad_ID" }},
		{"duplicate server id", func(c *cfg.MCPConfig) { c.Servers = append(c.Servers, c.Servers[0]) }},
		{"relative command", func(c *cfg.MCPConfig) { c.Servers[0].Command = "./fixture" }},
		{"shell command", func(c *cfg.MCPConfig) { c.Servers[0].Command = "/bin/sh" }},
		{"unsupported transport", func(c *cfg.MCPConfig) { c.Servers[0].Transport = "streamable-http" }},
		{"mutation tool", func(c *cfg.MCPConfig) { c.Servers[0].ApprovedTools[0].ReadOnly = false }},
		{"duplicate tool", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools = append(c.Servers[0].ApprovedTools, c.Servers[0].ApprovedTools[0])
		}},
		{"alias collision", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools = []cfg.MCPApprovedTool{{Name: "a.b", ReadOnly: true}, {Name: "a_b", ReadOnly: true}}
		}},
		{"secret environment inheritance", func(c *cfg.MCPConfig) {
			c.Servers[0].EnvironmentAllowlist = []string{"CROWLER_DB_PASSWORD"}
		}},
		{"secret environment suffix", func(c *cfg.MCPConfig) {
			c.Servers[0].EnvironmentAllowlist = []string{"SERVICE_API_TOKEN"}
		}},
		{"invalid source", func(c *cfg.MCPConfig) { c.Servers[0].AllowedSources = []uint64{0} }},
		{"bad limit", func(c *cfg.MCPConfig) { c.Servers[0].MaxCallSeconds = 1000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := validMCPConfig(command)
			tc.mutate(&config)
			if err := SetMCPOperatorConfig(config); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			_ = SetMCPOperatorConfig(cfg.MCPConfig{})
		})
	}
}

func TestMCPEnvironmentDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("CROWLER_DB_PASSWORD", "secret-db")
	t.Setenv("MCP_ALLOWED_TEST", "allowed")
	server := cfg.MCPServerConfig{
		EnvironmentAllowlist: []string{"MCP_ALLOWED_TEST"},
		Environment:          map[string]string{"EXPLICIT_VALUE": "explicit"},
	}
	env := buildMCPEnvironment(server)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "MCP_ALLOWED_TEST=allowed") || !strings.Contains(joined, "EXPLICIT_VALUE=explicit") {
		t.Fatalf("approved environment missing: %v", env)
	}
	if strings.Contains(joined, "secret-db") || strings.Contains(joined, "CROWLER_DB_PASSWORD") {
		t.Fatalf("sensitive environment inherited: %v", env)
	}
}

func TestMCPDisabledServerNeedsNoCommand(t *testing.T) {
	config := cfg.MCPConfig{Servers: []cfg.MCPServerConfig{{ID: "disabled", Enabled: false}}}
	if err := SetMCPOperatorConfig(config); err != nil {
		t.Fatalf("disabled server config rejected: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	if _, ok := CurrentMCPApprovedTool(MCPToolAlias("disabled", "echo")); ok {
		t.Fatalf("disabled server exposed an approved tool")
	}
}

func TestMCPManagerDoesNotStartUnreferencedServer(t *testing.T) {
	server := validMCPConfig(os.Args[0]).Servers[0]
	server.ID = "unused"
	setMCPTestConfig(t, cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}})
	manager, err := NewMCPToolManager(context.Background(), cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}, mcpTestIdentity(), []string{"get_source_status"}, nil)
	if err != nil {
		t.Fatalf("unreferenced MCP server caused startup: %v", err)
	}
	if manager != nil {
		_ = manager.Close()
		t.Fatalf("unreferenced MCP server started a manager")
	}
}

func TestMCPGoalSpecValidationUsesOperatorInventory(t *testing.T) {
	server := validMCPConfig(os.Args[0]).Servers[0]
	setMCPTestConfig(t, cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}})
	alias := MCPToolAlias("fixture", "echo")
	spec := &AgentGoalSpec{
		Allowlist: []string{alias},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "mcp-observation", Description: "external observation exists",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceToolObservation,
				Where: &EvidenceSelector{Tool: alias}},
		}}},
	}
	if err := validateGoalTools(spec); err != nil {
		t.Fatalf("approved MCP goal tool rejected: %v", err)
	}
	spec.Allowlist = []string{"mcp_unapproved_tool"}
	spec.Acceptance.Criteria[0].Predicate.Where.Tool = "mcp_unapproved_tool"
	if err := validateGoalTools(spec); err == nil {
		t.Fatalf("unapproved MCP goal tool was accepted")
	}
}

func validMCPHTTPConfig() cfg.MCPServerConfig {
	return cfg.MCPServerConfig{
		ID: "remote", Enabled: true, Transport: mcpTransportStreamableHTTP,
		Endpoint:      "https://mcp.example.com/mcp",
		ApprovedTools: []cfg.MCPApprovedTool{{Name: "echo", ReadOnly: true}},
	}
}

func TestMCPHTTPOperatorConfigValidation(t *testing.T) {
	if err := SetMCPOperatorConfig(mcpTestConfigFor(validMCPHTTPConfig())); err != nil {
		t.Fatalf("valid HTTP MCP config rejected: %v", err)
	}
	if approved, ok := CurrentMCPApprovedTool(MCPToolAlias("remote", "echo")); !ok || approved.ServerID != "remote" {
		t.Fatalf("HTTP approved tool lookup failed: %+v ok=%v", approved, ok)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })

	cases := []struct {
		name   string
		mutate func(*cfg.MCPServerConfig)
	}{
		{"remote plain HTTP", func(c *cfg.MCPServerConfig) { c.Endpoint = "http://mcp.example.com/mcp" }},
		{"non-loopback insecure HTTP", func(c *cfg.MCPServerConfig) {
			c.Endpoint = "http://mcp.example.com/mcp"
			c.Network.AllowInsecureLoopback = true
		}},
		{"userinfo", func(c *cfg.MCPServerConfig) { c.Endpoint = "https://user:pass@mcp.example.com/mcp" }},
		{"fragment", func(c *cfg.MCPServerConfig) { c.Endpoint = "https://mcp.example.com/mcp#fragment" }},
		{"query", func(c *cfg.MCPServerConfig) { c.Endpoint = "https://mcp.example.com/mcp?token=bad" }},
		{"unsupported scheme", func(c *cfg.MCPServerConfig) { c.Endpoint = "ftp://mcp.example.com/mcp" }},
		{"ambiguous path", func(c *cfg.MCPServerConfig) { c.Endpoint = "https://mcp.example.com/a/../mcp" }},
		{"invalid port", func(c *cfg.MCPServerConfig) { c.Endpoint = "https://mcp.example.com:notaport/mcp" }},
		{"public private-CIDR exception", func(c *cfg.MCPServerConfig) {
			c.Network.AllowedPrivateCIDRs = []string{"8.8.8.0/24"}
		}},
		{"private CIDR spanning public range", func(c *cfg.MCPServerConfig) {
			c.Network.AllowedPrivateCIDRs = []string{"192.168.0.0/15"}
		}},
		{"bearer without source", func(c *cfg.MCPServerConfig) { c.Auth.Mode = "bearer" }},
		{"bearer with two sources", func(c *cfg.MCPServerConfig) {
			c.Auth = cfg.MCPHTTPAuthConfig{Mode: "bearer", TokenEnv: "MCP_TOKEN", TokenFile: "/tmp/mcp-token"}
		}},
		{"stdio HTTP fields", func(c *cfg.MCPServerConfig) {
			c.Transport = mcpTransportStdio
			c.Command = "/bin/true"
			c.Endpoint = "https://mcp.example.com/mcp"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := validMCPHTTPConfig()
			tc.mutate(&server)
			if err := SetMCPOperatorConfig(mcpTestConfigFor(server)); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			_ = SetMCPOperatorConfig(cfg.MCPConfig{})
		})
	}
}

func TestMCPLoopbackHTTPFixturePolicyIsExplicit(t *testing.T) {
	server := validMCPHTTPConfig()
	server.Endpoint = "http://127.0.0.1:8080/mcp"
	server.Network = cfg.MCPHTTPNetworkConfig{
		AllowInsecureLoopback: true,
		AllowedPrivateCIDRs:   []string{"127.0.0.1/32"},
	}
	if err := SetMCPOperatorConfig(mcpTestConfigFor(server)); err != nil {
		t.Fatalf("explicit loopback HTTP test policy rejected: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
}

func TestMCPMutationEffectClassification(t *testing.T) {
	base := validMCPConfig("/bin/true")
	mutation := cfg.MCPApprovedTool{
		Name: "mutate_counter", Effect: "mutation", AllowMutationProposal: true,
		Description: "fixture mutation", RequiredCapabilities: []string{"tool_execution"},
	}
	server := base.Servers[0]
	server.ApprovedTools = []cfg.MCPApprovedTool{mutation}
	if err := SetMCPOperatorConfig(cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}); err != nil {
		t.Fatalf("explicit mutation tool rejected: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	alias := MCPToolAlias("fixture", "mutate_counter")
	if _, ok := CurrentMCPApprovedMutationTool(alias); !ok {
		t.Fatalf("mutation approval lookup failed")
	}
	if _, ok := CurrentMCPApprovedTool(alias); ok {
		t.Fatalf("mutation tool exposed through read-only lookup")
	}

	cases := []struct {
		name string
		tool cfg.MCPApprovedTool
	}{
		{"read_only false without effect", cfg.MCPApprovedTool{Name: "mutate_counter", ReadOnly: false}},
		{"unknown effect", cfg.MCPApprovedTool{Name: "mutate_counter", Effect: "write"}},
		{"mutation marked read-only", cfg.MCPApprovedTool{Name: "mutate_counter", ReadOnly: true, Effect: "mutation", AllowMutationProposal: true}},
		{"mutation without proposal opt-in", cfg.MCPApprovedTool{Name: "mutate_counter", Effect: "mutation"}},
		{"read-only effect with false flag", cfg.MCPApprovedTool{Name: "mutate_counter", Effect: "read_only"}},
		{"mutation proposal flag on read-only", cfg.MCPApprovedTool{Name: "mutate_counter", ReadOnly: true, Effect: "read_only", AllowMutationProposal: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := base.Servers[0]
			server.ApprovedTools = []cfg.MCPApprovedTool{tc.tool}
			if err := SetMCPOperatorConfig(cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			_ = SetMCPOperatorConfig(cfg.MCPConfig{})
		})
	}
}

func TestDocumentedMutationInventoryUsesNormalizedServerIdentity(t *testing.T) {
	server := cfg.MCPServerConfig{
		ID: "approved-server", Enabled: true, Transport: "stdio", Command: "/bin/echo",
		ApprovedTools: []cfg.MCPApprovedTool{{
			Name: "approved_tool", Effect: "mutation", AllowMutationProposal: true,
			RequiredCapabilities: []string{"tool_execution"}, AllowedSources: []uint64{42},
		}},
	}
	normalized, err := normalizeMCPOperatorConfig(cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}})
	if err != nil {
		t.Fatalf("documented mutation inventory rejected: %v", err)
	}
	if normalized.Servers[0].ID != "approved-server" || normalized.Servers[0].ApprovedTools[0].Name != "approved_tool" {
		t.Fatalf("unexpected normalized inventory: %+v", normalized.Servers[0])
	}
	alias := MCPToolAlias("approved-server", "approved_tool")
	if alias != "mcp_approved-server_approved_tool" {
		t.Fatalf("documented alias = %q", alias)
	}
	policy := MCPMutationPolicy{
		Enabled: true, PolicyID: "approved-policy", Version: "1",
		Operations: []MCPMutationOperationPolicy{{
			ServerID: "approved-server", ToolName: "approved_tool", ToolAlias: alias,
			AllowedAgents: []string{"agent-id"}, RequiredCapabilities: []string{"tool_execution"},
			MinTrustLevel: "trusted", AllowedSources: []uint64{42},
			RequiredArgumentFields: []string{"source_id"}, AllowedArgumentFields: []string{"source_id"},
			ProposalTTL: 5 * time.Minute, MaxProposalsPerRun: 4, MaxPendingProposals: 8,
			MaxInputBytes: 65536, MaxOutputBytes: 65536, RequireApproval: true,
			ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
		}},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("documented mutation policy rejected: %v", err)
	}
	invalid := server
	invalid.ID = "approved_server"
	if _, err := normalizeMCPServerConfig(invalid); err == nil {
		t.Fatalf("underscore server ID was accepted by production normalization")
	}
}
