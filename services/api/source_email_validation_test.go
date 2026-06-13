package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/mattn/go-sqlite3"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestSourceHandlersValidateEmailRequests(t *testing.T) {
	oldDBHandler := dbHandler
	oldDBSemaphore := dbSemaphore
	t.Cleanup(func() {
		dbHandler = oldDBHandler
		dbSemaphore = oldDBSemaphore
	})

	t.Run("create accepts valid email source", func(t *testing.T) {
		handler, cleanup := setupSourceAPITestDB(t)
		defer cleanup()
		dbHandler = handler
		dbSemaphore = make(chan struct{}, 1)

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(emailSourceRequest("imap", "imaps://mail.example.test", "")))
		addSourceHandler(recorder, request)

		if recorder.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
		}
		var stored string
		if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT config FROM Sources WHERE url = ?`, "https://source.example.test").Scan(&stored); err != nil {
			t.Fatalf("read stored source config: %v", err)
		}
		var source cfg.SourceConfig
		if err := json.Unmarshal([]byte(stored), &source); err != nil {
			t.Fatalf("decode stored source config: %v", err)
		}
		if source.Email == nil || source.Email.Connector.Provider != "imap" {
			t.Fatalf("stored email config = %#v", source.Email)
		}
	})

	t.Run("create rejects invalid email source", func(t *testing.T) {
		dbSemaphore = make(chan struct{}, 1)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(emailSourceRequest("smtp", "smtp://mail.example.test", "")))
		addSourceHandler(recorder, request)

		assertSourceValidationError(t, recorder, "connector.provider")
	})

	t.Run("update accepts valid email source", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("create sql mock: %v", err)
		}
		defer db.Close()
		dbHandler = &sourceAPITestHandler{db: db}
		dbSemaphore = make(chan struct{}, 1)

		mock.ExpectQuery(`SELECT url, status, restricted, disabled, flags, config, details`).
			WithArgs(int64(41)).
			WillReturnRows(sqlmock.NewRows([]string{"url", "status", "restricted", "disabled", "flags", "config", "details"}).
				AddRow("https://source.example.test", "new", 2, false, 0, `{}`, `{}`))
		mock.ExpectExec(`UPDATE Sources`).
			WithArgs("https://source.example.test", "new", 2, false, 0, sqlmock.AnyArg(), sqlmock.AnyArg(), int64(41)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		body := `{"source_id":41,"config":` + emailSourceConfig("imap", "imaps://mail.example.test", "") + `}`
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/source/update", strings.NewReader(body))
		updateSourceHandler(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations: %v", err)
		}
	})

	t.Run("update rejects invalid email source before database access", func(t *testing.T) {
		dbHandler = nil
		dbSemaphore = make(chan struct{}, 1)
		body := `{"source_id":41,"config":` + emailSourceConfig("smtp", "smtp://mail.example.test", "") + `}`
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/source/update", strings.NewReader(body))
		updateSourceHandler(recorder, request)

		assertSourceValidationError(t, recorder, "connector.provider")
	})

	t.Run("validation response redacts request secrets", func(t *testing.T) {
		dbHandler = nil
		dbSemaphore = make(chan struct{}, 1)
		const secret = "SHOULD_NOT_LEAK"
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(emailSourceRequest("smtp", "smtp://mail.example.test", secret)))
		addSourceHandler(recorder, request)

		assertSourceValidationError(t, recorder, "connector.provider")
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("validation response leaked request secret: %s", recorder.Body.String())
		}
	})
}

func assertSourceValidationError(t *testing.T, recorder *httptest.ResponseRecorder, detail string) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		ErrCode int    `json:"error_code"`
		Err     string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if response.ErrCode != http.StatusBadRequest || !strings.Contains(response.Err, detail) {
		t.Fatalf("unexpected validation response: %#v", response)
	}
}

func emailSourceRequest(provider, endpoint, secret string) string {
	return `{"url":"https://source.example.test","config":` + emailSourceConfig(provider, endpoint, secret) + `}`
}

func emailSourceConfig(provider, endpoint, secret string) string {
	extensions := ""
	if secret != "" {
		extensions = `,"extensions":{"password":"` + secret + `"}`
	}
	return `{"version":"1.0","format_version":"1.0","source_name":"mail archive","crawling_config":{"site":"` + endpoint + `","source_type":"email"},"email":{"connector":{"provider":"` + provider + `","endpoint":"` + endpoint + `"},"auth":{"credential_ref":"secret/archive"}` + extensions + `}}`
}

type sourceAPITestHandler struct{ db *sql.DB }

func (h *sourceAPITestHandler) Connect(cfg.Config) error { return nil }
func (h *sourceAPITestHandler) Close() error             { return h.db.Close() }
func (h *sourceAPITestHandler) Ping() error              { return h.db.Ping() }
func (h *sourceAPITestHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.Query(query, args...)
}
func (h *sourceAPITestHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return h.db.Exec(query, args...)
}
func (h *sourceAPITestHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return h.db.ExecContext(ctx, query, args...)
}
func (h *sourceAPITestHandler) DBMS() string            { return cdb.DBSQLiteStr }
func (h *sourceAPITestHandler) Begin() (*sql.Tx, error) { return h.db.Begin() }
func (h *sourceAPITestHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.db.BeginTx(ctx, opts)
}
func (h *sourceAPITestHandler) Commit(tx *sql.Tx) error   { return tx.Commit() }
func (h *sourceAPITestHandler) Rollback(tx *sql.Tx) error { return tx.Rollback() }
func (h *sourceAPITestHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.db.QueryRow(query, args...)
}
func (h *sourceAPITestHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.QueryContext(ctx, query, args...)
}
func (h *sourceAPITestHandler) CheckConnection(cfg.Config) error { return nil }
func (h *sourceAPITestHandler) NewListener() cdb.Listener        { return nil }

func setupSourceAPITestDB(t *testing.T) (cdb.Handler, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			name TEXT,
			priority TEXT,
			category_id INTEGER NOT NULL DEFAULT 0,
			usr_id INTEGER NOT NULL DEFAULT 0,
			restricted INTEGER NOT NULL DEFAULT 0,
			flags INTEGER NOT NULL DEFAULT 0,
			config TEXT,
			disabled BOOLEAN NOT NULL DEFAULT FALSE,
			status TEXT NOT NULL DEFAULT 'new',
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		db.Close()
		t.Fatalf("create Sources table: %v", err)
	}
	handler := &sourceAPITestHandler{db: db}
	return handler, func() { _ = db.Close() }
}
