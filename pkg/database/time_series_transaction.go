// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"database/sql"
	"fmt"
	"strings"
)

// TransactionTimeSeriesRepository exposes the emitter-facing Task 3 helpers on
// an existing indexing transaction, so fail_indexing participates in rollback.
type TransactionTimeSeriesRepository struct {
	Tx   *sql.Tx
	DBMS string
}

// ListMetrics lists metric definitions without leaving the caller's transaction.
func (r TransactionTimeSeriesRepository) ListMetrics(filter TimeSeriesMetricFilter) ([]TimeSeriesMetric, error) {
	if r.Tx == nil {
		return nil, fmt.Errorf("time-series transaction is nil")
	}
	p := newInformationSeedPlaceholders(r.DBMS)
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
	rows, err := r.Tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transaction time-series metrics: %w", err)
	}
	defer rows.Close()
	metrics := []TimeSeriesMetric{}
	for rows.Next() {
		metric, scanErr := scanTimeSeriesMetric(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		metrics = append(metrics, *metric)
	}
	return metrics, rows.Err()
}

// PreviousObservation finds the prior comparable observation in the transaction.
func (r TransactionTimeSeriesRepository) PreviousObservation(lookup TimeSeriesChangeLookup) (*TimeSeriesObservation, error) {
	if r.Tx == nil {
		return nil, fmt.Errorf("time-series transaction is nil")
	}
	if lookup.MetricID == 0 {
		return nil, fmt.Errorf("time-series change lookup metric ID is required")
	}
	filter := TimeSeriesQueryFilter{MetricID: &lookup.MetricID, InformationSeedID: lookup.Scope.InformationSeedID, InformationSeedCandidateID: lookup.Scope.InformationSeedCandidateID, SourceID: lookup.Scope.SourceID, SourceInformationSeedID: lookup.Scope.SourceInformationSeedID, IndexID: lookup.Scope.IndexID, EntityID: lookup.Scope.EntityID, SubjectType: lookup.Scope.SubjectType, SubjectID: lookup.Scope.SubjectID, ObjectType: lookup.Scope.ObjectType, ObjectID: lookup.Scope.ObjectID, Dimensions: lookup.Dimensions, End: &lookup.Before, TimeBasis: lookup.TimeBasis, IncludeDeleted: true, Descending: true}
	conditions, args, _, err := buildTimeSeriesQueryConditions(r.DBMS, filter, "o")
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + prefixColumns(timeSeriesObservationColumns, "o") + ` FROM TimeSeriesObservations o WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY o.observed_at DESC, o.observation_id DESC`
	rows, err := r.Tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup previous transaction observation: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		observation, scanErr := scanTimeSeriesObservation(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		if dimensionsContain(observation.Dimensions, lookup.Dimensions) {
			return observation, nil
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return nil, ErrTimeSeriesObservationNotFound
}

// InsertObservation applies the idempotent Task 3 insert helper in the transaction.
func (r TransactionTimeSeriesRepository) InsertObservation(observation *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	if r.Tx == nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series transaction is nil")
	}
	return insertTimeSeriesObservationTx(r.Tx, r.DBMS, observation)
}
