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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	search "github.com/pzaino/thecrowler/pkg/search"
)

const noQueryProvided = "no query provided"

func searchEngine(db *cdb.Handler) *search.Searcher {
	return search.NewSearcher(db, config)
}

func requestSearchQuery(raw string, qType int, target interface{}, value func() string, pagination func() (int, int)) (string, error) {
	if qType == getQuery {
		return raw, nil
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), target); err != nil {
		return "", err
	}
	query := strings.TrimSpace(value())
	if query == "" {
		return "", errors.New(noQueryProvided)
	}
	if pagination != nil {
		limit, offset := pagination()
		if limit < 0 || offset < 0 {
			return "", errors.New("limit and offset must be non-negative")
		}
		if limit > 0 {
			query += "&limit:" + strconv.Itoa(limit)
		}
		if offset > 0 {
			query += "&offset:" + strconv.Itoa(offset)
		}
	}
	return query, nil
}

func performSearch(query string, db *cdb.Handler) (SearchResult, error) {
	result, err := searchEngine(db).Search(query)
	if err != nil {
		return SearchResult{}, err
	}
	defer result.Rows.Close() //nolint:errcheck

	var response SearchResult
	for result.Rows.Next() {
		var item struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Summary string `json:"summary"`
			DocType string `json:"type"`
			Lang    string `json:"lang"`
			Snippet string `json:"snippet"`
		}
		if err := result.Rows.Scan(&item.Title, &item.Link, &item.Summary, &item.DocType, &item.Lang, &item.Snippet); err != nil {
			return SearchResult{}, err
		}
		response.Items = append(response.Items, item)
	}
	if err := result.Rows.Err(); err != nil {
		return SearchResult{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}

func performScreenshotSearch(query string, qType int, db *cdb.Handler) (ScreenshotResponse, error) {
	var req ScreenshotRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.URL }, nil)
	if err != nil {
		return ScreenshotResponse{}, err
	}
	result, err := searchEngine(db).SearchScreenshots(searchQuery)
	if err != nil {
		return ScreenshotResponse{}, err
	}
	defer result.Rows.Close() //nolint:errcheck

	response := ScreenshotResponse{Limit: result.Limit, Offset: result.Offset}
	for result.Rows.Next() {
		if err := result.Rows.Scan(&response.Link, &response.CreatedAt, &response.LastUpdatedAt, &response.Type, &response.Format, &response.Width, &response.Height, &response.ByteSize); err != nil {
			return ScreenshotResponse{}, err
		}
	}
	if err := result.Rows.Err(); err != nil {
		return ScreenshotResponse{}, err
	}
	return response, nil
}

func performWebObjectSearch(query string, qType int, db *cdb.Handler) (WebObjectResponse, error) {
	var req WebObjectRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.URL }, func() (int, int) { return req.Limit, req.Offset })
	if err != nil {
		return WebObjectResponse{}, err
	}
	result, err := searchEngine(db).SearchWebObjects(searchQuery)
	if err != nil {
		return WebObjectResponse{}, err
	}
	return readWebObjects(result)
}

func performWebObjectSearchBySourceID(sourceID int64, db *cdb.Handler) (WebObjectResponse, error) {
	result, err := searchEngine(db).SearchWebObjectsBySourceID(sourceID)
	if err != nil {
		return WebObjectResponse{}, err
	}
	return readWebObjects(result)
}

func readWebObjects(result *search.QueryResult) (WebObjectResponse, error) {
	defer result.Rows.Close() //nolint:errcheck
	var response WebObjectResponse
	for result.Rows.Next() {
		var row WebObjectRow
		var details []byte
		if err := result.Rows.Scan(&row.ObjectLink, &row.CreatedAt, &row.LastUpdatedAt, &row.ObjectType, &row.ObjectHash, &row.ObjectContent, &row.ObjectHTML, &details); err != nil {
			return WebObjectResponse{}, err
		}
		if !json.Valid(details) {
			return WebObjectResponse{}, fmt.Errorf("invalid WebObjects.details JSON for %q", row.ObjectLink)
		}
		row.Details = append(json.RawMessage(nil), details...)
		response.Items = append(response.Items, row)
	}
	if err := result.Rows.Err(); err != nil {
		return WebObjectResponse{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}

func performScrapedDataSearch(query string, qType int, db *cdb.Handler) (ScrapedDataResponse, error) {
	var req ScrapedDataRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.URL }, nil)
	if err != nil {
		return ScrapedDataResponse{}, err
	}
	result, err := searchEngine(db).SearchScrapedData(searchQuery)
	if err != nil {
		return ScrapedDataResponse{}, err
	}
	defer result.Rows.Close() //nolint:errcheck
	var response ScrapedDataResponse
	for result.Rows.Next() {
		var row ScrapedDataRow
		var details []byte
		if err := result.Rows.Scan(&row.SourceID, &row.URL, &row.CollectedAt, &details); err != nil {
			return ScrapedDataResponse{}, err
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &row.Details); err != nil {
				return ScrapedDataResponse{}, err
			}
		}
		response.Items = append(response.Items, row)
	}
	if err := result.Rows.Err(); err != nil {
		return ScrapedDataResponse{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}

func performCorrelatedSitesSearch(query string, qType int, db *cdb.Handler) (CorrelatedSitesResponse, error) {
	var req CorrelatedSitesRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.URL }, nil)
	if err != nil {
		return CorrelatedSitesResponse{}, err
	}
	result, err := searchEngine(db).SearchCorrelatedSites(searchQuery)
	if err != nil {
		return CorrelatedSitesResponse{}, err
	}
	defer result.Rows.Close() //nolint:errcheck
	var response CorrelatedSitesResponse
	for result.Rows.Next() {
		var row CorrelatedSitesRow
		var createdAt string
		var whois, ssl []byte
		if err := result.Rows.Scan(&row.SourceID, &row.URL, &createdAt, &whois, &ssl); err != nil {
			return CorrelatedSitesResponse{}, err
		}
		if whois != nil {
			if err := json.Unmarshal(whois, &row.WHOIS); err != nil {
				return CorrelatedSitesResponse{}, err
			}
		}
		if ssl != nil {
			if err := json.Unmarshal(ssl, &row.SSLInfo); err != nil {
				return CorrelatedSitesResponse{}, err
			}
		}
		response.Items = append(response.Items, row)
	}
	if err := result.Rows.Err(); err != nil {
		return CorrelatedSitesResponse{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}

func performNetInfoSearch(query string, qType int, db *cdb.Handler) (NetInfoResponse, error) {
	var req QueryRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.Title }, nil)
	if err != nil {
		return NetInfoResponse{}, err
	}
	result, err := searchEngine(db).SearchNetInfo(searchQuery)
	if err != nil {
		return NetInfoResponse{}, err
	}
	defer result.Rows.Close() //nolint:errcheck
	var response NetInfoResponse
	for result.Rows.Next() {
		var row NetInfoRow
		var details []byte
		if err := result.Rows.Scan(&row.CreatedAt, &row.LastUpdatedAt, &details); err != nil {
			return NetInfoResponse{}, err
		}
		if details != nil {
			if err := json.Unmarshal(details, &row.Details); err != nil {
				return NetInfoResponse{}, err
			}
		}
		response.Items = append(response.Items, row)
	}
	if err := result.Rows.Err(); err != nil {
		return NetInfoResponse{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}

func performHTTPInfoSearch(query string, qType int, db *cdb.Handler) (HTTPInfoResponse, error) {
	var req QueryRequest
	searchQuery, err := requestSearchQuery(query, qType, &req, func() string { return req.Title }, nil)
	if err != nil {
		return HTTPInfoResponse{}, err
	}
	result, err := searchEngine(db).SearchHTTPInfo(searchQuery)
	if err != nil {
		return HTTPInfoResponse{}, err
	}
	defer result.Rows.Close() //nolint:errcheck
	var response HTTPInfoResponse
	for result.Rows.Next() {
		var row HTTPInfoRow
		var details []byte
		if err := result.Rows.Scan(&row.CreatedAt, &row.LastUpdatedAt, &details); err != nil {
			return HTTPInfoResponse{}, err
		}
		if details != nil {
			if err := json.Unmarshal(details, &row.Details); err != nil {
				return HTTPInfoResponse{}, err
			}
		}
		response.Items = append(response.Items, row)
	}
	if err := result.Rows.Err(); err != nil {
		return HTTPInfoResponse{}, err
	}
	response.Queries.Limit, response.Queries.Offset = result.Limit, result.Offset
	return response, nil
}
