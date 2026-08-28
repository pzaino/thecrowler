package database

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestDetermineConnectionLimits(t *testing.T) {
	tests := []struct {
		name       string
		database   cfg.Database
		open, idle int
	}{
		{"defaults", cfg.Database{}, 8, 2},
		{"explicit", cfg.Database{MaxConns: 31, MaxIdleConns: 7}, 31, 7},
		{"none explicit", cfg.Database{OptimizeFor: " none ", MaxConns: 30, MaxIdleConns: 6}, 30, 6},
		{"write", cfg.Database{OptimizeFor: " WRITE ", MaxConns: 31, MaxIdleConns: 7}, 10, 2},
		{"query", cfg.Database{OptimizeFor: "query", MaxConns: 31, MaxIdleConns: 7}, 12, 4},
		{"unknown fallback", cfg.Database{OptimizeFor: "read", MaxConns: 31, MaxIdleConns: 7}, 8, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOpen, gotIdle := DetermineConnectionLimits(cfg.Config{Database: tt.database})
			if gotOpen != tt.open || gotIdle != tt.idle {
				t.Errorf("got (%d, %d), want (%d, %d)", gotOpen, gotIdle, tt.open, tt.idle)
			}
		})
	}
}

func TestPostgresCheckConnectionHealthy(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("creating sqlmock database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	handler := &PostgresHandler{
		db:   db,
		dbms: DBPostgresStr,
	}

	mock.ExpectPing()

	if err := handler.CheckConnection(cfg.Config{}); err != nil {
		t.Fatalf("CheckConnection() returned error for healthy pool: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestPostgresCheckConnectionReturnsPingError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("creating sqlmock database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	handler := &PostgresHandler{
		db:   db,
		dbms: DBPostgresStr,
	}

	pgErr := &pq.Error{
		Code:    "53300",
		Message: "sorry, too many clients already",
	}

	mock.ExpectPing().WillReturnError(pgErr)

	err = handler.CheckConnection(cfg.Config{})
	if err == nil {
		t.Fatal("CheckConnection() returned nil for failed PostgreSQL ping")
	}

	var returnedPGErr *pq.Error
	if !errors.As(err, &returnedPGErr) {
		t.Fatalf("CheckConnection() did not preserve pq.Error: %v", err)
	}

	if string(returnedPGErr.Code) != "53300" {
		t.Fatalf(
			"PostgreSQL error code = %q, want 53300",
			returnedPGErr.Code,
		)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestPostgresCheckConnectionDoesNotReplaceOrResizePool(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("creating sqlmock database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	const (
		fleetMaxOpen = 7
		fleetMaxIdle = 3
	)

	db.SetMaxOpenConns(fleetMaxOpen)
	db.SetMaxIdleConns(fleetMaxIdle)

	handler := &PostgresHandler{
		db:      db,
		dbms:    DBPostgresStr,
		connStr: "unchanged",
	}

	originalPool := handler.db

	mock.ExpectPing().WillReturnError(&pq.Error{
		Code:    "53300",
		Message: "remaining connection slots are reserved for roles with the SUPERUSER attribute",
	})

	config := cfg.Config{
		Database: cfg.Database{
			MaxConns:     15000,
			MaxIdleConns: 3000,
		},
	}

	if err := handler.CheckConnection(config); err == nil {
		t.Fatal("CheckConnection() returned nil for failed PostgreSQL ping")
	}

	if handler.db != originalPool {
		t.Fatal("CheckConnection() replaced the PostgreSQL connection pool")
	}

	stats := handler.db.Stats()
	if stats.MaxOpenConnections != fleetMaxOpen {
		t.Fatalf(
			"MaxOpenConnections changed to %d, want %d",
			stats.MaxOpenConnections,
			fleetMaxOpen,
		)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestPostgresCheckConnectionRejectsUninitializedPool(t *testing.T) {
	handler := &PostgresHandler{}

	if err := handler.CheckConnection(cfg.Config{}); err == nil {
		t.Fatal("CheckConnection() returned nil for uninitialized pool")
	}
}
