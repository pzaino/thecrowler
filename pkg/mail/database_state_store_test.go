package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	db "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

type testDatabaseHandler struct {
	database *sql.DB
	dbms     string
}

func (h *testDatabaseHandler) Connect(cfg.Config) error { return nil }
func (h *testDatabaseHandler) Close() error             { return h.database.Close() }
func (h *testDatabaseHandler) Ping() error              { return h.database.Ping() }
func (h *testDatabaseHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return h.database.Query(query, args...)
}
func (h *testDatabaseHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return h.database.Exec(query, args...)
}
func (h *testDatabaseHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return h.database.ExecContext(ctx, query, args...)
}
func (h *testDatabaseHandler) DBMS() string { return h.dbms }
func (h *testDatabaseHandler) Begin() (*sql.Tx, error) {
	return h.database.Begin()
}
func (h *testDatabaseHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.database.BeginTx(ctx, opts)
}
func (h *testDatabaseHandler) Commit(tx *sql.Tx) error   { return tx.Commit() }
func (h *testDatabaseHandler) Rollback(tx *sql.Tx) error { return tx.Rollback() }
func (h *testDatabaseHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.database.QueryRow(query, args...)
}
func (h *testDatabaseHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.database.QueryContext(ctx, query, args...)
}
func (h *testDatabaseHandler) CheckConnection(cfg.Config) error { return h.database.Ping() }
func (h *testDatabaseHandler) NewListener() db.Listener         { return nil }

func openMailStateStore(t *testing.T) (*DatabaseStateStore, *sql.DB) {
	t.Helper()

	database, err := sql.Open("sqlite3", fmt.Sprintf("file:mail-state-%d?mode=memory&cache=shared&_foreign_keys=1", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })

	if _, err = database.Exec(`CREATE TABLE Sources (source_id INTEGER PRIMARY KEY); INSERT INTO Sources(source_id) VALUES (7);`); err != nil {
		t.Fatalf("create source prerequisite: %v", err)
	}
	migration, err := os.ReadFile("../database/sqlite-migration-v1.11.sqlite3")
	if err != nil {
		t.Fatalf("read mail state migration: %v", err)
	}
	for run := 1; run <= 2; run++ {
		if _, err = database.Exec(string(migration)); err != nil {
			t.Fatalf("apply mail state migration run %d: %v", run, err)
		}
	}

	store, err := NewDatabaseStateStore(&testDatabaseHandler{database: database, dbms: db.DBSQLiteStr})
	if err != nil {
		t.Fatalf("create database state store: %v", err)
	}
	return store, database
}

func TestDatabaseStateStoreCursorUpdatesAndStatusTransitions(t *testing.T) {
	store, database := openMailStateStore(t)
	ctx := context.Background()
	key := MailboxKey{SourceID: "7", Provider: "IMAP", AccountID: "account-1", Mailbox: Mailbox{ID: "inbox"}}

	missing, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(missing) error = %v", err)
	}
	if missing != (Checkpoint{}) {
		t.Fatalf("LoadCheckpoint(missing) = %#v, want zero checkpoint", missing)
	}

	renewedAt := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	first := Checkpoint{
		Cursor:        Cursor{Token: "page-1", HistoryID: 18446744073709551615, UID: 40, UIDValidity: 9001},
		MessageStatus: MessageStatusDiscovered,
		ContentHash:   "sha256:first",
		ErrorCount:    1,
		LastError:     "retry scheduled",
		Renewal: RenewalMetadata{
			SubscriptionID: "subscription-1", ResourcePath: "/users/example/messages", Status: RenewalStatusHealthy,
			LastRenewedAt: renewedAt, ExpiresAt: renewedAt.Add(7 * 24 * time.Hour), LastAttemptAt: renewedAt,
		},
		Version: "caller-version",
	}
	if err = store.CommitCheckpoint(ctx, key, "", first); err != nil {
		t.Fatalf("CommitCheckpoint(first) error = %v", err)
	}
	stored, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(first) error = %v", err)
	}
	if stored.Version != "1" || stored.Cursor != first.Cursor || stored.MessageStatus != first.MessageStatus || stored.ContentHash != first.ContentHash || stored.ErrorCount != first.ErrorCount || stored.LastError != first.LastError || stored.Renewal != first.Renewal {
		t.Fatalf("LoadCheckpoint(first) = %#v", stored)
	}

	second := Checkpoint{
		Cursor:        Cursor{Token: "page-2", HistoryID: 9223372036854775808, UID: 41, UIDValidity: 9001},
		MessageStatus: MessageStatusFetched,
		ContentHash:   "sha256:second",
	}
	if err = store.CommitCheckpoint(ctx, key, stored.Version, second); err != nil {
		t.Fatalf("CommitCheckpoint(second) error = %v", err)
	}
	updated, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(second) error = %v", err)
	}
	if updated.Version != "2" || updated.Cursor != second.Cursor || updated.MessageStatus != second.MessageStatus || updated.ContentHash != second.ContentHash || updated.ErrorCount != 0 || updated.LastError != "" {
		t.Fatalf("LoadCheckpoint(second) = %#v", updated)
	}

	err = store.CommitCheckpoint(ctx, key, updated.Version, Checkpoint{Cursor: Cursor{UID: 99}, MessageStatus: MessageStatusCompleted})
	if !errors.Is(err, ErrInvalidMessageStatusTransition) {
		t.Fatalf("invalid status transition error = %v, want ErrInvalidMessageStatusTransition", err)
	}
	afterInvalid, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(after invalid transition) error = %v", err)
	}
	if afterInvalid != updated {
		t.Fatalf("invalid transition changed checkpoint from %#v to %#v", updated, afterInvalid)
	}

	if err = store.CommitCheckpoint(ctx, key, "1", first); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("stale CommitCheckpoint error = %v, want ErrCheckpointConflict", err)
	}
	var rows int
	if err = database.QueryRow("SELECT COUNT(*) FROM EmailMailboxState").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("mailbox row count = %d, error = %v", rows, err)
	}
}

func TestDatabaseStateStoreRollsBackFailedUpdate(t *testing.T) {
	store, database := openMailStateStore(t)
	ctx := context.Background()
	key := MailboxKey{SourceID: "7", Provider: "imap", AccountID: "account-1", Mailbox: Mailbox{ID: "inbox"}}
	first := Checkpoint{Cursor: Cursor{UID: 1, UIDValidity: 2}, MessageStatus: MessageStatusDiscovered}
	if err := store.CommitCheckpoint(ctx, key, "", first); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_mail_cursor BEFORE UPDATE ON EmailMailboxState
		WHEN NEW.cursor_token = 'reject' BEGIN SELECT RAISE(ABORT, 'forced checkpoint failure'); END;`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}

	err := store.CommitCheckpoint(ctx, key, "1", Checkpoint{Cursor: Cursor{Token: "reject", UID: 2, UIDValidity: 2}, MessageStatus: MessageStatusFetched})
	if err == nil {
		t.Fatal("CommitCheckpoint(forced failure) returned nil")
	}
	stored, loadErr := store.LoadCheckpoint(ctx, key)
	if loadErr != nil {
		t.Fatalf("LoadCheckpoint(after rollback) error = %v", loadErr)
	}
	if stored.Version != "1" || stored.Cursor != first.Cursor || stored.MessageStatus != first.MessageStatus {
		t.Fatalf("failed transaction persisted partial state: %#v", stored)
	}
}

func TestEmailMessageStateConstraintsDeduplicateAndCascade(t *testing.T) {
	store, database := openMailStateStore(t)
	key := MailboxKey{SourceID: "7", Provider: "imap", AccountID: "account-1", Mailbox: Mailbox{ID: "inbox"}}
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{}); err != nil {
		t.Fatalf("create mailbox state: %v", err)
	}

	insert := `INSERT INTO EmailMessageState (
		source_id, provider, account_key, mailbox_key, provider_message_key,
		document_id, disposition, deleted_at, quarantined_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := database.Exec(insert, 7, "imap", "account-1", "inbox", "9001:42", "mail:7:account-1:inbox:9001:42", "indexed", nil, nil); err != nil {
		t.Fatalf("insert message state: %v", err)
	}
	if _, err := database.Exec(insert, 7, "imap", "account-1", "inbox", "9001:42", "different-document", "indexed", nil, nil); err == nil {
		t.Fatal("duplicate provider message identity was accepted")
	}
	if _, err := database.Exec(insert, 7, "imap", "account-1", "inbox", "9001:43", "mail:7:account-1:inbox:9001:42", "indexed", nil, nil); err == nil {
		t.Fatal("duplicate canonical document identity was accepted")
	}
	if _, err := database.Exec(insert, 7, "imap", "account-1", "inbox", "9001:44", "mail:deleted", "deleted", nil, nil); err == nil {
		t.Fatal("deleted disposition without tombstone timestamp was accepted")
	}

	if _, err := database.Exec("DELETE FROM Sources WHERE source_id = 7"); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	for _, table := range []string{"EmailMailboxState", "EmailMessageState"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after source cascade = %d, error = %v", table, count, err)
		}
	}
}

func TestNewDatabaseStateStoreCompatibility(t *testing.T) {
	tests := []struct {
		dbms string
		want string
	}{
		{dbms: "postgresql", want: db.DBPostgresStr},
		{dbms: "sqlite", want: db.DBSQLiteStr},
		{dbms: "mariadb", want: db.DBMySQLStr},
	}
	for _, test := range tests {
		t.Run(test.dbms, func(t *testing.T) {
			handler := &testDatabaseHandler{dbms: test.dbms}
			store, err := NewSQLStateStore(handler)
			if err != nil {
				t.Fatalf("NewSQLStateStore(%q) error = %v", test.dbms, err)
			}
			if store.dbms != test.want {
				t.Fatalf("normalized DBMS = %q, want %q", store.dbms, test.want)
			}
		})
	}
	if _, err := NewDatabaseStateStore(&testDatabaseHandler{dbms: "oracle"}); err == nil {
		t.Fatal("unsupported DBMS was accepted")
	}
	if _, err := NewDatabaseStateStore(nil); err == nil {
		t.Fatal("nil database handler was accepted")
	}
}
