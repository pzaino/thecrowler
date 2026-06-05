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
	"net/url"
	"path"
	"strings"
)

const (
	// ProviderCommonCrawlIndex queries Common Crawl CDX index-compatible endpoints.
	ProviderCommonCrawlIndex = "common_crawl_index"

	commonCrawlIndexDefaultHost   = "https://index.commoncrawl.org"
	commonCrawlIndexDefaultOutput = "json"
	commonCrawlIndexMaxBodyBytes  = 4 << 20
)

// CommonCrawlIndexProvider implements bounded Common Crawl CDX index search.
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
	endpoints, err := configuredCommonCrawlIndexEndpoints(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	budget := newRequestBudget(options.MaxRequests)
	results := make([]Result, 0, options.PageSize*options.MaxPages)
	for _, endpoint := range endpoints {
		for page := 0; page < options.MaxPages; page++ {
			if options.MaxRequests > 0 && budget != nil && budget.remaining == 0 {
				return trimResults(rankCommonCrawlResults(results), options.PageSize*options.MaxPages), nil
			}
			pageURL := *endpoint
			values := pageURL.Query()
			applyCommonCrawlIndexQuery(values, query, options.Parameters, options.PageSize, page)
			pageURL.RawQuery = values.Encode()

			body, err := doPublicDocumentRequest(ctx, p.Name(), p.Client, pageURL.String(), "application/json, application/x-ndjson", options.Timeout, options.RateLimit, budget, commonCrawlIndexMaxBodyBytes, safeBrowserSearchHeaders(options.Headers))
			if err != nil {
				return nil, err
			}
			pageResults, err := parseCommonCrawlIndexResults(body, commonCrawlIndexName(endpoint))
			if err != nil {
				return nil, safeProviderError(p.Name(), err)
			}
			results = append(results, pageResults...)
			if len(pageResults) == 0 || len(results) >= options.PageSize*options.MaxPages {
				return trimResults(rankCommonCrawlResults(results), options.PageSize*options.MaxPages), nil
			}
		}
	}
	return trimResults(rankCommonCrawlResults(results), options.PageSize*options.MaxPages), nil
}

func configuredCommonCrawlIndexEndpoints(options Options) ([]*url.URL, error) {
	configured := make([]string, 0)
	for _, key := range []string{"index_endpoint", "index_endpoints", "endpoint", "endpoints", "index", "indexes"} {
		configured = append(configured, splitCommonCrawlIndexList(options.Parameters[key])...)
	}
	if len(configured) == 0 {
		endpoint, err := buildEndpoint(options)
		if err != nil {
			return nil, err
		}
		return []*url.URL{endpoint}, nil
	}
	base, err := buildEndpoint(Options{Host: options.Host})
	if err != nil {
		return nil, err
	}
	endpoints := make([]*url.URL, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, raw := range configured {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid common_crawl_index endpoint")
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			parsed = base.ResolveReference(parsed)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("invalid common_crawl_index endpoint scheme")
		}
		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		endpoints = append(endpoints, parsed)
	}
	return endpoints, nil
}

func splitCommonCrawlIndexList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
	items := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func applyCommonCrawlIndexQuery(values url.Values, query string, parameters map[string]string, pageSize, page int) {
	if values.Get("url") == "" {
		values.Set("url", renderCommonCrawlIndexQuery(query, parameters))
	}
	if values.Get("output") == "" {
		values.Set("output", commonCrawlIndexDefaultOutput)
	}
	if pageSize > 0 && values.Get("pageSize") == "" && values.Get("page_size") == "" {
		values.Set("pageSize", fmt.Sprintf("%d", pageSize))
	}
	if page > 0 && values.Get("page") == "" {
		values.Set("page", fmt.Sprintf("%d", page))
	}
	applyCommonCrawlIndexFilters(values, parameters)
	for key, value := range parameters {
		trimmed := strings.TrimSpace(key)
		switch strings.ToLower(trimmed) {
		case "index_endpoint", "index_endpoints", "endpoint", "endpoints", "index", "indexes", "url_template", "domain_template", "query_template", "filter", "mime", "mime_type", "mime_types", "http_status", "http_statuses", "status", "statuses":
			continue
		}
		if trimmed != "" {
			values.Set(trimmed, value)
		}
	}
}

func renderCommonCrawlIndexQuery(query string, parameters map[string]string) string {
	trimmed := strings.TrimSpace(query)
	template := strings.TrimSpace(parameters["query_template"])
	if template == "" {
		if commonCrawlLooksLikeDomain(trimmed) {
			template = strings.TrimSpace(parameters["domain_template"])
			if template == "" {
				template = "{domain}/*"
			}
		} else {
			template = strings.TrimSpace(parameters["url_template"])
			if template == "" {
				template = "{url}"
			}
		}
	}
	domain := commonCrawlQueryDomain(trimmed)
	replacer := strings.NewReplacer("{query}", trimmed, "{url}", trimmed, "{domain}", domain)
	return replacer.Replace(template)
}

func commonCrawlLooksLikeDomain(query string) bool {
	if strings.Contains(query, "://") || strings.ContainsAny(query, "/*?") {
		return false
	}
	return strings.Contains(query, ".") && !strings.ContainsAny(query, " \t\n\r")
}

func commonCrawlQueryDomain(query string) string {
	if parsed, err := url.Parse(query); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.Trim(query, " /")
}

func applyCommonCrawlIndexFilters(values url.Values, parameters map[string]string) {
	filters := append([]string{}, values["filter"]...)
	filters = append(filters, splitCommonCrawlIndexList(parameters["filter"])...)
	for _, key := range []string{"mime", "mime_type", "mime_types"} {
		for _, value := range splitCommonCrawlIndexList(parameters[key]) {
			filters = append(filters, "mime:"+value)
		}
	}
	for _, key := range []string{"http_status", "http_statuses", "status", "statuses"} {
		for _, value := range splitCommonCrawlIndexList(parameters[key]) {
			filters = append(filters, "status:"+value)
		}
	}
	if len(filters) > 0 {
		values.Del("filter")
		for _, filter := range filters {
			if strings.TrimSpace(filter) != "" {
				values.Add("filter", filter)
			}
		}
	}
}

func parseCommonCrawlIndexResults(body []byte, indexName string) ([]Result, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("malformed common_crawl_index response")
		}
		return commonCrawlResultsFromItems(items, indexName), nil
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
	return commonCrawlResultsFromItems(items, indexName), nil
}

func commonCrawlResultsFromItems(items []map[string]interface{}, indexName string) []Result {
	results := make([]Result, 0, len(items))
	for _, item := range items {
		link := firstString(item, "url")
		if strings.TrimSpace(link) == "" {
			continue
		}
		rank := len(results) + 1
		timestamp := firstString(item, "timestamp")
		mime := firstString(item, "mime")
		status := firstString(item, "status")
		digest := firstString(item, "digest")
		urlKey := firstString(item, "urlkey")
		results = append(results, Result{URL: link, Title: commonCrawlTitle(timestamp), Rank: rank, Score: reciprocalRank(rank), Metadata: map[string]interface{}{"provider": ProviderCommonCrawlIndex, "score_basis": "index_order", "index": indexName, "index_name": indexName, "digest": digest, "timestamp": timestamp, "mime": mime, "mime_type": mime, "status": status, "http_status": status, "urlkey": urlKey, "original_url_key": urlKey, "filename": firstString(item, "filename")}})
	}
	return results
}

func rankCommonCrawlResults(results []Result) []Result {
	for idx := range results {
		rank := idx + 1
		results[idx].Rank = rank
		results[idx].Score = reciprocalRank(rank)
	}
	return results
}

func commonCrawlIndexName(endpoint *url.URL) string {
	if endpoint == nil {
		return ""
	}
	base := path.Base(strings.TrimRight(endpoint.Path, "/"))
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func commonCrawlTitle(timestamp string) string {
	if strings.TrimSpace(timestamp) == "" {
		return "Common Crawl capture"
	}
	return "Common Crawl capture " + timestamp
}
