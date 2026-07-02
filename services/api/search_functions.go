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

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	search "github.com/pzaino/thecrowler/pkg/search"
)

const defaultSearchFunctionLanguage = "english"

// SearchFunctionQuery is the internal superset used to parse typed
// PostgreSQL search-function endpoint requests. Do not use this struct for
// OpenAPI route registration; each endpoint below has a dedicated public query
// struct matching its backing database function signature.
type SearchFunctionQuery struct {
	Q          string `json:"q,omitempty" yaml:"q,omitempty"`
	Domain     string `json:"domain,omitempty" yaml:"domain,omitempty"`
	SourceID   int64  `json:"source_id,omitempty" yaml:"source_id,omitempty"`
	SourceUID  string `json:"source_uid,omitempty" yaml:"source_uid,omitempty"`
	SourceName string `json:"source_name,omitempty" yaml:"source_name,omitempty"`
	SourceURL  string `json:"source_url,omitempty" yaml:"source_url,omitempty"`
	Lang       string `json:"lang,omitempty" yaml:"lang,omitempty"`
	FieldName  string `json:"field_name,omitempty" yaml:"field_name,omitempty"`
	FieldValue string `json:"field_value,omitempty" yaml:"field_value,omitempty"`
	Filters    string `json:"filters,omitempty" yaml:"filters,omitempty"`
	Limit      int    `json:"limit,omitempty" yaml:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty" yaml:"offset,omitempty"`
}

type correlatedSourcesSearchQuery struct {
	Domain string `json:"domain,omitempty" yaml:"domain,omitempty" desc:"Domain argument passed to find_correlated_sources_by_domain(domain TEXT)."`
	Limit  int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of rows to return. Zero means no explicit limit."`
	Offset int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Number of rows to skip. Zero means no offset."`
}

type pagesSearchQuery struct {
	Q      string `json:"q,omitempty" yaml:"q,omitempty" desc:"Query argument passed to search_pages(q TEXT, lang TEXT)."`
	Lang   string `json:"lang,omitempty" yaml:"lang,omitempty" desc:"PostgreSQL text-search configuration passed to search_pages(q TEXT, lang TEXT). Defaults to english."`
	Limit  int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of rows to return. Zero means no explicit limit."`
	Offset int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Number of rows to skip. Zero means no offset."`
}

type qSearchFunctionQuery struct {
	Q      string `json:"q,omitempty" yaml:"q,omitempty" desc:"Query argument passed to the backing PostgreSQL search function."`
	Limit  int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of rows to return. Zero means no explicit limit."`
	Offset int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Number of rows to skip. Zero means no offset."`
}

type fieldSearchFunctionQuery struct {
	FieldName  string `json:"field_name,omitempty" yaml:"field_name,omitempty" desc:"field_name argument passed to the backing PostgreSQL search function."`
	FieldValue string `json:"field_value,omitempty" yaml:"field_value,omitempty" desc:"field_value argument passed to the backing PostgreSQL search function."`
	Limit      int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of rows to return. Zero means no explicit limit."`
	Offset     int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Number of rows to skip. Zero means no offset."`
}

type filtersSearchFunctionQuery struct {
	Filters string `json:"filters,omitempty" yaml:"filters,omitempty" desc:"JSON object passed as the filters JSONB argument to the backing PostgreSQL search function."`
	Limit   int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of rows to return. Zero means no explicit limit."`
	Offset  int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Number of rows to skip. Zero means no offset."`
}

type webObjectsBySourceIDQuery struct {
	SourceID int64 `json:"source_id,omitempty" yaml:"source_id,omitempty" desc:"BIGINT source identifier used to retrieve all associated web objects."`
}

type sourceUIDQuery struct {
	SourceUID string `json:"source_uid,omitempty" yaml:"source_uid,omitempty" desc:"Stable source identifier."`
}

type sourceNameQuery struct {
	SourceName string `json:"source_name,omitempty" yaml:"source_name,omitempty" desc:"Source name used to find its stable identifier."`
}

type sourceURLQuery struct {
	SourceURL string `json:"source_url,omitempty" yaml:"source_url,omitempty" desc:"Source URL used to find its stable identifier."`
}

// SearchFunctionResponse is the common response envelope for typed
// PostgreSQL search-function endpoints.
type SearchFunctionResponse struct {
	Kind string `json:"kind"`
	URL  struct {
		Type     string `json:"type"`
		Template string `json:"template"`
	} `json:"url"`
	Queries struct {
		Request []QueryRequest `json:"request"`
		Limit   int            `json:"limit"`
		Offset  int            `json:"offset"`
	} `json:"queries"`
	Items interface{} `json:"items"`
}

type apiCorrelatedSourceSearchResult struct {
	SourceID  uint64 `json:"source_id"`
	SourceUID string `json:"source_uid"`
	URL       string `json:"url"`
}

type apiSourceUIDResult struct {
	SourceUID string `json:"source_uid"`
	Name      string `json:"name"`
	URL       string `json:"url"`
}

type apiPageSearchResult struct {
	IndexID       uint64     `json:"index_id"`
	SourceUID     string     `json:"source_uid"`
	PageURL       string     `json:"page_url"`
	Title         *string    `json:"title,omitempty"`
	Snippet       *string    `json:"snippet,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
	Rank          float64    `json:"rank"`
}

type apiScrapedDataSearchResult struct {
	IndexID       uint64     `json:"index_id"`
	SourceUID     string     `json:"source_uid"`
	PageURL       *string    `json:"page_url,omitempty"`
	JSONField     *string    `json:"json_field,omitempty"`
	JSONValue     *string    `json:"json_val,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
	Rank          float64    `json:"rank"`
}

type apiArtifactSearchResult struct {
	SourceType    string     `json:"source_type"`
	ArtifactID    uint64     `json:"artifact_id"`
	SourceUID     string     `json:"source_uid"`
	PageURL       *string    `json:"page_url,omitempty"`
	JSONField     *string    `json:"json_field,omitempty"`
	JSONValue     *string    `json:"json_val,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
	Rank          float64    `json:"rank"`
}

type apiArtifactFieldsSearchResult struct {
	SourceType    string          `json:"source_type"`
	ArtifactID    uint64          `json:"artifact_id"`
	SourceUID     string          `json:"source_uid"`
	PageURL       *string         `json:"page_url,omitempty"`
	CreatedAt     *time.Time      `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time      `json:"last_updated_at,omitempty"`
	MatchedFields json.RawMessage `json:"matched_fields,omitempty"`
	Rank          float64         `json:"rank"`
}

type apiArtifactAttributeSearchResult struct {
	SourceType     string     `json:"source_type"`
	ArtifactID     uint64     `json:"artifact_id"`
	SourceUID      string     `json:"source_uid"`
	PageURL        *string    `json:"page_url,omitempty"`
	AttributeKey   *string    `json:"attribute_key,omitempty"`
	AttributeValue *string    `json:"attribute_value,omitempty"`
	AttributeType  *string    `json:"attribute_type,omitempty"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
	LastUpdatedAt  *time.Time `json:"last_updated_at,omitempty"`
	Rank           float64    `json:"rank"`
}

type apiObjectAttributeSearchResult struct {
	SourceType    string          `json:"source_type"`
	ObjectID      uint64          `json:"object_id"`
	SourceUID     string          `json:"source_uid"`
	PageURL       *string         `json:"page_url,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	Attributes    json.RawMessage `json:"attributes,omitempty"`
	CreatedAt     *time.Time      `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time      `json:"last_updated_at,omitempty"`
	Rank          float64         `json:"rank"`
}

type apiObjectAttributesSearchResult struct {
	SourceType    string          `json:"source_type"`
	ObjectID      uint64          `json:"object_id"`
	SourceUID     string          `json:"source_uid"`
	PageURL       *string         `json:"page_url,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	Attributes    json.RawMessage `json:"attributes,omitempty"`
	CreatedAt     *time.Time      `json:"created_at,omitempty"`
	LastUpdatedAt *time.Time      `json:"last_updated_at,omitempty"`
	MatchedFields json.RawMessage `json:"matched_fields,omitempty"`
	Rank          float64         `json:"rank"`
}

type searchFunctionExecutor func(context.Context, SearchFunctionQuery, *cdb.Handler) (interface{}, error)

type searchFunctionBadRequestError struct {
	err error
}

func (err searchFunctionBadRequestError) Error() string {
	return err.err.Error()
}

func (err searchFunctionBadRequestError) Unwrap() error {
	return err.err
}

func newSearchFunctionBadRequest(format string, args ...interface{}) error {
	return searchFunctionBadRequestError{err: fmt.Errorf(format, args...)}
}

func isSearchFunctionBadRequest(err error) bool {
	var badRequest searchFunctionBadRequestError
	return errors.As(err, &badRequest)
}

func searchCorrelatedSourcesByDomainHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "correlated_sources#search", "correlated_sources", "domain", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		if strings.TrimSpace(query.Domain) == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'domain' is required")
		}
		results, err := search.FindCorrelatedSourcesByDomain(ctx, db, query.Domain, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapCorrelatedSourceResults(results), nil
	})
}

func searchPagesFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "pages#search", "pages", "q", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		if strings.TrimSpace(query.Q) == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'q' is required")
		}
		lang := strings.TrimSpace(query.Lang)
		if lang == "" {
			lang = defaultSearchFunctionLanguage
		}
		results, err := search.SearchPages(ctx, db, query.Q, lang, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapPageSearchResults(results), nil
	})
}

func searchWebObjectsBySourceIDHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "webobjects_by_source#search", "webobjects_by_source", "source_id", func(_ context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		if query.SourceID <= 0 {
			return nil, newSearchFunctionBadRequest("query parameter 'source_id' is required and must be a positive BIGINT")
		}
		results, err := performWebObjectSearchBySourceID(query.SourceID, db)
		if err != nil {
			return nil, err
		}
		return results.Items, nil
	})
}

func searchWebObjectsBySourceUIDHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "webobjects_by_source_uid#search", "webobjects_by_source_uid", "source_uid", func(_ context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		sourceUID := strings.TrimSpace(query.SourceUID)
		if sourceUID == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'source_uid' is required")
		}
		results, err := performWebObjectSearchBySourceUID(sourceUID, db)
		if err != nil {
			return nil, err
		}
		return results.Items, nil
	})
}

func searchSourceStatusByUIDHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "source_status_by_uid#search", "source_status_by_uid", "source_uid", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		sourceUID := strings.TrimSpace(query.SourceUID)
		if sourceUID == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'source_uid' is required")
		}
		results, err := search.GetSourceStatusByUID(ctx, db, sourceUID)
		if err != nil {
			return nil, err
		}
		return sourceStatusRowsFromDB(results), nil
	})
}

func searchSourceUIDByNameHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "source_uid_by_name#search", "source_uid_by_name", "source_name", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		name := strings.TrimSpace(query.SourceName)
		if name == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'source_name' is required")
		}
		results, err := search.FindSourceUIDsByName(ctx, db, name)
		if err != nil {
			return nil, err
		}
		return mapSourceUIDResults(results), nil
	})
}

func searchSourceUIDByURLHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "source_uid_by_url#search", "source_uid_by_url", "source_url", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		sourceURL := strings.TrimSpace(query.SourceURL)
		if sourceURL == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'source_url' is required")
		}
		results, err := search.FindSourceUIDsByURL(ctx, db, sourceURL)
		if err != nil {
			return nil, err
		}
		return mapSourceUIDResults(results), nil
	})
}

func searchScrapedDataFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "scraped_data_function#search", "scraped_data", "q", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		if strings.TrimSpace(query.Q) == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'q' is required")
		}
		results, err := search.SearchScrapedDataFunction(ctx, db, query.Q, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapScrapedDataSearchResults(results), nil
	})
}

func searchScrapedDataFieldFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "scraped_data_field#search", "scraped_data_field", "field_name", executeScrapedDataFieldSearch)
}

func searchArtifactsFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "artifacts#search", "artifacts", "q", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		if strings.TrimSpace(query.Q) == "" {
			return nil, newSearchFunctionBadRequest("query parameter 'q' is required")
		}
		results, err := search.SearchArtifacts(ctx, db, query.Q, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapArtifactSearchResults(results), nil
	})
}

func searchArtifactsFieldFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "artifacts_field#search", "artifacts_field", "field_name", executeArtifactFieldSearch)
}

func searchArtifactsFieldsFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "artifacts_fields#search", "artifacts_fields", "filters", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		filters, err := searchFunctionFilters(query)
		if err != nil {
			return nil, err
		}
		results, err := search.SearchArtifactsFields(ctx, db, filters, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapArtifactFieldsSearchResults(results), nil
	})
}

func searchArtifactsByAttributeFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "artifacts_attribute#search", "artifacts_attribute", "field_name", executeArtifactAttributeSearch)
}

func searchObjectsByAttributeFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "objects_attribute#search", "objects_attribute", "field_name", executeObjectAttributeSearch)
}

func searchObjectsByAttributesFunctionHandler(w http.ResponseWriter, r *http.Request) {
	handleSearchFunctionEndpoint(w, r, "objects_attributes#search", "objects_attributes", "filters", func(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
		filters, err := searchFunctionFilters(query)
		if err != nil {
			return nil, err
		}
		results, err := search.SearchObjectsByAttributes(ctx, db, filters, searchFunctionOptions(query))
		if err != nil {
			return nil, err
		}
		return mapObjectAttributesSearchResults(results), nil
	})
}

func executeScrapedDataFieldSearch(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
	if err := requireFieldSearchParams(query); err != nil {
		return nil, err
	}
	results, err := search.SearchScrapedDataField(ctx, db, query.FieldName, query.FieldValue, searchFunctionOptions(query))
	if err != nil {
		return nil, err
	}
	return mapScrapedDataSearchResults(results), nil
}

func executeArtifactFieldSearch(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
	if err := requireFieldSearchParams(query); err != nil {
		return nil, err
	}
	results, err := search.SearchArtifactsField(ctx, db, query.FieldName, query.FieldValue, searchFunctionOptions(query))
	if err != nil {
		return nil, err
	}
	return mapArtifactSearchResults(results), nil
}

func executeArtifactAttributeSearch(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
	if err := requireFieldSearchParams(query); err != nil {
		return nil, err
	}
	results, err := search.SearchArtifactsByAttribute(ctx, db, query.FieldName, query.FieldValue, searchFunctionOptions(query))
	if err != nil {
		return nil, err
	}
	return mapArtifactAttributeSearchResults(results), nil
}

func executeObjectAttributeSearch(ctx context.Context, query SearchFunctionQuery, db *cdb.Handler) (interface{}, error) {
	if err := requireFieldSearchParams(query); err != nil {
		return nil, err
	}
	results, err := search.SearchObjectsByAttribute(ctx, db, query.FieldName, query.FieldValue, searchFunctionOptions(query))
	if err != nil {
		return nil, err
	}
	return mapObjectAttributeSearchResults(results), nil
}

func handleSearchFunctionEndpoint(w http.ResponseWriter, r *http.Request, kind, templateKind, searchParam string, executor searchFunctionExecutor) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		query, err := parseSearchFunctionQuery(r)
		defer r.Body.Close() //nolint:errcheck // best-effort cleanup after optional POST body parsing
		if err != nil {
			totalErrors.Add(1)
			handleErrorAndRespond(w, err, nil, "Invalid search-function request: %v", http.StatusBadRequest, http.StatusOK)
			return
		}

		items, err := executor(r.Context(), query, &dbHandler)
		if err != nil {
			totalErrors.Add(1)
			errCode := http.StatusInternalServerError
			if isSearchFunctionBadRequest(err) {
				errCode = http.StatusBadRequest
			}
			handleErrorAndRespond(w, err, nil, "Error performing search-function request: %v", errCode, http.StatusOK)
			return
		}

		response := newSearchFunctionResponse(kind, templateKind, r.Method, query, searchParam, items)
		totalSuccess.Add(1)
		handleErrorAndRespond(w, nil, response, "", http.StatusInternalServerError, http.StatusOK)
	case <-time.After(5 * time.Second):
		totalErrors.Add(1)
		healthStatus := HealthCheck{Status: "DB is overloaded, please try again later"}
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

func parseSearchFunctionQuery(r *http.Request) (SearchFunctionQuery, error) {
	var query SearchFunctionQuery
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			return query, fmt.Errorf("invalid JSON request body: %w", err)
		}
		return validateSearchFunctionPagination(query)
	}

	values := r.URL.Query()
	query.Q = values.Get("q")
	query.Domain = values.Get("domain")
	query.SourceUID = values.Get("source_uid")
	query.SourceName = values.Get("source_name")
	query.SourceURL = values.Get("source_url")
	sourceID := strings.TrimSpace(values.Get("source_id"))
	if sourceID != "" {
		var err error
		query.SourceID, err = strconv.ParseInt(sourceID, 10, 64)
		if err != nil {
			return query, fmt.Errorf("invalid source_id value: must be a BIGINT")
		}
	}
	query.Lang = values.Get("lang")
	query.FieldName = values.Get("field_name")
	query.FieldValue = values.Get("field_value")
	query.Filters = values.Get("filters")

	var err error
	query.Limit, err = parseOptionalNonNegativeInt(values.Get("limit"), "limit")
	if err != nil {
		return query, err
	}
	query.Offset, err = parseOptionalNonNegativeInt(values.Get("offset"), "offset")
	if err != nil {
		return query, err
	}
	return query, nil
}

func parseOptionalNonNegativeInt(value, name string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: must be an integer", name)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("invalid %s value: must be non-negative", name)
	}
	return parsed, nil
}

func validateSearchFunctionPagination(query SearchFunctionQuery) (SearchFunctionQuery, error) {
	if query.Limit < 0 || query.Offset < 0 {
		return query, fmt.Errorf("limit and offset must be non-negative")
	}
	return query, nil
}

func searchFunctionOptions(query SearchFunctionQuery) search.FunctionOptions {
	return search.FunctionOptions{Limit: query.Limit, Offset: query.Offset}
}

func requireFieldSearchParams(query SearchFunctionQuery) error {
	if strings.TrimSpace(query.FieldName) == "" {
		return newSearchFunctionBadRequest("query parameter 'field_name' is required")
	}
	if strings.TrimSpace(query.FieldValue) == "" {
		return newSearchFunctionBadRequest("query parameter 'field_value' is required")
	}
	return nil
}

func searchFunctionFilters(query SearchFunctionQuery) (map[string]string, error) {
	filtersJSON := strings.TrimSpace(query.Filters)
	if filtersJSON == "" {
		return nil, newSearchFunctionBadRequest("query parameter 'filters' is required")
	}
	filters := map[string]string{}
	if err := json.Unmarshal([]byte(filtersJSON), &filters); err != nil {
		return nil, newSearchFunctionBadRequest("invalid filters JSON object: %w", err)
	}
	if len(filters) == 0 {
		return nil, newSearchFunctionBadRequest("at least one filter must be provided")
	}
	return filters, nil
}

func newSearchFunctionResponse(kind, templateKind, method string, query SearchFunctionQuery, searchParam string, items interface{}) SearchFunctionResponse {
	count := searchFunctionItemCount(items)
	response := SearchFunctionResponse{Kind: kind, Items: items}
	response.URL.Type = jsonResponse
	response.URL.Template = GetQueryTemplate(templateKind, "v1/search", method)
	response.Queries.Limit = query.Limit
	response.Queries.Offset = query.Offset
	response.Queries.Request = []QueryRequest{{
		Title:          "search",
		TotalResults:   count,
		SearchTerms:    searchFunctionSearchTerms(query, searchParam),
		Count:          count,
		StartIndex:     query.Offset,
		InputEncoding:  "utf8",
		OutputEncoding: "utf8",
		Safe:           "off",
		Cx:             "0",
	}}
	return response
}

func searchFunctionSearchTerms(query SearchFunctionQuery, searchParam string) string {
	switch searchParam {
	case "domain":
		return query.Domain
	case "source_id":
		return strconv.FormatInt(query.SourceID, 10)
	case "source_uid":
		return query.SourceUID
	case "source_name":
		return query.SourceName
	case "source_url":
		return query.SourceURL
	case "field_name":
		return query.FieldName + ":" + query.FieldValue
	case "filters":
		return query.Filters
	default:
		return query.Q
	}
}

func searchFunctionItemCount(items interface{}) int {
	switch value := items.(type) {
	case []apiCorrelatedSourceSearchResult:
		return len(value)
	case []apiPageSearchResult:
		return len(value)
	case []apiScrapedDataSearchResult:
		return len(value)
	case []apiArtifactSearchResult:
		return len(value)
	case []apiArtifactFieldsSearchResult:
		return len(value)
	case []apiArtifactAttributeSearchResult:
		return len(value)
	case []apiObjectAttributeSearchResult:
		return len(value)
	case []apiObjectAttributesSearchResult:
		return len(value)
	case []WebObjectRow:
		return len(value)
	case []StatusResponseRow:
		return len(value)
	case []apiSourceUIDResult:
		return len(value)
	default:
		return 0
	}
}

func mapSourceUIDResults(results []search.SourceUIDResult) []apiSourceUIDResult {
	items := make([]apiSourceUIDResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiSourceUIDResult{
			SourceUID: result.SourceUID,
			Name:      result.Name,
			URL:       result.URL,
		})
	}
	return items
}

func mapCorrelatedSourceResults(results []cdb.CorrelatedSourceSearchResult) []apiCorrelatedSourceSearchResult {
	items := make([]apiCorrelatedSourceSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiCorrelatedSourceSearchResult{SourceID: result.SourceID, SourceUID: result.SourceUID, URL: result.URL})
	}
	return items
}

func mapPageSearchResults(results []cdb.PageSearchResult) []apiPageSearchResult {
	items := make([]apiPageSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiPageSearchResult{
			IndexID:       result.IndexID,
			SourceUID:     result.SourceUID,
			PageURL:       result.PageURL,
			Title:         nullStringPtr(result.Title),
			Snippet:       nullStringPtr(result.Snippet),
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			Rank:          result.Rank,
		})
	}
	return items
}

func mapScrapedDataSearchResults(results []cdb.ScrapedDataSearchResult) []apiScrapedDataSearchResult {
	items := make([]apiScrapedDataSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiScrapedDataSearchResult{
			IndexID:       result.IndexID,
			SourceUID:     result.SourceUID,
			PageURL:       nullStringPtr(result.PageURL),
			JSONField:     nullStringPtr(result.JSONField),
			JSONValue:     nullStringPtr(result.JSONValue),
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			Rank:          result.Rank,
		})
	}
	return items
}

func mapArtifactSearchResults(results []cdb.ArtifactSearchResult) []apiArtifactSearchResult {
	items := make([]apiArtifactSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiArtifactSearchResult{
			SourceType:    result.SourceType,
			ArtifactID:    result.ArtifactID,
			SourceUID:     result.SourceUID,
			PageURL:       nullStringPtr(result.PageURL),
			JSONField:     nullStringPtr(result.JSONField),
			JSONValue:     nullStringPtr(result.JSONValue),
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			Rank:          result.Rank,
		})
	}
	return items
}

func mapArtifactFieldsSearchResults(results []cdb.ArtifactFieldsSearchResult) []apiArtifactFieldsSearchResult {
	items := make([]apiArtifactFieldsSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiArtifactFieldsSearchResult{
			SourceType:    result.SourceType,
			ArtifactID:    result.ArtifactID,
			SourceUID:     result.SourceUID,
			PageURL:       nullStringPtr(result.PageURL),
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			MatchedFields: result.MatchedFields,
			Rank:          result.Rank,
		})
	}
	return items
}

func mapArtifactAttributeSearchResults(results []cdb.ArtifactAttributeSearchResult) []apiArtifactAttributeSearchResult {
	items := make([]apiArtifactAttributeSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiArtifactAttributeSearchResult{
			SourceType:     result.SourceType,
			ArtifactID:     result.ArtifactID,
			SourceUID:      result.SourceUID,
			PageURL:        nullStringPtr(result.PageURL),
			AttributeKey:   nullStringPtr(result.AttributeKey),
			AttributeValue: nullStringPtr(result.AttributeValue),
			AttributeType:  nullStringPtr(result.AttributeType),
			CreatedAt:      nullTimePtr(result.CreatedAt),
			LastUpdatedAt:  nullTimePtr(result.LastUpdatedAt),
			Rank:           result.Rank,
		})
	}
	return items
}

func mapObjectAttributeSearchResults(results []cdb.ObjectAttributeSearchResult) []apiObjectAttributeSearchResult {
	items := make([]apiObjectAttributeSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiObjectAttributeSearchResult{
			SourceType:    result.SourceType,
			ObjectID:      result.ObjectID,
			SourceUID:     result.SourceUID,
			PageURL:       nullStringPtr(result.PageURL),
			Details:       result.Details,
			Attributes:    result.Attributes,
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			Rank:          result.Rank,
		})
	}
	return items
}

func mapObjectAttributesSearchResults(results []cdb.ObjectAttributesSearchResult) []apiObjectAttributesSearchResult {
	items := make([]apiObjectAttributesSearchResult, 0, len(results))
	for _, result := range results {
		items = append(items, apiObjectAttributesSearchResult{
			SourceType:    result.SourceType,
			ObjectID:      result.ObjectID,
			SourceUID:     result.SourceUID,
			PageURL:       nullStringPtr(result.PageURL),
			Details:       result.Details,
			Attributes:    result.Attributes,
			CreatedAt:     nullTimePtr(result.CreatedAt),
			LastUpdatedAt: nullTimePtr(result.LastUpdatedAt),
			MatchedFields: result.MatchedFields,
			Rank:          result.Rank,
		})
	}
	return items
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func registerSearchFunctionRoute(path string, handler http.HandlerFunc, description string, querySchema interface{}) {
	tags_none := []string{}
	http.Handle(path, withPublicMiddlewares(handler))
	cmn.RegisterAPIRoute(path, []string{"GET"}, description, tags_none, false, false, 200, nil, querySchema, SearchFunctionResponse{})
}
