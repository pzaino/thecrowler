package database

import "github.com/prometheus/client_golang/prometheus"

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
)

func init() {
	prometheus.MustRegister(timeSeriesAggregationRowsScanned, timeSeriesAggregationWindowsCompleted, timeSeriesAggregationRetries)
}
