package timeseries

import "github.com/prometheus/client_golang/prometheus"

// Emitter operation names and outcomes are fixed here. Do not add metric keys,
// query text, object identifiers, or other user-controlled values as labels.
const (
	metricOperationObservationAttempt = "observation_attempt"
	metricOperationInsert             = "insert"
	metricOperationMetricLookup       = "metric_lookup"
	metricOperationPreviousLookup     = "previous_observation_lookup"
	metricOperationCardinalityCheck   = "cardinality_check"
	metricOperationScopeResolution    = "scope_resolution"
)

var emitterOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "crowler_timeseries_emitter_operations_total",
	Help: "Time-series emitter operations by bounded operation and outcome.",
}, []string{"operation", "outcome"})

func init() {
	prometheus.MustRegister(emitterOperations)
}

func recordEmitterOperation(operation, outcome string) {
	emitterOperations.WithLabelValues(operation, outcome).Inc()
}
