package agent

import (
	"sync"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func deepCopyTestManifest() *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "iso", Name: "Iso", TrustLevel: "trusted",
			Capabilities: []string{"ai_reasoning"},
		},
		Jobs: []Job{{Name: "Iso", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{
					"prompt": "hello",
				}},
				{"action": "AIInteraction", "params": map[string]interface{}{
					"prompt": "again",
					"body":   map[string]interface{}{"nested": "$response"},
				}},
			}}},
	}
}

// TestDeepCopyJobIsolatesManifestState: repeated runs must not leak step
// state (input injection, config writes) back into the stored manifest.
func TestDeepCopyJobIsolatesManifestState(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})

	manifest := deepCopyTestManifest()
	for i := 0; i < 2; i++ {
		if err := engine.ExecuteJobs(manifest, runtimeEnforcedCfg()); err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	step2Params, _ := manifest.Jobs[0].Steps[1]["params"].(map[string]interface{})
	if _, exists := step2Params[StrRequest]; exists {
		t.Fatalf("stored manifest gained %q from a previous run", StrRequest)
	}
	if body, ok := step2Params["body"].(map[string]interface{}); ok {
		if body["nested"] != "$response" {
			t.Fatalf("stored nested params mutated: %#v", body)
		}
	}
	step1Params, _ := manifest.Jobs[0].Steps[0]["params"].(map[string]interface{})
	if configMap, ok := step1Params[StrConfig].(map[string]interface{}); ok {
		if _, exists := configMap[cfgKeyAgentRuntime]; exists {
			t.Fatalf("stored step config gained runtime context from a previous run")
		}
	}
}

// TestParallelRunsShareNoMutableState: concurrent executions of one manifest
// must not race on shared step maps (run with -race).
func TestParallelRunsShareNoMutableState(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})

	manifest := deepCopyTestManifest()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- engine.ExecuteJobs(manifest, runtimeEnforcedCfg())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent run failed: %v", err)
		}
	}
}

// TestLegacyRolloutOffUnchanged: with enforcement off, legacy jobs still run.
func TestLegacyRolloutOffUnchanged(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})
	legacy := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial",
		TriggerType: "manual", TriggerName: "legacy",
		Steps: []map[string]interface{}{
			{"action": "RunCommand", "params": map[string]interface{}{"command": "echo hi"}},
		}}}}
	if err := engine.ExecuteJobs(legacy, map[string]any{
		cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: false},
	}); err != nil {
		t.Fatalf("expected legacy execution to pass, got %v", err)
	}
}
