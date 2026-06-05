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
