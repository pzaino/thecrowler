package agent

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// sqlmockHandler delegates query methods to a sqlmock database so adapters
// run against recorded expectations without a live PostgreSQL instance.
type sqlmockHandler struct {
	fakeDBHandler
	db *sql.DB
}

func (h *sqlmockHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.db.QueryRow(query, args...)
}

func (h *sqlmockHandler) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return h.db.QueryRowContext(ctx, query, args...)
}

func (h *sqlmockHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.QueryContext(ctx, query, args...)
}

func newSQLMockRuntime(t *testing.T) (*sql.DB, sqlmock.Sqlmock, cdb.Handler, ToolAuthContext) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var handler cdb.Handler = &sqlmockHandler{db: db}
	auth := ToolAuthContext{
		Identity:    AgentIdentity{AgentID: "op", TrustLevel: "trusted", Capabilities: []string{"db_read"}},
		Enforcement: true,
		Allowlist:   map[string]bool{"get_source_status": true, "search_indexed_pages": true},
		RunID:       "run-1",
		TraceID:     "trace-1",
	}
	return db, mock, handler, auth
}

func toolTestContext(handler cdb.Handler, auth ToolAuthContext) context.Context {
	return ContextWithToolRuntime(context.Background(), ToolRuntime{
		DB:       handler,
		Auth:     auth,
		Deadline: time.Now().UTC().Add(time.Minute),
	})
}

// TestReadToolsRegister proves both adapters pass registry admission.
func TestReadToolsRegister(t *testing.T) {
	registry := NewAgentToolRegistry()
	if err := RegisterReadTools(registry); err != nil {
		t.Fatalf("register read tools: %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("expected 2 tools, got %d", registry.Len())
	}
	for _, name := range []string{"get_source_status", "search_indexed_pages"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if len(tool.RequiredCapabilities()) == 0 {
			t.Fatalf("tool %s needs capabilities", name)
		}
	}
	defs := registry.ToolDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 advertised definitions, got %d", len(defs))
	}
}

// TestGetSourceStatusAdapter pins the safe subset and scope rules.
func TestGetSourceStatusAdapter(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	tool := &sourceStatusTool{}
	ctx := toolTestContext(handler, auth)

	rows := sqlmock.NewRows([]string{"source_id", "source_uid", "url", "name", "priority",
		"sub_priority", "category_id", "usr_id", "restricted", "flags", "config"}).
		AddRow(int64(7), "uid-7", "https://example.com", "Example", "high",
			int64(0), int64(1), int64(99), int64(0), int64(0), []byte(`{"secret":"s3cret"}`))
	mock.ExpectQuery("SELECT source_id").WillReturnRows(rows)

	result, err := tool.Execute(ctx, map[string]any{"source_id": float64(7)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, forbidden := range []string{"config", "Config", "usr_id", "UsrID", "secret", "s3cret"} {
		if _, exists := result[forbidden]; exists {
			t.Fatalf("sensitive field %q exposed", forbidden)
		}
	}
	if result["source_id"] != uint64(7) || result["url"] != "https://example.com" {
		t.Fatalf("bad safe subset: %v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	// Unknown source surfaces without leaking driver text.
	mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
	if _, err := tool.Execute(ctx, map[string]any{"source_id": float64(404)}); err == nil {
		t.Fatalf("expected unknown-source error")
	}

	// Scoped runs deny out-of-scope IDs before any query.
	auth.AllowedSources = map[uint64]bool{7: true}
	scopedCtx := toolTestContext(handler, auth)
	if _, err := tool.Execute(scopedCtx, map[string]any{"source_id": float64(8)}); err == nil {
		t.Fatalf("expected scope denial")
	}
	if _, err := tool.Execute(scopedCtx, map[string]any{"source_id": "7"}); err == nil {
		t.Fatalf("expected string source_id rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("scope checks must not touch the DB: %v", err)
	}

	// Missing runtime denies without a handler.
	if _, err := tool.Execute(context.Background(), map[string]any{"source_id": float64(7)}); err == nil {
		t.Fatalf("expected missing-runtime error")
	}
}

// TestSearchIndexedPagesAdapter pins bounding, filtering, and redaction.
func TestSearchIndexedPagesAdapter(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	tool := &pageSearchTool{}
	ctx := toolTestContext(handler, auth)

	rows := sqlmock.NewRows([]string{"index_id", "source_uid", "page_url", "title",
		"snippet", "created_at", "last_updated_at", "rank"}).
		AddRow(1, "uid-a", "https://a.example/x", "TA", "SNIP", nil, nil, 0.9).
		AddRow(2, "uid-b", "https://b.example/y", "TB", "SNIP", nil, nil, 0.8)
	mock.ExpectQuery("search_pages").WillReturnRows(rows)

	result, err := tool.Execute(ctx, map[string]any{"query": "forecast", "limit": float64(10)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result["count"] != 2 {
		t.Fatalf("expected 2 pages, got %v", result)
	}

	// Scoped run without source_id is denied.
	auth.AllowedSources = map[uint64]bool{7: true}
	scopedCtx := toolTestContext(handler, auth)
	if _, err := tool.Execute(scopedCtx, map[string]any{"query": "x"}); err == nil {
		t.Fatalf("expected scoped source_id requirement")
	}

	// Scoped run filters to the allowed source UID.
	scopedRows := sqlmock.NewRows([]string{"source_id", "source_uid", "url", "name", "priority", "sub_priority", "category_id", "usr_id", "restricted", "flags", "config"})
	scopedRows.AddRow(int64(7), "uid-a", "https://a.example", "A", "high", int64(0), int64(1), int64(1), int64(0), int64(0), []byte("{}"))
	mock.ExpectQuery("SELECT source_id").WillReturnRows(scopedRows)
	mock.ExpectQuery("search_pages").WillReturnRows(
		sqlmock.NewRows([]string{"index_id", "source_uid", "page_url", "title",
			"snippet", "created_at", "last_updated_at", "rank"}).
			AddRow(1, "uid-a", "https://a.example/x", "TA", "SNIP", nil, nil, 0.9).
			AddRow(2, "uid-b", "https://b.example/y", "TB", "SNIP", nil, nil, 0.8))
	result, err = tool.Execute(scopedCtx, map[string]any{"query": "x", "source_id": float64(7)})
	if err != nil {
		t.Fatalf("scoped execute: %v", err)
	}
	if result["count"] != 1 {
		t.Fatalf("expected UID filtering to 1 page, got %v", result)
	}

	// Bounds: empty/huge query, bad limit.
	for _, args := range []map[string]any{
		{"query": " "},
		{"query": strings.Repeat("q", maxSearchQueryLen+1)},
		{"query": "x", "limit": float64(11)},
		{"query": "x", "limit": float64(0)},
	} {
		if _, err := tool.Execute(ctx, args); err == nil {
			t.Fatalf("args %v: expected rejection", args)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
