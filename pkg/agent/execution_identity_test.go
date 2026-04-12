package agent

import (
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type testStepAction struct {
	name   string
	sleep  time.Duration
	execFn func(params map[string]interface{})
}

func (a *testStepAction) Name() string { return a.name }

func (a *testStepAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	if a.sleep > 0 {
		time.Sleep(a.sleep)
	}
	if a.execFn != nil {
		a.execFn(params)
	}
	return map[string]interface{}{StrStatus: StatusSuccess, StrResponse: map[string]interface{}{}}, nil
}

func makeIdentityAgent(actionNames ...string) *JobConfig {
	steps := make([]map[string]interface{}, 0, len(actionNames))
	for _, actionName := range actionNames {
		steps = append(steps, map[string]interface{}{"action": actionName, "params": map[string]interface{}{}})
	}
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID:      "agent-id",
			Name:         "Identity Agent",
			TrustLevel:   "trusted",
			Capabilities: []string{"all"},
		},
		Jobs: []Job{{Name: "Identity Agent", Process: "serial", TriggerType: "manual", TriggerName: "run", Steps: steps}},
	}
}

func runtimeEnforcedCfg() map[string]any {
	return map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: true}}
}

func TestExecuteAgentCapabilityDeny(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})

	agentCfg := makeIdentityAgent("RunCommand")
	agentCfg.AgentIdentity.Capabilities = []string{"create_event"}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "capability gate denied action RunCommand") {
		t.Fatalf("expected capability denied error, got: %v", err)
	}
}

func TestExecuteAgentTrustLevelDeny(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "DBQuery"})

	agentCfg := makeIdentityAgent("DBQuery")
	agentCfg.AgentIdentity.TrustLevel = "restricted"
	agentCfg.AgentIdentity.Capabilities = []string{"db_query"}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "trust gate denied action DBQuery") {
		t.Fatalf("expected trust denied error, got: %v", err)
	}
}

func TestExecuteAgentConstraintBudgetExhaustion(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "CreateEvent"})

	agentCfg := makeIdentityAgent("CreateEvent", "CreateEvent")
	agentCfg.AgentIdentity.Capabilities = []string{"create_event"}
	agentCfg.AgentIdentity.Constraints = &AgentConstraints{EventRateLimit: 1}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "event_rate_limit exceeded") {
		t.Fatalf("expected event rate limit error, got: %v", err)
	}
}

func TestExecuteAgentStepCountTermination(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})

	agentCfg := makeIdentityAgent("AIInteraction", "AIInteraction")
	agentCfg.AgentIdentity.Capabilities = []string{"ai_interaction"}
	agentCfg.AgentIdentity.Constraints = &AgentConstraints{MaxSteps: 1}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected max_steps exceeded error, got: %v", err)
	}
}

func TestExecuteAgentTimeBudgetTermination(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", sleep: 25 * time.Millisecond})

	agentCfg := makeIdentityAgent("AIInteraction", "AIInteraction")
	agentCfg.AgentIdentity.Capabilities = []string{"ai_interaction"}
	agentCfg.AgentIdentity.Constraints = &AgentConstraints{TimeBudget: "5ms"}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "time_budget exceeded") {
		t.Fatalf("expected time_budget exceeded error, got: %v", err)
	}
}

func TestExecuteAgentIdentityContextInjected(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction", execFn: func(params map[string]interface{}) {
		configMap, ok := params[StrConfig].(map[string]interface{})
		if !ok {
			t.Fatalf("expected config map")
		}
		runtimeRaw, ok := configMap[cfgKeyAgentRuntime].(map[string]interface{})
		if !ok {
			t.Fatalf("expected agent_runtime context map")
		}
		if strings.TrimSpace(runtimeRaw["run_id"].(string)) == "" {
			t.Fatalf("expected non-empty run_id")
		}
		if strings.TrimSpace(runtimeRaw["trace_id"].(string)) == "" {
			t.Fatalf("expected non-empty trace_id")
		}
	}})

	agentCfg := makeIdentityAgent("AIInteraction")
	agentCfg.AgentIdentity.Capabilities = []string{"ai_interaction"}
	agentCfg.AgentIdentity.Owner = "security-team"

	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLegacyJobRunsWhenIdentityEnforcementDisabled(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})

	legacy := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial", TriggerType: "manual", TriggerName: "legacy", Steps: []map[string]interface{}{{"action": "RunCommand", "params": map[string]interface{}{}}}}}}

	if err := engine.ExecuteJobs(legacy, map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: false}}); err != nil {
		t.Fatalf("expected legacy execution to pass with enforcement disabled, got %v", err)
	}
}

func TestExecuteAgentByIDAndName(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})

	AgentsRegistry = NewJobConfig()
	defCfg := makeIdentityAgent("AIInteraction")
	defCfg.AgentIdentity.Capabilities = []string{"ai_interaction"}
	AgentsRegistry.RegisterAgent(defCfg)

	if err := engine.ExecuteAgent("agent-id", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected id lookup to work, got %v", err)
	}
	if err := engine.ExecuteAgent("Identity Agent", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected name lookup to work, got %v", err)
	}
}
