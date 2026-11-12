//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPostgresUpsertSourceWithPolicyPersistsUIDStatusAndOwnership(t *testing.T) {
	handler, sqlDB := openPostgresIntegrationTestDB(t)
	sourceURL := fmt.Sprintf("https://policy-integration-%d.example.test/", time.Now().UnixNano())
	source := &Source{URL: sourceURL, Name: "Policy Integration", Priority: "high", CategoryID: 4, UsrID: 7}
	config := cfg.SourceConfig{Version: "1.0", FormatVersion: "1.0", SourceName: "Policy Integration", CrawlingConfig: cfg.CrawlingConfig{Site: sourceURL, SourceType: "website"}}
	result, err := UpsertSourceWithPolicyContext(context.Background(), handler, source, config, SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true, Status: "pending", AuthoritativeOwnership: true})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM SourceOwnership WHERE source_id = $1`, result.SourceID)
		_, _ = sqlDB.Exec(`DELETE FROM Sources WHERE source_id = $1`, result.SourceID)
	})
	var uid, status, method string
	var configJSON []byte
	if err := sqlDB.QueryRow(`SELECT source_uid, status, config, (SELECT verification_method FROM SourceOwnership WHERE source_id = $1) FROM Sources WHERE source_id = $1`, result.SourceID).Scan(&uid, &status, &configJSON, &method); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if uid != CalculateSourceUID(source.Name, sourceURL) || len(uid) != 64 {
		t.Fatalf("source UID = %q", uid)
	}
	if status != "pending" || method != "information_seed_discovery" || len(configJSON) == 0 {
		t.Fatalf("unexpected source status/config/ownership: status=%q method=%q config=%s", status, method, configJSON)
	}
}
