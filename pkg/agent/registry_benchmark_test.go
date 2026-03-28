package agent

import (
	"fmt"
	"testing"
)

func makeBenchAgent(i int) *AgentDefinition {
	id := fmt.Sprintf("agent-%06d", i)
	name := fmt.Sprintf("Agent %06d", i)
	trigger := fmt.Sprintf("trigger-%06d", i)
	return &AgentDefinition{
		FormatVersion: AgentFormatVersionV2,
		Identity: AgentIdentity{
			AgentID:      id,
			Name:         name,
			TrustLevel:   "trusted",
			Capabilities: []string{"all"},
		},
		Jobs: []Job{{Name: name, Process: "serial", TriggerType: "manual", TriggerName: trigger, Steps: []map[string]any{{"action": "AIInteraction", "params": map[string]any{}}}}},
	}
}

func TestRegistryLoadStressLargeCatalog(t *testing.T) {
	const totalAgents = 3000
	reg := NewAgentRegistry()
	for i := 0; i < totalAgents; i++ {
		if err := reg.Register(makeBenchAgent(i)); err != nil {
			t.Fatalf("unexpected register failure at %d: %v", i, err)
		}
	}

	if got := len(reg.ordered); got != totalAgents {
		t.Fatalf("expected %d ordered agents, got %d", totalAgents, got)
	}

	mid := totalAgents / 2
	if _, ok := reg.GetByID(fmt.Sprintf("agent-%06d", mid)); !ok {
		t.Fatalf("expected id lookup for middle record")
	}
	if _, ok := reg.GetByName(fmt.Sprintf("Agent %06d", mid)); !ok {
		t.Fatalf("expected name lookup for middle record")
	}
	matches := reg.GetByTrigger("manual", fmt.Sprintf("trigger-%06d", mid))
	if len(matches) != 1 {
		t.Fatalf("expected one trigger match, got %d", len(matches))
	}
}

func BenchmarkAgentRegistryRegisterLarge(b *testing.B) {
	for i := 0; i < b.N; i++ {
		reg := NewAgentRegistry()
		for j := 0; j < 2000; j++ {
			if err := reg.Register(makeBenchAgent(j)); err != nil {
				b.Fatalf("register failed at %d: %v", j, err)
			}
		}
	}
}

func BenchmarkAgentRegistryLookupLarge(b *testing.B) {
	reg := NewAgentRegistry()
	const totalAgents = 5000
	for i := 0; i < totalAgents; i++ {
		if err := reg.Register(makeBenchAgent(i)); err != nil {
			b.Fatalf("register failed at %d: %v", i, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % totalAgents
		id := fmt.Sprintf("agent-%06d", idx)
		name := fmt.Sprintf("Agent %06d", idx)
		trigger := fmt.Sprintf("trigger-%06d", idx)
		if _, ok := reg.GetByID(id); !ok {
			b.Fatalf("id lookup failed for %s", id)
		}
		if _, ok := reg.GetByName(name); !ok {
			b.Fatalf("name lookup failed for %s", name)
		}
		if matches := reg.GetByTrigger("manual", trigger); len(matches) != 1 {
			b.Fatalf("trigger lookup failed for %s", trigger)
		}
	}
}
