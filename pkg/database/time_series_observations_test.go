package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// recordingTimeSeriesHandler keeps these tests on the repository's SQLite
// fixture while exposing the generated queries and their keyset arguments.
type recordingTimeSeriesHandler struct {
	Handler
	queries      []string
	args         [][]interface{}
	cancel       context.CancelFunc
	cancelOnCall int
}

func TestPreviousObservationReferenceEquivalence(t *testing.T) {
	bases := []struct {
		name  string
		basis cfg.TimeSeriesTimeBasis
		set   func(*TimeSeriesObservation, time.Time)
	}{
		{"observed", cfg.TimeSeriesTimeObservedAt, func(o *TimeSeriesObservation, at time.Time) { o.ObservedAt = at }},
		{"event", cfg.TimeSeriesTimeEventAt, func(o *TimeSeriesObservation, at time.Time) { o.EffectiveAt = &at }},
		{"source", cfg.TimeSeriesTimeSourceTimestamp, func(o *TimeSeriesObservation, at time.Time) { o.SourceUpdatedAt = &at }},
	}
	for _, tc := range bases {
		t.Run(tc.name, func(t *testing.T) {
			db, _, handler := newTimeSeriesObservationFixture(t)
			defer db.Close()
			// These current operational rows intentionally have opposite lifecycle
			// states. Previous-observation lookup must depend on neither table.
			if _, err := db.Exec(`CREATE TABLE WebObjects (object_id INTEGER, deleted_at TIMESTAMP); CREATE TABLE Sources (source_id INTEGER, deleted_at TIMESTAMP); INSERT INTO WebObjects VALUES (91, CURRENT_TIMESTAMP); INSERT INTO Sources VALUES (7, NULL)`); err != nil {
				t.Fatal(err)
			}
			source, object := uint64(7), uint64(91)
			scope := TimeSeriesScope{SourceID: &source, ObjectType: "web_object", ObjectID: &object, SubjectType: "url"}
			dimensions := map[string]interface{}{"region": "eu", "nested": map[string]interface{}{"tier": 2}}
			base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
			insert := func(id uint64, active time.Time, value string, rowScope TimeSeriesScope, rowDimensions map[string]interface{}) {
				o := TimeSeriesObservation{ID: id, MetricID: 12, ObservedAt: base.Add(time.Duration(id) * time.Minute), CollectedAt: base, BucketStart: base, BucketEnd: base.Add(time.Hour), Scope: rowScope, ValueHash: value, DedupeKey: fmt.Sprintf("dedupe-%s-%d", tc.name, id), Dimensions: rowDimensions, IsChanged: true}
				tc.set(&o, active)
				series, err := TimeSeriesSeriesHash(o.MetricID, o.Scope, o.Dimensions)
				if err != nil {
					t.Fatal(err)
				}
				dims, _ := CanonicalTimeSeriesJSON(o.Dimensions)
				_, err = db.Exec(`INSERT INTO TimeSeriesObservations (observation_id,metric_id,observed_at,effective_at,collected_at,source_updated_at,bucket_start,bucket_end,source_id,subject_type,object_type,object_id,value_hash,series_hash,is_changed,dedupe_key,dimensions,created_at,last_updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, o.MetricID, o.ObservedAt, o.EffectiveAt, o.CollectedAt, o.SourceUpdatedAt, o.BucketStart, o.BucketEnd, source, o.Scope.SubjectType, o.Scope.ObjectType, object, o.ValueHash, series, o.IsChanged, o.DedupeKey, string(dims), base, base)
				if err != nil {
					t.Fatal(err)
				}
			}
			insert(1, base.Add(time.Minute), "old", scope, dimensions)
			insert(2, base.Add(2*time.Minute), "same-time-lower-id", scope, dimensions)
			insert(3, base.Add(2*time.Minute), "winner", scope, dimensions)
			insert(4, base.Add(3*time.Minute), "at-boundary", scope, dimensions)
			// Later rows in another complete scope and another dimension cannot win.
			otherScope := scope
			other := uint64(92)
			otherScope.ObjectID = &other
			insert(20, base.Add(2*time.Minute+30*time.Second), "other-scope", otherScope, dimensions)
			insert(21, base.Add(2*time.Minute+45*time.Second), "other-dimensions", scope, map[string]interface{}{"region": "us", "nested": map[string]interface{}{"tier": 2}})

			lookup := TimeSeriesChangeLookup{MetricID: 12, Scope: scope, Dimensions: dimensions, Before: base.Add(3 * time.Minute), TimeBasis: tc.basis}
			got, err := FindPreviousTimeSeriesObservation(handler, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != 3 || got.ValueHash != "winner" {
				t.Fatalf("previous = (%d,%q), want (3,winner)", got.ID, got.ValueHash)
			}
			query, _, queryErr := previousTimeSeriesObservationQuery(DBSQLiteStr, lookup)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			activeColumn := map[cfg.TimeSeriesTimeBasis]string{cfg.TimeSeriesTimeObservedAt: "observed_at", cfg.TimeSeriesTimeEventAt: "effective_at", cfg.TimeSeriesTimeSourceTimestamp: "source_updated_at"}[tc.basis]
			if !strings.Contains(query, "o.metric_id = $1 AND o.series_hash = $2 AND o."+activeColumn+" < $3") || !strings.Contains(query, "ORDER BY o."+activeColumn+" DESC, o.observation_id DESC LIMIT 1") || strings.Contains(strings.ToUpper(query), " JOIN ") {
				t.Fatalf("non-reference previous query: %s", query)
			}

			deletedAt := base.Add(4 * time.Minute)
			if _, err = db.Exec(`UPDATE TimeSeriesObservations SET deleted_at=? WHERE observation_id=3`, deletedAt); err != nil {
				t.Fatal(err)
			}
			got, err = FindPreviousTimeSeriesObservation(handler, lookup)
			if err != nil || got.ID != 3 || got.DeletedAt == nil {
				t.Fatalf("logically deleted previous = %#v, %v", got, err)
			}

			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			txGot, err := (TransactionTimeSeriesRepository{Tx: tx, DBMS: DBSQLiteStr}).PreviousObservationContext(context.Background(), lookup)
			_ = tx.Rollback()
			if err != nil || txGot.ID != got.ID || txGot.ValueHash != got.ValueHash || (txGot.DeletedAt == nil) != (got.DeletedAt == nil) {
				t.Fatalf("transaction result %#v differs from handler %#v: %v", txGot, got, err)
			}
		})
	}
}

func (h *recordingTimeSeriesHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	h.queries = append(h.queries, query)
	h.args = append(h.args, append([]interface{}(nil), args...))
	if h.cancelOnCall == len(h.queries) {
		h.cancel()
		return nil, ctx.Err()
	}
	return h.Handler.QueryContext(ctx, query, args...)
}

func newTimeSeriesObservationFixture(t *testing.T) (*sql.DB, *recordingTimeSeriesHandler, *Handler) {
	t.Helper()
	db := openSQLiteMemoryDB(t)
	db.SetMaxOpenConns(1)
	_, err := db.Exec(`CREATE TABLE TimeSeriesObservations (
		observation_id INTEGER PRIMARY KEY, metric_id INTEGER NOT NULL, observed_at TIMESTAMP NOT NULL,
		effective_at TIMESTAMP, collected_at TIMESTAMP NOT NULL, source_updated_at TIMESTAMP,
		bucket_start TIMESTAMP NOT NULL, bucket_end TIMESTAMP NOT NULL, information_seed_id INTEGER,
		information_seed_candidate_id INTEGER, source_id INTEGER, source_information_seed_id INTEGER,
		index_id INTEGER, entity_id INTEGER, subject_type TEXT, subject_id INTEGER, object_type TEXT,
		object_id INTEGER, correlation_rule_id INTEGER, correlation_object_type_1 TEXT,
		correlation_object_id_1 INTEGER, correlation_object_type_2 TEXT, correlation_object_id_2 INTEGER,
		value_numeric NUMERIC, value_integer INTEGER, value_boolean INTEGER, value_text TEXT,
		value_json TEXT, value_timestamp TIMESTAMP, value_hash TEXT NOT NULL, series_hash TEXT, previous_observation_id INTEGER,
		previous_value_hash TEXT, is_changed INTEGER NOT NULL DEFAULT 0, change_type TEXT,
		change_delta_numeric NUMERIC, change_detected_at TIMESTAMP, dedupe_key TEXT NOT NULL,
		dimensions TEXT, provenance TEXT, provenance_hash TEXT, created_at TIMESTAMP NOT NULL,
		deleted_at TIMESTAMP, last_updated_at TIMESTAMP NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	var base Handler = &SQLiteHandler{db: db, dbms: "SQLite"}
	recorder := &recordingTimeSeriesHandler{Handler: base}
	var handler Handler = recorder
	return db, recorder, &handler
}

func insertTimeSeriesQueryRows(t *testing.T, db *sql.DB, count int, dimensions func(int) string, observedAt func(int) time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO TimeSeriesObservations
		(observation_id, metric_id, observed_at, collected_at, bucket_start, bucket_end, value_hash,
		 dedupe_key, dimensions, created_at, last_updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for id := 1; id <= count; id++ {
		at := base.Add(time.Duration(id) * time.Second)
		if observedAt != nil {
			at = observedAt(id)
		}
		dims := `{"wanted":false}`
		if dimensions != nil {
			dims = dimensions(id)
		}
		if _, err = stmt.Exec(id, at, at, at, at.Add(time.Hour), fmt.Sprintf("hash-%d", id), fmt.Sprintf("key-%d", id), dims, at, at); err != nil {
			t.Fatalf("insert observation %d: %v", id, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func wantedIDs(result TimeSeriesObservationQueryResult) []uint64 {
	ids := make([]uint64, len(result.Observations))
	for i := range result.Observations {
		ids[i] = result.Observations[i].ID
	}
	return ids
}

func assertIDs(t *testing.T, got []uint64, want ...uint64) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("observation IDs = %v, want %v", got, want)
	}
}

func assertBoundedDimensionQueries(t *testing.T, recorder *recordingTimeSeriesHandler) {
	t.Helper()
	limitPlaceholder := regexp.MustCompile(`(?i)\bLIMIT\s+(?:\?|\$[0-9]+)\b`)
	if len(recorder.queries) < 2 {
		t.Fatalf("query count = %d, want a multi-chunk scan", len(recorder.queries))
	}
	for i, query := range recorder.queries {
		if !limitPlaceholder.MatchString(query) {
			t.Fatalf("dimension query %d is not bounded: %s", i+1, query)
		}
		if len(recorder.args[i]) == 0 || recorder.args[i][len(recorder.args[i])-1] != timeSeriesObservationDimensionChunkSize {
			t.Fatalf("dimension query %d has no finite chunk limit: %v", i+1, recorder.args[i])
		}
	}
	if len(recorder.args[1]) <= len(recorder.args[0]) {
		t.Fatalf("continuation arguments did not change: first=%v second=%v", recorder.args[0], recorder.args[1])
	}
}

func TestQueryTimeSeriesObservationsDimensionSparseChunks(t *testing.T) {
	db, recorder, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	insertTimeSeriesQueryRows(t, db, timeSeriesObservationDimensionChunkSize+6, func(id int) string {
		if id >= timeSeriesObservationDimensionChunkSize+1 && id%2 == 1 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)

	result, err := QueryTimeSeriesObservationsContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, wantedIDs(result), 1001, 1003)
	if !result.HasMore || result.Count != 2 {
		t.Fatalf("result metadata = count %d, hasMore %t", result.Count, result.HasMore)
	}
	assertBoundedDimensionQueries(t, recorder)
}

func TestQueryTimeSeriesObservationsDimensionMatchingOffset(t *testing.T) {
	db, _, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	insertTimeSeriesQueryRows(t, db, 8, func(id int) string {
		if id == 2 || id == 4 || id == 7 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesObservationsContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Offset: 1, Limit: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, wantedIDs(result), 4)
	if !result.HasMore {
		t.Fatal("expected the third matching row to set HasMore")
	}
}

func TestQueryTimeSeriesObservationsDimensionFinalPage(t *testing.T) {
	db, recorder, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	insertTimeSeriesQueryRows(t, db, timeSeriesObservationDimensionChunkSize+3, func(id int) string {
		if id == timeSeriesObservationDimensionChunkSize+1 || id == timeSeriesObservationDimensionChunkSize+3 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesObservationsContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, wantedIDs(result), 1001, 1003)
	if result.HasMore {
		t.Fatal("final page unexpectedly has more matches")
	}
	assertBoundedDimensionQueries(t, recorder)
}

func TestQueryTimeSeriesObservationsDimensionTimestampTieBreak(t *testing.T) {
	db, recorder, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	insertTimeSeriesQueryRows(t, db, timeSeriesObservationDimensionChunkSize+2, func(id int) string {
		if id >= timeSeriesObservationDimensionChunkSize {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, func(int) time.Time { return at })
	result, err := QueryTimeSeriesObservationsContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, wantedIDs(result), 1000, 1001, 1002)
	if result.HasMore {
		t.Fatal("timestamp collision result unexpectedly has more rows")
	}
	assertBoundedDimensionQueries(t, recorder)
	if !strings.Contains(recorder.queries[1], "o.observation_id > ") {
		t.Fatalf("continuation does not tie-break on observation_id: %s", recorder.queries[1])
	}
}

func TestQueryTimeSeriesObservationsDimensionDescendingChunks(t *testing.T) {
	db, recorder, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	insertTimeSeriesQueryRows(t, db, timeSeriesObservationDimensionChunkSize+5, func(id int) string {
		if id == 2 || id == 4 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesObservationsContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Descending: true, Pagination: TimeSeriesPagination{Limit: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, wantedIDs(result), 4, 2)
	if result.HasMore {
		t.Fatal("descending final page unexpectedly has more rows")
	}
	assertBoundedDimensionQueries(t, recorder)
	if !strings.Contains(recorder.queries[1], "o.observed_at < ") || !strings.Contains(recorder.queries[1], "o.observation_id < ") {
		t.Fatalf("descending query does not use reverse keyset continuation: %s", recorder.queries[1])
	}
}

func TestQueryTimeSeriesObservationsDimensionCancellationBetweenChunks(t *testing.T) {
	db, recorder, handler := newTimeSeriesObservationFixture(t)
	defer db.Close()
	insertTimeSeriesQueryRows(t, db, timeSeriesObservationDimensionChunkSize+2, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	recorder.cancel = cancel
	recorder.cancelOnCall = 2
	_, err := QueryTimeSeriesObservationsContext(ctx, handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 1},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if len(recorder.queries) != 2 {
		t.Fatalf("query count after cancellation = %d, want 2", len(recorder.queries))
	}
	assertBoundedDimensionQueries(t, recorder)
}
