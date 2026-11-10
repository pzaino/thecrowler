package agent

import (
	"context"
	"os"
	"strings"
	"testing"

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
