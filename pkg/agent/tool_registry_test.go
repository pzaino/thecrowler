package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// stubAgentTool is a minimal read-only fake.
type stubAgentTool struct {
	name         string
	description  string
	schema       map[string]any
	capabilities []string
	calls        int
}

func (t *stubAgentTool) Name() string { return t.name }

func (t *stubAgentTool) Description() string { return t.description }

func (t *stubAgentTool) InputSchema() map[string]any { return t.schema }

func (t *stubAgentTool) RequiredCapabilities() []string { return t.capabilities }

func (t *stubAgentTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	t.calls++
	return map[string]any{"ok": true}, nil
}

func validStubTool(name string) *stubAgentTool {
	return &stubAgentTool{
		name:         name,
		description:  "test tool",
		schema:       map[string]any{"type": "object"},
		capabilities: []string{"db_read"},
	}
}

// TestToolRegistryAcceptsValidTools pins the happy path and ordering.
func TestToolRegistryAcceptsValidTools(t *testing.T) {
	registry := NewAgentToolRegistry()
	for _, name := range []string{"zebra_tool", "alpha-tool", "m2"} {
		if err := registry.Register(validStubTool(name)); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	if registry.Len() != 3 {
		t.Fatalf("expected 3 tools, got %d", registry.Len())
	}
	names := registry.Names()
	if len(names) != 3 || names[0] != "alpha-tool" || names[1] != "m2" || names[2] != "zebra_tool" {
		t.Fatalf("expected sorted names, got %v", names)
	}
	if _, ok := registry.Get("m2"); !ok {
		t.Fatalf("expected m2 lookup to hit")
	}
	if _, ok := registry.Get("missing"); ok {
		t.Fatalf("expected missing lookup to miss")
	}
	defs := registry.ToolDefinitions()
	if len(defs) != 3 || defs[0].Function.Name != "alpha-tool" || defs[0].Type != "function" {
		t.Fatalf("bad transport export: %+v", defs)
	}
}

// TestToolRegistryRejectsInvalid pins admission validation.
func TestToolRegistryRejectsInvalid(t *testing.T) {
	registry := NewAgentToolRegistry()
	bad := []struct {
		name string
		tool AgentTool
	}{
		{"nil", nil},
		{"empty name", &stubAgentTool{name: " ", description: "d",
			schema: map[string]any{"type": "object"}}},
		{"bad chars", &stubAgentTool{name: "has space", description: "d",
			schema: map[string]any{"type": "object"}}},
		{"empty description", &stubAgentTool{name: "nodesc", description: " ",
			schema: map[string]any{"type": "object"}}},
		{"empty schema", &stubAgentTool{name: "noschema", description: "d"}},
		{"non-object schema", &stubAgentTool{name: "arrschema", description: "d",
			schema: map[string]any{"type": "array"}}},
		{"bad schema", &stubAgentTool{name: "badschema", description: "d",
			schema: map[string]any{"type": "object", "properties": map[string]any{
				"a": map[string]any{"$ref": "#/definitions/gone"}}}}},
	}
	for _, tc := range bad {
		if err := registry.Register(tc.tool); err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
	}
	if err := registry.Register(validStubTool("dup")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := registry.Register(validStubTool("dup")); err == nil {
		t.Fatalf("duplicate registration must fail")
	}
	oversized := validStubTool("big")
	oversizedDesc := make([]byte, maxToolDescriptionLen+1)
	for i := range oversizedDesc {
		oversizedDesc[i] = 'x'
	}
	oversized.description = string(oversizedDesc)
	if err := registry.Register(oversized); err == nil {
		t.Fatalf("oversized description must fail")
	}
	if registry.Len() != 1 {
		t.Fatalf("rejections must not register, len=%d", registry.Len())
	}
}

// TestToolRegistryConcurrentOps exercises registration and lookup under -race.
func TestToolRegistryConcurrentOps(t *testing.T) {
	registry := NewAgentToolRegistry()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%02d", n)
			if err := registry.Register(validStubTool(name)); err != nil {
				errs <- err
				return
			}
			if _, ok := registry.Get(name); !ok {
				errs <- fmt.Errorf("lookup missed %s", name)
				return
			}
			_ = registry.Names()
			_ = registry.Snapshot()
			_ = registry.ToolDefinitions()
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent op failed: %v", err)
		}
	}
	if registry.Len() != 16 {
		t.Fatalf("expected 16 tools, got %d", registry.Len())
	}
}

// TestToolRegistryMutationIsolation proves schemas are defensively copied.
func TestToolRegistryMutationIsolation(t *testing.T) {
	registry := NewAgentToolRegistry()
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"a": map[string]any{"type": "string"}}}
	tool := &stubAgentTool{name: "iso", description: "d", schema: schema,
		capabilities: []string{"db_read"}}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Mutate the caller's map after registration.
	schema["properties"].(map[string]any)["evil"] = map[string]any{"type": "string"}
	copied := registry.InputSchemaCopy("iso")
	if _, exists := copied["properties"].(map[string]any)["evil"]; exists {
		t.Fatalf("registration must copy schemas")
	}
	// Mutate the exported copy.
	copied["properties"].(map[string]any)["evil2"] = true
	again := registry.InputSchemaCopy("iso")
	if _, exists := again["properties"].(map[string]any)["evil2"]; exists {
		t.Fatalf("exports must copy schemas")
	}
	defs := registry.ToolDefinitions()
	defs[0].Function.Parameters["evil3"] = true
	fresh := registry.ToolDefinitions()
	if _, exists := fresh[0].Function.Parameters["evil3"]; exists {
		t.Fatalf("advertised definitions must copy schemas")
	}
}
