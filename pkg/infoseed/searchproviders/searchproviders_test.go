package searchproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestJSONProviderSendsHeadersParametersAndPaginatesWithinCaps(t *testing.T) {
	var requests []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(r.Context()))
		if got := r.Header.Get("X-Request-ID"); got != "${INFORMATION_SEED_REQUEST_ID}" {
			t.Fatalf("X-Request-ID = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ${INFORMATION_SEED_TOKEN}" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("safe"); got != "value" {
			t.Fatalf("safe parameter = %q", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "2" {
			t.Fatalf("page_size = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{"url": fmt.Sprintf("https://example.com/%d", len(requests))}},
		})
	}))
	defer server.Close()

	provider := &JSONProvider{ProviderName: "public_json"}
	results, err := provider.Search(context.Background(), "seed query", Options{
		Host:        server.URL,
		Timeout:     time.Second,
		Parameters:  map[string]string{"safe": "value"},
		Headers:     map[string]string{"X-Request-ID": "${INFORMATION_SEED_REQUEST_ID}", "Authorization": "Bearer ${INFORMATION_SEED_TOKEN}"},
		PageSize:    2,
		MaxPages:    2,
		MaxRequests: 2,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 paged requests, got %d", len(requests))
	}
	if got := requests[0].URL.Query().Get("page"); got != "" {
		t.Fatalf("first page should not set page parameter, got %q", got)
	}
	if got := requests[1].URL.Query().Get("page"); got != "2" {
		t.Fatalf("second page = %q", got)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 capped results, got %#v", results)
	}
}

func TestJSONProviderMaxRequestsCapsMaxPages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{"url": fmt.Sprintf("https://example.com/%d", requests)}},
		})
	}))
	defer server.Close()

	provider := &JSONProvider{ProviderName: "public_json"}
	_, err := provider.Search(context.Background(), "seed query", Options{Host: server.URL, Timeout: time.Second, PageSize: 1, MaxPages: 5, MaxRequests: 1})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected MaxRequests to cap pages at 1, got %d requests", requests)
	}
}

func TestRedactSensitiveCustomHeaderAndParameterNames(t *testing.T) {
	message := redactSensitive(`Get "https://example.invalid/search?client_secret=SHOULD_NOT_LEAK&safe=value": Authorization: SECRET_TOKEN`)
	if strings.Contains(message, "SHOULD_NOT_LEAK") || strings.Contains(message, "SECRET_TOKEN") {
		t.Fatalf("redaction leaked sensitive value: %s", message)
	}
	if !strings.Contains(message, "client_secret=REDACTED") || !strings.Contains(strings.ToLower(message), "authorization:redacted") {
		t.Fatalf("redaction did not mark sensitive fields: %s", message)
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

func TestProviderRetriesRetryableStatusesWithRetryAfterAndBudget(t *testing.T) {
	oldSleep := sleepContextFunc
	oldNow := nowFunc
	oldLimiters := rateLimiters
	defer func() {
		sleepContextFunc = oldSleep
		nowFunc = oldNow
		rateLimitersMu.Lock()
		rateLimiters = oldLimiters
		rateLimitersMu.Unlock()
	}()
	current := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	nowFunc = func() time.Time { return current }
	sleepContextFunc = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		current = current.Add(d)
		return nil
	}
	rateLimitersMu.Lock()
	rateLimiters = map[string]*providerLimiter{}
	rateLimitersMu.Unlock()

	client := &sequenceClient{statuses: []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK}, retryAfter: []string{"1", ""}}
	provider := &JSONProvider{ProviderName: "retry_provider", Client: client}
	results, err := provider.Search(context.Background(), "seed", Options{Host: "https://example.invalid/search", Timeout: 10 * time.Second, MaxRequests: 3, RateLimit: "2", PageSize: 1})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/final" {
		t.Fatalf("unexpected results: %#v", results)
	}
	if client.calls != 3 {
		t.Fatalf("expected 3 HTTP attempts, got %d", client.calls)
	}
	if len(sleeps) < 3 || sleeps[0] != time.Second || sleeps[1] != 400*time.Millisecond || sleeps[2] != 100*time.Millisecond {
		t.Fatalf("unexpected backoff/limiter sleeps: %#v", sleeps)
	}
}

func TestProviderRetryBudgetStopsBeforeSuccess(t *testing.T) {
	client := &sequenceClient{statuses: []int{http.StatusInternalServerError, http.StatusOK}}
	provider := &JSONProvider{ProviderName: "budget_provider", Client: client}
	_, err := provider.Search(context.Background(), "seed", Options{Host: "https://example.invalid/search", Timeout: time.Second, MaxRequests: 1, PageSize: 1})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected first retryable status to be final due to MaxRequests budget, got %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 HTTP attempt, got %d", client.calls)
	}
}

func TestParseRateLimitSemantics(t *testing.T) {
	tests := []struct {
		value        string
		wantInterval time.Duration
		wantBurst    int
	}{
		{value: "", wantInterval: 0, wantBurst: 0},
		{value: "0", wantInterval: 0, wantBurst: 0},
		{value: "2", wantInterval: 500 * time.Millisecond, wantBurst: 1},
		{value: "4,3", wantInterval: 250 * time.Millisecond, wantBurst: 3},
		{value: "200ms", wantInterval: 200 * time.Millisecond, wantBurst: 1},
	}
	for _, tc := range tests {
		got := ParseRateLimit(tc.value)
		if got.Interval != tc.wantInterval || got.Burst != tc.wantBurst {
			t.Fatalf("ParseRateLimit(%q) = %#v", tc.value, got)
		}
	}
}

type sequenceClient struct {
	statuses   []int
	retryAfter []string
	calls      int
}

func (c *sequenceClient) Do(req *http.Request) (*http.Response, error) {
	status := http.StatusOK
	idx := c.calls
	if idx < len(c.statuses) {
		status = c.statuses[idx]
	}
	c.calls++
	body := `{"results":[{"url":"https://example.com/final"}]}`
	if status != http.StatusOK {
		body = `{"error":"temporary api_key=SHOULD_NOT_LEAK"}`
	}
	resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	if idx < len(c.retryAfter) && c.retryAfter[idx] != "" {
		resp.Header.Set("Retry-After", c.retryAfter[idx])
	}
	return resp, nil
}
