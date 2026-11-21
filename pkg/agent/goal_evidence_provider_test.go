package agent

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestSourceMetadataProviderSafeSubset(t *testing.T) {
	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	var handler cdb.Handler = &sqlmockHandler{db: db}
	provider := &sourceMetadataProvider{db: handler}
	observations, err := provider.Read(context.Background(), EvidenceRequest{
		Selector: EvidenceSelector{SourceID: uint64Ptr(7)}, Now: time.Now().UTC(),
	})
	if err != nil || len(observations) != 1 {
		t.Fatalf("read: %v, %v", observations, err)
	}
	payload := observations[0].Payload
	if payload["source_id"] != uint64(7) || payload["uid"] != "uid-7" {
		t.Fatalf("safe fields missing: %+v", payload)
	}
	if _, exists := payload["config"]; exists {
		t.Fatalf("source config exposed: %+v", payload)
	}
	if _, exists := payload["usr_id"]; exists {
		t.Fatalf("source user linkage exposed: %+v", payload)
	}
}

func TestIndexedPageProviderReadsMetadataOnly(t *testing.T) {
	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	rows := sqlmock.NewRows([]string{"index_id", "source_uid", "page_url", "created_at", "last_updated_at"}).
		AddRow(uint64(11), "uid-7", "https://example.com/page", time.Now().UTC(), time.Now().UTC())
	mock.ExpectQuery("SELECT si.index_id").
		WithArgs(uint64(7), 16).
		WillReturnRows(rows)
	var handler cdb.Handler = &sqlmockHandler{db: db}
	provider := &indexedPageProvider{db: handler}
	observations, err := provider.Read(context.Background(), EvidenceRequest{
		Selector: EvidenceSelector{SourceID: uint64Ptr(7)}, Limit: 16, Now: time.Now().UTC(),
	})
	if err != nil || len(observations) != 1 {
		t.Fatalf("read: %v, %v", observations, err)
	}
	payload := observations[0].Payload
	if payload["page_url"] != "https://example.com/page" || payload["index_id"] != uint64(11) {
		t.Fatalf("metadata missing: %+v", payload)
	}
	if _, exists := payload["title"]; exists {
		t.Fatalf("page title marked verified: %+v", payload)
	}
	if _, exists := payload["snippet"]; exists {
		t.Fatalf("page snippet marked verified: %+v", payload)
	}
	if len(observations[0].SourceIDs) != 1 || observations[0].SourceIDs[0] != 7 {
		t.Fatalf("source scope missing: %+v", observations[0])
	}
}

func TestSourceStatusProviderReadsStatus(t *testing.T) {
	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	statusRows := sqlmock.NewRows([]string{
		"source_id", "source_uid", "url", "status", "priority", "sub_priority",
		"engine", "created_at", "last_updated_at", "last_crawled_at",
		"last_error", "last_error_at", "restricted", "disabled", "flags", "config",
	}).AddRow(
		uint64(7), "uid-7", "https://example.com", "completed", "high", 0,
		"engine", "2026-11-06 12:00:00", "2026-11-06 12:01:00", "2026-11-06 12:01:00",
		"", "", 0, false, 0, []byte(`{}`),
	)
	mock.ExpectQuery("WHERE source_uid").WillReturnRows(statusRows)
	mock.ExpectQuery("SELECT mailbox.source_id").WillReturnRows(sqlmock.NewRows([]string{
		"source_id", "mailbox_count", "active_mailbox_count", "last_reconciled_at",
		"listener_healthy_at", "has_token_cursor", "has_history_cursor", "has_uid_cursor",
		"checkpointed_mailboxes", "processed_count", "failed_count",
	}))
	var handler cdb.Handler = &sqlmockHandler{db: db}
	provider := &sourceStatusProvider{db: handler}
	observations, err := provider.Read(context.Background(), EvidenceRequest{
		Selector: EvidenceSelector{SourceID: uint64Ptr(7), Status: "completed"},
		Now:      time.Now().UTC(),
	})
	if err != nil || len(observations) != 1 {
		t.Fatalf("read: %v, %v", observations, err)
	}
	if observations[0].Payload["status"] != "completed" {
		t.Fatalf("status missing: %+v", observations[0].Payload)
	}
}
