// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const timeSeriesMetricColumns = `metric_id, metric_key, display_name, description, source_kind,
	value_type, aggregate, bucket, time_basis, dedupe_scope, object_type, failure_policy,
	selector, dimensions, retention_policy, cardinality_policy, unit, enabled,
	store_value_text, hash_only, created_at, deleted_at, last_updated_at`

func validateTimeSeriesDB(db *Handler) (string, error) {
	if db == nil || *db == nil {
		return "", fmt.Errorf("database handler is nil")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return "", fmt.Errorf("unsupported database type for time-series operations: %s", (*db).DBMS())
	}
	return dbms, nil
}

func normalizeMetricJSON(raw json.RawMessage, required bool) (interface{}, error) {
	if len(raw) == 0 {
		if required {
			return "{}", nil
		}
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("metric JSON field is invalid")
	}
	canonical, err := CanonicalTimeSeriesJSON(raw)
	if err != nil {
		return nil, err
	}
	return string(canonical), nil
}

// UpsertTimeSeriesMetric creates or updates a metric definition. Existing
// value_type is immutable: callers must create a new metric key to change it.
func UpsertTimeSeriesMetric(db *Handler, metric *TimeSeriesMetric) (*TimeSeriesMetric, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if metric == nil {
		return nil, fmt.Errorf("time-series metric is nil")
	}
	metric.Key = strings.TrimSpace(metric.Key)
	if metric.Key == "" {
		return nil, fmt.Errorf("time-series metric key is required")
	}
	if metric.ValueType == "" {
		return nil, fmt.Errorf("time-series metric value type is required")
	}
	if metric.DisplayName == "" {
		metric.DisplayName = metric.Key
	}
	if existing, lookupErr := GetTimeSeriesMetricByKey(db, metric.Key); lookupErr == nil {
		if existing.ValueType != metric.ValueType {
			return nil, fmt.Errorf("%w: metric %q is %q, requested %q", ErrTimeSeriesMetricTypeConflict, metric.Key, existing.ValueType, metric.ValueType)
		}
	} else if !errors.Is(lookupErr, ErrTimeSeriesMetricNotFound) {
		return nil, lookupErr
	}
	selector, err := normalizeMetricJSON(metric.Selector, true)
	if err != nil {
		return nil, err
	}
	dimensions, err := normalizeMetricJSON(metric.Dimensions, false)
	if err != nil {
		return nil, err
	}
	retention, err := normalizeMetricJSON(metric.RetentionPolicy, false)
	if err != nil {
		return nil, err
	}
	cardinality, err := normalizeMetricJSON(metric.CardinalityPolicy, false)
	if err != nil {
		return nil, err
	}
	args := []interface{}{metric.Key, metric.DisplayName, metric.Description, string(metric.SourceKind), string(metric.ValueType), string(metric.Aggregate), string(metric.Bucket), string(metric.TimeBasis), string(metric.DedupeScope), string(metric.ObjectType), string(metric.FailurePolicy), selector, dimensions, retention, cardinality, nullableString(metric.Unit), metric.Enabled, metric.StoreValueText, metric.HashOnly}
	p := newInformationSeedPlaceholders(dbms)
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	if dbms == DBPostgresStr {
		for _, idx := range []int{11, 12, 13, 14} {
			if args[idx] != nil {
				values[idx] += "::jsonb"
			}
		}
	}
	base := `INSERT INTO TimeSeriesMetrics (metric_key, display_name, description, source_kind, value_type,
		aggregate, bucket, time_basis, dedupe_scope, object_type, failure_policy, selector,
		dimensions, retention_policy, cardinality_policy, unit, enabled, store_value_text, hash_only)
		VALUES (` + strings.Join(values, ", ") + `)`
	updates := `display_name=excluded.display_name, description=excluded.description, source_kind=excluded.source_kind,
		aggregate=excluded.aggregate, bucket=excluded.bucket, time_basis=excluded.time_basis,
		dedupe_scope=excluded.dedupe_scope, object_type=excluded.object_type, failure_policy=excluded.failure_policy,
		selector=excluded.selector, dimensions=excluded.dimensions, retention_policy=excluded.retention_policy,
		cardinality_policy=excluded.cardinality_policy, unit=excluded.unit, enabled=excluded.enabled,
		store_value_text=excluded.store_value_text, hash_only=excluded.hash_only, deleted_at=NULL, last_updated_at=CURRENT_TIMESTAMP`
	if dbms == DBMySQLStr {
		mysqlUpdates := `display_name=VALUES(display_name), description=VALUES(description), source_kind=VALUES(source_kind),
			aggregate=VALUES(aggregate), bucket=VALUES(bucket), time_basis=VALUES(time_basis),
			dedupe_scope=VALUES(dedupe_scope), object_type=VALUES(object_type), failure_policy=VALUES(failure_policy),
			selector=VALUES(selector), dimensions=VALUES(dimensions), retention_policy=VALUES(retention_policy),
			cardinality_policy=VALUES(cardinality_policy), unit=VALUES(unit), enabled=VALUES(enabled),
			store_value_text=VALUES(store_value_text), hash_only=VALUES(hash_only), deleted_at=NULL, last_updated_at=CURRENT_TIMESTAMP`
		_, err = (*db).Exec(base+` ON DUPLICATE KEY UPDATE `+mysqlUpdates, args...)
	} else {
		_, err = (*db).Exec(base+` ON CONFLICT (metric_key) DO UPDATE SET `+updates, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("upsert time-series metric %q: %w", metric.Key, err)
	}
	stored, err := GetTimeSeriesMetricByKey(db, metric.Key)
	if err != nil {
		return nil, err
	}
	if stored.ValueType != metric.ValueType {
		return nil, fmt.Errorf("%w: metric %q is %q, requested %q", ErrTimeSeriesMetricTypeConflict, metric.Key, stored.ValueType, metric.ValueType)
	}
	return stored, nil
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

// GetTimeSeriesMetricByID retrieves an active metric by ID.
func GetTimeSeriesMetricByID(db *Handler, id uint64) (*TimeSeriesMetric, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, fmt.Errorf("time-series metric ID is required")
	}
	return queryTimeSeriesMetric((*db).QueryRow(`SELECT `+timeSeriesMetricColumns+` FROM TimeSeriesMetrics WHERE metric_id = `+informationSeedPlaceholderForDBMS(dbms, 1)+` AND deleted_at IS NULL`, id))
}

// GetTimeSeriesMetricByKey retrieves an active metric by stable key.
func GetTimeSeriesMetricByKey(db *Handler, key string) (*TimeSeriesMetric, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("time-series metric key is required")
	}
	return queryTimeSeriesMetric((*db).QueryRow(`SELECT `+timeSeriesMetricColumns+` FROM TimeSeriesMetrics WHERE metric_key = `+informationSeedPlaceholderForDBMS(dbms, 1)+` AND deleted_at IS NULL`, key))
}

func queryTimeSeriesMetric(row *sql.Row) (*TimeSeriesMetric, error) {
	metric, err := scanTimeSeriesMetric(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTimeSeriesMetricNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query time-series metric: %w", err)
	}
	return metric, nil
}

func scanTimeSeriesMetric(scan func(...interface{}) error) (*TimeSeriesMetric, error) {
	var m TimeSeriesMetric
	var sourceKind, valueType, aggregate, bucket, timeBasis, dedupeScope, objectType, failurePolicy string
	var description, dimensions, retention, cardinality, unit sql.NullString
	var selector string
	var deleted sql.NullTime
	err := scan(&m.ID, &m.Key, &m.DisplayName, &description, &sourceKind, &valueType, &aggregate, &bucket, &timeBasis, &dedupeScope, &objectType, &failurePolicy, &selector, &dimensions, &retention, &cardinality, &unit, &m.Enabled, &m.StoreValueText, &m.HashOnly, &m.CreatedAt, &deleted, &m.LastUpdatedAt)
	if err != nil {
		return nil, err
	}
	m.Description = description.String
	m.SourceKind = cfg.TimeSeriesSourceKind(sourceKind)
	m.ValueType = cfg.TimeSeriesValueType(valueType)
	m.Aggregate = cfg.TimeSeriesAggregate(aggregate)
	m.Bucket = cfg.TimeSeriesBucketInterval(bucket)
	m.TimeBasis = cfg.TimeSeriesTimeBasis(timeBasis)
	m.DedupeScope = cfg.TimeSeriesDedupeScope(dedupeScope)
	m.ObjectType = cfg.TimeSeriesObjectType(objectType)
	m.FailurePolicy = cfg.TimeSeriesFailurePolicy(failurePolicy)
	m.Selector = json.RawMessage(selector)
	m.Unit = unit.String
	if dimensions.Valid {
		m.Dimensions = json.RawMessage(dimensions.String)
	}
	if retention.Valid {
		m.RetentionPolicy = json.RawMessage(retention.String)
	}
	if cardinality.Valid {
		m.CardinalityPolicy = json.RawMessage(cardinality.String)
	}
	if deleted.Valid {
		m.DeletedAt = &deleted.Time
	}
	return &m, nil
}

// ListTimeSeriesMetrics lists metric definitions in stable ID order.
func ListTimeSeriesMetrics(db *Handler, filter TimeSeriesMetricFilter) ([]TimeSeriesMetric, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	p := newInformationSeedPlaceholders(dbms)
	conditions := []string{"1=1"}
	args := []interface{}{}
	if !filter.IncludeDeleted {
		conditions = append(conditions, "deleted_at IS NULL")
	}
	if filter.Key != "" {
		conditions = append(conditions, "metric_key = "+p.Next())
		args = append(args, filter.Key)
	}
	if filter.SourceKind != "" {
		conditions = append(conditions, "source_kind = "+p.Next())
		args = append(args, string(filter.SourceKind))
	}
	if filter.ValueType != "" {
		conditions = append(conditions, "value_type = "+p.Next())
		args = append(args, string(filter.ValueType))
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = "+p.Next())
		args = append(args, *filter.Enabled)
	}
	limit, offset, err := normalizeTimeSeriesPagination(filter.Pagination)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + timeSeriesMetricColumns + ` FROM TimeSeriesMetrics WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY metric_id ASC LIMIT ` + p.Next() + ` OFFSET ` + p.Next()
	args = append(args, limit, offset)
	rows, err := (*db).ExecuteQuery(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list time-series metrics: %w", err)
	}
	defer rows.Close()
	metrics := []TimeSeriesMetric{}
	for rows.Next() {
		m, e := scanTimeSeriesMetric(rows.Scan)
		if e != nil {
			return nil, e
		}
		metrics = append(metrics, *m)
	}
	return metrics, rows.Err()
}

func normalizeTimeSeriesPagination(p TimeSeriesPagination) (int, int, error) {
	if p.Limit < 0 || p.Offset < 0 {
		return 0, 0, fmt.Errorf("time-series pagination values cannot be negative")
	}
	limit := p.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	return limit, p.Offset, nil
}
