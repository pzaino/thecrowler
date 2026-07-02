// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package searchproviders

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	browserWebDriverMaxOperations = 64

	BrowserTransportHTTP      = "http"
	BrowserTransportWebDriver = "webdriver"

	BrowserActionInitial    = "initial"
	BrowserActionConsent    = "consent"
	BrowserActionQuery      = "query"
	BrowserActionPagination = "pagination"

	BrowserActionModeSelenium = "selenium"
	BrowserActionModeHBS      = "hbs"
)

// BrowserOptions contains the bounded, non-privileged settings for the
// explicitly selected WebDriver transport.
type BrowserOptions struct {
	VDIAllowList           []string
	NavigationTimeout      time.Duration
	PageReadinessTimeout   time.Duration
	HBSEnabled             bool
	SeleniumFallback       bool
	InitialActions         []string
	ConsentActions         []string
	QueryActions           []string
	PaginationActions      []string
	ScrapingRules          []string
	AllowedNavigationHosts []string
	DeniedHosts            []string
	MaxPages               int
	MaxRequests            int
	MaxCandidates          int
	ScreenshotOnError      bool
}

// BrowserSessionRequest is the immutable lease request supplied to an injected
// browser-session provider.
type BrowserSessionRequest struct {
	ProviderName string
	VDIAllowList []string
	AllowedHosts []string
	ReadyTimeout time.Duration
}

// BrowserSession is the minimum browser surface required by this provider.
type BrowserSession interface {
	Navigate(ctx context.Context, target string) error
	CurrentURL(ctx context.Context) (string, error)
	Close(ctx context.Context) error
}

// BrowserSessionLease releases the underlying VDI or equivalent resource.
type BrowserSessionLease interface {
	Release(ctx context.Context) error
}

// BrowserSessionProvider acquires an isolated session and its independently
// releasable lease. Implementations are injected; this package uses no globals.
type BrowserSessionProvider interface {
	Acquire(ctx context.Context, request BrowserSessionRequest) (BrowserSession, BrowserSessionLease, error)
}

// BrowserActionRule and BrowserScrapingRule are opaque, read-only rule values.
// Resolver implementations may attach immutable runtime-specific data to Value.
type BrowserActionRule struct {
	Name  string
	Value interface{}
}

type BrowserScrapingRule struct {
	Name  string
	Value interface{}
}

// BrowserRuleResolver resolves configured references without permitting the
// search provider to mutate the backing ruleset.
type BrowserRuleResolver interface {
	ResolveActionRules(ctx context.Context, references []string) ([]BrowserActionRule, error)
	ResolveScrapingRules(ctx context.Context, references []string) ([]BrowserScrapingRule, error)
}

// BrowserActionRequest describes one configured action phase.
type BrowserActionRequest struct {
	Phase string
	Mode  string
	Query string
	Rules []BrowserActionRule
}

// BrowserActionExecutor executes rules against the injected session.
type BrowserActionExecutor interface {
	Execute(ctx context.Context, session BrowserSession, request BrowserActionRequest) error
}

// BrowserResultScraper returns rule extraction records. Each record must
// contain a URL; recognized optional fields are mapped to Result.
type BrowserResultScraper interface {
	Scrape(ctx context.Context, session BrowserSession, rules []BrowserScrapingRule) ([]map[string]interface{}, error)
}

// BrowserOperationBudget is shared by every query for one configured provider.
type BrowserOperationBudget struct {
	mu        sync.Mutex
	remaining int
}

func NewBrowserOperationBudget(limit int) *BrowserOperationBudget {
	return &BrowserOperationBudget{remaining: limit}
}

func (b *BrowserOperationBudget) consume(options Options) error {
	if b == nil {
		observeBrowserDiagnostic(options, BrowserDiagnosticBudgetExhausted, BrowserOutcomeFailure, BrowserReasonBudgetExhausted, 1, time.Time{})
		return errors.New("webdriver operation budget exhausted")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining < 1 {
		observeBrowserDiagnostic(options, BrowserDiagnosticBudgetExhausted, BrowserOutcomeFailure, BrowserReasonBudgetExhausted, 1, time.Time{})
		return errors.New("webdriver operation budget exhausted")
	}
	b.remaining--
	return nil
}

func (p *BrowserSearchProvider) searchWebDriver(ctx context.Context, query string, options Options, endpoint *url.URL) (results []Result, err error) {
	if p.Sessions == nil || p.Actions == nil || p.Scraper == nil || p.Rules == nil {
		return nil, safeProviderError(p.Name(), errors.New("webdriver provider configuration requires browser sessions, actions, scraping, and read-only rule resolution dependencies"))
	}

	browser := boundedWebDriverOptions(options)
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	actions, scrapingRules, err := p.resolveWebDriverRules(ctx, browser)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	leaseStarted := time.Now()
	session, lease, err := p.Sessions.Acquire(ctx, BrowserSessionRequest{
		ProviderName: p.Name(),
		VDIAllowList: append([]string(nil), browser.VDIAllowList...),
		AllowedHosts: append([]string(nil), browser.AllowedNavigationHosts...),
		ReadyTimeout: browser.PageReadinessTimeout,
	})
	if err != nil {
		observeBrowserDiagnostic(options, BrowserDiagnosticLeaseWait, BrowserOutcomeFailure, BrowserReasonOperationFailure, 1, leaseStarted)
		return nil, safeProviderError(p.Name(), err)
	}
	observeBrowserDiagnostic(options, BrowserDiagnosticLeaseWait, BrowserOutcomeSuccess, BrowserReasonNone, 1, leaseStarted)
	if session == nil || lease == nil {
		if session != nil {
			_ = session.Close(context.WithoutCancel(ctx))
		}
		if lease != nil {
			_ = lease.Release(context.WithoutCancel(ctx))
		}
		return nil, safeProviderError(p.Name(), errors.New("webdriver session acquisition returned an incomplete lease"))
	}
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		closeErr := session.Close(cleanupCtx)
		releaseErr := lease.Release(cleanupCtx)
		if closeErr != nil {
			observeBrowserDiagnostic(options, BrowserDiagnosticCleanupFailures, BrowserOutcomeFailure, BrowserReasonSessionClose, 1, time.Time{})
		}
		if releaseErr != nil {
			observeBrowserDiagnostic(options, BrowserDiagnosticCleanupFailures, BrowserOutcomeFailure, BrowserReasonLeaseRelease, 1, time.Time{})
		}
		if err == nil && (closeErr != nil || releaseErr != nil) {
			err = safeProviderError(p.Name(), errors.Join(closeErr, releaseErr))
			results = nil
		}
	}()

	budget := options.OperationBudget
	if budget == nil {
		budget = NewBrowserOperationBudget(browser.MaxRequests)
	}
	startURL := webDriverStartURL(endpoint, query, len(actions[BrowserActionQuery]) > 0, options.Parameters)
	if err = p.navigateWebDriver(ctx, session, startURL, browser.NavigationTimeout, budget, options); err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	if err = p.runWebDriverActions(ctx, session, query, browser, BrowserActionInitial, actions[BrowserActionInitial], budget, options); err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	if err = p.runWebDriverActions(ctx, session, query, browser, BrowserActionConsent, actions[BrowserActionConsent], budget, options); err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	if err = p.runWebDriverActions(ctx, session, query, browser, BrowserActionQuery, actions[BrowserActionQuery], budget, options); err != nil {
		return nil, safeProviderError(p.Name(), err)
	}

	seen := make(map[string]struct{})
	visited := make(map[string]struct{})
	markCurrentNavigation(ctx, session, visited)
	for page := 1; page <= browser.MaxPages; page++ {
		if budgetErr := budget.consume(options); budgetErr != nil {
			return nil, safeProviderError(p.Name(), budgetErr)
		}
		scrapeStarted := time.Now()
		records, scrapeErr := p.Scraper.Scrape(ctx, session, scrapingRules)
		if scrapeErr != nil {
			observeBrowserDiagnostic(options, BrowserDiagnosticScraping, BrowserOutcomeFailure, BrowserReasonOperationFailure, 1, scrapeStarted)
			return nil, safeProviderError(p.Name(), scrapeErr)
		}
		observeBrowserDiagnostic(options, BrowserDiagnosticScraping, BrowserOutcomeSuccess, BrowserReasonNone, 1, scrapeStarted)
		observeBrowserDiagnostic(options, BrowserDiagnosticPages, BrowserOutcomeSuccess, BrowserReasonNone, 1, time.Time{})
		observeBrowserDiagnostic(options, BrowserDiagnosticRawRecords, BrowserOutcomeSuccess, BrowserReasonNone, len(records), time.Time{})
		currentURL, currentErr := session.CurrentURL(ctx)
		if currentErr != nil {
			return nil, safeProviderError(p.Name(), currentErr)
		}
		pageResults := mapWebDriverResults(records, currentURL, p.Name(), page, seen, visited, browser, options)
		observeBrowserDiagnostic(options, BrowserDiagnosticAcceptedCandidates, BrowserOutcomeSuccess, BrowserReasonNone, len(pageResults), time.Time{})
		results = append(results, pageResults...)
		if len(results) >= browser.MaxCandidates || page == browser.MaxPages || len(actions[BrowserActionPagination]) == 0 {
			break
		}
		before := currentURL
		if err = p.runWebDriverActions(ctx, session, query, browser, BrowserActionPagination, actions[BrowserActionPagination], budget, options); err != nil {
			return nil, safeProviderError(p.Name(), err)
		}
		after, currentErr := session.CurrentURL(ctx)
		if currentErr != nil {
			return nil, safeProviderError(p.Name(), currentErr)
		}
		if strings.TrimSpace(after) == "" || sameNormalizedURL(before, after) {
			break
		}
		if !allowedNavigationURL(after, browser.AllowedNavigationHosts) {
			return nil, safeProviderError(p.Name(), errors.New("webdriver navigation left the allowed hosts"))
		}
		visited[normalizedComparisonURL(after)] = struct{}{}
	}
	return trimResults(results, browser.MaxCandidates), nil
}

func (p *BrowserSearchProvider) resolveWebDriverRules(ctx context.Context, browser BrowserOptions) (map[string][]BrowserActionRule, []BrowserScrapingRule, error) {
	actions := make(map[string][]BrowserActionRule, 4)
	phases := []struct {
		name string
		refs []string
	}{
		{BrowserActionInitial, browser.InitialActions},
		{BrowserActionConsent, browser.ConsentActions},
		{BrowserActionQuery, browser.QueryActions},
		{BrowserActionPagination, browser.PaginationActions},
	}
	for _, phase := range phases {
		if len(phase.refs) == 0 {
			continue
		}
		rules, err := p.Rules.ResolveActionRules(ctx, append([]string(nil), phase.refs...))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve %s actions: %w", phase.name, err)
		}
		actions[phase.name] = rules
	}
	rules, err := p.Rules.ResolveScrapingRules(ctx, append([]string(nil), browser.ScrapingRules...))
	if err != nil {
		return nil, nil, fmt.Errorf("resolve scraping rules: %w", err)
	}
	if len(rules) == 0 {
		return nil, nil, errors.New("webdriver scraping rules resolved empty")
	}
	return actions, rules, nil
}

func (p *BrowserSearchProvider) runWebDriverActions(ctx context.Context, session BrowserSession, query string, browser BrowserOptions, phase string, rules []BrowserActionRule, budget *BrowserOperationBudget, options Options) error {
	if len(rules) == 0 {
		return nil
	}
	if err := budget.consume(options); err != nil {
		return err
	}
	started := time.Now()
	mode := BrowserActionModeSelenium
	if browser.HBSEnabled {
		mode = BrowserActionModeHBS
	}
	request := BrowserActionRequest{Phase: phase, Mode: mode, Query: query, Rules: append([]BrowserActionRule(nil), rules...)}
	if err := p.Actions.Execute(ctx, session, request); err != nil {
		if mode != BrowserActionModeHBS || !browser.SeleniumFallback {
			observeBrowserDiagnostic(options, BrowserDiagnosticActions, BrowserOutcomeFailure, BrowserReasonOperationFailure, 1, started)
			return fmt.Errorf("execute %s actions: %w", phase, err)
		}
		observeBrowserDiagnostic(options, BrowserDiagnosticHBSFallbacks, BrowserOutcomeSuccess, BrowserReasonHBSFailure, 1, time.Time{})
		if budgetErr := budget.consume(options); budgetErr != nil {
			return budgetErr
		}
		request.Mode = BrowserActionModeSelenium
		if fallbackErr := p.Actions.Execute(ctx, session, request); fallbackErr != nil {
			observeBrowserDiagnostic(options, BrowserDiagnosticActions, BrowserOutcomeFailure, BrowserReasonOperationFailure, 1, started)
			return fmt.Errorf("execute %s actions: %w", phase, errors.Join(err, fallbackErr))
		}
	}
	observeBrowserDiagnostic(options, BrowserDiagnosticActions, BrowserOutcomeSuccess, BrowserReasonNone, 1, started)
	return nil
}

func (p *BrowserSearchProvider) navigateWebDriver(ctx context.Context, session BrowserSession, target string, timeout time.Duration, budget *BrowserOperationBudget, options Options) error {
	if err := budget.consume(options); err != nil {
		return err
	}
	started := time.Now()
	navigationCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		navigationCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	if err := session.Navigate(navigationCtx, target); err != nil {
		observeBrowserDiagnostic(options, BrowserDiagnosticNavigation, BrowserOutcomeFailure, BrowserReasonOperationFailure, 1, started)
		return fmt.Errorf("navigate webdriver session: %w", err)
	}
	observeBrowserDiagnostic(options, BrowserDiagnosticNavigation, BrowserOutcomeSuccess, BrowserReasonNone, 1, started)
	return nil
}

func boundedWebDriverOptions(options Options) BrowserOptions {
	browser := options.Browser
	if browser.NavigationTimeout <= 0 || browser.NavigationTimeout > options.Timeout {
		browser.NavigationTimeout = options.Timeout
	}
	if browser.PageReadinessTimeout <= 0 || browser.PageReadinessTimeout > options.Timeout {
		browser.PageReadinessTimeout = options.Timeout
	}
	if browser.MaxPages < 1 {
		browser.MaxPages = options.MaxPages
	}
	if browser.MaxPages < 1 {
		browser.MaxPages = browserSearchDefaultMaxPages
	}
	if browser.MaxPages > browserSearchMaxPages {
		browser.MaxPages = browserSearchMaxPages
	}
	if browser.MaxRequests < 1 {
		browser.MaxRequests = options.MaxRequests
	}
	if browser.MaxRequests < 1 {
		browser.MaxRequests = browserSearchDefaultMaxRequests
	}
	if browser.MaxRequests > browserWebDriverMaxOperations {
		browser.MaxRequests = browserWebDriverMaxOperations
	}
	if browser.MaxPages > browser.MaxRequests {
		browser.MaxPages = browser.MaxRequests
	}
	if browser.MaxCandidates < 1 {
		browser.MaxCandidates = options.PageSize * browser.MaxPages
	}
	if browser.MaxCandidates < 1 {
		browser.MaxCandidates = browserSearchDefaultPageSize
	}
	maxCandidates := browserSearchMaxPageSize * browserSearchMaxPages
	if browser.MaxCandidates > maxCandidates {
		browser.MaxCandidates = maxCandidates
	}
	browser.DeniedHosts = append(browser.DeniedHosts, splitHostParameters(options.Parameters["denied_hosts"])...)
	return browser
}

func webDriverStartURL(endpoint *url.URL, query string, hasQueryActions bool, parameters map[string]string) string {
	pageURL := *endpoint
	values := pageURL.Query()
	if !hasQueryActions && query != "" && values.Get("q") == "" && values.Get("query") == "" {
		values.Set("q", query)
	}
	applyParameters(values, safeBrowserSearchParameters(parameters))
	pageURL.RawQuery = values.Encode()
	return pageURL.String()
}

func mapWebDriverResults(records []map[string]interface{}, currentURL, provider string, page int, seen, visited map[string]struct{}, browser BrowserOptions, options Options) []Result {
	base, _ := url.Parse(currentURL)
	results := make([]Result, 0, len(records))
	for _, record := range records {
		rawURL, ok := extractionString(record["url"])
		if !ok {
			observeBrowserDiagnostic(options, BrowserDiagnosticURLRejections, BrowserOutcomeSkipped, BrowserReasonMissingURL, 1, time.Time{})
			continue
		}
		normalized, ok := normalizeExtractedURL(rawURL, base)
		if !ok {
			observeBrowserDiagnostic(options, BrowserDiagnosticURLRejections, BrowserOutcomeSkipped, BrowserReasonInvalidURL, 1, time.Time{})
			continue
		}
		if reason := rejectedExtractedURL(normalized, currentURL, seen, visited, browser); reason != "" {
			observeBrowserDiagnostic(options, BrowserDiagnosticURLRejections, BrowserOutcomeSkipped, reason, 1, time.Time{})
			continue
		}
		seen[normalized] = struct{}{}
		rank, hasRank := extractionInt(record["rank"])
		if !hasRank || rank < 1 {
			rank = len(seen)
		}
		score, hasScore := extractionFloat(record["score"])
		if !hasScore {
			score = reciprocalRank(rank)
		}
		metadata, _ := record["metadata"].(map[string]interface{})
		metadata = cloneMetadata(metadata)
		if metadata == nil {
			metadata = make(map[string]interface{})
		}
		metadata["provider"] = provider
		metadata["evidence"] = map[string]interface{}{"rank": rank, "page": page}
		result := Result{URL: normalized, Rank: rank, Score: score, Metadata: metadata}
		result.Title, _ = extractionString(record["title"])
		result.Snippet, _ = extractionString(record["snippet"])
		results = append(results, result)
		if len(seen) >= browser.MaxCandidates {
			break
		}
	}
	return results
}

func normalizeExtractedURL(raw string, base *url.URL) (string, bool) {
	candidate, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || candidate == nil {
		return "", false
	}
	if !candidate.IsAbs() {
		if base == nil || base.Scheme == "" || base.Host == "" {
			return "", false
		}
		candidate = base.ResolveReference(candidate)
	}
	candidate.Scheme = strings.ToLower(candidate.Scheme)
	candidate.Host = strings.ToLower(candidate.Host)
	candidate.Fragment = ""
	if (candidate.Scheme != "http" && candidate.Scheme != "https") || candidate.Hostname() == "" || candidate.User != nil {
		return "", false
	}
	if net.ParseIP(candidate.Hostname()) == nil && strings.Contains(candidate.Hostname(), "_") {
		return "", false
	}
	return candidate.String(), true
}

func rejectedExtractedURL(candidate, current string, seen, visited map[string]struct{}, browser BrowserOptions) string {
	if _, ok := seen[candidate]; ok {
		return BrowserReasonDuplicateURL
	}
	comparison := normalizedComparisonURL(candidate)
	if comparison == normalizedComparisonURL(current) {
		return BrowserReasonCurrentPage
	}
	if _, ok := visited[comparison]; ok {
		return BrowserReasonVisitedPage
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return BrowserReasonInvalidURL
	}
	if hostMatchesAny(u.Hostname(), browser.DeniedHosts) {
		return BrowserReasonDeniedHost
	}
	if isSearchNavigationURL(u) {
		return BrowserReasonSearchNavigation
	}
	return ""
}

func isSearchNavigationURL(candidate *url.URL) bool {
	path := strings.ToLower(strings.Trim(candidate.EscapedPath(), "/"))
	if path == "search" || strings.HasSuffix(path, "/search") || path == "results" || strings.HasSuffix(path, "/results") {
		return true
	}
	query := candidate.Query()
	return query.Has("q") || query.Has("query") || query.Has("page") || query.Has("start") || query.Has("offset")
}

func allowedNavigationURL(raw string, allowed []string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme != "" && u.Hostname() != "" && (len(allowed) == 0 || hostMatchesAny(u.Hostname(), allowed))
}

func hostMatchesAny(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		} else if host == pattern {
			return true
		}
	}
	return false
}

func splitHostParameters(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' })
}

func extractionString(value interface{}) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}

func extractionInt(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func extractionFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func cloneMetadata(metadata map[string]interface{}) map[string]interface{} {
	if metadata == nil {
		return nil
	}
	capacity := len(metadata)
	if capacity <= math.MaxInt-2 {
		capacity += 2
	}
	clone := make(map[string]interface{}, capacity)
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

func markCurrentNavigation(ctx context.Context, session BrowserSession, visited map[string]struct{}) {
	current, err := session.CurrentURL(ctx)
	if err == nil && strings.TrimSpace(current) != "" {
		visited[normalizedComparisonURL(current)] = struct{}{}
	}
}

func normalizedComparisonURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

func sameNormalizedURL(left, right string) bool {
	return normalizedComparisonURL(left) == normalizedComparisonURL(right)
}
