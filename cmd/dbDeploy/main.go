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

// Package main implements the additive schema-1.17 MCP capability deployment
// and verification command. It never changes the schema version or writer
// generation, never drops MCP history on rollback, and refuses to run without
// an explicit backup for deployment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type deployOptions struct {
	configFile string
	check      bool
	apply      bool
	disable    bool
	backupDir  string
	dbUser     string
	pgDump     string
	timeout    time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func parseOptions(args []string) (deployOptions, error) {
	var opts deployOptions
	flags := flag.NewFlagSet("dbDeploy", flag.ContinueOnError)
	flags.StringVar(&opts.configFile, "config", "config.yaml", "Path to the configuration file")
	flags.BoolVar(&opts.check, "check", false, "Verify schema 1.17, MCP capability, SQLite foreign keys and privileges")
	flags.BoolVar(&opts.apply, "apply", false, "Apply the additive MCP capability after creating a backup")
	flags.BoolVar(&opts.disable, "disable", false, "Remove only the MCP capability marker, preserving all history")
	flags.StringVar(&opts.backupDir, "backup-dir", "", "Directory for the full database backup required by -apply")
	flags.StringVar(&opts.dbUser, "db-user", "", "Application database user for ownership, grants and privilege checks (defaults to database.user)")
	flags.StringVar(&opts.pgDump, "pg-dump", "pg_dump", "pg_dump binary used for PostgreSQL backups")
	flags.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "Overall maintenance command timeout")
	if err := flags.Parse(args); err != nil {
		return deployOptions{}, err
	}
	actions := 0
	for _, selected := range []bool{opts.check, opts.apply, opts.disable} {
		if selected {
			actions++
		}
	}
	if actions > 1 {
		return deployOptions{}, errors.New("-check, -apply and -disable are mutually exclusive")
	}
	if actions == 0 {
		opts.check = true
	}
	if opts.apply && strings.TrimSpace(opts.backupDir) == "" {
		return deployOptions{}, errors.New("-apply requires -backup-dir")
	}
	if opts.timeout <= 0 {
		return deployOptions{}, errors.New("-timeout must be positive")
	}
	return opts, nil
}

func run(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	config, err := cfg.LoadConfig(opts.configFile)
	if err != nil {
		return err
	}
	backend := strings.ToLower(strings.TrimSpace(config.Database.Type))
	if backend != "postgres" && backend != "sqlite" && backend != "sqlite3" {
		return fmt.Errorf("dbDeploy supports postgres or sqlite, not %q", config.Database.Type)
	}
	if strings.Contains(backend, "sqlite") {
		if _, err := os.Stat(config.Database.DBName); err != nil {
			return fmt.Errorf("SQLite database %q is not accessible: %w; use the clean-install setup first", config.Database.DBName, err)
		}
	}
	if strings.TrimSpace(opts.dbUser) == "" {
		opts.dbUser = strings.TrimSpace(config.Database.User)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	handler, err := cdb.ConnectForMaintenance(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = handler.Close() }()

	switch {
	case opts.apply:
		return applyCapability(ctx, handler, config, opts)
	case opts.disable:
		return disableCapability(ctx, handler, config, opts)
	default:
		return checkCapability(ctx, handler, config, opts)
	}
}

func checkCapability(ctx context.Context, db cdb.Handler, config cfg.Config, opts deployOptions) error {
	if err := cdb.CheckSchemaVersion117(ctx, db); err != nil {
		return err
	}
	if err := cdb.CheckStartupCompatibility(ctx, db); err != nil {
		return err
	}
	if err := cdb.CheckMCPMutationCapability(ctx, &db); err != nil {
		if errors.Is(err, cdb.ErrMCPMutationSchemaUnavailable) {
			return fmt.Errorf("MCP mutation capability is not deployed; run dbDeploy -apply -backup-dir <dir>: %w", err)
		}
		return err
	}
	if err := cdb.CheckMCPMutationPrivileges(ctx, db, opts.dbUser); err != nil {
		return err
	}
	fmt.Printf("schema=%s writer_generation=%s capability=%s backend=%s\n",
		cdb.RequiredSchemaVersion, cdb.WriterGeneration, cdb.MCPMutationCapability, config.Database.Type)
	return nil
}

func applyCapability(ctx context.Context, db cdb.Handler, config cfg.Config, opts deployOptions) error {
	if err := cdb.CheckSchemaVersion117(ctx, db); err != nil {
		return err
	}
	if err := cdb.CheckStartupCompatibility(ctx, db); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.backupDir, 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	backupPath := backupFilePath(config, opts.backupDir)
	if err := cdb.BackupMCPMutationHistory(ctx, db, config, backupPath, opts.pgDump); err != nil {
		return err
	}
	if err := cdb.ApplyMCPMutationMigration(ctx, db, opts.dbUser); err != nil {
		return err
	}
	if err := cdb.CheckMCPMutationCapability(ctx, &db); err != nil {
		return fmt.Errorf("verify deployed MCP capability: %w", err)
	}
	if err := cdb.CheckMCPMutationPrivileges(ctx, db, opts.dbUser); err != nil {
		return err
	}
	if err := cdb.CheckStartupCompatibility(ctx, db); err != nil {
		return fmt.Errorf("post-deployment compatibility check: %w", err)
	}
	fmt.Printf("applied capability=%s schema=%s writer_generation=%s backup=%s\n",
		cdb.MCPMutationCapability, cdb.RequiredSchemaVersion, cdb.WriterGeneration, backupPath)
	return nil
}

func disableCapability(ctx context.Context, db cdb.Handler, config cfg.Config, opts deployOptions) error {
	if err := cdb.CheckSchemaVersion117(ctx, db); err != nil {
		return err
	}
	before, err := cdb.CountMCPMutationHistory(ctx, db)
	if err != nil {
		return fmt.Errorf("count MCP history before disable: %w", err)
	}
	if strings.TrimSpace(opts.backupDir) != "" {
		if err := os.MkdirAll(opts.backupDir, 0o750); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
		backupPath := backupFilePath(config, opts.backupDir)
		if err := cdb.BackupMCPMutationHistory(ctx, db, config, backupPath, opts.pgDump); err != nil {
			return err
		}
		fmt.Printf("backup=%s\n", backupPath)
	}
	removed, err := cdb.DisableMCPMutationCapability(ctx, db)
	if err != nil {
		return fmt.Errorf("disable MCP capability: %w", err)
	}
	after, err := cdb.CountMCPMutationHistory(ctx, db)
	if err != nil {
		return fmt.Errorf("count MCP history after disable: %w", err)
	}
	for table, count := range before {
		if after[table] != count {
			return fmt.Errorf("disable changed %s rows: before=%d after=%d", table, count, after[table])
		}
	}
	if err := cdb.CheckMCPMutationCapability(ctx, &db); err == nil {
		return errors.New("capability marker is still present after disable")
	} else if !errors.Is(err, cdb.ErrMCPMutationSchemaUnavailable) {
		return fmt.Errorf("unexpected capability state after disable: %w", err)
	}
	fmt.Printf("disabled capability=%s marker_rows_removed=%d history_preserved=true backend=%s\n",
		cdb.MCPMutationCapability, removed, config.Database.Type)
	return nil
}

func backupFilePath(config cfg.Config, backupDir string) string {
	name := filepath.Base(strings.TrimSpace(config.Database.DBName))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "crowler"
	}
	var cleaned strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			cleaned.WriteRune(r)
		} else {
			cleaned.WriteRune('_')
		}
	}
	suffix := "dump"
	if strings.Contains(strings.ToLower(strings.TrimSpace(config.Database.Type)), "sqlite") {
		suffix = "db"
	}
	return filepath.Join(backupDir, fmt.Sprintf("%s-mcp-%s.%s",
		cleaned.String(), time.Now().UTC().Format("20060102T150405Z"), suffix))
}
