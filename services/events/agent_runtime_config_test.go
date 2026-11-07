package main

import (
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// TestAgentInputConfigForwardsRuntimeFlags proves that agent runtime rollout
// flags are supplied to event-triggered agents from process configuration. The
// goal runtime requires identity enforcement; failing to forward the flags makes
// every event-triggered AgentGoalRun deny before doing any work.
func TestAgentInputConfigForwardsRuntimeFlags(t *testing.T) {
	configMutex.Lock()
	previous := config.Agent
	config.Agent = cfg.AgentRuntimeConfig{
		IdentityEnforcement: true,
		ContractEnforcement: true,
		MemoryRuntime:       true,
	}
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		config.Agent = previous
		configMutex.Unlock()
	})

	iCfg := agentInputConfig(cdb.Event{Type: "test_event"}, map[string]interface{}{"fixture": "value"})
	flags, ok := iCfg["agent_runtime"].(cfg.AgentRuntimeConfig)
	if !ok {
		t.Fatalf("agent_runtime missing or wrong type: %#v", iCfg["agent_runtime"])
	}
	if !flags.IdentityEnforcement || !flags.ContractEnforcement || !flags.MemoryRuntime {
		t.Fatalf("agent runtime flags were not forwarded: %+v", flags)
	}
	if event, ok := iCfg["event"].(cdb.Event); !ok || event.Type != "test_event" {
		t.Fatalf("event was not preserved: %#v", iCfg["event"])
	}
}

// TestAgentInputConfigCannotBeSetByEventPayload proves the runtime flags come
// from configuration rather than event data. Event payloads are producer
// controlled and must never be able to relax enforcement.
func TestAgentInputConfigCannotBeSetByEventPayload(t *testing.T) {
	configMutex.Lock()
	previous := config.Agent
	config.Agent = cfg.AgentRuntimeConfig{IdentityEnforcement: true}
	configMutex.Unlock()
	t.Cleanup(func() {
		configMutex.Lock()
		config.Agent = previous
		configMutex.Unlock()
	})

	iCfg := agentInputConfig(cdb.Event{Type: "test_event"}, map[string]interface{}{
		"agent_runtime": map[string]interface{}{"identity_enforcement": false},
	})
	flags, ok := iCfg["agent_runtime"].(cfg.AgentRuntimeConfig)
	if !ok || !flags.IdentityEnforcement {
		t.Fatalf("event metadata weakened runtime enforcement: %#v", iCfg["agent_runtime"])
	}
}
