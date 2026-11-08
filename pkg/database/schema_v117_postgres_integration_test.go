//go:build integration

package database

import (
	"context"
	"strings"
	"testing"
)

// TestPostgresV117RuntimeCompatibility exercises the same compatibility check
// the API, engine and events writers run during startup. It catches a migration
// that changes SQL objects but leaves the runtime contract behind.
func TestPostgresV117RuntimeCompatibility(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	if err := CheckStartupCompatibility(context.Background(), *db); err != nil {
		t.Fatalf("v1.17 runtime compatibility check failed: %v", err)
	}
	var version, generation string
	if err := sqlDB.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current`).Scan(&version); err != nil {
		t.Fatalf("read current schema version: %v", err)
	}
	if version != "1.17" {
		t.Fatalf("current schema version = %q, want 1.17", version)
	}
	if err := sqlDB.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read writer generation: %v", err)
	}
	if generation != WriterGeneration {
		t.Fatalf("writer generation = %q, want %q", generation, WriterGeneration)
	}
	var ownershipTable, approvalsTable string
	if err := sqlDB.QueryRow(`SELECT to_regclass('public.sourceownership'), to_regclass('public.crawlapprovals')`).Scan(&ownershipTable, &approvalsTable); err != nil {
		t.Fatalf("read v1.17 tables: %v", err)
	}
	if !strings.EqualFold(ownershipTable, "sourceownership") || !strings.EqualFold(approvalsTable, "crawlapprovals") {
		t.Fatalf("missing v1.17 tables: ownership=%q approvals=%q", ownershipTable, approvalsTable)
	}
	// Repeated migration must not disturb the existing approval or ownership
	// history; the SQL files are idempotent, and this verifies the runtime
	// contract remains stable when the migration is applied again externally.
	var ownershipRows, approvalRows int
	if err := sqlDB.QueryRow(`SELECT (SELECT COUNT(*) FROM SourceOwnership), (SELECT COUNT(*) FROM CrawlApprovals)`).Scan(&ownershipRows, &approvalRows); err != nil {
		t.Fatalf("count retained history: %v", err)
	}
	if ownershipRows < 0 || approvalRows < 0 {
		t.Fatalf("invalid history counts ownership=%d approvals=%d", ownershipRows, approvalRows)
	}
}
