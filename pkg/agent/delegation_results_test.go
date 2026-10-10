package agent

import (
	"reflect"
	"strings"
	"testing"
)

// delegationChainAgent builds a caller: Decision to callee, then a capture
// step that records what the delegation returned through $response.
func delegationChainAgent(callerID, calleeRef string, capture *inputCaptureAction) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: callerID, Name: "Caller " + callerID,
			TrustLevel: "trusted", Capabilities: []string{"delegate", "api_request"}},
		Jobs: []Job{{Name: "Caller " + callerID, Process: "serial",
			TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{"condition": map[string]interface{}{
					"condition_type": "if", "expression": "true",
					"on_true": map[string]interface{}{"agent_id": calleeRef},
				}}},
				{"action": capture.name, "params": map[string]interface{}{}},
			}}},
	}
}

func payloadCalleeAgent(agentID, name string) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: agentID, Name: name,
			TrustLevel: "trusted", Capabilities: []string{"all"}},
		Jobs: []Job{{Name: name, Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{}},
			}}},
	}
}

// TestDelegationReturnsPayload proves the caller's next step sees the
// delegate's terminal result through $response instead of nil.
func TestDelegationReturnsPayload(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: map[string]any{"answer": float64(42)}})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	AgentsRegistry.RegisterAgent(payloadCalleeAgent("callee-obj", "CalleeObj"))
	AgentsRegistry.RegisterAgent(delegationChainAgent("caller-obj", "callee-obj", capture))

	if err := engine.ExecuteAgent("caller-obj", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("delegation failed: %v", err)
	}
	if len(capture.got) != 1 {
		t.Fatalf("expected one capture, got %d", len(capture.got))
	}
	want := map[string]any{"answer": float64(42)}
	if !reflect.DeepEqual(capture.got[0], want) {
		t.Fatalf("caller saw %#v, want %#v", capture.got[0], want)
	}
}

// TestNestedDelegationReturnsValues proves values propagate through two
// delegation hops; failures still propagate as errors.
func TestNestedDelegationReturnsValues(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: map[string]any{"leaf": "deep"}})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	leaf := payloadCalleeAgent("leaf", "Leaf")
	middle := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "middle", Name: "Middle",
			TrustLevel: "trusted", Capabilities: []string{"delegate"}},
		Jobs: []Job{{Name: "Middle", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{"condition": map[string]interface{}{
					"condition_type": "if", "expression": "true",
					"on_true": map[string]interface{}{"agent_id": "leaf"},
				}}},
			}}},
	}
	AgentsRegistry.RegisterAgent(leaf)
	AgentsRegistry.RegisterAgent(middle)
	AgentsRegistry.RegisterAgent(delegationChainAgent("top", "middle", capture))

	if err := engine.ExecuteAgent("top", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("nested delegation failed: %v", err)
	}
	want := map[string]any{"leaf": "deep"}
	if len(capture.got) != 1 || !reflect.DeepEqual(capture.got[0], want) {
		t.Fatalf("nested value lost: %#v", capture.got)
	}
}

// TestDelegationFailurePropagates ensures callee errors still surface.
func TestDelegationFailurePropagates(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&flakyAction{name: "AIInteraction", failFirst: 100})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	AgentsRegistry.RegisterAgent(payloadCalleeAgent("bad-callee", "BadCallee"))
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	AgentsRegistry.RegisterAgent(delegationChainAgent("fail-caller", "bad-callee", capture))

	err := engine.ExecuteAgent("fail-caller", runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "BadCallee") {
		t.Fatalf("expected callee failure to propagate, got %v", err)
	}
	if len(capture.got) != 0 {
		t.Fatalf("failed delegation must not feed the next step")
	}
}

// payloadSeqAction returns a configured payload per invocation, in order.
type payloadSeqAction struct {
	name     string
	payloads []any
	calls    int
}

func (a *payloadSeqAction) Name() string { return a.name }

func (a *payloadSeqAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	payload := map[string]any{}
	if a.calls < len(a.payloads) {
		if m, ok := a.payloads[a.calls].(map[string]any); ok {
			payload = m
		}
	}
	a.calls++
	config, _ := params[StrConfig].(map[string]interface{})
	return map[string]interface{}{
		StrResponse: payload,
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   config,
	}, nil
}

// TestSerialGroupsReturnLastTerminal pins multi-group serial semantics.
func TestSerialGroupsReturnLastTerminal(t *testing.T) {
	engine := NewJobEngine()
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	engine.RegisterAction(&payloadSeqAction{name: "AIInteraction", payloads: []any{
		map[string]any{"which": "first"},
		map[string]any{"which": "second"},
	}})
	multi := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "multi", Name: "Multi",
			TrustLevel: "trusted", Capabilities: []string{"all"}},
		Jobs: []Job{
			{Name: "Multi", Process: "serial", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]interface{}{
					{"action": "AIInteraction", "params": map[string]interface{}{}},
				}},
			{Name: "Second", Process: "serial", TriggerType: "manual", TriggerName: "run2",
				Steps: []map[string]interface{}{
					{"action": "AIInteraction", "params": map[string]interface{}{}},
				}},
		},
	}
	AgentsRegistry.RegisterAgent(multi)

	got, err := engine.ExecuteAgentResult("multi", runtimeEnforcedCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok || gotMap["which"] != "second" {
		t.Fatalf("expected last serial terminal, got %#v", got)
	}
}

// TestParallelGroupsReturnKeyedMap pins deterministic keyed results.
func TestParallelGroupsReturnKeyedMap(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: map[string]any{"v": float64(1)}})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	par := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "par", Name: "Alpha",
			TrustLevel: "trusted", Capabilities: []string{"all"}},
		Jobs: []Job{
			{Name: "Alpha", Process: "parallel", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]interface{}{
					{"action": "AIInteraction", "params": map[string]interface{}{}},
				}},
			{Name: "Beta", Process: "parallel", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]interface{}{
					{"action": "AIInteraction", "params": map[string]interface{}{}},
				}},
		},
	}
	AgentsRegistry.RegisterAgent(par)

	got, err := engine.ExecuteAgentResult("par", runtimeEnforcedCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected keyed group results, got %#v", got)
	}
	alpha, ok := gotMap["Alpha"].(map[string]any)
	beta, ok2 := gotMap["Beta"].(map[string]any)
	if !ok || !ok2 {
		t.Fatalf("expected keyed group results, got %#v", got)
	}
	if alpha["v"] != float64(1) || beta["v"] != float64(1) {
		t.Fatalf("unexpected group payloads: %#v", got)
	}
}

// TestExecuteAgentErrorOnlyCompatible proves the retained wrapper keeps
// error-only behavior for existing callers.
func TestExecuteAgentErrorOnlyCompatible(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	AgentsRegistry.RegisterAgent(payloadCalleeAgent("ok-callee", "OkCallee"))
	caller := delegationChainAgent("compat-caller", "ok-callee",
		&inputCaptureAction{name: "APIRequest"})
	engine.RegisterAction(&inputCaptureAction{name: "APIRequest"})
	AgentsRegistry.RegisterAgent(caller)

	if err := engine.ExecuteAgent("compat-caller", runtimeEnforcedCfg()); err != nil {
		t.Fatalf("ExecuteAgent must stay error-only compatible, got %v", err)
	}
}
