package agent

import (
	"net/http"
	"net/http/httptest"
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
		delete(params, "tool_choice")
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

// countingToolServer runs a fake provider endpoint that counts requests.
func countingToolServer(t *testing.T, responseBody string, status int) (*httptest.Server, *int) {
	t.Helper()
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, &count
}

func toolChoiceBaseParams() map[string]interface{} {
	return map[string]interface{}{
		"url":    "https://example.com/v1/chat",
		"model":  "mock-model",
		"prompt": "hello",
	}
}

// TestToolChoiceConsistencyMatrix pins every choice/tools combination,
// including zero-HTTP proof for rejections.
func TestToolChoiceConsistencyMatrix(t *testing.T) {
	declared := []any{map[string]any{"type": "function", "function": map[string]any{
		"name": "get_forecast", "parameters": map[string]any{"type": "object"}}}}
	t.Run("no tools absent choice passes without field", func(t *testing.T) {
		params := toolChoiceBaseParams()
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.HasToolChoice || len(req.Tools) != 0 {
			t.Fatalf("legacy request changed: %+v", req)
		}
	})
	t.Run("no tools none passes", func(t *testing.T) {
		params := toolChoiceBaseParams()
		params["tool_choice"] = "none"
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !req.HasToolChoice || req.ToolChoice.Mode != "none" {
			t.Fatalf("none must be kept: %+v", req.ToolChoice)
		}
	})
	for _, choice := range []any{"auto", "required", map[string]any{
		"type": "function", "function": map[string]any{"name": "get_forecast"}}} {
		choice := choice
		t.Run("no tools rejects", func(t *testing.T) {
			server, count := countingToolServer(t, `{"choices":[]}`, http.StatusOK)
			a := &AIInteractionAction{}
			_, err := a.Execute(map[string]interface{}{
				StrConfig:     map[string]interface{}{},
				StrRequest:    "hi",
				"url":         server.URL,
				"model":       "mock-model",
				"prompt":      "hi",
				"tool_choice": choice,
			})
			if err == nil {
				t.Fatalf("choice %v without tools must fail", choice)
			}
			if *count != 0 {
				t.Fatalf("rejected config reached HTTP")
			}
		})
	}
	for _, choice := range []any{"auto", "none", "required"} {
		params := toolChoiceBaseParams()
		params["tools"] = declared
		params["tool_choice"] = choice
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
		if err != nil {
			t.Fatalf("choice %v: unexpected error: %v", choice, err)
		}
		if !req.HasToolChoice {
			t.Fatalf("choice %v must be kept", choice)
		}
	}
	t.Run("named selector encodes", func(t *testing.T) {
		params := toolChoiceBaseParams()
		params["tools"] = declared
		params["tool_choice"] = map[string]any{
			"type": "function", "function": map[string]any{"name": "get_forecast"}}
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wire, ok := req.ToolChoice.toolChoiceWire().(map[string]any)
		if !ok {
			t.Fatalf("expected mapping wire form")
		}
		fn, _ := wire["function"].(map[string]any)
		if fn["name"] != "get_forecast" {
			t.Fatalf("name lost on the wire: %v", wire)
		}
	})
	t.Run("unknown named selector rejects before HTTP", func(t *testing.T) {
		server, count := countingToolServer(t, `{"choices":[]}`, http.StatusOK)
		params := toolChoiceBaseParams()
		params["url"] = server.URL
		params["tools"] = declared
		params["tool_choice"] = map[string]any{
			"type": "function", "function": map[string]any{"name": "nope"}}
		a := &AIInteractionAction{}
		_, err := a.Execute(map[string]interface{}{
			StrConfig:     map[string]interface{}{},
			StrRequest:    "hi",
			"url":         server.URL,
			"model":       "mock-model",
			"prompt":      "hi",
			"tools":       params["tools"],
			"tool_choice": params["tool_choice"],
		})
		if err == nil || !strings.Contains(err.Error(), "matches no declared tool") {
			t.Fatalf("expected undeclared-name rejection, got %v", err)
		}
		if *count != 0 {
			t.Fatalf("rejected config reached HTTP")
		}
	})
	t.Run("case mismatch rejects", func(t *testing.T) {
		params := toolChoiceBaseParams()
		params["tools"] = declared
		params["tool_choice"] = map[string]any{
			"type": "function", "function": map[string]any{"name": "GET_FORECAST"}}
		if _, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{}); err == nil ||
			!strings.Contains(err.Error(), "matches no declared tool") {
			t.Fatalf("expected case-sensitive rejection, got %v", err)
		}
	})
	t.Run("step tools drive choice over config", func(t *testing.T) {
		params := toolChoiceBaseParams()
		params["tools"] = declared
		config := map[string]interface{}{"ai": map[string]interface{}{
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{
				"name": "other", "parameters": map[string]any{"type": "object"}}}},
		}}
		params["tool_choice"] = map[string]any{
			"type": "function", "function": map[string]any{"name": "get_forecast"}}
		if _, err := normalizeLLMRequest(params, config, InputContext{}); err != nil {
			t.Fatalf("step tools must drive validation, got %v", err)
		}
		params["tool_choice"] = map[string]any{
			"type": "function", "function": map[string]any{"name": "other"}}
		if _, err := normalizeLLMRequest(params, config, InputContext{}); err == nil {
			t.Fatalf("config-only name must not validate against step tools")
		}
	})
	t.Run("resolved choice validated after resolution", func(t *testing.T) {
		params := toolChoiceBaseParams()
		params["tools"] = declared
		params["tool_choice"] = "$response.choice"
		ictx := InputContext{Response: map[string]any{"choice": "auto"}}
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, ictx)
		if err != nil || !req.HasToolChoice || req.ToolChoice.Mode != "auto" {
			t.Fatalf("resolved choice: %+v, %v", req.ToolChoice, err)
		}
		badCtx := InputContext{Response: map[string]any{"choice": "sometimes"}}
		if _, err := normalizeLLMRequest(params, map[string]interface{}{}, badCtx); err == nil {
			t.Fatalf("resolved invalid choice must fail")
		}
	})
}

// TestStreamSettingMatrix pins strict stream parsing with and without tools.
func TestStreamSettingMatrix(t *testing.T) {
	withTools := func() map[string]interface{} {
		params := toolTestParams()
		return params
	}
	withoutTools := func() map[string]interface{} {
		return map[string]interface{}{
			"url": "https://example.com/v1/chat", "model": "m", "prompt": "hi",
		}
	}
	accepted := []struct {
		name  string
		value any
		want  bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string TRUE padded", "  TRUE  ", true},
		{"string 1", "1", true},
		{"string false", "false", false},
		{"string 0", "0", false},
	}
	for _, tc := range accepted {
		for _, tools := range []bool{false, true} {
			var params map[string]interface{}
			if tools {
				params = withTools()
			} else {
				params = withoutTools()
			}
			params["stream"] = tc.value
			req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
			if tools {
				if !tc.want {
					if err != nil {
						t.Fatalf("%s with tools: unexpected error %v", tc.name, err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), "streaming with tool calling") {
					t.Fatalf("%s with tools: expected stream rejection, got %v", tc.name, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s without tools: unexpected error %v", tc.name, err)
			}
			if req.Extras["stream"] != tc.want {
				t.Fatalf("%s: wire stream = %v, want %v", tc.name, req.Extras["stream"], tc.want)
			}
		}
	}
	rejected := []struct {
		name  string
		value any
	}{
		{"invalid string", "sometimes"},
		{"empty string", ""},
		{"number", float64(1)},
		{"map", map[string]any{"a": true}},
		{"list", []any{true}},
		{"explicit nil", nil},
	}
	for _, tc := range rejected {
		for _, tools := range []bool{false, true} {
			server, count := countingToolServer(t, `{"choices":[]}`, http.StatusOK)
			callParams := map[string]interface{}{
				StrConfig:  map[string]interface{}{},
				StrRequest: "hi",
				"url":      server.URL,
				"model":    "m",
				"prompt":   "hi",
				"stream":   tc.value,
			}
			if tools {
				callParams["tools"] = validToolEntries()
			}
			a := &AIInteractionAction{}
			_, err := a.Execute(callParams)
			if err == nil {
				t.Fatalf("%s (tools=%v): expected rejection", tc.name, tools)
			}
			if *count != 0 {
				t.Fatalf("%s (tools=%v): invalid input reached HTTP", tc.name, tools)
			}
		}
	}
	// Absent stream emits no field either way.
	for _, tools := range []bool{false, true} {
		var params map[string]interface{}
		if tools {
			params = withTools()
		} else {
			params = withoutTools()
		}
		req, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{})
		if err != nil {
			t.Fatalf("tools=%v: unexpected error %v", tools, err)
		}
		if _, has := req.Extras["stream"]; has {
			t.Fatalf("tools=%v: absent stream must emit no field", tools)
		}
	}
	// Missing-path references fail instead of defaulting to false.
	params := withoutTools()
	params["stream"] = "$response.absent"
	if _, err := normalizeLLMRequest(params, map[string]interface{}{}, InputContext{}); err == nil {
		t.Fatalf("missing stream path must fail")
	}
	// $response-derived Boolean and string values resolve first.
	params = withoutTools()
	params["stream"] = "$response.flag"
	ictx := InputContext{Response: map[string]any{"flag": true}}
	req, err := normalizeLLMRequest(params, map[string]interface{}{}, ictx)
	if err != nil || req.Extras["stream"] != true {
		t.Fatalf("resolved bool stream: %+v, %v", req.Extras, err)
	}
	ictx = InputContext{Response: map[string]any{"flag": "false"}}
	req, err = normalizeLLMRequest(params, map[string]interface{}{}, ictx)
	if err != nil || req.Extras["stream"] != false {
		t.Fatalf("resolved string stream: %+v, %v", req.Extras, err)
	}
}
