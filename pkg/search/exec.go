// Package search implements the search functionality for TheCrowler.
package search

import (
	"strconv"
)

// ExecParsed executes a parsed query and returns the results.
func (s *Searcher) ExecParsed(p *ParsedQuery) (*QueryResult, error) {
	sqlQuery := p.sqlQuery
	params := p.sqlParams

	limitIndex := len(params) - 1
	offsetIndex := len(params)

	sqlQuery += " LIMIT $" + strconv.Itoa(limitIndex) +
		" OFFSET $" + strconv.Itoa(offsetIndex) + ";"

	rows, err := (*s.DB).ExecuteQuery(sqlQuery, params...)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Rows:   rows,
		Limit:  p.limit,
		Offset: p.offset,
		SQL:    sqlQuery,
		Params: params,
	}, nil
}

// Execute parses a dorking query against queryBody and executes it with
// pagination. Callers that need a custom ORDER BY should use ExecuteOrdered.
func (s *Searcher) Execute(queryBody, query, parsingType string) (*QueryResult, error) {
	return s.ExecuteOrdered(queryBody, query, parsingType, "")
}

// ExecuteOrdered parses and executes a dorking query, appending orderBy before
// the LIMIT and OFFSET clauses.
func (s *Searcher) ExecuteOrdered(queryBody, query, parsingType, orderBy string) (*QueryResult, error) {
	parsed, err := s.ParseAdvancedQuery(queryBody, query, parsingType)
	if err != nil {
		return nil, err
	}
	if orderBy != "" {
		parsed.sqlQuery += " " + orderBy
	}
	return s.ExecParsed(&parsed)
}
