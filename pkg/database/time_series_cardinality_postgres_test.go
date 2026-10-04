package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newPostgresCardinalitySlotTest(t *testing.T) (*sql.Tx, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	return tx, mock
}

func expectNewPostgresCardinalityClaim(mock sqlmock.Sqlmock, active int) {
	mock.ExpectQuery(`SELECT identity_value`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO TimeSeriesCardinalityTokens`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
}

func expectSlotAttempt(mock sqlmock.Sqlmock, slot int64, inserted bool) {
	mock.ExpectExec(`SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT t.token_number`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(slot))
	rows := sqlmock.NewRows([]string{"slot_number"})
	if inserted {
		rows.AddRow(slot)
	}
	mock.ExpectQuery(`INSERT INTO TimeSeriesSeriesSlots`).WillReturnRows(rows)
}

func expectNoLockedSlot(mock sqlmock.Sqlmock, active int) {
	mock.ExpectExec(`SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FOR UPDATE OF t SKIP LOCKED`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
}

func expectPhysicalConflictCleanup(mock sqlmock.Sqlmock, active int) {
	mock.ExpectQuery(`SELECT identity_value`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`ROLLBACK TO SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
}

func verifyPostgresCardinalitySlotTest(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimPostgresCardinalitySlotNewReservation(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectSlotAttempt(mock, 0, true)
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotExistingIdentity(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("identity"))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotConflictFindsIdentity(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectSlotAttempt(mock, 0, false)
	mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("identity"))
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotPhysicalConflictRetries(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectSlotAttempt(mock, 0, false)
	expectPhysicalConflictCleanup(mock, 0)
	expectSlotAttempt(mock, 1, true)
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotLockedTokenAtCapacityRejects(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectNoLockedSlot(mock, 2)
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotLockedTokenUsesOptimisticCandidate(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectNoLockedSlot(mock, 0)
	mock.ExpectQuery(`ORDER BY t.token_number LIMIT 1`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO TimeSeriesSeriesSlots`).WillReturnRows(sqlmock.NewRows([]string{"slot_number"}).AddRow(0))
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotOptimisticConflictRetries(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectNoLockedSlot(mock, 0)
	mock.ExpectQuery(`ORDER BY t.token_number LIMIT 1`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO TimeSeriesSeriesSlots`).WillReturnRows(sqlmock.NewRows([]string{"slot_number"}))
	expectPhysicalConflictCleanup(mock, 0)
	expectSlotAttempt(mock, 1, true)
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || !admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotOptimisticConflictConsumesCapacity(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectNoLockedSlot(mock, 0)
	mock.ExpectQuery(`ORDER BY t.token_number LIMIT 1`).WillReturnRows(sqlmock.NewRows([]string{"token_number"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO TimeSeriesSeriesSlots`).WillReturnRows(sqlmock.NewRows([]string{"slot_number"}))
	expectPhysicalConflictCleanup(mock, 2)

	admitted, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err != nil || admitted {
		t.Fatalf("claim = %v, %v", admitted, err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotHashCollision(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("different"))

	if _, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2); err == nil || !strings.Contains(err.Error(), "hash collision") {
		t.Fatalf("expected hash collision, got %v", err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotConflictFindsHashCollision(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	expectSlotAttempt(mock, 0, false)
	mock.ExpectQuery(`SELECT identity_value`).WillReturnRows(sqlmock.NewRows([]string{"identity_value"}).AddRow("different"))
	mock.ExpectExec(`ROLLBACK TO SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`RELEASE SAVEPOINT timeseries_cardinality_slot_attempt`).WillReturnResult(sqlmock.NewResult(0, 0))

	if _, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2); err == nil || !strings.Contains(err.Error(), "hash collision") {
		t.Fatalf("expected hash collision, got %v", err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}

func TestClaimPostgresCardinalitySlotRetryExhaustion(t *testing.T) {
	tx, mock := newPostgresCardinalitySlotTest(t)
	expectNewPostgresCardinalityClaim(mock, 0)
	for slot := int64(0); slot < 2; slot++ {
		expectSlotAttempt(mock, slot, false)
		expectPhysicalConflictCleanup(mock, 0)
	}

	_, err := claimPostgresCardinalitySlot(context.Background(), tx, "TimeSeriesSeriesSlots", 7, "", "hash", "identity", 2)
	if err == nil || err == sql.ErrNoRows || !strings.Contains(err.Error(), "metric 7 after 2 attempts") {
		t.Fatalf("expected descriptive retry exhaustion, got %v", err)
	}
	verifyPostgresCardinalitySlotTest(t, mock)
}
