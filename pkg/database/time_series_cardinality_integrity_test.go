// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"testing"
)

// assertTimeSeriesCardinalityIntegrity is shared by lifecycle tests. In
// addition to exact observation memberships, PostgreSQL tests verify that reservations and
// active identities are an exact bijection (including collision witnesses).
// Call it after inserts, batches, duplicates, identity backfills, logical and
// physical deletion, rollback, retention, source cleanup, and rebuild.
func assertTimeSeriesCardinalityIntegrity(t testing.TB, db *sql.DB, dbms string) {
	t.Helper()
	ctx := context.Background()
	var mismatches int
	activeMismatch := `SELECT COUNT(*) FROM (
		(SELECT metric_id,series_hash FROM TimeSeriesActiveSeries EXCEPT SELECT metric_id,series_hash FROM TimeSeriesObservationSeries)
		UNION ALL
		(SELECT metric_id,series_hash FROM TimeSeriesObservationSeries EXCEPT SELECT metric_id,series_hash FROM TimeSeriesActiveSeries)
		UNION ALL
		(SELECT metric_id,dimension_key,value_hash FROM TimeSeriesActiveDimensionValues EXCEPT SELECT metric_id,dimension_key,value_hash FROM TimeSeriesObservationDimensions)
		UNION ALL
		(SELECT metric_id,dimension_key,value_hash FROM TimeSeriesObservationDimensions EXCEPT SELECT metric_id,dimension_key,value_hash FROM TimeSeriesActiveDimensionValues)
	) broken`
	if err := db.QueryRowContext(ctx, activeMismatch).Scan(&mismatches); err != nil {
		t.Fatalf("check active cardinality memberships: %v", err)
	}
	if mismatches != 0 {
		t.Fatalf("active cardinality has %d invalid references", mismatches)
	}
	if dbms != DBPostgresStr {
		return
	}
	reservationMismatch := `SELECT COUNT(*) FROM (
		(SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries
		 EXCEPT SELECT metric_id,series_hash,identity_value FROM TimeSeriesSeriesSlots)
		UNION ALL
		(SELECT metric_id,series_hash,identity_value FROM TimeSeriesSeriesSlots
		 EXCEPT SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries)
		UNION ALL
		(SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues
		 EXCEPT SELECT metric_id,dimension_key,value_hash,identity_value FROM TimeSeriesDimensionSlots)
		UNION ALL
		(SELECT metric_id,dimension_key,value_hash,identity_value FROM TimeSeriesDimensionSlots
		 EXCEPT SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues)
	) broken`
	if err := db.QueryRowContext(ctx, reservationMismatch).Scan(&mismatches); err != nil {
		t.Fatalf("compare active and reserved cardinality identities: %v", err)
	}
	if mismatches != 0 {
		t.Fatalf("active and reserved cardinality identities differ in %d rows", mismatches)
	}
}
