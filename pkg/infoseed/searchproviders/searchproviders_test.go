package searchproviders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
