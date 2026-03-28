package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type failingProvider struct{}

func (p *failingProvider) Name() string { return "failing-provider" }
func (p *failingProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	return nil, errors.New("provider unavailable")
}

type failingMemoryBackend struct {
	listErr bool
	setErr  bool
}

func (b *failingMemoryBackend) Get(_ context.Context, namespace, key string, now time.Time) (MemoryRecord, bool, error) {
	return MemoryRecord{}, false, nil
}

func (b *failingMemoryBackend) Set(_ context.Context, namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error {
	if b.setErr {
		return errors.New("memory backend set failure")
	}
	return nil
}

func (b *failingMemoryBackend) List(_ context.Context, namespace string, now time.Time) ([]MemoryRecord, error) {
	if b.listErr {
		return nil, errors.New("memory backend list failure")
	}
	return []MemoryRecord{}, nil
}

func (b *failingMemoryBackend) Delete(_ context.Context, namespace, key string) error { return nil }
func (b *failingMemoryBackend) DeleteExpired(_ context.Context, now time.Time) error  { return nil }
func (b *failingMemoryBackend) ClearNamespace(_ context.Context, namespace string) error {
	return nil
}

func TestMilestone9FaultInjectionInvalidConfig(t *testing.T) {
	err := ValidateAgentConfig([]byte("{not-json"), "json", ValidationModeLenient, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid json") {
		t.Fatalf("expected invalid json error, got %v", err)
	}
}

func TestMilestone9FaultInjectionProviderFailure(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	RegisterLLMProvider(&failingProvider{})

	a := &AIInteractionAction{}
	_, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{},
		StrRequest: "input",
		"provider": "failing-provider",
		"url":      "https://example.com/v1/chat/completions",
	})
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected provider failure to bubble up, got %v", err)
	}
}

func TestMilestone9FaultInjectionMemoryBackendFailureOnInject(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	engine.SetPersistentMemoryBackend(&failingMemoryBackend{listErr: true})

	agentCfg := makeMemoryJob("agent-failing-mem", "Agent Failing Memory", memoryModePersistent, "ns-failing", []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}})
	err := engine.ExecuteJobs(agentCfg, map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{MemoryRuntime: true}})
	if err == nil || !strings.Contains(err.Error(), "memory runtime inject failed") {
		t.Fatalf("expected memory inject failure, got %v", err)
	}
}

func TestMilestone9FaultInjectionMemoryBackendFailureOnPersist(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {}})
	engine.SetPersistentMemoryBackend(&failingMemoryBackend{setErr: true})

	agentCfg := makeMemoryJob("agent-failing-mem", "Agent Failing Memory", memoryModePersistent, "ns-failing", []map[string]interface{}{{
		"action": "AIInteraction",
		"params": map[string]interface{}{"memory_write_key": "checkpoint", "memory_write_value": map[string]any{"ok": true}},
	}})
	err := engine.ExecuteJobs(agentCfg, map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{MemoryRuntime: true}})
	if err == nil || !strings.Contains(err.Error(), "memory runtime persist failed") {
		t.Fatalf("expected memory persist failure, got %v", err)
	}
}

func TestMilestone9RolloutSafetyFlagsOffByDefault(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})

	legacy := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial", TriggerType: "manual", TriggerName: "legacy", Steps: []map[string]interface{}{{"action": "RunCommand", "params": map[string]interface{}{}}}}}}

	if err := engine.ExecuteJobs(legacy, map[string]any{}); err != nil {
		t.Fatalf("expected legacy execution to pass when rollout flags are omitted/off, got %v", err)
	}
}

func TestMilestone9CompatibilityGoldenLegacyFixtures(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		typeName string
	}{
		{name: "legacy-golden-yaml", path: filepath.Clean("testdata/legacy.golden.yaml"), typeName: "yaml"},
		{name: "legacy-golden-json", path: filepath.Clean("testdata/legacy.golden.json"), typeName: "json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read fixture failed: %v", err)
			}
			if err := ValidateAgentConfig(data, tc.typeName, ValidationModeLenient, nil); err != nil {
				t.Fatalf("expected legacy golden fixture to validate in lenient mode, got %v", err)
			}
			cfg, err := parseAgentsBytes(data, tc.typeName)
			if err != nil {
				t.Fatalf("expected legacy golden fixture to parse, got %v", err)
			}
			if len(cfg.Jobs) == 0 {
				t.Fatalf("expected at least one job from fixture")
			}
		})
	}
}
