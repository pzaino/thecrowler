package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"golang.org/x/time/rate"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

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

func setupInformationSeedAPITestDB(t *testing.T) (cdb.Handler, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
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
			usr_id INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			url TEXT NOT NULL UNIQUE,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
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
			details TEXT
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
