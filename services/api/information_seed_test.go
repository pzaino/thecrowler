package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	infoseedrunner "github.com/pzaino/thecrowler/pkg/infoseed"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

func TestTyrellInformationSeedExampleMatchesCurrentContracts(t *testing.T) {
	examplePath := filepath.Join("..", "..", "examples", "tyrell-information-seed.json")
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	var request informationSeedAddRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode API request example: %v", err)
	}
	if request.InformationSeed != "Tyrell Corporation" || request.Priority != "normal" {
		t.Fatalf("unexpected API request example: %#v", request)
	}
	if request.Config == nil {
		t.Fatal("expected seed-level config in API request example")
	}

	var runConfig infoseedrunner.SeedRunConfig
	if err := json.Unmarshal(*request.Config, &runConfig); err != nil {
		t.Fatalf("decode runner config example: %v", err)
	}
	wantProviders := []string{"rss_public_news", "common_crawl_latest", "public_json_adapter"}
	if len(runConfig.Providers) != len(wantProviders) {
		t.Fatalf("providers = %#v, want %#v", runConfig.Providers, wantProviders)
	}
	for i := range wantProviders {
		if runConfig.Providers[i] != wantProviders[i] {
			t.Fatalf("providers = %#v, want %#v", runConfig.Providers, wantProviders)
		}
	}
	if len(runConfig.SourceConfig) == 0 {
		t.Fatal("expected a seed-level default source_config")
	}
	var defaultSourceConfig cfg.SourceConfig
	if err := json.Unmarshal(runConfig.SourceConfig, &defaultSourceConfig); err != nil {
		t.Fatalf("decode default source_config: %v", err)
	}
	if defaultSourceConfig.CrawlingConfig.Site != "https://www.tyrell.example/" {
		t.Fatalf("default source_config site = %q", defaultSourceConfig.CrawlingConfig.Site)
	}
	if len(runConfig.CandidatePlugins) != 1 || runConfig.CandidatePlugins[0] != "tyrell_source_config" {
		t.Fatalf("candidate_plugins = %#v", runConfig.CandidatePlugins)
	}

	pluginPath := filepath.Join("..", "..", "examples", "tyrell-information-seed-candidate-plugin.js")
	pluginRaw, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read %s: %v", pluginPath, err)
	}
	plugin := plg.NewJSPlugin(string(pluginRaw))
	if plugin.Name != "tyrell_source_config" || plugin.EventType != "information_seed_candidate" {
		t.Fatalf("unexpected candidate plugin metadata: name=%q event_type=%q", plugin.Name, plugin.EventType)
	}
	candidateURL := "https://replicants.tyrell.example/nexus-6"
	processed, keep, err := (infoseedrunner.JSPluginProcessor{Plugin: *plugin, Timeout: 2}).ProcessCandidate(context.Background(), infoseedrunner.CandidatePluginInput{
		Candidate:      infoseedrunner.Candidate{URL: candidateURL, Host: "replicants.tyrell.example", Title: "Nexus-6", Score: 0.8},
		SourceDefaults: infoseedrunner.SourceDefaults{Name: "Tyrell Corporation — Nexus-6", Priority: "normal", Restricted: 1, SourceConfig: runConfig.SourceConfig},
	})
	if err != nil {
		t.Fatalf("execute candidate source-config example: %v", err)
	}
	if !keep || len(processed.SourceOverrides.SourceConfig) == 0 {
		t.Fatalf("candidate plugin did not provide source_config: keep=%v overrides=%#v", keep, processed.SourceOverrides)
	}
	var candidateSourceConfig cfg.SourceConfig
	if err := json.Unmarshal(processed.SourceOverrides.SourceConfig, &candidateSourceConfig); err != nil {
		t.Fatalf("decode candidate source_config: %v", err)
	}
	if candidateSourceConfig.CrawlingConfig.Site != candidateURL {
		t.Fatalf("candidate source_config site = %q, want %q", candidateSourceConfig.CrawlingConfig.Site, candidateURL)
	}

	providersPath := filepath.Join("..", "..", "examples", "information-seed-providers.yaml")
	providersRaw, err := os.ReadFile(providersPath)
	if err != nil {
		t.Fatalf("read %s: %v", providersPath, err)
	}
	globalConfig, err := cfg.ParseConfig(providersRaw)
	if err != nil {
		t.Fatalf("parse provider example: %v", err)
	}
	allowed := make(map[string]struct{}, len(globalConfig.InformationSeed.ProviderAllowList))
	for _, name := range globalConfig.InformationSeed.ProviderAllowList {
		allowed[name] = struct{}{}
	}
	for _, name := range wantProviders {
		if _, ok := globalConfig.InformationSeed.Providers[name]; !ok {
			t.Errorf("seed example selects provider %q missing from provider example", name)
		}
		if _, ok := allowed[name]; !ok {
			t.Errorf("seed example selects provider %q missing from provider allow-list", name)
		}
	}
}

type informationSeedAPITestHandler struct{ db *sql.DB }

func (h *informationSeedAPITestHandler) Connect(cfg.Config) error { return nil }
func (h *informationSeedAPITestHandler) Close() error             { return h.db.Close() }
func (h *informationSeedAPITestHandler) Ping() error              { return h.db.Ping() }
func (h *informationSeedAPITestHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.Query(query, args...)
}
func (h *informationSeedAPITestHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return h.db.Exec(query, args...)
}
func (h *informationSeedAPITestHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return h.db.ExecContext(ctx, query, args...)
}
func (h *informationSeedAPITestHandler) DBMS() string            { return cdb.DBSQLiteStr }
func (h *informationSeedAPITestHandler) Begin() (*sql.Tx, error) { return h.db.Begin() }
func (h *informationSeedAPITestHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.db.BeginTx(ctx, opts)
}
func (h *informationSeedAPITestHandler) Commit(tx *sql.Tx) error   { return tx.Commit() }
func (h *informationSeedAPITestHandler) Rollback(tx *sql.Tx) error { return tx.Rollback() }
func (h *informationSeedAPITestHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.db.QueryRow(query, args...)
}
func (h *informationSeedAPITestHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.QueryContext(ctx, query, args...)
}
func (h *informationSeedAPITestHandler) CheckConnection(cfg.Config) error { return nil }
func (h *informationSeedAPITestHandler) NewListener() cdb.Listener        { return nil }

func TestInformationSeedAddHandlerValidRequest(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	body := []byte(`{"information_seed":"api add seed","category_id":4,"user_id":9,"priority":"high","config":{"providers":["public_json"],"max_candidates":3}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/information_seed/add", bytes.NewReader(body))
	informationSeedAddHandler(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	if resp.Item.ID == 0 || resp.Item.InformationSeed != "api add seed" || resp.Item.UsrID != 9 || resp.Item.Status != "new" || resp.Item.DiscoveredSourceCount != 0 {
		t.Fatalf("unexpected add response: %#v", resp)
	}
	if resp.Item.Config == nil || !json.Valid(*resp.Item.Config) {
		t.Fatalf("expected valid config in add response: %#v", resp.Item.Config)
	}
}

func TestInformationSeedAddHandlerInvalidRequests(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	tests := []struct {
		name string
		body string
	}{
		{name: "missing seed", body: `{"config":{}}`},
		{name: "non object config", body: `{"information_seed":"bad config","config":"not an object"}`},
		{name: "provider credential", body: `{"information_seed":"bad credential","config":{"providers":[{"provider":"brave","api_key":"secret"}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/information_seed/add", bytes.NewReader([]byte(tt.body)))
			informationSeedAddHandler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestInformationSeedStatusHandlerLookup(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "status api", "pending", "normal", 5, 6, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/status?information_seed_id="+itoa(seedID), nil)
	informationSeedStatusHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if resp.Item.ID != seedID || resp.Item.Status != "pending" || resp.Item.InformationSeed != "status api" {
		t.Fatalf("unexpected status response: %#v", resp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/information_seed/status?information_seed_id=9999", nil)
	informationSeedStatusHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing seed, got %d: %s", rec.Code, rec.Body.String())
	}
}

/*
func TestInformationSeedHyphenatedListAlias(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	handler, cleanup := setupInformationSeedAPITestDB(t)
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = true
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = false
	config.API.Plugins.Enabled = false
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)
	t.Cleanup(func() {
		cleanup()
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	seedID := createInformationSeedAPITestSeed(t, &handler, "alias api", "new", "", 0, 0, false)
	initAPIv1()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information-seed/list", nil)
	http.DefaultServeMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode alias response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != seedID {
		t.Fatalf("unexpected alias response: %#v", resp)
	}
}
*/

func TestInformationSeedListHandlerFiltersAndPagination(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedOne := createInformationSeedAPITestSeed(t, &handler, "first", "pending", "high", 2, 20, false)
	createInformationSeedAPITestSeed(t, &handler, "second", "pending", "high", 2, 20, true)
	createInformationSeedAPITestSeed(t, &handler, "third", "new", "low", 3, 30, false)
	sourceID := insertInformationSeedAPITestSource(t, handler.(*informationSeedAPITestHandler).db, "https://list-count.example", "list count")
	if err := cdb.LinkSourceToInformationSeed(&handler, sourceID, seedOne); err != nil {
		t.Fatalf("link list source: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/list?status=pending&priority=high&disabled=false&category=2&user=20&limit=1&offset=0", nil)
	informationSeedListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if resp.Limit != 1 || resp.Offset != 0 || len(resp.Items) != 1 || resp.Items[0].ID != seedOne {
		t.Fatalf("unexpected filtered page for seed %d: %#v", seedOne, resp)
	}
	if resp.Items[0].Status != "pending" || resp.Items[0].Priority != "high" || resp.Items[0].HasError || resp.Items[0].Attempts != 0 || resp.Items[0].DiscoveredSourceCount != 1 {
		t.Fatalf("expected lifecycle fields and discovered source count, got: %#v", resp.Items[0])
	}
}

func TestInformationSeedListHandlerBadFilters(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/list?disabled=maybe", nil)
	informationSeedListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad disabled filter, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInformationSeedSourcesHandlerSuccessNotFoundAndPagination(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "with sources", "pending", "high", 2, 20, false)
	sourceOne := insertInformationSeedAPITestSource(t, handler.(*informationSeedAPITestHandler).db, "https://api-one.example", "api one")
	sourceTwo := insertInformationSeedAPITestSource(t, handler.(*informationSeedAPITestHandler).db, "https://api-two.example", "api two")
	provider := "api-provider"
	rank := 4
	metadata := json.RawMessage(`{"api":true}`)
	if err := cdb.LinkSourceToInformationSeedWithDiscoveryMetadata(&handler, sourceOne, seedID, cdb.InformationSeedDiscoveryMetadata{DiscoveryProvider: &provider, DiscoveryRank: &rank, DiscoveryMetadata: &metadata}); err != nil {
		t.Fatalf("link source one: %v", err)
	}
	if err := cdb.LinkSourceToInformationSeed(&handler, sourceTwo, seedID); err != nil {
		t.Fatalf("link source two: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/sources?information_seed_id="+itoa(seedID)+"&limit=1&offset=0", nil)
	informationSeedSourcesHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedLinkedSourceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode linked sources response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].SourceID != sourceOne || resp.Items[0].SourceInformationSeedIndex.DiscoveryProvider != provider || resp.Items[0].SourceInformationSeedIndex.DiscoveryRank == nil || *resp.Items[0].SourceInformationSeedIndex.DiscoveryRank != rank {
		t.Fatalf("unexpected linked source page: %#v", resp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/information_seed/sources?information_seed_id=9999", nil)
	informationSeedSourcesHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing seed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInformationSeedCandidateDecisionsHandlerSuccess(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "candidate api", "pending", "high", 2, 20, false)
	if err := cdb.UpsertInformationSeedCandidateDecisions(&handler, []cdb.InformationSeedCandidate{{InformationSeedID: seedID, NormalizedURL: "https://candidate.example/", Host: "candidate.example", Provider: "unit", Query: "seed", Rank: 1, Score: 0.8, DecisionStatus: cdb.InformationSeedCandidateDecisionAccepted, RunAttempt: 1}}); err != nil {
		t.Fatalf("upsert candidate decision: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/candidates?information_seed_id="+itoa(seedID)+"&limit=1", nil)
	informationSeedCandidateDecisionsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedCandidateListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode candidate response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].NormalizedURL != "https://candidate.example/" || resp.Items[0].DecisionStatus != cdb.InformationSeedCandidateDecisionAccepted {
		t.Fatalf("unexpected candidate response: %#v", resp)
	}
}

func TestInformationSeedPathLifecycleHandlers(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "path lifecycle", "completed", "high", 2, 20, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/information_seed/"+itoa(seedID)+"/rerun", nil)
	informationSeedRerunHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected rerun 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rerun response: %v", err)
	}
	if resp.Item.Status != "pending" || resp.Item.Disabled {
		t.Fatalf("expected completed seed to move to pending without disabling, got %#v", resp.Item)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/information_seed/"+itoa(seedID)+"/disable", nil)
	informationSeedPathDisableHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected disable 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode disable response: %v", err)
	}
	if !resp.Item.Disabled || resp.Item.Status != "pending" {
		t.Fatalf("expected disabled flag without status change, got %#v", resp.Item)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/information_seed/"+itoa(seedID)+"/enable", bytes.NewReader([]byte(`{"queue_pending":true}`)))
	informationSeedEnableHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected enable 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode enable response: %v", err)
	}
	if resp.Item.Disabled || resp.Item.Status != "pending" {
		t.Fatalf("expected enabled pending seed, got %#v", resp.Item)
	}
}

func TestInformationSeedEventsHandlerPagination(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "events api", "completed", "", 0, 0, false)
	otherID := createInformationSeedAPITestSeed(t, &handler, "other events api", "completed", "", 0, 0, false)
	db := handler.(*informationSeedAPITestHandler).db
	if _, err := db.Exec(`INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, details) VALUES (?, ?, ?, ?, ?, ?)`, "evt-old", nil, "information_seed.discovery_started", cdb.EventSeverityInfo, "2026-01-01T00:00:00Z", `{"information_seed_id":`+itoa(seedID)+`,"run_attempt":1}`); err != nil {
		t.Fatalf("insert old event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, details) VALUES (?, ?, ?, ?, ?, ?)`, "evt-new", nil, "information_seed.discovery_completed", cdb.EventSeverityInfo, "2026-01-02T00:00:00Z", `{"information_seed_id":`+itoa(seedID)+`,"run_attempt":1}`); err != nil {
		t.Fatalf("insert new event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, details) VALUES (?, ?, ?, ?, ?, ?)`, "evt-other", nil, "information_seed.discovery_completed", cdb.EventSeverityInfo, "2026-01-03T00:00:00Z", `{"information_seed_id":`+itoa(otherID)+`,"run_attempt":1}`); err != nil {
		t.Fatalf("insert other event: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/"+itoa(seedID)+"/events?limit=1&offset=0", nil)
	informationSeedEventsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected events 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp InformationSeedEventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "evt-new" || resp.Items[0].Type != "information_seed.discovery_completed" {
		t.Fatalf("unexpected events page: %#v", resp)
	}
}

/*
func TestInformationSeedEndToEndTyrellProviderPluginAndAPIs(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	oldDBHandler := dbHandler
	oldDBSemaphore := dbSemaphore
	oldSysReady := getSysReady()

	handler, cleanup := setupInformationSeedAPITestDB(t)
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "Tyrell Corporation" {
			t.Fatalf("unexpected provider query: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"url":"HTTPS://www.tyrell.example:443/replicants?utm_source=fixture#frag","title":"Tyrell Replicants","score":0.62,"metadata":{"fixture_case":"default_source"}},
				{"url":"https://reject.tyrell.example/off-world?utm_source=fixture","title":"Rejected Tyrell Candidate","score":0.50,"metadata":{"fixture_case":"plugin_reject"}},
				{"url":"https://override.tyrell.example/discover?utm_source=fixture","title":"Override Tyrell Candidate","score":0.64,"metadata":{"fixture_case":"plugin_override"}}
			]
		}`))
	}))
	defer providerServer.Close()

	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = true
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = false
	config.API.Plugins.Enabled = false
	config.InformationSeed = cfg.InformationSeedConfig{
		Enabled:              true,
		QueryTimer:           60,
		MaxConcurrentSeeds:   1,
		MaxQueriesPerSeed:    1,
		MaxCandidatesPerSeed: 10,
		ProcessingTimeout:    "5s",
		RetryInterval:        1,
		Providers: map[string]cfg.InformationSeedProviderConfig{
			"tyrell_fixture": {
				Provider:    searchproviders.ProviderHTTPJSON,
				Host:        providerServer.URL,
				Endpoint:    "/search",
				Timeout:     1,
				PageSize:    10,
				MaxPages:    1,
				MaxRequests: 1,
			},
		},
	}
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)
	setSysReady(2)
	t.Cleanup(func() {
		cleanup()
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
		dbHandler = oldDBHandler
		dbSemaphore = oldDBSemaphore
		setSysReady(oldSysReady)
	})

	plugin := plg.NewJSPlugin(tyrellCandidateProcessorPlugin)
	if plugin.Name != "tyrell_candidate_processor" {
		t.Fatalf("unexpected plugin name: %q", plugin.Name)
	}
	runner := &infoseedrunner.Runner{
		DB:     &handler,
		Config: config.InformationSeed,
		Providers: map[string]searchproviders.Provider{
			"tyrell_fixture": &searchproviders.JSONProvider{ProviderName: "tyrell_fixture"},
		},
		Processors: []infoseedrunner.CandidateProcessor{
			infoseedrunner.JSPluginProcessor{Plugin: *plugin, DB: &handler, Timeout: 2, MaxOutputSizeBytes: 16 * 1024},
		},
		Now: func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) },
	}
	stopScheduler := infoseedrunner.StartScheduler(context.Background(), &handler, config.InformationSeed, runner, "tyrell-e2e")
	defer stopScheduler()

	initAPIv1()
	apiServer := httptest.NewServer(http.DefaultServeMux)
	defer apiServer.Close()

	addBody := `{
		"information_seed":"Tyrell Corporation",
		"category_id":42,
		"user_id":24,
		"priority":"high",
		"config":{
			"queries":["{{ .InformationSeed }}"],
			"providers":["tyrell_fixture"],
			"tracking_params":["utm_source"],
			"candidate_plugins":["tyrell_candidate_processor"],
			"source_name_template":"{{ .Seed }} - {{ .Candidate.Title }}",
			"source_priority":"normal",
			"create_sources":true,
			"link_existing_sources":true,
			"update_existing_source_config":false,
			"status":"new",
			"restricted":2,
			"flags":3,
			"source_config":{
				"version":"1.0",
				"format_version":"1.0",
				"source_name":"Tyrell Default Source",
				"crawling_config":{"site":"https://www.tyrell.example/replicants","source_type":"website"},
				"custom":{"default_marker":true}
			}
		}
	}`
	addResp := httpPostInformationSeedE2E(t, apiServer.URL+"/v1/information_seed/add", addBody)
	seedID := addResp.Item.ID
	if seedID == 0 || addResp.Item.InformationSeed != "Tyrell Corporation" {
		t.Fatalf("unexpected add response: %#v", addResp)
	}
	if addResp.Item.Status != "new" && addResp.Item.Status != "processing" && addResp.Item.Status != "completed" {
		t.Fatalf("unexpected add status: %q", addResp.Item.Status)
	}

	waitForInformationSeedStatusE2E(t, apiServer.URL, seedID, "completed")

	assertTyrellSourcesPersistedE2E(t, handler, seedID)
	assertTyrellCandidateEvidenceE2E(t, handler, seedID)
	assertTyrellProvenanceE2E(t, handler, seedID)
	assertTyrellStatusCandidateSourceAndEventAPIs(t, apiServer.URL, seedID)
}
*/

const tyrellCandidateProcessorPlugin = `
// name: tyrell_candidate_processor
// description: Tyrell information-seed candidate processor fixture.
// type: engine_plugin
// version: 1.0.0

var candidate = params.candidate || {};
var host = String(candidate.host || candidate.Host || "").toLowerCase();
var accepted = true;
var score = Number(candidate.score || candidate.Score || 0);
var reason = "accepted by Tyrell fixture";
var metadata = { fixture_plugin: "tyrell_candidate_processor", input_host: host };
var sourceOverrides = null;

if (host === "reject.tyrell.example") {
  accepted = false;
  score = 0.01;
  reason = "rejected by Tyrell fixture";
} else if (host === "www.tyrell.example") {
  score = 0.93;
  reason = "accepted with user source defaults";
} else if (host === "override.tyrell.example") {
  score = 0.98;
  reason = "accepted with safe source override";
  sourceOverrides = {
    name: "Tyrell Override Source",
    priority: "critical",
    restricted: 4,
    flags: 9,
    source_config: {
      version: "1.0",
      format_version: "1.0",
      source_name: "Tyrell Override Source",
      crawling_config: { site: "https://override.tyrell.example/discover", source_type: "website" },
      custom: { override_marker: true }
    }
  };
}

var result = { accepted: accepted, score: score, reason: reason, metadata: metadata, tags: ["tyrell-fixture"] };
if (sourceOverrides !== null) {
  result.source_overrides = sourceOverrides;
}
`

func httpPostInformationSeedE2E(t *testing.T, endpoint, body string) InformationSeedResponse {
	t.Helper()
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post information seed: %v", err)
	}
	defer resp.Body.Close()
	var decoded InformationSeedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected add status 201, got %d: %#v", resp.StatusCode, decoded)
	}
	return decoded
}

func waitForInformationSeedStatusE2E(t *testing.T, baseURL string, seedID uint64, expected string) InformationSeedResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last InformationSeedResponse
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/information_seed/status?information_seed_id=" + itoa(seedID))
		if err != nil {
			t.Fatalf("get information seed status: %v", err)
		}
		if err := json.NewDecoder(resp.Body).Decode(&last); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode status response: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && last.Item.Status == expected {
			return last
		}
		if last.Item.Status == "error" {
			t.Fatalf("seed entered error status: %#v", last.Item)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for seed %d status %q; last response: %#v", seedID, expected, last)
	return last
}

func assertTyrellSourcesPersistedE2E(t *testing.T, handler cdb.Handler, seedID uint64) {
	t.Helper()
	type sourceRow struct {
		URL, Name, Priority, Config string
		CategoryID, UsrID           uint64
		Restricted, Flags           uint
	}
	deadline := time.Now().Add(5 * time.Second)
	var got []sourceRow
	for {
		rows, err := handler.ExecuteQuery(`SELECT url, name, priority, category_id, usr_id, restricted, flags, config FROM Sources ORDER BY url`)
		if err != nil {
			t.Fatalf("select Tyrell sources: %v", err)
		}
		got = got[:0]
		for rows.Next() {
			var row sourceRow
			if err := rows.Scan(&row.URL, &row.Name, &row.Priority, &row.CategoryID, &row.UsrID, &row.Restricted, &row.Flags, &row.Config); err != nil {
				_ = rows.Close()
				t.Fatalf("scan Tyrell source: %v", err)
			}
			got = append(got, row)
		}
		_ = rows.Close()
		if len(got) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly two accepted sources, got %#v", got)
	}
	if got[0].URL != "https://override.tyrell.example/discover" || got[1].URL != "https://www.tyrell.example/replicants" {
		t.Fatalf("unexpected source URLs: %#v", got)
	}
	if got[0].Name != "Tyrell Override Source" || got[0].Priority != "critical" || got[0].Restricted != 4 || got[0].Flags != 9 || got[0].CategoryID != 42 || got[0].UsrID != 24 {
		t.Fatalf("safe plugin overrides were not honored: %#v", got[0])
	}
	if !strings.Contains(got[0].Config, `"override_marker":true`) || strings.Contains(got[0].Config, `"default_marker":true`) {
		t.Fatalf("override source config did not replace defaults safely: %s", got[0].Config)
	}
	if got[1].Name != "Tyrell Corporation - Tyrell Replicants" || got[1].Priority != "normal" || got[1].Restricted != 2 || got[1].Flags != 3 || got[1].CategoryID != 42 || got[1].UsrID != 24 {
		t.Fatalf("user source defaults were not honored: %#v", got[1])
	}
	if !strings.Contains(got[1].Config, `"default_marker":true`) || strings.Contains(got[1].Config, `"override_marker":true`) {
		t.Fatalf("default source config missing or polluted: %s", got[1].Config)
	}
	assertNoTyrellRejectedSourceE2E(t, handler)
}

func assertNoTyrellRejectedSourceE2E(t *testing.T, handler cdb.Handler) {
	t.Helper()
	var count int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM Sources WHERE url LIKE '%reject.tyrell.example%'`).Scan(&count); err != nil {
		t.Fatalf("count rejected Tyrell sources: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rejected URL to be absent from Sources, got %d rows", count)
	}
}

func assertTyrellCandidateEvidenceE2E(t *testing.T, handler cdb.Handler, seedID uint64) {
	t.Helper()
	counts := map[string]int{}
	rows, err := handler.ExecuteQuery(`SELECT normalized_url, decision_status, rejection_reason, metadata FROM InformationSeedCandidate WHERE information_seed_id = $1`, seedID)
	if err != nil {
		t.Fatalf("select Tyrell candidates: %v", err)
	}
	defer rows.Close()
	seenRejected := false
	for rows.Next() {
		var normalizedURL, status, reason string
		var metadata sql.NullString
		if err := rows.Scan(&normalizedURL, &status, &reason, &metadata); err != nil {
			t.Fatalf("scan Tyrell candidate: %v", err)
		}
		counts[status]++
		if strings.Contains(normalizedURL, "reject.tyrell.example") {
			seenRejected = true
			if status != cdb.InformationSeedCandidateDecisionRejected || reason != infoseedrunner.CandidateRejectionStageUserPlugins+":"+infoseedrunner.CandidateRejectionCandidateProcessor {
				t.Fatalf("unexpected rejected candidate evidence: url=%s status=%s reason=%s", normalizedURL, status, reason)
			}
			if !metadata.Valid || !strings.Contains(metadata.String, `"fixture_case":"plugin_reject"`) {
				t.Fatalf("rejected candidate metadata missing provider fixture case: %s", metadata.String)
			}
		}
	}
	if counts[cdb.InformationSeedCandidateDecisionAccepted] != 2 || counts[cdb.InformationSeedCandidateDecisionRejected] != 1 || !seenRejected {
		t.Fatalf("unexpected candidate evidence counts=%v seenRejected=%t", counts, seenRejected)
	}
	assertNoTyrellRejectedSourceE2E(t, handler)
}

func assertTyrellProvenanceE2E(t *testing.T, handler cdb.Handler, seedID uint64) {
	t.Helper()
	rows, err := handler.ExecuteQuery(`
		SELECT src.url, idx.discovery_provider, idx.discovery_query, idx.discovery_rank, idx.candidate_score, idx.candidate_reason, idx.discovery_metadata
		FROM SourceInformationSeedIndex idx
		JOIN Sources src ON src.source_id = idx.source_id
		WHERE idx.information_seed_id = $1
		ORDER BY src.url`, seedID)
	if err != nil {
		t.Fatalf("select Tyrell provenance: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var sourceURL, provider, query, reason, metadata string
		var rank int
		var score float64
		if err := rows.Scan(&sourceURL, &provider, &query, &rank, &score, &reason, &metadata); err != nil {
			t.Fatalf("scan Tyrell provenance: %v", err)
		}
		count++
		if provider != "tyrell_fixture" || query != "Tyrell Corporation" || rank == 0 || score <= 0 || reason == "" {
			t.Fatalf("unexpected provenance for %s: provider=%s query=%s rank=%d score=%f reason=%s", sourceURL, provider, query, rank, score, reason)
		}
		if !strings.Contains(metadata, `"fixture_plugin":"tyrell_candidate_processor"`) {
			t.Fatalf("provenance metadata missing plugin details for %s: %s", sourceURL, metadata)
		}
	}
	if count != 2 {
		t.Fatalf("expected provenance for two accepted sources, got %d", count)
	}
}

func assertTyrellStatusCandidateSourceAndEventAPIs(t *testing.T, baseURL string, seedID uint64) {
	t.Helper()
	status := httpGetJSONE2E[InformationSeedResponse](t, baseURL+"/v1/information_seed/status?information_seed_id="+itoa(seedID), http.StatusOK)
	if status.Item.Status != "completed" || status.Item.DiscoveredSourceCount != 2 {
		t.Fatalf("status API did not expose completed run/source count: %#v", status)
	}

	candidates := httpGetJSONE2E[InformationSeedCandidateListResponse](t, baseURL+"/v1/information_seed/candidates?information_seed_id="+itoa(seedID)+"&limit=10", http.StatusOK)
	if len(candidates.Items) != 3 {
		t.Fatalf("candidate API did not expose all candidate evidence: %#v", candidates)
	}
	seenRejectedCandidate := false
	for _, candidate := range candidates.Items {
		if strings.Contains(candidate.NormalizedURL, "reject.tyrell.example") && candidate.DecisionStatus == cdb.InformationSeedCandidateDecisionRejected {
			seenRejectedCandidate = true
		}
	}
	if !seenRejectedCandidate {
		t.Fatalf("candidate API did not expose rejected Tyrell candidate: %#v", candidates)
	}

	sources := httpGetJSONE2E[InformationSeedLinkedSourceListResponse](t, baseURL+"/v1/information_seed/sources?information_seed_id="+itoa(seedID)+"&limit=10", http.StatusOK)
	if len(sources.Items) != 2 {
		t.Fatalf("source API did not expose linked sources: %#v", sources)
	}
	for _, item := range sources.Items {
		if strings.Contains(item.URL, "reject.tyrell.example") {
			t.Fatalf("source API exposed rejected URL: %#v", item)
		}
		if item.SourceInformationSeedIndex.ID == 0 || item.SourceInformationSeedIndex.DiscoveryProvider != "tyrell_fixture" {
			t.Fatalf("source API missing provenance: %#v", item)
		}
	}

	events := httpGetJSONE2E[InformationSeedEventListResponse](t, baseURL+"/v1/information_seed/"+itoa(seedID)+"/events?limit=10", http.StatusOK)
	if len(events.Items) == 0 {
		t.Fatalf("event API did not expose run events: %#v", events)
	}
	seenCompleted := false
	for _, event := range events.Items {
		if event.Type == "information_seed.discovery_completed" {
			seenCompleted = true
		}
	}
	if !seenCompleted {
		t.Fatalf("event API missing discovery_completed event: %#v", events)
	}
}

func httpGetJSONE2E[T any](t *testing.T, endpoint string, expectedStatus int) T {
	t.Helper()
	resp, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("get %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	var decoded T
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode %s response: %v", endpoint, err)
	}
	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected %s status %d, got %d: %#v", endpoint, expectedStatus, resp.StatusCode, decoded)
	}
	return decoded
}

func setupInformationSeedAPITestDB(t *testing.T) (cdb.Handler, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`
		CREATE TABLE InformationSeed (
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
		CREATE TABLE InformationSeedCandidate (
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
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			url TEXT NOT NULL UNIQUE,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
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
		);
		CREATE TABLE Events (
			event_id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_sha256 VARCHAR(64) NOT NULL UNIQUE,
			source_id INTEGER,
			event_type VARCHAR(255) NOT NULL,
			event_severity VARCHAR(50) NOT NULL,
			event_timestamp TIMESTAMP NOT NULL,
			expires_at TIMESTAMP,
			details TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	handler := cdb.Handler(&informationSeedAPITestHandler{db: db})
	return handler, func() { _ = db.Close() }
}

func createInformationSeedAPITestSeed(t *testing.T, handler *cdb.Handler, text, status, priority string, categoryID, usrID uint64, disabled bool) uint64 {
	t.Helper()
	id, err := cdb.CreateInformationSeed(handler, &cdb.InformationSeed{InformationSeed: text, Status: status, Priority: priority, CategoryID: categoryID, UsrID: usrID, Disabled: disabled})
	if err != nil {
		t.Fatalf("create api test seed: %v", err)
	}
	return id
}

func insertInformationSeedAPITestSource(t *testing.T, db *sql.DB, sourceURL, name string) uint64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO Sources (url, name, priority, category_id, usr_id, restricted, flags, config, disabled) VALUES (?, ?, 'medium', 7, 11, 2, 3, '{}', FALSE)`, sourceURL, name)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	return uint64(id)
}

func itoa(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func TestInformationSeedDiagnosticsHandlerRedactsSecrets(t *testing.T) {
	handler, cleanup := setupInformationSeedAPITestDB(t)
	defer cleanup()
	dbHandler = handler
	dbSemaphore = make(chan struct{}, 1)

	seedID := createInformationSeedAPITestSeed(t, &handler, "diagnostics api", "completed", "", 0, 0, false)
	db := handler.(*informationSeedAPITestHandler).db
	details := `{
		"information_seed_id":` + itoa(seedID) + `,
		"run_id":"information-seed-` + itoa(seedID) + `-attempt-1",
		"run_attempt":1,
		"provider_metrics":{"brave":{"requests":2,"errors":1}},
		"candidate_rejection_stages":{"user_plugins":{"candidate_processor":1}},
		"provider_failures":[{"provider":"brave","summary":"url https://example.invalid/?api_key=SHOULD_NOT_LEAK token=SECRET_TOKEN"}],
		"plugin_failures":[{"plugin":"policy","summary":"password=SECRET_PASSWORD"}],
		"error_summaries":["client_secret=SHOULD_NOT_LEAK"]
	}`
	if _, err := db.Exec(`INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, details) VALUES (?, ?, ?, ?, ?, ?)`, "evt-diagnostics", nil, "information_seed.discovery_completed", cdb.EventSeverityWarning, "2026-01-02T00:00:00Z", details); err != nil {
		t.Fatalf("insert diagnostics event: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/information_seed/"+itoa(seedID)+"/diagnostics", nil)
	informationSeedDiagnosticsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "SHOULD_NOT_LEAK") || strings.Contains(body, "SECRET_TOKEN") || strings.Contains(body, "SECRET_PASSWORD") {
		t.Fatalf("diagnostics leaked secret: %s", body)
	}
	var resp InformationSeedDiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if resp.ProviderRequests["brave"] != 2 || resp.RejectionStages["user_plugins"]["candidate_processor"] != 1 {
		t.Fatalf("unexpected diagnostics aggregates: %#v", resp)
	}
	if len(resp.ProviderFailures) != 1 || len(resp.PluginFailures) != 1 || len(resp.ErrorSummaries) != 1 {
		t.Fatalf("expected redacted failures and errors: %#v", resp)
	}
}
