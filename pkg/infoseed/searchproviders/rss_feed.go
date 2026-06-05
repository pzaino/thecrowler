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
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// ProviderRSSFeed reads bounded RSS or Atom feed documents as discovery inputs.
	ProviderRSSFeed = "rss_feed"

	rssFeedDefaultTimeout = 10 * time.Second
	rssFeedMaxBodyBytes   = 2 << 20
)

var feedQueryTokenPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// RSSFeedProvider implements an RSS/Atom feed adapter for configured public feeds.
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

// Search downloads configured RSS/Atom feed URLs and returns items whose title,
// link, body, category, or author fields match the rendered seed query.
func (p *RSSFeedProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	options = boundedOptions(options)
	if options.Timeout <= 0 {
		options.Timeout = rssFeedDefaultTimeout
	}
	feedURLs, err := configuredFeedURLs(options)
	if err != nil {
		return nil, safeProviderError(p.Name(), err)
	}
	budget := newRequestBudget(options.MaxRequests)
	results := make([]Result, 0, options.PageSize*options.MaxPages)
	for idx, feedURL := range feedURLs {
		if options.MaxRequests > 0 && idx >= options.MaxRequests {
			break
		}
		body, err := doPublicDocumentRequest(ctx, p.Name(), p.Client, feedURL, "application/rss+xml, application/atom+xml, application/xml, text/xml", options.Timeout, options.RateLimit, budget, rssFeedMaxBodyBytes, safeBrowserSearchHeaders(options.Headers))
		if err != nil {
			return nil, err
		}
		feedResults, err := parseFeedResults(body, query, feedURL)
		if err != nil {
			return nil, safeProviderError(p.Name(), err)
		}
		results = append(results, feedResults...)
		if len(results) >= options.PageSize*options.MaxPages {
			break
		}
	}
	for idx := range results {
		rank := idx + 1
		results[idx].Rank = rank
		results[idx].Score = reciprocalRank(rank)
	}
	return trimResults(results, options.PageSize*options.MaxPages), nil
}

func configuredFeedURLs(options Options) ([]string, error) {
	configured := make([]string, 0)
	for _, key := range []string{"feed_url", "feed_urls", "url", "urls"} {
		configured = append(configured, splitConfiguredFeedURLs(options.Parameters[key])...)
	}
	if len(configured) == 0 {
		endpoint, err := buildEndpoint(options)
		if err != nil {
			return nil, err
		}
		values := endpoint.Query()
		applyFeedQueryParameters(values, options.Parameters)
		endpoint.RawQuery = values.Encode()
		configured = append(configured, endpoint.String())
	}
	feedURLs := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, raw := range configured {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid feed URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("invalid feed URL scheme")
		}
		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		feedURLs = append(feedURLs, normalized)
	}
	return feedURLs, nil
}

func splitConfiguredFeedURLs(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
	urls := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func applyFeedQueryParameters(values url.Values, parameters map[string]string) {
	for key, value := range parameters {
		trimmed := strings.TrimSpace(key)
		switch strings.ToLower(trimmed) {
		case "feed_url", "feed_urls", "url", "urls":
			continue
		}
		if trimmed != "" {
			values.Set(trimmed, value)
		}
	}
}

type rssDocument struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Content     string   `xml:"encoded"`
	GUID        string   `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Categories  []string `xml:"category"`
	Author      string   `xml:"author"`
	Creator     string   `xml:"creator"`
}

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Summary    string         `xml:"summary"`
	Content    string         `xml:"content"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Links      []atomLink     `xml:"link"`
	ID         string         `xml:"id"`
	Categories []atomCategory `xml:"category"`
	Authors    []atomPerson   `xml:"author"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

type atomPerson struct {
	Name  string `xml:"name"`
	Email string `xml:"email"`
	URI   string `xml:"uri"`
}

func parseFeedResults(body []byte, query, feedURL string) ([]Result, error) {
	if strings.TrimSpace(string(body)) == "" {
		return nil, nil
	}
	var rss rssDocument
	if err := xml.Unmarshal(body, &rss); err == nil && rss.XMLName.Local == "rss" {
		return rssResults(rss.Channel.Items, query, feedURL), nil
	}
	var atom atomFeed
	if err := xml.Unmarshal(body, &atom); err == nil && atom.XMLName.Local == "feed" {
		return atomResults(atom.Entries, query, feedURL), nil
	}
	return nil, fmt.Errorf("malformed rss_feed response")
}

func rssResults(items []rssItem, query, feedURL string) []Result {
	results := make([]Result, 0, len(items))
	for _, item := range items {
		link := strings.TrimSpace(item.Link)
		if link == "" {
			link = strings.TrimSpace(item.GUID)
		}
		link = normalizeFeedItemLink(link, feedURL)
		if link == "" {
			continue
		}
		matchedFields := matchedFeedFields(query, map[string][]string{
			"title":      {item.Title},
			"link":       {link, item.Link},
			"content":    {item.Description, item.Content},
			"categories": item.Categories,
			"authors":    {item.Author, item.Creator},
		})
		if len(matchedFields) == 0 {
			continue
		}
		snippet := strings.TrimSpace(item.Description)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Content)
		}
		results = append(results, Result{URL: link, Title: strings.TrimSpace(item.Title), Snippet: snippet, Metadata: feedMetadata(feedURL, strings.TrimSpace(item.GUID), publishedTimestamp(item.PubDate), matchedFields)})
	}
	return results
}

func atomResults(entries []atomEntry, query, feedURL string) []Result {
	results := make([]Result, 0, len(entries))
	for _, entry := range entries {
		link := normalizeFeedItemLink(atomEntryLink(entry), feedURL)
		if link == "" {
			continue
		}
		matchedFields := matchedFeedFields(query, map[string][]string{
			"title":      {entry.Title},
			"link":       {link, atomEntryLink(entry)},
			"content":    {entry.Summary, entry.Content},
			"categories": atomCategoryValues(entry.Categories),
			"authors":    atomAuthorValues(entry.Authors),
		})
		if len(matchedFields) == 0 {
			continue
		}
		snippet := strings.TrimSpace(entry.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(entry.Content)
		}
		published := entry.Published
		if strings.TrimSpace(published) == "" {
			published = entry.Updated
		}
		results = append(results, Result{URL: link, Title: strings.TrimSpace(entry.Title), Snippet: snippet, Metadata: feedMetadata(feedURL, strings.TrimSpace(entry.ID), publishedTimestamp(published), matchedFields)})
	}
	return results
}

func normalizeFeedItemLink(rawLink, feedURL string) string {
	trimmed := strings.TrimSpace(rawLink)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if !parsed.IsAbs() {
		base, baseErr := url.Parse(feedURL)
		if baseErr != nil {
			return ""
		}
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

func feedMetadata(feedURL, itemID, published string, matchedFields []string) map[string]interface{} {
	metadata := map[string]interface{}{
		"provider":       ProviderRSSFeed,
		"feed_url":       feedURL,
		"matched_fields": matchedFields,
		"score_basis":    "feed_order",
	}
	if itemID != "" {
		metadata["item_id"] = itemID
	}
	if published != "" {
		metadata["published_timestamp"] = published
	}
	return metadata
}

func atomEntryLink(entry atomEntry) string {
	fallback := ""
	for _, link := range entry.Links {
		href := strings.TrimSpace(link.Href)
		if href == "" {
			continue
		}
		if strings.TrimSpace(link.Rel) == "" || strings.EqualFold(link.Rel, "alternate") {
			return href
		}
		if fallback == "" {
			fallback = href
		}
	}
	return fallback
}

func atomCategoryValues(categories []atomCategory) []string {
	values := make([]string, 0, len(categories)*2)
	for _, category := range categories {
		values = append(values, category.Term, category.Label)
	}
	return values
}

func atomAuthorValues(authors []atomPerson) []string {
	values := make([]string, 0, len(authors)*3)
	for _, author := range authors {
		values = append(values, author.Name, author.Email, author.URI)
	}
	return values
}

func publishedTimestamp(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850, "2006-01-02T15:04:05-07:00", "2006-01-02 15:04:05 -0700"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return trimmed
}

func matchedFeedFields(query string, fields map[string][]string) []string {
	if strings.TrimSpace(query) == "" {
		return sortedFeedFieldNames(fields)
	}
	matched := make([]string, 0, len(fields))
	for _, field := range []string{"title", "link", "content", "categories", "authors"} {
		if feedFieldMatches(query, fields[field]) {
			matched = append(matched, field)
		}
	}
	return matched
}

func sortedFeedFieldNames(fields map[string][]string) []string {
	matched := make([]string, 0, len(fields))
	for _, field := range []string{"title", "link", "content", "categories", "authors"} {
		if _, ok := fields[field]; ok {
			matched = append(matched, field)
		}
	}
	return matched
}

func feedFieldMatches(query string, values []string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	combined := strings.ToLower(strings.Join(values, " "))
	if strings.Contains(combined, query) {
		return true
	}
	tokens := feedQueryTokenPattern.FindAllString(query, -1)
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if !strings.Contains(combined, strings.ToLower(token)) {
			return false
		}
	}
	return true
}
