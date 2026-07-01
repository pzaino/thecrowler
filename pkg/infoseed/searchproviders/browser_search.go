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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	// ProviderBrowserSearch is an explicit opt-in adapter that extracts search
	// results from HTML pages. It is intentionally not enabled by default; callers
	// must configure and allow-list browser_search before it can run.
	ProviderBrowserSearch = "browser_search"

	browserSearchDefaultPageSize    = 10
	browserSearchMaxPageSize        = 20
	browserSearchDefaultMaxPages    = 1
	browserSearchMaxPages           = 2
	browserSearchDefaultMaxRequests = 1
	browserSearchMaxRequests        = 3
	browserSearchDefaultTimeout     = 5 * time.Second
	browserSearchMaxTimeout         = 10 * time.Second
	browserSearchMaxBodyBytes       = 1 << 20
)

// BrowserSearchProvider implements a conservative HTML search-result parser.
// It exists for controlled, policy-reviewed deployments and local fixtures; API
// providers such as Brave, Bing, or a custom http_json gateway remain the
// recommended production path.
type BrowserSearchProvider struct {
	ProviderName string
	Client       HTTPClient
	Sessions     BrowserSessionProvider
	Actions      BrowserActionExecutor
	Scraper      BrowserResultScraper
	Rules        BrowserRuleResolver
}

// Name returns this provider's stable name.
func (p *BrowserSearchProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderBrowserSearch
	}
	return strings.TrimSpace(p.ProviderName)
}

// Search loads bounded HTML result pages and extracts candidates using CSS
// selectors supplied in options.Parameters. Credential-bearing options are
// deliberately ignored so secrets are never sent by, logged by, or emitted from
// this adapter.
func (p *BrowserSearchProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	options = boundedBrowserSearchOptions(options)
	selectors := browserSearchSelectorsFromParameters(options.Parameters)
	endpoint, err := buildEndpoint(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	if strings.EqualFold(strings.TrimSpace(options.Transport), BrowserTransportWebDriver) {
		return p.searchWebDriver(ctx, query, options, endpoint)
	}

	results := make([]Result, 0, options.PageSize*options.MaxPages)
	nextURL := endpoint
	requests := 0
	for page := 1; page <= options.MaxPages && requests < options.MaxRequests && nextURL != nil; page++ {
		pageURL := *nextURL
		values := pageURL.Query()
		if page == 1 && query != "" && values.Get("q") == "" && values.Get("query") == "" {
			values.Set("q", query)
		}
		if page == 1 {
			applyParameters(values, safeBrowserSearchParameters(options.Parameters))
		}
		pageURL.RawQuery = values.Encode()

		doc, err := p.fetchDocument(ctx, pageURL.String(), options)
		requests++
		if err != nil {
			return nil, err
		}
		if selectors.ConsentPage != "" && doc.Find(selectors.ConsentPage).Length() > 0 {
			return nil, safeProviderError(p.Name(), fmt.Errorf("browser search consent page detected"))
		}
		pageResults := parseBrowserSearchResults(doc, selectors, page, len(results))
		results = append(results, pageResults...)
		if len(results) >= options.PageSize*options.MaxPages {
			break
		}
		nextURL = resolveBrowserSearchNextURL(&pageURL, doc, selectors.NextPage)
	}
	return trimResults(results, options.PageSize*options.MaxPages), nil
}

func (p *BrowserSearchProvider) fetchDocument(ctx context.Context, endpoint string, options Options) (*goquery.Document, error) {
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: options.Timeout}
	}
	limiter := limiterFor(p.Name(), options.RateLimit)
	if err := limiter.wait(ctx); err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	applyHeaders(req.Header, safeBrowserSearchHeaders(options.Headers))
	resp, err := client.Do(req)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, browserSearchMaxBodyBytes))
	if readErr != nil {
		return nil, safeProviderError(p.Name(), readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, providerStatusError(p.Name(), resp.StatusCode, body)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, safeProviderError(p.Name(), fmt.Errorf("malformed browser search response"))
	}
	return doc, nil
}

type browserSearchSelectors struct {
	ResultContainer string
	URL             string
	Title           string
	Snippet         string
	NextPage        string
	ConsentPage     string
}

func browserSearchSelectorsFromParameters(parameters map[string]string) browserSearchSelectors {
	return browserSearchSelectors{
		ResultContainer: firstParameter(parameters, "article, .result, [data-result]", "result_container_selector", "result_selector", "container_selector", "browser_result_container_selector"),
		URL:             firstParameter(parameters, "a[href]", "url_selector", "link_selector", "browser_url_selector"),
		Title:           firstParameter(parameters, "h2, h3, .title, a[href]", "title_selector", "browser_title_selector"),
		Snippet:         firstParameter(parameters, ".snippet, .description, p", "snippet_selector", "browser_snippet_selector"),
		NextPage:        firstParameter(parameters, "a[rel='next'], .next a, a.next", "next_page_selector", "next_selector", "browser_next_page_selector"),
		ConsentPage:     firstParameter(parameters, "", "consent_page_selector", "consent_selector", "browser_consent_page_selector"),
	}
}

func firstParameter(parameters map[string]string, fallback string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(parameters[key]); value != "" {
			return value
		}
	}
	return fallback
}

func parseBrowserSearchResults(doc *goquery.Document, selectors browserSearchSelectors, page, offset int) []Result {
	containers := doc.Find(selectors.ResultContainer)
	results := make([]Result, 0, containers.Length())
	containers.Each(func(_ int, container *goquery.Selection) {
		link := firstSelection(container, selectors.URL)
		href := strings.TrimSpace(link.AttrOr("href", ""))
		lowerHref := strings.ToLower(href)
		if href == "" ||
			strings.HasPrefix(lowerHref, "javascript:") ||
			strings.HasPrefix(lowerHref, "data:") ||
			strings.HasPrefix(lowerHref, "vbscript:") {
			return
		}
		rank := offset + len(results) + 1
		result := Result{
			URL:     href,
			Title:   selectionText(firstNonEmptySelection(container, selectors.Title, link)),
			Snippet: selectionText(firstSelection(container, selectors.Snippet)),
			Rank:    rank,
			Score:   reciprocalRank(rank),
			Metadata: map[string]interface{}{
				"provider":    ProviderBrowserSearch,
				"score_basis": "reciprocal_rank",
				"evidence": map[string]interface{}{
					"rank": rank,
					"page": page,
				},
			},
		}
		results = append(results, result)
	})
	return results
}

func firstSelection(root *goquery.Selection, selector string) *goquery.Selection {
	if strings.TrimSpace(selector) == "" {
		return root
	}
	return root.Find(selector).First()
}

func firstNonEmptySelection(root *goquery.Selection, selector string, fallback *goquery.Selection) *goquery.Selection {
	selection := firstSelection(root, selector)
	if strings.TrimSpace(selectionText(selection)) != "" {
		return selection
	}
	return fallback
}

func selectionText(selection *goquery.Selection) string {
	if selection == nil {
		return ""
	}
	return strings.Join(strings.Fields(selection.Text()), " ")
}

func resolveBrowserSearchNextURL(current *url.URL, doc *goquery.Document, selector string) *url.URL {
	if strings.TrimSpace(selector) == "" {
		return nil
	}
	href := strings.TrimSpace(doc.Find(selector).First().AttrOr("href", ""))
	if href == "" {
		return nil
	}
	next, err := url.Parse(href)
	if err != nil {
		return nil
	}
	return current.ResolveReference(next)
}

func boundedBrowserSearchOptions(options Options) Options {
	if options.PageSize < 1 {
		options.PageSize = browserSearchDefaultPageSize
	} else if options.PageSize > browserSearchMaxPageSize {
		options.PageSize = browserSearchMaxPageSize
	}
	if options.MaxPages < 1 {
		options.MaxPages = browserSearchDefaultMaxPages
	} else if options.MaxPages > browserSearchMaxPages {
		options.MaxPages = browserSearchMaxPages
	}
	if options.MaxRequests < 1 {
		options.MaxRequests = browserSearchDefaultMaxRequests
	} else if options.MaxRequests > browserSearchMaxRequests {
		options.MaxRequests = browserSearchMaxRequests
	}
	if options.MaxPages > options.MaxRequests {
		options.MaxPages = options.MaxRequests
	}
	if options.Timeout <= 0 {
		options.Timeout = browserSearchDefaultTimeout
	} else if options.Timeout > browserSearchMaxTimeout {
		options.Timeout = browserSearchMaxTimeout
	}
	return options
}

func safeBrowserSearchParameters(parameters map[string]string) map[string]string {
	if len(parameters) == 0 {
		return nil
	}
	safe := make(map[string]string, len(parameters))
	for key, value := range parameters {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" || isSensitiveKey(trimmed) || isBrowserSearchSelectorKey(trimmed) || isBrowserSearchDebugKey(trimmed) {
			continue
		}
		safe[trimmed] = value
	}
	return safe
}

func safeBrowserSearchHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	safe := make(map[string]string, len(headers))
	for key, value := range headers {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" || isSensitiveKey(trimmed) {
			continue
		}
		safe[trimmed] = value
	}
	return safe
}

func isBrowserSearchSelectorKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(normalized, "selector")
}

func isBrowserSearchDebugKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "screenshot", "screenshots", "debug", "debug_output", "debug_screenshots", "save_screenshot", "emit_html":
		return true
	default:
		return false
	}
}
