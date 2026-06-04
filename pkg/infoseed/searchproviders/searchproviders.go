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
		return "http_json"
	}
	return strings.TrimSpace(p.ProviderName)
}

// Search queries a JSON HTTP endpoint. The query is sent as a q parameter unless
// options.Parameters overrides the parameter set.
func (p *JSONProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, err
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

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: options.Timeout}
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
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

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("provider %s returned status %d", p.Name(), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseResults(body)
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
		return nil, fmt.Errorf("invalid provider host: %w", err)
	}
	if endpoint := strings.TrimSpace(options.Endpoint); endpoint != "" {
		rel, relErr := url.Parse(endpoint)
		if relErr != nil {
			return nil, fmt.Errorf("invalid provider endpoint: %w", relErr)
		}
		u = u.ResolveReference(rel)
	}
	return u, nil
}

func parseResults(body []byte) ([]Result, error) {
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	items := findResultItems(payload)
	results := make([]Result, 0, len(items))
	for idx, item := range items {
		result := resultFromValue(item, idx+1)
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
	if score, ok := item["score"].(float64); ok {
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
