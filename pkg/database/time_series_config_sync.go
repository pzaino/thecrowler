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
	for i := range ts.Metrics {
		metric, err := configuredTimeSeriesMetric(ts, ts.Metrics[i])
		if err != nil {
			return fmt.Errorf("configure time-series metric %q: %w", ts.Metrics[i].Key, err)
		}
		if _, err = UpsertTimeSeriesMetric(db, metric); err != nil {
			return err
		}
	}
	return nil
}

func configuredTimeSeriesMetric(ts cfg.TimeSeriesConfig, configured cfg.TimeSeriesMetricConfig) (*TimeSeriesMetric, error) {
	selector, err := marshalTimeSeriesConfigJSON(configured.Selector)
	if err != nil {
		return nil, fmt.Errorf("selector: %w", err)
	}
	dimensions, err := marshalTimeSeriesConfigJSON(configured.Dimensions)
	if err != nil {
		return nil, fmt.Errorf("dimensions: %w", err)
	}
	cardinality, err := marshalTimeSeriesConfigJSON(configured.Cardinality)
	if err != nil {
		return nil, fmt.Errorf("cardinality: %w", err)
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
