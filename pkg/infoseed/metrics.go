package infoseed

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var informationSeedMetricsEnabled atomic.Bool

var (
	informationSeedSeedsClaimed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_seeds_claimed_total",
		Help: "Total information seeds claimed by the scheduler.",
	}, []string{"engine"})
	informationSeedCandidatesFound = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_candidates_found_total",
		Help: "Total information seed candidates found.",
	}, []string{"provider"})
	informationSeedCandidatesRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_candidates_rejected_total",
		Help: "Total information seed candidates rejected.",
	}, []string{"stage"})
	informationSeedCandidatesAccepted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_information_seed_candidates_accepted_total",
		Help: "Total information seed candidates accepted.",
	})
	informationSeedSourcesCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_information_seed_sources_created_total",
		Help: "Total sources created from information seeds.",
	})
	informationSeedSourcesLinked = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_information_seed_sources_linked_total",
		Help: "Total sources linked to information seeds.",
	})
	informationSeedProviderFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_provider_failures_total",
		Help: "Total information seed provider failures.",
	}, []string{"provider"})
	informationSeedPluginFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_plugin_failures_total",
		Help: "Total information seed plugin failures.",
	}, []string{"plugin"})
	informationSeedProviderRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_provider_requests_total",
		Help: "Total information seed provider requests.",
	}, []string{"provider"})
	informationSeedBrowserOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crowler_information_seed_browser_operations_total",
		Help: "Bounded browser discovery operation outcomes.",
	}, []string{"provider", "operation", "outcome", "reason"})
	informationSeedBrowserOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crowler_information_seed_browser_operation_duration_seconds",
		Help:    "Browser discovery operation duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "operation"})
	informationSeedRunDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "crowler_information_seed_run_duration_seconds",
		Help:    "Information seed run duration in seconds.",
		Buckets: prometheus.DefBuckets,
	})
)

func init() {
	prometheus.MustRegister(
		informationSeedSeedsClaimed,
		informationSeedCandidatesFound,
		informationSeedCandidatesRejected,
		informationSeedCandidatesAccepted,
		informationSeedSourcesCreated,
		informationSeedSourcesLinked,
		informationSeedProviderFailures,
		informationSeedPluginFailures,
		informationSeedProviderRequests,
		informationSeedBrowserOperations,
		informationSeedBrowserOperationDuration,
		informationSeedRunDuration,
	)
}

// SetMetricsEnabled controls whether information-seed Prometheus collectors are updated.
func SetMetricsEnabled(enabled bool) {
	informationSeedMetricsEnabled.Store(enabled)
}

func recordInformationSeedSeedsClaimed(engine string, count int) {
	if !informationSeedMetricsEnabled.Load() || count <= 0 {
		return
	}
	informationSeedSeedsClaimed.WithLabelValues(safeInformationSeedMetricLabel(engine, "unknown")).Add(float64(count))
}

func recordInformationSeedRunMetrics(stats *seedDiscoveryStats, duration time.Duration) {
	if !informationSeedMetricsEnabled.Load() {
		return
	}
	if duration >= 0 {
		informationSeedRunDuration.Observe(duration.Seconds())
	}
	if stats == nil {
		return
	}
	for provider, count := range stats.ProviderCounts {
		if count > 0 {
			informationSeedCandidatesFound.WithLabelValues(safeInformationSeedMetricLabel(provider, "unknown")).Add(float64(count))
		}
	}
	for stage, reasons := range stats.RejectionStages {
		total := 0
		for _, count := range reasons {
			if count > 0 {
				total += count
			}
		}
		if total > 0 {
			informationSeedCandidatesRejected.WithLabelValues(safeInformationSeedMetricLabel(stage, "unspecified")).Add(float64(total))
		}
	}
	if stats.CandidatesAccepted > 0 {
		informationSeedCandidatesAccepted.Add(float64(stats.CandidatesAccepted))
	}
	if stats.SourcesCreated > 0 {
		informationSeedSourcesCreated.Add(float64(stats.SourcesCreated))
	}
	if stats.SourcesLinked > 0 {
		informationSeedSourcesLinked.Add(float64(stats.SourcesLinked))
	}
	for provider, metrics := range stats.ProviderMetrics {
		provider = safeInformationSeedMetricLabel(provider, "unknown")
		if count := metrics["requests"]; count > 0 {
			informationSeedProviderRequests.WithLabelValues(provider).Add(float64(count))
		}
		if count := metrics["errors"]; count > 0 {
			informationSeedProviderFailures.WithLabelValues(provider).Add(float64(count))
		}
	}
	for provider, operations := range stats.BrowserDiagnostics {
		provider = safeInformationSeedMetricLabel(provider, "unknown")
		for operation, observations := range operations {
			operation = safeBrowserMetricLabel(operation, "unknown")
			for outcomeReason, count := range observations {
				if count <= 0 {
					continue
				}
				outcome, reason := splitBrowserDiagnosticKey(outcomeReason)
				informationSeedBrowserOperations.WithLabelValues(provider, operation, outcome, reason).Add(float64(count))
			}
		}
	}
	for provider, operations := range stats.BrowserDurationsMS {
		provider = safeInformationSeedMetricLabel(provider, "unknown")
		for operation, milliseconds := range operations {
			if milliseconds >= 0 {
				informationSeedBrowserOperationDuration.WithLabelValues(provider, safeBrowserMetricLabel(operation, "unknown")).Observe(float64(milliseconds) / 1000)
			}
		}
	}
	for plugin, count := range stats.PluginFailures {
		if count > 0 {
			informationSeedPluginFailures.WithLabelValues(safeInformationSeedMetricLabel(plugin, "unknown")).Add(float64(count))
		}
	}
}

func safeInformationSeedMetricLabel(value, fallback string) string {
	value = strings.TrimSpace(redactInformationSeedError(redactInformationSeedURL(value)))
	if value == "" || strings.Contains(value, informationSeedRedactedValue) {
		return fallback
	}
	if len(value) > 120 {
		return value[:120]
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value
	}
	return value
}

func splitBrowserDiagnosticKey(value string) (string, string) {
	parts := strings.SplitN(value, ":", 2)
	outcome := safeBrowserMetricLabel(parts[0], "failure")
	reason := "operation_failure"
	if len(parts) == 2 {
		reason = safeBrowserMetricLabel(parts[1], reason)
	}
	return outcome, reason
}

func safeBrowserMetricLabel(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return fallback
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return fallback
		}
	}
	return value
}
