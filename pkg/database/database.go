// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package database is responsible for handling the database setup, configuration and abstraction.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// ErrConnectionPoolControlUnsupported indicates that a database backend does
// not expose runtime control of its database/sql connection pool.
var ErrConnectionPoolControlUnsupported = errors.New("database backend does not support connection pool control")

// ConnectionPoolController is optionally implemented by handlers which own a
// database/sql connection pool whose limits can be adjusted at runtime.
type ConnectionPoolController interface {
	SetConnectionLimits(maxOpen, maxIdle int) error
	ConnectionStats() sql.DBStats
}

// ListenerEventType represents the type of event that the listener has received.
type ListenerEventType int

const (
	// ListenerEventUnknown represents an unknown event.
	ListenerEventUnknown ListenerEventType = iota
	// ListenerEventConnected represents a notification event.
	ListenerEventConnected
	// ListenerEventDisconnected represents a disconnected event.
	ListenerEventDisconnected
	// ListenerEventReconnected represents a reconnected event.
	ListenerEventReconnected
)

// Handler is the interface that wraps the basic methods
// to interact with the database.
type Handler interface {
	Connect(c cfg.Config) error
	Close() error
	Ping() error
	ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error)
	Exec(query string, args ...interface{}) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	DBMS() string
	Begin() (*sql.Tx, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	Commit(tx *sql.Tx) error
	Rollback(tx *sql.Tx) error
	QueryRow(query string, args ...interface{}) *sql.Row
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	CheckConnection(c cfg.Config) error
	NewListener() Listener
}

// SetConnectionLimits updates the pool limits when the selected backend
// supports runtime connection-pool control.
func SetConnectionLimits(db *Handler, maxOpen, maxIdle int) error {
	controller, err := connectionPoolController(db)
	if err != nil {
		return err
	}
	return controller.SetConnectionLimits(maxOpen, maxIdle)
}

// ConnectionStats returns database/sql pool statistics when the selected
// backend supports runtime connection-pool control.
func ConnectionStats(db *Handler) (sql.DBStats, error) {
	controller, err := connectionPoolController(db)
	if err != nil {
		return sql.DBStats{}, err
	}
	return controller.ConnectionStats(), nil
}

func connectionPoolController(db *Handler) (ConnectionPoolController, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("%w: nil handler", ErrConnectionPoolControlUnsupported)
	}
	controller, ok := (*db).(ConnectionPoolController)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrConnectionPoolControlUnsupported, *db)
	}
	return controller, nil
}

// QueryRowContext executes a single-row query with cancellation support.
func QueryRowContext(ctx context.Context, db *Handler, query string, args ...interface{}) *sql.Row {
	return (*db).QueryRowContext(ctx, query, args...)
}

// Listener is the interface that wraps the basic methods
// to interact with the database listener.
type Listener interface {
	Connect(c cfg.Config, minReconnectInterval, maxReconnectInterval time.Duration, eventCallback func(ev ListenerEventType, err error)) error
	ConnectWithDBHandler(dbh *Handler, channel string) error
	Ping() error
	Close() error
	Listen(channel string) error
	Notify() <-chan Notification
	UnlistenAll() error
}

// Notification is the interface that wraps the basic methods
// to interact with the database notification.
type Notification interface {
	Channel() string
	Extra() string
}
