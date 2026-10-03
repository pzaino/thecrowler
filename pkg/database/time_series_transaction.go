// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// TransactionTimeSeriesRepository exposes the emitter-facing Task 3 helpers on
// an existing indexing transaction, so fail_indexing participates in rollback.
type TransactionTimeSeriesRepository struct {
	Tx   *sql.Tx
	DBMS string
}

// IsTransactionFatalError reports whether err came from PostgreSQL and has
// invalidated this caller-owned transaction. PostgreSQL aborts the transaction
// after every server ERROR (including 40P01 and 40001), not just errors that are
// conventionally retryable. Keeping this knowledge on the transaction-backed
// repository lets emitters distinguish SQL failures from validation failures.
func (r TransactionTimeSeriesRepository) IsTransactionFatalError(err error) bool {
	if r.DBMS != DBPostgresStr || err == nil {
		return false
	}
	var postgresErr *pq.Error
	return errors.As(err, &postgresErr) && (postgresErr.Severity == "" || postgresErr.Severity == "ERROR" || postgresErr.Severity == "FATAL" || postgresErr.Severity == "PANIC")
}

// ListMetrics lists metric definitions without leaving the caller's transaction.
func (r TransactionTimeSeriesRepository) ListMetrics(filter TimeSeriesMetricFilter) ([]TimeSeriesMetric, error) {
	return r.ListMetricsContext(context.Background(), filter)
}

// ListMetricsContext lists metric definitions using ctx.
func (r TransactionTimeSeriesRepository) ListMetricsContext(ctx context.Context, filter TimeSeriesMetricFilter) ([]TimeSeriesMetric, error) {
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
	rows, err := r.Tx.QueryContext(ctx, query, args...)
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
	return r.PreviousObservationContext(context.Background(), lookup)
}

// PreviousObservationContext finds the prior comparable observation using ctx.
func (r TransactionTimeSeriesRepository) PreviousObservationContext(ctx context.Context, lookup TimeSeriesChangeLookup) (*TimeSeriesObservation, error) {
	if r.Tx == nil {
		return nil, fmt.Errorf("time-series transaction is nil")
	}
	if lookup.MetricID == 0 {
		return nil, fmt.Errorf("time-series change lookup metric ID is required")
	}
	query, args, err := previousTimeSeriesObservationQuery(r.DBMS, lookup)
	if err != nil {
		return nil, err
	}
	observation, err := scanTimeSeriesObservation(func(dest ...interface{}) error {
		return r.Tx.QueryRowContext(ctx, query, args...).Scan(dest...)
	})
	if err == sql.ErrNoRows {
		return nil, ErrTimeSeriesObservationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup previous transaction observation: %w", err)
	}
	return observation, nil
}

// InsertObservation applies the idempotent Task 3 insert helper in the transaction.
func (r TransactionTimeSeriesRepository) InsertObservation(observation *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	return r.InsertObservationContext(context.Background(), observation)
}

// InsertObservationContext inserts an observation using ctx.
func (r TransactionTimeSeriesRepository) InsertObservationContext(ctx context.Context, observation *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	if r.Tx == nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series transaction is nil")
	}
	return insertTimeSeriesObservationTxContext(ctx, r.Tx, r.DBMS, observation)
}

// InsertObservationWithCardinality persists one observation while owning the
// complete cardinality lifecycle. In particular, callers must not reserve a
// series before calling this method: deduplication deliberately happens before
// admission, and every reservation is in the same transaction as the insert
// and active-reference accounting.
func (r TransactionTimeSeriesRepository) InsertObservationWithCardinality(observation *TimeSeriesObservation, cardinality cfg.TimeSeriesCardinalityConfig, failurePolicy cfg.TimeSeriesFailurePolicy) (TimeSeriesInsertResult, error) {
	return r.InsertObservationWithCardinalityContext(context.Background(), observation, cardinality, failurePolicy)
}

// InsertObservationWithCardinalityContext is the context-aware form of
// InsertObservationWithCardinality. Skippable policies use a savepoint so any
// SQL error can be returned to the emitter without poisoning PostgreSQL's
// surrounding page transaction or retaining partially acquired capacity.
func (r TransactionTimeSeriesRepository) InsertObservationWithCardinalityContext(ctx context.Context, observation *TimeSeriesObservation, cardinality cfg.TimeSeriesCardinalityConfig, failurePolicy cfg.TimeSeriesFailurePolicy) (TimeSeriesInsertResult, error) {
	if r.Tx == nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series transaction is nil")
	}
	return insertObservationWithCardinality(ctx, r.Tx, r.DBMS, observation, cardinality, failurePolicy)
}
