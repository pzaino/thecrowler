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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ProviderCommonCrawlIndex queries Common Crawl CDX index-compatible endpoints.
	ProviderCommonCrawlIndex = "common_crawl_index"

	commonCrawlIndexDefaultHost   = "https://index.commoncrawl.org"
	commonCrawlIndexDefaultOutput = "json"
	commonCrawlIndexMaxBodyBytes  = 4 << 20
)

// CommonCrawlIndexProvider implements Common Crawl CDX index search.
type CommonCrawlIndexProvider struct {
	ProviderName string
	Client       HTTPClient
}

func (p *CommonCrawlIndexProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderCommonCrawlIndex
	}
	return strings.TrimSpace(p.ProviderName)
}

func (p *CommonCrawlIndexProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	options = withDefaults(boundedOptions(options), commonCrawlIndexDefaultHost, options.Endpoint)
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	values := endpoint.Query()
	if query != "" && values.Get("url") == "" {
		values.Set("url", query)
	}
	if values.Get("output") == "" {
		values.Set("output", commonCrawlIndexDefaultOutput)
	}
	applyParameters(values, options.Parameters)
	endpoint.RawQuery = values.Encode()

	body, err := doPublicDocumentRequest(ctx, p.Name(), p.Client, endpoint.String(), "application/json, application/x-ndjson", options.Timeout, options.RateLimit, newRequestBudget(options.MaxRequests), commonCrawlIndexMaxBodyBytes, safeBrowserSearchHeaders(options.Headers))
	if err != nil {
		return nil, err
	}
	results, err := parseCommonCrawlIndexResults(body)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	return trimResults(results, options.PageSize*options.MaxPages), nil
}

func doPublicDocumentRequest(ctx context.Context, providerName string, client HTTPClient, endpoint, accept string, timeout time.Duration, rateLimit string, budget *requestBudget, maxBodyBytes int64, headers map[string]string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	limiter := limiterFor(providerName, rateLimit)
	if err := limiter.wait(ctx); err != nil {
		return nil, safeProviderError(providerName, err)
	}
	budget.consume()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, safeProviderError(providerName, err)
	}
	req.Header.Set("Accept", accept)
	applyHeaders(req.Header, headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, safeProviderError(providerName, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if readErr != nil {
		return nil, safeProviderError(providerName, readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, providerStatusError(providerName, resp.StatusCode, body)
	}
	return body, nil
}

func parseCommonCrawlIndexResults(body []byte) ([]Result, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("malformed common_crawl_index response")
		}
		return commonCrawlResultsFromItems(items), nil
	}
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	scanner.Buffer(make([]byte, 1024), int(commonCrawlIndexMaxBodyBytes))
	items := make([]map[string]interface{}, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("malformed common_crawl_index response")
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("malformed common_crawl_index response")
	}
	return commonCrawlResultsFromItems(items), nil
}

func commonCrawlResultsFromItems(items []map[string]interface{}) []Result {
	results := make([]Result, 0, len(items))
	for _, item := range items {
		link := firstString(item, "url")
		if strings.TrimSpace(link) == "" {
			continue
		}
		rank := len(results) + 1
		timestamp := firstString(item, "timestamp")
		results = append(results, Result{URL: link, Title: commonCrawlTitle(timestamp), Rank: rank, Score: reciprocalRank(rank), Metadata: map[string]interface{}{"provider": ProviderCommonCrawlIndex, "score_basis": "index_order", "timestamp": timestamp, "status": firstString(item, "status"), "mime": firstString(item, "mime"), "digest": firstString(item, "digest"), "filename": firstString(item, "filename")}})
	}
	return results
}

func commonCrawlTitle(timestamp string) string {
	if strings.TrimSpace(timestamp) == "" {
		return "Common Crawl capture"
	}
	return "Common Crawl capture " + timestamp
}
