package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
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
func (sourceClaimTestHandler) CheckConnection(cfg.Config) error { return nil }
func (sourceClaimTestHandler) NewListener() cdb.Listener        { return nil }

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
	if got := sources[0].SubPriority; got != 37 {
		t.Fatalf("source SubPriority = %d, want 37; source = %#v", got, sources[0])
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
