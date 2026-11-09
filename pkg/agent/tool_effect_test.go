package agent

import (
	"context"
	"testing"
)

type effectTestTool struct {
	name     string
	effect   ToolEffect
	caps     []string
	executed bool
}

func (t *effectTestTool) Name() string        { return t.name }
func (t *effectTestTool) Description() string { return "effect classification test tool" }
func (t *effectTestTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *effectTestTool) RequiredCapabilities() []string { return t.caps }
func (t *effectTestTool) Effect() ToolEffect             { return t.effect }
func (t *effectTestTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	t.executed = true
	return map[string]any{"ok": true}, nil
}

func TestToolEffectClassificationIsClosedSet(t *testing.T) {
	unknown := &effectTestTool{name: "unknown_effect", effect: ToolEffect("future_effect")}
	registry := NewAgentToolRegistry()
	if err := registry.Register(unknown); err == nil {
		t.Fatalf("Register accepted an unknown effect")
	}
	if err := registry.RegisterMutation(unknown); err == nil {
		t.Fatalf("RegisterMutation accepted an unknown effect")
	}
	if unknown.executed {
		t.Fatalf("rejected tool executed its handler")
	}
	if registry.Len() != 0 {
		t.Fatalf("registry retained a rejected tool")
	}

	readOnly := &effectTestTool{name: "read_tool", effect: ToolEffectReadOnly}
	mutation := &effectTestTool{name: "mutation_tool", effect: ToolEffectMutation}
	if err := registry.Register(readOnly); err != nil {
		t.Fatalf("register read-only tool: %v", err)
	}
	if err := registry.RegisterMutation(mutation); err != nil {
		t.Fatalf("register mutation tool: %v", err)
	}
	if err := registry.RegisterMutation(&effectTestTool{name: "wrong_mutation", effect: ToolEffectReadOnly}); err == nil {
		t.Fatalf("RegisterMutation accepted a read-only effect")
	}
	if err := registry.Register(&effectTestTool{name: "wrong_read", effect: ToolEffectMutation}); err == nil {
		t.Fatalf("Register accepted a mutation effect")
	}
	if effect, ok := registry.Effect("mutation_tool"); !ok || effect != ToolEffectMutation {
		t.Fatalf("registered effect = %q, ok=%v", effect, ok)
	}

	// Late mutation of the implementation's metadata must not change the
	// frozen authorized effect.
	mutation.effect = ToolEffectReadOnly
	frozen, ok := registry.Get("mutation_tool")
	if !ok {
		t.Fatalf("mutation tool missing")
	}
	auth := ToolAuthContext{
		Identity:          AgentIdentity{AgentID: "agent", Capabilities: []string{"tool_execution", "db_write"}, TrustLevel: "trusted"},
		Enforcement:       true,
		Allowlist:         map[string]bool{"mutation_tool": true},
		AllowedOperations: map[string]bool{"mutation_tool": true},
		MutationAllowed:   true,
		RunID:             "run-1",
	}
	if err := AuthorizeToolCall(auth, frozen, map[string]any{}); err != nil {
		t.Fatalf("frozen mutation effect was not retained: %v", err)
	}
	auth.MutationAllowed = false
	if err := AuthorizeToolCall(auth, frozen, map[string]any{}); err == nil {
		t.Fatalf("late metadata mutation bypassed the mutation gate")
	}
	if err := registry.Register(&effectTestTool{name: "direct_unknown", effect: ToolEffect("bogus")}); err == nil {
		t.Fatalf("direct unknown effect was not rejected")
	}
	direct := &effectTestTool{name: "direct_unknown", effect: ToolEffect("bogus")}
	directAuth := ToolAuthContext{
		Identity:    AgentIdentity{AgentID: "agent", Capabilities: []string{"tool_execution"}, TrustLevel: "trusted"},
		Enforcement: true, Allowlist: map[string]bool{"direct_unknown": true},
		MutationAllowed: true, AllowedOperations: map[string]bool{"direct_unknown": true}, RunID: "run-1",
	}
	if err := AuthorizeToolCall(directAuth, direct, map[string]any{}); err == nil {
		t.Fatalf("direct unknown effect was authorized")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != ToolDenyEffect {
		t.Fatalf("direct unknown effect error = %v", err)
	}
}

func TestToolEffectAuthorizationDeniesBeforeExecution(t *testing.T) {
	registry := NewAgentToolRegistry()
	tool := &effectTestTool{
		name: "scoped_mutation", effect: ToolEffectMutation,
		caps: []string{"tool_execution", "db_write"},
	}
	if err := registry.RegisterMutation(tool); err != nil {
		t.Fatalf("register mutation: %v", err)
	}
	frozen, _ := registry.Get("scoped_mutation")
	base := ToolAuthContext{
		Identity: AgentIdentity{
			AgentID: "agent", TrustLevel: "trusted",
			Capabilities: []string{"tool_execution", "db_write"},
		},
		Enforcement:     true,
		Allowlist:       map[string]bool{"scoped_mutation": true},
		MutationAllowed: true,
		RunID:           "run-1",
	}
	cases := []struct {
		name string
		auth ToolAuthContext
		code string
	}{
		{"missing mutation allowlist", base, ToolDenyOperation},
		{"forbidden contract", func() ToolAuthContext {
			a := base
			a.AllowedOperations = map[string]bool{"scoped_mutation": true}
			a.Identity.Contract = &AgentContract{ForbiddenActions: []string{"tool:scoped_mutation"}}
			return a
		}(), ToolDenyContract},
		{"missing db_write", func() ToolAuthContext {
			a := base
			a.AllowedOperations = map[string]bool{"scoped_mutation": true}
			a.Identity.Capabilities = []string{"tool_execution"}
			a.Identity.Contract = nil
			return a
		}(), ToolDenyCapability},
		{"missing tool_execution", func() ToolAuthContext {
			a := base
			a.AllowedOperations = map[string]bool{"scoped_mutation": true}
			a.Identity.Capabilities = []string{"db_write"}
			a.Identity.Contract = nil
			return a
		}(), ToolDenyCapability},
	}
	for _, tc := range cases {
		err := AuthorizeToolCall(tc.auth, frozen, map[string]any{})
		if err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
		authErr, ok := err.(*ToolAuthError)
		if !ok || authErr.Code != tc.code {
			t.Fatalf("%s error = %v, want code %s", tc.name, err, tc.code)
		}
	}
	if tool.executed {
		t.Fatalf("denied tool executed its handler")
	}
}
