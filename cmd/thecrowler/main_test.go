package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/time/rate"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

type sourceClaimTestHandler struct {
	db *sql.DB
}

func (h sourceClaimTestHandler) Connect(cfg.Config) error { return nil }
func (h sourceClaimTestHandler) Close() error             { return h.db.Close() }
func (h sourceClaimTestHandler) Ping() error              { return nil }
func (h sourceClaimTestHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.Query(query, args...)
}
func (h sourceClaimTestHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return h.db.Exec(query, args...)
}
func (h sourceClaimTestHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return h.db.ExecContext(ctx, query, args...)
}
func (sourceClaimTestHandler) DBMS() string              { return cdb.DBPostgresStr }
func (h sourceClaimTestHandler) Begin() (*sql.Tx, error) { return h.db.Begin() }
func (h sourceClaimTestHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.db.BeginTx(ctx, opts)
}
func (sourceClaimTestHandler) Commit(tx *sql.Tx) error   { return tx.Commit() }
func (sourceClaimTestHandler) Rollback(tx *sql.Tx) error { return tx.Rollback() }
func (h sourceClaimTestHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.db.QueryRow(query, args...)
}
func (h sourceClaimTestHandler) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return h.db.QueryRowContext(ctx, query, args...)
}
func (h sourceClaimTestHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.QueryContext(ctx, query, args...)
}
func (sourceClaimTestHandler) CheckConnection(cfg.Config) error                  { return nil }
func (sourceClaimTestHandler) WaitForConnection(cfg.Config, time.Duration) error { return nil }
func (sourceClaimTestHandler) NewListener() cdb.Listener                         { return nil }

func TestRetrieveAvailableSourcesPreservesClaimSubPriority(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const claimQuery = `
	SELECT
		l.source_id,
		l.source_uid,
		l.url,
		l.restricted,
		l.flags,
		l.config,
		l.sub_priority
	FROM
		update_sources($1,$2,$3,$4,$5,$6,$7) AS l
	ORDER BY l.sub_priority DESC, l.source_id ASC;`
	claimColumns := []string{
		"source_id", "source_uid", "url", "restricted", "flags", "config", "sub_priority",
	}

	mock.ExpectQuery("SELECT to_regclass('sourcecrawlclaims') IS NOT NULL AND to_regclass('sourcecrawlattempts') IS NOT NULL AND to_regclass('sourcecrawlclaimpolicy') IS NOT NULL").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(true))
	mock.ExpectQuery(`SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"mode"}).AddRow("legacy"))
	mock.ExpectBegin()
	mock.ExpectQuery(claimQuery).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(claimColumns).AddRow(
			uint64(19), "claim-uid", "https://claim.example", uint(1), uint(8), []byte(`{"source_name":"claim"}`), 37,
		))
	mock.ExpectCommit()

	sources, err := retrieveAvailableSources(sourceClaimTestHandler{db: db}, 1)
	if err != nil {
		t.Fatalf("retrieveAvailableSources() error = %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("retrieveAvailableSources() returned %d sources, want 1", len(sources))
	}
	if got := sources[0].Source.SubPriority; got != 37 {
		t.Fatalf("source SubPriority = %d, want 37; source = %#v", got, sources[0].Source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations were not met: %v", err)
	}
}

func TestInitAPIv1RegistersControlRoutes(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
	})

	initAPIv1()

	registeredRoutes := []string{
		"/v1/health",
		"/v1/health/",
		"/v1/ready",
		"/v1/ready/",
		"/v1/config",
	}
	for _, route := range registeredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != route {
				t.Fatalf("registered pattern for %q = %q, want %q", route, pattern, route)
			}
		})
	}

	unregisteredRoutes := []string{
		"/v1/search/general",
		"/v1/information_seed/list",
		"/v1/source/add",
	}
	for _, route := range unregisteredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != "" {
				t.Fatalf("unexpected registered pattern for %q = %q", route, pattern)
			}
		})
	}
}

func TestPipelineStatusJSONIncludesEmailRunSummary(t *testing.T) {
	want := mail.RunSummary{
		Counts:      mail.RunCounts{Mailboxes: 1, Completed: 3, Warnings: 2},
		Checkpoints: mail.CheckpointOutcomes{Committed: 1, Advanced: 1},
		Timing:      mail.RunTiming{Duration: 2 * time.Second},
	}
	statuses := []crowler.Status{{EmailSummary: &want, StartTime: time.Now()}}
	statuses[0].PipelineRunning.Store(1)

	reports := pipelineStatusJSON(&statuses)
	if len(reports) != 1 || reports[0].EmailSummary == nil || *reports[0].EmailSummary != want {
		t.Fatalf("pipeline status email summary = %#v, want %#v", reports, want)
	}
}

func TestParseSourceCrawlConfigDuration(t *testing.T) {
	cases := []struct {
		value    string
		fallback time.Duration
		want     time.Duration
	}{
		{"", 15 * time.Minute, 15 * time.Minute},
		{"15 minutes", 0, 15 * time.Minute},
		{"2 hours", 0, 2 * time.Hour},
		{"1 day", 0, 24 * time.Hour},
		{"30s", 0, 30 * time.Second},
		{"not-a-duration", 5 * time.Minute, 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := parseSourceCrawlConfigDuration(tc.value, tc.fallback); got != tc.want {
			t.Errorf("parseSourceCrawlConfigDuration(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestNewSourceCrawlClaimRequestID(t *testing.T) {
	first, err := newSourceCrawlClaimRequestID()
	if err != nil {
		t.Fatalf("newSourceCrawlClaimRequestID() error = %v", err)
	}
	second, err := newSourceCrawlClaimRequestID()
	if err != nil {
		t.Fatalf("newSourceCrawlClaimRequestID() second error = %v", err)
	}
	if len(first) != 64 || len(second) != 64 {
		t.Fatalf("claim request ID lengths = %d/%d, want 64", len(first), len(second))
	}
	if first == second {
		t.Fatal("claim request IDs are not unique")
	}
}

type fakeDrainCoordinator struct {
	reported chan cdb.SourceCrawlTerminalOutcome
}

func (f *fakeDrainCoordinator) SourceID() uint64          { return 42 }
func (f *fakeDrainCoordinator) AttemptID() string         { return "attempt" }
func (f *fakeDrainCoordinator) Generation() uint64        { return 1 }
func (f *fakeDrainCoordinator) ClaimedAt() time.Time      { return time.Now() }
func (f *fakeDrainCoordinator) LeaseExpiresAt() time.Time { return time.Now().Add(time.Hour) }
func (f *fakeDrainCoordinator) Bound() bool               { return false }
func (f *fakeDrainCoordinator) RenewLease(context.Context, *cdb.Handler, time.Duration) (cdb.SourceCrawlLeaseResult, error) {
	return cdb.SourceCrawlLeaseResult{Result: cdb.SourceCrawlResultApplied}, nil
}
func (f *fakeDrainCoordinator) ReportTerminal(_ context.Context, _ *cdb.Handler,
	outcome cdb.SourceCrawlTerminalOutcome, _ time.Time) (cdb.SourceCrawlTerminalResult, error) {
	if f.reported != nil {
		f.reported <- outcome
	}
	return cdb.SourceCrawlTerminalResult{Result: cdb.SourceCrawlResultApplied}, nil
}

func (f *fakeDrainCoordinator) ValidateCurrent(_ context.Context,
	_ *cdb.Handler) (cdb.SourceCrawlFenceStatus, error) {
	return cdb.SourceCrawlFenceStatus{Result: cdb.SourceCrawlFenceActive}, nil
}

func (f *fakeDrainCoordinator) ValidateTx(_ context.Context, _ *sql.Tx,
	_ *cdb.Handler) error {
	return nil
}

func TestDrainFencedSourcesReportsCancelled(t *testing.T) {
	reported := make(chan cdb.SourceCrawlTerminalOutcome, 1)
	sourceChan := make(chan claimedSource, 2)
	sourceChan <- claimedSource{Handoff: &fakeDrainCoordinator{reported: reported}}
	close(sourceChan)
	var db cdb.Handler
	done := make(chan struct{})
	go func() {
		defer close(done)
		drainFencedSources(&db, sourceChan)
	}()
	select {
	case outcome := <-reported:
		if outcome != cdb.SourceCrawlOutcomeCancelled {
			t.Fatalf("drained outcome = %s, want cancelled", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drainFencedSources did not report the queued source")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drainFencedSources did not finish")
	}
}

func TestRetrieveAvailableSourcesFencedSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fenced.db")
	c := cfg.Config{Database: cfg.Database{
		Type: "sqlite", DBName: path, TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(c)
	if err != nil {
		t.Fatalf("new SQLite handler: %v", err)
	}
	if err := handler.Connect(c); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	setup, err := os.ReadFile("../../pkg/database/sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	if _, err := handler.Exec(string(setup)); err != nil {
		t.Fatalf("apply SQLite setup: %v", err)
	}
	if _, err := handler.Exec(`UPDATE SourceCrawlClaimPolicy SET mode='fenced' WHERE policy_id=1`); err != nil {
		t.Fatalf("enable fenced mode: %v", err)
	}
	if _, err := handler.Exec(`INSERT INTO Sources
		(source_id, source_uid, url, name, status, disabled, usr_id, priority, sub_priority)
		VALUES (42, 'fenced-uid', 'https://fenced.example/', 'fenced', 'new', FALSE, 7, 'normal', 0)`); err != nil {
		t.Fatalf("insert fenced source: %v", err)
	}
	// Unbound source-status mutation requires durable owner-continuity proof,
	// so the verified provenance row must predate the claim.
	if _, err := handler.Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (42, 7, CURRENT_TIMESTAMP, 1, 'operator', 'fixture', 'fixture')`); err != nil {
		t.Fatalf("insert fenced ownership: %v", err)
	}
	oldConfig := config
	config = c
	config.Crawler.MaxSources = 1
	config.Crawler.SourcePriority = "normal"
	t.Cleanup(func() { config = oldConfig })

	sources, err := retrieveAvailableSources(handler, 1)
	if err != nil {
		t.Fatalf("retrieveAvailableSources() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Handoff == nil || sources[0].Source.ID != 42 {
		t.Fatalf("fenced sources = %#v", sources)
	}
	if sources[0].Handoff.Bound() {
		t.Fatal("ordinary fenced source unexpectedly bound to an operation")
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	result, err := sources[0].Handoff.ReportTerminal(context.Background(), &handler,
		cdb.SourceCrawlOutcomeCompleted, observed)
	if err != nil || result.Result != cdb.SourceCrawlResultApplied ||
		!result.SourceStatusChanged {
		t.Fatalf("fenced terminal = %+v err=%v", result, err)
	}
	var state string
	if err := handler.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&state); err != nil {
		t.Fatalf("read fenced source status: %v", err)
	}
	if state != "completed" {
		t.Fatalf("fenced source status = %s, want completed", state)
	}
}
