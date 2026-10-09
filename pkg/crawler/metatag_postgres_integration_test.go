//go:build integration

// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package crawler

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestPostgresMetaTagDuplicateAndConcurrentPersistence(t *testing.T) {
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
		t.Fatal(err)
	}
	configuration := cfg.Config{Database: cfg.Database{
		Type: cdb.DBPostgresStr, Host: env("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), Port: port,
		User:     env("DOCKER_POSTGRES_DB_USER", env("DOCKER_POSTGRES_USER", "postgres")),
		Password: env("DOCKER_POSTGRES_PASSWORD", "postgres"), DBName: env("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode: env("DOCKER_POSTGRES_SSL_MODE", "disable"), MaxConns: 8, MaxIdleConns: 8,
	}}
	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.Connect(configuration); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	suffix := fmt.Sprint(time.Now().UnixNano())
	var indexID uint64
	if err = handler.QueryRow(
		`INSERT INTO SearchIndex(page_url, title, summary)
		VALUES($1, 'metatags', '')
		RETURNING index_id`,
		"https://metatag-set.invalid/"+suffix,
	).Scan(&indexID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM SearchIndex WHERE index_id=$1`, indexID)
		_, _ = handler.Exec(`DELETE FROM MetaTags WHERE name=$1`, "audit-"+suffix)
	})

	tags := []MetaTag{{Name: "audit-" + suffix, Content: "shared"}}
	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, txErr := handler.Begin()
			if txErr == nil {
				txErr = insertMetaTagsWithTimeSeries(tx, indexID, tags, nil, nil)
			}
			if txErr == nil {
				txErr = tx.Commit()
			} else if tx != nil {
				_ = tx.Rollback()
			}
			errs <- txErr
		}()
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var metatagRows, linkRows int
	var metatagID, linkID int64
	var createdAt, updatedAt time.Time
	if err = handler.QueryRow(`SELECT min(m.metatag_id), min(mi.sim_id), count(DISTINCT m.metatag_id),
		count(DISTINCT mi.sim_id), min(mi.created_at), min(mi.last_updated_at)
		FROM MetaTags m JOIN MetaTagsIndex mi USING(metatag_id)
		WHERE m.name=$1 AND mi.index_id=$2`, tags[0].Name, indexID).
		Scan(&metatagID, &linkID, &metatagRows, &linkRows, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if metatagRows != 1 || linkRows != 1 {
		t.Fatalf("concurrent logical state: metatags=%d links=%d, want 1/1", metatagRows, linkRows)
	}

	tx, err := handler.Begin()
	if err == nil {
		err = insertMetaTagsWithTimeSeries(tx, indexID, append(tags, tags[0]), nil, nil)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	var duplicateMetaTagID, duplicateLinkID int64
	var duplicateCreatedAt, duplicateUpdatedAt time.Time
	if err = handler.QueryRow(`SELECT m.metatag_id, mi.sim_id, mi.created_at, mi.last_updated_at
		FROM MetaTags m JOIN MetaTagsIndex mi USING(metatag_id)
		WHERE m.name=$1 AND mi.index_id=$2`, tags[0].Name, indexID).
		Scan(&duplicateMetaTagID, &duplicateLinkID, &duplicateCreatedAt, &duplicateUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if duplicateMetaTagID != metatagID || duplicateLinkID != linkID ||
		!duplicateCreatedAt.Equal(createdAt) || !duplicateUpdatedAt.Equal(updatedAt) {
		t.Fatalf("duplicate changed identity/timestamps: before=(%d,%d,%s,%s) after=(%d,%d,%s,%s)",
			metatagID, linkID, createdAt, updatedAt,
			duplicateMetaTagID, duplicateLinkID, duplicateCreatedAt, duplicateUpdatedAt)
	}
}
