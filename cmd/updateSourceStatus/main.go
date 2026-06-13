// Copyright 2026 Paolo Fabio Zaino
// Licensed under the Apache License, Version 2.0

// Package main (updateSourceStatus) allows updating the status
// of one, many, or all Sources in TheCROWler DB.
package main

import (
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

// Examples of use:
// ./updateSourceStatus -url https://example.com -status new
// ./updateSourceStatus -id 42 -status new
// ./updateSourceStatus -bulk sites.csv -status new
// ./updateSourceStatus -all -status new
// ./updateSourceStatus -yesterday -status new
// ./updateSourceStatus -updated-within 48h -status new
// ./updateSourceStatus -updated-after 2026-02-23T00:00:00Z -status new
// ./updateSourceStatus -updated-after 2026-02-23T00:00:00Z -updated-before 2026-02-24T00:00:00Z -status new

var config cfg.Config

var (
	loadConfig   = cfg.LoadConfig
	openDatabase = sql.Open
	now          = time.Now
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("updateSourceStatus", flag.ContinueOnError)
	configFile := flags.String("config", "config.yaml", "Path to configuration file")
	url := flags.String("url", "", "Single source URL to update")
	sourceID := flags.Uint64("id", 0, "Single source ID to update")
	bulk := flags.String("bulk", "", "CSV file containing URLs to update")
	all := flags.Bool("all", false, "Update ALL sources")
	status := flags.String("status", "", "New status string (required)")

	// Time based options
	updatedWithin := flags.String("updated-within", "", "Update sources last updated within this duration (e.g. 24h, 48h, 30m)")
	yesterday := flags.Bool("yesterday", false, "Update sources last updated yesterday (Europe/London)")
	updatedAfter := flags.String("updated-after", "", "Update sources last updated at/after this RFC3339 timestamp (e.g. 2026-02-23T00:00:00Z)")
	updatedBefore := flags.String("updated-before", "", "Update sources last updated before this RFC3339 timestamp (optional)")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*status) == "" {
		return fmt.Errorf("you must provide -status")
	}

	// Load config
	var err error
	config, err = loadConfig(*configFile)
	if err != nil {
		return err
	}

	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Password,
		config.Database.DBName,
	)

	db, err := openDatabase(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		return err
	}
	defer db.Close() // nolint: errcheck // we don't need to check the error here it would be on program exit

	// Time window mode has priority if any is set
	if *yesterday || *updatedWithin != "" || *updatedAfter != "" || *updatedBefore != "" {
		start, end, err := computeTimeWindow(*yesterday, *updatedWithin, *updatedAfter, *updatedBefore)
		if err != nil {
			return fmt.Errorf("invalid time window: %w", err)
		}
		if err := updateSourcesByUpdatedAtRange(db, *status, start, end); err != nil {
			return fmt.Errorf("update failed: %w", err)
		}
		fmt.Println("Update completed successfully.")
		return nil
	}

	switch {
	case *all:
		err = updateAllSources(db, *status)

	case *url != "":
		err = updateSourceByURL(db, normalizeURL(*url), *status)

	case *sourceID != 0:
		err = updateSourceByID(db, *sourceID, *status)

	case *bulk != "":
		err = updateSourcesFromCSV(db, *bulk, *status)

	default:
		return fmt.Errorf("specify -url, -id, -bulk, -all, or one of: -yesterday, -updated-within, -updated-after/-updated-before")
	}

	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Println("Update completed successfully.")
	return nil
}

// computeTimeWindow returns [start, end) bounds.
// If end is nil, we treat it as "no upper bound".
func computeTimeWindow(useYesterday bool, withinStr, afterStr, beforeStr string) (time.Time, *time.Time, error) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		return time.Time{}, nil, err
	}

	// yesterday: [yesterday 00:00, today 00:00)
	if useYesterday {
		current := now().In(loc)
		todayStart := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, loc)
		yesterdayStart := todayStart.AddDate(0, 0, -1)
		end := todayStart
		return yesterdayStart, &end, nil
	}

	// updated-within: [now - duration, now]
	if withinStr != "" {
		d, err := time.ParseDuration(withinStr)
		if err != nil {
			return time.Time{}, nil, err
		}
		if d <= 0 {
			return time.Time{}, nil, fmt.Errorf("updated-within duration must be greater than zero")
		}
		current := now().In(loc)
		start := current.Add(-d)
		end := current
		return start, &end, nil
	}

	// updated-after / updated-before: RFC3339
	if afterStr == "" {
		return time.Time{}, nil, fmt.Errorf("when using -updated-after/-updated-before you must provide -updated-after")
	}

	start, err := time.Parse(time.RFC3339, afterStr)
	if err != nil {
		return time.Time{}, nil, err
	}

	if beforeStr == "" {
		return start, nil, nil
	}

	end, err := time.Parse(time.RFC3339, beforeStr)
	if err != nil {
		return time.Time{}, nil, err
	}
	if !end.After(start) {
		return time.Time{}, nil, fmt.Errorf("updated-before must be after updated-after")
	}
	return start, &end, nil
}

func updateSourcesByUpdatedAtRange(db *sql.DB, status string, start time.Time, end *time.Time) error {
	// IMPORTANT: This assumes Sources has a column called last_updated_at.
	// If your column name differs, change it here.
	if end == nil {
		res, err := db.Exec(
			`UPDATE Sources SET status = $1 WHERE last_updated_at >= $2`,
			status, start,
		)
		if err != nil {
			return err
		}
		rows, _ := res.RowsAffected()
		fmt.Printf("Updated %d sources (last_updated_at >= %s).\n", rows, start.Format(time.RFC3339))
		return nil
	}

	res, err := db.Exec(
		`UPDATE Sources SET status = $1 WHERE last_updated_at >= $2 AND last_updated_at < $3`,
		status, start, *end,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	fmt.Printf("Updated %d sources (%s <= last_updated_at < %s).\n", rows, start.Format(time.RFC3339), end.Format(time.RFC3339))
	return nil
}

func updateAllSources(db *sql.DB, status string) error {
	res, err := db.Exec(`UPDATE Sources SET status = $1`, status)
	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	fmt.Printf("Updated %d sources.\n", rows)
	return nil
}

func updateSourceByURL(db *sql.DB, url, status string) error {
	res, err := db.Exec(
		`UPDATE Sources SET status = $1 WHERE url = $2`,
		status, url,
	)
	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	fmt.Printf("Updated %d source(s).\n", rows)
	return nil
}

func updateSourceByID(db *sql.DB, id uint64, status string) error {
	res, err := db.Exec(
		`UPDATE Sources SET status = $1 WHERE source_id = $2`,
		status, id,
	)
	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	fmt.Printf("Updated %d source(s).\n", rows)
	return nil
}

func updateSourcesFromCSV(db *sql.DB, filename, status string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Only the first column is used, so allow optional metadata columns.
	reader.FieldsPerRecord = -1

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if len(record) < 1 || strings.TrimSpace(record[0]) == "" {
			continue
		}

		url := normalizeURL(record[0])
		if err := updateSourceByURL(db, url, status); err != nil {
			fmt.Printf("Failed updating %s: %v\n", url, err)
		}
	}

	return nil
}

func normalizeURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimRight(url, "/")
	return url
}
