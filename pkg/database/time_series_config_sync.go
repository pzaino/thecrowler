// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"encoding/json"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// SyncConfiguredTimeSeriesMetrics upserts enabled and disabled time-series
// metric declarations from the runtime configuration into TimeSeriesMetrics.
// Emitters load definitions from the database, so this bridges declarative YAML
// configuration with the persistence-backed emission path used by crawlers.
func SyncConfiguredTimeSeriesMetrics(db *Handler, ts cfg.TimeSeriesConfig) error {
	if !ts.Enabled || len(ts.Metrics) == 0 {
		return nil
	}
	normalized := cfg.NormalizeTimeSeriesConfig(ts)
	if err := normalized.Validate(); err != nil {
		return fmt.Errorf("validate time-series configuration before synchronization: %w", err)
	}
	for i := range normalized.Metrics {
		metric, err := configuredTimeSeriesMetric(normalized, normalized.Metrics[i])
		if err != nil {
			return fmt.Errorf("configure time-series metric %q: %w", normalized.Metrics[i].Key, err)
		}
		if _, err = UpsertTimeSeriesMetric(db, metric); err != nil {
			return err
		}
	}
	return nil
}

func configuredTimeSeriesMetric(ts cfg.TimeSeriesConfig, configured cfg.TimeSeriesMetricConfig) (*TimeSeriesMetric, error) {
	ts = cfg.NormalizeTimeSeriesConfig(ts)
	selector, err := marshalTimeSeriesConfigJSON(configured.Selector)
	if err != nil {
		return nil, fmt.Errorf("selector: %w", err)
	}
	dimensions, err := marshalTimeSeriesConfigJSON(configured.Dimensions)
	if err != nil {
		return nil, fmt.Errorf("dimensions: %w", err)
	}
	// Persist the effective policy rather than NULL. Emitters and retention do
	// not have access to the process configuration, and an explicit object keeps
	// inheritance decisions reviewable in the database. A NULL policy remains
	// supported for metrics created before 2.3.1 or registered directly through
	// UpsertTimeSeriesMetric.
	cardinalityPolicy := ts.Cardinality
	if configured.Cardinality != nil {
		cardinalityPolicy = *configured.Cardinality
	}
	if cardinalityPolicy.MaxDimensions == 0 {
		// For configured metrics 0 means "inherit the built-in default", never
		// "unlimited". Runtime emitters still treat a legacy NULL/zero policy as
		// no dimension cap, but synchronization always writes a concrete value.
		cardinalityPolicy.MaxDimensions = ts.Cardinality.MaxDimensions
	}
	cardinality, err := marshalTimeSeriesConfigJSON(cardinalityPolicy)
	if err != nil {
		return nil, fmt.Errorf("cardinality: %w", err)
	}
	rawRetention := strings.TrimSpace(ts.Retention.Raw)
	aggregateRetention := strings.TrimSpace(ts.Retention.Aggregated)
	if configured.Retention != nil {
		if value := strings.TrimSpace(configured.Retention.Raw); value != "" {
			rawRetention = value
		}
		if value := strings.TrimSpace(configured.Retention.Aggregated); value != "" {
			aggregateRetention = value
		}
	}
	retention, err := marshalTimeSeriesConfigJSON(timeSeriesStoredRetentionPolicy{Raw: rawRetention, Aggregated: aggregateRetention})
	if err != nil {
		return nil, fmt.Errorf("retention: %w", err)
	}
	privacy := ts.Privacy
	if configured.Privacy != nil {
		privacy = *configured.Privacy
	}
	valueType := configured.ValueType
	if valueType == "" {
		valueType = ts.Defaults.ValueType
	}
	aggregate := cfg.TimeSeriesAggregateCount
	aggregates := configured.Aggregates
	if len(aggregates) == 0 {
		aggregates = ts.Defaults.Aggregates
	}
	if len(aggregates) > 0 && aggregates[0] != "" {
		aggregate = aggregates[0]
	}
	bucket := configured.BucketInterval
	if bucket == "" {
		bucket = ts.Defaults.BucketInterval
	}
	timeBasis := configured.TimeBasis
	if timeBasis == "" {
		timeBasis = ts.Defaults.TimeBasis
	}
	dedupeScope := configured.DedupeScope
	if dedupeScope == "" {
		dedupeScope = ts.Defaults.DedupeScope
	}
	failurePolicy := configured.FailurePolicy
	if failurePolicy == "" {
		failurePolicy = ts.Defaults.FailurePolicy
	}
	return &TimeSeriesMetric{
		Key:               strings.TrimSpace(configured.Key),
		DisplayName:       strings.TrimSpace(configured.Key),
		Description:       configured.Description,
		SourceKind:        configured.SourceKind,
		ValueType:         valueType,
		Aggregate:         aggregate,
		Bucket:            bucket,
		TimeBasis:         timeBasis,
		DedupeScope:       dedupeScope,
		ObjectType:        configured.ObjectType,
		FailurePolicy:     failurePolicy,
		Selector:          selector,
		Dimensions:        dimensions,
		RetentionPolicy:   retention,
		CardinalityPolicy: cardinality,
		Unit:              configured.Unit,
		Enabled:           configured.Enabled,
		StoreValueText:    privacy.StoreValueText,
		HashOnly:          privacy.HashOnly,
	}, nil
}

func marshalTimeSeriesConfigJSON(value interface{}) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	return json.RawMessage(raw), nil
}
