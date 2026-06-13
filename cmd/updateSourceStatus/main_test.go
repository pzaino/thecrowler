// Copyright 2026 Paolo Fabio Zaino
// Licensed under the Apache License, Version 2.0

package main

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestComputeTimeWindow(t *testing.T) {
	originalNow := now
	t.Cleanup(func() { now = originalNow })
	fixed := time.Date(2026, time.March, 29, 12, 30, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("yesterday takes priority", func(t *testing.T) {
		start, end, err := computeTimeWindow(true, "bad duration", "bad timestamp", "bad timestamp")
		if err != nil {
			t.Fatalf("computeTimeWindow() error = %v", err)
		}
		wantStart := time.Date(2026, time.March, 28, 0, 0, 0, 0, london)
		wantEnd := time.Date(2026, time.March, 29, 0, 0, 0, 0, london)
		assertWindow(t, start, end, wantStart, &wantEnd)
	})

	t.Run("within takes priority over timestamps", func(t *testing.T) {
		start, end, err := computeTimeWindow(false, "90m", "bad timestamp", "bad timestamp")
		if err != nil {
			t.Fatalf("computeTimeWindow() error = %v", err)
		}
		current := fixed.In(london)
		wantStart := current.Add(-90 * time.Minute)
		assertWindow(t, start, end, wantStart, &current)
	})

	t.Run("after without upper bound", func(t *testing.T) {
		start, end, err := computeTimeWindow(false, "", "2026-02-23T00:00:00Z", "")
		if err != nil {
			t.Fatalf("computeTimeWindow() error = %v", err)
		}
		want := time.Date(2026, time.February, 23, 0, 0, 0, 0, time.UTC)
		assertWindow(t, start, end, want, nil)
	})

	t.Run("bounded timestamps", func(t *testing.T) {
		start, end, err := computeTimeWindow(false, "", "2026-02-23T00:00:00Z", "2026-02-24T00:00:00Z")
		if err != nil {
			t.Fatalf("computeTimeWindow() error = %v", err)
		}
		wantStart := time.Date(2026, time.February, 23, 0, 0, 0, 0, time.UTC)
		wantEnd := wantStart.Add(24 * time.Hour)
		assertWindow(t, start, end, wantStart, &wantEnd)
	})

	for _, test := range []struct {
		name, within, after, before, want string
	}{
		{name: "invalid duration", within: "soon", want: "invalid duration"},
		{name: "zero duration", within: "0s", want: "greater than zero"},
		{name: "negative duration", within: "-1h", want: "greater than zero"},
		{name: "missing after", before: "2026-02-24T00:00:00Z", want: "must provide -updated-after"},
		{name: "invalid after", after: "today", want: "cannot parse"},
		{name: "invalid before", after: "2026-02-23T00:00:00Z", before: "tomorrow", want: "cannot parse"},
		{name: "reversed range", after: "2026-02-24T00:00:00Z", before: "2026-02-23T00:00:00Z", want: "must be after"},
		{name: "empty range", after: "2026-02-24T00:00:00Z", before: "2026-02-24T00:00:00Z", want: "must be after"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := computeTimeWindow(false, test.within, test.after, test.before)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("computeTimeWindow() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestUpdateFunctions(t *testing.T) {
	start := time.Date(2026, time.February, 23, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	tests := []struct {
		name  string
		query string
		args  []driver.Value
		call  func(*sql.DB) error
	}{
		{name: "all", query: `UPDATE Sources SET status = $1`, args: []driver.Value{"new"}, call: func(db *sql.DB) error { return updateAllSources(db, "new") }},
		{name: "URL", query: `UPDATE Sources SET status = $1 WHERE url = $2`, args: []driver.Value{"new", "https://example.com"}, call: func(db *sql.DB) error { return updateSourceByURL(db, "https://example.com", "new") }},
		{name: "ID", query: `UPDATE Sources SET status = $1 WHERE source_id = $2`, args: []driver.Value{"new", uint64(42)}, call: func(db *sql.DB) error { return updateSourceByID(db, 42, "new") }},
		{name: "range without end", query: `UPDATE Sources SET status = $1 WHERE last_updated_at >= $2`, args: []driver.Value{"new", start}, call: func(db *sql.DB) error { return updateSourcesByUpdatedAtRange(db, "new", start, nil) }},
		{name: "bounded range", query: `UPDATE Sources SET status = $1 WHERE last_updated_at >= $2 AND last_updated_at < $3`, args: []driver.Value{"new", start, end}, call: func(db *sql.DB) error { return updateSourcesByUpdatedAtRange(db, "new", start, &end) }},
	}

	for _, test := range tests {
		t.Run(test.name+" succeeds", func(t *testing.T) {
			db, mock := newMockDB(t)
			expect := mock.ExpectExec(regexp.QuoteMeta(test.query))
			expect.WithArgs(test.args...).WillReturnResult(sqlmock.NewResult(0, 3))
			if err := test.call(db); err != nil {
				t.Fatalf("update error = %v", err)
			}
			assertExpectations(t, mock)
		})
		t.Run(test.name+" returns database error", func(t *testing.T) {
			db, mock := newMockDB(t)
			expect := mock.ExpectExec(regexp.QuoteMeta(test.query))
			expect.WithArgs(test.args...).WillReturnError(errors.New("update failed"))
			if err := test.call(db); err == nil || err.Error() != "update failed" {
				t.Fatalf("update error = %v, want update failed", err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestUpdateSourcesFromCSV(t *testing.T) {
	t.Run("updates valid rows and skips blanks", func(t *testing.T) {
		path := writeFile(t, "sites.csv", " https://one.example/// ,ignored\n\nhttps://two.example/\n")
		db, mock := newMockDB(t)
		expectURLUpdate(mock, "https://one.example", nil)
		expectURLUpdate(mock, "https://two.example", nil)

		if err := updateSourcesFromCSV(db, path, "new"); err != nil {
			t.Fatalf("updateSourcesFromCSV() error = %v", err)
		}
		assertExpectations(t, mock)
	})

	t.Run("continues after an update error", func(t *testing.T) {
		path := writeFile(t, "sites.csv", "https://one.example\nhttps://two.example\n")
		db, mock := newMockDB(t)
		expectURLUpdate(mock, "https://one.example", errors.New("database unavailable"))
		expectURLUpdate(mock, "https://two.example", nil)

		if err := updateSourcesFromCSV(db, path, "new"); err != nil {
			t.Fatalf("updateSourcesFromCSV() error = %v", err)
		}
		assertExpectations(t, mock)
	})

	t.Run("returns open error", func(t *testing.T) {
		db, _ := newMockDB(t)
		if err := updateSourcesFromCSV(db, filepath.Join(t.TempDir(), "missing.csv"), "new"); err == nil {
			t.Fatal("updateSourcesFromCSV() error = nil, want open error")
		}
	})

	t.Run("returns malformed CSV error", func(t *testing.T) {
		path := writeFile(t, "sites.csv", "\"unterminated\n")
		db, _ := newMockDB(t)
		if err := updateSourcesFromCSV(db, path, "new"); err == nil {
			t.Fatal("updateSourcesFromCSV() error = nil, want CSV error")
		}
	})
}

func TestNormalizeURL(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{input: "  https://example.com/path///  ", want: "https://example.com/path"},
		{input: "https://example.com", want: "https://example.com"},
		{input: " /// ", want: ""},
	} {
		if got := normalizeURL(test.input); got != test.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestRun(t *testing.T) {
	originalLoadConfig, originalOpenDatabase, originalNow := loadConfig, openDatabase, now
	t.Cleanup(func() {
		loadConfig, openDatabase, now = originalLoadConfig, originalOpenDatabase, originalNow
	})

	t.Run("rejects invalid flags", func(t *testing.T) {
		assertErrorContains(t, run([]string{"-unknown"}), "flag provided but not defined")
	})
	t.Run("requires nonblank status before loading config", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) { t.Fatal("loadConfig called"); return cfg.Config{}, nil }
		assertErrorContains(t, run([]string{"-status", "  ", "-all"}), "provide -status")
	})
	t.Run("returns config error", func(t *testing.T) {
		loadConfig = func(path string) (cfg.Config, error) {
			if path != "custom.yaml" {
				t.Fatalf("config path = %q", path)
			}
			return cfg.Config{}, errors.New("config failed")
		}
		assertErrorContains(t, run([]string{"-config", "custom.yaml", "-status", "new", "-all"}), "config failed")
	})
	t.Run("builds connection string and returns open error", func(t *testing.T) {
		loadConfig = testConfig
		openDatabase = func(driver, dataSource string) (*sql.DB, error) {
			if driver != cdb.DBPostgresStr {
				t.Fatalf("driver = %q", driver)
			}
			want := "host=db.example port=5433 user=alice password=secret dbname=crowler sslmode=disable"
			if dataSource != want {
				t.Fatalf("data source = %q, want %q", dataSource, want)
			}
			return nil, errors.New("open failed")
		}
		assertErrorContains(t, run([]string{"-status", "new", "-all"}), "open failed")
	})
	t.Run("requires an update mode and closes database", func(t *testing.T) {
		db, mock := configuredRunDB(t)
		mock.ExpectClose()
		assertErrorContains(t, run([]string{"-status", "new"}), "specify -url")
		assertExpectations(t, mock)
		_ = db
	})

	modes := []struct {
		name   string
		args   []string
		expect func(sqlmock.Sqlmock)
	}{
		{name: "all", args: []string{"-all"}, expect: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(`UPDATE Sources SET status = $1`)).WithArgs("new").WillReturnResult(sqlmock.NewResult(0, 2))
		}},
		{name: "URL has priority over ID and bulk", args: []string{"-url", " https://example.com/// ", "-id", "42", "-bulk", "unused.csv"}, expect: func(mock sqlmock.Sqlmock) { expectURLUpdate(mock, "https://example.com", nil) }},
		{name: "ID", args: []string{"-id", "42"}, expect: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(`UPDATE Sources SET status = $1 WHERE source_id = $2`)).WithArgs("new", uint64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
		}},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			_, mock := configuredRunDB(t)
			mode.expect(mock)
			mock.ExpectClose()
			args := append([]string{"-status", "new"}, mode.args...)
			if err := run(args); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			assertExpectations(t, mock)
		})
	}

	t.Run("time mode has priority and uses fixed clock", func(t *testing.T) {
		_, mock := configuredRunDB(t)
		fixed := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
		now = func() time.Time { return fixed }
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE Sources SET status = $1 WHERE last_updated_at >= $2 AND last_updated_at < $3`)).
			WithArgs("new", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 4))
		mock.ExpectClose()
		if err := run([]string{"-status", "new", "-all", "-updated-within", "24h"}); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		assertExpectations(t, mock)
	})
	t.Run("invalid time window", func(t *testing.T) {
		_, mock := configuredRunDB(t)
		mock.ExpectClose()
		assertErrorContains(t, run([]string{"-status", "new", "-updated-within", "bad"}), "invalid time window")
		assertExpectations(t, mock)
	})
	t.Run("wraps update error", func(t *testing.T) {
		_, mock := configuredRunDB(t)
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE Sources SET status = $1`)).WithArgs("new").WillReturnError(errors.New("write failed"))
		mock.ExpectClose()
		assertErrorContains(t, run([]string{"-status", "new", "-all"}), "update failed: write failed")
		assertExpectations(t, mock)
	})
}

func assertWindow(t *testing.T, gotStart time.Time, gotEnd *time.Time, wantStart time.Time, wantEnd *time.Time) {
	t.Helper()
	if !gotStart.Equal(wantStart) {
		t.Fatalf("start = %v, want %v", gotStart, wantStart)
	}
	if (gotEnd == nil) != (wantEnd == nil) {
		t.Fatalf("end = %v, want %v", gotEnd, wantEnd)
	}
	if gotEnd != nil && !gotEnd.Equal(*wantEnd) {
		t.Fatalf("end = %v, want %v", gotEnd, *wantEnd)
	}
}

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func assertExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectURLUpdate(mock sqlmock.Sqlmock, url string, returnedError error) {
	expect := mock.ExpectExec(regexp.QuoteMeta(`UPDATE Sources SET status = $1 WHERE url = $2`)).WithArgs("new", url)
	if returnedError != nil {
		expect.WillReturnError(returnedError)
	} else {
		expect.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func testConfig(string) (cfg.Config, error) {
	var configuration cfg.Config
	configuration.Database.Host = "db.example"
	configuration.Database.Port = 5433
	configuration.Database.User = "alice"
	configuration.Database.Password = "secret"
	configuration.Database.DBName = "crowler"
	return configuration, nil
}

func configuredRunDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newMockDB(t)
	loadConfig = testConfig
	openDatabase = func(string, string) (*sql.DB, error) { return db, nil }
	return db, mock
}
