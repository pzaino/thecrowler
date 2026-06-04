package infoseed

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestSchedulerProcessesNotifiedSeedBeforePollingInterval(t *testing.T) {
	handler := openSchedulerSQLiteDB(t)
	defer (*handler).Close()

	config := schedulerTestConfig(60)
	runner := NewRunner(handler, config)
	stop := StartScheduler(context.Background(), handler, config, runner, "test-engine-notify")
	defer stop()

	id, err := cdb.CreateInformationSeedAndNotify(context.Background(), handler, &cdb.InformationSeed{InformationSeed: "notified seed"})
	if err != nil {
		t.Fatalf("create and notify seed: %v", err)
	}
	assertSchedulerSeedStatus(t, handler, id, "completed", 2*time.Second)
}

func TestSchedulerPollingFallbackProcessesSeedWithoutNotification(t *testing.T) {
	handler := openSchedulerSQLiteDB(t)
	defer (*handler).Close()

	config := schedulerTestConfig(1)
	runner := NewRunner(handler, config)
	stop := StartScheduler(context.Background(), handler, config, runner, "test-engine-poll")
	defer stop()

	time.Sleep(150 * time.Millisecond)
	id, err := cdb.CreateInformationSeed(handler, &cdb.InformationSeed{InformationSeed: "poll fallback seed"})
	if err != nil {
		t.Fatalf("create seed without notification: %v", err)
	}
	assertSchedulerSeedStatus(t, handler, id, "completed", 3*time.Second)
}

func TestDrainWakeupsCoalescesPendingNotifications(t *testing.T) {
	wakeups := make(chan struct{}, 1)
	wakeups <- struct{}{}
	drainWakeups(wakeups)
	select {
	case <-wakeups:
		t.Fatal("expected wakeups to be drained")
	default:
	}
}

func schedulerTestConfig(queryTimer int) cfg.InformationSeedConfig {
	return cfg.InformationSeedConfig{
		Enabled:              true,
		QueryTimer:           queryTimer,
		MaxConcurrentSeeds:   10,
		MaxQueriesPerSeed:    1,
		MaxCandidatesPerSeed: 10,
		RetryInterval:        1,
		ProcessingTimeout:    "5s",
		Providers:            map[string]cfg.InformationSeedProviderConfig{},
	}
}

func openSchedulerSQLiteDB(t *testing.T) *cdb.Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "infoseed-scheduler.sqlite3")
	handlerValue, err := cdb.NewHandler(cfg.Config{Database: cfg.Database{Type: cdb.DBSQLiteStr, DBName: dbPath}})
	if err != nil {
		t.Fatalf("new sqlite handler: %v", err)
	}
	if err = handlerValue.Connect(cfg.Config{Database: cfg.Database{Type: cdb.DBSQLiteStr, DBName: dbPath}}); err != nil {
		t.Fatalf("connect sqlite handler: %v", err)
	}
	handler := cdb.Handler(handlerValue)
	createSchedulerInformationSeedSchema(t, &handler)
	return &handler
}

func createSchedulerInformationSeedSchema(t *testing.T, handler *cdb.Handler) {
	t.Helper()
	_, err := (*handler).Exec(`
		CREATE TABLE InformationSeed (
			information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			information_seed VARCHAR(256) NOT NULL,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			engine VARCHAR(256) DEFAULT '' NOT NULL,
			last_processed_at TIMESTAMP,
			last_error TEXT,
			last_error_at TIMESTAMP,
			disabled BOOLEAN DEFAULT FALSE,
			attempts INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255),
			usr_id INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			url TEXT NOT NULL UNIQUE,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE SourceInformationSeedIndex (
			source_information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			information_seed_id INTEGER NOT NULL,
			discovery_provider VARCHAR(255),
			discovery_query TEXT,
			discovery_rank INTEGER,
			candidate_score REAL,
			candidate_reason TEXT,
			discovery_metadata TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (source_id, information_seed_id)
		);
		CREATE TABLE Events (
			event_sha256 TEXT PRIMARY KEY,
			source_id INTEGER,
			event_type TEXT,
			event_severity TEXT,
			event_timestamp TEXT,
			expires_at TIMESTAMP,
			details TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`)
	if err != nil {
		t.Fatalf("create scheduler test schema: %v", err)
	}
}

func assertSchedulerSeedStatus(t *testing.T, handler *cdb.Handler, id uint64, expected string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus string
	for time.Now().Before(deadline) {
		seed, err := cdb.GetInformationSeedByID(handler, id)
		if err == nil {
			lastStatus = seed.Status
			if seed.Status == expected {
				return
			}
		} else if err != sql.ErrNoRows {
			lastStatus = err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("seed %d did not reach status %q before timeout; last status/error: %s", id, expected, lastStatus)
}
