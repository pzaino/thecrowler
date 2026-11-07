package agent

import (
	"fmt"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestAgentToolLoopExecutesMetadataTool(t *testing.T) {
	_, mock, handler, _ := newSQLMockRuntime(t)

	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WithArgs("price", "42", "uid-7", 5).WillReturnRows(metadataScrapedRows())

	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("c1", "find_scraped_field_metadata", `{"source_id":7,"field":"price","value":"42"}`)),
		chatWithText("done"),
	}}
	config := loopTestConfig(provider, loopTestRegistry(t))
	config.Allowlist = []string{"find_scraped_field_metadata"}
	config.AllowedSources = []uint64{7}
	config.DB = handler
	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("run tool loop: %v", err)
	}
	if result.Status != ToolLoopCompleted || len(result.ToolCalls) != 1 {
		t.Fatalf("unexpected tool loop result: %+v", result)
	}
	if result.ToolCalls[0].Name != "find_scraped_field_metadata" || result.ToolCalls[0].Status != "executed" {
		t.Fatalf("metadata tool was not executed: %+v", result.ToolCalls[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestAgentGoalRunNewNativeToolEvidence(t *testing.T) {
	resetLLMProvidersForTest()
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		AgentsEngine = nil
	})
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "find_scraped_field_metadata", `{"source_id":7,"field":"price","value":"42"}`)),
		chatWithText("done"),
	}}
	RegisterLLMProvider(provider)

	args := map[string]any{"source_id": float64(7), "field": "price", "value": "42"}
	params := goalTestParams()
	params["provider"] = "goal-scripted"
	params["url"] = "http://127.0.0.1:1"
	params["model"] = "scripted"
	params["allowlist"] = []any{"find_scraped_field_metadata"}
	params["allowed_sources"] = []any{float64(7)}
	params["evidence"] = map[string]any{
		"allow":             []any{"tool_observation"},
		"default_max_age":   "15m",
		"max_payload_bytes": float64(4096),
	}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{map[string]any{
			"id": "metadata_matches", "description": "The pinned metadata tool returned matches.",
			"predicate": map[string]any{
				"kind": "evidence_field_number", "evidence": "tool_observation",
				"where": map[string]any{
					"tool":             "find_scraped_field_metadata",
					"arguments_sha256": argumentsHash(args),
				},
				"path": "count", "op": "gte", "value": float64(1),
			},
		}},
	}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse goal spec: %v", err)
	}

	_, mock, handler, _ := newSQLMockRuntime(t)
	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WithArgs("price", "42", "uid-7", 5).WillReturnRows(metadataScrapedRows())

	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err != nil {
		t.Fatalf("goal run: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("unexpected goal result: %v", output)
	}
	if output["tool_call_count"] != 1 || output["model_rounds"] != 1 {
		t.Fatalf("unexpected run accounting: %v", output)
	}
	toolCalls, ok := output["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("missing tool call summary: %v", output["tool_calls"])
	}
	call := toolCalls[0].(map[string]any)
	if call["name"] != "find_scraped_field_metadata" || call["status"] != "executed" {
		t.Fatalf("tool call not executed: %v", call)
	}
	evidence, ok := output["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		t.Fatalf("missing evidence summary: %v", output["evidence"])
	}
	record := evidence[0].(map[string]any)
	if record["kind"] != string(EvidenceToolObservation) || record["trust_class"] != string(TrustToolObservation) {
		t.Fatalf("unexpected observation trust: %v", record)
	}
	if fmt.Sprint(record["source_ids"]) != "[7]" {
		t.Fatalf("unexpected observation source scope: %v", record["source_ids"])
	}
	decision := output["completion_decision"].(map[string]any)
	if decision["criteria_met"] != true || decision["model_claim_used"] != false {
		t.Fatalf("verifier did not decide completion: %v", decision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestAgentGoalRunOutOfScopeMetadataToolDoesNotExecute(t *testing.T) {
	resetLLMProvidersForTest()
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		AgentsEngine = nil
	})
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_crawl_status", `{"source_id":8}`)),
		chatWithText("done"),
	}}
	RegisterLLMProvider(provider)

	params := goalTestParams()
	params["provider"] = "goal-scripted"
	params["url"] = "http://127.0.0.1:1"
	params["model"] = "scripted"
	params["allowlist"] = []any{"get_source_crawl_status"}
	params["allowed_sources"] = []any{float64(7)}
	params["evidence"] = map[string]any{
		"allow":             []any{"tool_observation"},
		"default_max_age":   "15m",
		"max_payload_bytes": float64(4096),
	}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{map[string]any{
			"id": "status", "description": "The pinned crawl-status tool ran.",
			"predicate": map[string]any{
				"kind": "evidence_exists", "evidence": "tool_observation",
				"where": map[string]any{
					"tool":             "get_source_crawl_status",
					"arguments_sha256": argumentsHash(map[string]any{"source_id": float64(8)}),
				},
			},
		}},
	}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse goal spec: %v", err)
	}
	var handler cdb.Handler = &panicOnReadDBHandler{}
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err == nil {
		t.Fatal("out-of-scope tool call must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] == string(GoalCriteriaMet) {
		t.Fatalf("out-of-scope call produced success: %v", output)
	}
	for _, item := range output["tool_calls"].([]any) {
		call := item.(map[string]any)
		if call["status"] == "executed" {
			t.Fatalf("out-of-scope call executed: %v", call)
		}
	}
}
