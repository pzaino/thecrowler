package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func sourceStatusMockRows(status string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"source_id", "source_uid", "url", "status", "priority", "sub_priority",
		"engine", "created_at", "last_updated_at", "last_crawled_at",
		"last_error", "last_error_at", "restricted", "disabled", "flags", "config",
	}).AddRow(
		uint64(7), "uid-7", "https://example.com", status, "high", 0,
		"engine", "2026-11-06 12:00:00", "2026-11-06 12:01:00", "2026-11-06 12:01:00",
		"", "", 0, false, 0, []byte(`{}`),
	)
}

func expectEmptyMailboxStatus(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT mailbox.source_id").WillReturnRows(sqlmock.NewRows([]string{
		"source_id", "mailbox_count", "active_mailbox_count", "last_reconciled_at",
		"listener_healthy_at", "has_token_cursor", "has_history_cursor", "has_uid_cursor",
		"checkpointed_mailboxes", "processed_count", "failed_count",
	}))
}

func TestSourceStatusProviderSourceLookupFailures(t *testing.T) {
	sourceID := uint64(7)
	cases := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		ctx     func() context.Context
		wantErr error
	}{
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT source_id").WithArgs(sourceID).
					WillReturnError(errors.New("connection refused"))
			},
			ctx:     context.Background,
			wantErr: ErrEvidenceUnavailable,
		},
		{
			name: "missing source",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT source_id").WithArgs(sourceID).
					WillReturnError(sql.ErrNoRows)
			},
			ctx:     context.Background,
			wantErr: ErrEvidenceInvalid,
		},
		{
			name: "cancelled lookup",
			setup: func(sqlmock.Sqlmock) {
				// The provider must stop before issuing the lookup.
			},
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr: ErrEvidenceUnavailable,
		},
		{
			name: "expired lookup deadline",
			setup: func(sqlmock.Sqlmock) {
				// The provider must stop before issuing the lookup.
			},
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			wantErr: ErrEvidenceUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := newSQLMockDB(t)
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			tc.setup(mock)
			var handler cdb.Handler = &sqlmockHandler{db: db}
			provider := &sourceStatusProvider{db: handler}
			observations, err := provider.Read(tc.ctx(), EvidenceRequest{
				Selector: EvidenceSelector{SourceID: &sourceID}, Now: time.Now().UTC(),
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "connection refused") {
				t.Fatalf("database detail leaked into evidence error: %v", err)
			}
			if len(observations) != 0 {
				t.Fatalf("failed lookup returned observations: %+v", observations)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

func TestSourceStatusProviderZeroAndMatchingStatus(t *testing.T) {
	sourceID := uint64(7)
	t.Run("source exists but status does not match", func(t *testing.T) {
		db, mock, err := newSQLMockDB(t)
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
		mock.ExpectQuery("WHERE source_uid").WillReturnRows(sourceStatusMockRows("new"))
		expectEmptyMailboxStatus(mock)
		var handler cdb.Handler = &sqlmockHandler{db: db}
		provider := &sourceStatusProvider{db: handler}
		observations, err := provider.Read(context.Background(), EvidenceRequest{
			Selector: EvidenceSelector{SourceID: &sourceID, Status: "completed"},
			Now:      time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(observations) != 0 {
			t.Fatalf("mismatched status returned observations: %+v", observations)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("source exists and status matches", func(t *testing.T) {
		db, mock, err := newSQLMockDB(t)
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
		mock.ExpectQuery("WHERE source_uid").WillReturnRows(sourceStatusMockRows("completed"))
		expectEmptyMailboxStatus(mock)
		var handler cdb.Handler = &sqlmockHandler{db: db}
		provider := &sourceStatusProvider{db: handler}
		observations, err := provider.Read(context.Background(), EvidenceRequest{
			Selector: EvidenceSelector{SourceID: &sourceID, Status: "completed"},
			Now:      time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(observations) != 1 || observations[0].Payload["status"] != "completed" {
			t.Fatalf("matching status missing: %+v", observations)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func zeroStatusCountCriterion() map[string]any {
	return map[string]any{
		"id": "zero", "description": "zero matching status records",
		"predicate": map[string]any{
			"kind": "evidence_count", "evidence": "source_status",
			"where": map[string]any{"source_id": float64(7), "status": "completed"},
			"op":    "eq", "value": float64(0),
		},
	}
}

func TestActionZeroEvidenceSourceLookupFailureDoesNotSucceed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT source_id").WillReturnError(errors.New("connection refused"))
			},
		},
		{
			name: "missing source",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetLLMProvidersForTest()
			AgentsEngine = NewJobEngine()
			t.Cleanup(func() {
				resetLLMProvidersForTest()
				AgentsEngine = nil
			})
			model := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
			RegisterLLMProvider(model)

			db, mock, err := newSQLMockDB(t)
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			tc.setup(mock)
			var handler cdb.Handler = &sqlmockHandler{db: db}
			spec := goalActionSpec(t, []any{zeroStatusCountCriterion()}, []any{"source_status"}, nil)
			result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
			if err == nil {
				t.Fatal("source lookup failure must not succeed")
			}
			output, ok := result[StrResponse].(map[string]any)
			if !ok {
				t.Fatalf("missing goal result: %+v", result)
			}
			if output["status"] == string(GoalCriteriaMet) {
				t.Fatalf("source lookup failure produced criteria_met: %v", output)
			}
			if strings.Contains(fmt.Sprint(output), "connection refused") {
				t.Fatalf("database detail leaked into goal result: %v", output)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

func TestActionZeroEvidenceDefaultProviderCompletedEmptyQuerySucceeds(t *testing.T) {
	resetLLMProvidersForTest()
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		AgentsEngine = nil
	})
	model := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("should not run")}}
	RegisterLLMProvider(model)

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	mock.ExpectQuery("WHERE source_uid").WillReturnRows(sourceStatusMockRows("new"))
	expectEmptyMailboxStatus(mock)
	var handler cdb.Handler = &sqlmockHandler{db: db}
	spec := goalActionSpec(t, []any{zeroStatusCountCriterion()}, []any{"source_status"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err != nil {
		t.Fatalf("completed empty query should prove zero: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("unexpected result: %v", output)
	}
	if model.providerCalls() != 0 {
		t.Fatalf("verified zero-result query called the model %d times", model.providerCalls())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestReadGoalResourcesSourceLookupClassification(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal", SpecHash: "spec", AllowedSources: []uint64{7},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "status", Description: "status",
			Predicate: Predicate{
				Kind: PredicateResourceStatus, Resource: "source", ResourceID: 7, Status: "completed",
			},
		}}},
	}
	key := resourceObservationKey("source", 7)

	t.Run("database error is not source_not_found", func(t *testing.T) {
		db, mock, err := newSQLMockDB(t)
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery("SELECT source_id").WillReturnError(errors.New("connection refused"))
		var handler cdb.Handler = &sqlmockHandler{db: db}
		resources := readGoalResources(context.Background(), handler, spec, "run")
		if resources[key].Err != "source_lookup_unavailable" {
			t.Fatalf("unexpected classification: %+v", resources[key])
		}
	})

	t.Run("missing source", func(t *testing.T) {
		db, mock, err := newSQLMockDB(t)
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
		var handler cdb.Handler = &sqlmockHandler{db: db}
		resources := readGoalResources(context.Background(), handler, spec, "run")
		if resources[key].Err != "source_not_found" {
			t.Fatalf("unexpected classification: %+v", resources[key])
		}
	})

	t.Run("valid source status", func(t *testing.T) {
		db, mock, err := newSQLMockDB(t)
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
		mock.ExpectQuery("WHERE source_uid").WillReturnRows(sourceStatusMockRows("completed"))
		expectEmptyMailboxStatus(mock)
		var handler cdb.Handler = &sqlmockHandler{db: db}
		resources := readGoalResources(context.Background(), handler, spec, "run")
		if resources[key].Err != "" || resources[key].Status != "completed" {
			t.Fatalf("unexpected resource observation: %+v", resources[key])
		}
	})
}

func TestEquivalentProvidersSourceLookupClassification(t *testing.T) {
	sourceID := uint64(7)
	cases := []struct {
		name     string
		provider func(cdb.Handler) EvidenceProvider
		request  EvidenceRequest
	}{
		{
			name:     "source metadata",
			provider: func(db cdb.Handler) EvidenceProvider { return &sourceMetadataProvider{db: db} },
			request:  EvidenceRequest{Selector: EvidenceSelector{SourceID: &sourceID}},
		},
		{
			name:     "scraped field",
			provider: func(db cdb.Handler) EvidenceProvider { return &scrapedFieldProvider{db: db} },
			request: EvidenceRequest{Selector: EvidenceSelector{
				SourceID: &sourceID, Field: "price", Value: "42",
			}},
		},
		{
			name:     "artifact",
			provider: func(db cdb.Handler) EvidenceProvider { return &artifactProvider{db: db} },
			request: EvidenceRequest{Selector: EvidenceSelector{
				SourceID: &sourceID, Field: "kind", Value: "report",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+" database error", func(t *testing.T) {
			db, mock, err := newSQLMockDB(t)
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			mock.ExpectQuery("SELECT source_id").WillReturnError(errors.New("connection refused"))
			var handler cdb.Handler = &sqlmockHandler{db: db}
			_, err = tc.provider(handler).Read(context.Background(), tc.request)
			if !errors.Is(err, ErrEvidenceUnavailable) {
				t.Fatalf("got %v, want ErrEvidenceUnavailable", err)
			}
		})
		t.Run(tc.name+" missing source", func(t *testing.T) {
			db, mock, err := newSQLMockDB(t)
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
			var handler cdb.Handler = &sqlmockHandler{db: db}
			_, err = tc.provider(handler).Read(context.Background(), tc.request)
			if !errors.Is(err, ErrEvidenceInvalid) {
				t.Fatalf("got %v, want ErrEvidenceInvalid", err)
			}
		})
	}
}

func TestRefreshGoalEvidenceRejectsOutOfScopeSelectorBeforeRead(t *testing.T) {
	sourceID := uint64(8)
	spec := &AgentGoalSpec{
		GoalID: "goal", SpecHash: "spec", AllowedSources: []uint64{7},
		Evidence: EvidencePolicy{
			Allow: []EvidenceKind{EvidenceSourceStatus}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096,
		},
		Budgets: GoalBudgets{MaxEvidenceRecords: 8, MaxEvidenceBytes: 4096},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "status", Description: "status",
			Predicate: Predicate{
				Kind: PredicateEvidenceExists, Evidence: EvidenceSourceStatus,
				Where: &EvidenceSelector{SourceID: &sourceID},
			},
		}}},
	}
	provider := &countingGoalProvider{kind: EvidenceSourceStatus}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	ledger := NewGoalEvidenceLedger(
		"run", "nonce", "trace", spec.GoalID, spec.SpecHash, "agent", "owner",
		[]uint64{7}, spec.Evidence, spec.Budgets, time.Now().Add(time.Hour),
	)
	err := refreshGoalEvidence(context.Background(), spec, registry, ledger, map[string]EvidenceQueryResult{}, time.Now().UTC())
	if !errors.Is(err, ErrEvidenceDenied) {
		t.Fatalf("out-of-scope selector was not denied: %v", err)
	}
	if atomic.LoadInt32(&provider.calls) != 0 {
		t.Fatalf("out-of-scope selector reached the provider")
	}
}

func TestReadGoalResourcesRejectsOutOfScopeBeforeRead(t *testing.T) {
	spec := &AgentGoalSpec{
		GoalID: "goal", SpecHash: "spec", AllowedSources: []uint64{7},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "status", Description: "status",
			Predicate: Predicate{
				Kind: PredicateResourceStatus, Resource: "source", ResourceID: 8, Status: "completed",
			},
		}}},
	}
	var handler cdb.Handler = &panicOnReadDBHandler{}
	resources := readGoalResources(context.Background(), handler, spec, "run")
	obs := resources[resourceObservationKey("source", 8)]
	if obs.Err != "out_of_scope" {
		t.Fatalf("out-of-scope resource was not rejected: %+v", obs)
	}
}
