package agent

import (
	"strings"
	"testing"
)

func goalTestParams() map[string]any {
	return map[string]any{
		"provider":        "openai-compatible",
		"url":             "http://127.0.0.1:11434/v1/chat/completions",
		"model":           "test-model",
		"objective":       "Confirm source 7 is completed and has indexed pages.",
		"prompt":          "Gather evidence.",
		"allowlist":       []any{"get_source_status", "search_indexed_pages"},
		"allowed_sources": []any{float64(7)},
		"evidence": map[string]any{
			"allow":             []any{"source_status", "indexed_page", "tool_observation"},
			"default_max_age":   "15m",
			"max_payload_bytes": float64(4096),
		},
		"budgets": map[string]any{
			"max_model_rounds":        float64(4),
			"max_tool_calls":          float64(4),
			"max_evidence_records":    float64(8),
			"max_evidence_bytes":      float64(32768),
			"max_total_result_bytes":  float64(65536),
			"max_elapsed":             "30s",
			"max_consecutive_denials": float64(3),
		},
		"acceptance": map[string]any{
			"mode": "all",
			"criteria": []any{
				map[string]any{
					"id":          "source_completed",
					"description": "Source 7 reports completed.",
					"predicate": map[string]any{
						"kind": "resource_status", "resource": "source",
						"resource_id": float64(7), "status": "completed",
					},
				},
				map[string]any{
					"id":          "status_observed",
					"description": "The pinned status tool ran.",
					"predicate": map[string]any{
						"kind": "evidence_exists", "evidence": "tool_observation",
						"where": map[string]any{
							"tool":             "get_source_status",
							"arguments_sha256": strings.Repeat("a", 64),
						},
					},
				},
			},
		},
	}
}

func goalTestIdentity() *AgentIdentity {
	return &AgentIdentity{
		AgentID: "goal-agent", Name: "Goal Agent", TrustLevel: "trusted",
		Capabilities: []string{"goal_execution", "db_read"},
	}
}

func TestParseAgentGoalSpecValid(t *testing.T) {
	spec, err := parseAgentGoalSpec(goalTestParams(), goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if spec.GoalID == "" || spec.SpecHash == "" {
		t.Fatalf("missing id/hash: %+v", spec)
	}
	if spec.Acceptance.Mode != AcceptanceAll || len(spec.Acceptance.Criteria) != 2 {
		t.Fatalf("acceptance lost: %+v", spec.Acceptance)
	}
	if len(spec.AllowedSources) != 1 || spec.AllowedSources[0] != 7 {
		t.Fatalf("scope lost: %+v", spec.AllowedSources)
	}
	if spec.Budgets.MaxModelRounds != 4 || spec.Budgets.MaxElapsed.String() != "30s" {
		t.Fatalf("budgets lost: %+v", spec.Budgets)
	}
	if spec.Provider != "openai-compatible" || spec.URL == "" || spec.Model != "test-model" {
		t.Fatalf("provider lost: %+v", spec)
	}
}

func TestGoalSpecHashStableAndSensitive(t *testing.T) {
	first, err := parseAgentGoalSpec(goalTestParams(), goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := parseAgentGoalSpec(goalTestParams(), goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if first.SpecHash != second.SpecHash {
		t.Fatalf("hash is not stable")
	}
	changed := goalTestParams()
	changed["objective"] = "A different objective."
	third, err := parseAgentGoalSpec(changed, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if third.SpecHash == first.SpecHash {
		t.Fatalf("objective change did not change hash")
	}
}

func TestParseAgentGoalSpecRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"missing objective", func(m map[string]any) { delete(m, "objective") }, "objective is required"},
		{"marker objective", func(m map[string]any) { m["objective"] = "$response.goal" }, "statically declared"},
		{"empty allowlist", func(m map[string]any) { m["allowlist"] = []any{} }, "allowlist must contain"},
		{"empty sources", func(m map[string]any) { m["allowed_sources"] = []any{} }, "allowed_sources must contain"},
		{"unknown evidence", func(m map[string]any) { m["evidence"].(map[string]any)["allow"] = []any{"crawl_event"} }, "unsupported evidence kind"},
		{"event predicate", func(m map[string]any) {
			criteria := m["acceptance"].(map[string]any)["criteria"].([]any)
			criteria[0].(map[string]any)["predicate"] = map[string]any{"kind": "event_observed", "event_type": "crawl_completed"}
		}, "event_provenance_unavailable"},
		{"untrusted path", func(m map[string]any) {
			criteria := m["acceptance"].(map[string]any)["criteria"].([]any)
			criteria[1].(map[string]any)["predicate"] = map[string]any{
				"kind": "evidence_field_equals", "evidence": "indexed_page",
				"where": map[string]any{"source_id": float64(7)},
				"path":  "title", "value": "hello",
			}
		}, "untrusted_path"},
		{"tool observation without hash", func(m map[string]any) {
			criteria := m["acceptance"].(map[string]any)["criteria"].([]any)
			criteria[1].(map[string]any)["predicate"] = map[string]any{
				"kind": "evidence_exists", "evidence": "tool_observation",
				"where": map[string]any{"tool": "get_source_status"},
			}
		}, "tool_observation requires"},
		{"budget ceiling", func(m map[string]any) { m["budgets"].(map[string]any)["max_model_rounds"] = float64(99) }, "max_model_rounds"},
		{"unknown param", func(m map[string]any) { m["unknown"] = true }, "unknown AgentGoalRun param"},
	}
	for _, tc := range cases {
		params := goalTestParams()
		tc.mutate(params)
		if _, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestFreezeGoalSpecsAndDeepCopy(t *testing.T) {
	spec, err := parseAgentGoalSpec(goalTestParams(), goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	jc := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: goalTestIdentity(),
		Jobs: []Job{{
			Name: "Goal Agent", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]any{{
				"action": "AgentGoalRun",
				"params": goalTestParams(),
			}},
		}},
	}
	if err := jc.freezeGoalSpecs(); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if len(jc.Jobs[0].goalSpecs) != 1 || jc.Jobs[0].goalSpecs[0] == nil {
		t.Fatalf("spec not frozen")
	}
	if jc.Jobs[0].goalSpecs[0].SpecHash != spec.SpecHash {
		t.Fatalf("hash mismatch: %s vs %s", jc.Jobs[0].goalSpecs[0].SpecHash, spec.SpecHash)
	}
	copied := deepCopyJob(jc.Jobs[0])
	if len(copied.goalSpecs) != 1 || copied.goalSpecs[0] != jc.Jobs[0].goalSpecs[0] {
		t.Fatalf("deep copy lost frozen spec")
	}
	def, err := jc.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "test"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	roundTrip := def.toJobConfig()
	if len(roundTrip.Jobs[0].goalSpecs) != 1 || roundTrip.Jobs[0].goalSpecs[0] == nil {
		t.Fatalf("registry round trip lost frozen spec")
	}
}

func TestFreezeGoalSpecsRejectsV1AndUnknownTool(t *testing.T) {
	base := func() *JobConfig {
		return &JobConfig{
			FormatVersion: AgentFormatVersionV2,
			AgentIdentity: goalTestIdentity(),
			Jobs: []Job{{
				Name: "Goal Agent", Process: "serial", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]any{{"action": "AgentGoalRun", "params": goalTestParams()}},
			}},
		}
	}
	v1 := base()
	v1.FormatVersion = AgentFormatVersionV1
	v1.AgentIdentity = nil
	if err := v1.freezeGoalSpecs(); err == nil {
		t.Fatalf("v1 goal manifest accepted")
	}
	unknown := base()
	params := goalTestParams()
	params["allowlist"] = []any{"no_such_tool"}
	unknown.Jobs[0].Steps[0]["params"] = params
	if err := unknown.freezeGoalSpecs(); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unknown tool accepted: %v", err)
	}
}

func TestSetGoalRuntimeSpecOverwritesInjectedValues(t *testing.T) {
	spec, err := parseAgentGoalSpec(goalTestParams(), goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	params := map[string]any{
		StrConfig: map[string]any{
			cfgKeyAgentRuntime: map[string]any{
				"goal_spec":     map[string]any{"objective": "forged"},
				"goal_identity": &AgentIdentity{AgentID: "forged"},
			},
		},
	}
	setGoalRuntimeSpec(params, spec, 2, goalTestIdentity(), AgentExecutionContext{Owner: "platform", RunID: "corr", TraceID: "trace"})
	runtime := params[StrConfig].(map[string]any)[cfgKeyAgentRuntime].(map[string]any)
	if runtime["goal_spec"] != spec {
		t.Fatalf("frozen spec not installed")
	}
	if runtime["goal_step_index"] != 2 {
		t.Fatalf("step index missing")
	}
	identity, _ := runtime["goal_identity"].(*AgentIdentity)
	if identity == nil || identity.AgentID != "goal-agent" {
		t.Fatalf("trusted identity not installed: %+v", runtime["goal_identity"])
	}
}
