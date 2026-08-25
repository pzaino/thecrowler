// Package search implements the search functionality for TheCrowler.
package search

import (
	"context"
	"strconv"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// ExecParsed executes a parsed query and returns the results.
func (s *Searcher) ExecParsed(p *ParsedQuery) (*QueryResult, error) {
	return s.ExecParsedContext(context.Background(), p)
}
func (s *Searcher) ExecParsedContext(ctx context.Context, p *ParsedQuery) (*QueryResult, error) {
	sqlQuery := p.sqlQuery
	params := p.sqlParams

	limitIndex := len(params) - 1
	offsetIndex := len(params)

	sqlQuery += " LIMIT $" + strconv.Itoa(limitIndex) +
		" OFFSET $" + strconv.Itoa(offsetIndex) + ";"
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Generated SQL query: %s; parameters: %v", sqlQuery, params)

	rows, err := (*s.DB).QueryContext(ctx, sqlQuery, params...)
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
	return s.ExecuteContext(context.Background(), queryBody, query, parsingType)
}
func (s *Searcher) ExecuteContext(ctx context.Context, queryBody, query, parsingType string) (*QueryResult, error) {
	return s.ExecuteOrderedContext(ctx, queryBody, query, parsingType, "")
}

// ExecuteOrdered parses and executes a dorking query, appending orderBy before
// the LIMIT and OFFSET clauses.
func (s *Searcher) ExecuteOrdered(queryBody, query, parsingType, orderBy string) (*QueryResult, error) {
	return s.ExecuteOrderedContext(context.Background(), queryBody, query, parsingType, orderBy)
}
func (s *Searcher) ExecuteOrderedContext(ctx context.Context, queryBody, query, parsingType, orderBy string) (*QueryResult, error) {
	parsed, err := s.ParseAdvancedQuery(queryBody, query, parsingType)
	if err != nil {
		return nil, err
	}
	if orderBy != "" {
		parsed.sqlQuery += " " + orderBy
	}
	return s.ExecParsedContext(ctx, &parsed)
}
