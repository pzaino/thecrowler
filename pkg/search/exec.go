package search

import (
	"strconv"
	//cdb "github.com/pzaino/thecrowler/pkg/database"
)

func (s *Searcher) execParsed(p *ParsedQuery) (*QueryResult, error) {
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
