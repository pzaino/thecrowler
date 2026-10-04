// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Command rebuildTimeSeriesCardinality is the documented administrative entry
// point for reconstructing exact time-series cardinality memberships and
// PostgreSQL reservations. The database-level maintenance fence makes it safe
// to run while writers are connected, although running during a quiet period
// avoids making writers wait for the rebuild transaction.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("rebuildTimeSeriesCardinality", flag.ContinueOnError)
	configFile := flags.String("config", "config.yaml", "path to the CROWler configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := cfg.LoadConfig(*configFile)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	db, err := cdb.NewHandler(config)
	if err != nil {
		return err
	}
	if err = db.Connect(config); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer func() { _ = db.Close() }()
	result, err := cdb.RebuildTimeSeriesCardinality(ctx, &db)
	if err != nil {
		return fmt.Errorf("rebuild time-series cardinality: %w", err)
	}
	fmt.Printf("rebuilt time-series cardinality: observations=%d series=%d dimension_values=%d\n", result.Observations, result.Series, result.DimensionValues)
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
