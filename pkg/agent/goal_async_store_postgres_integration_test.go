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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Status (
		status_sha256 CHAR(64) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMPTZ,
		parent_status_sha256 CHAR(64),
		agent_id TEXT,
		source_id BIGINT,
		operation_type TEXT,
		status TEXT NOT NULL,
		progress NUMERIC,
		details JSONB NOT NULL
	);
	CREATE TABLE IF NOT EXISTS CrawlApprovals (
		approval_id CHAR(64) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		proposal_sha256 CHAR(64) NOT NULL,
		request_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		owner TEXT NOT NULL,
		principal_user_id BIGINT NOT NULL,
		source_id BIGINT NOT NULL,
		operation_type TEXT NOT NULL,
		issuer TEXT NOT NULL,
		issued_by_user_id BIGINT NOT NULL,
		issued_by TEXT NOT NULL,
		issued_by_issuer TEXT NOT NULL,
		approved_by_user_id BIGINT NOT NULL,
		approved_by TEXT NOT NULL,
		approved_by_issuer TEXT NOT NULL,
		approval_sha256 CHAR(64) NOT NULL,
		proposal_expires_at TIMESTAMPTZ NOT NULL,
		approved_at TIMESTAMPTZ NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		revoked_at TIMESTAMPTZ,
		revoked_by_user_id BIGINT,
		revoked_by TEXT,
		revocation_reason TEXT,
		UNIQUE (request_id, run_id, principal_user_id, source_id, operation_type)
	);
	CREATE TABLE IF NOT EXISTS Events (
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMPTZ,
		last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		source_id BIGINT,
		event_sha256 CHAR(64) PRIMARY KEY,
		event_counter INTEGER DEFAULT 1,
		event_name VARCHAR(128),
		event_type VARCHAR(255) NOT NULL,
		event_severity VARCHAR(50) NOT NULL,
		event_timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		event_recurrence VARCHAR(64),
		expires_at TIMESTAMPTZ,
		details JSONB NOT NULL
	);
	CREATE TABLE IF NOT EXISTS CrawlProposals (
		request_id CHAR(64) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		run_id TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		owner TEXT NOT NULL,
		principal_user_id BIGINT NOT NULL,
		source_id BIGINT NOT NULL,
		operation_type TEXT NOT NULL,
		proposal_sha256 CHAR(64) NOT NULL,
		trace_id TEXT NOT NULL DEFAULT '',
		max_requests INTEGER NOT NULL,
		budget_window_seconds BIGINT NOT NULL,
		policy_json TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending',
		expires_at TIMESTAMPTZ NOT NULL,
		submitted_at TIMESTAMPTZ,
		operation_id TEXT
	);`); err != nil {
		t.Fatalf("create disposable crawl observation tables: %v", err)
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

func TestPostgresGoalAsyncStoreCancellationBlocksTransitions(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create PostgreSQL checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-pg-cancel-start"))
	cancelAt := cp.UpdatedAt.Add(time.Second)
	if _, err := handler.Exec(`UPDATE AgentGoalRuns
		SET cancel_requested_at=$1, cancel_requested_by_user_id=7
		WHERE goal_run_id=$2`, cancelAt, cp.RunID); err != nil {
		t.Fatalf("set PostgreSQL cancellation marker: %v", err)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load PostgreSQL pending-cancellation checkpoint: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("audit load changed PostgreSQL checkpoint")
	}
	request := goalAsyncTestAwaitApprovalRequest(cp, "op-pg-cancel-blocked")
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request); !errors.Is(err, ErrGoalAsyncCancellationPending) {
		t.Fatalf("cancelled PostgreSQL transition error = %v, want cancellation pending", err)
	}
	var transitions, work int
	if err := handler.QueryRow(`SELECT
		(SELECT COUNT(*) FROM AgentGoalRunTransitions WHERE goal_run_id=$1),
		(SELECT COUNT(*) FROM AgentGoalRunWork WHERE goal_run_id=$1)`, cp.RunID).Scan(&transitions, &work); err != nil {
		t.Fatalf("count blocked PostgreSQL transition rows: %v", err)
	}
	if transitions != 1 || work != 0 {
		t.Fatalf("blocked PostgreSQL cancellation wrote transitions=%d work=%d", transitions, work)
	}
	var stillCanceled bool
	if err := handler.QueryRow(`SELECT cancel_requested_at IS NOT NULL FROM AgentGoalRuns WHERE goal_run_id=$1`, cp.RunID).Scan(&stillCanceled); err != nil {
		t.Fatalf("read PostgreSQL cancellation marker: %v", err)
	}
	if !stillCanceled {
		t.Fatal("blocked PostgreSQL transition erased cancellation marker")
	}
}

func TestPostgresGoalAsyncStoreRejectsCorruptTransitionLedger(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint)
	}{
		{
			name: "last reason mismatch",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint) {
				if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET reason_code='wrong_reason' WHERE goal_run_id=$1 AND revision=$2`, cp.RunID, cp.Revision); err != nil {
					t.Fatalf("corrupt PostgreSQL last reason: %v", err)
				}
			},
		},
		{
			name: "last timestamp mismatch",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint) {
				if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET created_at=$1 WHERE goal_run_id=$2 AND revision=$3`, cp.UpdatedAt.Add(-time.Microsecond), cp.RunID, cp.Revision); err != nil {
					t.Fatalf("corrupt PostgreSQL last timestamp: %v", err)
				}
			},
		},
		{
			name: "earlier timestamp outside interval",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint) {
				if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET created_at=$1 WHERE goal_run_id=$2 AND revision=2`, cp.CreatedAt.Add(-time.Second), cp.RunID); err != nil {
					t.Fatalf("corrupt PostgreSQL earlier timestamp: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, handler, _ := openGoalAsyncPostgresStore(t)
			cp := testAsyncGoalBaseCheckpoint(t)
			if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
				t.Fatalf("create PostgreSQL checkpoint: %v", err)
			}
			cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-pg-ledger-start"))
			cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitApprovalRequest(cp, "op-pg-ledger-approval"))
			test.mutate(t, handler, cp)
			if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
				t.Fatalf("PostgreSQL corrupt ledger error = %v, want store corrupt", err)
			}
		})
	}
}

func TestPostgresGoalAsyncObservationValidAndExpired(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	original := cloneGoalAsyncCheckpoint(cp)

	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("observe PostgreSQL accepted operation: %v", err)
	}
	if observation.Class != goalAsyncObservationPending || observation.OperationState != string(cdb.CrawlOperationAccepted) {
		t.Fatalf("unexpected PostgreSQL accepted observation: %+v", observation)
	}

	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationProcessing), cdb.CrawlReasonProcessing, testAsyncGoalNow.Add(6*time.Minute))
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(6*time.Minute))
	if err != nil || observation.Class != goalAsyncObservationProcessing {
		t.Fatalf("observe PostgreSQL processing operation = %+v err=%v", observation, err)
	}

	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationCompleted), "crawl_completed", testAsyncGoalNow.Add(7*time.Minute))
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, work.ApprovalExpiresAt.Add(time.Hour))
	if err != nil || observation.Class != goalAsyncObservationReportedCompleted || observation.ApprovalState != "expired" {
		t.Fatalf("observe PostgreSQL completed-after-expiry operation = %+v err=%v", observation, err)
	}
	if !reflect.DeepEqual(original, cp) {
		t.Fatalf("PostgreSQL observation mutated checkpoint")
	}
}

func TestPostgresGoalAsyncObservationRejectsConflict(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	mutateGoalAsyncObservationDetails(t, handler, work, func(details *goalAsyncOperationDetails) {
		details.RunID = "wrong-run"
	})
	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(5*time.Minute))
	if !errors.Is(err, errGoalAsyncObservationConflict) {
		t.Fatalf("PostgreSQL conflicting observation error = %v, want conflict", err)
	}
	if observation.Class != goalAsyncObservationConflict {
		t.Fatalf("PostgreSQL conflicting observation class = %q", observation.Class)
	}
}

func openGoalAsyncEligibilityPostgresFixture(t *testing.T) (*goalAsyncStore, cdb.Handler, goalAsyncCheckpoint, goalAsyncWorkRef) {
	t.Helper()
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp, work := buildGoalAsyncEligibilityFixture(t, store, handler)
	return store, handler, cp, work
}

func TestPostgresGoalAsyncObservationRejectsChronology(t *testing.T) {
	t.Run("accepted before approval issued", func(t *testing.T) {
		store, handler, _ := openGoalAsyncPostgresStore(t)
		cp, work := goalAsyncObservationCheckpoint(t)
		insertGoalAsyncObservationFixture(t, handler, cp, work)
		late := work.AcceptedAt.Add(time.Minute)
		if _, err := handler.Exec(`UPDATE CrawlApprovals SET approved_at=$1 WHERE approval_id=$2`, late, work.ApprovalID); err != nil {
			t.Fatalf("mutate PostgreSQL approval grant time: %v", err)
		}
		mutateGoalAsyncObservationDetails(t, handler, work, func(details *goalAsyncOperationDetails) {
			details.ApprovedAt = late.UTC().Format(time.RFC3339Nano)
		})
		observation, err := observeGoalAsyncTestOperation(t, store, cp, work, late.Add(time.Minute))
		if !errors.Is(err, errGoalAsyncObservationConflict) ||
			observation.ReasonCode != goalAsyncObservationReasonAcceptBeforeApproval {
			t.Fatalf("PostgreSQL chronology observation = %+v err=%v", observation, err)
		}
	})
	t.Run("status updated before created", func(t *testing.T) {
		store, handler, _ := openGoalAsyncPostgresStore(t)
		cp, work := goalAsyncObservationCheckpoint(t)
		insertGoalAsyncObservationFixture(t, handler, cp, work)
		if _, err := handler.Exec(`UPDATE Status SET last_updated_at=$1 WHERE status_sha256=$2`,
			work.AcceptedAt.Add(-time.Second), work.OperationID); err != nil {
			t.Fatalf("mutate PostgreSQL operation timestamp: %v", err)
		}
		observation, err := observeGoalAsyncTestOperation(t, store, cp, work, work.AcceptedAt.Add(time.Minute))
		if !errors.Is(err, errGoalAsyncObservationUncertain) ||
			observation.ReasonCode != goalAsyncObservationReasonTimestampInvalid {
			t.Fatalf("PostgreSQL timestamp observation = %+v err=%v", observation, err)
		}
	})
}

func TestPostgresGoalAsyncResumeEligibility(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligible {
			t.Fatalf("PostgreSQL eligibility decision = %+v err=%v", decision, err)
		}
	})
	t.Run("user disabled", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
			t.Fatalf("disable PostgreSQL user: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied {
			t.Fatalf("PostgreSQL disabled user decision = %+v err=%v", decision, err)
		}
	})
	t.Run("ownership transferred", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		insertGoalAsyncEligibilityUser(t, handler, 8, "other-owner")
		if _, err := handler.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer PostgreSQL source: %v", err)
		}
		if _, err := handler.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer PostgreSQL provenance: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedSourceOwnership {
			t.Fatalf("PostgreSQL transferred source decision = %+v err=%v", decision, err)
		}
	})
	t.Run("agent policy changed", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
		authority := goalAsyncEligibilityAuthorityFor(cp)
		authority.PolicySHA256 = strings.Repeat("f", 64)
		provider.set(authority, true, nil)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedPolicy {
			t.Fatalf("PostgreSQL policy change decision = %+v err=%v", decision, err)
		}
	})
	t.Run("cancellation requested", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE AgentGoalRuns SET cancel_requested_at=CURRENT_TIMESTAMP, cancel_requested_by_user_id=7 WHERE goal_run_id=$1`, cp.RunID); err != nil {
			t.Fatalf("set PostgreSQL cancellation: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedCancelled {
			t.Fatalf("PostgreSQL cancellation decision = %+v err=%v", decision, err)
		}
	})
}

func TestPostgresGoalAsyncEligibilityRealScheduler(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	runGoalAsyncRealSchedulerObservationScenario(t, store, handler)
}

func TestPostgresGoalAsyncResumeEligibilityHardening(t *testing.T) {
	t.Run("disabled source", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=42`); err != nil {
			t.Fatalf("disable PostgreSQL source: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedSourceDisabled {
			t.Fatalf("PostgreSQL disabled source decision = %+v err=%v", decision, err)
		}
	})
	t.Run("auth schema unavailable", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`DROP TABLE Users`); err != nil {
			t.Fatalf("drop PostgreSQL users: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailablePrincipal {
			t.Fatalf("PostgreSQL auth schema decision = %+v err=%v", decision, err)
		}
	})
	t.Run("external issuer mapping", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Users SET external_subject='ext-sub', external_issuer='https://issuer.example/' WHERE user_id=7`); err != nil {
			t.Fatalf("map PostgreSQL external identity: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedPrincipalExternal {
			t.Fatalf("PostgreSQL external identity decision = %+v err=%v", decision, err)
		}
	})
	t.Run("deadline during evaluation", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilityPostgresFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
		clockValue := work.AcceptedAt.Add(time.Minute)
		eligibility.clock = func() time.Time { return clockValue }
		var once sync.Once
		provider.onCall = func() {
			once.Do(func() { clockValue = cp.Deadline.Add(time.Second) })
		}
		decision, err := eligibility.Evaluate(context.Background(), cp.RunID, work.OperationID)
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedDeadline {
			t.Fatalf("PostgreSQL deadline decision = %+v err=%v", decision, err)
		}
	})
}

func TestPostgresGoalAsyncSourceCompletionIsNotOperationCompletion(t *testing.T) {
	store, handler, _ := openGoalAsyncPostgresStore(t)
	cp, work, acceptedAt := prepareGoalAsyncRealSchedulerOperation(t, store, handler)
	ctx := context.Background()
	if _, err := handler.Exec(`UPDATE Sources SET status='completed', last_updated_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
		t.Fatalf("mark PostgreSQL source completed: %v", err)
	}
	eventID := goalAsyncEligibilityHex("pg-source-level-crawl-completed")
	eventQuery := `INSERT INTO Events(event_sha256, source_id, event_type, event_severity, details)
		VALUES ($1, $2, 'crawl_completed', 'info', $3::jsonb)`
	if _, err := handler.Exec(eventQuery, eventID, 42, "{}"); err != nil {
		t.Fatalf("insert PostgreSQL source-level event: %v", err)
	}
	otherOperationID := goalAsyncEligibilityHex("pg-other-operation")
	otherQuery := `INSERT INTO Status(status_sha256, agent_id, source_id, operation_type, status, details, created_at, last_updated_at)
		VALUES ($1, $2, $3, 'crawl', 'completed', '{}'::jsonb, $4, $5)`
	if _, err := handler.Exec(otherQuery, otherOperationID, cp.AgentID, 42, acceptedAt, acceptedAt); err != nil {
		t.Fatalf("insert PostgreSQL unrelated completed operation: %v", err)
	}
	observation, err := store.observeCrawlOperationAt(ctx, cp, work.OperationID, acceptedAt.Add(time.Second))
	if err != nil || observation.Class != goalAsyncObservationPending {
		t.Fatalf("PostgreSQL source-level activity changed observation = %+v err=%v", observation, err)
	}
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
	decision, err := eligibility.evaluateAt(ctx, cp.RunID, work.OperationID, acceptedAt.Add(2*time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeWaiting {
		t.Fatalf("PostgreSQL source-level activity produced eligibility = %+v err=%v", decision, err)
	}
}
