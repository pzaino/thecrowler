// Package search implements the search functionality for TheCrowler.
package search

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Searcher is the search engine kernel.
// It generates SQL from dorking queries and returns raw DB rows.
type Searcher struct {
	DB     *cdb.Handler
	Config cfg.Config
}

// QueryResult contains raw DB results.
type QueryResult struct {
	Rows   *sql.Rows
	Limit  int
	Offset int
	SQL    string
	Params []any
}

// NewSearcher creates a new search engine kernel.
func NewSearcher(db *cdb.Handler, cfg cfg.Config) *Searcher {
	return &Searcher{
		DB:     db,
		Config: cfg,
	}
}

// Search performs the standard SearchIndex search.
func (s *Searcher) Search(query string) (*QueryResult, error) {
	body := sqlSearchIndexBodyNoContent
	if s.Config.API.ReturnContent {
		body = sqlSearchIndexBody
	}
	return s.Execute(body, query, "")
}

// SearchScreenshots searches screenshot metadata.
func (s *Searcher) SearchScreenshots(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlScreenshotBody, query, "", "ORDER BY s.created_at DESC")
}

// SearchWebObjects searches stored web objects.
func (s *Searcher) SearchWebObjects(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlWebObjectsBody, query, "", "ORDER BY wo.created_at DESC")
}

// SearchWebObjectsBySourceID returns every stored web object associated with a
// source. Source IDs are int64 values because the database column is BIGINT.
func (s *Searcher) SearchWebObjectsBySourceID(sourceID int64) (*QueryResult, error) {
	rows, err := (*s.DB).ExecuteQuery(sqlWebObjectsBySourceID, sourceID)
	if err != nil {
		return nil, err
	}
	return &QueryResult{
		Rows:   rows,
		SQL:    sqlWebObjectsBySourceID,
		Params: []any{sourceID},
	}, nil
}

// SearchWebObjectsBySourceUID returns every stored web object associated with
// the stable public identifier of a source.
func (s *Searcher) SearchWebObjectsBySourceUID(sourceUID string) (*QueryResult, error) {
	rows, err := (*s.DB).ExecuteQuery(sqlWebObjectsBySourceUID, sourceUID)
	if err != nil {
		return nil, err
	}
	return &QueryResult{
		Rows:   rows,
		SQL:    sqlWebObjectsBySourceUID,
		Params: []any{sourceUID},
	}, nil
}

// SearchScrapedData searches scraped-data JSON documents.
func (s *Searcher) SearchScrapedData(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlScrapedDataBody+" (", query, "", ") ORDER BY sd.last_updated_at DESC")
}

// SearchCorrelatedSites searches correlated sites for a domain.
func (s *Searcher) SearchCorrelatedSites(query string) (*QueryResult, error) {
	domain, limit, offset, err := parseSelfContainedSearchInput(query)
	if err != nil {
		return nil, err
	}

	sqlQuery := sqlCorrelatedSitesBody + " ORDER BY created_at DESC LIMIT $2 OFFSET $3;"
	params := []any{domain, limit, offset}
	rows, err := (*s.DB).ExecuteQuery(sqlQuery, params...)
	if err != nil {
		return nil, err
	}
	return &QueryResult{
		Rows:   rows,
		Limit:  limit,
		Offset: offset,
		SQL:    sqlQuery,
		Params: params,
	}, nil
}

func parseSelfContainedSearchInput(input string) (string, int, int, error) {
	limit := 10
	offset := 0
	tokensData := tokenize(input)
	terms := make([]string, 0, len(tokensData))

	for i := 0; i < len(tokensData); i++ {
		value := strings.TrimSpace(tokensData[i].tValue)
		if value == "" {
			continue
		}

		var parsed int
		if value, parsed = extractControlModifier(value, "limit", limit); parsed != limit || strings.TrimSpace(value) != strings.TrimSpace(tokensData[i].tValue) {
			limit = parsed
		}
		if value, parsed = extractControlModifier(value, "offset", offset); parsed != offset || strings.TrimSpace(value) != strings.TrimSpace(tokensData[i].tValue) {
			offset = parsed
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		switch value {
		case "&limit:", "&limit=", "&offset:", "&offset=":
			if i+1 >= len(tokensData) {
				continue
			}
			nextValue := strings.TrimSpace(tokensData[i+1].tValue)
			if nextValue == "" {
				continue
			}
			if n, err := strconv.Atoi(nextValue); err == nil {
				if strings.HasPrefix(value, "&limit") {
					limit = n
				} else {
					offset = n
				}
				i++
			}
			continue
		}
		terms = append(terms, value)
	}

	domain := strings.TrimSpace(strings.Join(terms, " "))
	if domain == "" {
		return "", 0, 0, errors.New("no valid query provided")
	}
	return domain, limit, offset, nil
}

// SearchNetInfo searches collected network information.
func (s *Searcher) SearchNetInfo(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlNetInfoBody, query, "", "ORDER BY ni.created_at DESC")
}

// SearchHTTPInfo searches collected HTTP information.
func (s *Searcher) SearchHTTPInfo(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlHTTPInfoBody, query, "", "ORDER BY hi.created_at DESC")
}
