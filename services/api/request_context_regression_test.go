package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// cancelOnQueryContextHandler lets the schema probe finish, then deterministically
// cancels the request as the linked-source QueryContext call begins.
type cancelOnQueryContextHandler struct {
	cdb.Handler
	cancel context.CancelFunc
}

func (h *cancelOnQueryContextHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	h.cancel()
	return h.Handler.QueryContext(ctx, query, args...)
}

func TestRequestContextDatabasePathsAreCancellationAware(t *testing.T) {
	t.Run("QueryRowContext source creation", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handler := cdb.Handler(&sourceAPITestHandler{db: db})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = cdb.CreateSourceContext(ctx, &handler, &cdb.Source{URL: "https://cancelled.example", Name: "cancelled"}, cfg.SourceConfig{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CreateSourceContext() error = %v, want context.Canceled", err)
		}
	})

	t.Run("QueryContext information seed linked sources", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pragma_table_info`).
			WithArgs("deleted_at").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		base := cdb.Handler(&sourceAPITestHandler{db: db})
		ctx, cancel := context.WithCancel(context.Background())
		handler := cdb.Handler(&cancelOnQueryContextHandler{Handler: base, cancel: cancel})

		_, err = cdb.ListSourcesForInformationSeedContext(ctx, &handler, 7, cdb.InformationSeedLinkedSourceFilter{Limit: 10})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListSourcesForInformationSeedContext() error = %v, want context.Canceled", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations: %v", err)
		}
	})

	t.Run("ExecContext owner mutation", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handler := cdb.Handler(&sourceAPITestHandler{db: db})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = performRemoveOwnerContext(ctx, "23", getQuery, &handler)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("performRemoveOwnerContext() error = %v, want context.Canceled", err)
		}
	})
}

func TestCancelledInformationSeedCreationRollsBackTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := cdb.Handler(&sourceAPITestHandler{db: db})
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO InformationSeed`).WillReturnError(context.Canceled)
	mock.ExpectRollback()

	_, err = cdb.CreateInformationSeedContext(context.Background(), &handler, &cdb.InformationSeed{InformationSeed: "cancel atomically"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateInformationSeedContext() error = %v, want context.Canceled", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("transaction was not rolled back: %v", err)
	}
}

func TestContextFreeOwnerAPIUsesIndependentLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := cdb.Handler(&sourceAPITestHandler{db: db})
	mock.ExpectQuery(`INSERT INTO Owners`).WillReturnRows(sqlmock.NewRows([]string{"owner_id"}).AddRow(41))

	response, err := performAddOwner(`{"details":{"name":"independent"}}`, postQuery, &handler)
	if err != nil {
		t.Fatalf("performAddOwner() error = %v", err)
	}
	if response.Message != "Owner added successfully with ID 41" {
		t.Fatalf("performAddOwner() response = %q", response.Message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
