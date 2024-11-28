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
	"database/sql"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

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
	DBMS() string
	Begin() (*sql.Tx, error)
	Commit(tx *sql.Tx) error
	Rollback(tx *sql.Tx) error
	QueryRow(query string, args ...interface{}) *sql.Row
	CheckConnection(c cfg.Config) error
	NewListener() Listener
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
