package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

const (
	testGoalLedgerID  = "ledger-test"
	testGoalRunID     = "run-test"
	testGoalGoalID    = "goal-test"
	testGoalSpecHash  = "spec-test"
	testGoalNonceHash = "nonce-test"
	testGoalAgentID   = "agent-test"
	testGoalOwner     = "owner-test"
)

func goalEvalSpec(mode AcceptanceMode, criteria ...CriterionSpec) *AgentGoalSpec {
	return &AgentGoalSpec{
		GoalID: "goal-test", Objective: "objective", SpecHash: "spec-test",
		AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{
			Allow: []EvidenceKind{
				EvidenceSourceStatus, EvidenceIndexedPage, EvidenceArtifact,
				EvidenceToolObservation,
			},
			DefaultMaxAge:   15 * time.Minute,
			MaxPayloadBytes: 4096,
		},
		Acceptance: AcceptanceSpec{Mode: mode, Criteria: criteria},
	}
}

func goalEvalRecord(kind EvidenceKind, selector EvidenceSelector, payload map[string]any, observedAt time.Time) EvidenceRecord {
	record := EvidenceRecord{
		EvidenceID: "ev_" + string(kind) + "_" + observedAt.Format("150405.000000000"),
		LedgerID:   testGoalLedgerID, RunID: testGoalRunID, RunNonceHash: testGoalNonceHash,
		GoalID: testGoalGoalID, GoalSpecHash: testGoalSpecHash,
		AgentID: testGoalAgentID, Owner: testGoalOwner,
		Kind: kind, Producer: "test", TrustClass: TrustSystemVerified,
		SelectorSHA256: selectorHash(selector),
		SourceIDs:      []uint64{7}, ObservedAt: observedAt, DataUpdatedAt: observedAt,
		ExpiresAt: observedAt.Add(15 * time.Minute), Payload: payload,
	}
	return sealEvalRecord(record)
}

func sealEvalRecord(record EvidenceRecord) EvidenceRecord {
	encoded, _ := json.Marshal(record.Payload)
	sum := sha256.Sum256(encoded)
	record.PayloadSHA256 = hex.EncodeToString(sum[:])
	record.RecordSHA256 = recordHash(record)
	return record
}

func goalEvalResource(resource string, id uint64, status string, observedAt, dataUpdatedAt time.Time) ResourceObservation {
	return ResourceObservation{
		Resource: resource, ID: id, Status: status,
		ObservedAt: observedAt, DataUpdatedAt: dataUpdatedAt,
		RunID: testGoalRunID, GoalID: testGoalGoalID, GoalSpecHash: testGoalSpecHash,
		Source: "db:test",
	}
}

func goalEvalInput(spec *AgentGoalSpec, records []EvidenceRecord, resources map[string]ResourceObservation) GoalEvalInput {
	return GoalEvalInput{
		Spec: spec, Records: records, Resources: resources,
		Queries: map[string]EvidenceQueryResult{}, Now: time.Now().UTC(),
		LedgerID: testGoalLedgerID, RunID: testGoalRunID, GoalID: testGoalGoalID,
		GoalSpecHash: testGoalSpecHash, RunNonceHash: testGoalNonceHash,
		AgentID: testGoalAgentID, Owner: testGoalOwner,
	}
}

func TestGoalEvaluatorAllAndAny(t *testing.T) {
	now := time.Now().UTC()
	record := goalEvalRecord(EvidenceIndexedPage, EvidenceSelector{SourceID: uint64Ptr(7)}, map[string]any{"page_url": "https://fixture/"}, now)
	criteria := []CriterionSpec{
		{ID: "pages", Description: "pages", Predicate: Predicate{
			Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
			Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
		}},
		{ID: "count", Description: "count", Predicate: Predicate{
			Kind: PredicateEvidenceCount, Evidence: EvidenceIndexedPage,
			Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
			Op:    CompareGte, Value: float64(1),
		}},
	}
	all := goalEvalInput(goalEvalSpec(AcceptanceAll, criteria...), []EvidenceRecord{record}, nil)
	result, err := EvaluateGoal(all)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.State != "met" {
		t.Fatalf("all should be met: %+v", result)
	}
	anySpec := goalEvalSpec(AcceptanceAny, criteria...)
	anyResult, err := EvaluateGoal(goalEvalInput(anySpec, []EvidenceRecord{record}, nil))
	if err != nil || anyResult.State != "met" {
		t.Fatalf("any should be met: %+v, %v", anyResult, err)
	}
}

func TestGoalEvaluatorPartialAndUnavailable(t *testing.T) {
	now := time.Now().UTC()
	pass := CriterionSpec{ID: "pass", Description: "pass", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
	}}
	fail := CriterionSpec{ID: "fail", Description: "fail", Predicate: Predicate{
		Kind: PredicateEvidenceCount, Evidence: EvidenceIndexedPage,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
		Op:    CompareGte, Value: float64(2),
	}}
	unavailable := CriterionSpec{ID: "missing", Description: "missing", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceArtifact,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7), Field: "kind", Value: "missing"},
	}}
	record := goalEvalRecord(EvidenceIndexedPage, EvidenceSelector{SourceID: uint64Ptr(7)}, map[string]any{"page_url": "x"}, now)
	result, err := EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, pass, fail), []EvidenceRecord{record}, nil))
	if err != nil || result.State != "not_met" {
		t.Fatalf("partial all must not be met: %+v, %v", result, err)
	}
	result, err = EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, pass, unavailable), []EvidenceRecord{record}, nil))
	if err != nil || result.State != "incomplete" {
		t.Fatalf("unavailable must be incomplete: %+v, %v", result, err)
	}
	result, err = EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAny, fail, unavailable), []EvidenceRecord{record}, nil))
	if err != nil || result.State == "met" {
		t.Fatalf("any without pass must not be met: %+v, %v", result, err)
	}
}

func TestGoalEvaluatorFieldPredicates(t *testing.T) {
	now := time.Now().UTC()
	selector := EvidenceSelector{SourceID: uint64Ptr(7)}
	records := []EvidenceRecord{
		goalEvalRecord(EvidenceIndexedPage, selector, map[string]any{"rank": 2.0, "page_url": "https://a/"}, now),
		goalEvalRecord(EvidenceIndexedPage, selector, map[string]any{"rank": 1.0, "page_url": "https://b/"}, now),
	}
	equalsAny := CriterionSpec{ID: "eq", Description: "eq", Predicate: Predicate{
		Kind: PredicateFieldEquals, Evidence: EvidenceIndexedPage, Where: &selector,
		Path: "page_url", Value: "https://a/", Quantifier: "any",
	}}
	equalsAll := CriterionSpec{ID: "eqall", Description: "eqall", Predicate: Predicate{
		Kind: PredicateFieldEquals, Evidence: EvidenceIndexedPage, Where: &selector,
		Path: "page_url", Value: "https://a/", Quantifier: "all",
	}}
	number := CriterionSpec{ID: "num", Description: "num", Predicate: Predicate{
		Kind: PredicateFieldNumber, Evidence: EvidenceIndexedPage, Where: &selector,
		Path: "rank", Op: CompareGte, Value: 1.0,
	}}
	in := CriterionSpec{ID: "in", Description: "in", Predicate: Predicate{
		Kind: PredicateFieldIn, Evidence: EvidenceIndexedPage, Where: &selector,
		Path: "page_url", Values: []any{"https://a/", "https://c/"},
	}}
	for _, criterion := range []CriterionSpec{equalsAny, number, in} {
		result, err := EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, criterion), records, nil))
		if err != nil || result.State != "met" {
			t.Fatalf("%s should pass: %+v, %v", criterion.ID, result, err)
		}
	}
	result, err := EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, equalsAll), records, nil))
	if err != nil || result.State != "not_met" {
		t.Fatalf("contradictory all should fail: %+v, %v", result, err)
	}
}

func TestGoalEvaluatorResourceAndScope(t *testing.T) {
	now := time.Now().UTC()
	resource := CriterionSpec{ID: "resource", Description: "resource", Predicate: Predicate{
		Kind: PredicateResourceStatus, Resource: "source", ResourceID: 7, Status: "completed",
	}}
	resourceInput := goalEvalInput(goalEvalSpec(AcceptanceAll, resource), nil, map[string]ResourceObservation{
		resourceObservationKey("source", 7): goalEvalResource("source", 7, "completed", now, now),
	})
	result, err := EvaluateGoal(resourceInput)
	if err != nil || result.State != "met" {
		t.Fatalf("resource status should pass: %+v, %v", result, err)
	}
	resourceInput.Resources[resourceObservationKey("source", 7)] = goalEvalResource("source", 7, "error", now, now)
	result, err = EvaluateGoal(resourceInput)
	if err != nil || result.State != "not_met" {
		t.Fatalf("resource mismatch must not pass: %+v, %v", result, err)
	}
	scope := CriterionSpec{ID: "scope", Description: "scope", Predicate: Predicate{
		Kind: PredicateEvidenceScope, Evidence: EvidenceIndexedPage,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
	}}
	record := goalEvalRecord(EvidenceIndexedPage, EvidenceSelector{SourceID: uint64Ptr(7)}, map[string]any{"page_url": "x"}, now)
	result, err = EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, scope), []EvidenceRecord{record}, nil))
	if err != nil || result.State != "met" {
		t.Fatalf("scope should pass: %+v, %v", result, err)
	}
	record.SourceIDs = []uint64{8}
	record = sealEvalRecord(record)
	result, err = EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, scope), []EvidenceRecord{record}, nil))
	if err != nil || result.State == "met" {
		t.Fatalf("out-of-scope evidence must not pass: %+v, %v", result, err)
	}
}

func TestGoalEvaluatorStaleAndModelClaim(t *testing.T) {
	now := time.Now().UTC()
	selector := EvidenceSelector{SourceID: uint64Ptr(7)}
	record := goalEvalRecord(EvidenceIndexedPage, selector, map[string]any{"page_url": "x"}, now.Add(-time.Hour))
	criterion := CriterionSpec{ID: "stale", Description: "stale", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &selector,
	}}
	result, err := EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, criterion), []EvidenceRecord{record}, nil))
	if err != nil || result.State == "met" {
		t.Fatalf("stale evidence must not pass: %+v, %v", result, err)
	}
}

func uint64Ptr(value uint64) *uint64 { return &value }

func TestGoalEvaluationConcurrentIsolation(t *testing.T) {
	now := time.Now().UTC()
	selector7 := EvidenceSelector{SourceID: uint64Ptr(7)}
	selector8 := EvidenceSelector{SourceID: uint64Ptr(8)}
	spec7 := goalEvalSpec(AcceptanceAll, CriterionSpec{ID: "s7", Description: "s7", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &selector7,
	}})
	spec7.AllowedSources = []uint64{7}
	spec8 := goalEvalSpec(AcceptanceAll, CriterionSpec{ID: "s8", Description: "s8", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &selector8,
	}})
	spec8.AllowedSources = []uint64{8}
	record7 := goalEvalRecord(EvidenceIndexedPage, selector7, map[string]any{"page_url": "seven"}, now)
	record7.SourceIDs = []uint64{7}
	record7 = sealEvalRecord(record7)
	record8 := goalEvalRecord(EvidenceIndexedPage, selector8, map[string]any{"page_url": "eight"}, now)
	record8.SourceIDs = []uint64{8}
	record8 = sealEvalRecord(record8)
	type outcome struct {
		state string
		err   error
	}
	results := make(chan outcome, 2)
	go func() {
		result, err := EvaluateGoal(goalEvalInput(spec7, []EvidenceRecord{record7}, nil))
		results <- outcome{result.State, err}
	}()
	go func() {
		result, err := EvaluateGoal(goalEvalInput(spec8, []EvidenceRecord{record8}, nil))
		results <- outcome{result.State, err}
	}()
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil || got.state != "met" {
			t.Fatalf("concurrent evaluation leaked: %+v", got)
		}
	}
}
