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
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"github.com/lib/pq"
	_ "github.com/lib/pq" // PostgreSQL driver
)

const (
	optDisable = "disable"
)

// ---------------------------------------------------------------
// Postgres handlers
// ---------------------------------------------------------------

// PostgresHandler is the implementation of the DatabaseHandler interface
type PostgresHandler struct {
	db      *sql.DB
	dbms    string
	connStr string
}

// Connect connects to the database
func (handler *PostgresHandler) Connect(c cfg.Config) error {
	connectionString := buildConnectionString(c)
	handler.connStr = connectionString
	handler.dbms = "PostgreSQL"

	// Set a limit for retries
	retryInterval := time.Duration(c.Database.RetryTime) * time.Second
	pingInterval := time.Duration(c.Database.PingTime) * time.Second

	var err error
	for {
		// Try to open the database connection
		handler.db, err = sql.Open("postgres", connectionString)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error opening database connection: %v", err)
			time.Sleep(retryInterval)
			continue // Retry opening the connection
		}
		break
	}

	// Retry pinging the database to establish a live connection
	pingRetries := uint64(0)
	for {
		err = handler.db.Ping()
		if err == nil {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Successfully connected to the database after %d retries", pingRetries)
			break // Ping successful, stop retrying
		}
		if pingRetries%5 == 0 {
			cmn.DebugMsg(cmn.DbgLvlError, "pinging the database: %v", err)
		}
		time.Sleep(pingInterval)
		pingRetries++
		if (pingRetries % 10) == 0 {
			cmn.DebugMsg(cmn.DbgLvlError, ": Failed to connect to the database after %d retries, I'll keep trying...", pingRetries)
		}
	}

	// Set connection parameters (open and idle connections)
	mxConns, mxIdleConns := determineConnectionLimits(c)
	handler.db.SetConnMaxLifetime(time.Minute * 5)
	handler.db.SetMaxOpenConns(mxConns)
	handler.db.SetMaxIdleConns(mxIdleConns)

	return err
}

// determineConnectionLimits calculates connection limits based on config
func determineConnectionLimits(c cfg.Config) (int, int) {
	mxConns, mxIdleConns := 25, 25

	optFor := strings.ToLower(strings.TrimSpace(c.Database.OptimizeFor))
	switch optFor {
	case "write", "query":
		mxConns = 100
		mxIdleConns = 100
	}

	// Use config-defined max connections if smaller
	if c.Database.MaxConns > 0 && c.Database.MaxConns < mxConns {
		mxConns = c.Database.MaxConns
	}
	if c.Database.MaxIdleConns > 0 && c.Database.MaxIdleConns < mxIdleConns {
		mxIdleConns = c.Database.MaxIdleConns
	}

	return mxConns, mxIdleConns
}

func buildConnectionString(c cfg.Config) string {
	var dbPort int
	if c.Database.Port == 0 {
		dbPort = 5432
	} else {
		dbPort = c.Database.Port
	}
	var dbHost string
	if strings.TrimSpace(c.Database.Host) == "" {
		dbHost = "localhost"
	} else {
		dbHost = strings.TrimSpace(c.Database.Host)
	}
	var dbUser string
	if strings.TrimSpace(c.Database.User) == "" {
		dbUser = "crowler"
	} else {
		dbUser = strings.TrimSpace(c.Database.User)
	}
	dbPassword := c.Database.Password
	var dbName string
	if strings.TrimSpace(c.Database.DBName) == "" {
		dbName = "SitesIndex"
	} else {
		dbName = strings.TrimSpace(c.Database.DBName)
	}
	var dbSSLMode string
	if strings.TrimSpace(c.Database.SSLMode) == "" {
		dbSSLMode = optDisable
	} else {
		sslmode := strings.ToLower(strings.TrimSpace(c.Database.SSLMode))
		// Validate the SSL mode
		if sslmode != optDisable && sslmode != "require" && sslmode != "verify-ca" && sslmode != "verify-full" {
			sslmode = optDisable
		}
		dbSSLMode = sslmode
	}
	connectionString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPort, dbUser, dbPassword, dbName, dbSSLMode)

	return connectionString
}

// Close closes the database connection
func (handler *PostgresHandler) Close() error {
	return handler.db.Close()
}

// Ping checks if the database connection is still alive
func (handler *PostgresHandler) Ping() error {
	return handler.db.Ping()
}

// ExecuteQuery executes a query and returns the result
func (handler *PostgresHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return handler.db.Query(query, args...)
}

// Exec executes a commands on the database
func (handler *PostgresHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return handler.db.Exec(query, args...)
}

// DBMS returns the database management system
func (handler *PostgresHandler) DBMS() string {
	return handler.dbms
}

// Begin starts a transaction
func (handler *PostgresHandler) Begin() (*sql.Tx, error) {
	return handler.db.Begin()
}

// Commit commits a transaction
func (handler *PostgresHandler) Commit(tx *sql.Tx) error {
	return tx.Commit()
}

// Rollback rolls back a transaction
func (handler *PostgresHandler) Rollback(tx *sql.Tx) error {
	return tx.Rollback()
}

// QueryRow executes a query that is expected to return at most one row.
// QueryRow always returns a non-nil value. Errors are deferred until
// Row's Scan method is called.
func (handler *PostgresHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return handler.db.QueryRow(query, args...)
}

// CheckConnection checks if the database connection is still alive
func (handler *PostgresHandler) CheckConnection(c cfg.Config) error {
	var err error
	if handler.Ping() != nil {
		err = handler.Connect(c)
	}
	return err
}

// NewListener creates a new listener
func (handler *PostgresHandler) NewListener() Listener {
	return &PostgresListener{
		connStr: handler.connStr,
	}
}

// PostgresListener is the implementation of the DatabaseListener interface
type PostgresListener struct {
	listener *pq.Listener
	connStr  string
	notify   chan Notification
	conn     *sql.Conn
	done     chan struct{}
}

// Connect connects to the database
func (l *PostgresListener) Connect(c cfg.Config, minReconnectInterval, maxReconnectInterval time.Duration, eventCallback func(ev ListenerEventType, err error)) error {
	// Extract connection details from Config and initialize pq.Listener
	connStr := ""
	if !c.IsEmpty() {
		connStr = buildConnectionString(c)
	} else {
		connStr = l.connStr
	}

	l.listener = pq.NewListener(connStr, minReconnectInterval, maxReconnectInterval, func(ev pq.ListenerEventType, err error) {
		// Map pq.ListenerEventType to ListenerEventType
		var abstractEvent ListenerEventType
		switch ev {
		case pq.ListenerEventConnected:
			abstractEvent = ListenerEventConnected
		case pq.ListenerEventDisconnected:
			abstractEvent = ListenerEventDisconnected
		case pq.ListenerEventReconnected:
			abstractEvent = ListenerEventReconnected
		default:
			abstractEvent = ListenerEventUnknown
		}

		// Call the provided callback with the abstract event type
		eventCallback(abstractEvent, err)
	})

	if l.listener == nil {
		return fmt.Errorf("failed to create listener")
	}

	// Create the notify channel
	l.notify = make(chan Notification)

	// Forward pq notifications to the notify channel
	go func() {
		for pqNotify := range l.listener.Notify {
			l.notify <- &PostgresNotification{
				channel: pqNotify.Channel,
				extra:   pqNotify.Extra,
			}
		}
		close(l.notify)
	}()

	return nil
}

// ConnectWithDBHandler connects to the database with a handler
func (l *PostgresListener) ConnectWithDBHandler(handler *Handler, channel string) error {
	postgresHandler, ok := (*handler).(*PostgresHandler)
	if !ok {
		return fmt.Errorf("failed to cast db handler to PostgresHandler")
	}

	// Obtain a connection from dbHandler
	conn, err := postgresHandler.db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("failed to create connection for listener: %w", err)
	}

	// Save the connection for future use
	l.conn = conn

	// Execute LISTEN on the connection
	_, err = conn.ExecContext(context.Background(), fmt.Sprintf("LISTEN %s", channel))
	if err != nil {
		return fmt.Errorf("failed to execute LISTEN on channel '%s': %w", channel, err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Listening on channel: %s", channel)

	// Spawn a goroutine to monitor notifications
	go l.pollNotifications()

	return nil
}

// Ping checks if the database connection is still alive
func (l *PostgresListener) Ping() error {
	if l.conn != nil {
		return l.conn.PingContext(context.Background())
	}
	if l.listener != nil {
		return l.listener.Ping()
	}

	return fmt.Errorf("listener is not connected")
}

// pollNotifications continuously polls for notifications
func (l *PostgresListener) pollNotifications() {
	for {
		select {
		case <-l.done:
			return
		default:
			// Use the connection to wait for notifications
			if _, err := l.conn.ExecContext(context.Background(), "SELECT 1"); err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Listener poll error: %v", err)
				continue
			}

			rows, err := l.conn.QueryContext(context.Background(), "SELECT pg_notification_queue_usage()")
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Listener poll error: %v", err)
				continue
			}

			defer rows.Close() //nolint:errcheck // We can't check returned error when using defer
			for rows.Next() {
				var channel, payload string
				if err := rows.Scan(&channel, &payload); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Listener poll error: %v", err)
					continue
				}
				l.notify <- &PostgresNotification{channel: channel, extra: payload}
			}
		}
	}
}

// Close closes the database connection
func (l *PostgresListener) Close() error {
	close(l.done)
	if l.conn != nil {
		return l.conn.Close()
	}
	if l.listener != nil {
		return l.listener.Close()
	}
	return nil
}

// Listen listens for notifications on a channel
func (l *PostgresListener) Listen(channel string) error {
	if l.listener == nil {
		return fmt.Errorf("listener is not connected")
	}

	// Use the `Listen` method of pq.Listener to subscribe to the channel
	if err := l.listener.Listen(channel); err != nil {
		return fmt.Errorf("failed to listen to channel '%s': %w", channel, err)
	}

	return nil
}

// Notify returns a channel to receive notifications
func (l *PostgresListener) Notify() <-chan Notification {
	return l.notify
}

// UnlistenAll unsubscribes from all channels
func (l *PostgresListener) UnlistenAll() error {
	if l.listener == nil {
		return fmt.Errorf("listener is not connected")
	}

	// Use the `UnlistenAll` method of pq.Listener to unsubscribe from all channels
	if err := l.listener.UnlistenAll(); err != nil {
		return fmt.Errorf("failed to unlisten all channels: %w", err)
	}

	return nil
}

// PostgresNotification is the implementation of the DatabaseNotification interface
type PostgresNotification struct {
	channel string
	extra   string
}

// Channel returns the channel name
func (n *PostgresNotification) Channel() string {
	return n.channel
}

// Extra returns the notification payload
func (n *PostgresNotification) Extra() string {
	return n.extra
}

// ---------------------------------------------------------------
// Server Configuration

// ConfigForWrite optimizes the database configuration for write operations
func (handler *PostgresHandler) ConfigForWrite() {
	params := map[string]string{
		"max_connections":                 "1000",
		"shared_buffers":                  "16GB",
		"effective_cache_size":            "48GB",
		"maintenance_work_mem":            "2GB",
		"checkpoint_completion_target":    "0.9",
		"wal_buffers":                     "16MB",
		"default_statistics_target":       "100",
		"random_page_cost":                "1.1",
		"effective_io_concurrency":        "200",
		"work_mem":                        "16MB",
		"min_wal_size":                    "1GB",
		"max_wal_size":                    "4GB",
		"max_worker_processes":            "8",
		"max_parallel_workers_per_gather": "4",
		"max_parallel_workers":            "8",
	}

	handler.ConfigForOptimize(params)
}

// ConfigForQuery optimizes the database configuration for queries
func (handler *PostgresHandler) ConfigForQuery() {
	params := map[string]string{
		"max_connections":                 "100",
		"shared_buffers":                  "4GB",
		"effective_cache_size":            "12GB",
		"maintenance_work_mem":            "1GB",
		"checkpoint_completion_target":    "0.5",
		"wal_buffers":                     "8MB",
		"default_statistics_target":       "100",
		"random_page_cost":                "1.1",
		"effective_io_concurrency":        "200",
		"work_mem":                        "4MB",
		"min_wal_size":                    "1GB",
		"max_wal_size":                    "2GB",
		"max_worker_processes":            "4",
		"max_parallel_workers_per_gather": "2",
		"max_parallel_workers":            "4",
	}

	handler.ConfigForOptimize(params)
}

// ConfigForOptimize optimizes the database configuration
func (handler *PostgresHandler) ConfigForOptimize(params map[string]string) {
	for key, value := range params {
		_, err := handler.db.Exec(fmt.Sprintf("ALTER SYSTEM SET %s TO '%s'", key, value))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting %s: %v", key, err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully set %s to %s", key, value)
		}
	}

	// Reload PostgreSQL configuration
	_, err := handler.db.Exec("SELECT pg_reload_conf()")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "reloading configuration: %v", err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Configuration reloaded successfully")
}
