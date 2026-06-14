// Package search implements the search functionality for TheCrowler.
package search

import (
	"database/sql"

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

// SearchScrapedData searches scraped-data JSON documents.
func (s *Searcher) SearchScrapedData(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlScrapedDataBody+" (", query, "", ") ORDER BY sd.last_updated_at DESC")
}

// SearchCorrelatedSites searches correlated sites for a domain.
func (s *Searcher) SearchCorrelatedSites(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlCorrelatedSitesBody, query, "self-contained", "ORDER BY created_at DESC")
}

// SearchNetInfo searches collected network information.
func (s *Searcher) SearchNetInfo(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlNetInfoBody, query, "", "ORDER BY ni.created_at DESC")
}

// SearchHTTPInfo searches collected HTTP information.
func (s *Searcher) SearchHTTPInfo(query string) (*QueryResult, error) {
	return s.ExecuteOrdered(sqlHTTPInfoBody, query, "", "ORDER BY hi.created_at DESC")
}
