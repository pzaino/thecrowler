package agent

import (
	"strings"
	"sync"
	"testing"
)

// countingProvider records whether a provider request was attempted.
type countingProvider struct {
	mu    sync.Mutex
	name  string
	calls int
}

func (p *countingProvider) Name() string { return p.name }

func (p *countingProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return map[string]interface{}{"choices": []any{map[string]any{
		"message": map[string]any{"content": "should not run"},
	}}}, nil
}

func (p *countingProvider) providerCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// resultInjectingAction returns an explicit action result that the engine
// merges into the next step, simulating prior-step config and key
// propagation.
type resultInjectingAction struct {
	result map[string]any
}

func (a *resultInjectingAction) Name() string { return "Decision" }

func (a *resultInjectingAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	return a.result, nil
}

func runInheritedPolicyCase(t *testing.T, priorResult map[string]any) error {
	t.Helper()
	resetLLMProvidersForTest()
	provider := &countingProvider{name: defaultLLMProvider}
	RegisterLLMProvider(provider)
	t.Cleanup(func() {
		AgentsEngine = nil
		resetLLMProvidersForTest()
	})

	engine := NewJobEngine()
	engine.RegisterAction(&resultInjectingAction{result: priorResult})
	engine.RegisterAction(&AgentToolLoopAction{})
	AgentsEngine = engine

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "inherited-policy", Name: "Inherited Policy",
			TrustLevel: "trusted", Capabilities: []string{"all"},
			Constraints: &AgentConstraints{MaxSteps: 5},
		},
		Jobs: []Job{{
			Name: "Inherited Policy", Process: "serial",
			TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{}},
				{"action": "AgentToolLoop", "params": map[string]interface{}{
					"prompt":    "go",
					"allowlist": []any{"get_source_status"},
				}},
			},
		}},
	}
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if provider.providerCalls() != 0 {
		t.Fatalf("provider was contacted %d times after denial", provider.providerCalls())
	}
	return err
}

// TestAgentToolLoopRejectsInheritedDynamicConfigFromPriorStep drives the
// engine merge path: a prior step returns an ai.url carrying $response, and
// the following AgentToolLoop must refuse before resolving any provider.
func TestAgentToolLoopRejectsInheritedDynamicConfigFromPriorStep(t *testing.T) {
	err := runInheritedPolicyCase(t, map[string]any{
		StrStatus:   StatusSuccess,
		StrResponse: map[string]interface{}{},
		StrConfig: map[string]any{
			"ai": map[string]any{"url": "$response.url"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "ai.url must be statically declared") {
		t.Fatalf("expected inherited config denial, got %v", err)
	}
}

// TestAgentToolLoopRejectsInheritedTopLevelProviderKeys covers prior-step
// keys merged into the step params (not only config.ai).
func TestAgentToolLoopRejectsInheritedTopLevelProviderKeys(t *testing.T) {
	cases := map[string]string{
		"url":      "$response.url",
		"provider": "$event.provider",
		"model":    "{{model}}",
		"auth":     "$response.auth",
	}
	for key, marker := range cases {
		err := runInheritedPolicyCase(t, map[string]any{
			StrStatus:   StatusSuccess,
			StrResponse: map[string]interface{}{},
			key:         marker,
		})
		if err == nil || !strings.Contains(err.Error(), key+" must be statically declared") {
			t.Fatalf("%s=%q: expected inherited key denial, got %v", key, marker, err)
		}
	}
}

// TestAgentToolLoopRejectsInheritedConfigProviderFields covers the config.ai
// fallback used by resolveLoopString. Every endpoint/credential field must be
// static there as well as in direct step params.
func TestAgentToolLoopRejectsInheritedConfigProviderFields(t *testing.T) {
	for _, field := range loopProviderPolicyFields {
		for _, marker := range []string{"$response.url", "$event.url", "{{host}}"} {
			params := loopTestParams()
			params["allowlist"] = []any{"get_source_status"}
			config := params[StrConfig].(map[string]interface{})
			config["ai"] = map[string]any{field: marker}
			_, err := normalizeToolLoopConfig(params, config, NewInputContext(params),
				AgentIdentity{AgentID: "op", TrustLevel: "trusted", Capabilities: []string{"all"}})
			if err == nil || !strings.Contains(err.Error(), "ai."+field+" must be statically declared") {
				t.Fatalf("%s=%q: expected inherited-policy denial, got %v", field, marker, err)
			}
		}
	}
}

// TestAgentToolLoopAllowsStaticInheritedProviderConfig protects legitimate
// operator-supplied static values and environment-expanded endpoints.
func TestAgentToolLoopAllowsStaticInheritedProviderConfig(t *testing.T) {
	config := map[string]interface{}{
		"ai": map[string]any{
			"provider": "ollama",
			"url":      "http://ollama:11434",
			"model":    "qwen3:8b",
			"auth":     "Bearer operator-static",
		},
	}
	if err := rejectLoopProviderMarkers(nil, config); err != nil {
		t.Fatalf("static inherited provider config rejected: %v", err)
	}
	params := loopTestParams()
	params["url"] = "http://localhost:11434"
	params["provider"] = "ollama"
	params["model"] = "qwen3:8b"
	params["auth"] = "Bearer operator-static"
	if err := rejectLoopProviderMarkers(params, nil); err != nil {
		t.Fatalf("static direct provider config rejected: %v", err)
	}
}
