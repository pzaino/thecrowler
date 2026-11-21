package agent

import (
	"testing"
)

// promptOnlyBody extracts the captured outbound payload for assertions.
func capturedBody(t *testing.T, captured *map[string]any) map[string]any {
	t.Helper()
	if captured == nil || *captured == nil {
		t.Fatalf("provider request was not captured")
	}
	return *captured
}

// TestOllamaPromptOnlySerializesAsChatMessages pins the correction: Ollama's
// compatible endpoint is a chat API, so a legacy prompt must become one user
// message rather than an unsupported `prompt` field.
func TestOllamaPromptOnlySerializesAsChatMessages(t *testing.T) {
	server, captured := toolCaptureServer(t,
		`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, 200)
	defer server.Close()

	provider := NewOllamaProvider(nil)
	if _, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: "hello ollama"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := capturedBody(t, captured)
	if _, has := body["prompt"]; has {
		t.Fatalf("ollama prompt-only request must not carry a completion prompt: %v", body)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("expected one synthesized message, got %v", body["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" || message["content"] != "hello ollama" {
		t.Fatalf("unexpected synthesized message: %v", message)
	}
	if body["model"] != "m" {
		t.Fatalf("model lost: %v", body)
	}
}

// TestOllamaExplicitMessagesTakePrecedence proves explicit messages are never
// replaced or augmented by a prompt fallback.
func TestOllamaExplicitMessagesTakePrecedence(t *testing.T) {
	server, captured := toolCaptureServer(t,
		`{"choices":[{"message":{"content":"ok"}}]}`, 200)
	defer server.Close()

	provider := NewOllamaProvider(nil)
	explicit := []any{
		map[string]any{"role": "system", "content": "be terse"},
		map[string]any{"role": "user", "content": "explicit question"},
	}
	if _, err := provider.Execute(LLMRequest{
		URL: server.URL, Model: "m", Messages: explicit, Prompt: "ignored prompt",
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := capturedBody(t, captured)
	if _, has := body["prompt"]; has {
		t.Fatalf("explicit-message request must not carry a prompt: %v", body)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("explicit messages lost: %v", body["messages"])
	}
	first, _ := messages[0].(map[string]any)
	second, _ := messages[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "be terse" {
		t.Fatalf("first explicit message changed: %v", first)
	}
	if second["role"] != "user" || second["content"] != "explicit question" {
		t.Fatalf("second explicit message changed: %v", second)
	}
}

// TestOllamaPromptWithToolsSerializesChatAndTools covers the prompt+tool
// combination at the provider boundary, even when callers bypass the
// AIInteraction normalizer.
func TestOllamaPromptWithToolsSerializesChatAndTools(t *testing.T) {
	server, captured := toolCaptureServer(t,
		`{"choices":[{"message":{"content":"ok"}}]}`, 200)
	defer server.Close()

	provider := NewOllamaProvider(nil)
	if _, err := provider.Execute(LLMRequest{
		URL:           server.URL,
		Model:         "m",
		Prompt:        "call the tool",
		Tools:         []LLMToolDefinition{{Type: "function", Function: LLMFunctionSpec{Name: "ping", Parameters: map[string]any{"type": "object"}}}},
		ToolChoice:    LLMToolChoice{Mode: "auto"},
		HasToolChoice: true,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := capturedBody(t, captured)
	if _, has := body["prompt"]; has {
		t.Fatalf("tool request must not carry a prompt: %v", body)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("tool request needs one synthesized user message: %v", body["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" || message["content"] != "call the tool" {
		t.Fatalf("unexpected tool message: %v", message)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools lost: %v", body["tools"])
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice lost: %v", body)
	}
}

// TestOpenAICompatiblePromptOnlyWireUnchanged protects the legacy
// openai-compatible behavior: prompt-only requests keep the completion-style
// `prompt` field and must not acquire chat messages.
func TestOpenAICompatiblePromptOnlyWireUnchanged(t *testing.T) {
	server, captured := toolCaptureServer(t,
		`{"choices":[{"message":{"content":"ok"}}]}`, 200)
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	if _, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: "legacy prompt"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := capturedBody(t, captured)
	if body["prompt"] != "legacy prompt" {
		t.Fatalf("legacy prompt payload changed: %v", body)
	}
	if _, has := body["messages"]; has {
		t.Fatalf("legacy provider must not synthesize chat messages: %v", body)
	}
}

// TestAIInteractionOllamaPromptOnlyEndToEnd drives the real action path with
// a prompt-only request and asserts the provider receives chat messages.
func TestAIInteractionOllamaPromptOnlyEndToEnd(t *testing.T) {
	resetLLMProvidersForTest()
	server, captured := toolCaptureServer(t,
		`{"choices":[{"message":{"content":"action ok"},"finish_reason":"stop"}]}`, 200)
	defer server.Close()

	action := &AIInteractionAction{}
	result, err := action.Execute(map[string]any{
		"input":    map[string]any{},
		"provider": ollamaProviderName,
		"url":      server.URL,
		"model":    "m",
		"prompt":   "hello from the action",
		"config":   map[string]any{},
	})
	if err != nil {
		t.Fatalf("action: %v (%v)", err, result[StrMessage])
	}
	body := capturedBody(t, captured)
	if _, has := body["prompt"]; has {
		t.Fatalf("action request must not carry a prompt: %v", body)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("action request needs one user message: %v", body["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["content"] != "hello from the action" {
		t.Fatalf("action prompt lost: %v", message)
	}
}
