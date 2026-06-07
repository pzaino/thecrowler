package searchproviders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBrowserSession struct {
	mu           sync.Mutex
	current      string
	navigations  []string
	closed       int
	navigateErr  error
	currentErr   error
	closeErr     error
	waitNavigate bool
}

func (s *fakeBrowserSession) Navigate(ctx context.Context, target string) error {
	if s.waitNavigate {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.navigateErr != nil {
		return s.navigateErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = target
	s.navigations = append(s.navigations, target)
	return nil
}

func (s *fakeBrowserSession) CurrentURL(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentErr != nil {
		return "", s.currentErr
	}
	return s.current, nil
}

func (s *fakeBrowserSession) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return s.closeErr
}

func (s *fakeBrowserSession) setCurrent(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = target
}

type fakeBrowserLease struct {
	mu         sync.Mutex
	released   int
	releaseErr error
}

func (l *fakeBrowserLease) Release(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released++
	return l.releaseErr
}

type fakeSessionProvider struct {
	session *fakeBrowserSession
	lease   *fakeBrowserLease
	err     error
	request BrowserSessionRequest
}

func (p *fakeSessionProvider) Acquire(_ context.Context, request BrowserSessionRequest) (BrowserSession, BrowserSessionLease, error) {
	p.request = request
	if p.err != nil {
		return nil, nil, p.err
	}
	return p.session, p.lease, nil
}

type fakeRuleResolver struct {
	actionErr error
	scrapeErr error
}

func (r *fakeRuleResolver) ResolveActionRules(_ context.Context, refs []string) ([]BrowserActionRule, error) {
	if r.actionErr != nil {
		return nil, r.actionErr
	}
	rules := make([]BrowserActionRule, len(refs))
	for idx, ref := range refs {
		rules[idx] = BrowserActionRule{Name: ref}
	}
	return rules, nil
}

func (r *fakeRuleResolver) ResolveScrapingRules(_ context.Context, refs []string) ([]BrowserScrapingRule, error) {
	if r.scrapeErr != nil {
		return nil, r.scrapeErr
	}
	rules := make([]BrowserScrapingRule, len(refs))
	for idx, ref := range refs {
		rules[idx] = BrowserScrapingRule{Name: ref}
	}
	return rules, nil
}

type fakeActionExecutor struct {
	mu           sync.Mutex
	requests     []BrowserActionRequest
	failPhase    string
	failMode     string
	failErr      error
	paginationTo []string
	pagination   int
}

func (e *fakeActionExecutor) Execute(_ context.Context, session BrowserSession, request BrowserActionRequest) error {
	e.mu.Lock()
	e.requests = append(e.requests, request)
	shouldFail := request.Phase == e.failPhase && request.Mode == e.failMode
	if request.Phase == BrowserActionPagination && !shouldFail && e.pagination < len(e.paginationTo) {
		target := e.paginationTo[e.pagination]
		e.pagination++
		e.mu.Unlock()
		session.(*fakeBrowserSession).setCurrent(target)
		return nil
	}
	e.mu.Unlock()
	if shouldFail {
		return e.failErr
	}
	return nil
}

type fakeResultScraper struct {
	mu      sync.Mutex
	pages   [][]map[string]interface{}
	calls   int
	err     error
	waitCtx bool
}

func (s *fakeResultScraper) Scrape(ctx context.Context, _ BrowserSession, _ []BrowserScrapingRule) ([]map[string]interface{}, error) {
	if s.waitCtx {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx >= len(s.pages) {
		return nil, nil
	}
	return s.pages[idx], nil
}

func newWebDriverFixture(records ...[]map[string]interface{}) (*BrowserSearchProvider, *fakeBrowserSession, *fakeBrowserLease, *fakeActionExecutor, *fakeResultScraper) {
	session := &fakeBrowserSession{}
	lease := &fakeBrowserLease{}
	actions := &fakeActionExecutor{}
	scraper := &fakeResultScraper{pages: records}
	provider := &BrowserSearchProvider{
		ProviderName: ProviderBrowserSearch,
		Sessions:     &fakeSessionProvider{session: session, lease: lease},
		Actions:      actions,
		Scraper:      scraper,
		Rules:        &fakeRuleResolver{},
	}
	return provider, session, lease, actions, scraper
}

func webDriverOptions() Options {
	return Options{
		Host:        "https://search.invalid",
		Endpoint:    "/search",
		Transport:   BrowserTransportWebDriver,
		Timeout:     time.Second,
		PageSize:    10,
		MaxPages:    2,
		MaxRequests: 10,
		Browser: BrowserOptions{
			InitialActions:         []string{"prepare"},
			ConsentActions:         []string{"accept-consent"},
			QueryActions:           []string{"submit-query"},
			PaginationActions:      []string{"next-page"},
			ScrapingRules:          []string{"extract-results"},
			AllowedNavigationHosts: []string{"search.invalid"},
			DeniedHosts:            []string{"denied.invalid"},
			MaxPages:               2,
			MaxRequests:            10,
			MaxCandidates:          10,
		},
	}
}

func TestBrowserSearchProviderWebDriverMapsRenderedResultsAndRejectsUnsafeURLs(t *testing.T) {
	provider, session, lease, _, _ := newWebDriverFixture([]map[string]interface{}{
		{"url": "../result/alpha#fragment", "title": " Alpha ", "snippet": "Rendered", "rank": "7", "score": "0.75", "metadata": map[string]interface{}{"kind": "rendered"}},
		{"url": "https://example.com/duplicate"},
		{"url": "https://example.com/duplicate#other"},
		{"url": "javascript:alert(1)"},
		{"url": "https://denied.invalid/result"},
		{"url": "https://search.invalid/search"},
		{"url": "https://bad_host.invalid/result"},
		{"title": "missing URL"},
	})
	options := webDriverOptions()
	options.Browser.MaxPages = 1
	options.Browser.MaxRequests = 10

	results, err := provider.Search(context.Background(), "rendered query", options)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two valid results, got %#v", results)
	}
	if results[0].URL != "https://search.invalid/result/alpha" || results[0].Title != "Alpha" || results[0].Snippet != "Rendered" || results[0].Rank != 7 || results[0].Score != 0.75 {
		t.Fatalf("unexpected mapped result: %#v", results[0])
	}
	if results[0].Metadata["kind"] != "rendered" || results[0].Metadata["provider"] != ProviderBrowserSearch {
		t.Fatalf("unexpected metadata: %#v", results[0].Metadata)
	}
	if len(session.navigations) != 1 || session.navigations[0] != "https://search.invalid/search" {
		t.Fatalf("unexpected bounded navigation: %#v", session.navigations)
	}
	assertWebDriverCleanup(t, session, lease)
}

func TestBrowserSearchProviderWebDriverRunsConsentQueryAndBoundedPagination(t *testing.T) {
	provider, session, lease, actions, scraper := newWebDriverFixture(
		[]map[string]interface{}{{"url": "https://example.com/one"}},
		[]map[string]interface{}{{"url": "https://example.com/two"}},
		[]map[string]interface{}{{"url": "https://example.com/three"}},
	)
	actions.paginationTo = []string{"https://search.invalid/search?page=2", "https://search.invalid/search?page=3"}
	options := webDriverOptions()
	options.MaxPages = 99
	options.MaxRequests = 99
	options.Browser.MaxPages = 99
	options.Browser.MaxRequests = 99

	results, err := provider.Search(context.Background(), "bounded query", options)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != browserSearchMaxPages || scraper.calls != browserSearchMaxPages {
		t.Fatalf("pagination escaped caps: results=%d scrapes=%d", len(results), scraper.calls)
	}
	var phases []string
	for _, request := range actions.requests {
		phases = append(phases, request.Phase)
		if request.Phase == BrowserActionQuery && request.Query != "bounded query" {
			t.Fatalf("query action received %q", request.Query)
		}
	}
	if strings.Join(phases, ",") != "initial,consent,query,pagination" {
		t.Fatalf("action phases = %#v", phases)
	}
	assertWebDriverCleanup(t, session, lease)
}

func TestBrowserSearchProviderWebDriverEmptyAndMalformedExtraction(t *testing.T) {
	provider, session, lease, _, _ := newWebDriverFixture([]map[string]interface{}{
		{}, {"url": 42}, {"url": ""}, {"url": ":// malformed"}, {"url": "ftp://example.com/file"},
	})
	options := webDriverOptions()
	options.Browser.MaxPages = 1
	results, err := provider.Search(context.Background(), "empty", options)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected malformed records to be skipped, got %#v", results)
	}
	assertWebDriverCleanup(t, session, lease)
}

func TestBrowserSearchProviderWebDriverCriticalFailuresCleanup(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BrowserSearchProvider, *fakeBrowserSession, *fakeActionExecutor, *fakeResultScraper)
	}{
		{name: "rule resolution", mutate: func(p *BrowserSearchProvider, _ *fakeBrowserSession, _ *fakeActionExecutor, _ *fakeResultScraper) {
			p.Rules = &fakeRuleResolver{scrapeErr: errors.New("rules failed")}
		}},
		{name: "navigation", mutate: func(_ *BrowserSearchProvider, s *fakeBrowserSession, _ *fakeActionExecutor, _ *fakeResultScraper) {
			s.navigateErr = errors.New("navigation failed")
		}},
		{name: "action", mutate: func(_ *BrowserSearchProvider, _ *fakeBrowserSession, a *fakeActionExecutor, _ *fakeResultScraper) {
			a.failPhase, a.failMode, a.failErr = BrowserActionQuery, BrowserActionModeSelenium, errors.New("action failed")
		}},
		{name: "scrape", mutate: func(_ *BrowserSearchProvider, _ *fakeBrowserSession, _ *fakeActionExecutor, s *fakeResultScraper) {
			s.err = errors.New("scrape failed")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, session, lease, actions, scraper := newWebDriverFixture(nil)
			tc.mutate(provider, session, actions, scraper)
			_, err := provider.Search(context.Background(), "failure", webDriverOptions())
			if err == nil {
				t.Fatal("expected critical failure")
			}
			if tc.name == "rule resolution" {
				if session.closed != 0 || lease.released != 0 {
					t.Fatalf("session should not be acquired before rule resolution: close=%d release=%d", session.closed, lease.released)
				}
				return
			}
			assertWebDriverCleanup(t, session, lease)
		})
	}
}

func TestBrowserSearchProviderWebDriverTimeoutAndCancellationCleanup(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		provider, session, lease, _, _ := newWebDriverFixture(nil)
		session.waitNavigate = true
		options := webDriverOptions()
		options.Timeout = 20 * time.Millisecond
		options.Browser.NavigationTimeout = 10 * time.Millisecond
		_, err := provider.Search(context.Background(), "timeout", options)
		if !errors.Is(errors.Unwrap(err), context.DeadlineExceeded) && !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
			t.Fatalf("expected deadline error, got %v", err)
		}
		assertWebDriverCleanup(t, session, lease)
	})
	t.Run("cancellation", func(t *testing.T) {
		provider, session, lease, _, scraper := newWebDriverFixture(nil)
		scraper.waitCtx = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := provider.Search(ctx, "cancel", webDriverOptions())
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
		assertWebDriverCleanup(t, session, lease)
	})
}

func TestBrowserSearchProviderWebDriverHBSFallback(t *testing.T) {
	provider, session, lease, actions, _ := newWebDriverFixture([]map[string]interface{}{{"url": "https://example.com/result"}})
	actions.failPhase = BrowserActionQuery
	actions.failMode = BrowserActionModeHBS
	actions.failErr = errors.New("hbs unavailable")
	options := webDriverOptions()
	options.Browser.HBSEnabled = true
	options.Browser.SeleniumFallback = true
	options.Browser.MaxPages = 1

	results, err := provider.Search(context.Background(), "fallback", options)
	if err != nil || len(results) != 1 {
		t.Fatalf("fallback search: results=%#v err=%v", results, err)
	}
	var queryModes []string
	for _, request := range actions.requests {
		if request.Phase == BrowserActionQuery {
			queryModes = append(queryModes, request.Mode)
		}
	}
	if strings.Join(queryModes, ",") != "hbs,selenium" {
		t.Fatalf("query modes = %#v", queryModes)
	}
	assertWebDriverCleanup(t, session, lease)
}

func TestBrowserSearchProviderWebDriverErrorsAreSecretSafe(t *testing.T) {
	provider, session, lease, _, scraper := newWebDriverFixture(nil)
	scraper.err = errors.New("scrape failed api_key=SECRET_PROVIDER_KEY token=SECRET_PROVIDER_TOKEN")
	_, err := provider.Search(context.Background(), "secret", webDriverOptions())
	assertRedactedError(t, err, "scrape failed")
	assertWebDriverCleanup(t, session, lease)
}

func assertWebDriverCleanup(t *testing.T, session *fakeBrowserSession, lease *fakeBrowserLease) {
	t.Helper()
	if session.closed != 1 || lease.released != 1 {
		t.Fatalf("cleanup counts: session close=%d lease release=%d", session.closed, lease.released)
	}
}

func TestBrowserDiagnosticsBoundLabelsAndNeverStoreSensitivePayloads(t *testing.T) {
	diagnostics := &BrowserDiagnostics{}
	diagnostics.Observe(BrowserDiagnostic{
		Operation: "navigation:https://user:password@example.test/?token=secret",
		Outcome:   "Authorization: Bearer credential",
		Reason:    "<html>secret</html>",
		Count:     1,
	})
	counts, _ := diagnostics.Snapshot()
	if got := counts[BrowserDiagnosticBudgetExhausted][BrowserOutcomeFailure+":"+BrowserReasonOperationFailure]; got != 1 {
		t.Fatalf("unexpected bounded diagnostics: %#v", counts)
	}
	encoded := fmt.Sprintf("%v", counts)
	for _, secret := range []string{"password", "credential", "<html>", "token=secret"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, encoded)
		}
	}
}

func TestBrowserSearchProviderWebDriverEnforcesSharedOperationBudget(t *testing.T) {
	provider, session, lease, actions, scraper := newWebDriverFixture([]map[string]interface{}{{"url": "https://example.test/result"}})
	options := webDriverOptions()
	options.Browser.MaxRequests = 2
	options.Diagnostics = &BrowserDiagnostics{}

	_, err := provider.Search(context.Background(), "budget", options)
	if err == nil || !strings.Contains(err.Error(), "operation budget exhausted") {
		t.Fatalf("expected bounded operation error, got %v", err)
	}
	if len(session.navigations) != 1 || len(actions.requests) != 1 || scraper.calls != 0 {
		t.Fatalf("budget was not shared: navigations=%d actions=%d scrapes=%d", len(session.navigations), len(actions.requests), scraper.calls)
	}
	if session.closed != 1 || lease.released != 1 {
		t.Fatalf("cleanup counts: close=%d release=%d", session.closed, lease.released)
	}
	counts, _ := options.Diagnostics.Snapshot()
	if counts[BrowserDiagnosticBudgetExhausted][BrowserOutcomeFailure+":"+BrowserReasonBudgetExhausted] != 1 {
		t.Fatalf("missing budget diagnostic: %#v", counts)
	}
}

func TestBrowserSearchProviderWebDriverRecordsBoundedDiscoveryDiagnostics(t *testing.T) {
	provider, _, _, _, _ := newWebDriverFixture([]map[string]interface{}{
		{"url": "https://example.test/result"},
		{"url": "javascript:alert(1)"},
		{"title": "missing"},
	})
	options := webDriverOptions()
	options.Browser.MaxPages = 1
	options.Diagnostics = &BrowserDiagnostics{}

	results, err := provider.Search(context.Background(), "diagnostics", options)
	if err != nil || len(results) != 1 {
		t.Fatalf("Search() results=%#v error=%v", results, err)
	}
	counts, _ := options.Diagnostics.Snapshot()
	checks := map[string]int{
		BrowserDiagnosticLeaseWait + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:           1,
		BrowserDiagnosticNavigation + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:          1,
		BrowserDiagnosticScraping + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:            1,
		BrowserDiagnosticPages + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:               1,
		BrowserDiagnosticRawRecords + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:          3,
		BrowserDiagnosticAcceptedCandidates + "/" + BrowserOutcomeSuccess + ":" + BrowserReasonNone:  1,
		BrowserDiagnosticURLRejections + "/" + BrowserOutcomeSkipped + ":" + BrowserReasonInvalidURL: 1,
		BrowserDiagnosticURLRejections + "/" + BrowserOutcomeSkipped + ":" + BrowserReasonMissingURL: 1,
	}
	for key, want := range checks {
		parts := strings.SplitN(key, "/", 2)
		if got := counts[parts[0]][parts[1]]; got != want {
			t.Errorf("diagnostic %s=%d, want %d; all=%#v", key, got, want, counts)
		}
	}
}

func TestBrowserSearchProviderWebDriverRecordsCleanupFailuresWithoutErrorDetails(t *testing.T) {
	provider, session, lease, _, _ := newWebDriverFixture([]map[string]interface{}{{"url": "https://example.test/result"}})
	session.closeErr = errors.New("close Authorization:Bearer_cleanup-secret")
	lease.releaseErr = errors.New("release token=cleanup-secret")
	options := webDriverOptions()
	options.Browser.MaxPages = 1
	options.Diagnostics = &BrowserDiagnostics{}

	_, err := provider.Search(context.Background(), "cleanup", options)
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if strings.Contains(err.Error(), "cleanup-secret") {
		t.Fatalf("cleanup error leaked secret: %v", err)
	}
	counts, _ := options.Diagnostics.Snapshot()
	if counts[BrowserDiagnosticCleanupFailures][BrowserOutcomeFailure+":"+BrowserReasonSessionClose] != 1 ||
		counts[BrowserDiagnosticCleanupFailures][BrowserOutcomeFailure+":"+BrowserReasonLeaseRelease] != 1 {
		t.Fatalf("missing bounded cleanup diagnostics: %#v", counts)
	}
}
