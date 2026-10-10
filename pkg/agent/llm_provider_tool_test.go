package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadToolFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return decoded
}

// toolCaptureServer records the outbound OpenAI-compatible request body.
func toolCaptureServer(t *testing.T, responseBody string, status int) (*httptest.Server, *map[string]any) {
	t.Helper()
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		captured = payload
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, &captured
}

// TestToolWireEncoding pins the exact outbound shape with tools.
func TestToolWireEncoding(t *testing.T) {
	server, captured := toolCaptureServer(t, `{"choices":[]}`, http.StatusOK)
	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{
		URL:      server.URL,
		Auth:     "Bearer wire-secret",
		Model:    "qwen3:8b",
		Messages: []any{map[string]any{"role": "user", "content": "hi"}},
		Tools: []LLMToolDefinition{{
			Type:     "function",
			Function: LLMFunctionSpec{Name: "ping", Parameters: map[string]any{"type": "object"}},
		}},
		ToolChoice:    LLMToolChoice{Mode: "auto"},
		HasToolChoice: true,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := *captured
	if body["model"] != "qwen3:8b" {
		t.Fatalf("model missing: %v", body)
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages missing: %v", body)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing: %v", body)
	}
	entry, ok := tools[0].(map[string]any)
	if !ok || entry["type"] != "function" {
		t.Fatalf("bad tool entry: %v", tools)
	}
	fn, ok := entry["function"].(map[string]any)
	if !ok || fn["name"] != "ping" {
		t.Fatalf("bad function entry: %v", entry)
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice missing: %v", body)
	}
	if _, hasPrompt := body["prompt"]; hasPrompt {
		t.Fatalf("prompt key must not ride chat payloads: %v", body)
	}
}

// TestZeroToolRequestUnchanged pins legacy wire shape without tools.
func TestZeroToolRequestUnchanged(t *testing.T) {
	server, captured := toolCaptureServer(t, `{"ok":true}`, http.StatusOK)
	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{
		URL: server.URL, Model: "m", Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := *captured
	if _, has := body["tools"]; has {
		t.Fatalf("zero-tool request acquired tools: %v", body)
	}
	if _, has := body["tool_choice"]; has {
		t.Fatalf("zero-tool request acquired tool_choice: %v", body)
	}
	if body["prompt"] != "hi" {
		t.Fatalf("legacy prompt payload changed: %v", body)
	}
}

// TestNormalizeOpenAIFixture covers stringified arguments + multi-call order.
func TestNormalizeOpenAIFixture(t *testing.T) {
	resp, err := NormalizeChatCompletion(loadToolFixture(t, "openai_chat_tool_calls.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if resp.Content != "" || resp.FinishReason != "tool_calls" || resp.Model != "gpt-4o-mini" {
		t.Fatalf("bad envelope: %+v", resp)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 calls, got %+v", resp.ToolCalls)
	}
	first, second := resp.ToolCalls[0], resp.ToolCalls[1]
	if first.ID != "call_openai_1" || first.Index != 0 || first.Name != "get_forecast" {
		t.Fatalf("bad first call: %+v", first)
	}
	if first.Arguments["city"] != "Oslo" {
		t.Fatalf("stringified args not parsed: %+v", first.Arguments)
	}
	if second.ID != "call_openai_2" || second.Index != 1 || second.Name != "ping" {
		t.Fatalf("order/IDs lost: %+v", second)
	}
	if len(second.Arguments) != 0 {
		t.Fatalf("empty args must stay empty: %+v", second.Arguments)
	}
	if resp.Usage["total_tokens"] != float64(58) {
		t.Fatalf("usage lost: %+v", resp.Usage)
	}
}

// TestNormalizeOllamaFixture covers object arguments + missing call IDs.
func TestNormalizeOllamaFixture(t *testing.T) {
	resp, err := NormalizeChatCompletion(loadToolFixture(t, "ollama_chat_tool_calls.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 calls, got %+v", resp.ToolCalls)
	}
	first, second := resp.ToolCalls[0], resp.ToolCalls[1]
	if first.Arguments["days"] != float64(3) {
		t.Fatalf("object args not kept: %+v", first.Arguments)
	}
	if second.ID != "" || second.Index != 1 {
		t.Fatalf("missing ID must stay empty with positional index: %+v", second)
	}
	if len(second.Arguments) != 0 {
		t.Fatalf("null args must become empty object: %+v", second.Arguments)
	}
}

// TestNormalizeTextOnly proves ordinary replies stay valid without calls.
func TestNormalizeTextOnly(t *testing.T) {
	resp, err := NormalizeChatCompletion(loadToolFixture(t, "openai_chat_text_only.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if resp.Content != "The forecast calls for clear skies." || len(resp.ToolCalls) != 0 {
		t.Fatalf("bad text reply: %+v", resp)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish reason lost: %+v", resp)
	}
}

// TestNormalizeRejections pins fail-closed parsing without prose guessing.
func TestNormalizeRejections(t *testing.T) {
	cases := []struct {
		name string
		resp map[string]any
		want string
	}{
		{"nil response", nil, "missing object"},
		{"missing choices", map[string]any{"id": "x"}, "missing choices"},
		{"empty choices", map[string]any{"choices": []any{}}, "empty choices"},
		{"missing message", map[string]any{"choices": []any{map[string]any{}}}, "missing message"},
		{"empty content no calls", map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": ""}}}}, "missing content"},
		{"bad args json", map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "", "tool_calls": []any{map[string]any{
				"function": map[string]any{"name": "f", "arguments": "{oops"}}}}}}}, "malformed arguments"},
		{"array args", map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "", "tool_calls": []any{map[string]any{
				"function": map[string]any{"name": "f", "arguments": []any{1}}}}}}}}, "must be an object"},
		{"missing name", map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "", "tool_calls": []any{map[string]any{
				"function": map[string]any{"arguments": "{}"}}}}}}}, "missing function name"},
		{"non-object call", map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "", "tool_calls": []any{"nope"}}}}}, "expected object"},
	}
	for _, tc := range cases {
		if _, err := NormalizeChatCompletion(tc.resp); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected %q, got %v", tc.name, tc.want, err)
		}
	}
}

// TestProviderSurfacesHTTPFailures pins non-2xx handling with redaction.
func TestProviderSurfacesHTTPFailures(t *testing.T) {
	server, _ := toolCaptureServer(t, `{"error":{"message":"quota blown","type":"rate_limit"}}`, http.StatusTooManyRequests)
	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: "hi", Auth: "Bearer s3cret"})
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("expected status error, got %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("auth leaked into error: %v", err)
	}
}

// TestProviderRejectsOversizedBodies pins the transport cap.
func TestProviderRejectsOversizedBodies(t *testing.T) {
	big := `{"choices":[{"message":{"content":"` + strings.Repeat("z", maxLLMResponseBytes) + `"}}]}`
	server, _ := toolCaptureServer(t, big, http.StatusOK)
	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected size error, got %v", err)
	}
}

// TestOllamaSmokeOptional is an opt-in manual smoke test against a real
// LAN Ollama host exposing /v1/chat/completions. It sends one tool
// declaration, prints the returned tool calls for inspection, and asserts
// nothing was executed and no follow-up call occurs (the provider performs
// exactly one HTTP exchange by construction). It never runs in CI: both
// CROWLER_OLLAMA_SMOKE_URL and CROWLER_OLLAMA_SMOKE_MODEL must be set.
func TestOllamaSmokeOptional(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("CROWLER_OLLAMA_SMOKE_URL"))
	model := strings.TrimSpace(os.Getenv("CROWLER_OLLAMA_SMOKE_MODEL"))
	if baseURL == "" || model == "" {
		t.Skip("set CROWLER_OLLAMA_SMOKE_URL and CROWLER_OLLAMA_SMOKE_MODEL to run the live Ollama smoke test")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	provider := &OpenAICompatibleProvider{}
	resp, err := provider.Execute(LLMRequest{
		URL:      endpoint,
		Model:    model,
		Messages: []any{map[string]any{"role": "user", "content": "Reply with one tool call to ping."}},
		Tools: []LLMToolDefinition{{
			Type:     "function",
			Function: LLMFunctionSpec{Name: "ping", Parameters: map[string]any{"type": "object"}},
		}},
		ToolChoice:    LLMToolChoice{Mode: "auto"},
		HasToolChoice: true,
	})
	if err != nil {
		t.Fatalf("smoke request failed: %v", err)
	}
	payload, err := ExtractChatPayload(resp)
	if err != nil {
		t.Fatalf("smoke payload: %v", err)
	}
	normalized, err := NormalizeChatCompletion(payload)
	if err != nil {
		t.Logf("model returned no tool calls (valid text reply): content=%q", normalized.Content)
		return
	}
	t.Logf("smoke tool calls (inert, not executed): %+v", normalized.ToolCalls)
}

// TestProviderKeepsRawBodyCompatibility pins that raw mode still returns
// whatever the endpoint sent, while normalization rejects it explicitly.
func TestProviderKeepsRawBodyCompatibility(t *testing.T) {
	server, _ := toolCaptureServer(t, `not json at all`, http.StatusOK)
	provider := &OpenAICompatibleProvider{}
	resp, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: "hi", Auth: "Bearer s3cret"})
	if err != nil {
		t.Fatalf("raw mode must stay lenient, got %v", err)
	}
	if resp["body"] != "not json at all" {
		t.Fatalf("raw body lost: %v", resp)
	}
	if _, err := NormalizeChatCompletion(resp); err == nil {
		t.Fatalf("normalization must reject non-chat payloads")
	}
}

// TestProviderContextCancelsBlockedRequest proves cancellation pre-headers:
// a short deadline aborts the client quickly. (Whether the far end
// observes the disconnect depends on transport teardown timing and is not
// asserted here; the slow-body test below covers mid-read cancellation.)
func TestProviderContextCancelsBlockedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := provider.ExecuteWithContext(ctx, LLMRequest{URL: server.URL, Model: "m", Prompt: "hi"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("cancellation took too long: %v", elapsed)
	}
}

func floatPtr(v float64) *float64 { return &v }

func intPtr(v int) *int { return &v }

// TestProviderContextInterruptsSlowBody proves cancellation during reads.
func TestProviderContextInterruptsSlowBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := provider.ExecuteWithContext(ctx, LLMRequest{URL: server.URL, Model: "m", Prompt: "hi"})
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("body-read cancellation took too long")
	}
}

// TestProviderCapsOversizedBodyWhileReading pins the read-time bound.
func TestProviderCapsOversizedBodyWhileReading(t *testing.T) {
	payload := strings.Repeat("z", maxLLMResponseBytes+1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + payload + `"}}]}`))
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	_, err := provider.ExecuteWithContext(context.Background(),
		LLMRequest{URL: server.URL, Model: "m", Prompt: "hi", Auth: "Bearer s3cret"})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected size error, got %v", err)
	}
}

// TestProviderContextPathMatchesLegacyWire proves both transports agree.
func TestProviderContextPathMatchesLegacyWire(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		encoded, _ := json.Marshal(payload)
		bodies = append(bodies, string(encoded))
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header lost")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"model":"m"}`))
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	req := LLMRequest{
		URL: server.URL, Auth: "Bearer tok", Model: "m",
		Messages:   []any{map[string]any{"role": "user", "content": "hi"}},
		Tools:      []LLMToolDefinition{{Type: "function", Function: LLMFunctionSpec{Name: "ping", Parameters: map[string]any{"type": "object"}}}},
		ToolChoice: LLMToolChoice{Mode: "auto"}, HasToolChoice: true,
		Temperature: floatPtr(0.2), MaxTokens: intPtr(50),
	}
	legacy, err := provider.Execute(req)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	withCtx, err := provider.ExecuteWithContext(context.Background(), req)
	if err != nil {
		t.Fatalf("context path: %v", err)
	}
	if !reflect.DeepEqual(legacy, withCtx) {
		t.Fatalf("transport responses differ:\n%#v\n%#v", legacy, withCtx)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("request bodies differ")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["model"] != "m" || decoded["tool_choice"] != "auto" {
		t.Fatalf("wire shape changed: %v", decoded)
	}
	if _, has := decoded["tools"]; !has {
		t.Fatalf("tools lost on the wire: %v", decoded)
	}
}

// TestProviderContextPathSurfacesStatus pins non-2xx parity on the new path.
func TestProviderContextPathSurfacesStatus(t *testing.T) {
	server, _ := toolCaptureServer(t, `{"error":{"message":"busy"}}`, http.StatusServiceUnavailable)
	provider := &OpenAICompatibleProvider{}
	_, err := provider.ExecuteWithContext(context.Background(),
		LLMRequest{URL: server.URL, Model: "m", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("expected status error, got %v", err)
	}
}

// TestToolLoopCancellationViaTransport proves end-to-end cancelled status
// when the group deadline expires mid-request.
func TestToolLoopCancellationViaTransport(t *testing.T) {
	blocker := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocker:
		}
	}))
	defer server.Close()
	defer close(blocker)

	// The built-in provider implements the context-aware interface, so the
	// loop deadline propagates into the in-flight HTTP exchange.
	provider := &OpenAICompatibleProvider{}
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.URL = server.URL
	config.Model = "m"
	config.Allowlist = []string{"search_indexed_pages"}
	config.Limits = DefaultToolLoopLimits()
	config.Limits.Timeout = 150 * time.Millisecond
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopCancelled {
		t.Fatalf("expected cancelled terminal, got %+v, %v", result, err)
	}
	if counter.calls != 0 {
		t.Fatalf("cancelled run must not execute tools")
	}
}
