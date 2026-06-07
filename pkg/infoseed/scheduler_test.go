package infoseed

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestSchedulerClaimsOnlyConfiguredPriority(t *testing.T) {
	handler := openSchedulerSQLiteDB(t)
	defer (*handler).Close()

	config := schedulerTestConfig(60)
	runner := NewRunner(handler, config)
	stop := StartScheduler(context.Background(), handler, config, runner, "priority-engine", "high")
	defer stop()

	lowID, err := cdb.CreateInformationSeedAndNotify(context.Background(), handler, &cdb.InformationSeed{InformationSeed: "low priority seed", Priority: "low"})
	if err != nil {
		t.Fatalf("create low-priority seed: %v", err)
	}
	highID, err := cdb.CreateInformationSeedAndNotify(context.Background(), handler, &cdb.InformationSeed{InformationSeed: "high priority seed", Priority: "high"})
	if err != nil {
		t.Fatalf("create high-priority seed: %v", err)
	}

	assertSchedulerSeedStatus(t, handler, highID, "completed", 2*time.Second)
	lowSeed, err := cdb.GetInformationSeedByID(handler, lowID)
	if err != nil {
		t.Fatalf("get low-priority seed: %v", err)
	}
	if lowSeed.Status != "new" {
		t.Fatalf("expected low-priority seed to remain unclaimed, got status %q", lowSeed.Status)
	}
}

func TestSchedulerCreatedSeedEmitsConfiguredAgentIdentity(t *testing.T) {
	handler := openSchedulerSQLiteDB(t)
	defer (*handler).Close()

	config := schedulerTestConfig(60)
	runner := NewRunner(handler, config)
	runner.AgentIdentity = AgentIdentity{
		ID:          "system.infoseed.configured-test",
		Name:        "Configured Test Information Seed Agent",
		Type:        "system",
		TrustLevel:  "system",
		Origin:      "built_in",
		RuntimePath: "infoseed.Runner.configured-test",
	}
	stop := StartScheduler(context.Background(), handler, config, runner, "test-engine-agent-path")
	defer stop()

	id, err := cdb.CreateInformationSeedAndNotify(context.Background(), handler, &cdb.InformationSeed{InformationSeed: "agent path seed"})
	if err != nil {
		t.Fatalf("create and notify seed: %v", err)
	}
	assertSchedulerSeedStatus(t, handler, id, "completed", 2*time.Second)
	details := assertSchedulerSeedEventDetails(t, handler, id, informationSeedDiscoveryCompleted, 2*time.Second)
	agent, ok := details["agent"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected event details to include agent identity, got %#v", details)
	}
	if agent["agent_id"] != "system.infoseed.configured-test" || agent["runtime_path"] != "infoseed.Runner.configured-test" {
		t.Fatalf("expected scheduler to trigger configured agent path, got %#v", agent)
	}
	if details["orchestration_model"] != "built_in_infoseed_runner" {
		t.Fatalf("expected built-in runner orchestration model, got %#v", details["orchestration_model"])
	}
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
		CREATE TABLE IF NOT EXISTS InformationSeedCandidate (
			information_seed_candidate_id INTEGER PRIMARY KEY AUTOINCREMENT,
			information_seed_id INTEGER NOT NULL,
			normalized_url VARCHAR(2048) NOT NULL,
			host VARCHAR(255),
			provider VARCHAR(255),
			query TEXT,
			rank INTEGER DEFAULT 0 NOT NULL,
			score REAL DEFAULT 0 NOT NULL,
			decision_status VARCHAR(32) NOT NULL CHECK (decision_status IN ('accepted', 'rejected')),
			rejection_reason TEXT DEFAULT '' NOT NULL,
			metadata TEXT,
			run_attempt INTEGER DEFAULT 0 NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (information_seed_id, normalized_url, provider, query, rank, run_attempt)
		);
		CREATE TABLE IF NOT EXISTS Sources (
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
		CREATE TABLE IF NOT EXISTS SourceInformationSeedIndex (
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
		CREATE TABLE IF NOT EXISTS Events (
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

func assertSchedulerSeedEventDetails(t *testing.T, handler *cdb.Handler, seedID uint64, eventType string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastDetails string
	for time.Now().Before(deadline) {
		rows, err := (*handler).ExecuteQuery(`SELECT details FROM Events WHERE event_type = ? ORDER BY created_at DESC`, eventType)
		if err != nil {
			lastDetails = err.Error()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for rows.Next() {
			var details string
			if err := rows.Scan(&details); err != nil {
				lastDetails = err.Error()
				continue
			}
			lastDetails = details
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(details), &payload); err != nil {
				lastDetails = err.Error()
				continue
			}
			if numericID, ok := payload["information_seed_id"].(float64); ok && uint64(numericID) == seedID {
				_ = rows.Close()
				return payload
			}
		}
		_ = rows.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("seed %d did not emit event %q before timeout; last details/error: %s", seedID, eventType, lastDetails)
	return nil
}
