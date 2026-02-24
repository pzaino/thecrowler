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

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

var config cfg.Config

func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	url := flag.String("url", "", "Single source URL to update")
	sourceID := flag.Uint64("id", 0, "Single source ID to update")
	bulk := flag.String("bulk", "", "CSV file containing URLs to update")
	all := flag.Bool("all", false, "Update ALL sources")
	status := flag.String("status", "", "New status string (required)")
	flag.Parse()

	if *status == "" {
		log.Fatal("You must provide -status")
	}

	// Load config
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Password,
		config.Database.DBName,
	)

	db, err := sql.Open(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

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
		log.Fatal("Specify -url, -id, -bulk or -all")
	}

	if err != nil {
		log.Fatalf("Update failed: %v", err)
	}

	fmt.Println("Update completed successfully.")
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

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if len(record) == 0 {
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
