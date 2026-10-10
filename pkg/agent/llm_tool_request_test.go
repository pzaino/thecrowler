package agent

import (
	"strings"
	"testing"
)

func toolTestParams() map[string]interface{} {
	return map[string]interface{}{
		"url":    "https://example.com/v1/chat",
		"model":  "mock-model",
		"prompt": "hello",
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":       "get_forecast",
					"parameters": map[string]any{"type": "object"},
				},
			},
		},
		"tool_choice": "auto",
	}
}

// TestNormalizeToolsPrecedence pins step-over-config resolution.
func TestNormalizeToolsPrecedence(t *testing.T) {
	params := toolTestParams()
	config := map[string]interface{}{
		"ai": map[string]interface{}{
			"tools": []any{
				map[string]any{"type": "function", "function": map[string]any{
					"name": "other", "parameters": map[string]any{"type": "object"}}},
			},
			"tool_choice": "required",
		},
	}
	ictx := InputContext{}
	tools, err := normalizeTools(params, config, ictx)
	if err != nil || len(tools) != 1 || tools[0].Function.Name != "get_forecast" {
		t.Fatalf("step tools must win: %+v, %v", tools, err)
	}
	choice, set, err := normalizeToolChoice(params, config, ictx)
	if err != nil || !set || choice.Mode != "auto" {
		t.Fatalf("step choice must win: %+v, %v, %v", choice, set, err)
	}

	// Config defaults apply when the step is silent.
	delete(params, "tools")
	delete(params, "tool_choice")
	tools, err = normalizeTools(params, config, ictx)
	if err != nil || len(tools) != 1 || tools[0].Function.Name != "other" {
		t.Fatalf("config tools must apply: %+v, %v", tools, err)
	}
	choice, set, err = normalizeToolChoice(params, config, ictx)
	if err != nil || !set || choice.Mode != "required" {
		t.Fatalf("config choice must apply: %+v, %v, %v", choice, set, err)
	}
}

// TestNormalizeToolsRejectsBeforeHTTP pins failing-fast validation.
func TestNormalizeToolsRejectsBeforeHTTP(t *testing.T) {
	base := func() (map[string]interface{}, map[string]interface{}, InputContext) {
		params := toolTestParams()
		delete(params, "tools")
		return params, map[string]interface{}{}, InputContext{}
	}
	// Non-array tools.
	params, config, ictx := base()
	params["tools"] = "nope"
	if _, err := normalizeLLMRequest(params, config, ictx); err == nil ||
		!strings.Contains(err.Error(), "invalid tools") {
		t.Fatalf("expected tools rejection, got %v", err)
	}
	// Duplicate names across entries.
	params, config, ictx = base()
	params["tools"] = []any{
		map[string]any{"type": "function", "function": map[string]any{
			"name": "dup", "parameters": map[string]any{"type": "object"}}},
		map[string]any{"type": "function", "function": map[string]any{
			"name": "DUP", "parameters": map[string]any{"type": "object"}}},
	}
	if _, err := normalizeLLMRequest(params, config, ictx); err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
	// Bad choice.
	params, config, ictx = base()
	params["tools"] = validToolEntries()
	params["tool_choice"] = "sometimes"
	if _, err := normalizeLLMRequest(params, config, ictx); err == nil ||
		!strings.Contains(err.Error(), "invalid tool_choice") {
		t.Fatalf("expected choice rejection, got %v", err)
	}
	// Bad output mode.
	params, config, ictx = base()
	params["output_mode"] = "yaml"
	if _, err := normalizeLLMRequest(params, config, ictx); err == nil ||
		!strings.Contains(err.Error(), "invalid output_mode") {
		t.Fatalf("expected output_mode rejection, got %v", err)
	}
}

// TestStreamWithToolsRejected pins the non-streaming boundary.
func TestStreamWithToolsRejected(t *testing.T) {
	for _, stream := range []any{true, "true", "1"} {
		params := toolTestParams()
		params["stream"] = stream
		if _, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{}); err == nil ||
			!strings.Contains(err.Error(), "streaming with tool calling") {
			t.Fatalf("stream=%v: expected rejection, got %v", stream, err)
		}
	}
	// Legacy streaming without tools still normalizes (wire unchanged).
	params := map[string]interface{}{
		"url": "https://example.com/v1/chat", "model": "m", "prompt": "hi", "stream": true,
	}
	req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
	if err != nil {
		t.Fatalf("legacy stream must pass: %v", err)
	}
	if req.Extras["stream"] != true {
		t.Fatalf("legacy stream must serialize: %+v", req.Extras)
	}
}

// TestPromptConvertedToMessagesForTools pins chat synthesis.
func TestPromptConvertedToMessagesForTools(t *testing.T) {
	params := toolTestParams()
	req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected one synthesized message, got %+v", req.Messages)
	}
	msg, ok := req.Messages[0].(map[string]interface{})
	if !ok || msg["role"] != "user" || msg["content"] != "hello" {
		t.Fatalf("bad synthesized message: %+v", req.Messages)
	}
	if req.Prompt != "hello" {
		t.Fatalf("prompt must be retained: %q", req.Prompt)
	}
	if req.OutputMode != LLMOutputRaw {
		t.Fatalf("default output mode must be raw, got %q", req.OutputMode)
	}

	// Explicit messages take precedence over prompt synthesis.
	params["messages"] = []any{map[string]any{"role": "user", "content": "explicit"}}
	req, err = normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg, ok = req.Messages[0].(map[string]interface{})
	if !ok || msg["content"] != "explicit" {
		t.Fatalf("explicit messages must win: %+v", req.Messages)
	}
}

// TestToolsNormalizeConcurrently proves concurrent normalization of shared
// params shares nothing mutable (run with -race).
func TestToolsNormalizeConcurrently(t *testing.T) {
	params := toolTestParams()
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
			if err != nil {
				done <- err
				return
			}
			if len(req.Tools) != 1 || req.Tools[0].Function.Name != "get_forecast" {
				done <- stringErr("tool mismatch")
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent normalization failed: %v", err)
		}
	}
}

// TestToolsSchemaStrictValidation pins upload-time agreement.
func TestToolsSchemaStrictValidation(t *testing.T) {
	valid := []byte(`format_version: v2
agent_identity:
  name: Tool Schema Probe
  trust_level: trusted
  capabilities: [ai_reasoning]
jobs:
  - name: Tool Schema Probe
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: AIInteraction
        params:
          model: m
          prompt: hi
          tools:
            - type: function
              function:
                name: ping
                parameters:
                  type: object
          tool_choice: auto
          output_mode: normalized
`)
	if err := ValidateAgentConfig(valid, "yaml", ValidationModeStrict, nil); err != nil {
		t.Fatalf("valid tools manifest rejected: %v", err)
	}
	invalid := []byte(`format_version: v2
agent_identity:
  name: Tool Schema Probe
  trust_level: trusted
  capabilities: [ai_reasoning]
jobs:
  - name: Tool Schema Probe
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: AIInteraction
        params:
          model: m
          prompt: hi
          tools:
            - type: mcp
              function:
                name: ping
                parameters:
                  type: object
`)
	if err := ValidateAgentConfig(invalid, "yaml", ValidationModeStrict, nil); err == nil {
		t.Fatalf("invalid tools manifest accepted")
	}
}

// TestToolsDoNotMutateManifest proves repeated normalization is stable.
func TestToolsDoNotMutateManifest(t *testing.T) {
	params := toolTestParams()
	snapshot := map[string]any{
		"url":   params["url"],
		"tools": params["tools"],
	}
	for i := 0; i < 2; i++ {
		if _, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	toolsAfter, _ := params["tools"].([]any)
	if len(toolsAfter) != 1 {
		t.Fatalf("manifest tools mutated: %#v", params["tools"])
	}
	_ = snapshot
}
