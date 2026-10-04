package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCardinalityDecisionMetrics(t *testing.T) {
	for _, resource := range []string{cardinalityResourceSeries, cardinalityResourceDimensionValue} {
		for _, outcome := range []string{cardinalityOutcomeExisting, cardinalityOutcomeAdmitted, cardinalityOutcomeRejected} {
			t.Run(resource+"/"+outcome, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				mock.ExpectBegin()
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				dimensionKey := ""
				if resource == cardinalityResourceDimensionValue {
					dimensionKey = "region"
				}
				before := testutil.ToFloat64(timeSeriesCardinalityDecisions.WithLabelValues(resource, outcome))
				mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
				if outcome == cardinalityOutcomeExisting {
					mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("identity"))
				} else {
					mock.ExpectQuery(`SELECT identity_value`).WillReturnError(sql.ErrNoRows)
					mock.ExpectExec(`INSERT INTO TimeSeriesCardinalityTokens`).WillReturnResult(sqlmock.NewResult(0, 0))
					active := 0
					if outcome == cardinalityOutcomeRejected {
						active = 1
					}
					mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
					if outcome == cardinalityOutcomeAdmitted {
						mock.ExpectExec(`SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
						mock.ExpectQuery(`SELECT t.token_number`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(0))
						mock.ExpectQuery(`INSERT INTO`).WillReturnRows(sqlmock.NewRows([]string{"slot_number"}).AddRow(0))
						mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
					}
				}
				admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, map[string]string{cardinalityResourceSeries: "TimeSeriesSeriesSlots", cardinalityResourceDimensionValue: "TimeSeriesDimensionSlots"}[resource], 1, dimensionKey, "hash", "identity", 1)
				if err != nil {
					t.Fatal(err)
				}
				if admitted != (outcome != cardinalityOutcomeRejected) {
					t.Fatalf("admitted = %v for outcome %s", admitted, outcome)
				}
				if got := testutil.ToFloat64(timeSeriesCardinalityDecisions.WithLabelValues(resource, outcome)); got != before+1 {
					t.Fatalf("counter = %v, want %v", got, before+1)
				}
				if err = mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestDuplicateReservationCleanupMetrics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, _ := db.Begin()
	mock.ExpectExec(`DELETE FROM TimeSeriesSeriesSlots`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM TimeSeriesDimensionSlots`).WillReturnResult(sqlmock.NewResult(0, 1))
	seriesBefore := testutil.ToFloat64(timeSeriesCardinalityReservationCleanup.WithLabelValues(cardinalityResourceSeries))
	dimensionBefore := testutil.ToFloat64(timeSeriesCardinalityReservationCleanup.WithLabelValues(cardinalityResourceDimensionValue))
	o := &TimeSeriesObservation{MetricID: 1, SeriesHash: "series", Dimensions: map[string]interface{}{"region": "private-value"}}
	if err = newTimeSeriesCardinalityMutation(context.Background(), tx, DBPostgresStr).releaseUnreferencedReservations(o); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(timeSeriesCardinalityReservationCleanup.WithLabelValues(cardinalityResourceSeries)); got != seriesBefore+1 {
		t.Fatalf("series cleanup counter = %v", got)
	}
	if got := testutil.ToFloat64(timeSeriesCardinalityReservationCleanup.WithLabelValues(cardinalityResourceDimensionValue)); got != dimensionBefore+1 {
		t.Fatalf("dimension cleanup counter = %v", got)
	}
}

func TestCardinalityIntegrityErrorMetric(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, _ := db.Begin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("different"))
	before := testutil.ToFloat64(timeSeriesCardinalityIntegrityErrors.WithLabelValues(cardinalityResourceSeries))
	if _, err = claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 1, "", "hash", "identity", 1); err == nil {
		t.Fatal("expected identity collision")
	}
	if got := testutil.ToFloat64(timeSeriesCardinalityIntegrityErrors.WithLabelValues(cardinalityResourceSeries)); got != before+1 {
		t.Fatalf("integrity counter = %v, want %v", got, before+1)
	}
}

func TestCardinalityContentionMetricsDoNotReportRejection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, _ := db.Begin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT identity_value`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO TimeSeriesCardinalityTokens`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FOR UPDATE OF t SKIP LOCKED`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`ORDER BY t.token_number LIMIT 1`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO TimeSeriesSeriesSlots`).WillReturnRows(sqlmock.NewRows([]string{"slot_number"}).AddRow(0))
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	waitsBefore := testutil.ToFloat64(timeSeriesCardinalityReservationWaits)
	rejectedBefore := testutil.ToFloat64(timeSeriesCardinalityDecisions.WithLabelValues(cardinalityResourceSeries, cardinalityOutcomeRejected))
	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 1, "", "hash", "identity", 1)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	if got := testutil.ToFloat64(timeSeriesCardinalityReservationWaits); got != waitsBefore+1 {
		t.Fatalf("wait counter = %v, want %v", got, waitsBefore+1)
	}
	if got := testutil.ToFloat64(timeSeriesCardinalityDecisions.WithLabelValues(cardinalityResourceSeries, cardinalityOutcomeRejected)); got != rejectedBefore {
		t.Fatalf("rejected counter = %v, want unchanged %v", got, rejectedBefore)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
