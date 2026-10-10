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
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SQLClassificationType captures the result of classifying raw PostgreSQL text.
type SQLClassificationType string

const (
	// SQLRead authorizes db_read: structurally recognized read-only shape.
	SQLRead SQLClassificationType = "db_read"
	// SQLWrite requires db_write: explicit mutations, privileged operations,
	// and read-shaped statements with unverifiable side effects.
	SQLWrite SQLClassificationType = "db_write"
	// SQLRejected denies every identity: unparseable, empty, multi-statement,
	// or unrecognized AST input. The pre-existing `all` wildcard still
	// bypasses capability checks but never masks a rejection.
	SQLRejected = "rejected"
)

// DBQueryClassifier is the global classifier used by DBQueryAction.
var DBQueryClassifier SQLClassifier

// SQLClassifier inspects raw SQL and classifies it via pg_query_go AST parsing.
//
// Security model: the entire input is parsed once by the real PostgreSQL
// grammar; no homemade tokenizing or semicolon splitting acts as a security
// boundary (a semicolon inside a string, comment, quoted identifier, or
// dollar-quoted literal is not a second statement). SQLRead is granted only
// to an explicit allowlist of read shapes. Every other recognized statement
// form is SQLWrite; every unrecognized form is SQLRejected, so a db_read-only
// agent can never gain authorization for an arbitrary parseable statement.
//
// Function side effects cannot be proven from syntax alone. Calls to an
// allowlist of ubiquitous pure built-ins keep read candidacy; every other
// call escalates the statement to SQLWrite, which only db_write holders (who
// may already mutate) can run. Database roles and grants remain the
// authoritative defense in depth.
type SQLClassifier struct{}

// Classify evaluates raw SQL text and returns the classification.
func (c *SQLClassifier) Classify(sql string) SQLClassificationType {
	if strings.TrimSpace(sql) == "" {
		return SQLRejected
	}
	// The parser is authoritative for comments, string literals, quoted
	// identifiers, and dollar-quoted bodies.
	tree, err := pgq.Parse(sql)
	if err != nil {
		return SQLRejected
	}
	// Exactly one statement: empty/comment-only input parses to zero, and
	// any batch parses to more than one. Both are rejected.
	if len(tree.Stmts) != 1 || tree.Stmts[0] == nil || tree.Stmts[0].Stmt == nil {
		return SQLRejected
	}
	return verdictToClassification(classifyStmtNode(tree.Stmts[0].Stmt))
}

// sqlVerdict is the internal three-state outcome.
type sqlVerdict int

const (
	verdictReject sqlVerdict = iota
	verdictRead
	verdictWrite
)

func verdictToClassification(v sqlVerdict) SQLClassificationType {
	switch v {
	case verdictRead:
		return SQLRead
	case verdictWrite:
		return SQLWrite
	default:
		return SQLRejected
	}
}

func combineVerdicts(acc, next sqlVerdict) sqlVerdict {
	if acc == verdictReject || next == verdictReject {
		return verdictReject
	}
	if acc == verdictWrite || next == verdictWrite {
		return verdictWrite
	}
	return verdictRead
}

// nodeKind returns the protobuf oneof member name of a pg_query Node
// (for example "select_stmt"), or "" when unset.
func nodeKind(node *pgq.Node) string {
	if node == nil {
		return ""
	}
	m := node.ProtoReflect()
	ods := m.Descriptor().Oneofs()
	for i := 0; i < ods.Len(); i++ {
		if fd := m.WhichOneof(ods.Get(i)); fd != nil {
			return string(fd.Name())
		}
	}
	return ""
}

// classifyStmtNode is the shared dispatcher for statement Nodes: the
// top-level statement, CTE bodies, and set-operation branches.
func classifyStmtNode(node *pgq.Node) sqlVerdict {
	switch kind := nodeKind(node); {
	case kind == "select_stmt":
		return inspectSelect(node.GetSelectStmt())
	case kind == "set_operation_stmt":
		return inspectSetOperation(node.GetSetOperationStmt())
	case kind == "explain_stmt":
		return inspectExplain(node.GetExplainStmt())
	case kind == "common_table_expr":
		return inspectCommonTableExpr(node.GetCommonTableExpr())
	case kind == "common_table_expr":
		return inspectCommonTableExpr(node.GetCommonTableExpr())
	case kind == "":
		return verdictReject
	case strings.HasSuffix(kind, "_stmt"):
		// Explicit privileged-operation policy: every other recognized
		// statement form (DML, DDL, CALL, DO, COPY, transaction control,
		// session settings, informational utilities such as SHOW) requires
		// db_write. Informational utilities are privileged rather than
		// rejected so writers keep a working path; readers stay protected.
		return verdictWrite
	default:
		return verdictReject
	}
}

// inspectSelect enforces the read-shape allowlist for SELECT statements.
func inspectSelect(sel *pgq.SelectStmt) sqlVerdict {
	if sel == nil {
		return verdictReject
	}
	// SELECT ... INTO writes a new table.
	if sel.GetIntoClause() != nil {
		return verdictWrite
	}
	// SELECT ... FOR UPDATE/SHARE takes row locks.
	if len(sel.GetLockingClause()) > 0 {
		return verdictWrite
	}
	// UNION/INTERSECT/EXCEPT operands must each be reads.
	if sel.GetLarg() != nil {
		if v := inspectSelect(sel.GetLarg()); v != verdictRead {
			return v
		}
	}
	if sel.GetRarg() != nil {
		if v := inspectSelect(sel.GetRarg()); v != verdictRead {
			return v
		}
	}
	// Every remaining nested form must be recognized and side-effect free.
	return walkMessage(sel.ProtoReflect())
}

// inspectSetOperation classifies set-operation branches deterministically.
func inspectSetOperation(stmt *pgq.SetOperationStmt) sqlVerdict {
	if stmt == nil {
		return verdictReject
	}
	strongest := verdictRead
	for _, branch := range []*pgq.Node{stmt.GetLarg(), stmt.GetRarg()} {
		if branch == nil {
			continue
		}
		strongest = combineVerdicts(strongest, classifyStmtNode(branch))
		if strongest == verdictReject {
			return verdictReject
		}
	}
	return strongest
}

// inspectCommonTableExpr classifies one CTE definition by its body query.
// Data-modifying bodies (INSERT/UPDATE/DELETE) escalate to write;
// unrecognized bodies reject the whole statement.
func inspectCommonTableExpr(cte *pgq.CommonTableExpr) sqlVerdict {
	if cte == nil || cte.GetCtequery() == nil {
		return verdictReject
	}
	return classifyStmtNode(cte.GetCtequery())
}

// inspectExplain applies the explicit EXPLAIN policy. EXPLAIN ANALYZE
// executes its inner statement, so it inherits the inner verdict. Plain
// EXPLAIN executes nothing, but it never downgrades privilege: a read inner
// stays a read while anything stronger keeps its verdict.
func inspectExplain(stmt *pgq.ExplainStmt) sqlVerdict {
	if stmt == nil || stmt.Query == nil {
		return verdictReject
	}
	inner := classifyStmtNode(stmt.Query)
	if inner != verdictRead {
		return inner
	}
	for _, opt := range stmt.Options {
		if opt == nil {
			continue
		}
		if elem := opt.GetDefElem(); elem != nil && strings.EqualFold(elem.GetDefname(), "analyze") {
			return inner
		}
	}
	return verdictRead
}

// walkMessage folds one child verdict per nested message: rejection
// dominates, then write. Unrecognized message forms reject.
func walkMessage(m protoreflect.Message) sqlVerdict {
	if m == nil {
		return verdictReject
	}
	strongest := verdictRead
	fds := m.Descriptor().Fields()
	for i := 0; i < fds.Len(); i++ {
		fd := fds.Get(i)
		switch {
		case fd.IsMap():
			mv := m.Get(fd).Map()
			stopped := false
			mv.Range(func(_ protoreflect.MapKey, value protoreflect.Value) bool {
				if msg, ok := value.Interface().(protoreflect.Message); ok {
					strongest = combineVerdicts(strongest, visitMessage(msg))
				}
				if strongest == verdictReject {
					stopped = true
					return false
				}
				return true
			})
			if stopped {
				return verdictReject
			}
		case fd.IsList():
			lv := m.Get(fd).List()
			for j := 0; j < lv.Len(); j++ {
				strongest = combineVerdicts(strongest, visitMessage(lv.Get(j).Message()))
				if strongest == verdictReject {
					return verdictReject
				}
			}
		default:
			if fd.Message() == nil || !m.Has(fd) {
				continue
			}
			strongest = combineVerdicts(strongest, visitMessage(m.Get(fd).Message()))
			if strongest == verdictReject {
				return verdictReject
			}
		}
	}
	return strongest
}

// nodeMemberMessage returns the full message name wrapped by a Node's set
// oneof member (for example "pg_query.ResTarget"), or "" when unset.
func nodeMemberMessage(node *pgq.Node) string {
	if node == nil {
		return ""
	}
	m := node.ProtoReflect()
	ods := m.Descriptor().Oneofs()
	for i := 0; i < ods.Len(); i++ {
		fd := m.WhichOneof(ods.Get(i))
		if fd == nil || fd.Message() == nil {
			continue
		}
		return string(fd.Message().FullName())
	}
	return ""
}

// isStatementKind reports whether a oneof member name occupies a statement
// position (full dispatch) rather than an expression position.
func isStatementKind(kind string) bool {
	switch kind {
	case "select_stmt", "set_operation_stmt", "explain_stmt",
		"common_table_expr", "insert_stmt", "update_stmt",
		"delete_stmt", "merge_stmt":
		return true
	case "":
		return true
	}
	return strings.HasSuffix(kind, "_stmt")
}

// visitMessage classifies a single nested message.
func visitMessage(m protoreflect.Message) sqlVerdict {
	if m == nil {
		return verdictReject
	}
	switch name := string(m.Descriptor().FullName()); {
	case name == "pg_query.Node":
		node, ok := m.Interface().(*pgq.Node)
		if !ok {
			return verdictReject
		}
		if kind := nodeKind(node); isStatementKind(kind) {
			return classifyStmtNode(node)
		}
		// Expression position: the wrapped member decides.
		switch member := nodeMemberMessage(node); {
		case member == "pg_query.FuncCall":
			if v := checkFuncCallMessage(node); v != verdictRead {
				return v
			}
			return walkMessage(m)
		case member == "pg_query.SelectStmt",
			member == "pg_query.SetOperationStmt",
			transparentMessage[member]:
			return walkMessage(m)
		default:
			return verdictReject
		}
	case name == "pg_query.SelectStmt":
		sel, ok := m.Interface().(*pgq.SelectStmt)
		if !ok {
			return verdictReject
		}
		return inspectSelect(sel)
	case name == "pg_query.SetOperationStmt":
		stmt, ok := m.Interface().(*pgq.SetOperationStmt)
		if !ok {
			return verdictReject
		}
		return inspectSetOperation(stmt)
	case name == "pg_query.FuncCall":
		if v := checkFuncCall(m); v != verdictRead {
			return v
		}
		return walkMessage(m)
	case transparentMessage[name]:
		return walkMessage(m)
	default:
		return verdictReject
	}
}

// checkFuncCallMessage applies the volatility policy to a Node wrapping a
// FuncCall member.
func checkFuncCallMessage(node *pgq.Node) sqlVerdict {
	if node == nil {
		return verdictReject
	}
	m := node.ProtoReflect()
	ods := m.Descriptor().Oneofs()
	for i := 0; i < ods.Len(); i++ {
		fd := m.WhichOneof(ods.Get(i))
		if fd == nil || fd.Message() == nil {
			continue
		}
		if string(fd.Message().FullName()) != "pg_query.FuncCall" {
			return verdictReject
		}
		return checkFuncCall(m.Get(fd).Message())
	}
	return verdictReject
}

// checkFuncCall applies the conservative volatility policy: calls to
// allowlisted ubiquitous pure built-ins keep read candidacy; every other
// call escalates the statement to db_write. Arguments are still traversed
// by the caller afterwards.
func checkFuncCall(m protoreflect.Message) sqlVerdict {
	call, ok := m.Interface().(*pgq.FuncCall)
	if !ok || call == nil {
		return verdictReject
	}
	parts := make([]string, 0, len(call.Funcname))
	for _, part := range call.Funcname {
		name := funcNamePart(part)
		if name == "" {
			return verdictReject
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return verdictReject
	}
	if len(parts) > 1 && !strings.EqualFold(parts[0], "pg_catalog") {
		// Qualification outside pg_catalog means user-defined volatility.
		return verdictWrite
	}
	if !pureBuiltinFunc[strings.ToLower(parts[len(parts)-1])] {
		return verdictWrite
	}
	return verdictRead
}

// funcNamePart extracts one dotted name component from a FuncCall funcname.
func funcNamePart(node *pgq.Node) string {
	if nodeKind(node) != "string" {
		return ""
	}
	m := node.ProtoReflect()
	fd := m.Descriptor().Fields().ByName("string")
	if fd == nil || !m.Has(fd) {
		return ""
	}
	sub := m.Get(fd).Message()
	if sub == nil {
		return ""
	}
	if s, ok := sub.Interface().(*pgq.String); ok && s != nil {
		return s.Sval
	}
	return ""
}

// transparentMessage lists nested message forms that are read-neutral and
// safe to traverse. Every form not listed here (and not dispatched above)
// rejects the statement, so newly introduced grammar stays fail-closed.
// Locking/INTO markers, sequence advances, planner artifacts, DCL specs,
// and DDL fragments are deliberately absent: reaching them through the
// walker means a structural check was bypassed.
var transparentMessage = map[string]bool{
	"pg_query.A_ArrayExpr": true, "pg_query.A_Const": true,
	"pg_query.A_Expr": true, "pg_query.A_Indices": true,
	"pg_query.A_Indirection": true, "pg_query.A_Star": true,
	"pg_query.Aggref": true, "pg_query.Alias": true,
	"pg_query.ArrayCoerceExpr": true, "pg_query.ArrayExpr": true,
	"pg_query.BitString": true, "pg_query.BoolExpr": true,
	"pg_query.Boolean": true, "pg_query.BooleanTest": true,
	"pg_query.CaseExpr": true, "pg_query.CaseTestExpr": true,
	"pg_query.CaseWhen": true, "pg_query.CoalesceExpr": true,
	"pg_query.CoerceToDomain": true, "pg_query.CoerceToDomainValue": true,
	"pg_query.CoerceViaIO": true, "pg_query.CollateClause": true,
	"pg_query.CollateExpr": true, "pg_query.ColumnRef": true,
	"pg_query.ConvertRowtypeExpr": true, "pg_query.CTECycleClause": true,
	"pg_query.CTESearchClause": true, "pg_query.DistinctExpr": true,
	"pg_query.FieldSelect": true, "pg_query.FieldStore": true,
	"pg_query.Float": true, "pg_query.FromExpr": true,
	"pg_query.FuncExpr": true, "pg_query.GroupingFunc": true,
	"pg_query.GroupingSet": true, "pg_query.InferClause": true,
	"pg_query.InferenceElem": true, "pg_query.Integer": true,
	"pg_query.IntList": true, "pg_query.JoinExpr": true,
	"pg_query.JsonAggConstructor": true, "pg_query.JsonArgument": true,
	"pg_query.JsonArrayAgg": true, "pg_query.JsonArrayConstructor": true,
	"pg_query.JsonArrayQueryConstructor": true, "pg_query.JsonBehavior": true,
	"pg_query.JsonConstructorExpr": true, "pg_query.JsonExpr": true,
	"pg_query.JsonFormat": true, "pg_query.JsonFuncExpr": true,
	"pg_query.JsonIsPredicate": true, "pg_query.JsonKeyValue": true,
	"pg_query.JsonObjectAgg": true, "pg_query.JsonObjectConstructor": true,
	"pg_query.JsonOutput": true, "pg_query.JsonParseExpr": true,
	"pg_query.JsonReturning": true, "pg_query.JsonScalarExpr": true,
	"pg_query.JsonSerializeExpr": true, "pg_query.JsonTable": true,
	"pg_query.JsonTableColumn": true, "pg_query.JsonTablePath": true,
	"pg_query.JsonTablePathScan": true, "pg_query.JsonTablePathSpec": true,
	"pg_query.JsonTableSiblingJoin": true, "pg_query.JsonValueExpr": true,
	"pg_query.List": true, "pg_query.MinMaxExpr": true,
	"pg_query.MultiAssignRef": true, "pg_query.NamedArgExpr": true,
	"pg_query.NullIfExpr": true, "pg_query.NullTest": true,
	"pg_query.OidList": true, "pg_query.OpExpr": true,
	"pg_query.Param": true, "pg_query.ParamRef": true,
	"pg_query.RangeFunction": true, "pg_query.RangeSubselect": true,
	"pg_query.RangeTableFunc": true, "pg_query.RangeTableFuncCol": true,
	"pg_query.RangeTableSample": true, "pg_query.RangeTblEntry": true,
	"pg_query.RangeTblFunction": true, "pg_query.RangeTblRef": true,
	"pg_query.RangeVar": true, "pg_query.RelabelType": true,
	"pg_query.ResTarget": true, "pg_query.RowCompareExpr": true,
	"pg_query.RowExpr": true, "pg_query.ScalarArrayOpExpr": true,
	"pg_query.SetToDefault": true, "pg_query.SortBy": true,
	"pg_query.SortGroupClause": true, "pg_query.SQLValueFunction": true,
	"pg_query.String": true, "pg_query.SubLink": true,
	"pg_query.SubscriptingRef": true, "pg_query.TableFunc": true,
	"pg_query.TableSampleClause": true, "pg_query.TargetEntry": true,
	"pg_query.TypeCast": true, "pg_query.TypeName": true,
	"pg_query.Var": true, "pg_query.WindowClause": true,
	"pg_query.WindowDef": true, "pg_query.WindowFunc": true,
	"pg_query.WindowFuncRunCondition": true, "pg_query.WithClause": true,
	"pg_query.XmlExpr": true, "pg_query.XmlSerialize": true,
}

// pureBuiltinFunc allowlists ubiquitous side-effect-free built-ins (matched
// case-insensitively on the unqualified name). Anything else escalates the
// statement to db_write; this list is a conservative usability tradeoff,
// not a purity proof, and database roles stay authoritative.
var pureBuiltinFunc = map[string]bool{
	// Aggregates and window functions.
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
	"array_agg": true, "string_agg": true, "json_agg": true, "jsonb_agg": true,
	"bool_and": true, "bool_or": true, "every": true,
	"stddev": true, "stddev_pop": true, "stddev_samp": true,
	"variance": true, "var_pop": true, "var_samp": true,
	"covar_pop": true, "covar_samp": true, "corr": true,
	"regr_slope": true, "regr_intercept": true, "regr_count": true,
	"regr_r2": true, "regr_avgx": true, "regr_avgy": true,
	"regr_sxx": true, "regr_syy": true, "regr_sxy": true,
	"percentile_cont": true, "percentile_disc": true, "mode": true,
	"rank": true, "dense_rank": true, "row_number": true,
	"lag": true, "lead": true, "first_value": true, "last_value": true,
	"nth_value": true, "ntile": true, "cume_dist": true, "percent_rank": true,
	// Math.
	"abs": true, "cbrt": true, "ceil": true, "ceiling": true,
	"degrees": true, "div": true, "exp": true, "floor": true,
	"ln": true, "log": true, "log10": true, "mod": true,
	"pi": true, "power": true, "radians": true, "round": true,
	"sign": true, "sqrt": true, "trunc": true, "width_bucket": true,
	"gcd": true, "lcm": true,
	// Strings.
	"ascii": true, "bit_length": true, "btrim": true,
	"char_length": true, "character_length": true, "chr": true,
	"concat": true, "concat_ws": true, "convert_from": true, "convert_to": true,
	"encode": true, "format": true, "initcap": true,
	"left": true, "length": true, "lower": true, "lpad": true, "ltrim": true,
	"md5": true, "octet_length": true, "overlay": true,
	"position": true, "strpos": true, "quote_ident": true,
	"quote_literal": true, "quote_nullable": true,
	"regexp_match": true, "regexp_matches": true, "regexp_replace": true,
	"regexp_split_to_array": true, "regexp_split_to_table": true,
	"repeat": true, "replace": true, "reverse": true, "right": true,
	"rpad": true, "rtrim": true, "sha224": true, "sha256": true,
	"sha384": true, "sha512": true, "split_part": true, "starts_with": true,
	"substr": true, "substring": true, "translate": true, "trim": true,
	"upper": true,
	// Arrays.
	"array_append": true, "array_prepend": true, "array_cat": true,
	"array_dims": true, "array_fill": true, "array_length": true,
	"array_lower": true, "array_ndims": true, "array_position": true,
	"array_positions": true, "array_remove": true, "array_replace": true,
	"array_to_string": true, "cardinality": true, "string_to_array": true,
	"unnest": true,
	// Ranges.
	"isempty": true, "lower_inc": true, "upper_inc": true,
	"lower_inf": true, "upper_inf": true,
	// Date and time (volatile clock reads mutate nothing).
	"age": true, "clock_timestamp": true, "current_date": true,
	"current_time": true, "current_timestamp": true, "localtime": true,
	"localtimestamp": true, "now": true, "statement_timestamp": true,
	"timeofday": true, "transaction_timestamp": true,
	"date_bin": true, "date_part": true, "date_trunc": true, "extract": true,
	"make_date": true, "make_time": true, "make_timestamp": true,
	"make_timestamptz": true, "make_interval": true,
	"justify_days": true, "justify_hours": true, "justify_interval": true,
	"timezone": true, "to_char": true, "to_date": true, "to_number": true,
	"to_timestamp": true,
	// Conditionals.
	"coalesce": true, "nullif": true, "greatest": true, "least": true,
	// JSON constructors and accessors.
	"to_json": true, "to_jsonb": true, "row_to_json": true,
	"array_to_json": true, "json_build_array": true, "json_build_object": true,
	"json_object": true, "jsonb_build_array": true, "jsonb_build_object": true,
	"json_extract_path": true, "jsonb_extract_path": true,
	"json_extract_path_text": true, "jsonb_extract_path_text": true,
	"json_array_length": true, "jsonb_array_length": true,
	"json_each": true, "jsonb_each": true,
	"json_each_text": true, "jsonb_each_text": true,
	"json_object_keys": true, "jsonb_object_keys": true,
	"json_typeof": true, "jsonb_typeof": true,
	"json_strip_nulls": true, "jsonb_strip_nulls": true, "jsonb_pretty": true,
	// Introspection without side effects.
	"version": true, "current_database": true, "current_schema": true,
	"current_schemas": true, "current_user": true, "session_user": true,
	"user": true, "current_setting": true, "pg_typeof": true,
	"random": true, "gen_random_uuid": true,
}
