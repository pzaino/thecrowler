// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// informationSeedObservationEvent is the persisted, user-data fact supplied to
// information-seed metric selectors. It deliberately contains no scheduler,
// queue, provider-latency, or process-health fields.
type informationSeedObservationEvent struct {
	SourceKind cfg.TimeSeriesSourceKind
	Event      string
	Identity   string
	ObservedAt time.Time
	Scope      TimeSeriesScope
	Fields     map[string]interface{}
	Provenance map[string]interface{}
}

// emitInformationSeedObservationsTx matches enabled metric definitions and
// writes observations in the same transaction as the durable lifecycle fact.
// Thus a failed commit cannot leave an observation behind and retries converge
// through a stable identity/value dedupe key.
func emitInformationSeedObservationsTx(tx *sql.Tx, dbms string, event informationSeedObservationEvent) error {
	repo := TransactionTimeSeriesRepository{Tx: tx, DBMS: dbms}
	enabled := true
	metrics, err := repo.ListMetrics(TimeSeriesMetricFilter{SourceKind: event.SourceKind, Enabled: &enabled, Pagination: TimeSeriesPagination{Limit: 10000}})
	if err != nil {
		// Older/test schemas may intentionally predate time-series storage.
		if isMissingTimeSeriesTableError(err) {
			return nil
		}
		return err
	}
	for i := range metrics {
		metric := metrics[i]
		if !metric.Enabled || metric.SourceKind != event.SourceKind {
			continue
		}
		if err = emitInformationSeedMetricTx(repo, metric, event); err != nil {
			if errors.Is(err, ErrTimeSeriesValueRejected) || metric.FailurePolicy == cfg.TimeSeriesFailureSkip {
				continue
			}
			if metric.FailurePolicy == cfg.TimeSeriesFailureFailIndexing || metric.FailurePolicy == cfg.TimeSeriesFailureRetry {
				return fmt.Errorf("emit information-seed metric %q: %w", metric.Key, err)
			}
			// log/log_skip are intentionally best-effort in this persistence layer;
			// callers have no logger contract and the user-data write must proceed.
		}
	}
	return nil
}

func emitInformationSeedMetricTx(repo TransactionTimeSeriesRepository, metric TimeSeriesMetric, event informationSeedObservationEvent) error {
	selector, err := decodeTimeSeriesMap(metric.Selector)
	if err != nil {
		return err
	}
	if !informationSeedSelectorMatches(selector, event) {
		return nil
	}
	selected, ok := informationSeedSelectedValue(selector, event.Fields)
	if !ok {
		return nil
	}
	value, err := informationSeedTimeSeriesValue(metric.ValueType, selected)
	if err != nil {
		return err
	}
	dimensions, err := informationSeedDimensions(metric.Dimensions, event.Fields)
	if err != nil {
		return err
	}
	observedAt := event.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	bucketStart, bucketEnd, err := TimeSeriesBucketBounds(observedAt, metric.Bucket)
	if err != nil {
		return err
	}
	observation := TimeSeriesObservation{
		MetricID: metric.ID, ObservedAt: observedAt, CollectedAt: time.Now().UTC(),
		BucketStart: bucketStart, BucketEnd: bucketEnd, Scope: event.Scope,
		Value: value, Dimensions: dimensions,
	}
	policy := TimeSeriesPreparationPolicy{StoreValueText: metric.StoreValueText, HashOnly: metric.HashOnly, MaxValueLength: 512}
	prepared, err := PrepareTimeSeriesObservation(observation, metric.ValueType, policy)
	if err != nil {
		return err
	}
	observation = prepared.Observation
	previous, previousErr := repo.PreviousObservation(TimeSeriesChangeLookup{MetricID: metric.ID, Scope: event.Scope, Dimensions: dimensions, Before: observedAt, TimeBasis: metric.TimeBasis})
	if previousErr != nil && !errors.Is(previousErr, ErrTimeSeriesObservationNotFound) {
		return previousErr
	}
	applyInformationSeedObservationChange(&observation, previous, previousErr, observedAt)
	observation.DedupeKey = informationSeedObservationDedupeKey(metric.ID, event.Identity, observation.ValueHash, dimensions)
	provenance := cloneInformationSeedMap(event.Provenance)
	provenance["source_kind"] = string(event.SourceKind)
	provenance["lifecycle_event"] = event.Event
	provenance["transition_identity"] = event.Identity
	provenance["metric_selector"] = selector
	observation.Provenance, err = CanonicalTimeSeriesJSON(provenance)
	if err != nil {
		return err
	}
	_, err = repo.InsertObservation(&observation)
	return err
}

func informationSeedSelectorMatches(selector map[string]interface{}, event informationSeedObservationEvent) bool {
	if expected := normalizedSelectorString(selector["event"]); expected != "" && expected != strings.ToLower(event.Event) {
		return false
	}
	if expected := normalizedSelectorString(selector["transition"]); expected != "" && expected != strings.ToLower(event.Event) {
		return false
	}
	if where, ok := selector["where"].(map[string]interface{}); ok {
		for key, expected := range where {
			actual, found := lookupInformationSeedField(event.Fields, key)
			if !found || normalizedSelectorString(actual) != normalizedSelectorString(expected) {
				return false
			}
		}
	}
	// Direct lifecycle constraints are convenient for concise metric configs.
	for _, key := range []string{"status", "decision_status", "provider", "rejection_reason", "reason"} {
		if expected := normalizedSelectorString(selector[key]); expected != "" {
			actual, found := lookupInformationSeedField(event.Fields, key)
			if !found || normalizedSelectorString(actual) != expected {
				return false
			}
		}
	}
	return true
}

func informationSeedSelectedValue(selector, fields map[string]interface{}) (interface{}, bool) {
	path := strings.TrimSpace(fmt.Sprint(selector["field"]))
	if path == "" || path == "<nil>" {
		path = strings.TrimSpace(fmt.Sprint(selector["path"]))
	}
	if path == "" || path == "<nil>" {
		if configured, exists := selector["value"]; exists {
			if name, isString := configured.(string); isString {
				if value, found := lookupInformationSeedField(fields, name); found {
					return value, true
				}
			}
			return configured, true
		}
		return int64(1), true
	}
	return lookupInformationSeedField(fields, path)
}

func informationSeedDimensions(raw json.RawMessage, fields map[string]interface{}) (map[string]interface{}, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]interface{}{}, nil
	}
	var definitions []cfg.TimeSeriesDimensionConfig
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, fmt.Errorf("decode information-seed dimensions: %w", err)
	}
	result := make(map[string]interface{}, len(definitions))
	for _, definition := range definitions {
		key := strings.TrimSpace(definition.Key)
		if key == "" {
			continue
		}
		path := strings.TrimSpace(fmt.Sprint(definition.Selector["field"]))
		if path == "" || path == "<nil>" {
			path = strings.TrimSpace(fmt.Sprint(definition.Selector["path"]))
		}
		var value interface{}
		var ok bool
		if path != "" && path != "<nil>" {
			value, ok = lookupInformationSeedField(fields, path)
		} else if fixed, exists := definition.Selector["value"]; exists {
			value, ok = fixed, true
		}
		if ok {
			result[key] = boundedInformationSeedDimension(value)
		}
	}
	return result, nil
}

func lookupInformationSeedField(fields map[string]interface{}, path string) (interface{}, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	current := interface{}(fields)
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func informationSeedTimeSeriesValue(valueType cfg.TimeSeriesValueType, selected interface{}) (TimeSeriesValue, error) {
	text := strings.TrimSpace(fmt.Sprint(selected))
	switch valueType {
	case cfg.TimeSeriesValueCount:
		value := int64(1)
		return TimeSeriesValue{Integer: &value}, nil
	case cfg.TimeSeriesValueInteger:
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return TimeSeriesValue{}, err
		}
		return TimeSeriesValue{Integer: &value}, nil
	case cfg.TimeSeriesValueDecimal, cfg.TimeSeriesValueDuration:
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return TimeSeriesValue{}, err
		}
		return TimeSeriesValue{Numeric: &value}, nil
	case cfg.TimeSeriesValueBoolean:
		value, err := strconv.ParseBool(strings.ToLower(text))
		if err != nil {
			return TimeSeriesValue{}, err
		}
		return TimeSeriesValue{Boolean: &value}, nil
	case cfg.TimeSeriesValueString:
		value := fmt.Sprint(selected)
		return TimeSeriesValue{Text: &value}, nil
	case cfg.TimeSeriesValueJSON:
		value, err := json.Marshal(selected)
		if err != nil {
			return TimeSeriesValue{}, err
		}
		return TimeSeriesValue{JSON: value}, nil
	case cfg.TimeSeriesValueTimestamp:
		value, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return TimeSeriesValue{}, err
		}
		value = value.UTC()
		return TimeSeriesValue{Timestamp: &value}, nil
	default:
		return TimeSeriesValue{}, fmt.Errorf("unsupported information-seed metric value type %q", valueType)
	}
}

func applyInformationSeedObservationChange(observation *TimeSeriesObservation, previous *TimeSeriesObservation, err error, at time.Time) {
	if errors.Is(err, ErrTimeSeriesObservationNotFound) || previous == nil {
		observation.IsChanged = true
		observation.ChangeType = "new"
		observation.ChangeDetectedAt = &at
		return
	}
	observation.PreviousObservationID = &previous.ID
	observation.PreviousValueHash = previous.ValueHash
	observation.IsChanged = previous.ValueHash != observation.ValueHash
	if observation.IsChanged {
		observation.ChangeType = "changed"
		observation.ChangeDetectedAt = &at
	} else {
		observation.ChangeType = "unchanged"
	}
	if previous.Value.Numeric != nil && observation.Value.Numeric != nil {
		delta := *observation.Value.Numeric - *previous.Value.Numeric
		observation.ChangeDeltaNumeric = &delta
	}
}

func informationSeedObservationDedupeKey(metricID uint64, identity, valueHash string, dimensions map[string]interface{}) string {
	dimensionJSON, _ := CanonicalTimeSeriesJSON(dimensions)
	hash := sha256.Sum256([]byte(fmt.Sprintf("information-seed\x00%d\x00%s\x00%s\x00%s", metricID, identity, valueHash, dimensionJSON)))
	return hex.EncodeToString(hash[:])
}

func boundedInformationSeedDimension(value interface{}) interface{} {
	text, ok := value.(string)
	if !ok {
		return value
	}
	text = strings.TrimSpace(text)
	if len(text) > 120 {
		hash := sha256.Sum256([]byte(text))
		return "sha256:" + hex.EncodeToString(hash[:])
	}
	return text
}

func normalizedSelectorString(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
}

func decodeTimeSeriesMap(raw json.RawMessage) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if len(raw) == 0 || string(raw) == "null" {
		return result, nil
	}
	return result, json.Unmarshal(raw, &result)
}

func cloneInformationSeedMap(input map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(input)+4)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func isMissingTimeSeriesTableError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeseriesmetrics") && (strings.Contains(message, "no such table") || strings.Contains(message, "doesn't exist") || strings.Contains(message, "does not exist"))
}
