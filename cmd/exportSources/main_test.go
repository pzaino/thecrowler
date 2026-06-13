package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var exportColumns = []string{
	"source_id", "source_url", "index_id", "page_url", "page_created_at",
	"page_last_updated_at", "object_id", "object_type", "object_link",
	"object_hash", "object_content", "object_html", "details",
	"object_created_at", "object_last_updated_at",
}

func TestCollectExport(t *testing.T) {
	db, mock := newMockDB(t)
	exportedAt := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
	pageCreatedAt := exportedAt.Add(-3 * time.Hour)
	objectCreatedAt := exportedAt.Add(-2 * time.Hour)
	objectUpdatedAt := exportedAt.Add(-time.Hour)

	rows := sqlmock.NewRows(exportColumns).
		AddRow(2, "https://two.example", 20, "/first", pageCreatedAt, nil,
			200, "link", "https://target.example", "hash", "content", "<p>html</p>",
			[]byte(`{"status":200}`), objectCreatedAt, objectUpdatedAt).
		AddRow(2, "https://two.example", 20, "/first", pageCreatedAt, nil,
			201, "image", "/image.png", "image-hash", nil, nil,
			[]byte(`{"width":100}`), objectCreatedAt, nil).
		AddRow(2, "https://two.example", 21, "/empty", pageCreatedAt, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil).
		AddRow(1, "https://one.example", 10, "/", pageCreatedAt, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).WillReturnRows(rows)

	got, err := collectExport(db, exportedAt)
	if err != nil {
		t.Fatalf("collectExport() error = %v", err)
	}

	if got.ExportedAt != exportedAt {
		t.Fatalf("ExportedAt = %v, want %v", got.ExportedAt, exportedAt)
	}
	if len(got.Sources) != 2 || got.Sources[0].SourceID != 2 || got.Sources[1].SourceID != 1 {
		t.Fatalf("Sources = %#v, want query order preserved", got.Sources)
	}
	if len(got.Sources[0].Pages) != 2 {
		t.Fatalf("first source page count = %d, want 2", len(got.Sources[0].Pages))
	}
	objects := got.Sources[0].Pages[0].Objects
	if len(objects) != 2 {
		t.Fatalf("object count = %d, want 2", len(objects))
	}
	if objects[0].ObjectContent == nil || *objects[0].ObjectContent != "content" {
		t.Fatalf("ObjectContent = %v, want content", objects[0].ObjectContent)
	}
	if objects[0].ObjectHTML == nil || *objects[0].ObjectHTML != "<p>html</p>" {
		t.Fatalf("ObjectHTML = %v, want HTML", objects[0].ObjectHTML)
	}
	if objects[0].LastUpdatedAt == nil || !objects[0].LastUpdatedAt.Equal(objectUpdatedAt) {
		t.Fatalf("LastUpdatedAt = %v, want %v", objects[0].LastUpdatedAt, objectUpdatedAt)
	}
	if objects[1].ObjectContent != nil || objects[1].ObjectHTML != nil || objects[1].LastUpdatedAt != nil {
		t.Fatalf("nullable object fields were unexpectedly populated: %#v", objects[1])
	}
	if got.Sources[0].Pages[1].Objects == nil || len(got.Sources[0].Pages[1].Objects) != 0 {
		t.Fatalf("empty page objects = %#v, want non-nil empty slice", got.Sources[0].Pages[1].Objects)
	}
	assertExpectations(t, mock)
}

func TestCollectExportErrors(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).WillReturnError(errors.New("query failed"))
		_, err := collectExport(db, time.Time{})
		if err == nil || err.Error() != "query failed" {
			t.Fatalf("collectExport() error = %v, want query failed", err)
		}
	})

	t.Run("scan", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).
			WillReturnRows(sqlmock.NewRows(exportColumns).AddRow(
				"not-an-id", "url", 1, "/", time.Now(), nil,
				nil, nil, nil, nil, nil, nil, nil, nil, nil,
			))
		if _, err := collectExport(db, time.Time{}); err == nil {
			t.Fatal("collectExport() error = nil, want scan error")
		}
	})

	t.Run("negative object ID", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).
			WillReturnRows(sqlmock.NewRows(exportColumns).AddRow(
				1, "url", 1, "/", time.Now(), nil,
				-1, "type", "link", "hash", nil, nil, nil, time.Now(), nil,
			))
		_, err := collectExport(db, time.Time{})
		if err == nil || err.Error() != "web object ID cannot be negative" {
			t.Fatalf("collectExport() error = %v, want negative ID error", err)
		}
	})

	t.Run("rows", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).
			WillReturnRows(sqlmock.NewRows(exportColumns).RowError(0, errors.New("row failed")).
				AddRow(1, "url", 1, "/", time.Now(), nil,
					nil, nil, nil, nil, nil, nil, nil, nil, nil))
		_, err := collectExport(db, time.Time{})
		if err == nil || err.Error() != "row failed" {
			t.Fatalf("collectExport() error = %v, want row failed", err)
		}
	})
}

func TestEncodeExport(t *testing.T) {
	t.Run("writes indented JSON", func(t *testing.T) {
		var output bytes.Buffer
		export := Export{Sources: []Source{}}
		if err := encodeExport(&output, export); err != nil {
			t.Fatalf("encodeExport() error = %v", err)
		}
		if !strings.Contains(output.String(), "\n  \"sources\": []\n") {
			t.Fatalf("encodeExport() output is not indented: %q", output.String())
		}
	})

	t.Run("returns writer error", func(t *testing.T) {
		if err := encodeExport(errorWriter{}, Export{}); err == nil {
			t.Fatal("encodeExport() error = nil, want writer error")
		}
	})
}

func TestRun(t *testing.T) {
	originalLoadConfig, originalOpenDatabase := loadConfig, openDatabase
	originalNow, originalCreateFile := now, createFile
	t.Cleanup(func() {
		loadConfig, openDatabase = originalLoadConfig, originalOpenDatabase
		now, createFile = originalNow, originalCreateFile
	})

	t.Run("invalid flags", func(t *testing.T) {
		if err := run([]string{"-unknown"}, io.Discard); err == nil {
			t.Fatal("run() error = nil, want flag error")
		}
	})

	t.Run("config error", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) {
			return cfg.Config{}, errors.New("config failed")
		}
		if err := run(nil, io.Discard); err == nil || err.Error() != "config failed" {
			t.Fatalf("run() error = %v, want config failed", err)
		}
	})

	t.Run("exports to stdout", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).WillReturnRows(sqlmock.NewRows(exportColumns))
		mock.ExpectClose()
		setRunDependencies(db)
		fixedNow := time.Date(2026, time.June, 13, 1, 2, 3, 0, time.FixedZone("test", 3600))
		now = func() time.Time { return fixedNow }

		var output bytes.Buffer
		if err := run([]string{"-config", "custom.yaml"}, &output); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		var got Export
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatalf("invalid JSON output: %v", err)
		}
		if !got.ExportedAt.Equal(fixedNow.UTC()) || got.ExportedAt.Location() != time.UTC {
			t.Fatalf("ExportedAt = %v, want %v UTC", got.ExportedAt, fixedNow.UTC())
		}
		assertExpectations(t, mock)
	})

	t.Run("creates output file", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).WillReturnRows(sqlmock.NewRows(exportColumns))
		mock.ExpectClose()
		setRunDependencies(db)
		file := &recordingWriteCloser{}
		createFile = func(name string) (io.WriteCloser, error) {
			if name != "export.json" {
				t.Fatalf("createFile name = %q, want export.json", name)
			}
			return file, nil
		}

		if err := run([]string{"-out", "export.json"}, io.Discard); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		if !file.closed || file.Len() == 0 {
			t.Fatalf("output file closed = %v, bytes = %d", file.closed, file.Len())
		}
		assertExpectations(t, mock)
	})

	t.Run("database open error", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
		openDatabase = func(string, string) (*sql.DB, error) {
			return nil, errors.New("open failed")
		}
		if err := run(nil, io.Discard); err == nil || err.Error() != "open failed" {
			t.Fatalf("run() error = %v, want open failed", err)
		}
	})

	t.Run("output creation error", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(exportQuery)).WillReturnRows(sqlmock.NewRows(exportColumns))
		mock.ExpectClose()
		setRunDependencies(db)
		createFile = func(string) (io.WriteCloser, error) {
			return nil, errors.New("create failed")
		}
		if err := run([]string{"-out", "bad"}, io.Discard); err == nil || err.Error() != "create failed" {
			t.Fatalf("run() error = %v, want create failed", err)
		}
	})
}

func setRunDependencies(db *sql.DB) {
	loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
	openDatabase = func(string, string) (*sql.DB, error) { return db, nil }
	now = time.Now
	createFile = func(string) (io.WriteCloser, error) {
		return nil, errors.New("unexpected createFile call")
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

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type recordingWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (writer *recordingWriteCloser) Close() error {
	writer.closed = true
	return nil
}
