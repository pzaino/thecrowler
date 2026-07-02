package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type fakeTimeSeriesAPIRepository struct {
	metric       *cdb.TimeSeriesMetric
	metrics      []cdb.TimeSeriesMetric
	aggregates   cdb.TimeSeriesAggregateQueryResult
	observations cdb.TimeSeriesObservationQueryResult
	aggregate    *cdb.TimeSeriesAggregate
	lastFilter   cdb.TimeSeriesQueryFilter
	err          error
}

func (f *fakeTimeSeriesAPIRepository) MetricByID(uint64) (*cdb.TimeSeriesMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.metric, nil
}
func (f *fakeTimeSeriesAPIRepository) MetricByKey(string) (*cdb.TimeSeriesMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.metric, nil
}
func (f *fakeTimeSeriesAPIRepository) ListMetrics(cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error) {
	return f.metrics, f.err
}
func (f *fakeTimeSeriesAPIRepository) QueryAggregates(filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesAggregateQueryResult, error) {
	f.lastFilter = filter
	return f.aggregates, f.err
}
func (f *fakeTimeSeriesAPIRepository) QueryObservations(filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesObservationQueryResult, error) {
	f.lastFilter = filter
	return f.observations, f.err
}
func (f *fakeTimeSeriesAPIRepository) AggregateByHash(string) (*cdb.TimeSeriesAggregate, error) {
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
			_, _, err := parseTimeSeriesQuery(values, timeSeriesObservationMaxLimit, timeSeriesRawMaxRange, raw)
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
