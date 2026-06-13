package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gopkg.in/yaml.v2"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const insertSourceQuery = `INSERT INTO Sources \(`

func TestPrepareURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "trims whitespace and slashes", url: "  https://Example.COM/path///  ", want: "https://Example.COM/path"},
		{name: "deobfuscates HTTP", url: "hxxp[:]//example[.]com[/]path[?]a=1", want: "http://example.com/path?a=1"},
		{name: "deobfuscates HTTPS", url: "hxxps(:)//example(.)com", want: "https://example.com"},
		{name: "deobfuscates FTP", url: "fxp{:}//example{.}com{/}file", want: "ftp://example.com/file"},
		{name: "deobfuscates FTPS", url: "fxps://example.com/", want: "ftps://example.com"},
		{name: "leaves ordinary URL unchanged", url: "mailto:user@example.com", want: "mailto:user@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prepareURL(tt.url); got != tt.want {
				t.Fatalf("prepareURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	got := normalizeURL("  hxxps://example[.]com/// ")
	if want := "https://example.com"; got != want {
		t.Fatalf("normalizeURL() = %q, want %q", got, want)
	}
}

func TestRunValidationErrors(t *testing.T) {
	t.Run("invalid flag", func(t *testing.T) {
		if err := run([]string{"-not-a-real-flag"}); err == nil {
			t.Fatal("run() error = nil, want flag parsing error")
		}
	})

	t.Run("missing config", func(t *testing.T) {
		if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml"), "-url", "example.com"}); err == nil {
			t.Fatal("run() error = nil, want config loading error")
		}
	})

	t.Run("requires URL or bulk file", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content, err := yaml.Marshal(cfg.NewConfig())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, content, 0o600); err != nil {
			t.Fatal(err)
		}

		err = run([]string{"-config", configPath})
		if err == nil || err.Error() != "please provide a URL of the website to add or a file name for a bulk add" {
			t.Fatalf("run() error = %v, want missing input error", err)
		}
	})
}

func TestGetSourceConfig(t *testing.T) {
	t.Run("empty filename returns empty JSON", func(t *testing.T) {
		got, err := getSourceConfig(" \t ")
		if err != nil {
			t.Fatalf("getSourceConfig() error = %v", err)
		}
		if got == nil || len(*got) != 0 {
			t.Fatalf("getSourceConfig() = %v, want non-nil empty raw message", got)
		}
	})

	t.Run("valid file preserves JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "source.json")
		content := []byte(`{"version":"1.0","format_version":"1.0"}`)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := getSourceConfig(path)
		if err != nil {
			t.Fatalf("getSourceConfig() error = %v", err)
		}
		if got == nil || string(*got) != string(content) {
			t.Fatalf("getSourceConfig() = %q, want %q", string(*got), string(content))
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := getSourceConfig(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("getSourceConfig() error = nil, want file error")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := getSourceConfig(path)
		if err == nil {
			t.Fatal("getSourceConfig() error = nil, want JSON error")
		}
	})
}

func TestInsertWebsite(t *testing.T) {
	t.Run("inserts normalized source", func(t *testing.T) {
		db, mock := newMockDB(t)
		rawConfig := json.RawMessage(`{"version":"1.0"}`)
		source := cdb.Source{
			URL:        "  hxxps://Example[.]com/// ",
			CategoryID: 12,
			UsrID:      34,
			Restricted: 1,
			Flags:      5,
			Config:     &rawConfig,
		}

		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://Example.com", uint64(12), uint64(34), uint(1), uint(5), &rawConfig).
			WillReturnRows(sqlmock.NewRows([]string{"source_id"}).AddRow(uint64(99)))

		if err := insertWebsite(db, &source); err != nil {
			t.Fatalf("insertWebsite() error = %v", err)
		}
		if source.URL != "https://Example.com" {
			t.Fatalf("source.URL = %q, want normalized URL", source.URL)
		}
		assertMockExpectations(t, mock)
	})

	t.Run("returns query error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://example.com", uint64(0), uint64(0), uint(0), uint(0), nil).
			WillReturnError(errors.New("insert failed"))

		err := insertWebsite(db, &cdb.Source{URL: "https://example.com"})
		if err == nil || err.Error() != "insert failed" {
			t.Fatalf("insertWebsite() error = %v, want insert failed", err)
		}
		assertMockExpectations(t, mock)
	})

	t.Run("returns scan error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).
			WillReturnRows(sqlmock.NewRows([]string{"source_id"}).AddRow("not-a-number"))

		if err := insertWebsite(db, &cdb.Source{URL: "example.com"}); err == nil {
			t.Fatal("insertWebsite() error = nil, want scan error")
		}
		assertMockExpectations(t, mock)
	})
}

func TestInsertWebsitesFromFile(t *testing.T) {
	t.Run("inserts full and default records", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "source.json")
		if err := os.WriteFile(configPath, []byte(`{"version":"1.0"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		csvPath := writeCSV(t,
			" hxxps://one[.]example/ ,2,3,0,7,"+configPath+"\n"+
				"https://two.example,4\n"+
				"https://five-columns.example,5,6,1,9\n")

		db, mock := newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://one.example", uint64(2), uint64(3), uint(0), uint(7), sqlmock.AnyArg()).
			WillReturnRows(sourceIDRow(1))
		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://two.example", uint64(4), uint64(0), uint(1), uint(0), nil).
			WillReturnRows(sourceIDRow(2))
		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://five-columns.example", uint64(5), uint64(6), uint(1), uint(9), nil).
			WillReturnRows(sourceIDRow(3))

		forceInsert = false
		t.Cleanup(func() { forceInsert = false })
		if err := insertWebsitesFromFile(db, csvPath); err != nil {
			t.Fatalf("insertWebsitesFromFile() error = %v", err)
		}
		assertMockExpectations(t, mock)
	})

	t.Run("missing CSV file", func(t *testing.T) {
		db, _ := newMockDB(t)
		if err := insertWebsitesFromFile(db, filepath.Join(t.TempDir(), "missing.csv")); err == nil {
			t.Fatal("insertWebsitesFromFile() error = nil, want file error")
		}
	})

	for _, tc := range []struct {
		name   string
		record string
	}{
		{name: "invalid category", record: "example.com,nope\n"},
		{name: "invalid user", record: "example.com,1,nope\n"},
		{name: "invalid restricted", record: "example.com,1,2,nope\n"},
		{name: "invalid flags", record: "example.com,1,2,1,nope\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := newMockDB(t)
			forceInsert = false
			if err := insertWebsitesFromFile(db, writeCSV(t, tc.record)); err == nil {
				t.Fatal("insertWebsitesFromFile() error = nil, want parse error")
			}
		})
	}

	t.Run("returns malformed CSV error unless forced", func(t *testing.T) {
		db, _ := newMockDB(t)
		path := writeCSV(t, "\"unterminated\n")

		forceInsert = false
		if err := insertWebsitesFromFile(db, path); err == nil {
			t.Fatal("insertWebsitesFromFile() error = nil, want CSV error")
		}

		forceInsert = true
		t.Cleanup(func() { forceInsert = false })
		if err := insertWebsitesFromFile(db, path); err != nil {
			t.Fatalf("forced insert returned error: %v", err)
		}
	})

	t.Run("config error obeys force flag", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing.json")
		path := writeCSV(t, "https://example.com,1,2,1,0,"+missing+"\n")

		db, _ := newMockDB(t)
		forceInsert = false
		if err := insertWebsitesFromFile(db, path); err == nil {
			t.Fatal("insertWebsitesFromFile() error = nil, want config error")
		}

		db, mock := newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).
			WithArgs("https://example.com", uint64(1), uint64(2), uint(1), uint(0), nil).
			WillReturnRows(sourceIDRow(1))
		forceInsert = true
		t.Cleanup(func() { forceInsert = false })
		if err := insertWebsitesFromFile(db, path); err != nil {
			t.Fatalf("forced insert returned error: %v", err)
		}
		assertMockExpectations(t, mock)
	})

	t.Run("insert error obeys force flag", func(t *testing.T) {
		path := writeCSV(t, "https://one.example\nhttps://two.example\n")

		db, mock := newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).WillReturnError(errors.New("database unavailable"))
		forceInsert = false
		if err := insertWebsitesFromFile(db, path); err == nil {
			t.Fatal("insertWebsitesFromFile() error = nil, want insert error")
		}
		assertMockExpectations(t, mock)

		db, mock = newMockDB(t)
		mock.ExpectQuery(insertSourceQuery).WillReturnError(errors.New("first failed"))
		mock.ExpectQuery(insertSourceQuery).WillReturnRows(sourceIDRow(2))
		forceInsert = true
		t.Cleanup(func() { forceInsert = false })
		if err := insertWebsitesFromFile(db, path); err != nil {
			t.Fatalf("forced insert returned error: %v", err)
		}
		assertMockExpectations(t, mock)
	})
}

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func assertMockExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func sourceIDRow(id uint64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source_id"}).AddRow(id)
}

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sources.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInsertSourceQueryPatternIsValid(t *testing.T) {
	if _, err := regexp.Compile(insertSourceQuery); err != nil {
		t.Fatalf("insertSourceQuery is invalid: %v", err)
	}
}
