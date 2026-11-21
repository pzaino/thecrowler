package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestOpenAIAliasIsRegistered keeps the documented `provider: openai`
// spelling working alongside `openai-compatible` without duplicating the
// transport.
func TestOpenAIAliasIsRegistered(t *testing.T) {
	resetLLMProvidersForTest()
	provider, ok := getLLMProvider("openai")
	if !ok {
		t.Fatalf("documented openai provider is not registered")
	}
	if provider.Name() != "openai" {
		t.Fatalf("unexpected alias name %q", provider.Name())
	}
	caps := ProviderCapabilitiesFor(provider, "gpt-4o-mini")
	if caps.Provider != "openai" || caps.Chat != LLMCapabilitySupported {
		t.Fatalf("unexpected alias capabilities: %+v", caps)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	response, err := provider.Execute(LLMRequest{URL: server.URL, Model: "gpt-4o-mini", Prompt: "hi"})
	if err != nil {
		t.Fatalf("alias execution failed: %v", err)
	}
	payload, err := ExtractChatPayload(response)
	if err != nil {
		t.Fatalf("alias payload: %v", err)
	}
	normalized, err := NormalizeChatCompletion(payload)
	if err != nil || normalized.Content != "ok" {
		t.Fatalf("alias response lost: %+v, %v", normalized, err)
	}
}

// TestValidateLLMEndpoint pins the provider endpoint contract: HTTP(S) only,
// an explicit host, no embedded credentials, no fragment, and no reliance on
// a dotted DNS suffix (localhost and container names must work).
func TestValidateLLMEndpoint(t *testing.T) {
	valid := []string{
		"http://localhost:11434",
		"http://ollama:11434",
		"http://crowler-ollama:11434/v1/chat/completions",
		"http://127.0.0.1:11434/v1/chat/completions",
		"https://[::1]:11434/v1/chat/completions",
		"https://llm.example.com/v1/chat/completions?tenant=one",
	}
	for _, endpoint := range valid {
		if err := ValidateLLMEndpoint(endpoint); err != nil {
			t.Fatalf("valid endpoint %q rejected: %v", endpoint, err)
		}
	}
	invalid := []string{
		"",
		"   ",
		"ftp://llm.example.com/model",
		"file:///tmp/model",
		"ws://llm.example.com/model",
		"llm.example.com",
		"http:///v1/chat/completions",
		"http://user:secret@llm.example.com/v1/chat/completions",
		"http://llm.example.com/v1/chat/completions#fragment",
		"http://llm example.com/v1",
		"http://llm.example.com/\x00",
		"http://ollama:",
		"http://ollama:0",
		"http://ollama:99999",
		"http://example.com%2f@evil.com/",
	}
	for _, endpoint := range invalid {
		if err := ValidateLLMEndpoint(endpoint); err == nil {
			t.Fatalf("invalid endpoint %q accepted", endpoint)
		}
	}
}

// TestOllamaEndpointNormalization pins base-URL handling and the explicit
// rejection of the native dialect this adapter does not implement.
func TestOllamaEndpointNormalization(t *testing.T) {
	cases := map[string]string{
		"http://ollama:11434":                              "http://ollama:11434/v1/chat/completions",
		"http://ollama:11434/":                             "http://ollama:11434/v1/chat/completions",
		"http://ollama:11434/v1":                           "http://ollama:11434/v1/chat/completions",
		"http://ollama:11434/v1/":                          "http://ollama:11434/v1/chat/completions",
		"http://ollama:11434/v1/chat/completions":          "http://ollama:11434/v1/chat/completions",
		"https://gateway.local/ollama/v1/chat/completions": "https://gateway.local/ollama/v1/chat/completions",
	}
	for input, want := range cases {
		got, err := NormalizeOllamaEndpoint(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if got != want {
			t.Fatalf("%q: got %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{
		"http://ollama:11434/api/chat",
		"http://ollama:11434/api/generate",
		"http://ollama:11434/generate",
		"ftp://ollama:11434",
		"",
	} {
		if _, err := NormalizeOllamaEndpoint(input); err == nil {
			t.Fatalf("%q: expected rejection", input)
		}
	}
}

// TestOllamaToolModelsFromEnv pins the operator attestation parser.
func TestOllamaToolModelsFromEnv(t *testing.T) {
	t.Setenv(ollamaToolModelsEnv, "")
	if got := ollamaToolModelsFromEnv(); got == nil || len(got) != 0 {
		t.Fatalf("empty value must mean explicit unsupported, got %#v", got)
	}
	t.Setenv(ollamaToolModelsEnv, " qwen3:8b, llama3.1:8b ,,")
	got := ollamaToolModelsFromEnv()
	want := []string{"qwen3:8b", "llama3.1:8b"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

// TestOllamaProviderCapabilities pins the tri-state operator attestation.
func TestOllamaProviderCapabilities(t *testing.T) {
	resetLLMProvidersForTest()
	if _, ok := getLLMProvider(ollamaProviderName); !ok {
		t.Fatalf("ollama provider is not registered")
	}

	unknown := NewOllamaProvider(nil)
	if state := unknown.CapabilitiesForModel("any-model").ToolCalls; state != LLMCapabilityUnknown {
		t.Fatalf("nil attestation must be unknown, got %q", state)
	}

	unsupported := NewOllamaProvider([]string{})
	if state := unsupported.CapabilitiesForModel("any-model").ToolCalls; state != LLMCapabilityUnsupported {
		t.Fatalf("empty attestation must be unsupported, got %q", state)
	}

	declared := NewOllamaProvider([]string{"qwen3:8b", "llama3.1:8b"})
	if state := declared.CapabilitiesForModel("qwen3:8b").ToolCalls; state != LLMCapabilitySupported {
		t.Fatalf("declared model must be supported, got %q", state)
	}
	if state := declared.Capabilities().ToolCalls; state != LLMCapabilityUnknown {
		t.Fatalf("provider-level capability with a model list must stay model-dependent, got %q", state)
	}
	if state := declared.CapabilitiesForModel("QWEN3:8B").ToolCalls; state != LLMCapabilitySupported {
		t.Fatalf("case-insensitive declared model must be supported, got %q", state)
	}
	if state := declared.CapabilitiesForModel("mistral:7b").ToolCalls; state != LLMCapabilityUnsupported {
		t.Fatalf("undeclared model must be unsupported, got %q", state)
	}

	wildcard := NewOllamaProvider([]string{"*"})
	if state := wildcard.CapabilitiesForModel("any-model").ToolCalls; state != LLMCapabilitySupported {
		t.Fatalf("wildcard attestation must be supported, got %q", state)
	}

	caps := declared.CapabilitiesForModel("qwen3:8b")
	if caps.Provider != ollamaProviderName || caps.NativeProtocol != "openai-compatible" {
		t.Fatalf("unexpected provider identity: %+v", caps)
	}
	if caps.Chat != LLMCapabilitySupported || caps.Cancellation != LLMCapabilitySupported {
		t.Fatalf("unexpected transport capabilities: %+v", caps)
	}
}

// TestOllamaProviderToolRoundTrip drives a complete Ollama-compatible
// model/tool/result/model exchange through RunToolLoop. The mock server uses
// Ollama's shape: object arguments and a missing call ID.
func TestOllamaProviderToolRoundTrip(t *testing.T) {
	type exchange struct {
		path    string
		payload map[string]any
	}
	var mu sync.Mutex
	var exchanges []exchange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		mu.Lock()
		exchanges = append(exchanges, exchange{path: r.URL.Path, payload: payload})
		round := len(exchanges)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if round == 1 {
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-1","model":"qwen3:8b",
				"choices":[{"index":0,"message":{"role":"assistant","content":"",
					"tool_calls":[{"function":{"name":"lookup","arguments":{"q":"invoice"}}}]},
					"finish_reason":"tool_calls"}],
				"usage":{"total_tokens":12}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-2","model":"qwen3:8b",
			"choices":[{"index":0,"message":{"role":"assistant","content":"completed"},
				"finish_reason":"stop"}]
		}`))
	}))
	defer server.Close()

	registry := NewAgentToolRegistry()
	tool := &stubAgentTool{
		name:        "lookup",
		description: "lookup test data",
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"q": map[string]any{"type": "string"},
			},
			"required":             []any{"q"},
			"additionalProperties": false,
		},
		capabilities: []string{"db_read"},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := NewOllamaProvider([]string{"qwen3:8b"})
	cfg := loopTestConfig(provider, registry)
	cfg.ProviderName = ollamaProviderName
	cfg.URL = server.URL
	cfg.Model = "qwen3:8b"
	cfg.Prompt = "look up invoice"
	cfg.Allowlist = []string{"lookup"}

	result, err := RunToolLoop(cfg)
	if err != nil {
		t.Fatalf("tool loop: %v", err)
	}
	if result.Status != ToolLoopCompleted || result.Content != "completed" {
		t.Fatalf("unexpected terminal: %+v", result)
	}
	if result.ModelRounds != 2 || result.ToolCallCount != 1 {
		t.Fatalf("unexpected accounting: %+v", result)
	}
	if tool.calls != 1 {
		t.Fatalf("tool executed %d times, want 1", tool.calls)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "local-r1-c0" {
		t.Fatalf("synthetic call ID not correlated: %+v", result.ToolCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(exchanges) != 2 {
		t.Fatalf("expected 2 provider exchanges, got %d", len(exchanges))
	}
	for _, ex := range exchanges {
		if ex.path != "/v1/chat/completions" {
			t.Fatalf("base URL not normalized, got path %q", ex.path)
		}
	}
	if _, has := exchanges[0].payload["prompt"]; has {
		t.Fatalf("chat payload must not carry a legacy prompt: %v", exchanges[0].payload)
	}
	tools, ok := exchanges[0].payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools not advertised: %v", exchanges[0].payload)
	}
	secondMessages, ok := exchanges[1].payload["messages"].([]any)
	if !ok || len(secondMessages) < 3 {
		t.Fatalf("tool result not returned to the model: %v", exchanges[1].payload)
	}
	assistant, _ := secondMessages[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant echo missing: %v", secondMessages)
	}
	toolMessage, _ := secondMessages[2].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "local-r1-c0" {
		t.Fatalf("tool message not correlated: %v", secondMessages)
	}
	content, _ := toolMessage["content"].(string)
	if !strings.Contains(content, `"ok":true`) {
		t.Fatalf("tool observation lost: %q", content)
	}
}

// TestToolLoopDeniesExplicitlyUnsupportedProvider proves the capability gate
// runs before any HTTP exchange.
func TestToolLoopDeniesExplicitlyUnsupportedProvider(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"should not run"}}]}`))
	}))
	defer server.Close()

	provider := NewOllamaProvider([]string{}) // explicit: no tool-capable model
	cfg := loopTestConfig(provider, loopTestRegistry(t))
	cfg.ProviderName = ollamaProviderName
	cfg.URL = server.URL
	cfg.Model = "qwen3:8b"

	result, err := RunToolLoop(cfg)
	if err == nil || result.Status != ToolLoopDenied {
		t.Fatalf("expected denial, got %+v, %v", result, err)
	}
	if !strings.Contains(result.Reason, "does not support tool calls") {
		t.Fatalf("unexpected reason: %q", result.Reason)
	}
	if hits != 0 {
		t.Fatalf("provider was contacted %d times after denial", hits)
	}
}

// TestLegacyProviderWithoutCapabilitiesStillRuns pins backward compatibility:
// a provider implementing only LLMProvider is unknown, not unsupported.
func TestLegacyProviderWithoutCapabilitiesStillRuns(t *testing.T) {
	registry := NewAgentToolRegistry()
	tool := &stubAgentTool{name: "ping", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	provider := &scriptedProvider{name: "legacy", responses: []any{
		chatWithCalls(stringCall("c1", "ping", `{}`)),
		chatWithText("done"),
	}}
	cfg := loopTestConfig(provider, registry)
	cfg.Allowlist = []string{"ping"}
	result, err := RunToolLoop(cfg)
	if err != nil {
		t.Fatalf("legacy provider must keep working, got %v", err)
	}
	if result.Status != ToolLoopCompleted || tool.calls != 1 {
		t.Fatalf("legacy provider lost tool execution: %+v calls=%d", result, tool.calls)
	}
}

// TestProviderBlocksCrossOriginRedirect pins redirect containment, including
// a different port on the same host, and proves credentials are not forwarded.
func TestProviderBlocksCrossOriginRedirect(t *testing.T) {
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		if r.Header.Get("Authorization") != "" {
			t.Errorf("authorization leaked to redirect target")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"redirected"}}]}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusFound)
	}))
	defer redirector.Close()

	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{
		URL:    redirector.URL + "/v1/chat/completions",
		Auth:   "Bearer secret-token",
		Model:  "m",
		Prompt: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect denial, got %v", err)
	}
	if targetHits != 0 {
		t.Fatalf("redirect target contacted %d times", targetHits)
	}
}

// TestAgentToolLoopRejectsDataflowProviderFields pins that the inference
// endpoint and credential are policy, not dataflow.
func TestAgentToolLoopRejectsDataflowProviderFields(t *testing.T) {
	for _, field := range loopProviderPolicyFields {
		params := loopTestParams()
		params[field] = "$response." + field
		action := &AgentToolLoopAction{}
		if _, err := action.Execute(params); err == nil ||
			!strings.Contains(err.Error(), "must be statically declared") {
			t.Fatalf("%s: expected static-policy denial, got %v", field, err)
		}
	}
	// Interpolated key/value syntax is likewise authority-bearing.
	params := loopTestParams()
	params["url"] = "http://{{host}}/v1/chat/completions"
	if _, err := (&AgentToolLoopAction{}).Execute(params); err == nil ||
		!strings.Contains(err.Error(), "must be statically declared") {
		t.Fatalf("template endpoint accepted: %v", err)
	}
}

// TestProviderEndpointErrorsDoNotEchoSecrets pins that validation and
// transport failures never print a credential-bearing URL.
func TestProviderEndpointErrorsDoNotEchoSecrets(t *testing.T) {
	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{
		URL:    "http://user:supersecret@llm.example.com/v1/chat/completions",
		Model:  "m",
		Prompt: "hi",
	})
	if err == nil {
		t.Fatalf("credential-bearing endpoint accepted")
	}
	if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "llm.example.com") {
		t.Fatalf("endpoint error leaked URL data: %v", err)
	}
}

// TestProviderAliasContractMatching proves an agent contract cannot be
// bypassed by choosing `openai` instead of `openai-compatible` or vice versa.
func TestProviderAliasContractMatching(t *testing.T) {
	cases := []struct {
		forbidden string
		requested string
	}{
		{"provider:openai", "openai-compatible"},
		{"provider:openai-compatible", "openai"},
		{"provider:openai*", "openai"},
	}
	for _, tc := range cases {
		identity := AgentIdentity{
			TrustLevel: "trusted",
			Contract:   &AgentContract{ForbiddenActions: []string{tc.forbidden}},
		}
		err := enforceAIUsagePolicyForIdentity(identity, LLMRequest{Provider: tc.requested, Model: "m"})
		if err == nil {
			t.Fatalf("contract %q failed to block requested provider %q", tc.forbidden, tc.requested)
		}
	}
	unrelated := AgentIdentity{
		TrustLevel: "trusted",
		Contract:   &AgentContract{ForbiddenActions: []string{"provider:gemini"}},
	}
	if err := enforceAIUsagePolicyForIdentity(unrelated, LLMRequest{Provider: "openai", Model: "m"}); err != nil {
		t.Fatalf("unrelated provider contract blocked openai: %v", err)
	}
}

// TestProviderAllowsSameOriginRedirect proves the tightened policy does not
// break a legitimate same-origin rewrite on the configured endpoint.
func TestProviderAllowsSameOriginRedirect(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v1/chat/completions" {
			http.Redirect(w, r, "/v1/chat/completions-final", http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get("Authorization") != "Bearer endpoint-token" {
			t.Errorf("same-origin redirect lost authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"same origin"}}]}`))
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	response, err := provider.Execute(LLMRequest{
		URL:    server.URL + "/v1/chat/completions",
		Auth:   "Bearer endpoint-token",
		Model:  "m",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("same-origin redirect rejected: %v", err)
	}
	payload, err := ExtractChatPayload(response)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	normalized, err := NormalizeChatCompletion(payload)
	if err != nil || normalized.Content != "same origin" {
		t.Fatalf("redirect response lost: %+v, %v", normalized, err)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
}

// TestProviderCapabilitiesForUnknownLegacyProvider pins the all-unknown
// default used by legacy providers.
func TestProviderCapabilitiesForUnknownLegacyProvider(t *testing.T) {
	caps := ProviderCapabilitiesFor(&scriptedProvider{name: "legacy"}, "m")
	if caps.Provider != "legacy" {
		t.Fatalf("provider name missing: %+v", caps)
	}
	for name, state := range map[string]LLMCapabilityState{
		"chat": caps.Chat, "tools": caps.ToolCalls,
		"structured": caps.StructuredOutput, "streaming": caps.Streaming,
		"tool results": caps.ToolResultMessages, "cancellation": caps.Cancellation,
	} {
		if state != LLMCapabilityUnknown {
			t.Fatalf("%s must default to unknown, got %q", name, state)
		}
	}
}

var _ ProviderCapabilityReporter = (*OpenAICompatibleProvider)(nil)
var _ ModelCapabilityReporter = (*OllamaProvider)(nil)
var _ ContextAwareProvider = (*OllamaProvider)(nil)
