package infoseed

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

/*
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
*/

/*
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
*/

/*
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
*/

/*
func TestPersistCandidatesExistingConfigOverrideAndNoOverwritePolicy(t *testing.T) {
	handler := newPersistPolicyTestDB(t)
	existingID := insertPersistPolicySource(t, handler, "https://example.test/", "existing", "high", `{"version":"old"}`)
	runCfg := defaultSeedRunConfig()
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
*/

/*
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
*/

func TestParseSeedRunConfigPolicyDefaults(t *testing.T) {
	runCfg, err := parseSeedRunConfig(cdb.InformationSeed{})
	if err != nil {
		t.Fatalf("parse empty config: %v", err)
	}
	if !runCfg.CreateSources || !runCfg.LinkExistingSources || !runCfg.UpdateExistingSourceConfig || runCfg.Disabled || runCfg.Status != "new" {
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
	if err := handlerValue.Connect(dbConfig); err != nil {
		t.Fatalf("init sqlite schema: %v", err)
	}
	t.Cleanup(func() { _ = handlerValue.Close() })
	handler := cdb.Handler(handlerValue)
	return &handler
}

func persistPolicySeed() cdb.InformationSeed {
	return cdb.InformationSeed{ID: 1, InformationSeed: "seed", CategoryID: 7, UsrID: 11}
}

func insertPersistPolicySource(t *testing.T, handler *cdb.Handler, sourceURL, name, priority, config string) uint64 {
	t.Helper()
	result, err := (*handler).Exec(`
		INSERT INTO Sources (url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
		VALUES (?, ?, ?, 99, 98, 5, 6, ?, FALSE, 'new')`, sourceURL, name, priority, config)
	if err != nil {
		t.Fatalf("insert source %s: %v", sourceURL, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source insert id: %v", err)
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
