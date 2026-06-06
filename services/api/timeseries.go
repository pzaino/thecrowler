// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	timeSeriesDefaultLimit        = 100
	timeSeriesAggregateMaxLimit   = 1000
	timeSeriesObservationMaxLimit = 200
	timeSeriesDrilldownMaxLimit   = 100
	timeSeriesDefaultCardinality  = 25
	timeSeriesAbsoluteCardinality = 100
	timeSeriesAggregateMaxRange   = 366 * 24 * time.Hour
	timeSeriesRawMaxRange         = 31 * 24 * time.Hour
)

var timeSeriesDimensionKeyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

type timeSeriesAPIRepository interface {
	MetricByID(uint64) (*cdb.TimeSeriesMetric, error)
	MetricByKey(string) (*cdb.TimeSeriesMetric, error)
	ListMetrics(cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error)
	QueryAggregates(cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesAggregateQueryResult, error)
	QueryObservations(cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesObservationQueryResult, error)
	AggregateByHash(string) (*cdb.TimeSeriesAggregate, error)
}

type handlerTimeSeriesAPIRepository struct{ db *cdb.Handler }

func (r handlerTimeSeriesAPIRepository) MetricByID(id uint64) (*cdb.TimeSeriesMetric, error) {
	return cdb.GetTimeSeriesMetricByID(r.db, id)
}
func (r handlerTimeSeriesAPIRepository) MetricByKey(key string) (*cdb.TimeSeriesMetric, error) {
	return cdb.GetTimeSeriesMetricByKey(r.db, key)
}
func (r handlerTimeSeriesAPIRepository) ListMetrics(filter cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error) {
	return cdb.ListTimeSeriesMetrics(r.db, filter)
}
func (r handlerTimeSeriesAPIRepository) QueryAggregates(filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesAggregateQueryResult, error) {
	return cdb.QueryTimeSeriesAggregates(r.db, filter)
}
func (r handlerTimeSeriesAPIRepository) QueryObservations(filter cdb.TimeSeriesQueryFilter) (cdb.TimeSeriesObservationQueryResult, error) {
	return cdb.QueryTimeSeriesObservations(r.db, filter)
}
func (r handlerTimeSeriesAPIRepository) AggregateByHash(hash string) (*cdb.TimeSeriesAggregate, error) {
	return cdb.GetTimeSeriesAggregateByHash(r.db, hash)
}

var newTimeSeriesAPIRepository = func() timeSeriesAPIRepository {
	return handlerTimeSeriesAPIRepository{db: &dbHandler}
}

type timeSeriesParsedQuery struct {
	filter    cdb.TimeSeriesQueryFilter
	aggregate cfg.TimeSeriesAggregate
	limit     int
	offset    int
}

func timeSeriesMetricsHandler(w http.ResponseWriter, r *http.Request) {
	if !timeSeriesRequireGET(w, r) {
		return
	}
	values := r.URL.Query()
	limit, offset, err := parseTimeSeriesPagination(values, timeSeriesDefaultLimit, timeSeriesAggregateMaxLimit)
	if err != nil {
		timeSeriesError(w, err, http.StatusBadRequest)
		return
	}
	filter := cdb.TimeSeriesMetricFilter{Key: strings.TrimSpace(values.Get("metric_key")), Pagination: cdb.TimeSeriesPagination{Limit: limit + 1, Offset: offset}}
	if rawID := strings.TrimSpace(values.Get("metric_id")); rawID != "" {
		id, parseErr := strconv.ParseUint(rawID, 10, 64)
		if parseErr != nil || id == 0 {
			timeSeriesError(w, fmt.Errorf("metric_id must be a positive integer"), http.StatusBadRequest)
			return
		}
		metric, lookupErr := newTimeSeriesAPIRepository().MetricByID(id)
		if lookupErr != nil {
			timeSeriesError(w, lookupErr, timeSeriesStatus(lookupErr))
			return
		}
		items := []TimeSeriesMetricDefinition{publicTimeSeriesMetric(metric)}
		timeSeriesJSON(w, http.StatusOK, TimeSeriesMetricListResponse{Items: items, Pagination: timeSeriesPage(limit, offset, len(items), false)})
		return
	}
	if source := strings.TrimSpace(values.Get("source")); source != "" {
		filter.SourceKind = cfg.TimeSeriesSourceKind(source)
	}
	if raw := strings.TrimSpace(values.Get("enabled")); raw != "" {
		enabled, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			timeSeriesError(w, fmt.Errorf("enabled must be true or false"), http.StatusBadRequest)
			return
		}
		filter.Enabled = &enabled
	}
	metrics, err := newTimeSeriesAPIRepository().ListMetrics(filter)
	if err != nil {
		timeSeriesError(w, err, http.StatusInternalServerError)
		return
	}
	hasMore := len(metrics) > limit
	if hasMore {
		metrics = metrics[:limit]
	}
	items := make([]TimeSeriesMetricDefinition, 0, len(metrics))
	for i := range metrics {
		items = append(items, publicTimeSeriesMetric(&metrics[i]))
	}
	timeSeriesJSON(w, http.StatusOK, TimeSeriesMetricListResponse{Items: items, Pagination: timeSeriesPage(limit, offset, len(items), hasMore)})
}

func timeSeriesAggregatesHandler(w http.ResponseWriter, r *http.Request) {
	if !timeSeriesRequireGET(w, r) {
		return
	}
	parsed, metric, err := parseTimeSeriesQuery(r.URL.Query(), timeSeriesAggregateMaxLimit, timeSeriesAggregateMaxRange, false)
	if err != nil {
		timeSeriesError(w, err, timeSeriesStatus(err))
		return
	}
	result, err := newTimeSeriesAPIRepository().QueryAggregates(parsed.filter)
	if err != nil {
		timeSeriesError(w, err, http.StatusInternalServerError)
		return
	}
	items := make([]TimeSeriesAggregateBucket, 0, len(result.Aggregates))
	for i := range result.Aggregates {
		items = append(items, publicTimeSeriesAggregate(&result.Aggregates[i], metric, parsed.aggregate))
	}
	timeSeriesJSON(w, http.StatusOK, TimeSeriesAggregateResponse{Items: items, Pagination: timeSeriesPage(parsed.limit, parsed.offset, len(items), result.HasMore)})
}

func timeSeriesObservationsHandler(w http.ResponseWriter, r *http.Request) {
	if !timeSeriesRequireGET(w, r) {
		return
	}
	parsed, metric, err := parseTimeSeriesQuery(r.URL.Query(), timeSeriesObservationMaxLimit, timeSeriesRawMaxRange, true)
	if err != nil {
		timeSeriesError(w, err, timeSeriesStatus(err))
		return
	}
	result, err := newTimeSeriesAPIRepository().QueryObservations(parsed.filter)
	if err != nil {
		timeSeriesError(w, err, http.StatusInternalServerError)
		return
	}
	items := publicTimeSeriesObservations(result.Observations, metric)
	timeSeriesJSON(w, http.StatusOK, TimeSeriesObservationListResponse{Items: items, Pagination: timeSeriesPage(parsed.limit, parsed.offset, len(items), result.HasMore)})
}

func timeSeriesDrilldownHandler(w http.ResponseWriter, r *http.Request) {
	if !timeSeriesRequireGET(w, r) {
		return
	}
	values := r.URL.Query()
	hash := strings.TrimSpace(values.Get("aggregate_hash"))
	if hash != "" && !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(hash) {
		timeSeriesError(w, fmt.Errorf("aggregate_hash must be a 64-character hexadecimal hash"), http.StatusBadRequest)
		return
	}
	repo := newTimeSeriesAPIRepository()
	var aggregate *cdb.TimeSeriesAggregate
	if hash != "" {
		var err error
		aggregate, err = repo.AggregateByHash(strings.ToLower(hash))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cdb.ErrTimeSeriesAggregateNotFound) || errors.Is(err, cdb.ErrTimeSeriesMetricNotFound) {
				status = http.StatusNotFound
			}
			timeSeriesError(w, err, status)
			return
		}
		values = cloneTimeSeriesValues(values)
		applyAggregateScope(values, aggregate)
	} else if strings.TrimSpace(values.Get("metric_id")) == "" && strings.TrimSpace(values.Get("metric_key")) == "" {
		timeSeriesError(w, fmt.Errorf("aggregate_hash or a complete aggregate scope with metric and from/to is required"), http.StatusBadRequest)
		return
	} else if values.Get("from") == "" || values.Get("to") == "" {
		timeSeriesError(w, fmt.Errorf("complete aggregate scope requires from and to"), http.StatusBadRequest)
		return
	}
	parsed, metric, err := parseTimeSeriesQuery(values, timeSeriesDrilldownMaxLimit, timeSeriesRawMaxRange, true)
	if err != nil {
		timeSeriesError(w, err, timeSeriesStatus(err))
		return
	}
	result, err := repo.QueryObservations(parsed.filter)
	if err != nil {
		timeSeriesError(w, err, http.StatusInternalServerError)
		return
	}
	response := TimeSeriesDrilldownResponse{AggregateHash: hash, Observations: publicTimeSeriesObservations(result.Observations, metric), Pagination: timeSeriesPage(parsed.limit, parsed.offset, len(result.Observations), result.HasMore)}
	if aggregate != nil {
		bucket := publicTimeSeriesAggregate(aggregate, metric, parsed.aggregate)
		response.Aggregate = &bucket
	}
	timeSeriesJSON(w, http.StatusOK, response)
}

func timeSeriesDimensionsHandler(w http.ResponseWriter, r *http.Request) {
	if !timeSeriesRequireGET(w, r) {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("dimension_key"))
	if !timeSeriesDimensionKeyRE.MatchString(key) {
		timeSeriesError(w, fmt.Errorf("dimension_key is required and must contain only letters, numbers, dot, underscore, or hyphen"), http.StatusBadRequest)
		return
	}
	comparisonValues := cloneTimeSeriesValues(r.URL.Query())
	comparisonValues.Set("limit", "10000")
	comparisonValues.Set("offset", "0")
	parsed, metric, err := parseTimeSeriesQuery(comparisonValues, 10000, timeSeriesAggregateMaxRange, false)
	if err != nil {
		timeSeriesError(w, err, timeSeriesStatus(err))
		return
	}
	if !metricAllowsDimension(metric, key) {
		timeSeriesError(w, fmt.Errorf("dimension %q is not defined for metric %q", key, metric.Key), http.StatusBadRequest)
		return
	}
	limit := config.TimeSeries.Cardinality.MaxValuesPerDimension
	if limit <= 0 {
		limit = timeSeriesDefaultCardinality
	}
	if limit > timeSeriesAbsoluteCardinality {
		limit = timeSeriesAbsoluteCardinality
	}
	result, err := newTimeSeriesAPIRepository().QueryAggregates(parsed.filter)
	if err != nil {
		timeSeriesError(w, err, http.StatusInternalServerError)
		return
	}
	if result.HasMore {
		timeSeriesError(w, fmt.Errorf("dimension comparison scope exceeds the maximum of 10000 aggregate rows"), http.StatusUnprocessableEntity)
		return
	}
	type dimensionGroup struct {
		value interface{}
		items []TimeSeriesAggregateBucket
	}
	groupsByCanonical := map[string]*dimensionGroup{}
	order := []string{}
	for i := range result.Aggregates {
		value, ok := result.Aggregates[i].Dimensions[key]
		if !ok || value == nil {
			continue
		}
		canonicalBytes, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			continue
		}
		canonical := string(canonicalBytes)
		group := groupsByCanonical[canonical]
		if group == nil {
			if len(order) >= limit {
				timeSeriesError(w, fmt.Errorf("dimension cardinality exceeds privacy limit of %d", limit), http.StatusUnprocessableEntity)
				return
			}
			group = &dimensionGroup{value: value}
			groupsByCanonical[canonical] = group
			order = append(order, canonical)
		}
		group.items = append(group.items, publicTimeSeriesAggregate(&result.Aggregates[i], metric, parsed.aggregate))
	}
	sort.Strings(order)
	groups := make([]TimeSeriesDimensionGroup, 0, len(order))
	for _, canonical := range order {
		group := groupsByCanonical[canonical]
		groups = append(groups, TimeSeriesDimensionGroup{DimensionValue: group.value, Buckets: group.items})
	}
	timeSeriesJSON(w, http.StatusOK, TimeSeriesDimensionComparisonResponse{DimensionKey: key, Groups: groups, Cardinality: len(groups), Limit: limit})
}

func parseTimeSeriesQuery(values url.Values, maxLimit int, maxRange time.Duration, raw bool) (timeSeriesParsedQuery, *cdb.TimeSeriesMetric, error) {
	limit, offset, err := parseTimeSeriesPagination(values, timeSeriesDefaultLimit, maxLimit)
	if err != nil {
		return timeSeriesParsedQuery{}, nil, err
	}
	filter := cdb.TimeSeriesQueryFilter{Dimensions: map[string]interface{}{}, Descending: parseTimeSeriesOrder(values.Get("order")), Pagination: cdb.TimeSeriesPagination{Limit: limit, Offset: offset}}
	idNames := []struct {
		name string
		dest **uint64
	}{
		{"metric_id", &filter.MetricID}, {"information_seed_id", &filter.InformationSeedID}, {"information_seed_candidate_id", &filter.InformationSeedCandidateID},
		{"source_id", &filter.SourceID}, {"source_information_seed_id", &filter.SourceInformationSeedID}, {"index_id", &filter.IndexID}, {"entity_id", &filter.EntityID},
		{"subject_id", &filter.SubjectID}, {"object_id", &filter.ObjectID}, {"correlation_rule_id", &filter.CorrelationRuleID},
		{"correlation_object_id_1", &filter.CorrelationObjectID1}, {"correlation_object_id_2", &filter.CorrelationObjectID2},
	}
	idAliases := map[string]string{"information_seed_id": "information_seed", "source_id": "source", "index_id": "index", "entity_id": "entity", "object_id": "object"}
	for _, field := range idNames {
		rawID := strings.TrimSpace(values.Get(field.name))
		if rawID == "" {
			rawID = strings.TrimSpace(values.Get(idAliases[field.name]))
		}
		if rawID != "" {
			id, parseErr := strconv.ParseUint(rawID, 10, 64)
			if parseErr != nil || id == 0 {
				return timeSeriesParsedQuery{}, nil, fmt.Errorf("%s must be a positive integer", field.name)
			}
			*field.dest = &id
		}
	}
	filter.MetricKey = strings.TrimSpace(values.Get("metric_key"))
	if filter.MetricID == nil && filter.MetricKey == "" {
		return timeSeriesParsedQuery{}, nil, fmt.Errorf("metric_id or metric_key is required")
	}
	if filter.MetricID != nil && filter.MetricKey != "" {
		return timeSeriesParsedQuery{}, nil, fmt.Errorf("use either metric_id or metric_key, not both")
	}
	filter.SubjectType = cleanTimeSeriesToken(values.Get("subject_type"))
	if subject := cleanTimeSeriesToken(values.Get("subject")); subject != "" {
		if filter.SubjectID == nil {
			if id, parseErr := strconv.ParseUint(subject, 10, 64); parseErr == nil && id > 0 {
				filter.SubjectID = &id
			} else {
				filter.SubjectText = subject
			}
		} else {
			filter.SubjectText = subject
		}
	}
	filter.ObjectType = cleanTimeSeriesToken(values.Get("object_type"))
	filter.CorrelationObjectType1 = cleanTimeSeriesToken(values.Get("correlation_object_type_1"))
	filter.CorrelationObjectType2 = cleanTimeSeriesToken(values.Get("correlation_object_type_2"))
	filter.Dimensions, err = parseTimeSeriesDimensions(values)
	if err != nil {
		return timeSeriesParsedQuery{}, nil, err
	}
	filter.Start, err = parseTimeSeriesDate(values.Get("from"), false)
	if err != nil {
		return timeSeriesParsedQuery{}, nil, fmt.Errorf("invalid from: %w", err)
	}
	filter.End, err = parseTimeSeriesDate(values.Get("to"), true)
	if err != nil {
		return timeSeriesParsedQuery{}, nil, fmt.Errorf("invalid to: %w", err)
	}
	if filter.Start != nil && filter.End != nil {
		if !filter.Start.Before(*filter.End) {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("from must be before to")
		}
		if filter.End.Sub(*filter.Start) > maxRange {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("requested range exceeds maximum of %s", maxRange)
		}
	}
	if raw && (filter.Start == nil || filter.End == nil) {
		return timeSeriesParsedQuery{}, nil, fmt.Errorf("raw observation queries require bounded from and to values")
	}
	if bucket := strings.TrimSpace(values.Get("bucket")); bucket != "" {
		filter.Bucket = cfg.TimeSeriesBucketInterval(bucket)
		if !validTimeSeriesBucket(filter.Bucket) {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("invalid bucket %q", bucket)
		}
	}
	if basis := strings.TrimSpace(values.Get("time_basis")); basis != "" {
		filter.TimeBasis = cfg.TimeSeriesTimeBasis(basis)
		if !validTimeSeriesTimeBasis(filter.TimeBasis) {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("invalid time_basis %q", basis)
		}
	}
	repo := newTimeSeriesAPIRepository()
	var metric *cdb.TimeSeriesMetric
	if filter.MetricID != nil {
		metric, err = repo.MetricByID(*filter.MetricID)
	} else {
		metric, err = repo.MetricByKey(filter.MetricKey)
	}
	if err != nil {
		return timeSeriesParsedQuery{}, nil, err
	}
	if filter.TimeBasis == "" {
		filter.TimeBasis = metric.TimeBasis
	}
	if filter.Bucket == "" {
		filter.Bucket = metric.Bucket
	}
	for key := range filter.Dimensions {
		if !metricAllowsDimension(metric, key) {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("dimension %q is not defined for metric %q", key, metric.Key)
		}
	}
	aggregate := metric.Aggregate
	if requested := strings.TrimSpace(values.Get("aggregate")); requested != "" {
		aggregate = cfg.TimeSeriesAggregate(requested)
		if !validTimeSeriesAggregate(aggregate) {
			return timeSeriesParsedQuery{}, nil, fmt.Errorf("invalid aggregate %q", requested)
		}
	}
	return timeSeriesParsedQuery{filter: filter, aggregate: aggregate, limit: limit, offset: offset}, metric, nil
}

func parseTimeSeriesPagination(values url.Values, defaultLimit, maxLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	var err error
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > maxLimit {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxLimit)
		}
	}
	if raw := strings.TrimSpace(values.Get("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

func parseTimeSeriesDate(raw string, endOfDate bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fmt.Errorf("must be RFC3339 or YYYY-MM-DD")
	}
	if endOfDate {
		parsed = parsed.Add(24 * time.Hour)
	}
	utc := parsed.UTC()
	return &utc, nil
}

func parseTimeSeriesDimensions(values url.Values) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	parts := append([]string{}, values["dimension"]...)
	for _, combined := range values["dimensions"] {
		parts = append(parts, strings.Split(combined, ",")...)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		separator := strings.Index(part, "=")
		if separator < 0 {
			separator = strings.Index(part, ":")
		}
		if separator <= 0 || separator == len(part)-1 {
			return nil, fmt.Errorf("dimension filters must use key=value syntax")
		}
		key := strings.TrimSpace(part[:separator])
		if !timeSeriesDimensionKeyRE.MatchString(key) {
			return nil, fmt.Errorf("invalid dimension key %q", key)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate dimension key %q", key)
		}
		rawValue := strings.TrimSpace(part[separator+1:])
		var value interface{}
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			value = rawValue
		}
		if _, composite := value.(map[string]interface{}); composite {
			return nil, fmt.Errorf("dimension %q must be a scalar value", key)
		}
		if _, composite := value.([]interface{}); composite {
			return nil, fmt.Errorf("dimension %q must be a scalar value", key)
		}
		result[key] = value
	}
	if config.TimeSeries.Cardinality.MaxDimensions > 0 && len(result) > config.TimeSeries.Cardinality.MaxDimensions {
		return nil, fmt.Errorf("dimension filter count exceeds configured maximum of %d", config.TimeSeries.Cardinality.MaxDimensions)
	}
	return result, nil
}

func publicTimeSeriesMetric(metric *cdb.TimeSeriesMetric) TimeSeriesMetricDefinition {
	return TimeSeriesMetricDefinition{ID: metric.ID, Key: metric.Key, DisplayName: metric.DisplayName, Description: metric.Description, SourceKind: metric.SourceKind, ValueType: metric.ValueType, DefaultAggregate: metric.Aggregate, Bucket: metric.Bucket, TimeBasis: metric.TimeBasis, Unit: metric.Unit, DimensionKeys: timeSeriesMetricDimensionKeys(metric), Enabled: metric.Enabled}
}

func publicTimeSeriesAggregate(row *cdb.TimeSeriesAggregate, metric *cdb.TimeSeriesMetric, aggregate cfg.TimeSeriesAggregate) TimeSeriesAggregateBucket {
	values := TimeSeriesAggregateValues{Count: row.ValueCount, OccurrenceTotal: row.OccurrenceTotal, DistinctCount: row.DistinctValueCount, NumericCount: row.NumericCount, Sum: row.NumericSum, Minimum: row.NumericMin, Maximum: row.NumericMax, Average: row.NumericAverage, P50: row.Percentile50, P75: row.Percentile75, P90: row.Percentile90, P95: row.Percentile95, P99: row.Percentile99, First: publicAggregateEdge(row.First, metric), Last: publicAggregateLast(row, metric), ChangeCount: row.ChangeCount}
	return TimeSeriesAggregateBucket{AggregateHash: row.AggregateHash, MetricID: row.MetricID, MetricKey: metric.Key, BucketStart: row.BucketStart.UTC().Format(time.RFC3339Nano), BucketEnd: row.BucketEnd.UTC().Format(time.RFC3339Nano), Aggregate: aggregate, Value: selectTimeSeriesAggregateValue(aggregate, values), Values: values, SampleCount: row.ValueCount, OccurrenceTotal: row.OccurrenceTotal, Dimensions: nonNilDimensions(row.Dimensions), Scope: publicTimeSeriesScope(row.Scope)}
}

func publicTimeSeriesObservations(rows []cdb.TimeSeriesObservation, metric *cdb.TimeSeriesMetric) []TimeSeriesObservationResponse {
	items := make([]TimeSeriesObservationResponse, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		item := TimeSeriesObservationResponse{ID: row.ID, MetricID: row.MetricID, MetricKey: metric.Key, ObservedAt: row.ObservedAt.UTC().Format(time.RFC3339Nano), CollectedAt: row.CollectedAt.UTC().Format(time.RFC3339Nano), BucketStart: row.BucketStart.UTC().Format(time.RFC3339Nano), BucketEnd: row.BucketEnd.UTC().Format(time.RFC3339Nano), Value: publicTimeSeriesValue(row.Value, metric), IsChanged: row.IsChanged, ChangeType: row.ChangeType, Dimensions: nonNilDimensions(row.Dimensions), Scope: publicTimeSeriesScope(row.Scope)}
		if row.EffectiveAt != nil {
			formatted := row.EffectiveAt.UTC().Format(time.RFC3339Nano)
			item.EffectiveAt = &formatted
		}
		items = append(items, item)
	}
	return items
}

func publicTimeSeriesValue(value cdb.TimeSeriesValue, metric *cdb.TimeSeriesMetric) interface{} {
	if value.Numeric != nil {
		return *value.Numeric
	}
	if value.Integer != nil {
		return *value.Integer
	}
	if value.Boolean != nil {
		return *value.Boolean
	}
	if value.Timestamp != nil {
		return value.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if metric.HashOnly || !metric.StoreValueText {
		return nil
	}
	if value.Text != nil && *value.Text != "[REDACTED]" {
		return *value.Text
	}
	if len(value.JSON) > 0 && string(value.JSON) != "null" {
		var decoded interface{}
		if json.Unmarshal(value.JSON, &decoded) == nil {
			return decoded
		}
	}
	return nil
}

func publicAggregateEdge(edge cdb.TimeSeriesAggregateEdge, metric *cdb.TimeSeriesMetric) interface{} {
	if edge.ValueNumeric != nil {
		return *edge.ValueNumeric
	}
	if metric.HashOnly || !metric.StoreValueText || edge.ValueText == nil || *edge.ValueText == "[REDACTED]" {
		return nil
	}
	return *edge.ValueText
}
func publicAggregateLast(row *cdb.TimeSeriesAggregate, metric *cdb.TimeSeriesMetric) interface{} {
	if value := publicAggregateEdge(row.Last, metric); value != nil {
		return value
	}
	if row.LastValueBoolean != nil {
		return *row.LastValueBoolean
	}
	if metric.HashOnly || !metric.StoreValueText || len(row.LastValueJSON) == 0 {
		return nil
	}
	var decoded interface{}
	if json.Unmarshal(row.LastValueJSON, &decoded) == nil {
		return decoded
	}
	return nil
}

func selectTimeSeriesAggregateValue(aggregate cfg.TimeSeriesAggregate, values TimeSeriesAggregateValues) interface{} {
	switch aggregate {
	case cfg.TimeSeriesAggregateCount:
		return values.Count
	case cfg.TimeSeriesAggregateSum:
		return values.Sum
	case cfg.TimeSeriesAggregateAverage:
		return values.Average
	case cfg.TimeSeriesAggregateMinimum:
		return values.Minimum
	case cfg.TimeSeriesAggregateMaximum:
		return values.Maximum
	case cfg.TimeSeriesAggregateDistinctCount:
		return values.DistinctCount
	case cfg.TimeSeriesAggregateFirst:
		return values.First
	case cfg.TimeSeriesAggregateLast:
		return values.Last
	case cfg.TimeSeriesAggregateP50:
		return values.P50
	case cfg.TimeSeriesAggregateP75:
		return values.P75
	case cfg.TimeSeriesAggregateP90:
		return values.P90
	case cfg.TimeSeriesAggregateP95:
		return values.P95
	case cfg.TimeSeriesAggregateP99:
		return values.P99
	default:
		return values.Count
	}
}

func publicTimeSeriesScope(scope cdb.TimeSeriesScope) TimeSeriesScopeResponse {
	return TimeSeriesScopeResponse{InformationSeedID: scope.InformationSeedID, InformationSeedCandidateID: scope.InformationSeedCandidateID, SourceID: scope.SourceID, SourceInformationSeedID: scope.SourceInformationSeedID, IndexID: scope.IndexID, EntityID: scope.EntityID, SubjectType: scope.SubjectType, SubjectID: scope.SubjectID, ObjectType: scope.ObjectType, ObjectID: scope.ObjectID, CorrelationRuleID: scope.CorrelationRuleID, CorrelationObjectType1: scope.CorrelationObjectType1, CorrelationObjectID1: scope.CorrelationObjectID1, CorrelationObjectType2: scope.CorrelationObjectType2, CorrelationObjectID2: scope.CorrelationObjectID2}
}

func timeSeriesMetricDimensionKeys(metric *cdb.TimeSeriesMetric) []string {
	if len(metric.Dimensions) == 0 {
		return []string{}
	}
	var definitions []struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(metric.Dimensions, &definitions) != nil {
		return []string{}
	}
	keys := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if timeSeriesDimensionKeyRE.MatchString(definition.Key) {
			keys = append(keys, definition.Key)
		}
	}
	sort.Strings(keys)
	return keys
}
func metricAllowsDimension(metric *cdb.TimeSeriesMetric, key string) bool {
	for _, allowed := range timeSeriesMetricDimensionKeys(metric) {
		if key == allowed {
			return true
		}
	}
	return false
}

func applyAggregateScope(values url.Values, aggregate *cdb.TimeSeriesAggregate) {
	values.Set("metric_id", strconv.FormatUint(aggregate.MetricID, 10))
	values.Del("metric_key")
	values.Set("from", aggregate.BucketStart.UTC().Format(time.RFC3339Nano))
	values.Set("to", aggregate.BucketEnd.UTC().Format(time.RFC3339Nano))
	setTimeSeriesScopeValue(values, "information_seed_id", aggregate.Scope.InformationSeedID)
	setTimeSeriesScopeValue(values, "information_seed_candidate_id", aggregate.Scope.InformationSeedCandidateID)
	setTimeSeriesScopeValue(values, "source_id", aggregate.Scope.SourceID)
	setTimeSeriesScopeValue(values, "source_information_seed_id", aggregate.Scope.SourceInformationSeedID)
	setTimeSeriesScopeValue(values, "index_id", aggregate.Scope.IndexID)
	setTimeSeriesScopeValue(values, "entity_id", aggregate.Scope.EntityID)
	setTimeSeriesScopeValue(values, "subject_id", aggregate.Scope.SubjectID)
	setTimeSeriesScopeValue(values, "object_id", aggregate.Scope.ObjectID)
	setTimeSeriesScopeValue(values, "correlation_rule_id", aggregate.Scope.CorrelationRuleID)
	setTimeSeriesScopeValue(values, "correlation_object_id_1", aggregate.Scope.CorrelationObjectID1)
	setTimeSeriesScopeValue(values, "correlation_object_id_2", aggregate.Scope.CorrelationObjectID2)
	values.Del("subject_type")
	values.Del("subject")
	values.Del("object_type")
	values.Del("correlation_object_type_1")
	values.Del("correlation_object_type_2")
	if aggregate.Scope.SubjectType != "" {
		values.Set("subject_type", aggregate.Scope.SubjectType)
	}
	if aggregate.Scope.SubjectText != "" {
		values.Set("subject", aggregate.Scope.SubjectText)
	}
	if aggregate.Scope.ObjectType != "" {
		values.Set("object_type", aggregate.Scope.ObjectType)
	}
	if aggregate.Scope.CorrelationObjectType1 != "" {
		values.Set("correlation_object_type_1", aggregate.Scope.CorrelationObjectType1)
	}
	if aggregate.Scope.CorrelationObjectType2 != "" {
		values.Set("correlation_object_type_2", aggregate.Scope.CorrelationObjectType2)
	}
	values.Del("dimension")
	values.Del("dimensions")
	keys := make([]string, 0, len(aggregate.Dimensions))
	for key := range aggregate.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		encoded, _ := json.Marshal(aggregate.Dimensions[key])
		values.Add("dimension", key+"="+string(encoded))
	}
}

func validTimeSeriesBucket(value cfg.TimeSeriesBucketInterval) bool {
	switch value {
	case cfg.TimeSeriesBucketNone, cfg.TimeSeriesBucketOneMinute, cfg.TimeSeriesBucketFiveMinutes, cfg.TimeSeriesBucketFifteenMinutes, cfg.TimeSeriesBucketOneHour, cfg.TimeSeriesBucketOneDay, cfg.TimeSeriesBucketOneWeek, cfg.TimeSeriesBucketOneMonth:
		return true
	}
	return false
}
func validTimeSeriesTimeBasis(value cfg.TimeSeriesTimeBasis) bool {
	switch value {
	case cfg.TimeSeriesTimeObservedAt, cfg.TimeSeriesTimeEventAt, cfg.TimeSeriesTimeSourceTimestamp:
		return true
	}
	return false
}
func validTimeSeriesAggregate(value cfg.TimeSeriesAggregate) bool {
	switch value {
	case cfg.TimeSeriesAggregateCount, cfg.TimeSeriesAggregateSum, cfg.TimeSeriesAggregateAverage, cfg.TimeSeriesAggregateMinimum, cfg.TimeSeriesAggregateMaximum, cfg.TimeSeriesAggregateDistinctCount, cfg.TimeSeriesAggregateFirst, cfg.TimeSeriesAggregateLast, cfg.TimeSeriesAggregateP50, cfg.TimeSeriesAggregateP75, cfg.TimeSeriesAggregateP90, cfg.TimeSeriesAggregateP95, cfg.TimeSeriesAggregateP99:
		return true
	}
	return false
}

func timeSeriesRequireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	timeSeriesError(w, fmt.Errorf("method must be GET"), http.StatusMethodNotAllowed)
	return false
}
func timeSeriesStatus(err error) int {
	if errors.Is(err, cdb.ErrTimeSeriesMetricNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
func timeSeriesError(w http.ResponseWriter, err error, status int) {
	handleErrorAndRespond(w, err, nil, "Invalid time-series request: %v", status, http.StatusOK)
}
func timeSeriesJSON(w http.ResponseWriter, status int, value interface{}) {
	handleErrorAndRespond(w, nil, value, "", http.StatusInternalServerError, status)
}
func timeSeriesPage(limit, offset, count int, hasMore bool) TimeSeriesPaginationResponse {
	return TimeSeriesPaginationResponse{Limit: limit, Offset: offset, Count: count, HasMore: hasMore}
}
func parseTimeSeriesOrder(raw string) bool   { return strings.EqualFold(strings.TrimSpace(raw), "desc") }
func cleanTimeSeriesToken(raw string) string { return strings.TrimSpace(raw) }
func nonNilDimensions(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	return value
}
func cloneTimeSeriesValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, entries := range values {
		clone[key] = append([]string{}, entries...)
	}
	return clone
}
func setTimeSeriesScopeValue(values url.Values, key string, value *uint64) {
	if value == nil {
		values.Del(key)
		return
	}
	values.Set(key, strconv.FormatUint(*value, 10))
}
