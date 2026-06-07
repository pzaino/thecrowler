package infoseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const browserFixtureQuery = "hermetic browser discovery"

func TestBrowserSearchFakeDriverEndToEnd(t *testing.T) {
	handler := openInformationSeedE2ESQLiteDB(t)
	driver := newFixtureWebDriver("http://fixture.test/index.html")
	sessions := &fixtureSessionProvider{driver: driver}
	runner, seed := newBrowserSearchRunner(t, handler, "http://fixture.test", sessions)

	result, err := runner.RunSeed(context.Background(), seed)
	if err != nil {
		t.Fatalf("RunSeed() error = %v", err)
	}
	if result.CandidatesFound != 3 || result.Candidates != 3 || result.SourcesCreated != 3 || result.Linked != 3 {
		t.Fatalf("RunSeed() result = %#v, want three capped and persisted candidates", result)
	}
	if driver.query != browserFixtureQuery || !driver.consentAccepted || driver.page != 2 {
		t.Fatalf("fixture actions: query=%q consent=%t page=%d", driver.query, driver.consentAccepted, driver.page)
	}
	if sessions.acquireCount != 1 || sessions.lease.releaseCount != 1 || sessions.session.closeCount != 1 {
		t.Fatalf("lease lifecycle: acquire=%d close=%d release=%d", sessions.acquireCount, sessions.session.closeCount, sessions.lease.releaseCount)
	}

	assertBrowserDiscoveryRows(t, handler, seed.ID)
}

func TestBrowserSearchFakeDriverCancellationReleasesLease(t *testing.T) {
	driver := newFixtureWebDriver("http://fixture.test/index.html")
	started := make(chan struct{})
	sessions := &fixtureSessionProvider{driver: driver, blockNavigation: true, navigationStarted: started}
	provider := newBrowserSearchProvider(sessions)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	_, err := provider.Search(ctx, browserFixtureQuery, browserSearchOptions("http://fixture.test"))
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Search() error = %v, want context cancellation", err)
	}
	if sessions.acquireCount != 1 || sessions.lease.releaseCount != 1 || sessions.session.closeCount != 1 {
		t.Fatalf("canceled lease lifecycle: acquire=%d close=%d release=%d", sessions.acquireCount, sessions.session.closeCount, sessions.lease.releaseCount)
	}
}

func newBrowserSearchRunner(t *testing.T, handler *cdb.Handler, fixtureBaseURL string, sessions searchproviders.BrowserSessionProvider) (*Runner, cdb.InformationSeed) {
	t.Helper()
	seedConfig := json.RawMessage(`{
		"queries":["{{ .InformationSeed }}"],
		"providers":["browser_fixture"],
		"source_name_template":"{{ .Candidate.Title }}",
		"source_priority":"normal",
		"create_sources":true,
		"link_existing_sources":true,
		"status":"new",
		"restricted":2
	}`)
	seedID, err := cdb.CreateInformationSeed(handler, &cdb.InformationSeed{
		InformationSeed: browserFixtureQuery,
		CategoryID:      7,
		UsrID:           11,
		Config:          &seedConfig,
	})
	if err != nil {
		t.Fatalf("CreateInformationSeed() error = %v", err)
	}
	seed, err := cdb.GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("GetInformationSeedByID() error = %v", err)
	}
	providerConfig := browserProviderConfig(fixtureBaseURL)
	config := cfg.InformationSeedConfig{
		Enabled:              true,
		ProviderAllowList:    []string{"browser_fixture"},
		MaxConcurrentSeeds:   1,
		MaxQueriesPerSeed:    1,
		MaxCandidatesPerSeed: 10,
		ProcessingTimeout:    "5s",
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"browser_fixture": providerConfig,
		},
	}
	runner := NewRunner(handler, config, RunnerOptions{Browser: BrowserDependencies{
		Sessions: sessions,
		Actions:  applicationBrowserActions{},
		Scraper:  applicationBrowserScraper{},
		Rules:    browserFixtureRules(),
	}})
	return runner, *seed
}

func newBrowserSearchProvider(sessions searchproviders.BrowserSessionProvider) *searchproviders.BrowserSearchProvider {
	return &searchproviders.BrowserSearchProvider{
		ProviderName: "browser_fixture",
		Sessions:     sessions,
		Actions:      applicationBrowserActions{},
		Scraper:      applicationBrowserScraper{},
		Rules:        browserFixtureRules(),
	}
}

func browserProviderConfig(fixtureBaseURL string) cfg.InformationSeedProviderConfig {
	host, _ := url.Parse(fixtureBaseURL)
	return cfg.InformationSeedProviderConfig{
		Provider:    searchproviders.ProviderBrowserSearch,
		Transport:   searchproviders.BrowserTransportWebDriver,
		Host:        fixtureBaseURL,
		Endpoint:    "/index.html",
		Timeout:     3,
		PageSize:    10,
		MaxPages:    2,
		MaxRequests: 2,
		Browser: cfg.InformationSeedBrowserConfig{
			NavigationTimeout:      2,
			PageReadinessTimeout:   2,
			ConsentActions:         []string{"accept fixture consent"},
			QueryActions:           []string{"enter fixture query", "submit fixture query"},
			PaginationActions:      []string{"next fixture page"},
			ScrapingRules:          []string{"fixture rendered results"},
			AllowedNavigationHosts: []string{host.Hostname()},
			MaxPages:               2,
			MaxRequests:            2,
			MaxCandidates:          3,
		},
	}
}

func browserSearchOptions(fixtureBaseURL string) searchproviders.Options {
	provider := browserProviderConfig(fixtureBaseURL)
	return providerOptions("browser_fixture", provider)
}

func browserFixtureRules() *browserRuleSnapshot {
	selector := func(css string) rules.Selector {
		return rules.Selector{SelectorType: "css", Selector: css}
	}
	querySelector := selector("#query")
	urlSelector := selector(".result-url")
	urlSelector.Extract.Type = "attribute"
	urlSelector.Extract.Pattern = "href"
	urlSelector.ExtractAllOccurrences = true
	titleSelector := selector(".result-title")
	titleSelector.Extract.Type = "text"
	titleSelector.ExtractAllOccurrences = true
	snippetSelector := selector(".result-snippet")
	snippetSelector.Extract.Type = "text"
	snippetSelector.ExtractAllOccurrences = true
	return &browserRuleSnapshot{
		actions: map[string]rules.ActionRule{
			"accept fixture consent": {RuleName: "accept fixture consent", ActionType: "click", Selectors: []rules.Selector{selector("#accept-consent")}},
			"enter fixture query":    {RuleName: "enter fixture query", ActionType: "input_text", Value: browserFixtureQuery, Selectors: []rules.Selector{querySelector}},
			"submit fixture query":   {RuleName: "submit fixture query", ActionType: "click", Selectors: []rules.Selector{selector("#submit-query")}},
			"next fixture page":      {RuleName: "next fixture page", ActionType: "click", Selectors: []rules.Selector{selector("#next-page")}},
		},
		scraping: map[string]rules.ScrapingRule{
			"fixture rendered results": {
				RuleName: "fixture rendered results",
				Elements: []rules.Element{
					{Key: "url", Critical: true, Selectors: []rules.Selector{urlSelector}},
					{Key: "title", Selectors: []rules.Selector{titleSelector}},
					{Key: "snippet", Selectors: []rules.Selector{snippetSelector}},
				},
			},
		},
	}
}

func assertBrowserDiscoveryRows(t *testing.T, handler *cdb.Handler, seedID uint64) {
	t.Helper()
	rows, err := (*handler).ExecuteQuery(`
		SELECT src.url, candidate.provider, candidate.query, candidate.rank, candidate.score,
		       candidate.metadata, idx.discovery_provider, idx.discovery_query, idx.discovery_rank,
		       idx.candidate_score, idx.discovery_metadata
		FROM InformationSeedCandidate candidate
		JOIN Sources src ON src.url = candidate.normalized_url
		JOIN SourceInformationSeedIndex idx ON idx.source_id = src.source_id AND idx.information_seed_id = candidate.information_seed_id
		WHERE candidate.information_seed_id = $1 AND candidate.decision_status = 'accepted'
		ORDER BY candidate.rank`, seedID)
	if err != nil {
		t.Fatalf("select browser discovery rows: %v", err)
	}
	defer rows.Close()
	wantURLs := []string{
		"https://alpha.example.test/article",
		"https://beta.example.test/report",
		"https://gamma.example.test/news",
	}
	count := 0
	for rows.Next() {
		var sourceURL, provider, query, candidateMetadata, discoveryProvider, discoveryQuery, discoveryMetadata string
		var rank, discoveryRank int
		var score, discoveryScore float64
		if err := rows.Scan(&sourceURL, &provider, &query, &rank, &score, &candidateMetadata, &discoveryProvider, &discoveryQuery, &discoveryRank, &discoveryScore, &discoveryMetadata); err != nil {
			t.Fatalf("scan browser discovery row: %v", err)
		}
		if count >= len(wantURLs) || sourceURL != wantURLs[count] {
			t.Fatalf("candidate %d URL = %q, want %q", count, sourceURL, wantURLs[count])
		}
		if provider != "browser_fixture" || discoveryProvider != provider || query != browserFixtureQuery || discoveryQuery != query || rank != count+1 || discoveryRank != rank || score != discoveryScore {
			t.Fatalf("candidate %d provenance mismatch: provider=%q/%q query=%q/%q rank=%d/%d score=%f/%f", count, provider, discoveryProvider, query, discoveryQuery, rank, discoveryRank, score, discoveryScore)
		}
		page := 1
		if count == 2 {
			page = 2
		}
		for label, metadata := range map[string]string{"candidate": candidateMetadata, "discovery": discoveryMetadata} {
			if !strings.Contains(metadata, `"provider":"browser_fixture"`) || !strings.Contains(metadata, fmt.Sprintf(`"page":%d`, page)) || !strings.Contains(metadata, fmt.Sprintf(`"rank":%d`, rank)) {
				t.Fatalf("%s metadata for candidate %d = %s", label, count, metadata)
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate browser discovery rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("accepted browser discovery rows = %d, want 3", count)
	}
	assertE2ETableCount(t, handler, "Sources", 3)
	assertE2ETableCount(t, handler, "SourceInformationSeedIndex", 3)
	assertE2ETableCount(t, handler, "InformationSeedCandidate", 3)
	assertE2ENoSourceURLLike(t, handler, "%delta.example.test%")
}

type fixtureSessionProvider struct {
	driver            *fixtureWebDriver
	blockNavigation   bool
	navigationStarted chan struct{}
	acquireCount      int
	session           *fixtureBrowserSession
	lease             *fixtureBrowserLease
}

func (p *fixtureSessionProvider) Acquire(ctx context.Context, _ searchproviders.BrowserSessionRequest) (searchproviders.BrowserSession, searchproviders.BrowserSessionLease, error) {
	p.acquireCount++
	p.session = &fixtureBrowserSession{driver: p.driver, blockNavigation: p.blockNavigation, navigationStarted: p.navigationStarted}
	p.lease = &fixtureBrowserLease{}
	return p.session, p.lease, nil
}

type fixtureBrowserSession struct {
	driver            *fixtureWebDriver
	blockNavigation   bool
	navigationStarted chan struct{}
	closeCount        int
}

func (s *fixtureBrowserSession) Navigate(ctx context.Context, target string) error {
	if s.blockNavigation {
		if s.navigationStarted != nil {
			close(s.navigationStarted)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return s.driver.Get(target)
}
func (s *fixtureBrowserSession) CurrentURL(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.driver.CurrentURL()
}
func (s *fixtureBrowserSession) Close(context.Context) error { s.closeCount++; return nil }
func (s *fixtureBrowserSession) webDriver() vdi.WebDriver    { return s.driver }

type fixtureBrowserLease struct{ releaseCount int }

func (l *fixtureBrowserLease) Release(context.Context) error { l.releaseCount++; return nil }

type fixtureWebDriver struct {
	vdi.WebDriver
	mu              sync.Mutex
	currentURL      string
	query           string
	page            int
	consentAccepted bool
}

func newFixtureWebDriver(startURL string) *fixtureWebDriver {
	return &fixtureWebDriver{currentURL: startURL}
}

func (d *fixtureWebDriver) Get(target string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.currentURL = target
	d.page = 0
	d.query = ""
	d.consentAccepted = false
	return nil
}
func (d *fixtureWebDriver) CurrentURL() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.currentURL, nil
}
func (d *fixtureWebDriver) FindElements(by, selector string) ([]vdi.WebElement, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if by != vdi.ByCSSSelector {
		return nil, errors.New("fixture driver supports CSS selectors only")
	}
	switch selector {
	case "#accept-consent":
		if !d.consentAccepted {
			return []vdi.WebElement{&fixtureWebElement{click: func() { d.consentAccepted = true }}}, nil
		}
	case "#query":
		if d.consentAccepted {
			return []vdi.WebElement{&fixtureWebElement{click: func() {}, sendKeys: func(value string) { d.query += value }, clear: func() { d.query = "" }}}, nil
		}
	case "#submit-query":
		if d.consentAccepted {
			return []vdi.WebElement{&fixtureWebElement{click: func() { d.page = 1; d.setResultsURLLocked(1) }}}, nil
		}
	case "#next-page":
		if d.page == 1 {
			return []vdi.WebElement{&fixtureWebElement{click: func() { d.page = 2; d.setPageURLLocked(2) }}}, nil
		}
	}
	return nil, fmt.Errorf("fixture element %q not found", selector)
}
func (d *fixtureWebDriver) PageSource() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.page {
	case 1:
		return fixtureResultsHTML([][3]string{
			{"https://alpha.example.test/article", "Alpha rendered result", "Rendered on fixture page one."},
			{"https://beta.example.test/report", "Beta rendered result", "Second result on fixture page one."},
		}), nil
	case 2:
		return fixtureResultsHTML([][3]string{
			{"https://gamma.example.test/news", "Gamma rendered result", "Rendered on fixture page two."},
			{"https://delta.example.test/ignored-by-cap", "Delta capped result", "This result proves the candidate cap."},
		}), nil
	default:
		return `<html><body><section id="consent-wall"></section><form id="query-form"></form></body></html>`, nil
	}
}
func (d *fixtureWebDriver) setResultsURLLocked(page int) {
	if page == 1 {
		parsed, _ := url.Parse(d.currentURL)
		parsed.Path = strings.TrimSuffix(parsed.Path, "/index.html") + "/results.html"
		d.currentURL = parsed.String()
	}
	d.setPageURLLocked(page)
}

func (d *fixtureWebDriver) setPageURLLocked(page int) {
	parsed, _ := url.Parse(d.currentURL)
	values := parsed.Query()
	values.Set("q", d.query)
	values.Set("page", fmt.Sprint(page))
	parsed.RawQuery = values.Encode()
	d.currentURL = parsed.String()
}

func fixtureResultsHTML(records [][3]string) string {
	var body strings.Builder
	body.WriteString("<html><body><main id=\"results\">")
	for _, record := range records {
		fmt.Fprintf(&body, `<article class="result"><a class="result-url" href="%s"><h2 class="result-title">%s</h2></a><p class="result-snippet">%s</p></article>`, record[0], record[1], record[2])
	}
	body.WriteString("</main></body></html>")
	return body.String()
}

type fixtureWebElement struct {
	vdi.WebElement
	click    func()
	sendKeys func(string)
	clear    func()
}

func (e *fixtureWebElement) Click() error {
	if e.click != nil {
		e.click()
	}
	return nil
}
func (e *fixtureWebElement) SendKeys(value string) error {
	if e.sendKeys != nil {
		e.sendKeys(value)
	}
	return nil
}
func (e *fixtureWebElement) Clear() error {
	if e.clear != nil {
		e.clear()
	}
	return nil
}
func (e *fixtureWebElement) Text() (string, error) { return browserFixtureQuery, nil }
