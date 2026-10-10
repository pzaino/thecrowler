package agent

import (
	"reflect"
	"strings"
	"testing"
)

// payloadAction returns a fixed payload of any JSON shape.
type payloadAction struct {
	name    string
	payload any
}

func (a *payloadAction) Name() string { return a.name }

func (a *payloadAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, _ := params[StrConfig].(map[string]interface{})
	return map[string]interface{}{
		StrResponse: a.payload,
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   config,
	}, nil
}

// inputCaptureAction records the input it received for assertions.
type inputCaptureAction struct {
	name string
	got  []any
}

func (a *inputCaptureAction) Name() string { return a.name }

func (a *inputCaptureAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	input, _ := params[StrRequest]
	a.got = append(a.got, input)
	config, _ := params[StrConfig].(map[string]interface{})
	return map[string]interface{}{
		StrResponse: map[string]interface{}{"seen": true},
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   config,
	}, nil
}

func propagationAgent(caps []string, steps ...map[string]any) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "prop", Name: "Prop", TrustLevel: "trusted",
			Capabilities: caps,
		},
		Jobs: []Job{{Name: "Prop", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: steps}},
	}
}

// TestPayloadShapesPropagate covers nil/string/bool/numeric/slice/nested-map
// previous responses flowing into the next step without panic or reshaping.
func TestPayloadShapesPropagate(t *testing.T) {
	payloads := []any{
		nil,
		"plain string payload",
		true,
		float64(42),
		[]any{"a", float64(1), true},
		map[string]any{"nested": map[string]any{"deep": []any{float64(1)}}},
	}
	for i, payload := range payloads {
		engine := NewJobEngine()
		engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: payload})
		capture := &inputCaptureAction{name: "APIRequest"}
		engine.RegisterAction(capture)
		agentCfg := propagationAgent(
			[]string{"ai_reasoning", "api_request"},
			map[string]any{"action": "AIInteraction", "params": map[string]any{}},
			map[string]any{"action": "APIRequest", "params": map[string]any{}},
		)
		if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
			t.Fatalf("payload %d (%T): unexpected error: %v", i, payload, err)
		}
		if len(capture.got) != 1 {
			t.Fatalf("payload %d: expected one capture, got %d", i, len(capture.got))
		}
		if !reflect.DeepEqual(capture.got[0], payload) {
			t.Fatalf("payload %d (%T): got %#v, want %#v", i, payload, capture.got[0], payload)
		}
	}
}

// TestStringPayloadWithExplicitRequest is the headline no-panic case: a
// string response followed by an explicitly-configured step.
func TestStringPayloadWithExplicitRequest(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: "raw string"})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	agentCfg := propagationAgent(
		[]string{"ai_reasoning", "api_request"},
		map[string]any{"action": "AIInteraction", "params": map[string]any{}},
		map[string]any{"action": "APIRequest", "params": map[string]any{
			StrRequest: map[string]any{"fixed": true},
		}},
	)
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := capture.got[0].(map[string]any)
	if !ok {
		t.Fatalf("expected mapping input, got %#v", capture.got[0])
	}
	if got["fixed"] != true {
		t.Fatalf("explicit request must survive: %#v", got)
	}
}

// TestExplicitScalarRequestStandsAlone pins the replacement policy for
// explicit scalar inputs.
func TestExplicitScalarRequestStandsAlone(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: map[string]any{"a": float64(1)}})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	agentCfg := propagationAgent(
		[]string{"ai_reasoning", "api_request"},
		map[string]any{"action": "AIInteraction", "params": map[string]any{}},
		map[string]any{"action": "APIRequest", "params": map[string]any{
			StrRequest: "explicit scalar",
		}},
	)
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.got[0] != "explicit scalar" {
		t.Fatalf("explicit scalar must win, got %#v", capture.got[0])
	}
}

// TestMappingMergePayloadWins documents collision precedence: on key
// conflicts the previous payload overwrites the explicit mapping, and
// neither side is mutated.
func TestMappingMergePayloadWins(t *testing.T) {
	explicit := map[string]any{"keep": "explicit", "clash": "explicit"}
	payload := map[string]any{"clash": "payload", "extra": float64(1)}
	engine := NewJobEngine()
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: payload})
	capture := &inputCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	agentCfg := propagationAgent(
		[]string{"ai_reasoning", "api_request"},
		map[string]any{"action": "AIInteraction", "params": map[string]any{}},
		map[string]any{"action": "APIRequest", "params": map[string]any{StrRequest: explicit}},
	)
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := capture.got[0].(map[string]any)
	if got["keep"] != "explicit" || got["clash"] != "payload" || got["extra"] != float64(1) {
		t.Fatalf("unexpected merge: %#v", got)
	}
	if explicit["clash"] != "explicit" || len(explicit) != 2 {
		t.Fatalf("explicit map mutated: %#v", explicit)
	}
	if payload["clash"] != "payload" || len(payload) != 2 {
		t.Fatalf("payload mutated: %#v", payload)
	}
	if _, exists := agentCfg.Jobs[0].Steps[1]["params"].(map[string]any)[StrRequest].(map[string]any)["extra"]; exists {
		t.Fatalf("stored manifest mutated by merge")
	}
}

// TestPriorConfigShapes covers nil (ignored), mapping (merged), and scalar
// (descriptive error, never panic) prior configs.
func TestPriorConfigShapes(t *testing.T) {
	// Scalar prior config surfaces a controlled error.
	engine2 := NewJobEngine()
	engine2.RegisterAction(&poisonConfigAction{name: "AIInteraction"})
	engine2.RegisterAction(&testStepAction{name: "APIRequest"})
	agentCfg2 := propagationAgent(
		[]string{"ai_reasoning", "api_request"},
		map[string]any{"action": "AIInteraction", "params": map[string]any{}},
		map[string]any{"action": "APIRequest", "params": map[string]any{}},
	)
	err := engine2.ExecuteJobs(agentCfg2, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "invalid prior step config") {
		t.Fatalf("expected invalid prior config error, got %v", err)
	}

	// Nil and mapping prior configs merge cleanly (unit level).
	params := map[string]any{StrConfig: map[string]any{"kept": true}}
	if err := mergePriorConfig(params, nil); err != nil {
		t.Fatalf("nil prior config must be ignored, got %v", err)
	}
	if err := mergePriorConfig(params, map[string]any{"added": float64(1)}); err != nil {
		t.Fatalf("mapping prior config must merge, got %v", err)
	}
	config := params[StrConfig].(map[string]any)
	if config["kept"] != true || config["added"] != float64(1) {
		t.Fatalf("unexpected merged config: %#v", config)
	}
}

// poisonConfigAction returns a scalar config to exercise the error path.
type poisonConfigAction struct{ name string }

func (a *poisonConfigAction) Name() string { return a.name }

func (a *poisonConfigAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		StrResponse: map[string]interface{}{"a": float64(1)},
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   "not-a-mapping",
	}, nil
}

// TestEventAddressableAtJobLevel proves the triggering event flows into
// every step's config and resolves as $event through the canonical root.
func TestEventAddressableAtJobLevel(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&payloadAction{name: "AIInteraction", payload: map[string]any{"a": float64(1)}})
	capture := &configCaptureAction{name: "APIRequest"}
	engine.RegisterAction(capture)
	agentCfg := propagationAgent(
		[]string{"ai_reasoning", "api_request"},
		map[string]any{"action": "AIInteraction", "params": map[string]any{}},
		map[string]any{"action": "APIRequest", "params": map[string]any{}},
	)
	iCfg := map[string]any{
		"agent_runtime": map[string]any{"identity_enforcement": true, "contract_enforcement": true},
		"event":         map[string]any{"kind": "crawl_completed"},
	}
	if err := engine.ExecuteJobs(agentCfg, iCfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capture.got) != 1 {
		t.Fatalf("expected one capture, got %d", len(capture.got))
	}
	// The same shape resolves $event.kind through NewInputContext.
	params := map[string]any{
		StrRequest: map[string]any{"a": float64(1)},
		StrConfig:  capture.got[0],
	}
	got, err := ResolveString(NewInputContext(params), "$event.kind")
	if err != nil || got != "crawl_completed" {
		t.Fatalf("expected event resolution, got %q, %v", got, err)
	}
}

// configCaptureAction records the step config it received.
type configCaptureAction struct {
	name string
	got  []map[string]any
}

func (a *configCaptureAction) Name() string { return a.name }

func (a *configCaptureAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, _ := params[StrConfig].(map[string]interface{})
	a.got = append(a.got, map[string]any(config))
	return map[string]interface{}{
		StrResponse: map[string]interface{}{"seen": true},
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   config,
	}, nil
}
