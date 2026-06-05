package infoseed

import (
	"context"
	"fmt"
	"testing"

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
