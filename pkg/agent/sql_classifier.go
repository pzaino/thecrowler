// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"strings"

	pgq "github.com/pganalyze/pg_query_go/v6"
)

// SQLClassificationType captures the result of classifying raw PostgreSQL text.
type SQLClassificationType string

const (
	SQLRead     SQLClassificationType = "db_read"
	SQLWrite    SQLClassificationType = "db_write"
	SQLRejected                       = "rejected"
)

// DBQueryClassifier is the global classifier used by DBQueryAction.
var DBQueryClassifier SQLClassifier

// SQLClassifier inspects raw SQL and classifies it via pg_query_go AST parsing.
type SQLClassifier struct{}

// Classify evaluates raw SQL text and returns the classification.
func (c *SQLClassifier) Classify(sql string) SQLClassificationType {
	return c.classifySQL(sql)
}

// classifySQL dispatches on the first keyword after stripping comments and multi-statement checks.
func (c *SQLClassifier) classifySQL(sql string) SQLClassificationType {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return SQLRejected
	}

	cleaned := stripLeadingComments(sql)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return SQLRejected
	}

	// Reject multi-statement input (allow a single trailing semicolon).
	if strings.Contains(cleaned, ";") {
		if len(splitSQLStatements(cleaned)) > 1 {
			return SQLRejected
		}
		// Single statement with trailing semicolon: strip it for parsing.
		cleaned = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cleaned), ";"))
		if cleaned == "" {
			return SQLRejected
		}
	}

	firstToken := firstNonWhitespaceToken(cleaned)

	head := strings.ToUpper(firstToken)
	switch head {
	case "EXPLAIN":
		return c.classifyInnerOfExplain(cleaned)
	case "WITH":
		return c.classifyCTE(cleaned)
	default:
		return c.classifyStatementByHead(cleaned, head)
	}
}

// classifyStatementByHead dispatches by first keyword.
func (c *SQLClassifier) classifyStatementByHead(sql, head string) SQLClassificationType {
	switch head {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return SQLWrite
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "REINDEX", "VACUUM", "ANALYZE":
		return SQLWrite
	case "GRANT", "REVOKE", "SET", "BEGIN", "COMMIT", "ROLLBACK", "COPY", "DO", "PREPARE", "EXECUTE", "DEALLOCATE":
		return SQLWrite
	case "SELECT", "VALUES":
		return c.classifySelect(sql)
	default:
		_, err := pgq.Parse(sql)
		if err != nil {
			return SQLRejected
		}
		return SQLRead
	}
}

func stripLeadingComments(sql string) string {
	result := sql
	for {
		trimmed := strings.TrimSpace(result)
		if len(trimmed) == 0 {
			return ""
		}
		if strings.HasPrefix(trimmed, "/*") {
			endIdx := strings.Index(trimmed, "*/")
			if endIdx == -1 {
				return ""
			}
			result = strings.TrimSpace(trimmed[endIdx+2:])
			continue
		}
		if len(trimmed) >= 2 && trimmed[0] == '-' && trimmed[1] == '-' {
			idx := strings.IndexAny(trimmed, "\n\r")
			if idx == -1 {
				return ""
			}
			result = strings.TrimSpace(trimmed[idx+1:])
			continue
		}
		break
	}
	return result
}

func firstNonWhitespaceToken(sql string) string {
	for i, r := range sql {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			spaceIdx := strings.IndexAny(sql[i:], " \t\n\r")
			if spaceIdx == -1 {
				return sql[i:]
			}
			return sql[i : i+spaceIdx]
		}
	}
	return ""
}

func (c *SQLClassifier) classifyInnerOfExplain(sql string) SQLClassificationType {
	rest := strings.TrimSpace(sql[len("EXPLAIN"):])
	upperRest := strings.ToUpper(strings.TrimSpace(rest))
	if strings.HasPrefix(upperRest, "ANALYZE") || strings.HasPrefix(upperRest, "ANALYSE") {
		if len(strings.TrimSpace(rest)) > len("ANALYZE") {
			rest = strings.TrimSpace(rest[len("ANALYZE"):])
		} else {
			rest = ""
		}
	}
	if rest == "" {
		return SQLRejected
	}
	rest = stripLeadingParens(rest)
	rest = strings.TrimSpace(rest)
	t, err := pgq.Parse(rest)
	if err != nil {
		return SQLRejected
	}
	return classifyAllStmts(t)
}

func stripLeadingParens(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "(") {
		return trimmed
	}
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		switch trimmed[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(trimmed[1:i])
			}
		}
	}
	return strings.TrimSpace(trimmed[1:])
}

func (c *SQLClassifier) classifyCTE(sql string) SQLClassificationType {
	t, err := pgq.Parse(sql)
	if err != nil {
		return SQLRejected
	}
	return classifyAllStmts(t)
}

func (c *SQLClassifier) classifySelect(sql string) SQLClassificationType {
	t, err := pgq.Parse(sql)
	if err != nil {
		return SQLRejected
	}
	if len(t.Stmts) != 1 || t.Stmts[0].Stmt == nil {
		return SQLRead
	}
	return classifySelectLike(t.Stmts[0].Stmt)
}

// classifySelectLike checks whether a SELECT-like AST is effectively read-only.
func classifySelectLike(node *pgq.Node) SQLClassificationType {
	sel := node.GetSelectStmt()
	if sel == nil {
		return SQLRead
	}
	return classifySelectLikeTree(sel)
}

// classifySelectLikeTree classifies a SelectStmt (recursively for UNION/INTERSECT/EXCEPT branches).
func classifySelectLikeTree(sel *pgq.SelectStmt) SQLClassificationType {
	if sel == nil {
		return SQLRead
	}

	// SELECT ... INTO → write
	if sel.GetIntoClause() != nil {
		return SQLWrite
	}

	// SELECT ... FOR UPDATE / FOR SHARE → write
	if len(sel.GetLockingClause()) > 0 {
		return SQLWrite
	}

	// WITH clause at sub-query level → check CTE bodies for DML.
	if sel.GetWithClause() != nil {
		if hasDMLInWithClause(sel.GetWithClause()) {
			return SQLWrite
		}
	}

	// Recurse into UNION/INTERSECT/EXCEPT operands (Larg/Rarg).
	if sel.GetLarg() != nil {
		if classifySelectLikeTree(sel.GetLarg()) == SQLWrite {
			return SQLWrite
		}
	}
	if sel.GetRarg() != nil {
		if classifySelectLikeTree(sel.GetRarg()) == SQLWrite {
			return SQLWrite
		}
	}

	// Check FromClause subqueries for writes.
	for _, fn := range sel.GetFromClause() {
		if subq := fn.GetRangeSubselect(); subq != nil && subq.GetSubquery() != nil {
			inner := subq.GetSubquery().GetSelectStmt()
			if inner != nil {
				if classifySelectLikeTree(inner) == SQLWrite {
					return SQLWrite
				}
			}
		}
	}

	return SQLRead
}

// splitSQLStatements splits raw SQL on ';' respecting single/double quotes.
// It returns the non-empty trimmed segments.
func splitSQLStatements(sql string) []string {
	var parts []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch {
		case inSingle:
			cur.WriteByte(ch)
			if ch == '\'' {
				// Handle '' escape inside string literal.
				if i+1 < len(sql) && sql[i+1] == '\'' {
					cur.WriteByte(sql[i+1])
					i++
				} else {
					inSingle = false
				}
			}
		case inDouble:
			cur.WriteByte(ch)
			if ch == '"' {
				inDouble = false
			}
		case ch == '\'':
			inSingle = true
			cur.WriteByte(ch)
		case ch == '"':
			inDouble = true
			cur.WriteByte(ch)
		case ch == ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				parts = append(parts, s)
			}
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

// hasDMLInWithClause checks if any CTE in the WithClause has a DML query body.
func hasDMLInWithClause(wc *pgq.WithClause) bool {
	if wc == nil {
		return false
	}
	// WithClause.Ctes is []*Node; each wraps a CommonTableExpr.
	for _, n := range wc.GetCtes() {
		if n == nil {
			continue
		}
		if bodyNode := getNodeCTEQuery(n); bodyNode != nil {
			if classifyNodeForDML(bodyNode) == SQLWrite {
				return true
			}
		}
	}
	return false
}

// getNodeCTEQuery extracts the CTE body query from a Node in WithClause.Ctes.
func getNodeCTEQuery(n *pgq.Node) *pgq.Node {
	if n == nil {
		return nil
	}
	if cte := n.GetCommonTableExpr(); cte != nil {
		return cte.GetCtequery()
	}
	return nil
}

// classifyNodeForDML checks if a Node represents a DML or a SELECT with writes.
func classifyNodeForDML(node *pgq.Node) SQLClassificationType {
	if node == nil {
		return SQLRead
	}
	switch {
	case node.GetInsertStmt() != nil:
		return SQLWrite
	case node.GetUpdateStmt() != nil:
		return SQLWrite
	case node.GetDeleteStmt() != nil:
		return SQLWrite
	case node.GetMergeStmt() != nil:
		return SQLWrite
	case node.GetSelectStmt() != nil:
		return classifySelectLikeTree(node.GetSelectStmt())
	}
	return SQLRead
}

// classifyAllStmts classifies each statement and returns the strongest.
func classifyAllStmts(tree *pgq.ParseResult) SQLClassificationType {
	strongest := SQLRead
	for _, raw := range tree.Stmts {
		if raw.Stmt == nil {
			continue
		}
		cls := classifyNodeForDML(raw.Stmt)
		if cls == SQLWrite {
			return SQLWrite
		}
		if cls == SQLRead {
			strongest = SQLRead
		}
	}
	return strongest
}
