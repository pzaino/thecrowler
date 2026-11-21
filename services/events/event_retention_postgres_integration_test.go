//go:build integration

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

package main

// PostgreSQL coverage for the Events janitor (WP-REL230-RC2 section 4D).

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func openEventRetentionPostgres(t *testing.T) cdb.Handler {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	env := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	port, err := strconv.Atoi(env("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	configuration := cfg.Config{Database: cfg.Database{
		Type:         cdb.DBPostgresStr,
		Host:         env("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
		Port:         port,
		User:         env("DOCKER_POSTGRES_DB_USER", env("DOCKER_POSTGRES_USER", "postgres")),
		Password:     env("DOCKER_POSTGRES_PASSWORD", "postgres"),
		DBName:       env("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode:      env("DOCKER_POSTGRES_SSL_MODE", "disable"),
		MaxConns:     2,
		MaxIdleConns: 1,
	}}
	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if err := handler.Connect(configuration); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return handler
}

// TestPostgresEventJanitorDeletesOnlyExpiredRows proves the janitor contract on
// the production backend: expired rows are deleted, future rows and rows with no
// expiry are kept. Together with TestEventRemovalActionPolicy this pins the two
// independent retention mechanisms, so "automatic_events_removal: never" is
// understood as consumer-level retention bounded by the event TTL.
func TestPostgresEventJanitorDeletesOnlyExpiredRows(t *testing.T) {
	handler := openEventRetentionPostgres(t)
	suffix := fmt.Sprint(time.Now().UnixNano())

	insert := func(label string, expiresAt any) string {
		t.Helper()
		uid := fmt.Sprintf("rc2-janitor-%s-%s", label, suffix)
		if _, err := handler.Exec(`
			INSERT INTO Events (event_sha256, source_id, event_type, event_severity,
				event_timestamp, expires_at, details)
			VALUES ($1, 0, 'rc2_retention', 'info', CURRENT_TIMESTAMP, $2, '{}'::jsonb)`,
			uid, expiresAt); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return uid
	}
	expired := insert("expired", time.Now().Add(-time.Hour).UTC())
	future := insert("future", time.Now().Add(time.Hour).UTC())
	noExpiry := insert("no-expiry", nil)
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM Events WHERE event_sha256 = ANY($1::text[])`,
			pqTextArrayForEvents([]string{expired, future, noExpiry}))
	})

	cleanupExpiredEvents(&handler)

	for _, tc := range []struct {
		uid  string
		want int
	}{
		{expired, 0},
		{future, 1},
		{noExpiry, 1},
	} {
		var count int
		if err := handler.QueryRow(`SELECT COUNT(*) FROM Events WHERE event_sha256 = $1`,
			tc.uid).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.uid, err)
		}
		if count != tc.want {
			t.Fatalf("%s rows after janitor = %d want %d", tc.uid, count, tc.want)
		}
	}
}

// pqTextArrayForEvents keeps the test independent of lib/pq's array helper name
// used elsewhere in the repository.
func pqTextArrayForEvents(values []string) interface{} {
	out := make([]string, len(values))
	copy(out, values)
	return out
}
