// Copyright 2023 Paolo Fabio Zaino
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

package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// SourceCrawlAttemptCapability is the additive schema-1.17 capability that
	// enables fenced source-crawl attempt ownership.
	SourceCrawlAttemptCapability = "source-crawl-attempt-v1"
	// SourceCrawlAttemptCapabilityChecksum is the canonical capability
	// descriptor checksum stored in SchemaCapabilities.
	SourceCrawlAttemptCapabilityChecksum = "f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e"
)

var (
	// ErrSourceCrawlAttemptUnsupported means the backend does not support the
	// optional fenced attempt store.
	ErrSourceCrawlAttemptUnsupported = errors.New("source crawl attempt persistence is unsupported by this database backend")
	// ErrSourceCrawlAttemptSchemaUnavailable means schema 1.17 is present but
	// the additive attempt objects or marker are missing/incompatible.
	ErrSourceCrawlAttemptSchemaUnavailable = errors.New("source crawl attempt schema is unavailable")
)

// SourceCrawlAttemptSupported reports whether this release has an attempt
// store for the backend. MySQL remains legacy-only.
func SourceCrawlAttemptSupported(backend string) bool {
	switch normalizeDBMS(backend) {
	case "postgres", "sqlite":
		return true
	default:
		return false
	}
}

var sourceCrawlAttemptRequiredColumns = map[string][]string{
	"SourceCrawlAttempts": {
		"attempt_id", "claim_request_id", "source_id", "generation", "engine_id",
		"state", "claimed_at", "lease_expires_at", "superseded_by_generation",
		"terminal_at", "reason_code", "operation_id", "request_id", "run_id",
		"agent_id", "owner", "principal_user_id", "approval_id",
		"approval_sha256", "created_at", "last_updated_at",
	},
	"SourceCrawlClaims": {
		"source_id", "attempt_id", "claim_request_id", "generation", "engine_id",
		"claimed_at", "lease_expires_at", "last_updated_at",
	},
	"SourceCrawlClaimPolicy": {"policy_id", "mode", "updated_at"},
	"SchemaCapabilities":     {"capability", "applied_at", "applied_by", "checksum", "details"},
}

var sourceCrawlAttemptForbiddenColumns = []string{
	"payload", "raw_payload", "prompt", "prompts", "messages",
	"tool_arguments", "arguments", "raw_result", "result", "token",
	"password", "secret", "dsn", "cookie", "cookies", "request_body",
	"response_body", "model_text", "html",
}

var sourceCrawlAttemptRequiredIndexes = []string{
	"idx_source_crawl_attempts_source",
	"idx_source_crawl_attempts_request",
	"idx_source_crawl_attempts_lease",
	"uq_source_crawl_attempts_active_operation",
	"idx_source_crawl_claims_attempt",
	"idx_source_crawl_claims_request",
}

var sourceCrawlAttemptUniqueColumns = []struct {
	table   string
	columns []string
}{
	{"SourceCrawlAttempts", []string{"source_id", "generation"}},
	{"SourceCrawlAttempts", []string{"source_id", "generation", "attempt_id"}},
	{"SourceCrawlAttempts", []string{"claim_request_id", "source_id"}},
	{"SourceCrawlClaims", []string{"source_id", "generation"}},
	{"SourceCrawlClaims", []string{"attempt_id"}},
}

type sourceCrawlForeignKeySpec struct {
	table         string
	name          string
	parent        string
	columns       []string
	parentColumns []string
	onDelete      string
}

var sourceCrawlAttemptForeignKeySpecs = []sourceCrawlForeignKeySpec{
	{"SourceCrawlClaims", "fk_source_crawl_claim_source", "Sources",
		[]string{"source_id"}, []string{"source_id"}, "CASCADE"},
	{"SourceCrawlClaims", "fk_source_crawl_claim_exact_attempt", "SourceCrawlAttempts",
		[]string{"source_id", "generation", "attempt_id"},
		[]string{"source_id", "generation", "attempt_id"}, ""},
}

var sourceCrawlAttemptRequiredChecks = map[string][]string{
	"SourceCrawlAttempts": {
		"chk_source_crawl_attempt_request", "chk_source_crawl_attempt_id",
		"chk_source_crawl_attempt_lease", "chk_source_crawl_attempt_binding",
		"chk_source_crawl_attempt_state", "chk_source_crawl_attempt_reason",
	},
	"SourceCrawlClaims": {
		"chk_source_crawl_claim_request", "chk_source_crawl_claim_lease",
	},
}

// CheckSourceCrawlAttemptSchema verifies the additive schema-1.17 attempt
// objects. It is read-only and deliberately not called by ordinary service
// startup. The opt-in claim API must require it before mutation.
func CheckSourceCrawlAttemptSchema(ctx context.Context, db *Handler) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return ErrSourceCrawlAttemptUnsupported
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

	for table, columns := range sourceCrawlAttemptRequiredColumns {
		if err := probeColumns(ctx, *db, table, columns); err != nil {
			missing = append(missing, table+"("+strings.Join(columns, ",")+")")
			continue
		}
		actual, err := asyncGoalTableColumns(ctx, *db, table)
		if err != nil {
			missing = append(missing, table+"(metadata)")
			continue
		}
		for _, forbidden := range sourceCrawlAttemptForbiddenColumns {
			if actual[forbidden] {
				missing = append(missing, table+"."+forbidden+"(forbidden)")
			}
		}
	}
	for _, index := range sourceCrawlAttemptRequiredIndexes {
		if !metadataObjectExists(ctx, *db, "index", index) {
			missing = append(missing, "index "+index)
		}
	}
	for _, unique := range sourceCrawlAttemptUniqueColumns {
		if !tableUniqueColumnsExist(ctx, *db, unique.table, unique.columns) {
			missing = append(missing, "unique "+unique.table+"("+strings.Join(unique.columns, ",")+")")
		}
	}
	for table := range sourceCrawlAttemptRequiredColumns {
		ok := false
		if normalizeDBMS((*db).DBMS()) == "postgres" {
			ok = sourceCrawlPostgresPrimaryKeyExists(ctx, *db, table)
		} else {
			ok = tableConstraintExists(ctx, *db, table, "PRIMARY KEY")
		}
		if !ok {
			missing = append(missing, "primary key on "+table)
		}
	}
	if normalizeDBMS((*db).DBMS()) == "postgres" {
		for _, spec := range sourceCrawlAttemptForeignKeySpecs {
			if !sourceCrawlPostgresForeignKeyMatches(ctx, *db, spec) {
				missing = append(missing, "foreign key "+spec.name+" on "+spec.table)
			}
		}
		for table, names := range sourceCrawlAttemptRequiredChecks {
			for _, name := range names {
				if !asyncGoalNamedConstraintExists(ctx, *db, table, name, "c") {
					missing = append(missing, "check "+name+" on "+table)
				}
			}
		}
		var policyMode string
		if err := (*db).QueryRowContext(ctx, `SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).Scan(&policyMode); err != nil ||
			(policyMode != "legacy" && policyMode != "fenced") {
			missing = append(missing, "SourceCrawlClaimPolicy mode")
		}
		var modeFunction bool
		if err := (*db).QueryRowContext(ctx,
			`SELECT to_regprocedure('set_source_crawl_claim_mode(text)') IS NOT NULL`).Scan(&modeFunction); err != nil || !modeFunction {
			missing = append(missing, "set_source_crawl_claim_mode function")
		}
		var fencedAware bool
		if err := (*db).QueryRowContext(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
				WHERE n.nspname = current_schema() AND p.proname = 'update_sources'
				  AND pg_get_functiondef(p.oid) LIKE '%SourceCrawlClaimPolicy%'
				  AND pg_get_functiondef(p.oid) LIKE '%fenced%'
			)`).Scan(&fencedAware); err != nil || !fencedAware {
			missing = append(missing, "fenced update_sources function")
		}
		for table, privileges := range map[string]string{
			"SourceCrawlAttempts":    "SELECT,INSERT,UPDATE",
			"SourceCrawlClaims":      "SELECT,INSERT,UPDATE",
			"SourceCrawlClaimPolicy": "SELECT",
			"SchemaCapabilities":     "SELECT,INSERT",
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
		for _, spec := range sourceCrawlAttemptForeignKeySpecs {
			if !sourceCrawlSQLiteForeignKeyExists(ctx, *db, spec) {
				missing = append(missing, "foreign key "+spec.name+" on "+spec.table)
			}
		}
		var policyMode string
		if err := (*db).QueryRowContext(ctx, `SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).Scan(&policyMode); err != nil ||
			(policyMode != "legacy" && policyMode != "fenced") {
			missing = append(missing, "SourceCrawlClaimPolicy mode")
		}
	}

	var checksum string
	if err := (*db).QueryRowContext(ctx,
		`SELECT checksum FROM SchemaCapabilities WHERE capability = `+sourceCrawlPlaceholder(*db, 1),
		SourceCrawlAttemptCapability).Scan(&checksum); err != nil ||
		checksum != SourceCrawlAttemptCapabilityChecksum {
		missing = append(missing, "capability "+SourceCrawlAttemptCapability)
	}

	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: missing or invalid: %s", ErrSourceCrawlAttemptSchemaUnavailable, strings.Join(missing, ", "))
	}
	return nil
}

func sourceCrawlPostgresPrimaryKeyExists(ctx context.Context, db Handler, table string) bool {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1
			  FROM pg_constraint c
			  JOIN pg_class t ON t.oid = c.conrelid
			  JOIN pg_namespace n ON n.oid = t.relnamespace
			 WHERE n.nspname = current_schema()
			   AND lower(t.relname) = lower($1)
			   AND c.contype = 'p'
		)`, table).Scan(&exists)
	return err == nil && exists
}

func sourceCrawlPostgresForeignKeyMatches(ctx context.Context, db Handler, spec sourceCrawlForeignKeySpec) bool {
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
	if !strings.Contains(definition, "foreign key ("+childColumns+")") ||
		!strings.Contains(definition, strings.ToLower(spec.parent)+"("+parentColumns+")") {
		return false
	}
	if spec.onDelete != "" && !strings.Contains(definition, "on delete "+strings.ToLower(spec.onDelete)) {
		return false
	}
	return true
}

func sourceCrawlSQLiteForeignKeyExists(ctx context.Context, db Handler, spec sourceCrawlForeignKeySpec) bool {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	rows, err := db.QueryContext(ctx,
		`SELECT id, seq, "table", "from", COALESCE("to", ''), on_delete
		 FROM pragma_foreign_key_list(`+p(1)+`) ORDER BY id, seq`, spec.table)
	if err != nil {
		return false
	}
	defer rows.Close() //nolint:errcheck
	groups := map[int]*sourceCrawlSQLiteForeignKeyGroup{}
	for rows.Next() {
		var id, seq int
		var parent, column, parentColumn, onDelete string
		if err := rows.Scan(&id, &seq, &parent, &column, &parentColumn, &onDelete); err != nil {
			return false
		}
		group, ok := groups[id]
		if !ok {
			group = &sourceCrawlSQLiteForeignKeyGroup{parent: parent, onDelete: onDelete}
			groups[id] = group
		}
		group.columns = append(group.columns, column)
		group.parentColumns = append(group.parentColumns, parentColumn)
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, group := range groups {
		if !strings.EqualFold(group.parent, spec.parent) ||
			!equalFoldStrings(group.columns, spec.columns) ||
			!equalFoldStrings(group.parentColumns, spec.parentColumns) {
			continue
		}
		if spec.onDelete != "" && !strings.EqualFold(group.onDelete, spec.onDelete) {
			continue
		}
		return true
	}
	return false
}

type sourceCrawlSQLiteForeignKeyGroup struct {
	parent        string
	columns       []string
	parentColumns []string
	onDelete      string
}

func sourceCrawlPlaceholder(db Handler, position int) string {
	if normalizeDBMS(db.DBMS()) == "postgres" {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

// SourceCrawlAttemptSchemaPresent reports whether the optional attempt objects
// exist at all. It deliberately does not validate their structure. Callers use
// it to distinguish "the optional capability is not installed" (legacy is the
// documented behavior) from "the fenced claim store is present but unreadable
// or corrupt" (fail closed).
func SourceCrawlAttemptSchemaPresent(ctx context.Context, db *Handler) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, fmt.Errorf("database handler is nil")
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return false, nil
	}
	tables := []string{"SourceCrawlClaims", "SourceCrawlAttempts", "SourceCrawlClaimPolicy"}
	switch normalizeDBMS((*db).DBMS()) {
	case "postgres":
		var present bool
		const presentQuery = "SELECT to_regclass('sourcecrawlclaims') IS NOT NULL AND to_regclass('sourcecrawlattempts') IS NOT NULL AND to_regclass('sourcecrawlclaimpolicy') IS NOT NULL"
		if err := (*db).QueryRowContext(ctx, presentQuery).Scan(&present); err != nil {
			return false, err
		}
		return present, nil
	default:
		var count int
		if err := (*db).QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table'
			  AND name IN ('SourceCrawlClaims','SourceCrawlAttempts','SourceCrawlClaimPolicy')`).
			Scan(&count); err != nil {
			return false, err
		}
		return count == len(tables), nil
	}
}
