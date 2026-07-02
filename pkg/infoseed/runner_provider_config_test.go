package infoseed

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
)

type recordingProvider struct {
	calls   int
	options []searchproviders.Options
}

func (p *recordingProvider) Name() string { return "allowed" }

func (p *recordingProvider) Search(ctx context.Context, query string, options searchproviders.Options) ([]searchproviders.Result, error) {
	p.calls++
	p.options = append(p.options, options)
	return []searchproviders.Result{{URL: fmt.Sprintf("https://example.com/%d", p.calls), Rank: 1}}, nil
}

func TestBuildProvidersHonorsAllowList(t *testing.T) {
	providers := BuildProviders(cfg.InformationSeedConfig{
		ProviderAllowList: []string{"allowed"},
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"allowed": {Provider: "http_json"},
			"blocked": {Provider: "http_json"},
		},
	})
	if providers["allowed"] == nil {
		t.Fatal("expected allowed provider to be built")
	}
	if providers["blocked"] != nil {
		t.Fatal("expected provider outside allow-list to be removed")
	}
}

func TestQueryProvidersSendsConfigAndEnforcesCaps(t *testing.T) {
	provider := &recordingProvider{}
	runner := &Runner{
		Config: cfg.InformationSeedConfig{
			MaxConcurrentSeeds: 4,
			MaxQueriesPerSeed:  5,
			ProviderAllowList:  []string{"allowed"},
			Providers: map[string]cfg.InformationSeedProviderConfig{
				"allowed": {
					Provider:    "http_json",
					Host:        "https://example.invalid",
					MaxRequests: 3,
					MaxPages:    2,
					PageSize:    7,
					Parameters:  map[string]string{"safe": "value", "api_key": "${INFORMATION_SEED_API_KEY}"},
					Headers:     map[string]string{"X-Request-ID": "${INFORMATION_SEED_REQUEST_ID}", "Authorization": "Bearer ${INFORMATION_SEED_TOKEN}"},
				},
			},
		},
		Providers: map[string]searchproviders.Provider{"allowed": provider},
	}

	queries := []string{"one", "two", "three", "four", "five"}
	candidates, err := runner.queryProviders(context.Background(), cdb.InformationSeed{ID: 42, InformationSeed: "seed"}, SeedRunConfig{Providers: []string{"blocked", "allowed"}}, queries)
	if err != nil {
		t.Fatalf("queryProviders returned error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected MaxRequests/MaxPages cap to allow 1 provider search, got %d", provider.calls)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %d", len(candidates))
	}
	if len(provider.options) != 1 {
		t.Fatalf("expected one options capture, got %d", len(provider.options))
	}
	options := provider.options[0]
	if options.PageSize != 7 || options.MaxPages != 2 || options.MaxRequests != 3 {
		t.Fatalf("unexpected caps in options: %#v", options)
	}
	if options.Parameters["safe"] != "value" || options.Parameters["api_key"] != "${INFORMATION_SEED_API_KEY}" {
		t.Fatalf("parameters not passed: %#v", options.Parameters)
	}
	if options.Headers["X-Request-ID"] != "${INFORMATION_SEED_REQUEST_ID}" || options.Headers["Authorization"] != "Bearer ${INFORMATION_SEED_TOKEN}" {
		t.Fatalf("headers not passed: %#v", options.Headers)
	}

	options.Parameters["safe"] = "mutated"
	if got := runner.Config.Providers["allowed"].Parameters["safe"]; got != "value" {
		t.Fatalf("provider options parameters map should not alias config, got %q", got)
	}
}

type failingProvider struct{}

func (p *failingProvider) Name() string { return "failing" }

func (p *failingProvider) Search(ctx context.Context, query string, options searchproviders.Options) ([]searchproviders.Result, error) {
	return nil, fmt.Errorf("temporary failure api_key=SHOULD_NOT_LEAK token=SECRET")
}

/*
func TestRunSeedTreatsPartialProviderFailureAsWarning(t *testing.T) {
	handler := openSchedulerSQLiteDB(t)
	defer (*handler).Close()

	seedID, err := cdb.CreateInformationSeed(handler, &cdb.InformationSeed{InformationSeed: "partial seed", Status: "processing"})
	if err != nil {
		t.Fatalf("create seed: %v", err)
	}
	seed, err := cdb.GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}
	runner := &Runner{
		DB: handler,
		Config: cfg.InformationSeedConfig{
			MaxConcurrentSeeds:   2,
			MaxQueriesPerSeed:    1,
			MaxCandidatesPerSeed: 10,
			ProcessingTimeout:    "5s",
			ProviderAllowList:    []string{"allowed", "failing"},
			Providers: map[string]cfg.InformationSeedProviderConfig{
				"allowed": {Provider: "http_json", MaxRequests: 1, MaxPages: 1, PageSize: 1},
				"failing": {Provider: "http_json", MaxRequests: 1, MaxPages: 1, PageSize: 1},
			},
		},
		Providers: map[string]searchproviders.Provider{"allowed": &recordingProvider{}, "failing": &failingProvider{}},
		Now:       func() time.Time { return time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC) },
	}

	result, err := runner.RunSeed(context.Background(), *seed)
	if err != nil {
		t.Fatalf("RunSeed returned error for partial provider failure: %v", err)
	}
	if result.Linked != 1 {
		t.Fatalf("expected one linked candidate, got %#v", result)
	}
	updated, err := cdb.GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("get updated seed: %v", err)
	}
	if updated.Status != "completed" || updated.LastError.Valid {
		t.Fatalf("expected completed seed without last error, got status=%q last_error=%v", updated.Status, updated.LastError)
	}

	var severity, details string
	err = (*handler).QueryRow(`SELECT event_severity, details FROM Events WHERE event_type = ? ORDER BY created_at DESC LIMIT 1`, informationSeedDiscoveryCompleted).Scan(&severity, &details)
	if err != nil {
		t.Fatalf("query completed event: %v", err)
	}
	if severity != cdb.EventSeverityWarning {
		t.Fatalf("expected warning completion event, got %q", severity)
	}
	if strings.Contains(details, "SHOULD_NOT_LEAK") || strings.Contains(details, "SECRET") {
		t.Fatalf("event details leaked secret: %s", details)
	}
	if !strings.Contains(details, `"failing":{"errors":1}`) {
		t.Fatalf("event details missing compact provider error metric: %s", details)
	}
}
*/

func TestInformationSeedEventPayloadRedactsErrorSummaries(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addError(providerQueryError{SeedID: 7, Failures: []providerFailure{{Provider: "bad", Summary: "bad: provider failed api_key=SHOULD_NOT_LEAK authorization: SECRET"}}})
	payload := informationSeedEventPayload(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, 0, stats)
	summaries := payload["error_summaries"].([]string)
	if len(summaries) != 1 {
		t.Fatalf("expected one summary, got %#v", summaries)
	}
	if strings.Contains(summaries[0], "SHOULD_NOT_LEAK") || strings.Contains(summaries[0], "SECRET") {
		t.Fatalf("summary leaked secret: %q", summaries[0])
	}
	metrics := payload["provider_metrics"].(map[string]map[string]int)
	if metrics["bad"]["errors"] != 1 {
		t.Fatalf("expected provider error metric, got %#v", metrics)
	}
}

type stubBrowserSessions struct{}

func (stubBrowserSessions) Acquire(context.Context, searchproviders.BrowserSessionRequest) (searchproviders.BrowserSession, searchproviders.BrowserSessionLease, error) {
	return nil, nil, nil
}

type stubBrowserActions struct{}

func (stubBrowserActions) Execute(context.Context, searchproviders.BrowserSession, searchproviders.BrowserActionRequest) error {
	return nil
}

type stubBrowserScraper struct{}

func (stubBrowserScraper) Scrape(context.Context, searchproviders.BrowserSession, []searchproviders.BrowserScrapingRule) ([]map[string]interface{}, error) {
	return nil, nil
}

type stubBrowserRules struct{}

func (stubBrowserRules) ResolveActionRules(context.Context, []string) ([]searchproviders.BrowserActionRule, error) {
	return nil, nil
}

func (stubBrowserRules) ResolveScrapingRules(context.Context, []string) ([]searchproviders.BrowserScrapingRule, error) {
	return nil, nil
}

func TestBuildProvidersInjectsWebDriverDependencies(t *testing.T) {
	dependencies := BrowserDependencies{
		Sessions: stubBrowserSessions{},
		Actions:  stubBrowserActions{},
		Scraper:  stubBrowserScraper{},
		Rules:    stubBrowserRules{},
	}
	providers := BuildProviders(cfg.InformationSeedConfig{
		ProviderAllowList: []string{"browser"},
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"browser": {Provider: "browser_search", Transport: searchproviders.BrowserTransportWebDriver},
		},
	}, dependencies)

	provider, ok := providers["browser"].(*searchproviders.BrowserSearchProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *searchproviders.BrowserSearchProvider", providers["browser"])
	}
	if provider.Sessions == nil || provider.Actions == nil || provider.Scraper == nil || provider.Rules == nil {
		t.Fatal("expected all WebDriver dependencies to be injected")
	}
}

func TestHTTPOnlyRunnerNeedsNoBrowserDependencies(t *testing.T) {
	runner := NewRunner(nil, cfg.InformationSeedConfig{
		ProviderAllowList: []string{"http"},
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"http": {Provider: "http_json", Transport: searchproviders.BrowserTransportHTTP},
		},
	})
	if runner.Providers["http"] == nil {
		t.Fatal("expected HTTP provider to be constructed without browser dependencies")
	}
}

func TestWebDriverProviderWithoutDependenciesReturnsConfigurationError(t *testing.T) {
	providers := BuildProviders(cfg.InformationSeedConfig{
		ProviderAllowList: []string{"browser"},
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"browser": {
				Provider:  "browser_search",
				Transport: searchproviders.BrowserTransportWebDriver,
				Host:      "https://search.example",
				Timeout:   1,
			},
		},
	})
	_, err := providers["browser"].Search(context.Background(), "seed", searchproviders.Options{
		Host:      "https://search.example",
		Transport: searchproviders.BrowserTransportWebDriver,
		Timeout:   time.Second,
	})
	if err == nil {
		t.Fatal("expected missing WebDriver dependencies to return an error")
	}
	message := err.Error()
	for _, expected := range []string{"provider browser", "configuration", "browser sessions", "read-only rule resolution"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("error %q does not contain %q", message, expected)
		}
	}
}
