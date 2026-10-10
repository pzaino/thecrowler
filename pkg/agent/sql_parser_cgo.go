//go:build cgo

package agent

import pgq "github.com/pganalyze/pg_query_go/v6"

// parsePostgresSQL uses the native PostgreSQL parser when CGO is enabled.
// The parsed protobuf AST is consumed by sql_classifier.go in both builds.
func parsePostgresSQL(sql string) (*pgq.ParseResult, error) {
	return pgq.Parse(sql)
}
