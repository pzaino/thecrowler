package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return data
}

// TestNormalizedOutputEndToEnd runs a tool-enabled AI step against a local
// OpenAI-compatible server and asserts the normalized envelope.
func TestNormalizedOutputEndToEnd(t *testing.T) {
	body := fixtureBytes(t, "openai_chat_tool_calls.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	a := &AIInteractionAction{}
	result, err := a.Execute(map[string]interface{}{
		StrConfig:     map[string]interface{}{},
		StrRequest:    map[string]interface{}{},
		"url":         server.URL,
		"model":       "gpt-4o-mini",
		"prompt":      "check the weather",
		"tools":       validToolEntries(),
		"output_mode": "normalized",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	out, ok := result[StrResponse].(map[string]any)
	if !ok {
		t.Fatalf("expected mapping output, got %#v", result[StrResponse])
	}
	if out["content"] != "" || out["finish_reason"] != "tool_calls" || out["model"] != "gpt-4o-mini" {
		t.Fatalf("bad normalized envelope: %v", out)
	}
	calls, ok := out["tool_calls"].([]any)
	if !ok || len(calls) != 2 {
		t.Fatalf("bad tool_calls: %v", out)
	}
	first, ok := calls[0].(map[string]any)
	if !ok || first["name"] != "get_forecast" {
		t.Fatalf("bad first call: %v", calls)
	}
	args, ok := first["arguments"].(map[string]any)
	if !ok || args["city"] != "Oslo" {
		t.Fatalf("bad arguments: %v", first)
	}
	if _, hasRaw := out["raw"]; !hasRaw {
		t.Fatalf("normalized output must keep raw: %v", out)
	}

	// $response references into the normalized shape.
	ictx := NewInputContext(map[string]any{StrRequest: out})
	name, err := ResolveString(ictx, "$response.tool_calls[0].name")
	if err != nil || name != "get_forecast" {
		t.Fatalf("tool call name reference: %q, %v", name, err)
	}
	city, err := ResolveString(ictx, "$response.tool_calls[0].arguments.city")
	if err != nil || city != "Oslo" {
		t.Fatalf("argument reference: %q, %v", city, err)
	}
	whole, err := ResolveValue(ictx, "$response.tool_calls")
	if err != nil {
		t.Fatalf("whole-object reference: %v", err)
	}
	if arr, ok := whole.([]any); !ok || len(arr) != 2 {
		t.Fatalf("whole tool_calls must stay an array: %#v", whole)
	}
	if _, err := ResolveString(ictx, "$response.tool_calls[5].name"); err == nil {
		t.Fatalf("out-of-range path must error")
	}
}

// TestNormalizedOutputDelegatedPropagation proves tool-call data flows
// through delegation without being executed.
func TestNormalizedOutputDelegatedPropagation(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	RegisterLLMProvider(&fixtureChatProvider{})

	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&AIInteractionAction{})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	callee := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "tool-callee", Name: "ToolCallee",
			TrustLevel: "trusted", Capabilities: []string{"all"}},
		Jobs: []Job{{Name: "ToolCallee", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{
					"provider":    "fixture-chat",
					"url":         "https://example.com/v1/chat",
					"model":       "fixture",
					"prompt":      "do it",
					"output_mode": "normalized",
				}},
			}}},
	}
	caller := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "tool-caller", Name: "ToolCaller",
			TrustLevel: "trusted", Capabilities: []string{"delegate", "api_request"}},
		Jobs: []Job{{Name: "ToolCaller", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{"condition": map[string]interface{}{
					"condition_type": "if", "expression": "true",
					"on_true": map[string]interface{}{"agent_id": "tool-callee"},
				}}},
				{"action": "APIRequest", "params": map[string]interface{}{}},
			}}},
	}
	AgentsRegistry.RegisterAgent(callee)
	AgentsRegistry.RegisterAgent(caller)

	if err := engine.ExecuteAgent("tool-caller", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("delegation failed: %v", err)
	}
	if len(capture.got) != 1 {
		t.Fatalf("expected one capture, got %d", len(capture.got))
	}
	seen, ok := capture.got[0].(map[string]any)
	if !ok {
		t.Fatalf("expected mapping, got %#v", capture.got[0])
	}
	calls, ok := seen["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("delegated tool_calls lost: %#v", seen)
	}
	entry, ok := calls[0].(map[string]any)
	if !ok || entry["name"] != "ping" {
		t.Fatalf("bad delegated call: %v", calls)
	}
}

// fixtureChatProvider returns a decoded Ollama-style chat completion.
type fixtureChatProvider struct{ lastReq LLMRequest }

func (p *fixtureChatProvider) Name() string { return "fixture-chat" }

func (p *fixtureChatProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	p.lastReq = req
	return map[string]interface{}{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"id":   "fx-1",
					"type": "function",
					"function": map[string]any{
						"name":      "ping",
						"arguments": map[string]any{"host": "db"},
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"model": "fixture",
	}, nil
}

// TestRawDefaultUnchanged pins the legacy default output shape.
func TestRawDefaultUnchanged(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	cp := &captureProvider{name: "mock-raw"}
	RegisterLLMProvider(cp)

	a := &AIInteractionAction{}
	result, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{},
		StrRequest: "hi",
		"provider": "mock-raw",
		"url":      "https://example.com/v1/chat",
		"model":    "m",
		"prompt":   "hi",
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "ping", "parameters": map[string]any{"type": "object"}}}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw default returns the provider map untouched (with tools requested
	// but no normalization asked for).
	out, ok := result[StrResponse].(map[string]interface{})
	if !ok || out["provider"] != "mock-raw" {
		t.Fatalf("raw output changed: %#v", result[StrResponse])
	}
}

// TestInvalidOutputModeFailsBeforeHTTP proves ordering: contract errors come
// before provider lookup and transport.
func TestInvalidOutputModeFailsBeforeHTTP(t *testing.T) {
	a := &AIInteractionAction{}
	_, err := a.Execute(map[string]interface{}{
		StrConfig:     map[string]interface{}{},
		StrRequest:    "hi",
		"provider":    "does-not-exist",
		"url":         "https://example.com/v1/chat",
		"model":       "m",
		"prompt":      "hi",
		"output_mode": "yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid output_mode") {
		t.Fatalf("expected output_mode error, got %v", err)
	}
}

// hostileToolProvider returns tool calls named after CROWler actions with
// argument payloads that would be harmful if ever interpreted.
type hostileToolProvider struct{}

func (p *hostileToolProvider) Name() string { return "hostile-tools" }

func (p *hostileToolProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	return map[string]interface{}{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{
					map[string]any{"id": "h1", "type": "function", "function": map[string]any{
						"name": "RunCommand", "arguments": map[string]any{"command": "touch /tmp/crowler-phase2-pwned"}}},
					map[string]any{"id": "h2", "type": "function", "function": map[string]any{
						"name": "DBQuery", "arguments": map[string]any{"query": "DROP TABLE Events"}}},
					map[string]any{"id": "h3", "type": "function", "function": map[string]any{
						"name": "APIRequest", "arguments": map[string]any{"url": "http://169.254.169.254/"}}},
				},
			},
			"finish_reason": "tool_calls",
		}},
		"model": "hostile",
	}, nil
}

// TestToolCallsRemainInert proves model-proposed calls matching CROWler
// action names produce data only: no command runs, no database writes, no
// HTTP dispatch, no follow-up model call.
func TestToolCallsRemainInert(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	RegisterLLMProvider(&hostileToolProvider{})

	canary := "/tmp/crowler-phase2-pwned"
	_ = os.Remove(canary)
	t.Cleanup(func() { _ = os.Remove(canary) })

	engine := NewJobEngine()
	engine.RegisterAction(&AIInteractionAction{})
	capture := &inputCaptureAction{name: "CreateEvent"}
	engine.RegisterAction(capture)
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "inert", Name: "Inert",
			TrustLevel: "trusted", Capabilities: []string{"ai_reasoning", "emit_event"}},
		Jobs: []Job{{Name: "Inert", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{
					"input":    map[string]interface{}{},
					"provider": "hostile-tools",
					"url":      "https://example.com/v1/chat",
					"model":    "hostile",
					"prompt":   "do harm",
					"tools": []any{map[string]any{"type": "function", "function": map[string]any{
						"name": "RunCommand", "parameters": map[string]any{"type": "object"}}}},
					"output_mode": "normalized",
				}},
				{"action": "CreateEvent", "params": map[string]interface{}{}},
			}}},
	}
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Fatalf("tool call was executed: canary exists")
	}
	if len(capture.got) != 1 {
		t.Fatalf("expected one capture, got %d", len(capture.got))
	}
	seen, ok := capture.got[0].(map[string]any)
	if !ok {
		t.Fatalf("expected mapping, got %#v", capture.got[0])
	}
	calls, ok := seen["tool_calls"].([]any)
	if !ok || len(calls) != 3 {
		t.Fatalf("tool calls lost in dataflow: %#v", seen)
	}
	names := map[string]bool{}
	for _, raw := range calls {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("bad call entry: %#v", raw)
		}
		name, _ := entry["name"].(string)
		names[name] = true
	}
	for _, want := range []string{"RunCommand", "DBQuery", "APIRequest"} {
		if !names[want] {
			t.Fatalf("call %q missing: %v", want, names)
		}
	}
}

// yamlUnmarshalToolDoc decodes YAML exactly like the manifest loader does,
// surfacing map[interface{}]interface{} nesting.
func yamlUnmarshalToolDoc(doc string, out any) error {
	return yaml.Unmarshal([]byte(doc), out)
}

// TestNestedYAMLToolMaps proves YAML-decoded nested maps (which decode as
// map[interface{}]interface{}) validate and normalize.
func TestNestedYAMLToolMaps(t *testing.T) {
	doc := "type: function\nfunction:\n  name: f\n  parameters:\n    type: object\n    properties:\n      o:\n        type: object\n        properties:\n          x:\n            type: string\n        required:\n          - x\n"
	var decoded map[interface{}]interface{}
	if err := yamlUnmarshalToolDoc(doc, &decoded); err != nil {
		t.Fatalf("yaml decode: %v", err)
	}
	defs, err := validateToolDefinitions([]any{decoded})
	if err != nil {
		t.Fatalf("yaml-decoded tools rejected: %v", err)
	}
	if defs[0].Function.Name != "f" {
		t.Fatalf("bad def: %+v", defs[0])
	}
}
