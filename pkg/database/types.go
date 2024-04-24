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

// Package database is responsible for handling the database
// setup, configuration and abstraction.
package database

import (
	"database/sql"
	"encoding/json"

	cfg "github.com/pzaino/thecrowler/pkg/config"
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
}

// PostgresHandler is the implementation of the DatabaseHandler interface
type PostgresHandler struct {
	db   *sql.DB
	dbms string
}

// SQLiteHandler is the implementation of the DatabaseHandler interface
type SQLiteHandler struct {
	db   *sql.DB
	dbms string
}

type TxHandler struct {
	sql.Tx
}

// Source represents the structure of the Sources table
// for a record we have decided we need to crawl.
// Id represents the unique identifier of the source.
// URL represents the URL of the source.
// Restricted indicates whether the crawling has to be restricted to the source domain or not.
// Flags represents additional flags associated with the source.
type Source struct {
	// ID is the unique identifier of the source.
	ID int64
	// URL is the URL of the source.
	URL string
	// Restricted indicates whether the crawling has to be restricted to the source domain or not.
	Restricted int
	// Flags represents additional flags associated with the source.
	Flags int
	// Config is a JSON object containing the configuration for the source.
	Config *json.RawMessage // we use json.RawMessage to avoid unmarshalling the JSON object

	// The following fields are not stored in the database but are used internally.
	Status int
}

// DefaultSourceCfgJSON is the default configuration for a source in JSON format.
var DefaultSourceCfgJSON = []byte(`{"config":"default"}`)

// DefaultSourceCfgRaw is the default configuration for a source.
var DefaultSourceCfgRaw = json.RawMessage(DefaultSourceCfgJSON)
