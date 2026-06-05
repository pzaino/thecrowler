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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ProviderRSSFeed reads bounded RSS or Atom feed documents as discovery inputs.
	ProviderRSSFeed = "rss_feed"
	// ProviderCommonCrawlIndex queries Common Crawl CDX index-compatible endpoints.
	ProviderCommonCrawlIndex = "common_crawl_index"

	rssFeedDefaultTimeout         = 10 * time.Second
	rssFeedMaxBodyBytes           = 2 << 20
	commonCrawlIndexDefaultHost   = "https://index.commoncrawl.org"
	commonCrawlIndexDefaultOutput = "json"
	commonCrawlIndexMaxBodyBytes  = 4 << 20
)

// RSSFeedProvider implements a simple RSS/Atom feed adapter for public feeds.
type RSSFeedProvider struct {
	ProviderName string
	Client       HTTPClient
}

func (p *RSSFeedProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderRSSFeed
	}
	return strings.TrimSpace(p.ProviderName)
}

func (p *RSSFeedProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	options = boundedOptions(options)
	if options.Timeout <= 0 {
		options.Timeout = rssFeedDefaultTimeout
	}
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	values := endpoint.Query()
	applyParameters(values, options.Parameters)
	endpoint.RawQuery = values.Encode()

	body, err := doPublicDocumentRequest(ctx, p.Name(), p.Client, endpoint.String(), "application/rss+xml, application/atom+xml, application/xml, text/xml", options.Timeout, options.RateLimit, newRequestBudget(options.MaxRequests), rssFeedMaxBodyBytes, safeBrowserSearchHeaders(options.Headers))
	if err != nil {
		return nil, err
	}
	results, err := parseFeedResults(body, query)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	return trimResults(results, options.PageSize*options.MaxPages), nil
}

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

type rssDocument struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string     `xml:"title"`
	Summary string     `xml:"summary"`
	Content string     `xml:"content"`
	Updated string     `xml:"updated"`
	Links   []atomLink `xml:"link"`
	ID      string     `xml:"id"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

func parseFeedResults(body []byte, query string) ([]Result, error) {
	var rss rssDocument
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		results := make([]Result, 0, len(rss.Channel.Items))
		for _, item := range rss.Channel.Items {
			link := strings.TrimSpace(item.Link)
			if link == "" {
				link = strings.TrimSpace(item.GUID)
			}
			if link == "" || !matchesFeedQuery(query, item.Title, item.Description, link) {
				continue
			}
			rank := len(results) + 1
			results = append(results, Result{URL: link, Title: strings.TrimSpace(item.Title), Snippet: strings.TrimSpace(item.Description), Rank: rank, Score: reciprocalRank(rank), Metadata: map[string]interface{}{"provider": ProviderRSSFeed, "published": strings.TrimSpace(item.PubDate), "score_basis": "feed_order"}})
		}
		return results, nil
	}
	var atom atomFeed
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		results := make([]Result, 0, len(atom.Entries))
		for _, entry := range atom.Entries {
			link := atomEntryLink(entry)
			if link == "" || !matchesFeedQuery(query, entry.Title, entry.Summary, entry.Content, link) {
				continue
			}
			rank := len(results) + 1
			snippet := strings.TrimSpace(entry.Summary)
			if snippet == "" {
				snippet = strings.TrimSpace(entry.Content)
			}
			results = append(results, Result{URL: link, Title: strings.TrimSpace(entry.Title), Snippet: snippet, Rank: rank, Score: reciprocalRank(rank), Metadata: map[string]interface{}{"provider": ProviderRSSFeed, "updated": strings.TrimSpace(entry.Updated), "id": strings.TrimSpace(entry.ID), "score_basis": "feed_order"}})
		}
		return results, nil
	}
	return nil, fmt.Errorf("malformed rss_feed response")
}

func atomEntryLink(entry atomEntry) string {
	fallback := ""
	for _, link := range entry.Links {
		href := strings.TrimSpace(link.Href)
		if href == "" {
			continue
		}
		if strings.TrimSpace(link.Rel) == "" || link.Rel == "alternate" {
			return href
		}
		if fallback == "" {
			fallback = href
		}
	}
	return fallback
}

func matchesFeedQuery(query string, values ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
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
