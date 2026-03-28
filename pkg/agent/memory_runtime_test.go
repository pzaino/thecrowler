package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func runtimeMemoryCfg() map[string]any {
	return map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{MemoryRuntime: true}}
}

func makeMemoryJob(agentID, name, scope, namespace string, steps []map[string]interface{}) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: agentID,
			Name:    name,
			Memory:  &AgentMemory{Scope: scope, Namespace: namespace},
		},
		Jobs: []Job{{Name: name, Process: "serial", TriggerType: "manual", TriggerName: "run", Steps: steps}},
	}
}

func TestMemoryModeNoneDisablesWrites(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {
		if mem, _ := params["memory_context"].(map[string]any); mem["mode"] != memoryModeNone {
			t.Fatalf("expected mode none, got %v", mem["mode"])
		}
	}})

	agentCfg := makeMemoryJob("agent-none", "Agent None", memoryModeNone, "ns-none", []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}})
	if err := engine.ExecuteJobs(agentCfg, runtimeMemoryCfg()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	recs, err := engine.memory.persistent.List("ns-none", time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected no persisted records in none mode, got %d", len(recs))
	}
}

func TestMemoryEphemeralTTLExpiration(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {}})

	agentCfg := makeMemoryJob("agent-ephemeral", "Agent Ephemeral", memoryModeEphemeral, "ns-ephemeral", []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}})
	agentCfg.AgentIdentity.Memory.TTL = "5ms"

	if err := engine.ExecuteJobs(agentCfg, runtimeMemoryCfg()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	time.Sleep(12 * time.Millisecond)
	if err := engine.memory.ephemeral.DeleteExpired(time.Now().UTC()); err != nil {
		t.Fatalf("unexpected cleanup error: %v", err)
	}
	recs, err := engine.memory.ephemeral.List("ns-ephemeral", time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected ephemeral records to expire, got %d", len(recs))
	}
}

func TestMemoryPersistenceAcrossRuns(t *testing.T) {
	engine := NewJobEngine()
	var reads int
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {
		if _, ok := params["memory_read_value"].(map[string]any); ok {
			reads++
		}
	}})

	writer := makeMemoryJob("agent-persist", "Agent Persist", memoryModePersistent, "ns-persist", []map[string]interface{}{{
		"action": "AIInteraction",
		"params": map[string]interface{}{"memory_write_key": "checkpoint", "memory_write_value": map[string]any{"ok": true}},
	}})
	if err := engine.ExecuteJobs(writer, runtimeMemoryCfg()); err != nil {
		t.Fatalf("writer failed: %v", err)
	}

	reader := makeMemoryJob("agent-persist", "Agent Persist", memoryModePersistent, "ns-persist", []map[string]interface{}{{
		"action": "AIInteraction",
		"params": map[string]interface{}{"memory_read_key": "checkpoint"},
	}})
	if err := engine.ExecuteJobs(reader, runtimeMemoryCfg()); err != nil {
		t.Fatalf("reader failed: %v", err)
	}
	if reads == 0 {
		t.Fatalf("expected persisted memory to be readable across runs")
	}
}

func TestMemoryNamespaceIsolation(t *testing.T) {
	engine := NewJobEngine()
	var leaked bool
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {
		if _, ok := params["memory_read_value"].(map[string]any); ok {
			leaked = true
		}
	}})

	writer := makeMemoryJob("agent-a", "Agent A", memoryModePersistent, "ns-a", []map[string]interface{}{{
		"action": "AIInteraction",
		"params": map[string]interface{}{"memory_write_key": "shared-key", "memory_write_value": map[string]any{"secret": "a"}},
	}})
	if err := engine.ExecuteJobs(writer, runtimeMemoryCfg()); err != nil {
		t.Fatalf("writer failed: %v", err)
	}
	reader := makeMemoryJob("agent-b", "Agent B", memoryModePersistent, "ns-b", []map[string]interface{}{{
		"action": "AIInteraction",
		"params": map[string]interface{}{"memory_read_key": "shared-key"},
	}})
	if err := engine.ExecuteJobs(reader, runtimeMemoryCfg()); err != nil {
		t.Fatalf("reader failed: %v", err)
	}
	if leaked {
		t.Fatalf("expected namespace isolation; detected memory leakage")
	}
}

func TestMemoryRetentionLimit(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {}})

	steps := []map[string]interface{}{
		{"action": "AIInteraction", "params": map[string]interface{}{"memory_write_key": "k1", "memory_write_value": map[string]any{"v": 1}}},
		{"action": "AIInteraction", "params": map[string]interface{}{"memory_write_key": "k2", "memory_write_value": map[string]any{"v": 2}}},
		{"action": "AIInteraction", "params": map[string]interface{}{"memory_write_key": "k3", "memory_write_value": map[string]any{"v": 3}}},
	}
	agentCfg := makeMemoryJob("agent-retention", "Agent Retention", memoryModePersistent, "ns-ret", steps)
	agentCfg.AgentIdentity.Memory.Retention = 2
	if err := engine.ExecuteJobs(agentCfg, runtimeMemoryCfg()); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	recs, err := engine.memory.persistent.List("ns-ret", time.Now().UTC())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected retention limit 2, got %d", len(recs))
	}
}

func TestMemoryConcurrencySafety(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {}})

	const workers = 24
	wg := sync.WaitGroup{}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			cfg := makeMemoryJob("agent-concurrent", "Agent Concurrent", memoryModeEphemeral, "ns-concurrent", []map[string]interface{}{{
				"action": "AIInteraction",
				"params": map[string]interface{}{"memory_write_key": fmt.Sprintf("k-%d", idx), "memory_write_value": map[string]any{"i": idx}},
			}})
			_ = engine.ExecuteJobs(cfg, runtimeMemoryCfg())
		}(i)
	}
	wg.Wait()

	recs, err := engine.memory.ephemeral.List("ns-concurrent", time.Now().UTC())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(recs) != workers {
		t.Fatalf("expected %d concurrent records, got %d", workers, len(recs))
	}
}
