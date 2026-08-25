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
)

func newTimeSeriesAggregateQueryFixture(t *testing.T) (*sql.DB, *recordingTimeSeriesHandler, *Handler) {
	t.Helper()
	db := openSQLiteMemoryDB(t)
	db.SetMaxOpenConns(1)
	_, err := db.Exec(`CREATE TABLE TimeSeriesAggregates (
		aggregate_id INTEGER PRIMARY KEY, metric_id INTEGER NOT NULL, bucket_start TIMESTAMP NOT NULL,
		bucket_end TIMESTAMP NOT NULL, information_seed_id INTEGER, information_seed_candidate_id INTEGER,
		source_id INTEGER, source_information_seed_id INTEGER, index_id INTEGER, entity_id INTEGER,
		subject_type TEXT, subject_id INTEGER, object_type TEXT, object_id INTEGER, correlation_rule_id INTEGER,
		correlation_object_type_1 TEXT, correlation_object_id_1 INTEGER, correlation_object_type_2 TEXT,
		correlation_object_id_2 INTEGER, dimensions TEXT, value_count INTEGER NOT NULL DEFAULT 0,
		occurrence_total NUMERIC, distinct_value_count INTEGER NOT NULL DEFAULT 0,
		numeric_count INTEGER NOT NULL DEFAULT 0, numeric_sum NUMERIC, numeric_min NUMERIC, numeric_max NUMERIC,
		numeric_avg NUMERIC, percentile_50 NUMERIC, percentile_75 NUMERIC, percentile_90 NUMERIC,
		percentile_95 NUMERIC, percentile_99 NUMERIC, first_observation_id INTEGER, first_observed_at TIMESTAMP,
		first_value_numeric NUMERIC, first_value_text TEXT, first_value_hash TEXT, last_observation_id INTEGER,
		last_observed_at TIMESTAMP, last_value_numeric NUMERIC, last_value_text TEXT, last_value_hash TEXT,
		last_value_boolean INTEGER, last_value_json TEXT, first_seen_at TIMESTAMP, last_seen_at TIMESTAMP,
		change_count INTEGER NOT NULL DEFAULT 0, aggregate_hash TEXT NOT NULL, created_at TIMESTAMP NOT NULL,
		deleted_at TIMESTAMP, last_updated_at TIMESTAMP NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	var base Handler = &SQLiteHandler{db: db, dbms: "SQLite"}
	recorder := &recordingTimeSeriesHandler{Handler: base}
	var handler Handler = recorder
	return db, recorder, &handler
}

func insertTimeSeriesAggregateQueryRows(t *testing.T, db *sql.DB, count int, dimensions func(int) string, bucketStart func(int) time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO TimeSeriesAggregates
		(aggregate_id, metric_id, bucket_start, bucket_end, dimensions, aggregate_hash, created_at, last_updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for id := 1; id <= count; id++ {
		at := base.Add(time.Duration(id) * time.Second)
		if bucketStart != nil {
			at = bucketStart(id)
		}
		dims := `{"wanted":false}`
		if dimensions != nil {
			dims = dimensions(id)
		}
		if _, err = stmt.Exec(id, at, at.Add(time.Hour), dims, fmt.Sprintf("aggregate-%d", id), at, at); err != nil {
			t.Fatalf("insert aggregate %d: %v", id, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func aggregateIDs(result TimeSeriesAggregateQueryResult) []uint64 {
	ids := make([]uint64, len(result.Aggregates))
	for i := range result.Aggregates {
		ids[i] = result.Aggregates[i].ID
	}
	return ids
}

func assertAggregateIDs(t *testing.T, got []uint64, want ...uint64) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("aggregate IDs = %v, want %v", got, want)
	}
}

func assertBoundedAggregateDimensionQueries(t *testing.T, recorder *recordingTimeSeriesHandler) {
	t.Helper()
	limitPlaceholder := regexp.MustCompile(`(?i)\bLIMIT\s+(?:\?|\$[0-9]+)\b`)
	if len(recorder.queries) < 2 {
		t.Fatalf("query count = %d, want a multi-chunk scan", len(recorder.queries))
	}
	for i, query := range recorder.queries {
		if !limitPlaceholder.MatchString(query) {
			t.Fatalf("dimension query %d is not bounded: %s", i+1, query)
		}
		if len(recorder.args[i]) == 0 || recorder.args[i][len(recorder.args[i])-1] != timeSeriesAggregateDimensionChunkSize {
			t.Fatalf("dimension query %d has no finite chunk limit: %v", i+1, recorder.args[i])
		}
	}
	if len(recorder.args[1]) <= len(recorder.args[0]) {
		t.Fatalf("continuation arguments did not change: first=%v second=%v", recorder.args[0], recorder.args[1])
	}
}

func TestQueryTimeSeriesAggregatesDimensionSparseChunksAndHasMore(t *testing.T) {
	db, recorder, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	insertTimeSeriesAggregateQueryRows(t, db, timeSeriesAggregateDimensionChunkSize+6, func(id int) string {
		if id >= timeSeriesAggregateDimensionChunkSize+1 && id%2 == 1 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	metricID := uint64(1)
	result, err := QueryTimeSeriesAggregatesContext(context.Background(), handler, TimeSeriesQueryFilter{
		MetricID: &metricID, Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateIDs(t, aggregateIDs(result), 1001, 1003)
	if !result.HasMore || result.Count != 2 {
		t.Fatalf("result metadata = count %d, hasMore %t", result.Count, result.HasMore)
	}
	assertBoundedAggregateDimensionQueries(t, recorder)
	if !strings.Contains(recorder.queries[1], "a.metric_id = ") || recorder.args[1][0] != metricID {
		t.Fatalf("continuation lost original metric filter: query=%s args=%v", recorder.queries[1], recorder.args[1])
	}
	if !strings.Contains(recorder.queries[1], "a.bucket_start > ") || !strings.Contains(recorder.queries[1], "a.aggregate_id > ") {
		t.Fatalf("continuation predicate missing: %s", recorder.queries[1])
	}
}

func TestQueryTimeSeriesAggregatesDimensionMatchingOffset(t *testing.T) {
	db, _, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	insertTimeSeriesAggregateQueryRows(t, db, 8, func(id int) string {
		if id == 2 || id == 4 || id == 7 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesAggregatesContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Offset: 1, Limit: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateIDs(t, aggregateIDs(result), 4)
	if !result.HasMore {
		t.Fatal("expected the third matching aggregate to set HasMore")
	}
}

func TestQueryTimeSeriesAggregatesDimensionFinalPage(t *testing.T) {
	db, recorder, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	insertTimeSeriesAggregateQueryRows(t, db, timeSeriesAggregateDimensionChunkSize+3, func(id int) string {
		if id == timeSeriesAggregateDimensionChunkSize+1 || id == timeSeriesAggregateDimensionChunkSize+3 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesAggregatesContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateIDs(t, aggregateIDs(result), 1001, 1003)
	if result.HasMore {
		t.Fatal("final page unexpectedly has more aggregates")
	}
	assertBoundedAggregateDimensionQueries(t, recorder)
}

func TestQueryTimeSeriesAggregatesDimensionBucketTieBreak(t *testing.T) {
	db, recorder, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	insertTimeSeriesAggregateQueryRows(t, db, timeSeriesAggregateDimensionChunkSize+2, func(id int) string {
		if id >= timeSeriesAggregateDimensionChunkSize {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, func(int) time.Time { return at })
	result, err := QueryTimeSeriesAggregatesContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateIDs(t, aggregateIDs(result), 1000, 1001, 1002)
	if result.HasMore {
		t.Fatal("bucket collision result unexpectedly has more aggregates")
	}
	assertBoundedAggregateDimensionQueries(t, recorder)
	if !strings.Contains(recorder.queries[1], "a.aggregate_id > ") {
		t.Fatalf("continuation does not tie-break on aggregate_id: %s", recorder.queries[1])
	}
}

func TestQueryTimeSeriesAggregatesDimensionDescendingChunks(t *testing.T) {
	db, recorder, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	insertTimeSeriesAggregateQueryRows(t, db, timeSeriesAggregateDimensionChunkSize+5, func(id int) string {
		if id == 2 || id == 4 {
			return `{"wanted":true}`
		}
		return `{"wanted":false}`
	}, nil)
	result, err := QueryTimeSeriesAggregatesContext(context.Background(), handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Descending: true, Pagination: TimeSeriesPagination{Limit: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateIDs(t, aggregateIDs(result), 4, 2)
	if result.HasMore {
		t.Fatal("descending final page unexpectedly has more aggregates")
	}
	assertBoundedAggregateDimensionQueries(t, recorder)
	if !strings.Contains(recorder.queries[1], "a.bucket_start < ") || !strings.Contains(recorder.queries[1], "a.aggregate_id < ") {
		t.Fatalf("descending query does not use reverse keyset continuation: %s", recorder.queries[1])
	}
}

func TestQueryTimeSeriesAggregatesDimensionCancellationBetweenChunks(t *testing.T) {
	db, recorder, handler := newTimeSeriesAggregateQueryFixture(t)
	defer db.Close()
	insertTimeSeriesAggregateQueryRows(t, db, timeSeriesAggregateDimensionChunkSize+2, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	recorder.cancel = cancel
	recorder.cancelOnCall = 2
	_, err := QueryTimeSeriesAggregatesContext(ctx, handler, TimeSeriesQueryFilter{
		Dimensions: map[string]interface{}{"wanted": true}, Pagination: TimeSeriesPagination{Limit: 1},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if len(recorder.queries) != 2 {
		t.Fatalf("query count after cancellation = %d, want 2", len(recorder.queries))
	}
	assertBoundedAggregateDimensionQueries(t, recorder)
}
