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

// Package searchproviders contains provider interfaces and implementations used
// by information-seed discovery.
package searchproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderHTTPJSON = "http_json"
	ProviderBrave    = "brave_search"
	ProviderBing     = "bing_web_search"

	braveDefaultHost     = "https://api.search.brave.com"
	braveDefaultEndpoint = "/res/v1/web/search"
	bingDefaultHost      = "https://api.bing.microsoft.com"
	bingDefaultEndpoint  = "/v7.0/search"
)

// Provider is implemented by information-seed search providers.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, options Options) ([]Result, error)
}

// Options contains per-request provider settings.
type Options struct {
	Name        string
	Provider    string
	Host        string
	Endpoint    string
	APIKeyLabel string
	APIKey      string
	APIToken    string
	Token       string
	Timeout     time.Duration
	MaxRequests int
	Parameters  map[string]string
	Headers     map[string]string
}

// Result is one candidate returned by a provider.
type Result struct {
	URL      string                 `json:"url"`
	Title    string                 `json:"title,omitempty"`
	Snippet  string                 `json:"snippet,omitempty"`
	Rank     int                    `json:"rank,omitempty"`
	Score    float64                `json:"score,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// HTTPClient is the subset of http.Client used by JSONProvider; it makes the
// provider trivial to mock in tests.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewProvider returns the first-class adapter for a stable provider identifier.
// Unknown or generic JSON identifiers intentionally return JSONProvider to
// preserve existing custom provider behavior.
func NewProvider(name, provider string) Provider {
	stableName := strings.ToLower(strings.TrimSpace(name))
	if stableName == "" {
		stableName = strings.ToLower(strings.TrimSpace(provider))
	}
	switch normalizeProviderName(provider, stableName) {
	case ProviderBrave:
		return &BraveProvider{ProviderName: stableName}
	case ProviderBing:
		return &BingProvider{ProviderName: stableName}
	default:
		return &JSONProvider{ProviderName: stableName}
	}
}

func normalizeProviderName(values ...string) string {
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "brave", "brave_search", "brave_search_api", "brave-web", "brave_web_search":
			return ProviderBrave
		case "bing", "bing_search", "bing_web", "bing_web_search", "bing_web_search_api", "microsoft_bing":
			return ProviderBing
		case "", "json", "http_json", "generic_json":
			continue
		}
	}
	return ProviderHTTPJSON
}

// JSONProvider is a mockable HTTP JSON provider. It intentionally supports a
// small set of common response shapes so deployments can front real search APIs
// with an adapter without changing CROWler code.
type JSONProvider struct {
	ProviderName string
	Client       HTTPClient
}

// Name returns this provider's stable name.
func (p *JSONProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderHTTPJSON
	}
	return strings.TrimSpace(p.ProviderName)
}

// Search queries a JSON HTTP endpoint. The query is sent as a q parameter unless
// options.Parameters overrides the parameter set.
func (p *JSONProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	values := endpoint.Query()
	if query != "" && values.Get("q") == "" && values.Get("query") == "" {
		values.Set("q", query)
	}
	for key, value := range options.Parameters {
		if strings.TrimSpace(key) != "" {
			values.Set(key, value)
		}
	}
	if options.APIKeyLabel != "" && options.APIKey != "" {
		values.Set(options.APIKeyLabel, options.APIKey)
	}
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range options.Headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}
	if options.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+options.APIToken)
	} else if options.Token != "" {
		req.Header.Set("Authorization", "Bearer "+options.Token)
	}

	body, err := doJSONRequest(ctx, p.Name(), p.Client, req, options.Timeout)
	if err != nil {
		return nil, err
	}
	return parseResults(body)
}

// BraveProvider implements the Brave Search API web-search adapter.
type BraveProvider struct {
	ProviderName string
	Client       HTTPClient
}

func (p *BraveProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderBrave
	}
	return strings.TrimSpace(p.ProviderName)
}

func (p *BraveProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	body, err := p.search(ctx, query, options)
	if err != nil {
		return nil, err
	}
	return parseBraveResults(body)
}

func (p *BraveProvider) search(ctx context.Context, query string, options Options) ([]byte, error) {
	options = withDefaults(options, braveDefaultHost, braveDefaultEndpoint)
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	values := endpoint.Query()
	if query != "" && values.Get("q") == "" {
		values.Set("q", query)
	}
	for key, value := range options.Parameters {
		if strings.TrimSpace(key) != "" {
			values.Set(key, value)
		}
	}
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	req.Header.Set("Accept", "application/json")
	if options.APIKey != "" {
		req.Header.Set("X-Subscription-Token", options.APIKey)
	} else if options.APIToken != "" {
		req.Header.Set("X-Subscription-Token", options.APIToken)
	} else if options.Token != "" {
		req.Header.Set("X-Subscription-Token", options.Token)
	}
	for key, value := range options.Headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}
	return doJSONRequest(ctx, p.Name(), p.Client, req, options.Timeout)
}

// BingProvider implements the Bing Web Search API adapter.
type BingProvider struct {
	ProviderName string
	Client       HTTPClient
}

func (p *BingProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderBing
	}
	return strings.TrimSpace(p.ProviderName)
}

func (p *BingProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	body, err := p.search(ctx, query, options)
	if err != nil {
		return nil, err
	}
	return parseBingResults(body)
}

func (p *BingProvider) search(ctx context.Context, query string, options Options) ([]byte, error) {
	options = withDefaults(options, bingDefaultHost, bingDefaultEndpoint)
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	values := endpoint.Query()
	if query != "" && values.Get("q") == "" {
		values.Set("q", query)
	}
	for key, value := range options.Parameters {
		if strings.TrimSpace(key) != "" {
			values.Set(key, value)
		}
	}
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	req.Header.Set("Accept", "application/json")
	if options.APIKey != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", options.APIKey)
	} else if options.APIToken != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", options.APIToken)
	} else if options.Token != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", options.Token)
	}
	for key, value := range options.Headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}
	return doJSONRequest(ctx, p.Name(), p.Client, req, options.Timeout)
}

func withDefaults(options Options, host, endpoint string) Options {
	if strings.TrimSpace(options.Host) == "" {
		options.Host = host
	}
	if strings.TrimSpace(options.Endpoint) == "" {
		options.Endpoint = endpoint
	}
	return options
}

func doJSONRequest(ctx context.Context, providerName string, client HTTPClient, req *http.Request, timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, safeProviderError(providerName, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, safeProviderError(providerName, readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, providerStatusError(providerName, resp.StatusCode, body)
	}
	return body, nil
}

func buildEndpoint(options Options) (*url.URL, error) {
	base := strings.TrimSpace(options.Host)
	if base == "" {
		return nil, fmt.Errorf("provider host is required")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid provider host")
	}
	if endpoint := strings.TrimSpace(options.Endpoint); endpoint != "" {
		rel, relErr := url.Parse(endpoint)
		if relErr != nil {
			return nil, fmt.Errorf("invalid provider endpoint")
		}
		u = u.ResolveReference(rel)
	}
	return u, nil
}

func parseResults(body []byte) ([]Result, error) {
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("malformed provider response")
	}
	items := findResultItems(payload)
	pagination := genericPagination(payload)
	results := make([]Result, 0, len(items))
	for idx, item := range items {
		result := resultFromValue(item, idx+1)
		if strings.TrimSpace(result.URL) != "" {
			ensureMetadata(&result)
			if len(pagination) > 0 {
				result.Metadata["pagination"] = pagination
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func parseBraveResults(body []byte) ([]Result, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("malformed brave_search response")
	}
	webSection, _ := payload["web"].(map[string]interface{})
	if webSection == nil {
		return nil, fmt.Errorf("malformed brave_search response: missing web results")
	}
	items, ok := webSection["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("malformed brave_search response: invalid web results")
	}
	pagination := bravePagination(payload, webSection)
	results := make([]Result, 0, len(items))
	for idx, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("malformed brave_search response: invalid result")
		}
		result := Result{
			URL:      firstString(entry, "url"),
			Title:    firstString(entry, "title"),
			Snippet:  firstString(entry, "description", "snippet"),
			Rank:     idx + 1,
			Score:    reciprocalRank(idx + 1),
			Metadata: adapterMetadata("brave_search", entry, pagination, idx+1),
		}
		if strings.TrimSpace(result.URL) != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

func parseBingResults(body []byte) ([]Result, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("malformed bing_web_search response")
	}
	webPages, _ := payload["webPages"].(map[string]interface{})
	if webPages == nil {
		return nil, fmt.Errorf("malformed bing_web_search response: missing webPages")
	}
	items, ok := webPages["value"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("malformed bing_web_search response: invalid webPages value")
	}
	pagination := bingPagination(webPages)
	results := make([]Result, 0, len(items))
	for idx, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("malformed bing_web_search response: invalid result")
		}
		result := Result{
			URL:      firstString(entry, "url"),
			Title:    firstString(entry, "name", "title"),
			Snippet:  firstString(entry, "snippet", "description"),
			Rank:     idx + 1,
			Score:    reciprocalRank(idx + 1),
			Metadata: adapterMetadata("bing_web_search", entry, pagination, idx+1),
		}
		if strings.TrimSpace(result.URL) != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

func findResultItems(payload interface{}) []interface{} {
	switch typed := payload.(type) {
	case []interface{}:
		return typed
	case map[string]interface{}:
		for _, key := range []string{"results", "items", "webPages", "organic_results"} {
			if value, ok := typed[key]; ok {
				if key == "webPages" {
					if nested, ok := value.(map[string]interface{}); ok {
						if items, ok := nested["value"].([]interface{}); ok {
							return items
						}
					}
				}
				if items, ok := value.([]interface{}); ok {
					return items
				}
			}
		}
		return []interface{}{typed}
	default:
		return nil
	}
}

func resultFromValue(value interface{}, rank int) Result {
	result := Result{Rank: rank}
	if s, ok := value.(string); ok {
		result.URL = s
		return result
	}
	item, ok := value.(map[string]interface{})
	if !ok {
		return result
	}
	for _, key := range []string{"url", "link", "href", "displayUrl"} {
		if s, ok := item[key].(string); ok && strings.TrimSpace(s) != "" {
			result.URL = s
			break
		}
	}
	result.Title = firstString(item, "title", "name")
	result.Snippet = firstString(item, "snippet", "description", "summary")
	if score, ok := numericValue(item["score"]); ok {
		result.Score = score
	}
	result.Metadata = item
	return result
}

func firstString(item map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if s, ok := item[key].(string); ok {
			return s
		}
	}
	return ""
}

func numericValue(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		f, err := typed.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func reciprocalRank(rank int) float64 {
	if rank < 1 {
		return 0
	}
	return 1 / float64(rank)
}

func ensureMetadata(result *Result) {
	if result.Metadata == nil {
		result.Metadata = map[string]interface{}{}
	}
}

func adapterMetadata(provider string, entry map[string]interface{}, pagination map[string]interface{}, rank int) map[string]interface{} {
	metadata := map[string]interface{}{
		"provider":    provider,
		"score_basis": "reciprocal_rank",
		"evidence": map[string]interface{}{
			"rank": rank,
		},
	}
	if len(pagination) > 0 {
		metadata["pagination"] = pagination
	}
	for _, key := range []string{"id", "profile", "displayUrl", "display_url", "dateLastCrawled", "language", "family_friendly", "page_age"} {
		if value, ok := entry[key]; ok {
			metadata[key] = value
		}
	}
	return metadata
}

func genericPagination(payload interface{}) map[string]interface{} {
	root, ok := payload.(map[string]interface{})
	if !ok {
		return nil
	}
	pagination := make(map[string]interface{})
	for _, key := range []string{"total", "totalResults", "total_results", "totalEstimatedMatches", "count", "offset", "nextOffset"} {
		if value, ok := root[key]; ok {
			pagination[key] = value
		}
	}
	return pagination
}

func bravePagination(payload, webSection map[string]interface{}) map[string]interface{} {
	pagination := make(map[string]interface{})
	for _, key := range []string{"total", "family_friendly", "type"} {
		if value, ok := webSection[key]; ok {
			pagination[key] = value
		}
	}
	if query, ok := payload["query"].(map[string]interface{}); ok {
		pagination["query"] = query
	}
	return pagination
}

func bingPagination(webPages map[string]interface{}) map[string]interface{} {
	pagination := make(map[string]interface{})
	for _, key := range []string{"totalEstimatedMatches", "webSearchUrl"} {
		if value, ok := webPages[key]; ok {
			pagination[key] = value
		}
	}
	return pagination
}

func safeProviderError(providerName string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("provider %s request failed: %s", providerName, redactSensitive(err.Error()))
}

func providerStatusError(providerName string, statusCode int, body []byte) error {
	message := providerErrorMessage(body)
	if message == "" {
		return fmt.Errorf("provider %s returned status %d", providerName, statusCode)
	}
	return fmt.Errorf("provider %s returned status %d: %s", providerName, statusCode, message)
}

func providerErrorMessage(body []byte) string {
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	message := extractErrorMessage(payload)
	return trimMessage(redactSensitive(message), 240)
}

func extractErrorMessage(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []interface{}:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if message := extractErrorMessage(item); message != "" {
				parts = append(parts, message)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]interface{}:
		for _, key := range []string{"message", "detail", "description", "error_description", "code"} {
			if s, ok := typed[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		for _, key := range []string{"error", "errors"} {
			if nested, ok := typed[key]; ok {
				if message := extractErrorMessage(nested); message != "" {
					return message
				}
			}
		}
	}
	return ""
}

func redactSensitive(message string) string {
	if message == "" {
		return message
	}
	parsed, err := url.Parse(message)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		query := parsed.Query()
		for key := range query {
			if isSensitiveKey(key) {
				query.Set(key, "REDACTED")
			}
		}
		parsed.RawQuery = query.Encode()
		message = parsed.String()
	}
	for _, marker := range []string{"api_key=", "apikey=", "key=", "token=", "subscription-key=", "Ocp-Apim-Subscription-Key:"} {
		idx := strings.Index(strings.ToLower(message), strings.ToLower(marker))
		if idx >= 0 {
			valueStart := idx + len(marker)
			for valueStart < len(message) && strings.ContainsRune(" \t", rune(message[valueStart])) {
				valueStart++
			}
			end := valueStart
			for end < len(message) && !strings.ContainsRune(" &;\n\t\r", rune(message[end])) {
				end++
			}
			message = message[:idx+len(marker)] + "REDACTED" + message[end:]
		}
	}
	return message
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "api_key", "apikey", "key", "token", "api_token", "subscription-key", "ocp-apim-subscription-key", "x-subscription-token":
		return true
	default:
		return false
	}
}

func trimMessage(message string, limit int) string {
	message = strings.TrimSpace(message)
	if len(message) <= limit {
		return message
	}
	return message[:limit] + "..."
}
