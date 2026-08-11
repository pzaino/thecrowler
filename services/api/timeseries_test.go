package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type fakeTimeSeriesAPIRepository struct {
	metric                *cdb.TimeSeriesMetric
	metrics               []cdb.TimeSeriesMetric
	aggregates            cdb.TimeSeriesAggregateQueryResult
	observations          cdb.TimeSeriesObservationQueryResult
	aggregate             *cdb.TimeSeriesAggregate
	lastFilter            cdb.TimeSeriesQueryFilter
	lastContext           context.Context
	err                   error
	aggregateCandidates   []cdb.TimeSeriesAggregate
	observationCandidates []cdb.TimeSeriesObservation
	aggregateChunks       int
	observationChunks     int
}
type timeSeriesContextKey struct{}

func (f *fakeTimeSeriesAPIRepository) MetricByID(context.Context, uint64) (*cdb.TimeSeriesMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.metric, nil
}
func (f *fakeTimeSeriesAPIRepository) MetricByKey(ctx context.Context, _ string) (*cdb.TimeSeriesMetric, error) {
	f.lastContext = ctx
	if f.err != nil {
		return nil, f.err
	}
	return f.metric, nil
}
func (f *fakeTimeSeriesAPIRepository) ListMetrics(context.Context, cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error) {
	return f.metrics, f.err
}
func (f *fakeTimeSeriesAPIRepository) QueryAggregates(ctx context.Context, filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesAggregateQueryResult, error) {
	f.lastContext = ctx
	f.lastFilter = filter
	if f.aggregateCandidates != nil {
		rows, hasMore, chunks := filterAggregateCandidates(f.aggregateCandidates, filter, 4)
		f.aggregateChunks = chunks
		return cdb.TimeSeriesAggregateQueryResult{Aggregates: rows, Count: len(rows), HasMore: hasMore}, f.err
	}
	return f.aggregates, f.err
}
func (f *fakeTimeSeriesAPIRepository) QueryObservations(_ context.Context, filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesObservationQueryResult, error) {
	f.lastFilter = filter
	if f.observationCandidates != nil {
		rows, hasMore, chunks := filterObservationCandidates(f.observationCandidates, filter, 4)
		f.observationChunks = chunks
		return cdb.TimeSeriesObservationQueryResult{Observations: rows, Count: len(rows), HasMore: hasMore}, f.err
	}
	return f.observations, f.err
}
func (f *fakeTimeSeriesAPIRepository) AggregateByHash(context.Context, string) (*cdb.TimeSeriesAggregate, error) {
	if f.aggregate == nil && f.err == nil {
		return nil, errors.New("not found")
	}
	return f.aggregate, f.err
}

func useFakeTimeSeriesRepository(t *testing.T, fake *fakeTimeSeriesAPIRepository) {
	t.Helper()
	old := newTimeSeriesAPIRepository
	newTimeSeriesAPIRepository = func() timeSeriesAPIRepository { return fake }
	t.Cleanup(func() { newTimeSeriesAPIRepository = old })
}

func testTimeSeriesMetric() *cdb.TimeSeriesMetric {
	return &cdb.TimeSeriesMetric{ID: 7, Key: "pages.changed", DisplayName: "Changed pages", ValueType: cfg.TimeSeriesValueInteger, Aggregate: cfg.TimeSeriesAggregateSum, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, StoreValueText: true, Enabled: true, Dimensions: json.RawMessage(`[{"key":"region"}]`)}
}

func TestTimeSeriesAggregateHandlerUsesAggregateRowsAndStableShape(t *testing.T) {
	metric := testTimeSeriesMetric()
	sum := 12.5
	sourceID := uint64(44)
	fake := &fakeTimeSeriesAPIRepository{metric: metric, aggregates: cdb.TimeSeriesAggregateQueryResult{Aggregates: []cdb.TimeSeriesAggregate{{MetricID: metric.ID, BucketStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), BucketEnd: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), Scope: cdb.TimeSeriesScope{SourceID: &sourceID}, Dimensions: map[string]interface{}{"region": "eu"}, ValueCount: 3, OccurrenceTotal: 8, NumericSum: &sum, AggregateHash: strings.Repeat("a", 64)}}, HasMore: false}}
	useFakeTimeSeriesRepository(t, fake)

	req := httptest.NewRequest(http.MethodGet, "/v1/timeseries?metric_key=pages.changed&dimension=region=eu&aggregate=sum&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", nil)
	req = req.WithContext(context.WithValue(req.Context(), timeSeriesContextKey{}, "request"))
	res := httptest.NewRecorder()
	timeSeriesAggregatesHandler(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	var body TimeSeriesAggregateResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Value != 12.5 || body.Items[0].Values.Count != 3 || body.Items[0].Scope.SourceID == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
	if fake.lastFilter.MetricKey != metric.Key || fake.lastFilter.Dimensions["region"] != "eu" {
		t.Fatalf("filters did not compose: %+v", fake.lastFilter)
	}
	if got := fake.lastContext.Value(timeSeriesContextKey{}); got != "request" {
		t.Fatalf("repository did not receive request context: %v", got)
	}
}

func TestTimeSeriesQueryValidation(t *testing.T) {
	useFakeTimeSeriesRepository(t, &fakeTimeSeriesAPIRepository{metric: testTimeSeriesMetric()})
	tests := []struct{ name, query string }{
		{"bucket", "metric_id=7&bucket=year"},
		{"id", "metric_id=-1"},
		{"dimension", "metric_id=7&dimension=bad%20key=x"},
		{"inverted", "metric_id=7&from=2026-02-01&to=2026-01-01"},
		{"broad", "metric_id=7&from=2020-01-01&to=2026-01-01"},
		{"raw unbounded", "metric_id=7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _ := url.ParseQuery(tt.query)
			raw := tt.name == "raw unbounded"
			_, _, err := parseTimeSeriesQuery(context.Background(), values, timeSeriesObservationMaxLimit, timeSeriesRawMaxRange, raw)
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.query)
			}
		})
	}
}

func TestTimeSeriesObservationsOmitHashesProvenanceAndProtectedValues(t *testing.T) {
	metric := testTimeSeriesMetric()
	metric.HashOnly = true
	secret := "secret"
	fake := &fakeTimeSeriesAPIRepository{metric: metric, observations: cdb.TimeSeriesObservationQueryResult{Observations: []cdb.TimeSeriesObservation{{ID: 9, MetricID: metric.ID, ObservedAt: time.Now(), CollectedAt: time.Now(), BucketStart: time.Now(), BucketEnd: time.Now().Add(time.Hour), Value: cdb.TimeSeriesValue{Text: &secret}, ValueHash: "private", Provenance: json.RawMessage(`{"secret":true}`), ProvenanceHash: "private", Dimensions: map[string]interface{}{}}}}}
	useFakeTimeSeriesRepository(t, fake)

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/timeseries/observations?metric_id=7&from=2026-01-01&to=2026-01-02", nil)
	timeSeriesObservationsHandler(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, forbidden := range []string{"secret", "value_hash", "provenance"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, body)
		}
	}
}

func TestTimeSeriesDrilldownUsesServerAggregateScope(t *testing.T) {
	metric := testTimeSeriesMetric()
	sourceID := uint64(21)
	aggregate := &cdb.TimeSeriesAggregate{MetricID: metric.ID, BucketStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), BucketEnd: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), Scope: cdb.TimeSeriesScope{SourceID: &sourceID}, Dimensions: map[string]interface{}{"region": "eu"}, AggregateHash: strings.Repeat("b", 64)}
	fake := &fakeTimeSeriesAPIRepository{metric: metric, aggregate: aggregate}
	useFakeTimeSeriesRepository(t, fake)

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/timeseries/drilldown?aggregate_hash="+aggregate.AggregateHash+"&source_id=999&dimension=region%3Dus", nil)
	timeSeriesDrilldownHandler(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	if fake.lastFilter.SourceID == nil || *fake.lastFilter.SourceID != sourceID || fake.lastFilter.Dimensions["region"] != "eu" {
		t.Fatalf("drilldown trusted client scope: %+v", fake.lastFilter)
	}
}

func TestTimeSeriesDimensionComparisonEnforcesCardinality(t *testing.T) {
	oldConfig := config
	config.TimeSeries.Cardinality.MaxValuesPerDimension = 1
	t.Cleanup(func() { config = oldConfig })
	metric := testTimeSeriesMetric()
	fake := &fakeTimeSeriesAPIRepository{metric: metric, aggregates: cdb.TimeSeriesAggregateQueryResult{Aggregates: []cdb.TimeSeriesAggregate{{MetricID: 7, Dimensions: map[string]interface{}{"region": "eu"}}, {MetricID: 7, Dimensions: map[string]interface{}{"region": "us"}}}}}
	useFakeTimeSeriesRepository(t, fake)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/timeseries/dimensions?metric_id=7&dimension_key=region", nil)
	timeSeriesDimensionsHandler(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
}

// The API repository fake deliberately scans small bounded batches. This keeps
// the tests independent of the database package's private chunk-size constant
// while still proving that sparse candidate batches are invisible at the REST
// boundary and that offsets count dimension matches, not candidates.
func filterAggregateCandidates(candidates []cdb.TimeSeriesAggregate, filter cdb.TimeSeriesQueryFilter, chunkSize int) ([]cdb.TimeSeriesAggregate, bool, int) {
	matches := make([]cdb.TimeSeriesAggregate, 0)
	chunks := 0
	for start := 0; start < len(candidates); start += chunkSize {
		chunks++
		end := start + chunkSize
		if end > len(candidates) {
			end = len(candidates)
		}
		for _, row := range candidates[start:end] {
			if dimensionsMatch(row.Dimensions, filter.Dimensions) {
				matches = append(matches, row)
			}
		}
	}
	if filter.Descending {
		reverseAggregates(matches)
	}
	start := filter.Pagination.Offset
	if start > len(matches) {
		start = len(matches)
	}
	end := start + filter.Pagination.Limit
	hasMore := end < len(matches)
	if end > len(matches) {
		end = len(matches)
	}
	return matches[start:end], hasMore, chunks
}

func filterObservationCandidates(candidates []cdb.TimeSeriesObservation, filter cdb.TimeSeriesQueryFilter, chunkSize int) ([]cdb.TimeSeriesObservation, bool, int) {
	matches := make([]cdb.TimeSeriesObservation, 0)
	chunks := 0
	for start := 0; start < len(candidates); start += chunkSize {
		chunks++
		end := start + chunkSize
		if end > len(candidates) {
			end = len(candidates)
		}
		for _, row := range candidates[start:end] {
			if dimensionsMatch(row.Dimensions, filter.Dimensions) {
				matches = append(matches, row)
			}
		}
	}
	if filter.Descending {
		for left, right := 0, len(matches)-1; left < right; left, right = left+1, right-1 {
			matches[left], matches[right] = matches[right], matches[left]
		}
	}
	start := filter.Pagination.Offset
	if start > len(matches) {
		start = len(matches)
	}
	end := start + filter.Pagination.Limit
	hasMore := end < len(matches)
	if end > len(matches) {
		end = len(matches)
	}
	return matches[start:end], hasMore, chunks
}

func dimensionsMatch(row, requested map[string]interface{}) bool {
	for key, value := range requested {
		if !reflect.DeepEqual(row[key], value) {
			return false
		}
	}
	return true
}

func reverseAggregates(rows []cdb.TimeSeriesAggregate) {
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
}

func sparseAPIRows() ([]cdb.TimeSeriesAggregate, []cdb.TimeSeriesObservation) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	aggregates := make([]cdb.TimeSeriesAggregate, 10)
	observations := make([]cdb.TimeSeriesObservation, 10)
	for i := range aggregates {
		id := uint64(i + 1)
		dimensions := map[string]interface{}{"region": "us"}
		if id == 2 || id == 6 || id == 8 || id == 10 {
			dimensions = map[string]interface{}{"region": "eu"}
		}
		at := base.Add(time.Duration(id) * time.Minute)
		value := int64(id)
		aggregates[i] = cdb.TimeSeriesAggregate{ID: id, MetricID: 7, BucketStart: at, BucketEnd: at.Add(time.Hour), Dimensions: dimensions, ValueCount: value, AggregateHash: strings.Repeat(fmt.Sprintf("%x", id), 64)[:64]}
		observations[i] = cdb.TimeSeriesObservation{ID: id, MetricID: 7, ObservedAt: at, CollectedAt: at, BucketStart: at, BucketEnd: at.Add(time.Hour), Dimensions: dimensions, Value: cdb.TimeSeriesValue{Integer: &value}}
	}
	return aggregates, observations
}

func TestTimeSeriesAggregateDimensionPaginationAcrossSparseChunks(t *testing.T) {
	aggregates, _ := sparseAPIRows()
	for _, tc := range []struct {
		name    string
		offset  int
		wantIDs []uint64
		hasMore bool
	}{
		{"matching offset with more", 1, []uint64{6, 8}, true},
		{"final filtered page", 2, []uint64{8, 10}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTimeSeriesAPIRepository{metric: testTimeSeriesMetric(), aggregateCandidates: aggregates}
			useFakeTimeSeriesRepository(t, fake)
			res := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/timeseries?metric_id=7&dimension=region%%3Deu&limit=2&offset=%d", tc.offset), nil)
			timeSeriesAggregatesHandler(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
			}
			var body TimeSeriesAggregateResponse
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Pagination.Limit != 2 || body.Pagination.Offset != tc.offset || body.Pagination.HasMore != tc.hasMore {
				t.Fatalf("pagination = %+v", body.Pagination)
			}
			got := make([]uint64, len(body.Items))
			for i, item := range body.Items {
				got[i] = uint64(item.Values.Count)
				if item.Dimensions["region"] != "eu" {
					t.Fatalf("unfiltered item: %+v", item)
				}
			}
			if !reflect.DeepEqual(got, tc.wantIDs) {
				t.Fatalf("ordered IDs = %v, want %v", got, tc.wantIDs)
			}
			if fake.aggregateChunks < 2 {
				t.Fatalf("candidate chunks = %d, want multiple", fake.aggregateChunks)
			}
			assertTimeSeriesPublicEnvelope(t, res.Body.Bytes(), "items")
		})
	}
}

func TestTimeSeriesObservationDimensionPaginationAndDescendingSparseChunks(t *testing.T) {
	_, observations := sparseAPIRows()
	for _, tc := range []struct {
		name, order string
		offset      int
		want        []uint64
		hasMore     bool
	}{
		{"matching offset with more", "asc", 1, []uint64{6, 8}, true},
		{"final filtered page", "asc", 2, []uint64{8, 10}, false},
		{"descending sparse page", "desc", 0, []uint64{10, 8}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTimeSeriesAPIRepository{metric: testTimeSeriesMetric(), observationCandidates: observations}
			useFakeTimeSeriesRepository(t, fake)
			res := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/timeseries/observations?metric_id=7&dimension=region%%3Deu&from=2026-01-01&to=2026-01-02&limit=2&offset=%d&order=%s", tc.offset, tc.order), nil)
			timeSeriesObservationsHandler(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
			}
			var body TimeSeriesObservationListResponse
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Pagination.Limit != 2 || body.Pagination.Offset != tc.offset || body.Pagination.HasMore != tc.hasMore {
				t.Fatalf("pagination = %+v", body.Pagination)
			}
			got := make([]uint64, len(body.Items))
			for i, item := range body.Items {
				got[i] = item.ID
				if item.Dimensions["region"] != "eu" {
					t.Fatalf("unfiltered item: %+v", item)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ordered IDs = %v, want %v", got, tc.want)
			}
			if fake.observationChunks < 2 {
				t.Fatalf("candidate chunks = %d, want multiple", fake.observationChunks)
			}
			assertTimeSeriesPublicEnvelope(t, res.Body.Bytes(), "items")
		})
	}
}

func TestTimeSeriesDrilldownDimensionScopeBoundsSparsePage(t *testing.T) {
	_, observations := sparseAPIRows()
	sourceID := uint64(21)
	aggregate := &cdb.TimeSeriesAggregate{MetricID: 7, BucketStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), BucketEnd: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Scope: cdb.TimeSeriesScope{SourceID: &sourceID}, Dimensions: map[string]interface{}{"region": "eu"}, AggregateHash: strings.Repeat("b", 64)}
	fake := &fakeTimeSeriesAPIRepository{metric: testTimeSeriesMetric(), aggregate: aggregate, observationCandidates: observations}
	useFakeTimeSeriesRepository(t, fake)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/timeseries/drilldown?aggregate_hash="+aggregate.AggregateHash+"&dimension=region%3Dus&limit=2&offset=1", nil)
	timeSeriesDrilldownHandler(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	var body TimeSeriesDrilldownResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if fake.lastFilter.Dimensions["region"] != "eu" || fake.lastFilter.SourceID == nil || *fake.lastFilter.SourceID != sourceID {
		t.Fatalf("aggregate scope not propagated: %+v", fake.lastFilter)
	}
	if len(body.Observations) != 2 || body.Observations[0].ID != 6 || body.Observations[1].ID != 8 || !body.Pagination.HasMore || body.Pagination.Offset != 1 || body.Pagination.Limit != 2 {
		t.Fatalf("unexpected drilldown page: %+v", body)
	}
	if fake.observationChunks < 2 {
		t.Fatalf("candidate chunks = %d, want multiple", fake.observationChunks)
	}
	assertTimeSeriesPublicEnvelope(t, res.Body.Bytes(), "observations")
}

func assertTimeSeriesPublicEnvelope(t *testing.T, raw []byte, collection string) {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	page, ok := body["pagination"].(map[string]interface{})
	if !ok {
		t.Fatalf("pagination schema changed: %s", raw)
	}
	for _, key := range []string{"limit", "offset", "count"} {
		if _, ok := page[key].(float64); !ok {
			t.Fatalf("pagination.%s is not a JSON number: %T", key, page[key])
		}
	}
	if _, ok := page["has_more"].(bool); !ok {
		t.Fatalf("pagination.has_more is not boolean: %T", page["has_more"])
	}
	if _, ok := body[collection].([]interface{}); !ok {
		t.Fatalf("%s is not an array: %T", collection, body[collection])
	}
	for _, forbidden := range []string{"chunk_size", "continuation_cursor", "cursor"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("public response exposed %q", forbidden)
		}
		if _, exists := page[forbidden]; exists {
			t.Fatalf("pagination exposed %q", forbidden)
		}
	}
}
