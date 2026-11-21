package agent

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func mcpPolicyHash(policy MCPMutationPolicy) string {
	return policy.SHA256()
}

func validMCPMutationPolicyConfig() cfg.MCPConfig {
	return cfg.MCPConfig{
		Servers: []cfg.MCPServerConfig{{
			ID: "approved-server", Enabled: true, Transport: "stdio", Command: "/bin/echo",
			AllowedAgents: []string{"agent-id"}, AllowedSources: []uint64{42},
			ApprovedTools: []cfg.MCPApprovedTool{{
				Name: "approved_tool", Effect: "mutation", AllowMutationProposal: true,
				RequiredCapabilities: []string{"tool_execution"}, AllowedSources: []uint64{42},
			}},
		}},
		MutationPolicy: cfg.MCPMutationPolicyConfig{
			Enabled: true, PolicyID: "operator-approved-mutations", Version: "1",
			Operations: []cfg.MCPMutationOperationConfig{{
				ServerID: "approved-server", ToolName: "approved_tool",
				AllowedAgents: []string{"agent-id"}, RequiredCapabilities: []string{"tool_execution"},
				MinTrustLevel: "trusted", AllowedSources: []uint64{42},
				RequiredArgumentFields: []string{"source_id"}, AllowedArgumentFields: []string{"source_id"},
				ProposalTTL: "5m", MaxProposalsPerRun: 4, MaxPendingProposals: 8,
				MaxInputBytes: 65536, MaxOutputBytes: 65536, RequireApproval: true,
				ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
			}},
		},
	}
}

func TestMCPMutationPolicySnapshotRetainsAndFreezes(t *testing.T) {
	input := validMCPMutationPolicyConfig()
	if err := SetMCPOperatorConfig(input); err != nil {
		t.Fatalf("set valid policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })

	current := CurrentMCPOperatorConfig()
	if !current.MutationPolicy.Enabled || len(current.MutationPolicy.Operations) != 1 {
		t.Fatalf("policy was not retained: %+v", current.MutationPolicy)
	}
	operation := current.MutationPolicy.Operations[0]
	if operation.ServerID != "approved-server" || operation.ToolName != "approved_tool" {
		t.Fatalf("unexpected operation: %+v", operation)
	}
	runtimePolicy := CurrentMCPMutationPolicy()
	if !runtimePolicy.Enabled || len(runtimePolicy.Operations) != 1 {
		t.Fatalf("runtime policy missing: %+v", runtimePolicy)
	}
	if runtimePolicy.Operations[0].ToolAlias != "mcp_approved-server_approved_tool" {
		t.Fatalf("derived alias = %q", runtimePolicy.Operations[0].ToolAlias)
	}
	if runtimePolicy.Operations[0].ExecutionBackend != "none" || !runtimePolicy.Operations[0].RequireApproval ||
		!runtimePolicy.Operations[0].AuditRequired {
		t.Fatalf("runtime policy lost safety fields: %+v", runtimePolicy.Operations[0])
	}
	hash := runtimePolicy.SHA256()
	if hash == "" {
		t.Fatalf("runtime policy hash is empty")
	}
	fromConfig, err := MCPMutationPolicyFromConfig(input)
	if err != nil {
		t.Fatalf("convert valid policy: %v", err)
	}
	if mcpPolicyHash(fromConfig) != hash {
		t.Fatalf("policy conversion hash changed: %s != %s", mcpPolicyHash(fromConfig), hash)
	}

	// Caller mutation after Set cannot alter the global snapshot.
	input.MutationPolicy.Operations[0].AllowedAgents[0] = "mutated-input"
	input.MutationPolicy.Operations[0].AllowedSources[0] = 99
	input.Servers[0].AllowedAgents[0] = "mutated-server"

	// Returned copies cannot alter the global snapshot either.
	current.MutationPolicy.Operations[0].AllowedAgents[0] = "mutated-getter"
	current.MutationPolicy.Operations[0].AllowedSources[0] = 100
	runtimePolicy.Operations[0].AllowedAgents[0] = "mutated-policy"
	runtimePolicy.Operations[0].AllowedSources[0] = 101

	fresh := CurrentMCPOperatorConfig()
	if fresh.MutationPolicy.Operations[0].AllowedAgents[0] != "agent-id" ||
		fresh.MutationPolicy.Operations[0].AllowedSources[0] != 42 ||
		fresh.Servers[0].AllowedAgents[0] != "agent-id" {
		t.Fatalf("global snapshot was mutated through caller slices: %+v", fresh)
	}
	freshPolicy := CurrentMCPMutationPolicy()
	if freshPolicy.Operations[0].AllowedAgents[0] != "agent-id" ||
		freshPolicy.Operations[0].AllowedSources[0] != 42 ||
		mcpPolicyHash(freshPolicy) != hash {
		t.Fatalf("runtime snapshot was mutated through getter slices: %+v", freshPolicy)
	}
}

func TestMCPMutationPolicyDisabledGrantsNothing(t *testing.T) {
	config := validMCPMutationPolicyConfig()
	config.MutationPolicy = cfg.MCPMutationPolicyConfig{}
	if err := SetMCPOperatorConfig(config); err != nil {
		t.Fatalf("set disabled policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	if CurrentMCPMutationPolicy().Enabled {
		t.Fatalf("disabled policy produced an enabled runtime policy")
	}
	policy, err := MCPMutationPolicyFromConfig(config)
	if err != nil {
		t.Fatalf("disabled policy conversion: %v", err)
	}
	if policy.Enabled || len(policy.Operations) != 0 {
		t.Fatalf("disabled policy granted authority: %+v", policy)
	}
}

func TestMCPMutationPolicyRejectsInventoryMismatch(t *testing.T) {
	base := validMCPMutationPolicyConfig()
	cases := []struct {
		name   string
		mutate func(*cfg.MCPConfig)
	}{
		{"missing server", func(c *cfg.MCPConfig) {
			c.Servers = nil
		}},
		{"disabled server", func(c *cfg.MCPConfig) {
			c.Servers[0].Enabled = false
		}},
		{"missing tool", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools[0].Name = "other_tool"
		}},
		{"read-only tool", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools[0].Effect = "read_only"
			c.Servers[0].ApprovedTools[0].AllowMutationProposal = false
		}},
		{"missing proposal opt-in", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools[0].AllowMutationProposal = false
		}},
		{"tool capability missing from policy", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools[0].RequiredCapabilities = []string{"tool_execution", "admin_write"}
		}},
		{"agent outside server allowlist", func(c *cfg.MCPConfig) {
			c.Servers[0].AllowedAgents = []string{"other-agent"}
		}},
		{"source outside tool scope", func(c *cfg.MCPConfig) {
			c.Servers[0].ApprovedTools[0].AllowedSources = []uint64{43}
		}},
		{"resource scope conflicts with source scope", func(c *cfg.MCPConfig) {
			c.MutationPolicy.Operations[0].AllowedSources = nil
			c.MutationPolicy.Operations[0].AllowedResources = []string{"fixture-resource"}
		}},
		{"transport mismatch", func(c *cfg.MCPConfig) {
			c.MutationPolicy.Operations[0].AllowedTransports = []string{"streamable_http"}
		}},
		{"duplicate operation", func(c *cfg.MCPConfig) {
			c.MutationPolicy.Operations = append(c.MutationPolicy.Operations, c.MutationPolicy.Operations[0])
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.Servers = cloneMCPConfig(base).Servers
			config.MutationPolicy = base.MutationPolicy.Clone()
			test.mutate(&config)
			if err := SetMCPOperatorConfig(config); err == nil {
				t.Fatalf("invalid inventory policy was accepted")
			}
			if _, err := MCPMutationPolicyFromConfig(config); err == nil {
				t.Fatalf("invalid inventory policy converted without error")
			}
		})
	}
}

func TestMCPMutationPolicySetIsAtomicOnFailure(t *testing.T) {
	valid := validMCPMutationPolicyConfig()
	if err := SetMCPOperatorConfig(valid); err != nil {
		t.Fatalf("set valid policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	before := mcpPolicyHash(CurrentMCPMutationPolicy())

	invalid := validMCPMutationPolicyConfig()
	invalid.Servers[0].Enabled = false
	if err := SetMCPOperatorConfig(invalid); err == nil {
		t.Fatalf("invalid replacement policy was accepted")
	}
	if got := mcpPolicyHash(CurrentMCPMutationPolicy()); got != before {
		t.Fatalf("failed setter changed the frozen policy: %s != %s", got, before)
	}
	if !CurrentMCPOperatorConfig().MutationPolicy.Enabled {
		t.Fatalf("failed setter disabled the prior policy")
	}
}

func TestMCPMutationPolicySnapshotConcurrentCopies(t *testing.T) {
	input := validMCPMutationPolicyConfig()
	if err := SetMCPOperatorConfig(input); err != nil {
		t.Fatalf("set valid policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	wantHash := mcpPolicyHash(CurrentMCPMutationPolicy())

	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				config := CurrentMCPOperatorConfig()
				if len(config.MutationPolicy.Operations) == 1 {
					config.MutationPolicy.Operations[0].AllowedAgents[0] = "worker-mutation"
					config.MutationPolicy.Operations[0].AllowedSources[0] = 999
				}
				policy := CurrentMCPMutationPolicy()
				if len(policy.Operations) == 1 {
					policy.Operations[0].AllowedAgents[0] = "worker-policy"
					policy.Operations[0].AllowedSources[0] = 1000
				}
				if got := mcpPolicyHash(CurrentMCPMutationPolicy()); got != wantHash {
					t.Errorf("concurrent getter observed a mutated snapshot: %s != %s", got, wantHash)
					return
				}
			}
		}()
	}
	wait.Wait()
	final := CurrentMCPMutationPolicy()
	if final.Operations[0].AllowedAgents[0] != "agent-id" || final.Operations[0].AllowedSources[0] != 42 {
		t.Fatalf("concurrent copies mutated the global policy: %+v", final)
	}
}

func TestMCPConfigStringRedactsMutationPolicyDetails(t *testing.T) {
	config := validMCPMutationPolicyConfig()
	text := fmt.Sprintf("%#v", config)
	for _, secret := range []string{"agent-id", "source_id", "42"} {
		if strings.Contains(text, secret) {
			t.Fatalf("MCPConfig formatting leaked %q: %s", secret, text)
		}
	}
}

func TestMCPMutationPolicyConcurrentSetAndGet(t *testing.T) {
	valid := validMCPMutationPolicyConfig()
	disabled := validMCPMutationPolicyConfig()
	disabled.MutationPolicy = cfg.MCPMutationPolicyConfig{}
	if err := SetMCPOperatorConfig(valid); err != nil {
		t.Fatalf("set valid policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	validPolicy, err := MCPMutationPolicyFromConfig(valid)
	if err != nil {
		t.Fatalf("convert valid policy: %v", err)
	}
	wantHash := mcpPolicyHash(validPolicy)

	var wait sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 50; iteration++ {
				if err := SetMCPOperatorConfig(valid); err != nil {
					t.Errorf("concurrent valid set: %v", err)
					return
				}
				if err := SetMCPOperatorConfig(disabled); err != nil {
					t.Errorf("concurrent disabled set: %v", err)
					return
				}
			}
		}()
	}
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 200; iteration++ {
				policy := CurrentMCPMutationPolicy()
				switch {
				case policy.Enabled && mcpPolicyHash(policy) != wantHash:
					t.Errorf("concurrent getter observed an unexpected enabled policy")
					return
				case !policy.Enabled && len(policy.Operations) != 0:
					t.Errorf("concurrent getter observed a disabled policy with operations")
					return
				}
			}
		}()
	}
	wait.Wait()
	if err := SetMCPOperatorConfig(valid); err != nil {
		t.Fatalf("restore valid policy: %v", err)
	}
	if got := mcpPolicyHash(CurrentMCPMutationPolicy()); got != wantHash {
		t.Fatalf("restored policy hash = %s, want %s", got, wantHash)
	}
}

func TestMCPMutationPolicyRejectsAliasCollision(t *testing.T) {
	config := validMCPMutationPolicyConfig()
	config.Servers[0].ApprovedTools = append(config.Servers[0].ApprovedTools, cfg.MCPApprovedTool{
		Name: "approved.tool", Effect: "mutation", AllowMutationProposal: true,
		RequiredCapabilities: []string{"tool_execution"}, AllowedSources: []uint64{42},
	})
	if err := SetMCPOperatorConfig(config); err == nil {
		t.Fatalf("policy inventory with a derived alias collision was accepted")
	}
}

func TestMCPMutationPolicyFromConfigDoesNotInstall(t *testing.T) {
	if err := SetMCPOperatorConfig(cfg.MCPConfig{}); err != nil {
		t.Fatalf("clear operator config: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	if _, err := MCPMutationPolicyFromConfig(validMCPMutationPolicyConfig()); err != nil {
		t.Fatalf("convert valid policy: %v", err)
	}
	if CurrentMCPMutationPolicy().Enabled {
		t.Fatalf("conversion without installation changed the global policy")
	}
}
