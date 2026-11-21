package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrAsyncGoalPersistenceUnsupported means the database backend has no
	// asynchronous goal persistence contract in this release.
	ErrAsyncGoalPersistenceUnsupported = errors.New("asynchronous goal persistence is unsupported by this database backend")
	// ErrAsyncGoalPersistenceSchemaUnavailable means schema 1.17 is present but
	// the additive asynchronous goal structures are missing or incompatible.
	ErrAsyncGoalPersistenceSchemaUnavailable = errors.New("asynchronous goal persistence schema is unavailable")
)

// AsyncGoalPersistenceSupported reports whether this release has a goal
// persistence contract for the backend. MySQL is deliberately unsupported.
func AsyncGoalPersistenceSupported(backend string) bool {
	switch normalizeDBMS(backend) {
	case "postgres", "sqlite":
		return true
	default:
		return false
	}
}

var asyncGoalRequiredColumns = map[string][]string{
	"AgentGoalRuns": {
		"goal_run_id", "checkpoint_version", "contract_version", "revision",
		"trace_id", "correlation_id", "agent_id", "owner", "principal_kind",
		"principal_user_id", "principal_username", "principal_issuer",
		"goal_id", "goal_spec_hash", "policy_sha256", "state", "slices",
		"created_at", "updated_at", "deadline", "reason_code", "ledger_id",
		"run_nonce_hash", "last_transition_id", "last_transition_hash",
		"verification_verifier", "verification_sha256", "verified_at",
		"cancel_requested_at", "cancel_requested_by_user_id", "max_slices",
		"max_model_rounds", "max_tool_attempts", "max_result_bytes",
		"max_evidence_records", "max_evidence_bytes", "max_retries",
		"max_scheduled_work_requests", "max_consecutive_denials",
		"max_slice_elapsed_ns", "used_model_rounds", "used_tool_attempts",
		"used_result_bytes", "used_evidence_records", "used_evidence_bytes",
		"used_retries", "used_scheduled_work_requests", "used_consecutive_denials",
	},
	"AgentGoalRunTools":   {"goal_run_id", "tool_name"},
	"AgentGoalRunSources": {"goal_run_id", "source_id"},
	"AgentGoalRunWork": {
		"goal_run_id", "request_id", "kind", "state", "proposal_sha256",
		"approval_id", "approval_sha256", "operation_id", "source_id",
		"proposal_expires_at", "approval_expires_at", "submit_by",
		"accepted_at", "observed_at", "created_at", "updated_at",
	},
	"AgentGoalRunEvidence": {
		"goal_run_id", "evidence_id", "kind", "trust_class", "producer",
		"source_ids", "observed_at", "expires_at", "payload_sha256",
		"record_sha256", "size_bytes", "created_at",
	},
	"AgentGoalRunVerificationEvidence": {"goal_run_id", "evidence_id", "created_at"},
	"AgentGoalRunAuditRefs":            {"goal_run_id", "audit_ref", "created_at"},
	"AgentGoalRunTransitions": {
		"goal_run_id", "revision", "operation_id", "transition_hash",
		"from_state", "to_state", "reason_code", "created_at",
	},
}

var asyncGoalForbiddenColumns = []string{
	"payload", "raw_payload", "prompt", "prompts", "messages",
	"tool_arguments", "arguments", "raw_result", "result",
	"token", "password", "secret", "dsn", "cookie", "cookies",
	"request_body", "response_body", "model_text",
}

var asyncGoalRequiredIndexes = []string{
	"idx_agent_goal_runs_state_deadline", "idx_agent_goal_runs_principal",
	"idx_agent_goal_runs_agent", "idx_agent_goal_runs_owner",
	"idx_agent_goal_work_state", "idx_agent_goal_evidence_observed",
	"idx_agent_goal_transitions_created",
	"uq_agent_goal_work_operation", "uq_agent_goal_work_approval",
	"uq_agent_goal_evidence_record", "uq_agent_goal_transition_operation",
}

var asyncGoalUniqueColumns = []struct {
	table   string
	columns []string
}{
	{"AgentGoalRunWork", []string{"goal_run_id", "operation_id"}},
	{"AgentGoalRunWork", []string{"goal_run_id", "approval_id"}},
	{"AgentGoalRunEvidence", []string{"goal_run_id", "record_sha256"}},
	{"AgentGoalRunTransitions", []string{"goal_run_id", "operation_id"}},
}

type asyncGoalForeignKeySpec struct {
	table         string
	name          string
	parent        string
	columns       []string
	parentColumns []string
}

var asyncGoalForeignKeySpecs = []asyncGoalForeignKeySpec{
	{"AgentGoalRunTools", "fk_agent_goal_tool_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
	{"AgentGoalRunSources", "fk_agent_goal_source_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
	{"AgentGoalRunWork", "fk_agent_goal_work_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
	{"AgentGoalRunWork", "fk_agent_goal_work_source", "AgentGoalRunSources",
		[]string{"goal_run_id", "source_id"}, []string{"goal_run_id", "source_id"}},
	{"AgentGoalRunEvidence", "fk_agent_goal_evidence_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
	{"AgentGoalRunVerificationEvidence", "fk_agent_goal_verification_evidence", "AgentGoalRunEvidence",
		[]string{"goal_run_id", "evidence_id"}, []string{"goal_run_id", "evidence_id"}},
	{"AgentGoalRunAuditRefs", "fk_agent_goal_audit_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
	{"AgentGoalRunTransitions", "fk_agent_goal_transition_run", "AgentGoalRuns",
		[]string{"goal_run_id"}, []string{"goal_run_id"}},
}

var asyncGoalRequiredChecks = map[string][]string{
	"AgentGoalRuns": {
		"chk_agent_goal_versions", "chk_agent_goal_revision",
		"chk_agent_goal_state", "chk_agent_goal_principal",
		"chk_agent_goal_identity", "chk_agent_goal_hashes",
		"chk_agent_goal_timestamps", "chk_agent_goal_active_deadline",
		"chk_agent_goal_ceilings", "chk_agent_goal_used",
		"chk_agent_goal_verification", "chk_agent_goal_last_transition",
		"chk_agent_goal_cancel", "chk_agent_goal_reason",
	},
	"AgentGoalRunTools":   {"chk_agent_goal_tool_name"},
	"AgentGoalRunSources": {"chk_agent_goal_source_id"},
	"AgentGoalRunWork": {
		"chk_agent_goal_work_ids", "chk_agent_goal_work_state",
		"chk_agent_goal_work_times",
	},
	"AgentGoalRunEvidence": {
		"chk_agent_goal_evidence_ids", "chk_agent_goal_evidence_kind",
		"chk_agent_goal_evidence_sources", "chk_agent_goal_evidence_times",
		"chk_agent_goal_evidence_size",
	},
	"AgentGoalRunVerificationEvidence": {"chk_agent_goal_verification_evidence_time"},
	"AgentGoalRunAuditRefs":            {"chk_agent_goal_audit_ref"},
	"AgentGoalRunTransitions": {
		"chk_agent_goal_transition_ids", "chk_agent_goal_transition_states",
		"chk_agent_goal_transition_reason",
	},
}

// CheckAsyncGoalPersistenceSchema verifies the additive schema-1.17 goal
// persistence objects. It is read-only and deliberately not called by normal
// service startup. The future transactional store must require it before
// opening a write path.
func CheckAsyncGoalPersistenceSchema(ctx context.Context, db *Handler) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if !AsyncGoalPersistenceSupported((*db).DBMS()) {
		return ErrAsyncGoalPersistenceUnsupported
	}
	missing := make([]string, 0)

	version, err := CurrentSchemaVersion(ctx, *db)
	if err != nil || version != RequiredSchemaVersion {
		missing = append(missing, "schema version 1.17")
	}
	var generation string
	if err := (*db).QueryRowContext(ctx,
		`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1`).Scan(&generation); err != nil ||
		generation != WriterGeneration {
		missing = append(missing, "writer generation "+WriterGeneration)
	}

	for table, columns := range asyncGoalRequiredColumns {
		if err := probeColumns(ctx, *db, table, columns); err != nil {
			missing = append(missing, table+"("+strings.Join(columns, ",")+")")
			continue
		}
		actual, err := asyncGoalTableColumns(ctx, *db, table)
		if err != nil {
			missing = append(missing, table+"(metadata)")
			continue
		}
		for _, forbidden := range asyncGoalForbiddenColumns {
			if actual[forbidden] {
				missing = append(missing, table+"."+forbidden+"(forbidden)")
			}
		}
	}
	for _, index := range asyncGoalRequiredIndexes {
		if !metadataObjectExists(ctx, *db, "index", index) {
			missing = append(missing, "index "+index)
		}
	}
	for _, unique := range asyncGoalUniqueColumns {
		if !tableUniqueColumnsExist(ctx, *db, unique.table, unique.columns) {
			missing = append(missing, "unique "+unique.table+"("+strings.Join(unique.columns, ",")+")")
		}
	}
	for table := range asyncGoalRequiredColumns {
		if !tableConstraintExists(ctx, *db, table, "PRIMARY KEY") {
			missing = append(missing, "primary key on "+table)
		}
	}
	for _, spec := range asyncGoalForeignKeySpecs {
		if !asyncGoalForeignKeyMatches(ctx, *db, spec) {
			missing = append(missing, "foreign key "+spec.name+" on "+spec.table)
		}
	}
	for table, expected := range asyncGoalForeignKeyCounts() {
		if !asyncGoalForeignKeyCountMatches(ctx, *db, table, expected) {
			missing = append(missing, "foreign key set on "+table)
		}
	}
	for table, names := range asyncGoalRequiredChecks {
		for _, name := range names {
			if !asyncGoalNamedConstraintExists(ctx, *db, table, name, "c") {
				missing = append(missing, "check "+name+" on "+table)
			}
		}
	}
	if normalizeDBMS((*db).DBMS()) == "postgres" {
		for table, privileges := range map[string]string{
			"AgentGoalRuns":                    "SELECT,INSERT,UPDATE",
			"AgentGoalRunTools":                "SELECT,INSERT",
			"AgentGoalRunSources":              "SELECT,INSERT",
			"AgentGoalRunWork":                 "SELECT,INSERT,UPDATE,DELETE",
			"AgentGoalRunEvidence":             "SELECT,INSERT",
			"AgentGoalRunVerificationEvidence": "SELECT,INSERT",
			"AgentGoalRunAuditRefs":            "SELECT,INSERT",
			"AgentGoalRunTransitions":          "SELECT,INSERT",
		} {
			var allowed bool
			if err := (*db).QueryRowContext(ctx,
				`SELECT has_table_privilege(current_user, $1, $2)`,
				strings.ToLower(table), privileges).Scan(&allowed); err != nil || !allowed {
				missing = append(missing, "privileges "+table)
			}
		}
	} else {
		if err := CheckSQLiteForeignKeyEnforcement(ctx, *db); err != nil {
			missing = append(missing, err.Error())
		}
	}

	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: missing or invalid: %s", ErrAsyncGoalPersistenceSchemaUnavailable, strings.Join(missing, ", "))
	}
	return nil
}

func asyncGoalTableColumns(ctx context.Context, db Handler, table string) (map[string]bool, error) {
	out := map[string]bool{}
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		rows, err := db.QueryContext(ctx,
			`SELECT lower(column_name) FROM information_schema.columns
			 WHERE table_schema = current_schema() AND lower(table_name) = lower($1)`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				return nil, err
			}
			out[column] = true
		}
		return out, rows.Err()
	case "sqlite":
		rows, err := db.QueryContext(ctx, `SELECT lower(name) FROM pragma_table_info(?)`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				return nil, err
			}
			out[column] = true
		}
		return out, rows.Err()
	default:
		return nil, ErrAsyncGoalPersistenceUnsupported
	}
}

func asyncGoalNamedConstraintExists(ctx context.Context, db Handler, table, name, contype string) bool {
	var n int
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_constraint c
			 JOIN pg_class t ON t.oid = c.conrelid
			 JOIN pg_namespace ns ON ns.oid = t.relnamespace
			 WHERE ns.nspname = current_schema()
			   AND lower(t.relname) = lower($1)
			   AND c.conname = $2
			   AND c.contype = $3`, table, name, contype).Scan(&n)
		return err == nil && n > 0
	case "sqlite":
		if contype != "c" {
			return false
		}
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master
			 WHERE type = 'table' AND lower(tbl_name) = lower(?)
			   AND instr(lower(sql), ?) > 0`,
			table, "constraint "+strings.ToLower(name)+" check").Scan(&n)
		return err == nil && n > 0
	default:
		return false
	}
}

type asyncGoalSQLiteForeignKeyGroup struct {
	parent     string
	columns    []string
	parentCols []string
	onDelete   string
}

func asyncGoalForeignKeyMatches(ctx context.Context, db Handler, spec asyncGoalForeignKeySpec) bool {
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		var definition string
		err := db.QueryRowContext(ctx,
			`SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
			 JOIN pg_class t ON t.oid = c.conrelid
			 JOIN pg_namespace ns ON ns.oid = t.relnamespace
			 WHERE ns.nspname = current_schema()
			   AND lower(t.relname) = lower($1)
			   AND c.conname = $2
			   AND c.contype = 'f'`, spec.table, spec.name).Scan(&definition)
		if err != nil {
			return false
		}
		definition = strings.ToLower(definition)
		childColumns := strings.Join(spec.columns, ", ")
		parentColumns := strings.Join(spec.parentColumns, ", ")
		return strings.Contains(definition, "foreign key ("+childColumns+")") &&
			strings.Contains(definition, strings.ToLower(spec.parent)+"("+parentColumns+")") &&
			strings.Contains(definition, "on delete cascade")
	case "sqlite":
		rows, err := db.QueryContext(ctx,
			`SELECT id, seq, "table", "from", COALESCE("to", ''), on_delete
			 FROM pragma_foreign_key_list(?) ORDER BY id, seq`, spec.table)
		if err != nil {
			return false
		}
		defer rows.Close()
		groups := map[int]*asyncGoalSQLiteForeignKeyGroup{}
		order := make([]int, 0)
		for rows.Next() {
			var id, seq int
			var parent, column, parentColumn, onDelete string
			if err := rows.Scan(&id, &seq, &parent, &column, &parentColumn, &onDelete); err != nil {
				return false
			}
			group, ok := groups[id]
			if !ok {
				group = &asyncGoalSQLiteForeignKeyGroup{parent: parent, onDelete: onDelete}
				groups[id] = group
				order = append(order, id)
			}
			group.columns = append(group.columns, column)
			group.parentCols = append(group.parentCols, parentColumn)
		}
		if err := rows.Err(); err != nil {
			return false
		}
		used := make(map[int]bool, len(groups))
		for _, id := range order {
			group := groups[id]
			if used[id] || !strings.EqualFold(group.parent, spec.parent) ||
				!equalFoldStrings(group.columns, spec.columns) ||
				!equalFoldStrings(group.parentCols, spec.parentColumns) ||
				!strings.EqualFold(group.onDelete, "CASCADE") {
				continue
			}
			used[id] = true
			return true
		}
		return false
	default:
		return false
	}
}

func equalFoldStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(left[i], right[i]) {
			return false
		}
	}
	return true
}

func asyncGoalForeignKeyCounts() map[string]int {
	counts := map[string]int{}
	for _, spec := range asyncGoalForeignKeySpecs {
		counts[spec.table]++
	}
	return counts
}

func asyncGoalForeignKeyCountMatches(ctx context.Context, db Handler, table string, expected int) bool {
	var count int
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_constraint c
			 JOIN pg_class t ON t.oid = c.conrelid
			 JOIN pg_namespace ns ON ns.oid = t.relnamespace
			 WHERE ns.nspname = current_schema()
			   AND lower(t.relname) = lower($1)
			   AND c.contype = 'f'`, table).Scan(&count)
		return err == nil && count == expected
	case "sqlite":
		err := db.QueryRowContext(ctx,
			`SELECT count(DISTINCT id) FROM pragma_foreign_key_list(?)`, table).Scan(&count)
		return err == nil && count == expected
	default:
		return false
	}
}
