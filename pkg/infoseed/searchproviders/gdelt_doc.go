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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	gdeltDefaultHost     = "https://api.gdeltproject.org"
	gdeltDefaultEndpoint = "/api/v2/doc/doc"
)

// GDELTDOCProvider implements the keyless GDELT DOC 2.0 article-list API.
type GDELTDOCProvider struct {
	ProviderName string
	Client       HTTPClient
}

// Name returns this provider's stable name.
func (p *GDELTDOCProvider) Name() string {
	if strings.TrimSpace(p.ProviderName) == "" {
		return ProviderGDELTDOC
	}
	return strings.TrimSpace(p.ProviderName)
}

// Search sends the GDELT-specific query/mode/format parameters and parses the
// top-level articles array. Generic page_size/page parameters are deliberately
// never sent because GDELT does not use them.
func (p *GDELTDOCProvider) Search(ctx context.Context, query string, options Options) ([]Result, error) {
	options = withDefaults(boundedOptions(options), gdeltDefaultHost, gdeltDefaultEndpoint)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, safeProviderError(p.Name(), fmt.Errorf("query is required"))
	}
	budget := newRequestBudget(options.MaxRequests)
	results := make([]Result, 0, options.PageSize)
	for page := 1; page <= options.MaxPages; page++ {
		endpoint, err := buildEndpoint(options)
		if err != nil {
			return nil, safeProviderError(p.Name(), err)
		}
		values := endpoint.Query()
		applyParameters(values, options.Parameters)
		values.Del("q")
		values.Del("page")
		values.Del("page_size")
		values.Set("query", query)
		values.Set("mode", "artlist")
		values.Set("format", "json")
		values.Set("maxrecords", fmt.Sprintf("%d", options.PageSize))
		if strings.TrimSpace(values.Get("sort")) == "" {
			values.Set("sort", "datedesc")
		}
		if page > 1 {
			values.Set("startrecord", fmt.Sprintf("%d", (page-1)*options.PageSize+1))
		} else {
			values.Del("startrecord")
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
		applyHeaders(req.Header, options.Headers)
		if options.APIToken != "" {
			req.Header.Set("Authorization", "Bearer "+options.APIToken)
		} else if options.Token != "" {
			req.Header.Set("Authorization", "Bearer "+options.Token)
		}
		body, err := doJSONRequest(ctx, p.Name(), p.Client, req, options.Timeout, options.RateLimit, budget)
		if err != nil {
			return nil, err
		}
		pageResults, err := parseGDELTDOCResults(body)
		if err != nil {
			return nil, safeProviderError(p.Name(), err)
		}
		results = append(results, pageResults...)
		if len(pageResults) == 0 || len(results) >= options.PageSize*options.MaxPages {
			break
		}
	}
	return trimResults(results, options.PageSize*options.MaxPages), nil
}

func parseGDELTDOCResults(body []byte) ([]Result, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("malformed gdelt_doc response")
	}
	rawArticles, exists := payload["articles"]
	if !exists {
		return nil, fmt.Errorf("malformed gdelt_doc response: missing articles")
	}
	articles, ok := rawArticles.([]interface{})
	if !ok {
		return nil, fmt.Errorf("malformed gdelt_doc response: invalid articles")
	}
	results := make([]Result, 0, len(articles))
	for index, raw := range articles {
		article, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		articleURL := firstString(article, "url")
		if strings.TrimSpace(articleURL) == "" {
			continue
		}
		rank := index + 1
		metadata := adapterMetadata(ProviderGDELTDOC, article, nil, rank)
		for _, key := range []string{"seendate", "domain", "language", "sourcecountry", "socialimage"} {
			if value, ok := article[key]; ok {
				metadata[key] = value
			}
		}
		results = append(results, Result{
			URL:      articleURL,
			Title:    firstString(article, "title"),
			Rank:     rank,
			Score:    reciprocalRank(rank),
			Metadata: metadata,
		})
	}
	return results, nil
}
