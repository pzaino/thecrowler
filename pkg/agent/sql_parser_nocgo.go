//go:build !cgo

package agent

import (
	pgq "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// parsePostgresSQL uses a Wasm-backed libpg_query parser when CGO is off.
// The wrapper returns the same pg_query_go protobuf AST as the native parser.
func parsePostgresSQL(sql string) (*pgq.ParseResult, error) {
	return pgquery.Parse(sql)
}
