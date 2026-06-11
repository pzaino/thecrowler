package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	db "github.com/pzaino/thecrowler/pkg/database"
)

const (
	maxMailProviderLength       = 64
	maxMailAccountKeyLength     = 191
	maxMailMailboxKeyLength     = 191
	maxMailSubscriptionIDLength = 191
	maxMailResourcePathLength   = 2048
	maxMailErrorLength          = 2048
)

// DatabaseStateStore persists mailbox checkpoints in EmailMailboxState using
// optimistic compare-and-swap updates. The handler is shared with the rest of
// the database package; the store does not own or close it.
type DatabaseStateStore struct {
	db   db.Handler
	dbms string
}

// SQLStateStore is a compatibility alias for DatabaseStateStore.
type SQLStateStore = DatabaseStateStore

// NewDatabaseStateStore creates a durable StateStore backed by handler.
func NewDatabaseStateStore(handler db.Handler) (*DatabaseStateStore, error) {
	if handler == nil {
		return nil, errors.New("mail state store database handler is nil")
	}

	dbms := normalizeMailDBMS(handler.DBMS())
	switch dbms {
	case db.DBPostgresStr, db.DBSQLiteStr, db.DBMySQLStr:
	default:
		return nil, fmt.Errorf("unsupported database type for mail state: %s", handler.DBMS())
	}

	return &DatabaseStateStore{db: handler, dbms: dbms}, nil
}

// NewSQLStateStore creates a durable StateStore backed by handler.
func NewSQLStateStore(handler db.Handler) (*SQLStateStore, error) {
	return NewDatabaseStateStore(handler)
}

// LoadCheckpoint returns the committed checkpoint for key. Missing mailbox
// state is represented by the zero checkpoint, matching MemoryStateStore.
func (s *DatabaseStateStore) LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error) {
	identity, err := validateDatabaseMailboxKey(key)
	if err != nil {
		return Checkpoint{}, err
	}
	if err = ctx.Err(); err != nil {
		return Checkpoint{}, err
	}

	query := `SELECT cursor_token, cursor_history_id, cursor_uid, cursor_uid_validity, message_status,
		content_hash, error_count, last_error, watch_subscription_id, watch_resource_path,
		watch_renewal_status, watch_last_renewed_at, watch_expires_at, watch_last_attempt_at, watch_renewal_failure_count, watch_renewal_last_error, version
		FROM EmailMailboxState
		WHERE source_id = ` + mailPlaceholder(s.dbms, 1) + `
		  AND provider = ` + mailPlaceholder(s.dbms, 2) + `
		  AND account_key = ` + mailPlaceholder(s.dbms, 3) + `
		  AND mailbox_key = ` + mailPlaceholder(s.dbms, 4)

	rows, err := s.db.QueryContext(ctx, query, identity.args()...)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("load mail checkpoint: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return Checkpoint{}, fmt.Errorf("load mail checkpoint: %w", err)
		}
		return Checkpoint{}, nil
	}

	checkpoint, err := scanCheckpoint(rows.Scan)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("scan mail checkpoint: %w", err)
	}
	if rows.Next() {
		return Checkpoint{}, errors.New("load mail checkpoint: duplicate mailbox state rows")
	}
	if err = rows.Err(); err != nil {
		return Checkpoint{}, fmt.Errorf("load mail checkpoint: %w", err)
	}
	return checkpoint, nil
}

// CommitCheckpoint atomically inserts or replaces a checkpoint only when
// previousVersion matches the committed version. The database assigns a
// monotonically increasing decimal version and ignores next.Version.
func (s *DatabaseStateStore) CommitCheckpoint(ctx context.Context, key MailboxKey, previousVersion string, next Checkpoint) (err error) {
	identity, err := validateDatabaseMailboxKey(key)
	if err != nil {
		return err
	}
	if err = validateCheckpoint(next); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin mail checkpoint transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	current, exists, err := s.loadCheckpointTx(ctx, tx, identity)
	if err != nil {
		return err
	}
	if current.Version != previousVersion {
		return checkpointConflict(key, current.Version, previousVersion)
	}
	if err = validateCheckpointStatusTransition(current.MessageStatus, next.MessageStatus, exists); err != nil {
		return err
	}

	if !exists {
		if previousVersion != "" {
			return checkpointConflict(key, "", previousVersion)
		}
		err = s.insertCheckpointTx(ctx, tx, identity, next)
	} else {
		err = s.updateCheckpointTx(ctx, tx, identity, previousVersion, next)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit mail checkpoint transaction: %w", err)
	}
	return nil
}

type databaseMailboxIdentity struct {
	sourceID   uint64
	provider   string
	accountKey string
	mailboxKey string
}

func (i databaseMailboxIdentity) args() []interface{} {
	return []interface{}{i.sourceID, i.provider, i.accountKey, i.mailboxKey}
}

func validateDatabaseMailboxKey(key MailboxKey) (databaseMailboxIdentity, error) {
	source := strings.TrimSpace(key.SourceID)
	sourceID, err := strconv.ParseUint(source, 10, 64)
	if err != nil || sourceID == 0 {
		return databaseMailboxIdentity{}, fmt.Errorf("mailbox source_id %q must be a positive database source ID", key.SourceID)
	}
	provider := strings.ToLower(strings.TrimSpace(key.Provider))
	account := strings.TrimSpace(key.AccountID)
	mailbox := strings.TrimSpace(mailboxIdentity(key.Mailbox))
	if provider == "" || len(provider) > maxMailProviderLength {
		return databaseMailboxIdentity{}, fmt.Errorf("mailbox provider must contain 1-%d bytes", maxMailProviderLength)
	}
	if account == "" || len(account) > maxMailAccountKeyLength {
		return databaseMailboxIdentity{}, fmt.Errorf("mailbox account key must contain 1-%d bytes", maxMailAccountKeyLength)
	}
	if mailbox == "" || len(mailbox) > maxMailMailboxKeyLength {
		return databaseMailboxIdentity{}, fmt.Errorf("mailbox key must contain 1-%d bytes", maxMailMailboxKeyLength)
	}
	return databaseMailboxIdentity{sourceID: sourceID, provider: provider, accountKey: account, mailboxKey: mailbox}, nil
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if checkpoint.MessageStatus != "" && !checkpoint.MessageStatus.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidMessageStatus, checkpoint.MessageStatus)
	}
	if len(checkpoint.LastError) > maxMailErrorLength {
		return fmt.Errorf("mail checkpoint last error exceeds %d bytes", maxMailErrorLength)
	}
	if len(checkpoint.Renewal.SubscriptionID) > maxMailSubscriptionIDLength {
		return fmt.Errorf("mail checkpoint subscription ID exceeds %d bytes", maxMailSubscriptionIDLength)
	}
	if len(checkpoint.Renewal.ResourcePath) > maxMailResourcePathLength {
		return fmt.Errorf("mail checkpoint resource path exceeds %d bytes", maxMailResourcePathLength)
	}
	if checkpoint.Renewal.Status != "" && !checkpoint.Renewal.Status.Valid() {
		return fmt.Errorf("mail checkpoint renewal status is invalid: %q", checkpoint.Renewal.Status)
	}
	if len(checkpoint.Renewal.LastError) > maxMailErrorLength {
		return fmt.Errorf("mail checkpoint renewal error exceeds %d bytes", maxMailErrorLength)
	}
	return nil
}

func validateCheckpointStatusTransition(current, next MessageStatus, exists bool) error {
	if next == "" || current == next {
		return nil
	}
	if !exists || current == "" {
		if !next.Valid() {
			return fmt.Errorf("%w: %q", ErrInvalidMessageStatus, next)
		}
		return nil
	}
	return ValidateMessageStatusTransition(current, next)
}

func (s *DatabaseStateStore) loadCheckpointTx(ctx context.Context, tx *sql.Tx, identity databaseMailboxIdentity) (Checkpoint, bool, error) {
	query := `SELECT cursor_token, cursor_history_id, cursor_uid, cursor_uid_validity, message_status,
		content_hash, error_count, last_error, watch_subscription_id, watch_resource_path,
		watch_renewal_status, watch_last_renewed_at, watch_expires_at, watch_last_attempt_at, watch_renewal_failure_count, watch_renewal_last_error, version
		FROM EmailMailboxState
		WHERE source_id = ` + mailPlaceholder(s.dbms, 1) + `
		  AND provider = ` + mailPlaceholder(s.dbms, 2) + `
		  AND account_key = ` + mailPlaceholder(s.dbms, 3) + `
		  AND mailbox_key = ` + mailPlaceholder(s.dbms, 4)
	if s.dbms == db.DBPostgresStr || s.dbms == db.DBMySQLStr {
		query += " FOR UPDATE"
	}

	checkpoint, err := scanCheckpoint(tx.QueryRowContext(ctx, query, identity.args()...).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("load mail checkpoint transactionally: %w", err)
	}
	return checkpoint, true, nil
}

func (s *DatabaseStateStore) insertCheckpointTx(ctx context.Context, tx *sql.Tx, identity databaseMailboxIdentity, next Checkpoint) error {
	query := `INSERT INTO EmailMailboxState (
		source_id, provider, account_key, mailbox_key, cursor_token, cursor_history_id,
		cursor_uid, cursor_uid_validity, message_status, content_hash,
		error_count, last_error, watch_subscription_id, watch_resource_path, watch_renewal_status,
		watch_last_renewed_at, watch_expires_at, watch_last_attempt_at,
		watch_renewal_failure_count, watch_renewal_last_error, version
	) VALUES (` + mailPlaceholders(s.dbms, 21) + `)`
	args := checkpointArgs(identity, next, 1)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if isUniqueConstraintError(err) {
			return checkpointConflictFromIdentity(identity, "", "")
		}
		return fmt.Errorf("insert mail checkpoint: %w", err)
	}
	return nil
}

func (s *DatabaseStateStore) updateCheckpointTx(ctx context.Context, tx *sql.Tx, identity databaseMailboxIdentity, previousVersion string, next Checkpoint) error {
	version, err := strconv.ParseUint(previousVersion, 10, 64)
	if err != nil {
		return checkpointConflictFromIdentity(identity, "", previousVersion)
	}
	query := `UPDATE EmailMailboxState SET
		cursor_token = ` + mailPlaceholder(s.dbms, 1) + `,
		cursor_history_id = ` + mailPlaceholder(s.dbms, 2) + `,
		cursor_uid = ` + mailPlaceholder(s.dbms, 3) + `,
		cursor_uid_validity = ` + mailPlaceholder(s.dbms, 4) + `,
		message_status = ` + mailPlaceholder(s.dbms, 5) + `,
		content_hash = ` + mailPlaceholder(s.dbms, 6) + `,
		error_count = ` + mailPlaceholder(s.dbms, 7) + `,
		last_error = ` + mailPlaceholder(s.dbms, 8) + `,
		watch_subscription_id = ` + mailPlaceholder(s.dbms, 9) + `,
		watch_resource_path = ` + mailPlaceholder(s.dbms, 10) + `,
		watch_renewal_status = ` + mailPlaceholder(s.dbms, 11) + `,
		watch_last_renewed_at = ` + mailPlaceholder(s.dbms, 12) + `,
		watch_expires_at = ` + mailPlaceholder(s.dbms, 13) + `,
		watch_last_attempt_at = ` + mailPlaceholder(s.dbms, 14) + `,
		watch_renewal_failure_count = ` + mailPlaceholder(s.dbms, 15) + `,
		watch_renewal_last_error = ` + mailPlaceholder(s.dbms, 16) + `,
		version = version + 1,
		last_updated_at = CURRENT_TIMESTAMP
		WHERE source_id = ` + mailPlaceholder(s.dbms, 17) + `
		  AND provider = ` + mailPlaceholder(s.dbms, 18) + `
		  AND account_key = ` + mailPlaceholder(s.dbms, 19) + `
		  AND mailbox_key = ` + mailPlaceholder(s.dbms, 20) + `
		  AND version = ` + mailPlaceholder(s.dbms, 21)
	args := []interface{}{next.Cursor.Token, formatHistoryID(next.Cursor.HistoryID), next.Cursor.UID, next.Cursor.UIDValidity, nullableStatus(next.MessageStatus), next.ContentHash, next.ErrorCount, next.LastError, next.Renewal.SubscriptionID, next.Renewal.ResourcePath, string(next.Renewal.Status), nullableTime(next.Renewal.LastRenewedAt), nullableTime(next.Renewal.ExpiresAt), nullableTime(next.Renewal.LastAttemptAt), next.Renewal.FailureCount, next.Renewal.LastError, identity.sourceID, identity.provider, identity.accountKey, identity.mailboxKey, version}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update mail checkpoint: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read mail checkpoint update result: %w", err)
	}
	if rows != 1 {
		return checkpointConflictFromIdentity(identity, previousVersion, previousVersion)
	}
	return nil
}

func checkpointArgs(identity databaseMailboxIdentity, checkpoint Checkpoint, version uint64) []interface{} {
	return []interface{}{identity.sourceID, identity.provider, identity.accountKey, identity.mailboxKey, checkpoint.Cursor.Token, formatHistoryID(checkpoint.Cursor.HistoryID), checkpoint.Cursor.UID, checkpoint.Cursor.UIDValidity, nullableStatus(checkpoint.MessageStatus), checkpoint.ContentHash, checkpoint.ErrorCount, checkpoint.LastError, checkpoint.Renewal.SubscriptionID, checkpoint.Renewal.ResourcePath, string(checkpoint.Renewal.Status), nullableTime(checkpoint.Renewal.LastRenewedAt), nullableTime(checkpoint.Renewal.ExpiresAt), nullableTime(checkpoint.Renewal.LastAttemptAt), checkpoint.Renewal.FailureCount, checkpoint.Renewal.LastError, version}
}

func formatHistoryID(historyID uint64) string {
	return strconv.FormatUint(historyID, 10)
}

func nullableTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableStatus(status MessageStatus) interface{} {
	if status == "" {
		return nil
	}
	return string(status)
}

func scanCheckpoint(scan func(...interface{}) error) (Checkpoint, error) {
	var (
		checkpoint    Checkpoint
		status        sql.NullString
		historyID     string
		version       uint64
		lastRenewedAt sql.NullTime
		expiresAt     sql.NullTime
		lastAttemptAt sql.NullTime
		renewalStatus sql.NullString
	)
	err := scan(&checkpoint.Cursor.Token, &historyID, &checkpoint.Cursor.UID, &checkpoint.Cursor.UIDValidity, &status, &checkpoint.ContentHash, &checkpoint.ErrorCount, &checkpoint.LastError, &checkpoint.Renewal.SubscriptionID, &checkpoint.Renewal.ResourcePath, &renewalStatus, &lastRenewedAt, &expiresAt, &lastAttemptAt, &checkpoint.Renewal.FailureCount, &checkpoint.Renewal.LastError, &version)
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint.Cursor.HistoryID, err = strconv.ParseUint(historyID, 10, 64)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("invalid stored mail history ID %q: %w", historyID, err)
	}
	if status.Valid {
		checkpoint.MessageStatus = MessageStatus(status.String)
	}
	if renewalStatus.Valid {
		checkpoint.Renewal.Status = RenewalStatus(renewalStatus.String)
	}
	if lastRenewedAt.Valid {
		checkpoint.Renewal.LastRenewedAt = lastRenewedAt.Time.UTC()
	}
	if expiresAt.Valid {
		checkpoint.Renewal.ExpiresAt = expiresAt.Time.UTC()
	}
	if lastAttemptAt.Valid {
		checkpoint.Renewal.LastAttemptAt = lastAttemptAt.Time.UTC()
	}
	checkpoint.Version = strconv.FormatUint(version, 10)
	return checkpoint, nil
}

func checkpointConflict(key MailboxKey, current, previous string) error {
	return fmt.Errorf("%w: source %q provider %q account %q mailbox %q has version %q, not %q", ErrCheckpointConflict, key.SourceID, key.Provider, key.AccountID, mailboxIdentity(key.Mailbox), current, previous)
}

func checkpointConflictFromIdentity(identity databaseMailboxIdentity, current, previous string) error {
	return fmt.Errorf("%w: source %d provider %q account %q mailbox %q has version %q, not %q", ErrCheckpointConflict, identity.sourceID, identity.provider, identity.accountKey, identity.mailboxKey, current, previous)
}

func mailPlaceholder(dbms string, position int) string {
	if dbms == db.DBPostgresStr {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func mailPlaceholders(dbms string, count int) string {
	placeholders := make([]string, count)
	for i := range placeholders {
		placeholders[i] = mailPlaceholder(dbms, i+1)
	}
	return strings.Join(placeholders, ", ")
}

func normalizeMailDBMS(dbms string) string {
	normalized := strings.ToLower(strings.TrimSpace(dbms))
	switch normalized {
	case "postgresql", "pgx":
		return db.DBPostgresStr
	case "sqlite":
		return db.DBSQLiteStr
	case "mariadb":
		return db.DBMySQLStr
	default:
		return normalized
	}
}

func isUniqueConstraintError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "duplicate entry")
}

var _ StateStore = (*DatabaseStateStore)(nil)
