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

// WP-05B3C3B2R command-level safety tests: fail-closed claim mode handling and
// fenced null: deletion quarantine. No external service is contacted.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func openB2RCommandHandler(t *testing.T) cdb.Handler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd-b2r.db")
	c := cfg.Config{Database: cfg.Database{
		Type: "sqlite", DBName: path, TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(c)
	if err != nil {
		t.Fatalf("new SQLite handler: %v", err)
	}
	if err := handler.Connect(c); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	setup, err := os.ReadFile("../../pkg/database/sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	if _, err := handler.Exec(string(setup)); err != nil {
		t.Fatalf("apply SQLite setup: %v", err)
	}
	return handler
}

func TestRetrieveAvailableSourcesFailsClosedOnMissingPolicy(t *testing.T) {
	handler := openB2RCommandHandler(t)
	// The attempt tables are present, so this is a corrupt/unreadable fenced
	// claim store, not an uninstalled optional capability.
	if _, err := handler.Exec(`DELETE FROM SourceCrawlClaimPolicy WHERE policy_id=1`); err != nil {
		t.Fatalf("delete policy row: %v", err)
	}
	oldConfig := config
	t.Cleanup(func() { config = oldConfig })
	config.Crawler.MaxSources = 1

	sources, err := retrieveAvailableSources(handler, 1)
	if err == nil || !strings.Contains(err.Error(), "reading source crawl claim mode") {
		t.Fatalf("missing policy error = %v sources=%v", err, sources)
	}
	if len(sources) != 0 {
		t.Fatalf("missing policy returned %d sources", len(sources))
	}
}

func TestRetrieveAvailableSourcesKeepsLegacyWhenSchemaAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-b2r.db")
	c := cfg.Config{Database: cfg.Database{
		Type: "sqlite", DBName: path, TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(c)
	if err != nil {
		t.Fatalf("new SQLite handler: %v", err)
	}
	if err := handler.Connect(c); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	present, err := cdb.SourceCrawlAttemptSchemaPresent(context.Background(), &handler)
	if err != nil || present {
		t.Fatalf("schema presence = %v err=%v", present, err)
	}
	oldConfig := config
	t.Cleanup(func() { config = oldConfig })
	config.Crawler.MaxSources = 1

	_, err = retrieveAvailableSources(handler, 1)
	if err != nil && strings.Contains(err.Error(), "claim mode") {
		t.Fatalf("absent optional schema was treated as a fenced policy error: %v", err)
	}
}

func TestHandleNullSourceDeletionQuarantinesFenced(t *testing.T) {
	reported := make(chan cdb.SourceCrawlTerminalOutcome, 1)
	coordinator := &fakeDrainCoordinator{reported: reported}
	deleteChan := make(chan uint64, 1)
	var db cdb.Handler
	source := claimedSource{
		Source:  cdb.Source{ID: 42, URL: "null:https://delete.example/"},
		Handoff: coordinator,
	}
	handleNullSourceDeletion(&db, source, deleteChan)
	select {
	case id := <-deleteChan:
		t.Fatalf("fenced null: source %d was queued for deletion", id)
	default:
	}
	select {
	case outcome := <-reported:
		if outcome != cdb.SourceCrawlOutcomeFailed {
			t.Fatalf("fenced null: outcome = %s", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("fenced null: source did not receive a terminal result")
	}
}

func TestHandleNullSourceDeletionPreservesLegacy(t *testing.T) {
	deleteChan := make(chan uint64, 1)
	var db cdb.Handler
	source := claimedSource{Source: cdb.Source{ID: 43, URL: "null:https://delete.example/"}}
	handleNullSourceDeletion(&db, source, deleteChan)
	select {
	case id := <-deleteChan:
		if id != 43 {
			t.Fatalf("legacy null: queued ID = %d want 43", id)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy null: deletion was not queued")
	}
}

func TestIsNullSource(t *testing.T) {
	cases := map[string]bool{
		"null:https://example.test/": true,
		" NULL:anything":             true,
		"https://example.test/":      false,
		"":                           false,
	}
	for value, want := range cases {
		if got := isNullSource(claimedSource{Source: cdb.Source{URL: value}}); got != want {
			t.Errorf("isNullSource(%q) = %v want %v", value, got, want)
		}
	}
}
