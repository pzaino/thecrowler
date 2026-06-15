package database

import (
	"database/sql"
	"encoding/json"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

func TestCreateSourceSQLiteUpsertPreservesProcessingRows(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_uid VARCHAR(64) NOT NULL,
			url TEXT NOT NULL UNIQUE,
			name VARCHAR(255),
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT,
			disabled BOOLEAN DEFAULT FALSE,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			last_updated_at TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create Sources table: %v", err)
	}

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	config := cfg.SourceConfig{}
	firstID, err := CreateSource(&handler, &Source{
		URL:        " https://example.test ",
		Name:       " first ",
		Priority:   " high ",
		CategoryID: 1,
		UsrID:      2,
		Restricted: 3,
		Flags:      4,
		Disabled:   false,
	}, config)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if firstID == 0 {
		t.Fatal("expected inserted source id")
	}

	secondID, err := CreateSource(&handler, &Source{
		URL:        "https://example.test",
		Name:       " second ",
		Priority:   " low ",
		CategoryID: 10,
		UsrID:      20,
		Restricted: 30,
		Flags:      40,
		Disabled:   true,
	}, config)
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected existing id %d, got %d", firstID, secondID)
	}

	assertSourceRow(t, db, firstID, "second", "low", 10, 20, 30, 40, true, "new")

	_, err = db.Exec(`UPDATE Sources SET status = 'processing' WHERE source_id = ?`, firstID)
	if err != nil {
		t.Fatalf("mark source processing: %v", err)
	}

	thirdID, err := CreateSource(&handler, &Source{
		URL:        "https://example.test",
		Name:       " blocked ",
		Priority:   " blocked ",
		CategoryID: 99,
		UsrID:      98,
		Restricted: 97,
		Flags:      96,
		Disabled:   false,
	}, config)
	if err != nil {
		t.Fatalf("upsert processing source: %v", err)
	}
	if thirdID != firstID {
		t.Fatalf("expected processing row id %d, got %d", firstID, thirdID)
	}

	assertSourceRow(t, db, firstID, "second", "low", 10, 20, 30, 40, true, "processing")
}

func TestNormalizeSourceURLMakesNestedURLsSearchable(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "decodes nested URL scheme and path separators",
			raw:  " https://auth.aircall.io/login?redirect=https%3A%2F%2Fdashboard.aircall.io%2F ",
			want: "https://auth.aircall.io/login?redirect=https://dashboard.aircall.io/",
		},
		{
			name: "supports lowercase percent escapes",
			raw:  "https://example.test/?next=https%3a%2f%2fother.test%2fpath",
			want: "https://example.test/?next=https://other.test/path",
		},
		{
			name: "preserves encoded query delimiters",
			raw:  "https://example.test/?next=https%3A%2F%2Fother.test%2F%3Fa%3D1%26b%3D2%23fragment&token=a%2Bb",
			want: "https://example.test/?next=https://other.test/%3Fa%3D1%26b%3D2%23fragment&token=a%2Bb",
		},
		{
			name: "leaves malformed URLs trimmed",
			raw:  "  https://example.test/%zz  ",
			want: "https://example.test/%zz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeSourceURL(tt.raw); got != tt.want {
				t.Fatalf("NormalizeSourceURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCalculateSourceUIDIsStableAndSensitiveToSourceIdentity(t *testing.T) {
	got := CalculateSourceUID(" Example ", " https://example.test/path ")
	if len(got) != 64 {
		t.Fatalf("UID length = %d, want 64", len(got))
	}
	if got != CalculateSourceUID("Example", "https://example.test/path") {
		t.Fatal("UID should use trimmed name and normalized URL")
	}
	if got == CalculateSourceUID("Other", "https://example.test/path") {
		t.Fatal("UID should change when the source name changes")
	}
	if got == CalculateSourceUID("Example", "https://other.test/path") {
		t.Fatal("UID should change when the source URL changes")
	}
}

func TestCreateSourceNormalizesNestedURLBeforeStorage(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_uid VARCHAR(64) NOT NULL,
			url TEXT NOT NULL UNIQUE,
			name TEXT,
			priority TEXT NOT NULL DEFAULT '',
			category_id INTEGER NOT NULL DEFAULT 0,
			usr_id INTEGER NOT NULL DEFAULT 0,
			restricted INTEGER NOT NULL DEFAULT 2,
			flags INTEGER NOT NULL DEFAULT 0,
			config TEXT,
			disabled BOOLEAN NOT NULL DEFAULT FALSE,
			status TEXT NOT NULL DEFAULT 'new',
			last_updated_at TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create Sources table: %v", err)
	}

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	sourceID, err := CreateSource(&handler, &Source{
		URL: "https://auth.aircall.io/login?redirect=https%3A%2F%2Fdashboard.aircall.io%2F",
	}, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	var storedURL string
	if err := db.QueryRow(`SELECT url FROM Sources WHERE source_id = ?`, sourceID).Scan(&storedURL); err != nil {
		t.Fatalf("select stored source URL: %v", err)
	}
	if want := "https://auth.aircall.io/login?redirect=https://dashboard.aircall.io/"; storedURL != want {
		t.Fatalf("stored URL = %q, want %q", storedURL, want)
	}
}

func assertSourceRow(t *testing.T, db *sql.DB, id uint64, expectedName, expectedPriority string, expectedCategoryID, expectedUsrID, expectedRestricted, expectedFlags uint64, expectedDisabled bool, expectedStatus string) {
	t.Helper()

	var name, priority, status string
	var categoryID, usrID, restricted, flags uint64
	var disabled bool
	var config sql.NullString
	err := db.QueryRow(`
		SELECT name, priority, category_id, usr_id, restricted, flags, disabled, status, config
		FROM Sources
		WHERE source_id = ?`, id).Scan(&name, &priority, &categoryID, &usrID, &restricted, &flags, &disabled, &status, &config)
	if err != nil {
		t.Fatalf("select source row: %v", err)
	}
	if name != expectedName || priority != expectedPriority || categoryID != expectedCategoryID || usrID != expectedUsrID || restricted != expectedRestricted || flags != expectedFlags || disabled != expectedDisabled || status != expectedStatus {
		t.Fatalf("unexpected source row: name=%q priority=%q category_id=%d usr_id=%d restricted=%d flags=%d disabled=%t status=%q", name, priority, categoryID, usrID, restricted, flags, disabled, status)
	}
	if !config.Valid || !json.Valid([]byte(config.String)) {
		t.Fatalf("expected valid JSON config, got %q", config.String)
	}
}

func TestSourceStatusHelpersNormalizeURLAndListStatuses(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_uid VARCHAR(64) NOT NULL DEFAULT '',
			url TEXT NOT NULL UNIQUE,
			status TEXT,
			priority TEXT,
			engine TEXT,
			created_at TEXT,
			last_updated_at TEXT,
			last_crawled_at TEXT,
			last_error TEXT,
			last_error_at TEXT,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		)`)
	if err != nil {
		t.Fatalf("create Sources table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE EmailMailboxState (
			source_id INTEGER NOT NULL,
			cursor_token TEXT NOT NULL DEFAULT '',
			cursor_history_id TEXT NOT NULL DEFAULT '0',
			cursor_uid INTEGER NOT NULL DEFAULT 0,
			cursor_uid_validity INTEGER NOT NULL DEFAULT 0,
			message_status TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			last_reconciled_at TEXT,
			listener_healthy_at TEXT,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			last_updated_at TEXT
		);
		CREATE TABLE EmailMessageState (
			source_id INTEGER NOT NULL,
			disposition TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create email status tables: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO Sources
			(url, status, priority, engine, created_at, last_updated_at, last_crawled_at, last_error, last_error_at, restricted, disabled, flags, config)
		VALUES
			('https://example.test/path', 'new', 'high', 'engine-a', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z', '', '', 2, FALSE, 7, '{}'),
			('https://other.test', 'processing', 'low', 'engine-b', '2026-01-04T00:00:00Z', '2026-01-05T00:00:00Z', '2026-01-06T00:00:00Z', 'boom', '2026-01-07T00:00:00Z', 1, TRUE, 8, '{}')`)
	if err != nil {
		t.Fatalf("insert source statuses: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO EmailMailboxState
			(source_id, cursor_token, cursor_history_id, cursor_uid, cursor_uid_validity, message_status, last_error, last_reconciled_at, listener_healthy_at, active, last_updated_at)
		VALUES
			(1, 'opaque-secret-cursor', '0', 0, 0, 'completed', '', '2026-01-08T00:00:00Z', '2026-01-08T00:01:00Z', TRUE, '2026-01-08T00:01:00Z'),
			(1, '', '99', 0, 0, 'retryable_failure', 'authentication failed for mailbox@example.test', '2026-01-09T00:00:00Z', NULL, TRUE, '2026-01-09T00:01:00Z');
		INSERT INTO EmailMessageState (source_id, disposition) VALUES
			(1, 'indexed'), (1, 'deleted'), (1, 'skipped'), (1, 'quarantined')`)
	if err != nil {
		t.Fatalf("insert email statuses: %v", err)
	}

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	matches, err := GetSourceStatusByURL(&handler, " HTTPS://EXAMPLE.TEST/PATH/ ")
	if err != nil {
		t.Fatalf("get source status by URL: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one normalized URL match, got %d", len(matches))
	}
	if !matches[0].URL.Valid || matches[0].URL.String != "https://example.test/path" {
		t.Fatalf("unexpected URL match: %#v", matches[0].URL)
	}
	if matches[0].Flags != 7 {
		t.Fatalf("unexpected flags for matched status: %d", matches[0].Flags)
	}
	emailStatus := matches[0].EmailStatus
	if emailStatus == nil {
		t.Fatal("expected email status for source with mailbox state")
	}
	if emailStatus.ListenerStatus != "degraded" || !emailStatus.LastSynchronizedAt.Valid || emailStatus.LastSynchronizedAt.String != "2026-01-09T00:00:00Z" {
		t.Fatalf("unexpected listener/synchronization status: %#v", emailStatus)
	}
	if emailStatus.MailboxCount != 2 || emailStatus.CheckpointedMailboxes != 2 || !emailStatus.HasTokenCursor || !emailStatus.HasHistoryCursor || emailStatus.HasUIDCursor {
		t.Fatalf("unexpected safe cursor summary: %#v", emailStatus)
	}
	if emailStatus.ProcessedCount != 3 || emailStatus.FailedCount != 1 || emailStatus.LastErrorCategory != "transient" {
		t.Fatalf("unexpected email outcome summary: %#v", emailStatus)
	}

	statuses, err := ListSourceStatuses(&handler)
	if err != nil {
		t.Fatalf("list source statuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected two statuses, got %d", len(statuses))
	}
	for _, status := range statuses {
		if status.SourceID == 2 && status.EmailStatus != nil {
			t.Fatalf("expected absent email status for source without mailbox state, got %#v", status.EmailStatus)
		}
	}
}

func TestSourceGetSourceTypeSupportsEmailWithoutChangingDefaults(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name:     "email source",
			config:   `{"crawling_config":{"source_type":" Email "}}`,
			expected: cfg.SourceTypeEmail,
		},
		{
			name:     "legacy missing source type",
			config:   `{"crawling_config":{}}`,
			expected: "website",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(tt.config)
			source := Source{Config: &raw}
			if actual := source.GetSourceType(); actual != tt.expected {
				t.Fatalf("unexpected source type: got %q, want %q", actual, tt.expected)
			}
		})
	}
}

func TestValidateURLSupportsEmailSources(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		{name: "IMAP", url: "imap://mail.example.test:143/INBOX", valid: true},
		{name: "secure IMAP", url: "imaps://mail.example.test:993/Archive", valid: true},
		{name: "invalid IMAP", url: "imap://", valid: false},
		{name: "POP3", url: "pop3://mail.example.test:110", valid: true},
		{name: "secure POP3", url: "pop3s://mail.example.test:995", valid: true},
		{name: "invalid POP3", url: "pop3s://", valid: false},
		{name: "Gmail", url: "gmail://user@example.com", valid: true},
		{name: "invalid Gmail", url: "gmail://", valid: false},
		{name: "Graph Mail", url: "graph-mail://tenant/mailbox", valid: true},
		{name: "invalid Graph Mail", url: "graph-mail://", valid: false},
		{name: "Maildir", url: "maildir:///var/mail/user", valid: true},
		{name: "invalid Maildir", url: "maildir://relative/path", valid: false},
		{name: "Mbox", url: "mbox:///var/mail/user.mbox", valid: true},
		{name: "invalid Mbox", url: "mbox:///", valid: false},
		{name: "generic email", url: "email://mail.example.test", valid: true},
		{name: "invalid generic email", url: "email://", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)
			if tt.valid && err != nil {
				t.Fatalf("validateURL(%q) returned error: %v", tt.url, err)
			}
			if !tt.valid && err == nil {
				t.Fatalf("validateURL(%q) unexpectedly succeeded", tt.url)
			}
		})
	}
}
