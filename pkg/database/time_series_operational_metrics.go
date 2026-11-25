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
	timeSeriesAggregationSkippedMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_skipped_metrics_total",
		Help: "Explicit aggregate rebuilds that omitted a metric by bounded reason.",
	}, []string{"reason"})
	timeSeriesAggregationSkippedWindows = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_aggregation_skipped_windows_total",
		Help: "Incremental aggregation windows that advanced without replacing any aggregate because raw history was incomplete.",
	})
	timeSeriesRetentionRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_retention_runs_total",
		Help: "Time-series retention invocations by bounded outcome.",
	}, []string{"outcome"})
	timeSeriesRetentionRowsDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_retention_rows_deleted_total",
		Help: "Rows physically deleted by time-series retention by family.",
	}, []string{"family"})
	timeSeriesRetentionSkippedMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_timeseries_retention_skipped_metrics_total",
		Help: "Metric definitions skipped by time-series retention by bounded reason.",
	}, []string{"reason"})
	timeSeriesRetentionBudgetStops = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_timeseries_retention_budget_stops_total",
		Help: "Time-series retention invocations stopped by a batch or duration budget.",
	})
	timeSeriesRetentionRunDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "crowler_timeseries_retention_run_duration_seconds",
		Help:    "Wall-clock duration of one time-series retention invocation.",
		Buckets: prometheus.DefBuckets,
	})
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
	prometheus.MustRegister(timeSeriesAggregationRowsScanned, timeSeriesAggregationWindowsCompleted, timeSeriesAggregationRetries, timeSeriesAggregationBudgetStops, timeSeriesAggregationSkippedMetrics, timeSeriesAggregationSkippedWindows,
		timeSeriesRetentionRuns, timeSeriesRetentionRowsDeleted, timeSeriesRetentionSkippedMetrics, timeSeriesRetentionBudgetStops, timeSeriesRetentionRunDuration,
		timeSeriesCardinalityDecisions, timeSeriesCardinalityReservationRetries, timeSeriesCardinalityReservationWaits,
		timeSeriesCardinalityReservationCleanup, timeSeriesCardinalityIntegrityErrors, timeSeriesPostgresPersistenceDuration)
}

func observePostgresPersistence(operation string) func() {
	started := time.Now()
	return func() {
		timeSeriesPostgresPersistenceDuration.WithLabelValues(operation).Observe(time.Since(started).Seconds())
	}
}
