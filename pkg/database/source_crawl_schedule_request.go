package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CrawlOperationType is the only operation accepted by the WP-03B1
// scheduling foundation. It is not exposed as a model-facing tool.
const CrawlOperationType = "crawl"

// CrawlOperationState describes the durable scheduling request. It is
// deliberately separate from the source's observed crawl execution state.
type CrawlOperationState string

const (
	CrawlOperationProposed       CrawlOperationState = "proposed"
	CrawlOperationDenied         CrawlOperationState = "denied"
	CrawlOperationAccepted       CrawlOperationState = "accepted"
	CrawlOperationAlreadyPending CrawlOperationState = "already_pending"
	CrawlOperationProcessing     CrawlOperationState = "processing"
	CrawlOperationCompleted      CrawlOperationState = "completed"
	CrawlOperationFailed         CrawlOperationState = "failed"
	CrawlOperationCancelled      CrawlOperationState = "cancelled"
	CrawlOperationExpired        CrawlOperationState = "expired"
	CrawlOperationDisabled       CrawlOperationState = "disabled"
	CrawlOperationNotFound       CrawlOperationState = "not_found"
	CrawlOperationConflict       CrawlOperationState = "conflict"
	CrawlOperationUnsupported    CrawlOperationState = "unsupported"
)

// CrawlSourceState describes the source row observed during scheduling. It is
// evidence of state, not proof that a particular crawl completed.
type CrawlSourceState string

const (
	CrawlSourceUnknown    CrawlSourceState = "unknown"
	CrawlSourceNew        CrawlSourceState = "new"
	CrawlSourcePending    CrawlSourceState = "pending"
	CrawlSourceProcessing CrawlSourceState = "processing"
	CrawlSourceCompleted  CrawlSourceState = "completed"
	CrawlSourceError      CrawlSourceState = "error"
	CrawlSourceDisabled   CrawlSourceState = "disabled"
	CrawlSourceMissing    CrawlSourceState = "missing"
)

// Stable scheduling reason codes. They are safe for audit and caller logic;
// they never contain database errors or configuration data.
const (
	CrawlReasonScheduled           = "crawl_scheduled"
	CrawlReasonAlreadyPending      = "crawl_already_pending"
	CrawlReasonProcessing          = "crawl_processing"
	CrawlReasonDisabled            = "crawl_source_disabled"
	CrawlReasonNotFound            = "crawl_source_not_found"
	CrawlReasonExpired             = "crawl_request_expired"
	CrawlReasonConflict            = "crawl_request_conflict"
	CrawlReasonInvalid             = "crawl_request_invalid"
	CrawlReasonCancelled           = "crawl_request_cancelled"
	CrawlReasonTransactionTimeout  = "crawl_transaction_timeout"
	CrawlReasonDatabase            = "crawl_database_error"
	CrawlReasonUnsupported         = "crawl_store_unsupported"
	CrawlReasonIncompleteOperation = "crawl_operation_incomplete"
	CrawlReasonBudgetExhausted     = "crawl_request_budget_exhausted"
	CrawlReasonSourceScopeRequired = "crawl_source_scope_required"
	CrawlReasonSourceOutOfScope    = "crawl_source_out_of_scope"
)

var (
	// ErrCrawlSchedulingUnsupported means the backend has no durable operation
	// store. The foundation fails closed instead of pretending to be idempotent.
	ErrCrawlSchedulingUnsupported = errors.New("durable crawl scheduling is unsupported by this database backend")
	// ErrCrawlSchedulingFailed is a sanitized operational failure. Underlying
	// database errors are deliberately not returned to callers or models.
	ErrCrawlSchedulingFailed = errors.New("crawl scheduling failed")
)

// CrawlScheduleRequest is the trusted internal request for an idempotent
// scheduling operation. Approval fields must be produced by the trusted
// proposal/approval layer; they are never model arguments.
type CrawlScheduleRequest struct {
	RequestID        string
	AgentID          string
	Owner            string
	SourceID         uint64
	AllowedSources   []uint64
	OperationType    string
	ParametersSHA256 string
	ApprovalID       string
	ApprovalSHA256   string
	ApprovedBy       string
	ApprovedAt       time.Time
	ExpiresAt        time.Time
	MaxRequests      int
	BudgetWindow     time.Duration
	Now              time.Time
}

// CrawlScheduleResult is the structured scheduling outcome.
type CrawlScheduleResult struct {
	OperationID      string
	RequestID        string
	AgentID          string
	Owner            string
	SourceID         uint64
	AllowedSources   []uint64
	OperationType    string
	State            CrawlOperationState
	ReasonCode       string
	SourceState      CrawlSourceState
	SourceObservedAt time.Time
	ExpiresAt        time.Time
	Reused           bool
}

type crawlOperationDetails struct {
	RequestID         string `json:"request_id"`
	AgentID           string `json:"agent_id"`
	Owner             string `json:"owner"`
	SourceID          uint64 `json:"source_id"`
	OperationType     string `json:"operation_type"`
	ParametersSHA256  string `json:"parameters_sha256"`
	ApprovalID        string `json:"approval_id,omitempty"`
	ApprovalSHA256    string `json:"approval_sha256"`
	ApprovedBy        string `json:"approved_by,omitempty"`
	ApprovedAt        string `json:"approved_at,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	SourceStateBefore string `json:"source_state_before,omitempty"`
	ReasonCode        string `json:"reason_code,omitempty"`
}

type crawlOperationRow struct {
	Status  string
	Details crawlOperationDetails
}

// ScheduleSourceCrawlRequest performs an idempotent, approved scheduling
// request. It only marks an eligible source pending; it never waits for a
// crawler, starts a crawler, or reports crawl completion.
func ScheduleSourceCrawlRequest(ctx context.Context, db *Handler, req CrawlScheduleRequest) (CrawlScheduleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := req.Now.UTC()
	if req.Now.IsZero() {
		now = time.Now().UTC()
	}
	req.Now = now
	result := newCrawlScheduleResult(req, "")
	if err := validateCrawlScheduleRequest(req); err != nil {
		result.State = CrawlOperationDenied
		result.ReasonCode = CrawlReasonInvalid
		return result, nil
	}
	if len(req.AllowedSources) == 0 {
		result.State = CrawlOperationDenied
		result.ReasonCode = CrawlReasonSourceScopeRequired
		return result, nil
	}
	if !crawlSourceAllowed(req.AllowedSources, req.SourceID) {
		result.State = CrawlOperationDenied
		result.ReasonCode = CrawlReasonSourceOutOfScope
		return result, nil
	}
	if db == nil || *db == nil {
		result.State = CrawlOperationFailed
		result.ReasonCode = CrawlReasonDatabase
		return result, ErrCrawlSchedulingFailed
	}
	if !supportsCrawlOperationStore(*db) {
		result.State = CrawlOperationUnsupported
		result.ReasonCode = CrawlReasonUnsupported
		return result, ErrCrawlSchedulingUnsupported
	}
	if err := ctx.Err(); err != nil {
		result.State = CrawlOperationCancelled
		result.ReasonCode = CrawlReasonCancelled
		return result, err
	}
	if req.ExpiresAt.IsZero() || !req.ExpiresAt.After(now) {
		result.State = CrawlOperationExpired
		result.ReasonCode = CrawlReasonExpired
		return result, nil
	}

	key := crawlOperationID(req)
	result.OperationID = key
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		result.State = CrawlOperationFailed
		result.ReasonCode = CrawlReasonDatabase
		return result, ErrCrawlSchedulingFailed
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := readCrawlOperation(txCtx, tx, *db, key)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	if found {
		return resolveExistingCrawlOperation(txCtx, tx, *db, req, result, existing)
	}
	if err := lockCrawlSource(txCtx, tx, *db, req.SourceID); err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	withinBudget, err := crawlRequestWithinBudget(txCtx, tx, *db, req)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	if !withinBudget {
		result.State = CrawlOperationDenied
		result.ReasonCode = CrawlReasonBudgetExhausted
		return result, nil
	}

	details, err := crawlOperationDetailsFor(req)
	if err != nil {
		result.State = CrawlOperationFailed
		result.ReasonCode = CrawlReasonDatabase
		return result, ErrCrawlSchedulingFailed
	}
	inserted, err := insertCrawlOperation(txCtx, tx, *db, key, details)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	if !inserted {
		existing, found, err = readCrawlOperation(txCtx, tx, *db, key)
		if err != nil {
			return crawlOperationFailure(result, txCtx, err)
		}
		if !found {
			result.State = CrawlOperationFailed
			result.ReasonCode = CrawlReasonIncompleteOperation
			return result, ErrCrawlSchedulingFailed
		}
		return resolveExistingCrawlOperation(txCtx, tx, *db, req, result, existing)
	}

	sourceState, disabled, found, err := readCrawlSourceState(txCtx, tx, *db, req.SourceID)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	if !found {
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationNotFound, CrawlReasonNotFound, CrawlSourceMissing)
	}
	if disabled {
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationDisabled, CrawlReasonDisabled, CrawlSourceDisabled)
	}
	switch sourceState {
	case CrawlSourceProcessing:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationProcessing, CrawlReasonProcessing, sourceState)
	case CrawlSourcePending:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationAlreadyPending, CrawlReasonAlreadyPending, sourceState)
	}

	updated, err := markCrawlSourcePending(txCtx, tx, *db, req.SourceID)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	if updated {
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationAccepted, CrawlReasonScheduled, CrawlSourcePending)
	}

	// A concurrent request changed the source between the observation and the
	// conditional update. Re-read and report the actual unchanged state.
	sourceState, disabled, found, err = readCrawlSourceState(txCtx, tx, *db, req.SourceID)
	if err != nil {
		return crawlOperationFailure(result, txCtx, err)
	}
	switch {
	case !found:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationNotFound, CrawlReasonNotFound, CrawlSourceMissing)
	case disabled:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationDisabled, CrawlReasonDisabled, CrawlSourceDisabled)
	case sourceState == CrawlSourceProcessing:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationProcessing, CrawlReasonProcessing, sourceState)
	case sourceState == CrawlSourcePending:
		return finishCrawlOperation(txCtx, tx, *db, req, result, CrawlOperationAlreadyPending, CrawlReasonAlreadyPending, sourceState)
	default:
		result.State = CrawlOperationFailed
		result.ReasonCode = CrawlReasonIncompleteOperation
		return result, ErrCrawlSchedulingFailed
	}
}

func newCrawlScheduleResult(req CrawlScheduleRequest, key string) CrawlScheduleResult {
	return CrawlScheduleResult{
		OperationID:      key,
		RequestID:        req.RequestID,
		AgentID:          req.AgentID,
		Owner:            req.Owner,
		SourceID:         req.SourceID,
		OperationType:    req.OperationType,
		SourceState:      CrawlSourceUnknown,
		SourceObservedAt: req.Now,
		ExpiresAt:        req.ExpiresAt,
	}
}

func validateCrawlScheduleRequest(req CrawlScheduleRequest) error {
	for field, value := range map[string]string{
		"request_id": req.RequestID, "agent_id": req.AgentID,
		"owner": req.Owner, "approval_id": req.ApprovalID,
		"approved_by": req.ApprovedBy,
	} {
		if err := validateCrawlIdentifier(field, value, 128); err != nil {
			return err
		}
	}
	if req.SourceID == 0 {
		return fmt.Errorf("source_id is required")
	}
	if req.OperationType != CrawlOperationType {
		return fmt.Errorf("operation_type must be %q", CrawlOperationType)
	}
	if !validSHA256Hex(req.ParametersSHA256) || !validSHA256Hex(req.ApprovalSHA256) {
		return fmt.Errorf("parameters_sha256 and approval_sha256 must be lowercase SHA-256 digests")
	}
	if req.ApprovedAt.IsZero() || req.ApprovedAt.After(req.Now) {
		return fmt.Errorf("approved_at must be a valid past timestamp")
	}
	if req.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	if req.MaxRequests < 0 {
		return fmt.Errorf("max_requests must be non-negative")
	}
	if req.MaxRequests > 0 && req.BudgetWindow <= 0 {
		return fmt.Errorf("budget_window must be positive when max_requests is set")
	}
	return nil
}

func crawlSourceAllowed(allowed []uint64, sourceID uint64) bool {
	for _, id := range allowed {
		if id == sourceID {
			return true
		}
	}
	return false
}

func validateCrawlIdentifier(field, value string, max int) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return fmt.Errorf("%s must be 1..%d characters", field, max)
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains an invalid character", field)
		}
	}
	return nil
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func crawlOperationID(req CrawlScheduleRequest) string {
	h := sha256.New()
	for _, part := range []string{
		req.Owner, req.AgentID, strconv.FormatUint(req.SourceID, 10),
		req.OperationType, req.RequestID,
	} {
		_, _ = fmt.Fprintf(h, "%d:%s|", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func crawlOperationDetailsFor(req CrawlScheduleRequest) (crawlOperationDetails, error) {
	details := crawlOperationDetails{
		RequestID: req.RequestID, AgentID: req.AgentID, Owner: req.Owner,
		SourceID: req.SourceID, OperationType: req.OperationType,
		ParametersSHA256: req.ParametersSHA256, ApprovalID: req.ApprovalID,
		ApprovalSHA256: req.ApprovalSHA256, ApprovedBy: req.ApprovedBy,
		ExpiresAt: req.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if !req.ApprovedAt.IsZero() {
		details.ApprovedAt = req.ApprovedAt.UTC().Format(time.RFC3339Nano)
	}
	return details, nil
}

func supportsCrawlOperationStore(db Handler) bool {
	switch normalizeInformationSeedDBMS(db.DBMS()) {
	case DBPostgresStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

func crawlOperationFailure(result CrawlScheduleResult, ctx context.Context, err error) (CrawlScheduleResult, error) {
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.State = CrawlOperationCancelled
			result.ReasonCode = CrawlReasonTransactionTimeout
		} else {
			result.State = CrawlOperationCancelled
			result.ReasonCode = CrawlReasonCancelled
		}
		return result, ctx.Err()
	}
	result.State = CrawlOperationFailed
	result.ReasonCode = CrawlReasonDatabase
	return result, ErrCrawlSchedulingFailed
}

func resolveExistingCrawlOperation(ctx context.Context, tx *sql.Tx, db Handler, req CrawlScheduleRequest, result CrawlScheduleResult, existing crawlOperationRow) (CrawlScheduleResult, error) {
	if !sameCrawlOperation(existing.Details, req) {
		result.State = CrawlOperationConflict
		result.ReasonCode = CrawlReasonConflict
		return result, nil
	}
	state := CrawlOperationState(existing.Status)
	if state == "" || state == "scheduling" {
		state = CrawlOperationFailed
		result.ReasonCode = CrawlReasonIncompleteOperation
	} else {
		result.ReasonCode = existing.Details.ReasonCode
	}
	result.State = state
	result.Reused = true
	result.ExpiresAt = parseCrawlTime(existing.Details.ExpiresAt)
	sourceState, disabled, found, err := readCrawlSourceState(ctx, tx, db, req.SourceID)
	if err != nil {
		return crawlOperationFailure(result, ctx, err)
	}
	switch {
	case !found:
		result.SourceState = CrawlSourceMissing
	case disabled:
		result.SourceState = CrawlSourceDisabled
	default:
		result.SourceState = sourceState
	}
	return result, nil
}

func sameCrawlOperation(details crawlOperationDetails, req CrawlScheduleRequest) bool {
	return details.RequestID == req.RequestID &&
		details.AgentID == req.AgentID &&
		details.Owner == req.Owner &&
		details.SourceID == req.SourceID &&
		details.OperationType == req.OperationType &&
		details.ParametersSHA256 == req.ParametersSHA256 &&
		details.ApprovalSHA256 == req.ApprovalSHA256
}

func parseCrawlTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func finishCrawlOperation(ctx context.Context, tx *sql.Tx, db Handler, req CrawlScheduleRequest, result CrawlScheduleResult, state CrawlOperationState, reason string, sourceState CrawlSourceState) (CrawlScheduleResult, error) {
	result.State = state
	result.ReasonCode = reason
	result.SourceState = sourceState
	result.SourceObservedAt = req.Now
	details, err := crawlOperationDetailsFor(req)
	if err != nil {
		return result, ErrCrawlSchedulingFailed
	}
	details.SourceStateBefore = string(sourceState)
	details.ReasonCode = reason
	if err := updateCrawlOperation(ctx, tx, db, result.OperationID, state, details); err != nil {
		return crawlOperationFailure(result, ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return crawlOperationFailure(result, ctx, err)
	}
	return result, nil
}

// lockCrawlSource serializes scheduling decisions for one source on
// PostgreSQL, so budget checks and conditional updates cannot race. SQLite
// already serializes writes through its transaction.
func lockCrawlSource(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64) error {
	query := `SELECT source_id FROM Sources WHERE source_id = ` + crawlPlaceholder(db, 1)
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	var id uint64
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func crawlRequestWithinBudget(ctx context.Context, tx *sql.Tx, db Handler, req CrawlScheduleRequest) (bool, error) {
	if req.MaxRequests <= 0 {
		return true, nil
	}
	query := `SELECT status, details FROM Status
		WHERE agent_id = ` + crawlPlaceholder(db, 1) + `
		  AND source_id = ` + crawlPlaceholder(db, 2) + `
		  AND operation_type = ` + crawlPlaceholder(db, 3) + `
		  AND created_at >= ` + crawlPlaceholder(db, 4) + `
		ORDER BY created_at DESC LIMIT ` + crawlPlaceholder(db, 5)
	rows, err := tx.QueryContext(ctx, query, req.AgentID, req.SourceID, req.OperationType,
		req.Now.Add(-req.BudgetWindow), req.MaxRequests+1)
	if err != nil {
		return false, err
	}
	defer rows.Close() //nolint:errcheck
	count := 0
	for rows.Next() {
		var status string
		var raw []byte
		if err := rows.Scan(&status, &raw); err != nil {
			return false, err
		}
		var details crawlOperationDetails
		if err := json.Unmarshal(raw, &details); err != nil {
			return false, err
		}
		if details.Owner != req.Owner {
			continue
		}
		switch CrawlOperationState(status) {
		case CrawlOperationAccepted, CrawlOperationAlreadyPending, CrawlOperationProcessing:
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return count < req.MaxRequests, nil
}

func readCrawlSourceState(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64) (CrawlSourceState, bool, bool, error) {
	query := `SELECT status, disabled FROM Sources WHERE source_id = ` + crawlPlaceholder(db, 1)
	var status sql.NullString
	var disabled bool
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(&status, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlSourceMissing, false, false, nil
	}
	if err != nil {
		return CrawlSourceUnknown, false, false, err
	}
	return crawlSourceStateFromStatus(status.String), disabled, true, nil
}

func crawlSourceStateFromStatus(status string) CrawlSourceState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new", "":
		return CrawlSourceNew
	case "pending":
		return CrawlSourcePending
	case "processing":
		return CrawlSourceProcessing
	case "completed":
		return CrawlSourceCompleted
	case "error":
		return CrawlSourceError
	default:
		return CrawlSourceUnknown
	}
}

func markCrawlSourcePending(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64) (bool, error) {
	query := `UPDATE Sources
		SET status = 'pending'
		WHERE source_id = ` + crawlPlaceholder(db, 1) + `
		  AND disabled = FALSE
		  AND COALESCE(LOWER(TRIM(status)), '') NOT IN ('processing', 'pending')`
	result, err := tx.ExecContext(ctx, query, sourceID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func readCrawlOperation(ctx context.Context, tx *sql.Tx, db Handler, key string) (crawlOperationRow, bool, error) {
	query := `SELECT status, details FROM Status WHERE status_sha256 = ` + crawlPlaceholder(db, 1)
	var row crawlOperationRow
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, key).Scan(&row.Status, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return crawlOperationRow{}, false, nil
		}
		return crawlOperationRow{}, false, err
	}
	if err := json.Unmarshal(raw, &row.Details); err != nil {
		return crawlOperationRow{}, false, err
	}
	return row, true, nil
}

func insertCrawlOperation(ctx context.Context, tx *sql.Tx, db Handler, key string, details crawlOperationDetails) (bool, error) {
	raw, err := json.Marshal(details)
	if err != nil {
		return false, err
	}
	var query string
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query = `INSERT INTO Status
			(status_sha256, agent_id, source_id, operation_type, status, details)
			VALUES ($1, $2, $3, $4, 'scheduling', $5::jsonb)
			ON CONFLICT (status_sha256) DO NOTHING`
	} else {
		query = `INSERT OR IGNORE INTO Status
			(status_sha256, agent_id, source_id, operation_type, status, details)
			VALUES (?, ?, ?, ?, 'scheduling', ?)`
	}
	result, err := tx.ExecContext(ctx, query, key, details.AgentID, details.SourceID, details.OperationType, string(raw))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func updateCrawlOperation(ctx context.Context, tx *sql.Tx, db Handler, key string, state CrawlOperationState, details crawlOperationDetails) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var query string
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query = `UPDATE Status SET status = $1, last_updated_at = CURRENT_TIMESTAMP, details = $2::jsonb WHERE status_sha256 = $3`
	} else {
		query = `UPDATE Status SET status = ?, last_updated_at = CURRENT_TIMESTAMP, details = ? WHERE status_sha256 = ?`
	}
	_, err = tx.ExecContext(ctx, query, string(state), string(raw), key)
	return err
}

func crawlPlaceholder(db Handler, position int) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}
