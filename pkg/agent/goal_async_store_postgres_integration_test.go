//go:build integration

package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func goalAsyncPostgresTarget(t *testing.T) *url.URL {
	t.Helper()
	if strings.TrimSpace(os.Getenv("THECROWLER_POSTGRES_INTEGRATION")) != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	optedIn := os.Getenv("CROWLER_ASYNC_GOAL_POSTGRES_TEST_DISPOSABLE") == "1" ||
		os.Getenv("CROWLER_MCP_POSTGRES_TEST_DISPOSABLE") == "1"
	if !optedIn {
		t.Skip("set CROWLER_ASYNC_GOAL_POSTGRES_TEST_DISPOSABLE=1 (or the MCP disposable opt-in) for a disposable PostgreSQL database")
	}
	raw := strings.TrimSpace(os.Getenv("CROWLER_ASYNC_GOAL_POSTGRES_DSN"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("CROWLER_MCP_POSTGRES_DSN"))
	}
	if raw == "" {
		t.Skip("CROWLER_ASYNC_GOAL_POSTGRES_DSN or CROWLER_MCP_POSTGRES_DSN is required")
	}
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse disposable PostgreSQL DSN: %v", err)
	}
	if target.Scheme != "postgres" && target.Scheme != "postgresql" {
		t.Fatalf("disposable PostgreSQL DSN must use postgres://")
	}
	if strings.TrimSpace(target.Host) == "" {
		t.Fatalf("disposable PostgreSQL DSN must include a host")
	}
	return target
}

func goalAsyncPostgresRandomSuffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate disposable database suffix: %v", err)
	}
	return hex.EncodeToString(raw)
}

func newGoalAsyncPostgresDisposableDB(t *testing.T) (*sql.DB, *url.URL) {
	t.Helper()
	target := goalAsyncPostgresTarget(t)
	adminURL := *target
	adminURL.Path = "/postgres"
	admin, err := sql.Open(cdb.DBPostgresStr, adminURL.String())
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		fallback := *target
		_ = admin.Close()
		admin, err = sql.Open(cdb.DBPostgresStr, fallback.String())
		if err != nil {
			t.Fatalf("open PostgreSQL maintenance connection: %v", err)
		}
		if err := admin.PingContext(ctx); err != nil {
			_ = admin.Close()
			t.Fatalf("ping PostgreSQL maintenance connection: %v", err)
		}
	}
	dbName := "crowler_goal_store_" + goalAsyncPostgresRandomSuffix(t)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(dbName)); err != nil {
		_ = admin.Close()
		t.Fatalf("create disposable PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := admin.ExecContext(dropCtx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(dbName)+` WITH (FORCE)`); err != nil {
			t.Errorf("drop disposable PostgreSQL database %s: %v", dbName, err)
		}
		_ = admin.Close()
	})

	testURL := *target
	testURL.Path = "/" + dbName
	testDB, err := sql.Open(cdb.DBPostgresStr, testURL.String())
	if err != nil {
		t.Fatalf("open disposable PostgreSQL database: %v", err)
	}
	testDB.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = testDB.Close() })
	if err := testDB.PingContext(ctx); err != nil {
		t.Fatalf("ping disposable PostgreSQL database: %v", err)
	}
	applyGoalAsyncPostgresSchema(t, testDB)
	return testDB, &testURL
}

func applyGoalAsyncPostgresSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE DBSchemaVersion (
		version TEXT PRIMARY KEY,
		description TEXT,
		is_current BOOLEAN NOT NULL DEFAULT FALSE
	);
	INSERT INTO DBSchemaVersion(version, description, is_current) VALUES ('1.17', 'test', TRUE);
	CREATE TABLE DatabaseWriterCompatibility (
		lock_id INTEGER PRIMARY KEY,
		writer_generation TEXT NOT NULL
	);
	INSERT INTO DatabaseWriterCompatibility(lock_id, writer_generation)
		VALUES (1, 'verified-ownership-v1.17');`); err != nil {
		t.Fatalf("create disposable schema markers: %v", err)
	}
	setup, err := os.ReadFile("../database/postgresql-setup.pgsql")
	if err != nil {
		t.Fatalf("read PostgreSQL setup: %v", err)
	}
	const begin = "-- BEGIN AGENT GOAL PERSISTENCE (schema 1.17)"
	const end = "-- END AGENT GOAL PERSISTENCE (schema 1.17)"
	text := string(setup)
	start := strings.Index(text, begin)
	finish := strings.Index(text, end)
	if start < 0 || finish < start {
		t.Fatalf("PostgreSQL goal schema block markers are missing")
	}
	block := text[start : finish+len(end)]
	if _, err := db.Exec(block); err != nil {
		t.Fatalf("apply PostgreSQL goal schema block: %v", err)
	}
}

func connectGoalAsyncPostgres(t *testing.T, target *url.URL) (cfg.Config, cdb.Handler) {
	t.Helper()
	port, err := strconv.Atoi(target.Port())
	if err != nil {
		t.Fatalf("invalid PostgreSQL port: %v", err)
	}
	password, _ := target.User.Password()
	config := cfg.Config{Database: cfg.Database{
		Type:               cdb.DBPostgresStr,
		Host:               target.Hostname(),
		Port:               port,
		User:               target.User.Username(),
		Password:           password,
		DBName:             strings.TrimPrefix(target.Path, "/"),
		SSLMode:            target.Query().Get("sslmode"),
		MaxConns:           10,
		MaxIdleConns:       5,
		TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("create PostgreSQL handler: %v", err)
	}
	if err := handler.Connect(config); err != nil {
		t.Fatalf("connect PostgreSQL handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return config, handler
}

func openGoalAsyncPostgresStore(t *testing.T) (*goalAsyncStore, cdb.Handler, cfg.Config) {
	t.Helper()
	_, target := newGoalAsyncPostgresDisposableDB(t)
	config, handler := connectGoalAsyncPostgres(t, target)
	store, err := newGoalAsyncStore(context.Background(), &handler)
	if err != nil {
		t.Fatalf("construct PostgreSQL goal store: %v", err)
	}
	return store, handler, config
}

func TestPostgresGoalAsyncStoreRoundTripAfterRestart(t *testing.T) {
	store, handler, config := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load PostgreSQL checkpoint: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("PostgreSQL checkpoint differs from created checkpoint")
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close PostgreSQL handler: %v", err)
	}
	reopenedHandler, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("reopen PostgreSQL handler: %v", err)
	}
	if err := reopenedHandler.Connect(config); err != nil {
		t.Fatalf("reconnect PostgreSQL handler: %v", err)
	}
	t.Cleanup(func() { _ = reopenedHandler.Close() })
	reopened, err := newGoalAsyncStore(context.Background(), &reopenedHandler)
	if err != nil {
		t.Fatalf("construct reopened PostgreSQL store: %v", err)
	}
	loaded, err = reopened.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load PostgreSQL checkpoint after restart: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("PostgreSQL checkpoint changed after restart")
	}
}

func TestPostgresGoalAsyncStoreLifecycleAndConcurrency(t *testing.T) {
	store, _, _ := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}

	start := make(chan struct{})
	type result struct {
		outcome goalAsyncTransitionOutcome
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, operationID := range []string{"op-pg-concurrent-a", "op-pg-concurrent-b"} {
		operationID := operationID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, goalAsyncTestStartRequest(cp, operationID))
			results <- result{outcome: outcome, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	applied := 0
	conflicted := 0
	for result := range results {
		switch {
		case result.err == nil:
			if result.outcome != goalAsyncTransitionApplied {
				t.Fatalf("successful PostgreSQL transition outcome = %q", result.outcome)
			}
			applied++
		case errors.Is(result.err, ErrGoalAsyncRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected PostgreSQL transition error: %v", result.err)
		}
	}
	if applied != 1 || conflicted != 1 {
		t.Fatalf("PostgreSQL concurrent transitions applied=%d conflicted=%d, want 1/1", applied, conflicted)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load PostgreSQL checkpoint after concurrency: %v", err)
	}
	if loaded.Revision != cp.Revision+1 || loaded.Slices != 1 {
		t.Fatalf("PostgreSQL concurrency lost state: revision=%d slices=%d", loaded.Revision, loaded.Slices)
	}
}

func TestPostgresGoalAsyncStoreReplayOlderTransition(t *testing.T) {
	store, _, _ := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}
	start := goalAsyncTestStartRequest(cp, "op-pg-replay-start")
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, start)
	proposal := goalAsyncTestAwaitApprovalRequest(cp, "op-pg-replay-approval")
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, proposal)
	operation := testAsyncOperationWork()
	external := goalAsyncTestAwaitExternalRequest(t, cp, "op-pg-replay-external", operation, operation.AcceptedAt)
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, external)

	for _, replay := range []goalAsyncTransitionRequest{start, proposal, external} {
		current, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, replay)
		if err != nil {
			t.Fatalf("PostgreSQL replay %s: %v", replay.OperationID, err)
		}
		if outcome != goalAsyncTransitionReplayed || !reflect.DeepEqual(cp, current) {
			t.Fatalf("PostgreSQL replay %s outcome=%q", replay.OperationID, outcome)
		}
	}
	conflict := start
	conflict.ReasonCode = goalAsyncReasonResumed
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, conflict); !errors.Is(err, ErrGoalAsyncReplayConflict) {
		t.Fatalf("PostgreSQL different-hash replay error = %v, want replay conflict", err)
	}
}

func TestPostgresGoalAsyncStoreRejectsCorruptLedger(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-pg-corrupt-start"))
	if _, err := handler.Exec(`UPDATE AgentGoalRuns SET last_transition_hash=$1 WHERE goal_run_id=$2`, strings.Repeat("f", 64), cp.RunID); err != nil {
		t.Fatalf("corrupt PostgreSQL last transition hash: %v", err)
	}
	if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
		t.Fatalf("PostgreSQL corrupt ledger error = %v, want store corrupt", err)
	}
}

func TestPostgresGoalAsyncStorePreservesAcceptanceAndLateCompletion(t *testing.T) {
	store, _, _ := openGoalAsyncPostgresStore(t)
	goalAsyncLateCompletionScenario(t, store)
}

func TestPostgresGoalAsyncStoreEvidenceAndVerification(t *testing.T) {
	store, _, _ := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-pg-evidence-start"))
	expected, request := goalAsyncCriteriaMetRequest(t, cp, testAsyncGoalSpec(), "postgres-evidence-payload", []uint64{42})
	stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request)
	if err != nil {
		t.Fatalf("store PostgreSQL criteria-met transition: %v", err)
	}
	if outcome != goalAsyncTransitionApplied || !reflect.DeepEqual(expected, stored) {
		t.Fatalf("PostgreSQL criteria-met outcome=%q", outcome)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load PostgreSQL criteria-met checkpoint: %v", err)
	}
	if !reflect.DeepEqual(expected, loaded) {
		t.Fatalf("PostgreSQL criteria-met checkpoint differs after reload")
	}
	if loaded.State != goalAsyncCriteriaMet || loaded.Verification == nil ||
		len(loaded.Evidence) != 1 || len(loaded.Verification.EvidenceIDs) != 1 {
		t.Fatalf("PostgreSQL evidence/verification not reconstructed: %+v", loaded)
	}
}
