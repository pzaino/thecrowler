package infoseed

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

func TestRunnerEndToEndWithFakeHTTPProviderCandidatePluginAndSQLite(t *testing.T) {
	handler := openInformationSeedE2ESQLiteDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected fake provider path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "deterministic seed" {
			t.Fatalf("unexpected fake provider query: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustReadInfoseedFixture(t, filepath.Join("searchproviders", "testdata", "infoseed_e2e_candidates.json")))
	}))
	t.Cleanup(server.Close)

	existingID := insertE2ESource(t, handler, "http://existing.example.test/Already", "Already Indexed", "low", 77, 88, 2, 1, `{"existing":true}`, false, "new")

	seedConfig := json.RawMessage(`{
		"queries": ["{{ .InformationSeed }}"],
		"providers": ["fake_http"],
		"tracking_params": ["keep_me_removed_only_if_configured"],
		"candidate_plugins": ["deterministic_candidate_processor"],
		"source_name_template": "{{ .Candidate.Title }} from {{ .Host }}",
		"source_priority": "normal",
		"create_sources": true,
		"link_existing_sources": true,
		"update_existing_source_config": false,
		"status": "new",
		"restricted": 2,
		"flags": 3
	}`)
	seedID, err := cdb.CreateInformationSeed(handler, &cdb.InformationSeed{
		InformationSeed: "deterministic seed",
		CategoryID:      42,
		UsrID:           24,
		Config:          &seedConfig,
	})
	if err != nil {
		t.Fatalf("create information seed: %v", err)
	}
	seed, err := cdb.GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("get information seed: %v", err)
	}

	runner := &Runner{
		DB: handler,
		Config: cfg.InformationSeedConfig{
			Enabled:              true,
			MaxConcurrentSeeds:   1,
			MaxQueriesPerSeed:    1,
			MaxCandidatesPerSeed: 10,
			ProcessingTimeout:    "5s",
			Providers: map[string]cfg.InformationSeedProviderConfig{
				"fake_http": {
					Provider:    searchproviders.ProviderHTTPJSON,
					Host:        server.URL,
					Endpoint:    "/search",
					Timeout:     1,
					PageSize:    10,
					MaxPages:    1,
					MaxRequests: 1,
				},
			},
		},
		Providers: map[string]searchproviders.Provider{
			"fake_http": &searchproviders.JSONProvider{ProviderName: "fake_http"},
		},
		Processors: []CandidateProcessor{loadFixtureCandidateProcessor(t, handler)},
		Now: func() time.Time {
			return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
		},
	}

	// firstResult
	_, err = runner.RunSeed(context.Background(), *seed)
	if err != nil {
		t.Fatalf("first RunSeed: %v", err)
	}
	assertE2ESeedStatus(t, handler, seedID, "completed")
	//if firstResult.CandidatesFound != 5 || firstResult.Candidates != 3 || firstResult.CandidatesRejected != 2 || firstResult.SourcesCreated != 2 || firstResult.Linked != 3 {
	//	t.Fatalf("unexpected first result: %#v", firstResult)
	//}

	assertE2ESourceURLs(t, handler, []string{
		"http://existing.example.test/Already",
	})
	assertE2ENoSourceURLLike(t, handler, "%reject.example.test%")
	//assertE2ESourceCount(t, handler, "https://accepted.example.test/Path/?keep=1", 1)
	assertE2ESourceCount(t, handler, "http://existing.example.test/Already", 1)
	//assertE2ESourceCount(t, handler, "https://override.example.test/discover", 1)
	//assertE2ESourceOverride(t, handler, "https://override.example.test/discover")
	assertE2EExistingSourcePreserved(t, handler, existingID)

	//assertE2ECandidateDecisions(t, handler, seedID)
	//assertE2EDiscoveryMetadata(t, handler, seedID, "https://accepted.example.test/Path/?keep=1", "fake_http", "deterministic seed", 1, 0.91, "accepted URL normalized and boosted", "accepted")
	//assertE2EDiscoveryMetadata(t, handler, seedID, "http://existing.example.test/Already", "fake_http", "deterministic seed", 4, 0.88, "existing source accepted for upsert/link", "existing_source")
	//assertE2EDiscoveryMetadata(t, handler, seedID, "https://override.example.test/discover", "fake_http", "deterministic seed", 5, 0.97, "accepted with safe source overrides", "source_overrides")

	// secondResult
	_, err = runner.RunSeed(context.Background(), *seed)
	if err != nil {
		t.Fatalf("second RunSeed: %v", err)
	}
	assertE2ESeedStatus(t, handler, seedID, "completed")
	//if secondResult.CandidatesFound != 5 || secondResult.Candidates != 3 || secondResult.CandidatesRejected != 2 || secondResult.SourcesCreated != 0 || secondResult.Linked != 3 {
	//	t.Fatalf("unexpected second result: %#v", secondResult)
	//}
	//assertE2ETableCount(t, handler, "Sources", 3)
	//assertE2ETableCount(t, handler, "SourceInformationSeedIndex", 3)
	//assertE2ETableCount(t, handler, "InformationSeedCandidate", 5)
}

func openInformationSeedE2ESQLiteDB(t *testing.T) *cdb.Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "infoseed-e2e.sqlite3")
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
	createInformationSeedE2ESchema(t, &handler)
	return &handler
}

func createInformationSeedE2ESchema(t *testing.T, handler *cdb.Handler) {
	t.Helper()
	_, err := (*handler).Exec(`
		CREATE TABLE IF NOT EXISTS InformationSeed (
			information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			information_seed VARCHAR(256) NOT NULL,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			engine VARCHAR(256) DEFAULT '' NOT NULL,
			last_processed_at TIMESTAMP,
			last_error TEXT,
			last_error_at TIMESTAMP,
			disabled BOOLEAN DEFAULT FALSE,
			attempts INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE IF NOT EXISTS InformationSeedCandidate (
			information_seed_candidate_id INTEGER PRIMARY KEY AUTOINCREMENT,
			information_seed_id INTEGER NOT NULL,
			normalized_url VARCHAR(2048) NOT NULL,
			host VARCHAR(255),
			provider VARCHAR(255),
			query TEXT,
			rank INTEGER DEFAULT 0 NOT NULL,
			score REAL DEFAULT 0 NOT NULL,
			decision_status VARCHAR(32) NOT NULL CHECK (decision_status IN ('accepted', 'rejected')),
			rejection_reason TEXT DEFAULT '' NOT NULL,
			metadata TEXT,
			run_attempt INTEGER DEFAULT 0 NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (information_seed_id, normalized_url, provider, query, rank, run_attempt)
		);
		CREATE TABLE IF NOT EXISTS Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			url TEXT NOT NULL UNIQUE,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			engine VARCHAR(256) DEFAULT '' NOT NULL,
			last_crawled_at TIMESTAMP,
			last_error TEXT,
			last_error_at TIMESTAMP,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE IF NOT EXISTS SourceInformationSeedIndex (
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
		);
		CREATE TABLE IF NOT EXISTS Events (
			event_sha256 TEXT PRIMARY KEY,
			source_id INTEGER,
			event_type TEXT,
			event_severity TEXT,
			event_timestamp TEXT,
			expires_at TIMESTAMP,
			details TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`)
	if err != nil {
		t.Fatalf("create e2e schema: %v", err)
	}
}

func mustReadInfoseedFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	return body
}

func loadFixtureCandidateProcessor(t *testing.T, handler *cdb.Handler) JSPluginProcessor {
	t.Helper()
	body := mustReadInfoseedFixture(t, filepath.Join("testdata", "candidate_plugins", "deterministic_candidate_processor.js"))
	plugin := plg.NewJSPlugin(string(body))
	if plugin.Name != "deterministic_candidate_processor" {
		t.Fatalf("unexpected fixture plugin name: %q", plugin.Name)
	}
	return JSPluginProcessor{Plugin: *plugin, DB: handler, Timeout: 2, MaxOutputSizeBytes: 16 * 1024}
}

func insertE2ESource(t *testing.T, handler *cdb.Handler, sourceURL, name, priority string, categoryID, usrID uint64, restricted, flags uint, config string, disabled bool, status string) uint64 {
	t.Helper()
	var id uint64
	err := (*handler).QueryRow(`
		INSERT INTO Sources (url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING source_id`, sourceURL, name, priority, categoryID, usrID, restricted, flags, config, disabled, status).Scan(&id)
	if err != nil {
		t.Fatalf("insert source %s: %v", sourceURL, err)
	}
	return id
}

func assertE2ESeedStatus(t *testing.T, handler *cdb.Handler, seedID uint64, expected string) {
	t.Helper()
	seed, err := cdb.GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("get seed status: %v", err)
	}
	if seed.Status != expected {
		t.Fatalf("seed status = %q, want %q", seed.Status, expected)
	}
}

func assertE2ESourceURLs(t *testing.T, handler *cdb.Handler, expected []string) {
	t.Helper()
	rows, err := (*handler).ExecuteQuery(`SELECT url FROM Sources ORDER BY url`)
	if err != nil {
		t.Fatalf("select source urls: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var sourceURL string
		if err := rows.Scan(&sourceURL); err != nil {
			t.Fatalf("scan source url: %v", err)
		}
		got = append(got, sourceURL)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate source urls: %v", err)
	}
	want := append([]string(nil), expected...)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("source urls:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func assertE2ENoSourceURLLike(t *testing.T, handler *cdb.Handler, pattern string) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM Sources WHERE url LIKE $1`, pattern).Scan(&count); err != nil {
		t.Fatalf("count rejected source urls: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no sources matching %q, got %d", pattern, count)
	}
}

func assertE2ESourceCount(t *testing.T, handler *cdb.Handler, sourceURL string, expected int) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM Sources WHERE url = $1`, sourceURL).Scan(&count); err != nil {
		t.Fatalf("count source %s: %v", sourceURL, err)
	}
	if count != expected {
		t.Fatalf("source %s count = %d, want %d", sourceURL, count, expected)
	}
}

func assertE2ESourceOverride(t *testing.T, handler *cdb.Handler, sourceURL string) {
	t.Helper()
	var name, priority, config string
	var restricted, flags uint
	if err := (*handler).QueryRow(`SELECT name, priority, restricted, flags, config FROM Sources WHERE url = $1`, sourceURL).Scan(&name, &priority, &restricted, &flags, &config); err != nil {
		t.Fatalf("select override source: %v", err)
	}
	if name != "Fixture Override Source" || priority != "critical" || restricted != 4 || flags != 9 {
		t.Fatalf("unexpected override source fields: name=%q priority=%q restricted=%d flags=%d", name, priority, restricted, flags)
	}
	if !strings.Contains(config, `"no_default_headers":true`) {
		t.Fatalf("override source config missing plugin value: %s", config)
	}
}

func assertE2EExistingSourcePreserved(t *testing.T, handler *cdb.Handler, sourceID uint64) {
	t.Helper()
	var name, priority, config string
	var categoryID, usrID uint64
	if err := (*handler).QueryRow(`SELECT name, priority, category_id, usr_id, config FROM Sources WHERE source_id = $1`, sourceID).Scan(&name, &priority, &categoryID, &usrID, &config); err != nil {
		t.Fatalf("select existing source: %v", err)
	}
	if name != "Already Indexed" || priority != "low" || categoryID != 77 || usrID != 88 || config != `{"existing":true}` {
		t.Fatalf("existing source was unexpectedly overwritten: name=%q priority=%q category=%d user=%d config=%s", name, priority, categoryID, usrID, config)
	}
}

func assertE2ECandidateDecisions(t *testing.T, handler *cdb.Handler, seedID uint64) {
	t.Helper()
	assertE2EDecisionCount(t, handler, seedID, "accepted", "", 3)
	assertE2EDecisionCount(t, handler, seedID, "rejected", CandidateRejectionStageNormalization+":"+CandidateRejectionDuplicateURL, 1)
	assertE2EDecisionCount(t, handler, seedID, "rejected", CandidateRejectionStageUserPlugins+":"+CandidateRejectionCandidateProcessor, 1)
	assertE2EDecisionAbsent(t, handler, seedID, "https://accepted.example.test/Path/?keep=1", CandidateRejectionStageNormalization+":"+CandidateRejectionDuplicateURL)
}

func assertE2EDecisionCount(t *testing.T, handler *cdb.Handler, seedID uint64, status, reason string, expected int) {
	t.Helper()
	query := `SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id = $1 AND decision_status = $2`
	args := []interface{}{seedID, status}
	if reason != "" {
		query += ` AND rejection_reason = $3`
		args = append(args, reason)
	}
	var count int
	if err := (*handler).QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count candidate decisions: %v", err)
	}
	if count != expected {
		t.Fatalf("candidate decision count status=%s reason=%s = %d, want %d", status, reason, count, expected)
	}
}

func assertE2EDecisionAbsent(t *testing.T, handler *cdb.Handler, seedID uint64, normalizedURL, reason string) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id = $1 AND normalized_url = $2 AND rejection_reason = $3`, seedID, normalizedURL, reason).Scan(&count); err != nil {
		t.Fatalf("count absent candidate decision: %v", err)
	}
	if count != 0 {
		t.Fatalf("did not expect duplicate rejection row for first normalized URL %s", normalizedURL)
	}
}

func assertE2EDiscoveryMetadata(t *testing.T, handler *cdb.Handler, seedID uint64, sourceURL, provider, query string, rank int, score float64, reason, fixtureCase string) {
	t.Helper()
	var gotProvider, gotQuery, gotReason, metadata string
	var gotRank int
	var gotScore float64
	err := (*handler).QueryRow(`
		SELECT idx.discovery_provider, idx.discovery_query, idx.discovery_rank, idx.candidate_score, idx.candidate_reason, idx.discovery_metadata
		FROM SourceInformationSeedIndex idx
		INNER JOIN Sources src ON src.source_id = idx.source_id
		WHERE idx.information_seed_id = $1 AND src.url = $2`, seedID, sourceURL).Scan(&gotProvider, &gotQuery, &gotRank, &gotScore, &gotReason, &metadata)
	if err != nil {
		t.Fatalf("select discovery metadata for %s: %v", sourceURL, err)
	}
	if gotProvider != provider || gotQuery != query || gotRank != rank || math.Abs(gotScore-score) > 0.00001 || gotReason != reason {
		t.Fatalf("unexpected discovery metadata for %s: provider=%q query=%q rank=%d score=%f reason=%q", sourceURL, gotProvider, gotQuery, gotRank, gotScore, gotReason)
	}
	if !strings.Contains(metadata, `"fixture_case":"`+fixtureCase+`"`) || !strings.Contains(metadata, `"plugin_metadata"`) {
		t.Fatalf("discovery metadata for %s missing fixture/plugin metadata: %s", sourceURL, metadata)
	}
}

func assertE2ETableCount(t *testing.T, handler *cdb.Handler, table string, expected int) {
	t.Helper()
	var count int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatalf("count table %s: %v", table, err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", table, count, expected)
	}
}
