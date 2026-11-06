package agent

import (
	"context"
	"testing"
	"time"
)

type fakeGoalEvidenceProvider struct {
	kind         EvidenceKind
	observations []EvidenceObservation
	err          error
}

func (f *fakeGoalEvidenceProvider) Kind() EvidenceKind { return f.kind }

func (f *fakeGoalEvidenceProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]EvidenceObservation(nil), f.observations...), nil
}

func newTestGoalLedger() *goalEvidenceLedger {
	return NewGoalEvidenceLedger(
		"run-1", "nonce-1", "trace-1", "goal-1", "spec-hash", "agent-1", "owner",
		[]uint64{7},
		EvidencePolicy{Allow: []EvidenceKind{EvidenceSourceStatus, EvidenceIndexedPage, EvidenceToolObservation, EvidenceArtifact}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 1024},
		GoalBudgets{MaxEvidenceRecords: 4, MaxEvidenceBytes: 4096},
		time.Now().Add(time.Hour),
	)
}

func TestGoalEvidenceLedgerRecordAndDedupe(t *testing.T) {
	ledger := newTestGoalLedger()
	selector := EvidenceSelector{Tool: "get_source_status", ArgumentsSHA256: argumentsHash(map[string]any{"source_id": 7})}
	obs := EvidenceObservation{
		Kind: EvidenceToolObservation, Producer: "tool:get_source_status",
		SourceIDs: []uint64{7}, Operation: "read",
		Payload:        map[string]any{"status": "completed", "source_id": 7},
		SelectorSHA256: selectorHash(selector),
	}
	id, err := ledger.Record(context.Background(), obs)
	if err != nil || id == "" {
		t.Fatalf("record: %s, %v", id, err)
	}
	again, err := ledger.Record(context.Background(), obs)
	if err != nil || again != id {
		t.Fatalf("dedupe failed: %s vs %s, %v", again, id, err)
	}
	if ledger.Count() != 1 {
		t.Fatalf("dedupe count = %d", ledger.Count())
	}
	records := ledger.Snapshot()
	if records[0].RunNonceHash == "" || records[0].GoalSpecHash != "spec-hash" {
		t.Fatalf("provenance missing: %+v", records[0])
	}
	if records[0].TrustClass != TrustToolObservation {
		t.Fatalf("trust class wrong: %s", records[0].TrustClass)
	}
}

func TestGoalEvidenceLedgerScopeAndCaps(t *testing.T) {
	ledger := newTestGoalLedger()
	_, err := ledger.Record(context.Background(), EvidenceObservation{
		Kind: EvidenceSourceStatus, Producer: "db", SourceIDs: []uint64{8},
		Payload: map[string]any{"status": "completed"},
	})
	if err == nil {
		t.Fatalf("out-of-scope evidence accepted")
	}
	ledger = newTestGoalLedger()
	for i := 0; i < 4; i++ {
		_, err := ledger.Record(context.Background(), EvidenceObservation{
			Kind: EvidenceIndexedPage, Producer: "db", SourceIDs: []uint64{7},
			Payload: map[string]any{"index": i}, SelectorSHA256: selectorHash(EvidenceSelector{SourceID: uint64Ptr(7)}),
		})
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	_, err = ledger.Record(context.Background(), EvidenceObservation{
		Kind: EvidenceIndexedPage, Producer: "db", SourceIDs: []uint64{7},
		Payload: map[string]any{"index": 4}, SelectorSHA256: selectorHash(EvidenceSelector{SourceID: uint64Ptr(7)}),
	})
	if err == nil {
		t.Fatalf("evidence record cap not enforced")
	}
}

func TestGoalEvidenceLedgerRedactsAndHashes(t *testing.T) {
	ledger := newTestGoalLedger()
	id, err := ledger.Record(context.Background(), EvidenceObservation{
		Kind: EvidenceSourceStatus, Producer: "db", SourceIDs: []uint64{7},
		Payload: map[string]any{"status": "completed", "api_key": "secret-value"},
	})
	if err != nil || id == "" {
		t.Fatalf("record: %v", err)
	}
	record := ledger.Snapshot()[0]
	if record.Payload["api_key"] != "[redacted]" {
		t.Fatalf("secret not redacted: %+v", record.Payload)
	}
	if record.PayloadSHA256 == "" || record.RecordSHA256 == "" {
		t.Fatalf("hashes missing")
	}
	if record.ExpiresAt.IsZero() || record.ExpiresAt.After(time.Now().Add(16*time.Minute)) {
		t.Fatalf("expiry wrong: %s", record.ExpiresAt)
	}
}

func TestRefreshGoalEvidenceFromProvider(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal", Objective: "obj", SpecHash: "spec", AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceIndexedPage}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 1024},
		Budgets:  GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "pages", Description: "pages",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
				Where: &EvidenceSelector{SourceID: uint64Ptr(7)}},
		}}},
	}
	provider := &fakeGoalEvidenceProvider{kind: EvidenceIndexedPage, observations: []EvidenceObservation{{
		Kind: EvidenceIndexedPage, Producer: "fake", SourceIDs: []uint64{7},
		Payload: map[string]any{"page_url": "https://fixture/"},
	}}}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	ledger := NewGoalEvidenceLedger("run", "nonce", "trace", "goal", "spec", "agent", "owner", []uint64{7}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour))
	if err := refreshGoalEvidence(context.Background(), spec, registry, ledger, map[string]EvidenceQueryResult{}, time.Now().UTC()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ledger.Count() != 1 {
		t.Fatalf("expected one record, got %d", ledger.Count())
	}
	result, err := EvaluateGoal(GoalEvalInput{
		Spec: spec, Records: ledger.Snapshot(), Now: time.Now().UTC(),
		Queries:  map[string]EvidenceQueryResult{},
		LedgerID: ledger.ID(), RunID: "run", GoalID: spec.GoalID,
		GoalSpecHash: spec.SpecHash, RunNonceHash: ledger.RunNonceHash(),
		AgentID: "agent", Owner: "owner",
	})
	if err != nil || result.State != "met" {
		t.Fatalf("provider evidence should satisfy: %+v, %v", result, err)
	}
}

func TestRefreshGoalEvidenceMissingProviderFailsClosed(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal", AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceIndexedPage}, DefaultMaxAge: time.Minute, MaxPayloadBytes: 1024},
		Budgets:  GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "pages", Description: "pages",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
				Where: &EvidenceSelector{SourceID: uint64Ptr(7)}},
		}}},
	}
	ledger := NewGoalEvidenceLedger("run", "nonce", "trace", "goal", "spec", "agent", "owner", []uint64{7}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour))
	if err := refreshGoalEvidence(context.Background(), spec, NewGoalEvidenceRegistry(), ledger, map[string]EvidenceQueryResult{}, time.Now().UTC()); err == nil {
		t.Fatalf("missing provider accepted")
	}
}
