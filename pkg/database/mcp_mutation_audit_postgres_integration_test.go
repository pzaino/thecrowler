//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestPostgresMCPMutationAuditAndRetention exercises the durable denied
// admission writer and the bounded retention query against a database created
// by this test harness. It never touches the caller-selected database.
func TestPostgresMCPMutationAuditAndRetention(t *testing.T) {
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
	applyMCPMutationPostgresMigration(t, db, user)
	var handler Handler = &PostgresHandler{db: db, dbms: DBPostgresStr, connStr: target.String()}

	now := time.Now().UTC().Truncate(time.Microsecond)
	event := validMCPMutationAuditEvent(now)
	event.ProposalID = mcpMutationTestHash("proposal:postgres-audit")
	event.ProposalSHA256 = mcpMutationTestHash("fingerprint:postgres-audit")
	if err := InsertMCPMutationAudit(ctx, &handler, event); err != nil {
		t.Fatalf("PostgreSQL denied admission audit: %v", err)
	}
	var reason, detail string
	var principal int64
	if err := db.QueryRowContext(ctx, `SELECT reason_code, principal_user_id,
		redacted_detail::text FROM MCPMutationAudit`).Scan(&reason, &principal, &detail); err != nil {
		t.Fatalf("read PostgreSQL audit: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatalf("decode PostgreSQL audit detail: %v", err)
	}
	if reason != "invalid_arguments" || principal != 7 ||
		decoded["stage"] != "proposal" || decoded["call_id"] != "call-1" ||
		decoded["policy_id"] != "policy-1" {
		t.Fatalf("unexpected PostgreSQL audit row: reason=%s principal=%d detail=%s", reason, principal, detail)
	}

	old := event
	old.OccurredAt = now.Add(-100 * 24 * time.Hour)
	old.ReasonCode = "admission_denied"
	if err := InsertMCPMutationAudit(ctx, &handler, old); err != nil {
		t.Fatalf("PostgreSQL old audit: %v", err)
	}
	unknown := old
	unknown.EventType = "mcp_mutation_future_event"
	if err := InsertMCPMutationAudit(ctx, &handler, unknown); err != nil {
		t.Fatalf("PostgreSQL unknown audit: %v", err)
	}
	removed, err := CleanupMCPMutationAudit(ctx, &handler, now.Add(-90*24*time.Hour), 10)
	if err != nil {
		t.Fatalf("PostgreSQL audit cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("PostgreSQL audit cleanup removed %d rows, want 1", removed)
	}
	var unknownCount, recentCount int
	if err := db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN event_type='mcp_mutation_future_event' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event_type=$1 THEN 1 ELSE 0 END),0)
		FROM MCPMutationAudit`, event.EventType).Scan(&unknownCount, &recentCount); err != nil {
		t.Fatalf("count PostgreSQL audit rows: %v", err)
	}
	if unknownCount != 1 || recentCount != 1 {
		t.Fatalf("PostgreSQL audit rows unknown=%d recent=%d", unknownCount, recentCount)
	}
}
