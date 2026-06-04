package searchproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJSONProviderSearchParsesCommonResultShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "seed query" {
			t.Fatalf("expected query parameter to be set, got %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{"url": "https://example.com", "title": "Example", "score": 0.8}},
		})
	}))
	defer server.Close()

	provider := &JSONProvider{ProviderName: "test"}
	results, err := provider.Search(context.Background(), "seed query", Options{Host: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com" || results[0].Title != "Example" || results[0].Rank != 1 {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestNewProviderSelectsNativeAdaptersAndPreservesGenericJSON(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		wantProvider string
	}{
		{name: "brave_search", wantProvider: "*searchproviders.BraveProvider"},
		{name: "custom", provider: "brave", wantProvider: "*searchproviders.BraveProvider"},
		{name: "bing_web_search", wantProvider: "*searchproviders.BingProvider"},
		{name: "custom_json", provider: "http_json", wantProvider: "*searchproviders.JSONProvider"},
		{name: "legacy_adapter", provider: "unknown", wantProvider: "*searchproviders.JSONProvider"},
	}
	for _, tc := range tests {
		provider := NewProvider(tc.name, tc.provider)
		if got := strings.TrimPrefix(typeName(provider), "github.com/pzaino/thecrowler/pkg/infoseed/searchproviders."); got != tc.wantProvider {
			t.Fatalf("NewProvider(%q, %q) = %s, want %s", tc.name, tc.provider, got, tc.wantProvider)
		}
	}
}

func TestBraveProviderFixtureResponses(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		provider := &BraveProvider{ProviderName: "brave_search"}
		results, err := provider.Search(context.Background(), "seed query", fixtureOptions(t, "brave_success.json", http.StatusOK, func(r *http.Request) {
			if got := r.URL.Path; got != "/res/v1/web/search" {
				t.Fatalf("path = %q", got)
			}
			if got := r.URL.Query().Get("q"); got != "seed query" {
				t.Fatalf("q = %q", got)
			}
			if got := r.Header.Get("X-Subscription-Token"); got != "SECRET_PROVIDER_KEY" {
				t.Fatalf("missing Brave subscription header")
			}
		}))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		assertResult(t, results, "https://example.com/", "Example Domain", "Example snippet from Brave.", 1)
		if results[0].Score != 1 {
			t.Fatalf("score = %v", results[0].Score)
		}
		if results[0].Metadata["pagination"] == nil || results[0].Metadata["evidence"] == nil {
			t.Fatalf("expected metadata with pagination and evidence: %#v", results[0].Metadata)
		}
	})

	t.Run("empty", func(t *testing.T) {
		provider := &BraveProvider{ProviderName: "brave_search"}
		results, err := provider.Search(context.Background(), "empty", fixtureOptions(t, "brave_empty.json", http.StatusOK, nil))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected empty results, got %#v", results)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		provider := &BraveProvider{ProviderName: "brave_search"}
		_, err := provider.Search(context.Background(), "bad", fixtureOptions(t, "brave_malformed.json", http.StatusOK, nil))
		if err == nil || !strings.Contains(err.Error(), "malformed brave_search response") {
			t.Fatalf("expected malformed error, got %v", err)
		}
	})

	t.Run("provider error is redacted", func(t *testing.T) {
		provider := &BraveProvider{ProviderName: "brave_search"}
		_, err := provider.Search(context.Background(), "error", fixtureOptions(t, "brave_error.json", http.StatusUnauthorized, nil))
		assertRedactedError(t, err, "provider brave_search returned status 401")
	})
}

func TestBingProviderFixtureResponses(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		provider := &BingProvider{ProviderName: "bing_web_search"}
		results, err := provider.Search(context.Background(), "seed query", fixtureOptions(t, "bing_success.json", http.StatusOK, func(r *http.Request) {
			if got := r.URL.Path; got != "/v7.0/search" {
				t.Fatalf("path = %q", got)
			}
			if got := r.URL.Query().Get("q"); got != "seed query" {
				t.Fatalf("q = %q", got)
			}
			if got := r.Header.Get("Ocp-Apim-Subscription-Key"); got != "SECRET_PROVIDER_KEY" {
				t.Fatalf("missing Bing subscription header")
			}
		}))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		assertResult(t, results, "https://example.com/", "Example Domain", "Example snippet from Bing.", 1)
		if results[0].Metadata["pagination"] == nil || results[0].Metadata["displayUrl"] != "example.com" {
			t.Fatalf("expected metadata with pagination and evidence: %#v", results[0].Metadata)
		}
	})

	t.Run("empty", func(t *testing.T) {
		provider := &BingProvider{ProviderName: "bing_web_search"}
		results, err := provider.Search(context.Background(), "empty", fixtureOptions(t, "bing_empty.json", http.StatusOK, nil))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected empty results, got %#v", results)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		provider := &BingProvider{ProviderName: "bing_web_search"}
		_, err := provider.Search(context.Background(), "bad", fixtureOptions(t, "bing_malformed.json", http.StatusOK, nil))
		if err == nil || !strings.Contains(err.Error(), "malformed bing_web_search response") {
			t.Fatalf("expected malformed error, got %v", err)
		}
	})

	t.Run("provider error is redacted", func(t *testing.T) {
		provider := &BingProvider{ProviderName: "bing_web_search"}
		_, err := provider.Search(context.Background(), "error", fixtureOptions(t, "bing_error.json", http.StatusUnauthorized, nil))
		assertRedactedError(t, err, "provider bing_web_search returned status 401")
	})
}

func TestGenericJSONProviderFixtureResponses(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		provider := &JSONProvider{ProviderName: "public_json"}
		results, err := provider.Search(context.Background(), "seed query", fixtureOptions(t, "generic_success.json", http.StatusOK, nil))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		assertResult(t, results, "https://example.net/", "Generic Example", "Generic snippet.", 1)
		if results[0].Score != 0.75 {
			t.Fatalf("score = %v", results[0].Score)
		}
	})

	t.Run("empty", func(t *testing.T) {
		provider := &JSONProvider{ProviderName: "public_json"}
		results, err := provider.Search(context.Background(), "empty", fixtureOptions(t, "generic_empty.json", http.StatusOK, nil))
		if err != nil {
			t.Fatalf("Search returned error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected empty results, got %#v", results)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		provider := &JSONProvider{ProviderName: "public_json"}
		_, err := provider.Search(context.Background(), "bad", fixtureOptions(t, "generic_malformed.json", http.StatusOK, nil))
		if err == nil || !strings.Contains(err.Error(), "malformed provider response") {
			t.Fatalf("expected malformed error, got %v", err)
		}
	})

	t.Run("provider error is redacted", func(t *testing.T) {
		provider := &JSONProvider{ProviderName: "public_json"}
		_, err := provider.Search(context.Background(), "error", fixtureOptions(t, "generic_error.json", http.StatusForbidden, nil))
		assertRedactedError(t, err, "provider public_json returned status 403")
	})
}

func fixtureOptions(t *testing.T, fixture string, status int, inspect func(*http.Request)) Options {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		body := mustReadFixture(t, fixture)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return Options{Host: server.URL, APIKey: "SECRET_PROVIDER_KEY", APIToken: "SECRET_PROVIDER_KEY", Timeout: time.Second}
}

func mustReadFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	return body
}

func assertResult(t *testing.T, results []Result, url, title, snippet string, rank int) {
	t.Helper()
	if len(results) == 0 {
		t.Fatalf("expected at least one result")
	}
	if results[0].URL != url || results[0].Title != title || results[0].Snippet != snippet || results[0].Rank != rank {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
}

func assertRedactedError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error")
	}
	message := err.Error()
	if !strings.Contains(message, want) {
		t.Fatalf("error %q does not include %q", message, want)
	}
	if strings.Contains(message, "SHOULD_NOT_LEAK") || strings.Contains(message, "SECRET_") {
		t.Fatalf("error leaked secret: %q", message)
	}
}

func typeName(value interface{}) string {
	return fmt.Sprintf("%T", value)
}
