package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: probe <retention|fence|exhaust> [flags]")
	}
	config := cfg.Config{Database: cfg.Database{
		Type: os.Getenv("DB_TYPE"), Host: os.Getenv("POSTGRES_DB_HOST"), User: os.Getenv("CROWLER_DB_USER"),
		Password: os.Getenv("CROWLER_DB_PASSWORD"), DBName: os.Getenv("POSTGRES_DB"), SSLMode: "disable",
		RetryTime: 1, PingTime: 1, TransactionTimeout: 60, OptimizeFor: "none", MaxConns: 2, MaxIdleConns: 1,
	}}
	if config.Database.Type == "" {
		config.Database.Type = "postgres"
	}
	if config.Database.DBName == "" {
		config.Database.DBName = "SitesIndex"
	}
	if config.Database.Host == "" {
		config.Database.Host = "crowler-db"
	}
	if config.Database.Port == 0 {
		config.Database.Port = 5432
	}
	handlerValue, err := cdb.NewHandler(config)
	if err != nil {
		fatal("new handler: %v", err)
	}
	connected := make(chan struct{})
	go func() {
		select {
		case <-connected:
		case <-time.After(90 * time.Second):
			fmt.Fprintln(os.Stderr, "timed out connecting to the disposable database")
			os.Exit(3)
		}
	}()
	if err := handlerValue.Connect(config); err != nil {
		close(connected)
		fatal("connect: %v", err)
	}
	close(connected)
	defer func() { _ = handlerValue.Close() }()
	handler := cdb.Handler(handlerValue)
	switch os.Args[1] {
	case "retention":
		fs := flag.NewFlagSet("retention", flag.ExitOnError)
		dryRun := fs.Bool("dry-run", false, "")
		completed := fs.Int("completed-days", 30, "")
		disabled := fs.Int("disabled-days", 30, "")
		exhausted := fs.Int("exhausted-days", 30, "")
		batch := fs.Int("batch-size", 100, "")
		_ = fs.Parse(os.Args[2:])
		result, err := cdb.PurgeInformationSeedRetentionContext(context.Background(), &handler, cfg.InformationSeedRetentionConfig{
			Enabled: true, CompletedDays: *completed, DisabledDays: *disabled, ExhaustedDays: *exhausted, BatchSize: *batch, DryRun: *dryRun,
		})
		printJSON(map[string]any{"result": result, "error": errorString(err)})
	case "exhaust":
		fs := flag.NewFlagSet("exhaust", flag.ExitOnError)
		maxAttempts := fs.Int("max-attempts", 1, "")
		_ = fs.Parse(os.Args[2:])
		err := cdb.ExhaustExpiredInformationSeedClaimsContext(context.Background(), &handler, *maxAttempts)
		printJSON(map[string]any{"error": errorString(err)})
	case "fence":
		if len(os.Args) != 5 {
			fatal("usage: probe fence <seed-id> <claim-token> <fencing-token>")
		}
		id, err := strconv.ParseUint(os.Args[2], 10, 64)
		if err != nil {
			fatal("invalid seed id: %v", err)
		}
		fencing, err := strconv.ParseInt(os.Args[4], 10, 64)
		if err != nil {
			fatal("invalid fencing token: %v", err)
		}
		heartbeatErr := cdb.HeartbeatInformationSeedClaimContext(context.Background(), &handler, id, os.Args[3], fencing, time.Second)
		finalizeErr := cdb.UpdateInformationSeedStatusFencedContext(context.Background(), &handler, id, os.Args[3], fencing, "completed", "", nil)
		printJSON(map[string]any{"heartbeat_error": errorString(heartbeatErr), "finalize_error": errorString(finalizeErr)})
	default:
		fatal("unknown command %q", os.Args[1])
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func printJSON(value any) {
	encoded, _ := json.Marshal(value)
	fmt.Println(string(encoded))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
