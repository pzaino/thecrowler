package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

const poolControllerTestDriverName = "connection-pool-controller-test"

type poolControllerTestDriver struct{}

func (poolControllerTestDriver) Open(string) (driver.Conn, error) {
	return poolControllerTestConn{}, nil
}

type poolControllerTestConn struct{}

func (poolControllerTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (poolControllerTestConn) Close() error               { return nil }
func (poolControllerTestConn) Begin() (driver.Tx, error)  { return nil, errors.New("not implemented") }
func (poolControllerTestConn) Ping(context.Context) error { return nil }

func init() {
	sql.Register(poolControllerTestDriverName, poolControllerTestDriver{})
}

func newPoolControllerTestHandler(t *testing.T) (*PostgresHandler, *sql.DB) {
	t.Helper()
	db, err := sql.Open(poolControllerTestDriverName, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &PostgresHandler{db: db}, db
}

func TestPostgresSetConnectionLimitsRejectsInvalidMaximum(t *testing.T) {
	handler, db := newPoolControllerTestHandler(t)
	for _, maximum := range []int{0, -1, -20} {
		if err := handler.SetConnectionLimits(maximum, 1); err == nil {
			t.Errorf("SetConnectionLimits(%d, 1) unexpectedly succeeded", maximum)
		}
	}
	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("invalid limits changed maximum open connections to %d", got)
	}
}

func TestPostgresConnectionLimitsClampIdleAndExposeStats(t *testing.T) {
	handler, db := newPoolControllerTestHandler(t)
	if err := handler.SetConnectionLimits(2, 20); err != nil {
		t.Fatal(err)
	}

	connections := make([]*sql.Conn, 2)
	for i := range connections {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = conn
	}
	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	stats := handler.ConnectionStats()
	if stats.MaxOpenConnections != 2 || stats.Idle != 2 {
		t.Fatalf("stats after idle clamp = %+v, want max open 2 and idle 2", stats)
	}

	if err := handler.SetConnectionLimits(2, -4); err != nil {
		t.Fatal(err)
	}
	if got := handler.ConnectionStats().Idle; got != 0 {
		t.Fatalf("negative idle limit was not clamped to zero: %d idle connections", got)
	}
}

func TestPostgresConnectionLimitsScaleWithoutReconnect(t *testing.T) {
	handler, db := newPoolControllerTestHandler(t)
	originalDB := handler.db
	if err := handler.SetConnectionLimits(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := handler.SetConnectionLimits(4, 4); err != nil {
		t.Fatal(err)
	}

	connections := make([]*sql.Conn, 4)
	for i := range connections {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = conn
	}
	if got := handler.ConnectionStats().OpenConnections; got != 4 {
		t.Fatalf("scale-up opened %d connections, want 4", got)
	}
	if err := handler.SetConnectionLimits(2, 2); err != nil {
		t.Fatal(err)
	}
	if handler.db != originalDB {
		t.Fatal("pool limit update replaced the sql.DB")
	}
	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	stats := handler.ConnectionStats()
	if stats.MaxOpenConnections != 2 || stats.OpenConnections > 2 {
		t.Fatalf("pool did not converge after scale-down: %+v", stats)
	}
}

func TestConnectionPoolHelpers(t *testing.T) {
	postgres, _ := newPoolControllerTestHandler(t)
	var supported Handler = postgres
	if err := SetConnectionLimits(&supported, 3, 1); err != nil {
		t.Fatal(err)
	}
	stats, err := ConnectionStats(&supported)
	if err != nil || stats.MaxOpenConnections != 3 {
		t.Fatalf("ConnectionStats() = (%+v, %v), want max open 3", stats, err)
	}

	var unsupported Handler = &SQLiteHandler{}
	if err := SetConnectionLimits(&unsupported, 3, 1); !errors.Is(err, ErrConnectionPoolControlUnsupported) {
		t.Fatalf("unsupported SetConnectionLimits error = %v", err)
	}
	if _, err := ConnectionStats(&unsupported); !errors.Is(err, ErrConnectionPoolControlUnsupported) {
		t.Fatalf("unsupported ConnectionStats error = %v", err)
	}
	if err := SetConnectionLimits(nil, 3, 1); !errors.Is(err, ErrConnectionPoolControlUnsupported) {
		t.Fatalf("nil SetConnectionLimits error = %v", err)
	}
}
