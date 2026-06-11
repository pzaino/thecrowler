package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestSourceStatusResponseExposesSafeEmailOperationalSummary(t *testing.T) {
	rows := sourceStatusRowsFromDB([]cdb.SourceStatusRow{{
		SourceID: 42,
		URL:      sql.NullString{String: "imap://mail.example.test", Valid: true},
		EmailStatus: &cdb.SourceEmailStatusRow{
			ListenerStatus:        "degraded",
			LastSynchronizedAt:    sql.NullString{String: "2026-06-10T13:14:15Z", Valid: true},
			MailboxCount:          2,
			CheckpointedMailboxes: 2,
			HasTokenCursor:        true,
			HasHistoryCursor:      true,
			ProcessedCount:        7,
			FailedCount:           2,
			LastErrorCategory:     "transient",
		},
	}})

	payload, err := json.Marshal(StatusResponse{Message: infoAllSourcesStatus, Items: rows})
	if err != nil {
		t.Fatalf("marshal source status response: %v", err)
	}

	var response struct {
		Items []struct {
			EmailStatus *SourceEmailStatus `json:"email_status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("unmarshal source status response: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].EmailStatus == nil {
		t.Fatalf("expected one email status response, got %s", payload)
	}
	status := response.Items[0].EmailStatus
	if status.ListenerStatus != "degraded" || status.LastSynchronizedAt != "2026-06-10T13:14:15Z" {
		t.Fatalf("unexpected listener synchronization response: %#v", status)
	}
	if status.CursorSummary.MailboxCount != 2 || status.CursorSummary.CheckpointedMailboxes != 2 ||
		!status.CursorSummary.HasTokenCursor || !status.CursorSummary.HasHistoryCursor || status.CursorSummary.HasUIDCursor {
		t.Fatalf("unexpected cursor response: %#v", status.CursorSummary)
	}
	if status.ProcessedCount != 7 || status.FailedCount != 2 || status.LastErrorCategory != "transient" {
		t.Fatalf("unexpected outcome response: %#v", status)
	}

	encoded := string(payload)
	for _, forbidden := range []string{
		"opaque-secret-cursor",
		"mailbox@example.test",
		"authentication failed",
		"credential_ref",
		"password",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("source status response exposed forbidden value %q: %s", forbidden, encoded)
		}
	}
}

func TestSourceStatusResponseOmitsAbsentEmailState(t *testing.T) {
	rows := sourceStatusRowsFromDB([]cdb.SourceStatusRow{{
		SourceID: 7,
		URL:      sql.NullString{String: "https://example.test", Valid: true},
	}})

	payload, err := json.Marshal(StatusResponse{Message: infoAllSourcesStatus, Items: rows})
	if err != nil {
		t.Fatalf("marshal source status response: %v", err)
	}
	if strings.Contains(string(payload), "email_status") {
		t.Fatalf("response should omit absent email state: %s", payload)
	}
}
