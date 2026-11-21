package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetSourceByIDContextPreservesLookupErrors(t *testing.T) {
	const query = "SELECT source_id"

	t.Run("success", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		rows := sqlmock.NewRows([]string{
			"source_id", "source_uid", "url", "name", "priority",
			"sub_priority", "category_id", "usr_id", "restricted", "flags", "config",
		}).AddRow(7, "uid-7", "https://example.com", "Example", "high", 0, 1, 9, 0, 0, []byte(`{}`))
		mock.ExpectQuery(query).WithArgs(uint64(7)).WillReturnRows(rows)
		handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
		source, err := GetSourceByIDContext(context.Background(), &handler, 7)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if source.ID != 7 || source.UID != "uid-7" || source.Name != "Example" {
			t.Fatalf("unexpected source: %+v", source)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("missing row", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectQuery(query).WithArgs(uint64(8)).WillReturnError(sql.ErrNoRows)
		handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
		_, err = GetSourceByIDContext(context.Background(), &handler, 8)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing source must preserve sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("database failure", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		lookupErr := errors.New("connection refused")
		mock.ExpectQuery(query).WithArgs(uint64(9)).WillReturnError(lookupErr)
		handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
		_, err = GetSourceByIDContext(context.Background(), &handler, 9)
		if !errors.Is(err, lookupErr) {
			t.Fatalf("database failure must preserve its cause, got %v", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("database failure was misclassified as a missing source: %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
		_, err = GetSourceByIDContext(ctx, &handler, 10)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lookup must preserve context.Canceled, got %v", err)
		}
	})

	t.Run("expired deadline", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
		_, err = GetSourceByIDContext(ctx, &handler, 13)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired deadline must preserve context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		var handler Handler
		_, err := GetSourceByIDContext(context.Background(), &handler, 11)
		if err == nil {
			t.Fatal("nil handler must fail")
		}
	})
}

func TestGetSourceByIDCompatibilityWrapper(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
	handler := Handler(&PostgresHandler{db: db, dbms: DBPostgresStr})
	_, err = GetSourceByID(&handler, 12)
	if err == nil || err.Error() != "no source found with ID 12" {
		t.Fatalf("compatibility wrapper error changed: %v", err)
	}
}
