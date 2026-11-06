package agent

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type countingGoalProvider struct {
	kind         EvidenceKind
	observations []EvidenceObservation
	err          error
	calls        int32
}

func (p *countingGoalProvider) Kind() EvidenceKind { return p.kind }

func (p *countingGoalProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	atomic.AddInt32(&p.calls, 1)
	if p.err != nil {
		return nil, p.err
	}
	return append([]EvidenceObservation(nil), p.observations...), nil
}

func hardeningSpec(evidence EvidenceKind, selector EvidenceSelector, counts ...CriterionSpec) *AgentGoalSpec {
	spec := goalEvalSpec(AcceptanceAll, counts...)
	spec.Evidence.Allow = []EvidenceKind{evidence}
	if spec.GoalID == "" {
		spec.GoalID = "goal-test"
	}
	return spec
}

func TestZeroEvidenceRequiresCompletedQuery(t *testing.T) {
	criterion := CriterionSpec{ID: "zero", Description: "zero", Predicate: Predicate{
		Kind: PredicateEvidenceCount, Evidence: EvidenceIndexedPage,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
		Op:    CompareEq, Value: float64(0),
	}}
	spec := hardeningSpec(EvidenceIndexedPage, EvidenceSelector{SourceID: uint64Ptr(7)}, criterion)
	selector := EvidenceSelector{SourceID: uint64Ptr(7)}
	key := evidenceQueryKey(EvidenceIndexedPage, selector)

	cases := []struct {
		name  string
		query *EvidenceQueryResult
		want  string
	}{
		{"no query completion", nil, "incomplete"},
		{"completed empty", &EvidenceQueryResult{Kind: EvidenceIndexedPage, SelectorSHA256: selectorHash(selector), State: EvidenceQueryCompleted, Count: 0, Limit: 16}, "met"},
		{"provider unavailable", &EvidenceQueryResult{Kind: EvidenceIndexedPage, SelectorSHA256: selectorHash(selector), State: EvidenceQueryUnavailable, Reason: "evidence_query_unavailable"}, "incomplete"},
		{"query failed", &EvidenceQueryResult{Kind: EvidenceIndexedPage, SelectorSHA256: selectorHash(selector), State: EvidenceQueryFailed, Reason: "evidence_query_failed"}, "error"},
		{"query truncated", &EvidenceQueryResult{Kind: EvidenceIndexedPage, SelectorSHA256: selectorHash(selector), State: EvidenceQueryTruncated, Count: 16, Limit: 16, Reason: "evidence_query_truncated"}, "error"},
	}
	for _, tc := range cases {
		input := goalEvalInput(spec, nil, nil)
		if tc.query != nil {
			input.Queries[key] = *tc.query
		}
		result, err := EvaluateGoal(input)
		if err != nil {
			t.Fatalf("%s: evaluate: %v", tc.name, err)
		}
		if result.State != tc.want {
			t.Fatalf("%s: got state %q, want %q (%+v)", tc.name, result.State, tc.want, result)
		}
		if result.State == "met" && len(result.Criteria) != 1 || result.State == "met" && result.Criteria[0].Result != string(CriterionPass) {
			t.Fatalf("%s: unexpected passing criterion: %+v", tc.name, result.Criteria)
		}
	}
}

func TestActionZeroEvidenceUnavailableDatabaseDoesNotSucceed(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	criterion := map[string]any{
		"id": "zero", "description": "zero",
		"predicate": map[string]any{
			"kind": "evidence_count", "evidence": "source_status",
			"where": map[string]any{"source_id": float64(7)},
			"op":    "eq", "value": float64(0),
		},
	}
	spec := goalActionSpec(t, []any{criterion}, []any{"source_status"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, nil))
	if err == nil {
		t.Fatalf("unavailable DB must not prove zero matching records")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] == string(GoalCriteriaMet) {
		t.Fatalf("false success from unavailable DB: %v", output)
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("unexpected provider calls: %d", provider.providerCalls())
	}
}

func TestActionZeroEvidenceCompletedEmptyQuerySucceeds(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("should not run")}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	evidenceKind := EvidenceIndexedPage
	fake := &fakeGoalEvidenceProvider{kind: evidenceKind}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	criterion := map[string]any{
		"id": "zero", "description": "zero",
		"predicate": map[string]any{
			"kind": "evidence_count", "evidence": "indexed_page",
			"where": map[string]any{"source_id": float64(7)},
			"op":    "eq", "value": float64(0),
		},
	}
	spec := goalActionSpec(t, []any{criterion}, []any{"indexed_page"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, registry))
	if err != nil {
		t.Fatalf("completed empty query should prove zero: %v (%v)", err, result[StrMessage])
	}
	if result[StrResponse].(map[string]any)["status"] != string(GoalCriteriaMet) {
		t.Fatalf("unexpected result: %v", result[StrResponse])
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("zero-result proof called the model")
	}
}

func TestProviderObservationSelectorAndSourceBinding(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal-test", SpecHash: "spec-test", AllowedSources: []uint64{7, 8},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceIndexedPage}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets:  GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "pages", Description: "pages",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
				Where: &EvidenceSelector{SourceID: uint64Ptr(7)}},
		}}},
	}
	cases := []struct {
		name string
		obs  EvidenceObservation
	}{
		{"wrong source", EvidenceObservation{Kind: EvidenceIndexedPage, SourceIDs: []uint64{8}, Payload: map[string]any{"page_url": "x"}}},
		{"missing source", EvidenceObservation{Kind: EvidenceIndexedPage, Payload: map[string]any{"page_url": "x"}}},
		{"wrong kind", EvidenceObservation{Kind: EvidenceArtifact, SourceIDs: []uint64{7}, Payload: map[string]any{"page_url": "x"}}},
		{"mixed allowed sources", EvidenceObservation{Kind: EvidenceIndexedPage, SourceIDs: []uint64{7, 8}, Payload: map[string]any{"page_url": "x"}}},
	}
	for _, tc := range cases {
		provider := &countingGoalProvider{kind: EvidenceIndexedPage, observations: []EvidenceObservation{tc.obs}}
		registry := NewGoalEvidenceRegistry()
		if err := registry.Register(provider); err != nil {
			t.Fatalf("register: %v", err)
		}
		ledger := NewGoalEvidenceLedger("run", "nonce", "trace", "goal", "spec", "agent", "owner", []uint64{7, 8}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour))
		queries := map[string]EvidenceQueryResult{}
		err := refreshGoalEvidence(context.Background(), spec, registry, ledger, queries, time.Now().UTC())
		if err == nil || !errors.Is(err, ErrEvidenceInvalid) {
			t.Fatalf("%s: observation accepted: %v", tc.name, err)
		}
		if ledger.Count() != 0 {
			t.Fatalf("%s: rejected observation was admitted", tc.name)
		}
	}
}

func TestProviderSelectorHashIsRecalculatedNotTrusted(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal-test", SpecHash: "spec-test", AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceIndexedPage}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets:  GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "pages", Description: "pages",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
				Where: &EvidenceSelector{SourceID: uint64Ptr(7)}},
		}}},
	}
	provider := &countingGoalProvider{kind: EvidenceIndexedPage, observations: []EvidenceObservation{{
		Kind: EvidenceIndexedPage, SourceIDs: []uint64{7},
		SelectorSHA256: strings.Repeat("f", 64),
		Payload:        map[string]any{"page_url": "https://fixture/"},
	}}}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	ledger := NewGoalEvidenceLedger("run", "nonce", "trace", "goal", "spec", "agent", "owner", []uint64{7}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour))
	queries := map[string]EvidenceQueryResult{}
	if err := refreshGoalEvidence(context.Background(), spec, registry, ledger, queries, time.Now().UTC()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	records := ledger.Snapshot()
	if len(records) != 1 {
		t.Fatalf("expected one record: %+v", records)
	}
	if records[0].SelectorSHA256 == strings.Repeat("f", 64) {
		t.Fatalf("provider-supplied selector hash was trusted")
	}
	if records[0].SelectorSHA256 != selectorHash(*spec.Acceptance.Criteria[0].Predicate.Where) {
		t.Fatalf("record not stamped with requested selector")
	}
}

func TestEvaluatorRejectsForgedEvidence(t *testing.T) {
	criterion := CriterionSpec{ID: "pages", Description: "pages", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7)},
	}}
	spec := goalEvalSpec(AcceptanceAll, criterion)
	base := goalEvalRecord(EvidenceIndexedPage, EvidenceSelector{SourceID: uint64Ptr(7)}, map[string]any{"page_url": "x"}, time.Now().UTC())
	cases := []struct {
		name   string
		mutate func(*EvidenceRecord)
	}{
		{"model assertion", func(r *EvidenceRecord) { r.TrustClass = TrustModelAssertion; *r = sealEvalRecord(*r) }},
		{"retrieved content", func(r *EvidenceRecord) { r.TrustClass = TrustRetrievedContent; *r = sealEvalRecord(*r) }},
		{"wrong run", func(r *EvidenceRecord) { r.RunID = "other-run"; *r = sealEvalRecord(*r) }},
		{"wrong goal", func(r *EvidenceRecord) { r.GoalID = "other-goal"; *r = sealEvalRecord(*r) }},
		{"wrong spec", func(r *EvidenceRecord) { r.GoalSpecHash = "other-spec"; *r = sealEvalRecord(*r) }},
		{"wrong nonce", func(r *EvidenceRecord) { r.RunNonceHash = "other-nonce"; *r = sealEvalRecord(*r) }},
		{"wrong ledger", func(r *EvidenceRecord) { r.LedgerID = "other-ledger"; *r = sealEvalRecord(*r) }},
		{"wrong agent", func(r *EvidenceRecord) { r.AgentID = "other-agent"; *r = sealEvalRecord(*r) }},
		{"wrong owner", func(r *EvidenceRecord) { r.Owner = "other-owner"; *r = sealEvalRecord(*r) }},
		{"payload tampered", func(r *EvidenceRecord) { r.Payload["page_url"] = "tampered" }},
		{"record hash tampered", func(r *EvidenceRecord) { r.RecordSHA256 = strings.Repeat("0", 64) }},
		{"empty evidence id", func(r *EvidenceRecord) { r.EvidenceID = ""; *r = sealEvalRecord(*r) }},
		{"wrong source", func(r *EvidenceRecord) { r.SourceIDs = []uint64{8}; *r = sealEvalRecord(*r) }},
		{"missing source", func(r *EvidenceRecord) { r.SourceIDs = nil; *r = sealEvalRecord(*r) }},
		{"multiple sources", func(r *EvidenceRecord) { r.SourceIDs = []uint64{7, 8}; *r = sealEvalRecord(*r) }},
	}
	for _, tc := range cases {
		record := base
		record.Payload = cloneMap(base.Payload)
		tc.mutate(&record)
		result, err := EvaluateGoal(goalEvalInput(spec, []EvidenceRecord{record}, nil))
		if err != nil {
			t.Fatalf("%s: evaluate: %v", tc.name, err)
		}
		if result.State == "met" || result.Criteria[0].Result == string(CriterionPass) {
			t.Fatalf("%s: forged/tampered evidence satisfied the criterion: %+v", tc.name, result)
		}
	}
}

func TestEvaluatorRejectsWrongSourceWhenBothAllowed(t *testing.T) {
	selector := EvidenceSelector{SourceID: uint64Ptr(7)}
	criterion := CriterionSpec{ID: "pages", Description: "pages", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &selector,
	}}
	spec := goalEvalSpec(AcceptanceAll, criterion)
	spec.AllowedSources = []uint64{7, 8}
	record := goalEvalRecord(EvidenceIndexedPage, selector, map[string]any{"page_url": "eight"}, time.Now().UTC())
	record.SourceIDs = []uint64{8}
	record = sealEvalRecord(record)
	result, err := EvaluateGoal(goalEvalInput(spec, []EvidenceRecord{record}, nil))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.State == "met" || result.Criteria[0].Result == string(CriterionPass) {
		t.Fatalf("source 8 satisfied selector source 7 while both were allowed: %+v", result)
	}
}

func TestAnyAuthorizationPrecedence(t *testing.T) {
	passSelector := EvidenceSelector{SourceID: uint64Ptr(7)}
	deniedSelector := EvidenceSelector{SourceID: uint64Ptr(8)}
	passRecord := goalEvalRecord(EvidenceIndexedPage, passSelector, map[string]any{"page_url": "seven"}, time.Now().UTC())
	deniedRecord := goalEvalRecord(EvidenceIndexedPage, deniedSelector, map[string]any{"page_url": "eight"}, time.Now().UTC())
	deniedRecord.SourceIDs = []uint64{8}
	deniedRecord = sealEvalRecord(deniedRecord)
	pass := CriterionSpec{ID: "pass", Description: "pass", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &passSelector,
	}}
	denied := CriterionSpec{ID: "denied", Description: "denied", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &deniedSelector,
	}}
	spec := goalEvalSpec(AcceptanceAny, pass, denied)
	spec.AllowedSources = []uint64{7}
	result, err := EvaluateGoal(goalEvalInput(spec, []EvidenceRecord{passRecord, deniedRecord}, nil))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.State == "met" {
		t.Fatalf("authorization denial was masked by a passing any criterion: %+v", result)
	}
	// Nested `any` must also fail closed.
	nested := CriterionSpec{ID: "nested", Description: "nested", Predicate: Predicate{
		Kind: PredicateAny, Predicates: []Predicate{pass.Predicate, denied.Predicate},
	}}
	nestedResult, err := EvaluateGoal(goalEvalInput(goalEvalSpec(AcceptanceAll, nested), []EvidenceRecord{passRecord, deniedRecord}, nil))
	if err != nil {
		t.Fatalf("nested evaluate: %v", err)
	}
	if nestedResult.State == "met" || nestedResult.Criteria[0].Result == string(CriterionPass) {
		t.Fatalf("nested any masked denial: %+v", nestedResult)
	}
	// Unavailable/fail siblings must not block a valid `any` pass.
	unavailable := CriterionSpec{ID: "missing", Description: "missing", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceArtifact,
		Where: &EvidenceSelector{SourceID: uint64Ptr(7), Field: "kind", Value: "none"},
	}}
	okSpec := goalEvalSpec(AcceptanceAny, pass, unavailable)
	okSpec.AllowedSources = []uint64{7}
	okResult, err := EvaluateGoal(goalEvalInput(okSpec, []EvidenceRecord{passRecord}, nil))
	if err != nil || okResult.State != "met" {
		t.Fatalf("valid any pass was blocked by unavailable sibling: %+v, %v", okResult, err)
	}
}

type panicOnReadDBHandler struct{ fakeDBHandler }

func (h *panicOnReadDBHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	panic("unexpected database read")
}
func (h *panicOnReadDBHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	panic("unexpected database read")
}
func (h *panicOnReadDBHandler) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	panic("unexpected database read")
}
func (h *panicOnReadDBHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	panic("unexpected database read")
}
func (h *panicOnReadDBHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	panic("unexpected database write")
}
func (h *panicOnReadDBHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	panic("unexpected database write")
}

func TestInformationSeedResourceStatusDisabled(t *testing.T) {
	params := goalTestParams()
	predicate := params["acceptance"].(map[string]any)["criteria"].([]any)[0].(map[string]any)["predicate"].(map[string]any)
	predicate["resource"] = "information_seed"
	if _, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0); err == nil ||
		!strings.Contains(err.Error(), "information_seed_scope_unsupported") {
		t.Fatalf("information_seed resource accepted: %v", err)
	}
	spec := &AgentGoalSpec{
		GoalID: "goal", SpecHash: "spec", AllowedSources: []uint64{7},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "seed", Description: "seed",
			Predicate: Predicate{Kind: PredicateResourceStatus, Resource: "information_seed", ResourceID: 7, Status: "completed"},
		}}},
	}
	var handler cdb.Handler = &panicOnReadDBHandler{}
	resources := readGoalResources(context.Background(), handler, spec, "run")
	obs, ok := resources[resourceObservationKey("information_seed", 7)]
	if !ok || obs.Err != "information_seed_scope_unsupported" {
		t.Fatalf("info seed resource read was not disabled: %+v", resources)
	}
}

func TestGoalProviderCapabilityAndContractGates(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() { resetLLMProvidersForTest() })

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	params := goalTestParams()
	params["provider"] = "ollama"
	params["url"] = server.URL
	params["model"] = "incompatible-model"
	params["acceptance"] = map[string]any{"mode": "all", "criteria": []any{
		pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)}),
	}}
	params["evidence"] = map[string]any{"allow": []any{"tool_observation"}}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	RegisterLLMProvider(NewOllamaProvider([]string{}))
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, nil))
	if err == nil {
		t.Fatalf("incompatible provider must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalDenied) || output["reason_code"] != "provider_tool_calls_unsupported" {
		t.Fatalf("unexpected capability result: %v", output)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("incompatible provider received an HTTP request")
	}

	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
	RegisterLLMProvider(provider)
	forbidden := goalTestIdentity()
	forbidden.Contract = &AgentContract{ForbiddenActions: []string{"provider:goal-scripted"}}
	contractSpec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	result, err = (&AgentGoalRunAction{}).Execute(goalActionParams(contractSpec, forbidden, nil, nil))
	if err == nil {
		t.Fatalf("forbidden provider must not succeed")
	}
	output = result[StrResponse].(map[string]any)
	if output["status"] != string(GoalDenied) || output["reason_code"] != "ai_policy_denied" {
		t.Fatalf("unexpected contract result: %v", output)
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("forbidden provider received a model request")
	}
}

func TestGoalFreshnessUsesUnderlyingUpdateTime(t *testing.T) {
	now := time.Now().UTC()
	selector := EvidenceSelector{SourceID: uint64Ptr(7)}
	criterion := CriterionSpec{ID: "pages", Description: "pages", Predicate: Predicate{
		Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage, Where: &selector,
	}}
	spec := goalEvalSpec(AcceptanceAll, criterion)
	record := goalEvalRecord(EvidenceIndexedPage, selector, map[string]any{"page_url": "x"}, now)
	record.DataUpdatedAt = now.Add(-time.Hour)
	record = sealEvalRecord(record)
	result, err := EvaluateGoal(goalEvalInput(spec, []EvidenceRecord{record}, nil))
	if err != nil || result.State == "met" {
		t.Fatalf("old underlying data satisfied fresh evidence: %+v, %v", result, err)
	}

	resource := CriterionSpec{ID: "source", Description: "source", Predicate: Predicate{
		Kind: PredicateResourceStatus, Resource: "source", ResourceID: 7, Status: "completed",
	}}
	resourceSpec := goalEvalSpec(AcceptanceAll, resource)
	oldResource := goalEvalResource("source", 7, "completed", now, now.Add(-time.Hour))
	result, err = EvaluateGoal(goalEvalInput(resourceSpec, nil, map[string]ResourceObservation{
		resourceObservationKey("source", 7): oldResource,
	}))
	if err != nil || result.State == "met" {
		t.Fatalf("old source status satisfied fresh resource criterion: %+v, %v", result, err)
	}
	missingTime := goalEvalResource("source", 7, "completed", now, time.Time{})
	result, err = EvaluateGoal(goalEvalInput(resourceSpec, nil, map[string]ResourceObservation{
		resourceObservationKey("source", 7): missingTime,
	}))
	if err != nil || result.State == "met" {
		t.Fatalf("missing underlying time satisfied freshness: %+v, %v", result, err)
	}
}

func TestRefreshGoalEvidenceCancellationStopsReads(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal", SpecHash: "spec", AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceIndexedPage}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets:  GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "pages", Description: "pages",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceIndexedPage,
				Where: &EvidenceSelector{SourceID: uint64Ptr(7)}},
		}}},
	}
	provider := &countingGoalProvider{kind: EvidenceIndexedPage}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	ledger := NewGoalEvidenceLedger("run", "nonce", "trace", "goal", "spec", "agent", "owner", []uint64{7}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := refreshGoalEvidence(ctx, spec, registry, ledger, map[string]EvidenceQueryResult{}, time.Now().UTC()); err == nil {
		t.Fatalf("cancelled refresh returned success")
	}
	if atomic.LoadInt32(&provider.calls) != 0 {
		t.Fatalf("cancelled refresh performed a provider read")
	}
	resources := readGoalResources(ctx, nil, spec, "run")
	if len(resources) != 0 {
		// A resource criterion was not present in this spec; no reads expected.
		t.Fatalf("unexpected resources: %+v", resources)
	}
}
