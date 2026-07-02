package searchproviders

import (
	"strings"
	"sync"
	"time"
)

const (
	BrowserDiagnosticLeaseWait          = "lease_wait"
	BrowserDiagnosticNavigation         = "navigation"
	BrowserDiagnosticActions            = "actions"
	BrowserDiagnosticScraping           = "scraping"
	BrowserDiagnosticPages              = "pages"
	BrowserDiagnosticRawRecords         = "raw_records"
	BrowserDiagnosticAcceptedCandidates = "accepted_candidates"
	BrowserDiagnosticURLRejections      = "url_rejections"
	BrowserDiagnosticHBSFallbacks       = "hbs_fallbacks"
	BrowserDiagnosticCleanupFailures    = "cleanup_failures"
	BrowserDiagnosticBudgetExhausted    = "budget_exhausted"
)

const (
	BrowserOutcomeSuccess = "success"
	BrowserOutcomeFailure = "failure"
	BrowserOutcomeSkipped = "skipped"
)

const (
	BrowserReasonNone             = "none"
	BrowserReasonInvalidURL       = "invalid_url"
	BrowserReasonMissingURL       = "missing_url"
	BrowserReasonDuplicateURL     = "duplicate_url"
	BrowserReasonCurrentPage      = "current_page"
	BrowserReasonVisitedPage      = "visited_page"
	BrowserReasonDeniedHost       = "denied_host"
	BrowserReasonSearchNavigation = "search_navigation"
	BrowserReasonDisallowedHost   = "disallowed_host"
	BrowserReasonBudgetExhausted  = "budget_exhausted"
	BrowserReasonSessionClose     = "session_close"
	BrowserReasonLeaseRelease     = "lease_release"
	BrowserReasonHBSFailure       = "hbs_failure"
	BrowserReasonOperationFailure = "operation_failure"
)

var browserDiagnosticOperations = stringSet(
	BrowserDiagnosticLeaseWait, BrowserDiagnosticNavigation, BrowserDiagnosticActions,
	BrowserDiagnosticScraping, BrowserDiagnosticPages, BrowserDiagnosticRawRecords,
	BrowserDiagnosticAcceptedCandidates, BrowserDiagnosticURLRejections,
	BrowserDiagnosticHBSFallbacks, BrowserDiagnosticCleanupFailures, BrowserDiagnosticBudgetExhausted,
)
var browserDiagnosticOutcomes = stringSet(BrowserOutcomeSuccess, BrowserOutcomeFailure, BrowserOutcomeSkipped)
var browserDiagnosticReasons = stringSet(
	BrowserReasonNone, BrowserReasonInvalidURL, BrowserReasonMissingURL, BrowserReasonDuplicateURL,
	BrowserReasonCurrentPage, BrowserReasonVisitedPage, BrowserReasonDeniedHost, BrowserReasonSearchNavigation,
	BrowserReasonDisallowedHost, BrowserReasonBudgetExhausted, BrowserReasonSessionClose,
	BrowserReasonLeaseRelease, BrowserReasonHBSFailure, BrowserReasonOperationFailure,
)

// BrowserDiagnostic is deliberately count-and-duration only. It must never
// contain URLs, headers, page HTML, screenshots, credentials, or error text.
type BrowserDiagnostic struct {
	Operation string
	Outcome   string
	Reason    string
	Count     int
	Duration  time.Duration
}

// BrowserDiagnostics accumulates bounded, secret-safe browser observations.
type BrowserDiagnostics struct {
	mu       sync.Mutex
	counts   map[string]map[string]int
	duration map[string]time.Duration
}

func (d *BrowserDiagnostics) Observe(observation BrowserDiagnostic) {
	if d == nil || observation.Count <= 0 {
		return
	}
	operation := boundedBrowserDiagnosticLabel(observation.Operation, browserDiagnosticOperations, BrowserDiagnosticBudgetExhausted)
	outcome := boundedBrowserDiagnosticLabel(observation.Outcome, browserDiagnosticOutcomes, BrowserOutcomeFailure)
	reason := boundedBrowserDiagnosticLabel(observation.Reason, browserDiagnosticReasons, BrowserReasonOperationFailure)
	key := outcome + ":" + reason
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counts == nil {
		d.counts = make(map[string]map[string]int)
	}
	if d.duration == nil {
		d.duration = make(map[string]time.Duration)
	}
	if d.counts[operation] == nil {
		d.counts[operation] = make(map[string]int)
	}
	d.counts[operation][key] += observation.Count
	if observation.Duration > 0 {
		d.duration[operation] += observation.Duration
	}
}

func (d *BrowserDiagnostics) Snapshot() (map[string]map[string]int, map[string]time.Duration) {
	if d == nil {
		return map[string]map[string]int{}, map[string]time.Duration{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	counts := make(map[string]map[string]int, len(d.counts))
	for operation, values := range d.counts {
		counts[operation] = make(map[string]int, len(values))
		for key, value := range values {
			counts[operation][key] = value
		}
	}
	durations := make(map[string]time.Duration, len(d.duration))
	for operation, duration := range d.duration {
		durations[operation] = duration
	}
	return counts, durations
}

func boundedBrowserDiagnosticLabel(value string, allowed map[string]struct{}, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := allowed[value]; ok {
		return value
	}
	return fallback
}

func stringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func observeBrowserDiagnostic(options Options, operation, outcome, reason string, count int, started time.Time) {
	if options.Diagnostics == nil {
		return
	}
	duration := time.Duration(0)
	if !started.IsZero() {
		duration = time.Since(started)
	}
	options.Diagnostics.Observe(BrowserDiagnostic{Operation: operation, Outcome: outcome, Reason: reason, Count: count, Duration: duration})
}
