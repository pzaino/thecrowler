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
