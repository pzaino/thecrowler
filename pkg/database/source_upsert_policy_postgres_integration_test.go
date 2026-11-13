//go:build integration

package database

import (
	"context"
	"fmt"
	"strings"
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

// TestPostgresUpsertSourceWithPolicyRefreshesConfig ensures the PostgreSQL
// config-refresh predicate binds its JSON parameter with an explicit type.
// A bare "$1 IS NOT NULL" leaves the parameter type unknown to PostgreSQL and
// makes the whole refresh fail with SQLSTATE 42P08.
func TestPostgresUpsertSourceWithPolicyRefreshesConfig(t *testing.T) {
	handler, sqlDB := openPostgresIntegrationTestDB(t)
	sourceURL := fmt.Sprintf("https://policy-refresh-integration-%d.example.test/", time.Now().UnixNano())
	oldConfig := cfg.SourceConfig{Version: "1.0", FormatVersion: "1.0", SourceName: "Policy Refresh Old", CrawlingConfig: cfg.CrawlingConfig{Site: sourceURL, SourceType: "website"}}
	result, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: sourceURL, Name: "Policy Refresh Old", UsrID: 7}, oldConfig, SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true, AuthoritativeOwnership: true})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM SourceOwnership WHERE source_id = $1`, result.SourceID)
		_, _ = sqlDB.Exec(`DELETE FROM Sources WHERE source_id = $1`, result.SourceID)
	})

	newConfig := cfg.SourceConfig{Version: "1.0", FormatVersion: "1.0", SourceName: "Policy Refresh New", CrawlingConfig: cfg.CrawlingConfig{Site: sourceURL, SourceType: "website"}, MetaData: map[string]any{"refreshed": true}}
	reused, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: sourceURL, Name: "Policy Refresh New", UsrID: 7}, newConfig, SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true, UpdateExistingSourceConfig: true})
	if err != nil {
		t.Fatalf("refresh existing source config: %v", err)
	}
	if reused.Created || !reused.Existing || reused.SourceID != result.SourceID {
		t.Fatalf("unexpected refresh result: %#v", reused)
	}
	var configJSON []byte
	if err := sqlDB.QueryRow(`SELECT config FROM Sources WHERE source_id = $1`, result.SourceID).Scan(&configJSON); err != nil {
		t.Fatalf("read refreshed config: %v", err)
	}
	if !strings.Contains(string(configJSON), "Policy Refresh New") || !strings.Contains(string(configJSON), "refreshed") {
		t.Fatalf("config was not refreshed: %s", configJSON)
	}
}
