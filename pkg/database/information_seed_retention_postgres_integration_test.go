//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPostgresInformationSeedRetentionDeletesEligibleRows(t *testing.T) {
	handler, sqlDB := openPostgresIntegrationTestDB(t)
	completed, err := CreateInformationSeed(handler, &InformationSeed{InformationSeed: fmt.Sprintf("retention-%d", time.Now().UnixNano()), Status: "completed"})
	if err != nil {
		t.Fatalf("create retained seed: %v", err)
	}
	pending, err := CreateInformationSeed(handler, &InformationSeed{InformationSeed: fmt.Sprintf("retention-pending-%d", time.Now().UnixNano()), Status: "pending"})
	if err != nil {
		t.Fatalf("create pending seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM InformationSeed WHERE information_seed_id IN ($1,$2)`, completed, pending)
	})
	old := time.Now().UTC().Add(-60 * 24 * time.Hour)
	if _, err := sqlDB.Exec(`ALTER TABLE InformationSeed DISABLE TRIGGER trg_update_information_seed_last_updated_before_update`); err != nil {
		t.Fatalf("disable last-updated trigger: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE InformationSeed SET last_updated_at = $1 WHERE information_seed_id IN ($2,$3)`, old, completed, pending); err != nil {
		t.Fatalf("age seeds: %v", err)
	}
	if _, err := sqlDB.Exec(`ALTER TABLE InformationSeed ENABLE TRIGGER trg_update_information_seed_last_updated_before_update`); err != nil {
		t.Fatalf("re-enable last-updated trigger: %v", err)
	}
	result, err := PurgeInformationSeedRetentionContext(context.Background(), handler, cfg.InformationSeedRetentionConfig{
		Enabled: true, CompletedDays: 30, DisabledDays: 30, ExhaustedDays: 30, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("retention purge: %v", err)
	}
	if result.Considered != 1 || result.Deleted != 1 {
		t.Fatalf("retention result = %#v, want one considered/deleted", result)
	}
	if _, err := GetInformationSeedByID(handler, completed); err == nil {
		t.Fatal("completed seed was not deleted")
	}
	if _, err := GetInformationSeedByID(handler, pending); err != nil {
		t.Fatalf("pending seed was incorrectly deleted: %v", err)
	}
}
