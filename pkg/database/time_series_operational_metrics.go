package database

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	timeSeriesAggregationRowsScanned = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_rows_scanned_total",
		Help: "Observations scanned by time-series aggregation.",
	})
	timeSeriesAggregationWindowsCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_windows_completed_total",
		Help: "Time-series aggregation windows completed and checkpointed.",
	})
	timeSeriesAggregationRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_retries_total",
		Help: "PostgreSQL time-series aggregate replacement transaction retries.",
	})
	timeSeriesAggregationBudgetStops = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_budget_stops_total",
		Help: "Time-series aggregation invocations stopped at an atomic window boundary by budget.",
	}, []string{"reason"})
	timeSeriesCardinalityDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_cardinality_decisions_total",
		Help: "Time-series cardinality decisions by bounded resource type and outcome.",
	}, []string{"resource", "outcome"})
	timeSeriesCardinalityReservationRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_cardinality_reservation_retries_total",
		Help: "PostgreSQL cardinality reservations retried after a concurrent claim.",
	})
	timeSeriesCardinalityReservationWaits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_cardinality_reservation_waits_total",
		Help: "PostgreSQL cardinality reservations unable to acquire an available token because of contention.",
	})
	timeSeriesCardinalityReservationCleanup = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_cardinality_reservation_cleanup_total",
		Help: "Unreferenced PostgreSQL cardinality reservations removed by bounded resource type.",
	}, []string{"resource"})
	timeSeriesCardinalityIntegrityErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_cardinality_integrity_errors_total",
		Help: "Cardinality identity or membership integrity errors by bounded resource type.",
	}, []string{"resource"})
	timeSeriesPostgresPersistenceDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crowler_timeseries_postgres_persistence_duration_seconds",
		Help:    "PostgreSQL persistence time split into bounded reservation, membership, and observation operations.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})
)

const (
	cardinalityResourceSeries         = "series"
	cardinalityResourceDimensionValue = "dimension_value"
	cardinalityOutcomeExisting        = "existing"
	cardinalityOutcomeAdmitted        = "admitted"
	cardinalityOutcomeRejected        = "rejected"
	postgresOperationReservation      = "reservation_acquisition"
	postgresOperationMembership       = "active_membership_accounting"
	postgresOperationObservation      = "observation_insertion"
)

func init() {
	prometheus.MustRegister(timeSeriesAggregationRowsScanned, timeSeriesAggregationWindowsCompleted, timeSeriesAggregationRetries, timeSeriesAggregationBudgetStops,
		timeSeriesCardinalityDecisions, timeSeriesCardinalityReservationRetries, timeSeriesCardinalityReservationWaits,
		timeSeriesCardinalityReservationCleanup, timeSeriesCardinalityIntegrityErrors, timeSeriesPostgresPersistenceDuration)
}

func observePostgresPersistence(operation string) func() {
	started := time.Now()
	return func() {
		timeSeriesPostgresPersistenceDuration.WithLabelValues(operation).Observe(time.Since(started).Seconds())
	}
}
