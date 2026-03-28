package agent

import "testing"

func TestNormalizeLegacyJobConfigToAgentDefinition(t *testing.T) {
	jc := &JobConfig{
		Jobs: []Job{
			{
				Name:        "Legacy Runner",
				Process:     "serial",
				TriggerType: "event",
				TriggerName: "legacy_event",
				Steps: []map[string]any{
					{"action": "RunCommand", "params": map[string]any{"command": "echo legacy"}},
				},
			},
		},
	}

	def, err := jc.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "test", Format: "yaml"})
	if err != nil {
		t.Fatalf("expected normalization to succeed, got %v", err)
	}
	if def.Identity.AgentID != "legacy-runner" {
		t.Fatalf("expected derived agent_id legacy-runner, got %q", def.Identity.AgentID)
	}
	if def.FormatVersion != AgentFormatVersionV1 {
		t.Fatalf("expected format_version %q, got %q", AgentFormatVersionV1, def.FormatVersion)
	}
}

func TestNormalizeIdentityEnabledConfigToAgentDefinition(t *testing.T) {
	jc := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID:   "agent-123",
			Name:      "Planner",
			AgentType: "planner",
		},
		Jobs: []Job{
			{
				Name:        "Planner",
				Process:     "serial",
				TriggerType: "manual",
				TriggerName: "plan",
			},
		},
	}
	def, err := jc.NormalizeToAgentDefinition(AgentSourceMetadata{})
	if err != nil {
		t.Fatalf("expected normalization to succeed, got %v", err)
	}
	if def.Identity.AgentID != "agent-123" {
		t.Fatalf("expected agent-123, got %q", def.Identity.AgentID)
	}
	if def.Identity.AgentType != "planner" {
		t.Fatalf("expected planner type, got %q", def.Identity.AgentType)
	}
}

func TestAgentRegistryIndexesAndTriggerSelectorOrder(t *testing.T) {
	reg := NewAgentRegistry()
	a1 := &AgentDefinition{
		Identity: AgentIdentity{AgentID: "a1", Name: "Alpha"},
		Jobs: []Job{
			{Name: "Alpha", TriggerType: "event", TriggerName: "source.created"},
			{Name: "Alpha", TriggerType: "manual", TriggerName: "alpha.run"},
		},
	}
	a2 := &AgentDefinition{
		Identity: AgentIdentity{AgentID: "a2", Name: "Beta"},
		Jobs: []Job{
			{Name: "Beta", TriggerType: "event", TriggerName: "source.created"},
		},
	}
	if err := reg.Register(a1); err != nil {
		t.Fatalf("register a1 failed: %v", err)
	}
	if err := reg.Register(a2); err != nil {
		t.Fatalf("register a2 failed: %v", err)
	}

	if _, ok := reg.GetByID("a1"); !ok {
		t.Fatal("expected to find agent by id")
	}
	if _, ok := reg.GetByName("Beta"); !ok {
		t.Fatal("expected to find agent by name")
	}

	matches := reg.GetByTrigger("event", "source.created")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Identity.AgentID != "a1" || matches[1].Identity.AgentID != "a2" {
		t.Fatalf("expected deterministic insertion order [a1,a2], got [%s,%s]", matches[0].Identity.AgentID, matches[1].Identity.AgentID)
	}
}

func TestAgentRegistryDuplicateConflicts(t *testing.T) {
	reg := NewAgentRegistry()
	base := &AgentDefinition{
		Identity: AgentIdentity{AgentID: "dup-id", Name: "UniqueName"},
		Jobs:     []Job{{Name: "UniqueName", TriggerType: "event", TriggerName: "evt"}},
	}
	if err := reg.Register(base); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}

	err := reg.Register(&AgentDefinition{
		Identity: AgentIdentity{AgentID: "dup-id", Name: "Other"},
		Jobs:     []Job{{Name: "Other", TriggerType: "event", TriggerName: "evt2"}},
	})
	if err == nil {
		t.Fatal("expected duplicate agent_id conflict")
	}

	err = reg.Register(&AgentDefinition{
		Identity: AgentIdentity{AgentID: "other-id", Name: "UniqueName"},
		Jobs:     []Job{{Name: "UniqueName", TriggerType: "event", TriggerName: "evt3"}},
	})
	if err == nil {
		t.Fatal("expected duplicate name conflict")
	}
}

func TestJobConfigGettersUseRegistryBackedImplementation(t *testing.T) {
	jc := NewJobConfig()
	first := &JobConfig{
		Jobs: []Job{
			{Name: "Alpha Agent", TriggerType: "event", TriggerName: "evt.alpha"},
			{Name: "Alpha Agent", TriggerType: "manual", TriggerName: "alpha.manual"},
		},
	}
	second := &JobConfig{
		Jobs: []Job{
			{Name: "Beta Agent", TriggerType: "event", TriggerName: "evt.alpha"},
		},
	}
	jc.RegisterAgent(first)
	jc.RegisterAgent(second)

	if got, ok := jc.GetAgentByName("Alpha Agent"); !ok || len(got.Jobs) != 2 {
		t.Fatalf("expected full registry-backed agent by name, got ok=%v jobs=%d", ok, len(got.Jobs))
	}

	agents, ok := jc.GetAgentsByEventType("evt.alpha")
	if !ok {
		t.Fatal("expected event-triggered lookup to resolve")
	}
	if len(agents) != 2 {
		t.Fatalf("expected two agents for shared trigger selector, got %d", len(agents))
	}
}
