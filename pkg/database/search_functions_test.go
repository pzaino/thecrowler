package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestSearchFunctionWrappersRejectUnsupportedDBMS(t *testing.T) {
	db := Handler(&searchFunctionTestHandler{dbms: DBSQLiteStr})
	_, err := SearchPages(context.Background(), &db, "nginx", "english", SearchFunctionOptions{Limit: 10})
	if err == nil {
		t.Fatal("expected unsupported DBMS error")
	}
	if !strings.Contains(err.Error(), "unsupported database type") || !strings.Contains(err.Error(), "search_pages") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchFunctionWrappersRejectNilHandler(t *testing.T) {
	_, err := SearchArtifacts(context.Background(), nil, "nginx", SearchFunctionOptions{})
	if err == nil || !strings.Contains(err.Error(), "database handler is nil") {
		t.Fatalf("expected nil handler error, got %v", err)
	}
}

func TestSearchFunctionWrappersRejectNegativePagination(t *testing.T) {
	db := Handler(&searchFunctionTestHandler{dbms: "PostgreSQL"})
	_, err := SearchScrapedData(context.Background(), &db, "nginx", SearchFunctionOptions{Limit: -1})
	if err == nil || !strings.Contains(err.Error(), "limit and offset must be non-negative") {
		t.Fatalf("expected negative pagination error, got %v", err)
	}
}

func TestSearchFunctionWrapperAppliesPaginationOutsideFunctionCall(t *testing.T) {
	fake := &searchFunctionTestHandler{dbms: "PostgreSQL", queryErr: errors.New("stop after capture")}
	db := Handler(fake)

	_, err := SearchPages(context.Background(), &db, "nginx", "english", SearchFunctionOptions{Limit: 25, Offset: 50})
	if err == nil || !strings.Contains(err.Error(), "stop after capture") {
		t.Fatalf("expected captured query error, got %v", err)
	}
	if fake.lastQuery != "SELECT * FROM search_pages($1, $2) LIMIT $3 OFFSET $4" {
		t.Fatalf("unexpected query: %s", fake.lastQuery)
	}
	if len(fake.lastArgs) != 4 || fake.lastArgs[0] != "nginx" || fake.lastArgs[1] != "english" || fake.lastArgs[2] != 25 || fake.lastArgs[3] != 50 {
		t.Fatalf("unexpected args: %#v", fake.lastArgs)
	}
}

func TestSearchArtifactsFieldsUsesJSONBCast(t *testing.T) {
	fake := &searchFunctionTestHandler{dbms: "PostgreSQL", queryErr: errors.New("stop after capture")}
	db := Handler(fake)

	_, err := SearchArtifactsFields(context.Background(), &db, map[string]string{"server": "nginx"}, SearchFunctionOptions{Limit: 5})
	if err == nil || !strings.Contains(err.Error(), "stop after capture") {
		t.Fatalf("expected captured query error, got %v", err)
	}
	if fake.lastQuery != "SELECT * FROM search_artifacts_fields($1::jsonb) LIMIT $2" {
		t.Fatalf("unexpected query: %s", fake.lastQuery)
	}
	if len(fake.lastArgs) != 2 || fake.lastArgs[0] != `{"server":"nginx"}` || fake.lastArgs[1] != 5 {
		t.Fatalf("unexpected args: %#v", fake.lastArgs)
	}
}

func TestMarshalSearchFilters(t *testing.T) {
	jsonString, err := marshalSearchFilters(map[string]string{"server": "nginx", "status": "200"})
	if err != nil {
		t.Fatalf("marshal filters: %v", err)
	}
	if !strings.Contains(jsonString, `"server":"nginx"`) || !strings.Contains(jsonString, `"status":"200"`) {
		t.Fatalf("unexpected filter JSON: %s", jsonString)
	}

	_, err = marshalSearchFilters(nil)
	if err == nil || !strings.Contains(err.Error(), "at least one search filter") {
		t.Fatalf("expected empty filter error, got %v", err)
	}
}

func TestJoinFunctionArgs(t *testing.T) {
	joined := joinFunctionArgs([]string{"$1", "$2::jsonb"})
	if joined != "$1, $2::jsonb" {
		t.Fatalf("unexpected joined args: %q", joined)
	}
	if empty := joinFunctionArgs(nil); empty != "" {
		t.Fatalf("expected empty join result, got %q", empty)
	}
}

type searchFunctionTestHandler struct {
	dbms      string
	queryErr  error
	lastQuery string
	lastArgs  []interface{}
}

func (h *searchFunctionTestHandler) Connect(cfg.Config) error { return nil }
func (h *searchFunctionTestHandler) Close() error             { return nil }
func (h *searchFunctionTestHandler) Ping() error              { return nil }
func (h *searchFunctionTestHandler) ExecuteQuery(string, ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("unexpected ExecuteQuery call")
}
func (h *searchFunctionTestHandler) Exec(string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("unexpected Exec call")
}
func (h *searchFunctionTestHandler) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("unexpected ExecContext call")
}
func (h *searchFunctionTestHandler) DBMS() string { return h.dbms }
func (h *searchFunctionTestHandler) Begin() (*sql.Tx, error) {
	return nil, errors.New("unexpected Begin call")
}
func (h *searchFunctionTestHandler) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("unexpected BeginTx call")
}
func (h *searchFunctionTestHandler) Commit(*sql.Tx) error {
	return errors.New("unexpected Commit call")
}
func (h *searchFunctionTestHandler) Rollback(*sql.Tx) error {
	return errors.New("unexpected Rollback call")
}
func (h *searchFunctionTestHandler) QueryRow(string, ...interface{}) *sql.Row {
	return nil
}
func (h *searchFunctionTestHandler) QueryContext(_ context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	h.lastQuery = query
	h.lastArgs = append([]interface{}(nil), args...)
	if h.queryErr != nil {
		return nil, h.queryErr
	}
	return nil, errors.New("unexpected QueryContext call")
}
func (h *searchFunctionTestHandler) CheckConnection(cfg.Config) error { return nil }
func (h *searchFunctionTestHandler) NewListener() Listener            { return nil }
