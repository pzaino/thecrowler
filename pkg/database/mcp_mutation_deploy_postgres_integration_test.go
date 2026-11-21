//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPostgresMCPMutationDeploymentApplyDisable proves the additive 1.17
// capability can be applied twice, verified, disabled without losing history,
// and re-applied on a disposable PostgreSQL database.
func TestPostgresMCPMutationDeploymentApplyDisable(t *testing.T) {
	target, err := mcpPostgresDisposableTarget()
	if errors.Is(err, errMCPPostgresDisposableOptInRequired) || errors.Is(err, errMCPPostgresDSNRequired) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("disposable PostgreSQL target: %v", err)
	}
	db, _, _ := newMCPPostgresDisposableDB(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var user string
	if err := db.QueryRowContext(ctx, `SELECT current_user`).Scan(&user); err != nil {
		t.Fatalf("read PostgreSQL current user: %v", err)
	}
	var handler Handler = &PostgresHandler{db: db, dbms: DBPostgresStr, connStr: target.String()}
	if err := ApplyMCPMutationMigration(ctx, handler, user); err != nil {
		t.Fatalf("first PostgreSQL deployment: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("first PostgreSQL capability: %v", err)
	}
	if err := CheckMCPMutationPrivileges(ctx, handler, user); err != nil {
		t.Fatalf("PostgreSQL privileges: %v", err)
	}
	now := time.Now().UTC()
	event := validMCPMutationAuditEvent(now)
	if err := InsertMCPMutationAudit(ctx, &handler, event); err != nil {
		t.Fatalf("insert PostgreSQL audit history: %v", err)
	}
	before, err := CountMCPMutationHistory(ctx, handler)
	if err != nil {
		t.Fatalf("count PostgreSQL history: %v", err)
	}
	if err := ApplyMCPMutationMigration(ctx, handler, user); err != nil {
		t.Fatalf("repeat PostgreSQL deployment: %v", err)
	}
	removed, err := DisableMCPMutationCapability(ctx, handler)
	if err != nil {
		t.Fatalf("disable PostgreSQL capability: %v", err)
	}
	if removed != 1 {
		t.Fatalf("disable removed %d markers, want 1", removed)
	}
	after, err := CountMCPMutationHistory(ctx, handler)
	if err != nil {
		t.Fatalf("count PostgreSQL history after disable: %v", err)
	}
	for table, count := range before {
		if after[table] != count {
			t.Fatalf("disable changed %s rows: before=%d after=%d", table, count, after[table])
		}
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("post-disable PostgreSQL capability error = %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("PostgreSQL audit history after disable count=%d err=%v", auditCount, err)
	}
	if err := ApplyMCPMutationMigration(ctx, handler, user); err != nil {
		t.Fatalf("re-apply PostgreSQL deployment: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("re-applied PostgreSQL capability: %v", err)
	}
}
