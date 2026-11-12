package infoseed

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

func TestPersistCandidatesExistingURLLinksByDefaultWithoutOverwritingFields(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://example.test/", "existing", "high", `{"version":"old"}`)

	stats := newSeedDiscoveryStats()
	linked, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), defaultSeedRunConfig(), []Candidate{{URL: "https://example.test/", Host: "example.test", Title: "discovered"}}, stats)
	if err != nil {
		t.Fatalf("persist candidate: %v", err)
	}
	if linked != 1 || stats.SourcesCreated != 0 || stats.SourcesLinked != 1 {
		t.Fatalf("expected one default existing-source link and no created source, linked=%d stats=%#v", linked, stats)
	}
	assertPersistPolicySource(t, handler, existingID, "existing", "high", `{"version":"old"}`)
	assertPersistPolicyLinks(t, handler, existingID, 1)
}

func TestPersistCandidatesCreateSourcesFalseOnlyLinksExistingSources(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://existing.test/", "existing", "medium", `{}`)
	runCfg := defaultSeedRunConfig()
	runCfg.CreateSources = false

	linked, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), runCfg, []Candidate{
		{URL: "https://existing.test/", Host: "existing.test", Title: "existing"},
		{URL: "https://new.test/", Host: "new.test", Title: "new"},
	}, nil)
	if err != nil {
		t.Fatalf("persist candidates: %v", err)
	}
	if linked != 1 {
		t.Fatalf("expected only existing source to link, got %d", linked)
	}
	assertPersistPolicySourceCount(t, handler, 1)
	assertPersistPolicyLinks(t, handler, existingID, 1)
}

func TestPersistCandidatesLinkExistingSourcesFalseSkipsExistingLinks(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://existing.test/", "existing", "medium", `{}`)
	runCfg := defaultSeedRunConfig()
	runCfg.LinkExistingSources = false

	linked, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), runCfg, []Candidate{
		{URL: "https://existing.test/", Host: "existing.test", Title: "existing"},
		{URL: "https://new.test/", Host: "new.test", Title: "new"},
	}, nil)
	if err != nil {
		t.Fatalf("persist candidates: %v", err)
	}
	if linked != 1 {
		t.Fatalf("expected only newly created source to link, got %d", linked)
	}
	assertPersistPolicyLinks(t, handler, existingID, 0)
	assertPersistPolicySourceCount(t, handler, 2)
}

func TestPersistCandidatesExistingConfigOverrideAndNoOverwritePolicy(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://example.test/", "existing", "high", `{"version":"old"}`)
	runCfg := defaultSeedRunConfig()
	runCfg.UpdateExistingSourceConfig = true
	runCfg.SourcePriority = "low"
	runCfg.SourceConfig = rawSourceConfig(t, "seed-config", "https://example.test/")

	if _, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), runCfg, []Candidate{{URL: "https://example.test/", Host: "example.test", Title: "candidate"}}, nil); err != nil {
		t.Fatalf("persist with config update: %v", err)
	}
	assertPersistPolicySource(t, handler, existingID, "existing", "high", string(runCfg.SourceConfig))

	runCfg.UpdateExistingSourceConfig = false
	runCfg.SourceConfig = rawSourceConfig(t, "blocked-config", "https://example.test/")
	if _, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), runCfg, []Candidate{{URL: "https://example.test/", Host: "example.test", Title: "candidate"}}, nil); err != nil {
		t.Fatalf("persist with config no-overwrite: %v", err)
	}
	assertPersistPolicySource(t, handler, existingID, "existing", "high", rawSourceConfigString(t, "seed-config", "https://example.test/"))
}

func TestPersistCandidatesLinkIdempotency(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://example.test/", "existing", "medium", `{}`)
	candidate := Candidate{URL: "https://example.test/", Host: "example.test", Title: "candidate", Provider: "provider-a", Query: "query-a", Rank: 1, Score: 0.5}
	runner := &Runner{DB: handler}
	if _, err := runner.persistCandidates(context.Background(), persistPolicySeed(), defaultSeedRunConfig(), []Candidate{candidate}, nil); err != nil {
		t.Fatalf("first persist: %v", err)
	}
	if _, err := runner.persistCandidates(context.Background(), persistPolicySeed(), defaultSeedRunConfig(), []Candidate{candidate}, nil); err != nil {
		t.Fatalf("second persist: %v", err)
	}
	assertPersistPolicyLinks(t, handler, existingID, 1)
}

func TestParseSeedRunConfigPolicyDefaults(t *testing.T) {
	runCfg, err := parseSeedRunConfig(cdb.InformationSeed{})
	if err != nil {
		t.Fatalf("parse empty config: %v", err)
	}
	if !runCfg.CreateSources || !runCfg.LinkExistingSources || runCfg.UpdateExistingSourceConfig || runCfg.Disabled || runCfg.Status != "new" {
		t.Fatalf("unexpected policy defaults: %#v", runCfg)
	}

	raw := json.RawMessage(`{"create_sources":false,"link_existing_sources":false,"update_existing_source_config":false,"disabled":true,"status":"paused"}`)
	runCfg, err = parseSeedRunConfig(cdb.InformationSeed{Config: &raw})
	if err != nil {
		t.Fatalf("parse explicit config: %v", err)
	}
	if runCfg.CreateSources || runCfg.LinkExistingSources || runCfg.UpdateExistingSourceConfig || !runCfg.Disabled || runCfg.Status != "paused" {
		t.Fatalf("explicit policy values were not honored: %#v", runCfg)
	}
}

func newPersistPolicyTestDB(t *testing.T) *cdb.Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "infoseed-policy.sqlite3")
	dbConfig := cfg.Config{Database: cfg.Database{Type: cdb.DBSQLiteStr, DBName: dbPath}}
	handlerValue, err := cdb.NewHandler(dbConfig)
	if err != nil {
		t.Fatalf("create sqlite handler: %v", err)
	}
	if err := handlerValue.Connect(dbConfig); err != nil {
		t.Fatalf("connect sqlite handler: %v", err)
	}
	t.Cleanup(func() { _ = handlerValue.Close() })
	handler := cdb.Handler(handlerValue)
	if _, err := handler.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_uid VARCHAR(64) NOT NULL,
			url TEXT NOT NULL UNIQUE,
			name VARCHAR(255),
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			sub_priority INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT,
			disabled BOOLEAN DEFAULT FALSE,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP
		);
		CREATE TABLE SourceOwnership (
			source_id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			verified_at TIMESTAMP NOT NULL,
			verified_by_user_id INTEGER NOT NULL,
			verified_by TEXT NOT NULL,
			verification_method TEXT NOT NULL,
			verification_reason TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
		);
		CREATE TABLE SourceInformationSeedIndex (
			source_information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			information_seed_id INTEGER NOT NULL,
			discovery_provider VARCHAR(255),
			discovery_query TEXT,
			discovery_rank INTEGER,
			candidate_score REAL,
			candidate_reason TEXT,
			discovery_metadata TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (source_id, information_seed_id)
		);`); err != nil {
		t.Fatalf("create policy test schema: %v", err)
	}
	return &handler
}

func persistPolicySeed() cdb.InformationSeed {
	return cdb.InformationSeed{ID: 1, InformationSeed: "seed", CategoryID: 7, UsrID: 11}
}

func insertPersistPolicySource(t *testing.T, handler *cdb.Handler, sourceURL, name, priority, config string) uint64 {
	t.Helper()
	result, err := (*handler).Exec(`
		INSERT INTO Sources (source_uid, url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
		VALUES (?, ?, ?, ?, 99, 11, 5, 6, ?, FALSE, 'new')`, cdb.CalculateSourceUID(name, sourceURL), sourceURL, name, priority, config)
	if err != nil {
		t.Fatalf("insert source %s: %v", sourceURL, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source insert id: %v", err)
	}
	if _, err := (*handler).Exec(`INSERT INTO SourceOwnership (source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method)
		VALUES (?, 11, CURRENT_TIMESTAMP, 11, 'test', 'admin_verification')`, id); err != nil {
		t.Fatalf("insert source ownership: %v", err)
	}
	return uint64(id)
}

func assertPersistPolicySource(t *testing.T, handler *cdb.Handler, id uint64, expectedName, expectedPriority, expectedConfig string) {
	t.Helper()
	var name, priority, config string
	if err := (*handler).QueryRow(`SELECT name, priority, config FROM Sources WHERE source_id = ?`, id).Scan(&name, &priority, &config); err != nil {
		t.Fatalf("select source %d: %v", id, err)
	}
	if name != expectedName || priority != expectedPriority {
		t.Fatalf("unexpected source fields: name=%q priority=%q", name, priority)
	}
	assertJSONEqual(t, config, expectedConfig)
}

func assertPersistPolicySourceCount(t *testing.T, handler *cdb.Handler, expected int) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM Sources`).Scan(&count); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d sources, got %d", expected, count)
	}
}

func assertPersistPolicyLinks(t *testing.T, handler *cdb.Handler, sourceID uint64, expected int) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE source_id = ? AND information_seed_id = 1`, sourceID).Scan(&count); err != nil {
		t.Fatalf("count source links: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d source links for source %d, got %d", expected, sourceID, count)
	}
}

func rawSourceConfig(t *testing.T, sourceName, site string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(validPersistPolicySourceConfig(sourceName, site))
	if err != nil {
		t.Fatalf("marshal source config: %v", err)
	}
	return json.RawMessage(data)
}

func rawSourceConfigString(t *testing.T, sourceName, site string) string {
	t.Helper()
	return string(rawSourceConfig(t, sourceName, site))
}

func validPersistPolicySourceConfig(sourceName, site string) cfg.SourceConfig {
	return cfg.SourceConfig{
		Version:       "1.0",
		FormatVersion: "1.0",
		SourceName:    sourceName,
		CrawlingConfig: cfg.CrawlingConfig{
			Site:       site,
			SourceType: "website",
		},
		ExecutionPlan: []cfg.ExecutionPlanItem{{
			Label:      "crawl",
			Conditions: cfg.Condition{URLPatterns: []string{".*"}},
		}},
	}
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue interface{}
	var wantValue interface{}
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("invalid got JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("invalid want JSON %q: %v", want, err)
	}
	if !jsonValuesEqual(gotValue, wantValue) {
		t.Fatalf("unexpected JSON config: got %s want %s", got, want)
	}
}

func jsonValuesEqual(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}

func TestInformationSeedRetryDelayIsBoundedExponential(t *testing.T) {
	base := time.Minute
	max := 10 * time.Minute
	for _, test := range []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 1, want: time.Minute},
		{attempts: 2, want: 2 * time.Minute},
		{attempts: 3, want: 4 * time.Minute},
		{attempts: 4, want: 8 * time.Minute},
		{attempts: 5, want: max},
		{attempts: 20, want: max},
	} {
		if got := informationSeedRetryDelay(test.attempts, base, max); got != test.want {
			t.Fatalf("retry delay attempts=%d = %v, want %v", test.attempts, got, test.want)
		}
	}
}

func TestPersistCandidatesRejectsCrossOwnerSourceLink(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://other-owner.test/", "other", "high", `{}`)
	if _, err := (*handler).Exec(`UPDATE Sources SET usr_id = 8 WHERE source_id = ?`, existingID); err != nil {
		t.Fatalf("change source owner: %v", err)
	}
	_, err := (&Runner{DB: handler}).persistCandidates(context.Background(), persistPolicySeed(), defaultSeedRunConfig(), []Candidate{{URL: "https://other-owner.test/", Host: "other-owner.test", Title: "other"}}, nil)
	if !errors.Is(err, cdb.ErrSourceMutationForbidden) {
		t.Fatalf("cross-owner persistence error = %v, want forbidden", err)
	}
	assertPersistPolicyLinks(t, handler, existingID, 0)
}
