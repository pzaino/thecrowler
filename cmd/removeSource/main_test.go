package main

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	selectSourceQuery = "SELECT source_id FROM Sources WHERE url = $1"
	deleteSourceQuery = "DELETE FROM Sources WHERE source_id = $1"
	cleanupHTTPQuery  = "SELECT cleanup_orphaned_httpinfo();"
	cleanupNetQuery   = "SELECT cleanup_orphaned_netinfo();"
)

func TestRemoveSource(t *testing.T) {
	tests := []struct {
		name        string
		arrange     func(sqlmock.Sqlmock)
		wantMessage string
		wantError   string
	}{
		{
			name: "success",
			arrange: func(mock sqlmock.Sqlmock) {
				expectSourceLookup(mock, "https://example.com", 42)
				mock.ExpectExec(regexp.QuoteMeta(deleteSourceQuery)).
					WithArgs(int64(42)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta(cleanupHTTPQuery)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta(cleanupNetQuery)).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantMessage: "Source and related data removed successfully",
		},
		{
			name: "source lookup fails",
			arrange: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(selectSourceQuery)).
					WithArgs("https://example.com").
					WillReturnError(sql.ErrNoRows)
			},
			wantMessage: "Failed to remove the source",
			wantError:   sql.ErrNoRows.Error(),
		},
		{
			name: "delete fails",
			arrange: func(mock sqlmock.Sqlmock) {
				expectSourceLookup(mock, "https://example.com", 42)
				mock.ExpectExec(regexp.QuoteMeta(deleteSourceQuery)).
					WithArgs(int64(42)).
					WillReturnError(errors.New("delete failed"))
			},
			wantMessage: "Failed to delete source and related data",
			wantError:   "delete failed",
		},
		{
			name: "HTTP cleanup fails",
			arrange: func(mock sqlmock.Sqlmock) {
				expectSourceLookup(mock, "https://example.com", 42)
				mock.ExpectExec(regexp.QuoteMeta(deleteSourceQuery)).
					WithArgs(int64(42)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta(cleanupHTTPQuery)).
					WillReturnError(errors.New("HTTP cleanup failed"))
			},
			wantMessage: "Failed to cleanup orphaned httpinfo",
			wantError:   "HTTP cleanup failed",
		},
		{
			name: "network cleanup fails",
			arrange: func(mock sqlmock.Sqlmock) {
				expectSourceLookup(mock, "https://example.com", 42)
				mock.ExpectExec(regexp.QuoteMeta(deleteSourceQuery)).
					WithArgs(int64(42)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta(cleanupHTTPQuery)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta(cleanupNetQuery)).
					WillReturnError(errors.New("network cleanup failed"))
			},
			wantMessage: "Failed to cleanup orphaned netinfo",
			wantError:   "network cleanup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newMockDB(t)
			mock.ExpectBegin()
			tt.arrange(mock)

			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			got, err := removeSource(tx, "https://example.com")
			if got.Message != tt.wantMessage {
				t.Fatalf("removeSource() message = %q, want %q", got.Message, tt.wantMessage)
			}
			assertError(t, err, tt.wantError)
			assertExpectations(t, mock)
		})
	}
}

func TestRemoveSite(t *testing.T) {
	t.Run("commits successful removal", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		expectSuccessfulRemoval(mock)
		mock.ExpectCommit()

		if err := removeSite(db, "https://example.com"); err != nil {
			t.Fatalf("removeSite() error = %v", err)
		}
		assertExpectations(t, mock)
	})

	t.Run("returns begin error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		assertError(t, removeSite(db, "https://example.com"), "begin failed")
		assertExpectations(t, mock)
	})

	t.Run("rolls back removal error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSourceQuery)).
			WithArgs("https://example.com").
			WillReturnError(errors.New("lookup failed"))
		mock.ExpectRollback()

		assertError(t, removeSite(db, "https://example.com"), "lookup failed")
		assertExpectations(t, mock)
	})

	t.Run("preserves operation and rollback errors", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSourceQuery)).
			WithArgs("https://example.com").
			WillReturnError(errors.New("lookup failed"))
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		err := removeSite(db, "https://example.com")
		if err == nil || !strings.Contains(err.Error(), "lookup failed") ||
			!strings.Contains(err.Error(), "rollback transaction: rollback failed") {
			t.Fatalf("removeSite() error = %v, want operation and rollback errors", err)
		}
		assertExpectations(t, mock)
	})

	t.Run("returns commit error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		expectSuccessfulRemoval(mock)
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		assertError(t, removeSite(db, "https://example.com"), "commit failed")
		assertExpectations(t, mock)
	})
}

func TestRun(t *testing.T) {
	originalLoadConfig, originalOpenDatabase := loadConfig, openDatabase
	t.Cleanup(func() {
		loadConfig, openDatabase = originalLoadConfig, originalOpenDatabase
	})

	t.Run("rejects invalid flags", func(t *testing.T) {
		assertError(t, run([]string{"-unknown"}), "flag provided but not defined")
	})

	t.Run("returns config error", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) {
			return cfg.Config{}, errors.New("config failed")
		}
		assertError(t, run([]string{"-url", "https://example.com"}), "config failed")
	})

	t.Run("requires URL", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
		assertError(t, run(nil), "please provide a URL of the website to remove")
	})

	t.Run("returns database open error and builds connection string", func(t *testing.T) {
		loadConfig = func(path string) (cfg.Config, error) {
			if path != "custom.yaml" {
				t.Fatalf("config path = %q, want custom.yaml", path)
			}
			var configuration cfg.Config
			configuration.Database.Host = "db.example"
			configuration.Database.Port = 5433
			configuration.Database.User = "alice"
			configuration.Database.Password = "secret"
			configuration.Database.DBName = "crowler"
			return configuration, nil
		}
		openDatabase = func(driver, dataSource string) (*sql.DB, error) {
			if driver != cdb.DBPostgresStr {
				t.Fatalf("driver = %q, want %q", driver, cdb.DBPostgresStr)
			}
			want := "host=db.example port=5433 user=alice password=secret dbname=crowler sslmode=disable"
			if dataSource != want {
				t.Fatalf("data source = %q, want %q", dataSource, want)
			}
			return nil, errors.New("open failed")
		}

		assertError(t, run([]string{"-config", "custom.yaml", "-url", "https://example.com"}), "open failed")
	})

	t.Run("removes source and closes database", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		expectSuccessfulRemoval(mock)
		mock.ExpectCommit()
		mock.ExpectClose()
		loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
		openDatabase = func(string, string) (*sql.DB, error) { return db, nil }

		if err := run([]string{"-url", "https://example.com"}); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		assertExpectations(t, mock)
	})
}

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func expectSourceLookup(mock sqlmock.Sqlmock, sourceURL string, sourceID int64) {
	mock.ExpectQuery(regexp.QuoteMeta(selectSourceQuery)).
		WithArgs(sourceURL).
		WillReturnRows(sqlmock.NewRows([]string{"source_id"}).AddRow(sourceID))
}

func expectSuccessfulRemoval(mock sqlmock.Sqlmock) {
	expectSourceLookup(mock, "https://example.com", 42)
	mock.ExpectExec(regexp.QuoteMeta(deleteSourceQuery)).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(cleanupHTTPQuery)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(cleanupNetQuery)).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func assertError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want error containing %q", err, want)
	}
}

func assertExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
