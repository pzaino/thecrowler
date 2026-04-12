package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type captureProvider struct {
	name    string
	lastReq LLMRequest
}

func (p *captureProvider) Name() string { return p.name }
func (p *captureProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	p.lastReq = req
	return map[string]interface{}{"ok": true, "provider": p.name}, nil
}

func TestLLMProviderInterfaceContract(t *testing.T) {
	provider := &OpenAICompatibleProvider{}
	if provider.Name() != defaultLLMProvider {
		t.Fatalf("expected provider name %s, got %s", defaultLLMProvider, provider.Name())
	}
}

func TestProviderSelectionPriority(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})

	cp := &captureProvider{name: "mock-provider"}
	RegisterLLMProvider(cp)

	a := &AIInteractionAction{}
	result, err := a.Execute(map[string]interface{}{
		StrConfig: map[string]interface{}{
			"ai": map[string]interface{}{"provider": "openai-compatible", "url": "https://example.com/v1/chat", "auth": "Bearer x", "model": "gpt-4o-mini"},
		},
		StrRequest: "hello",
		"provider": "mock-provider",
		"url":      "https://example.com/v1/chat",
		"prompt":   "hi",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result[StrStatus] != StatusSuccess {
		t.Fatalf("expected success status")
	}
	if cp.lastReq.Provider != "mock-provider" {
		t.Fatalf("expected step-level provider override, got %s", cp.lastReq.Provider)
	}
}

func TestNormalizeLLMRequestMapping(t *testing.T) {
	params := map[string]interface{}{
		"prompt":      "hello",
		"url":         "https://example.com/v1/chat/completions",
		"model":       "gpt-4o-mini",
		"temperature": "0.2",
		"max_tokens":  "128",
		"top_p":       "0.7",
	}
	config := map[string]interface{}{}
	input := map[string]interface{}{StrRequest: "ignored"}

	req, err := normalizeLLMRequest(params, config, input)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if req.Model != "gpt-4o-mini" || req.URL == "" {
		t.Fatalf("unexpected normalized request: %+v", req)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Fatalf("expected parsed temperature")
	}
	if req.MaxTokens == nil || *req.MaxTokens != 128 {
		t.Fatalf("expected parsed max_tokens")
	}
	if req.TopP == nil || *req.TopP != 0.7 {
		t.Fatalf("expected parsed top_p")
	}
}

func TestNormalizeLLMRequestMessagesPriority(t *testing.T) {
	params := map[string]interface{}{
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		"prompt":   "fallback",
		"url":      "https://example.com/v1/chat/completions",
	}
	req, err := normalizeLLMRequest(params, map[string]interface{}{}, map[string]interface{}{StrRequest: "ignored"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(req.Messages) != 1 || req.Prompt != "fallback" {
		t.Fatalf("expected messages to be normalized and prompt retained for fallback")
	}
}

func TestAIUsagePolicyContractRestrictions(t *testing.T) {
	cfg := map[string]interface{}{
		cfgKeyAgentRuntime: map[string]interface{}{
			"identity_snapshot": map[string]interface{}{
				"trust_level": "trusted",
				"agent_contract": map[string]interface{}{
					"forbidden_actions": []interface{}{"model:gpt-4*", "provider:bad*"},
				},
			},
		},
	}
	if err := enforceAIUsagePolicy(cfg, LLMRequest{Provider: "openai-compatible", Model: "gpt-4o"}); err == nil {
		t.Fatalf("expected model contract denial")
	}
	if err := enforceAIUsagePolicy(cfg, LLMRequest{Provider: "bad-provider", Model: "gpt-4o-mini"}); err == nil {
		t.Fatalf("expected provider contract denial")
	}
}

func TestAIUsagePolicyTrustRestriction(t *testing.T) {
	cfg := map[string]interface{}{
		cfgKeyAgentRuntime: map[string]interface{}{
			"identity_snapshot": map[string]interface{}{"trust_level": "restricted"},
		},
	}
	if err := enforceAIUsagePolicy(cfg, LLMRequest{Provider: defaultLLMProvider, Model: "gpt-4.1"}); err == nil {
		t.Fatalf("expected trust-based denial")
	}
	if err := enforceAIUsagePolicy(cfg, LLMRequest{Provider: defaultLLMProvider, Model: "gpt-4o-mini"}); err != nil {
		t.Fatalf("expected mini model to be allowed, got %v", err)
	}
}

func TestAIInteractionMockProviderIntegration(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})

	cp := &captureProvider{name: "mock"}
	RegisterLLMProvider(cp)

	a := &AIInteractionAction{}
	result, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{},
		StrRequest: "input prompt",
		"provider": "mock",
		"url":      "https://example.com/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result[StrStatus] != StatusSuccess {
		t.Fatalf("expected success status")
	}
	if cp.lastReq.Prompt != "input prompt" {
		t.Fatalf("expected prompt fallback from input")
	}
}

func TestAIInteractionOpenAICompatibleRegression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST method")
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed decoding body: %v", err)
		}
		if payload["model"] != "gpt-4o-mini" {
			t.Fatalf("expected model in request body")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "resp-1", "choices": []interface{}{}})
	}))
	defer server.Close()

	a := &AIInteractionAction{}
	result, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{"auth": "Bearer test"},
		StrRequest: "legacy compatible prompt",
		"url":      server.URL,
		"model":    "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result[StrStatus] != StatusSuccess {
		t.Fatalf("expected success status")
	}
	out, ok := result[StrResponse].(map[string]interface{})
	if !ok {
		t.Fatalf("expected response map")
	}
	if len(out) == 0 {
		t.Fatalf("expected non-empty provider response in output")
	}
}

func TestAIInteractionUnknownProvider(t *testing.T) {
	a := &AIInteractionAction{}
	_, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{},
		StrRequest: "hello",
		"provider": "does-not-exist",
		"url":      "https://example.com/v1/chat/completions",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported AI provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}
