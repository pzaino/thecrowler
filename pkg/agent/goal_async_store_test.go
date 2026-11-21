// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func goalAsyncSQLiteConfig(path string) cfg.Config {
	return cfg.Config{Database: cfg.Database{
		Type:               cdb.DBSQLiteStr,
		DBName:             path,
		TransactionTimeout: 30,
	}}
}

func openGoalAsyncSQLiteHandler(t *testing.T, path string) cdb.Handler {
	t.Helper()
	config := goalAsyncSQLiteConfig(path)
	handler, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("create SQLite handler: %v", err)
	}
	if err := handler.Connect(config); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return handler
}

func openGoalAsyncSQLiteStore(t *testing.T) (*goalAsyncStore, cdb.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "goal-store.db")
	setup, err := os.ReadFile("../database/sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	raw, err := sql.Open(cdb.DBSQLiteStr, path+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open SQLite setup database: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(string(setup)); err != nil {
		_ = raw.Close()
		t.Fatalf("apply SQLite setup: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close SQLite setup database: %v", err)
	}
	handler := openGoalAsyncSQLiteHandler(t, path)
	store, err := newGoalAsyncStore(context.Background(), &handler)
	if err != nil {
		t.Fatalf("construct goal store: %v", err)
	}
	return store, handler, path
}

func goalAsyncTestStartRequest(cp goalAsyncCheckpoint, operationID string) goalAsyncTransitionRequest {
	return goalAsyncTransitionRequest{
		OperationID:      operationID,
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionStart,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonStarted,
	}
}

func goalAsyncTestAwaitApprovalRequest(cp goalAsyncCheckpoint, operationID string) goalAsyncTransitionRequest {
	work := testAsyncProposalWork()
	return goalAsyncTransitionRequest{
		OperationID:      operationID,
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &goalAsyncUsed{ScheduledWorkRequests: 1},
		Work:             &work,
	}
}

func goalAsyncTestAwaitExternalRequest(t *testing.T, cp goalAsyncCheckpoint, operationID string, work goalAsyncWorkRef, observedAt time.Time) goalAsyncTransitionRequest {
	t.Helper()
	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, observedAt)
	return goalAsyncTransitionRequest{
		OperationID:      operationID,
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitExternalWork,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingExternalWork,
		Used:             &goalAsyncUsed{ScheduledWorkRequests: 1},
		Scheduling:       &scheduling,
	}
}

func applyAndStoreGoalAsyncTransition(t *testing.T, store *goalAsyncStore, cp goalAsyncCheckpoint, request goalAsyncTransitionRequest) goalAsyncCheckpoint {
	t.Helper()
	next, _, err := applyGoalAsyncTransition(cp, request)
	if err != nil {
		t.Fatalf("apply transition %s: %v", request.Kind, err)
	}
	stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request)
	if err != nil {
		t.Fatalf("store transition %s: %v", request.Kind, err)
	}
	if outcome != goalAsyncTransitionApplied {
		t.Fatalf("store transition %s outcome = %q, want applied", request.Kind, outcome)
	}
	if !reflect.DeepEqual(next, stored) {
		t.Fatalf("stored transition %s differs from pure transition", request.Kind)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load transition %s: %v", request.Kind, err)
	}
	if !reflect.DeepEqual(next, loaded) {
		t.Fatalf("loaded transition %s differs from pure transition", request.Kind)
	}
	return next
}

func TestGoalAsyncStoreSQLiteRoundTripAfterRestart(t *testing.T) {
	store, handler, path := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("loaded checkpoint differs from created checkpoint")
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close SQLite handler: %v", err)
	}

	handler = openGoalAsyncSQLiteHandler(t, path)
	reopened, err := newGoalAsyncStore(context.Background(), &handler)
	if err != nil {
		t.Fatalf("reconstruct goal store: %v", err)
	}
	loaded, err = reopened.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after restart: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("checkpoint changed after database restart")
	}
	if !loaded.Deadline.Equal(cp.Deadline) {
		t.Fatalf("deadline changed after restart: %s != %s", loaded.Deadline, cp.Deadline)
	}
}

func TestGoalAsyncStoreSQLiteLifecycleAndBudgets(t *testing.T) {
	store, handler, path := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	start := goalAsyncTestStartRequest(cp, "op-store-start")
	start.AuditRefs = []string{"audit-store-start"}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, start)
	if len(cp.AuditRefs) != 1 || cp.AuditRefs[0] != "audit-store-start" {
		t.Fatalf("audit reference was not applied: %+v", cp.AuditRefs)
	}
	if cp.State != goalAsyncRunning || cp.Slices != 1 {
		t.Fatalf("unexpected running checkpoint: state=%s slices=%d", cp.State, cp.Slices)
	}

	proposal := goalAsyncTestAwaitApprovalRequest(cp, "op-store-await-approval")
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, proposal)
	if cp.State != goalAsyncAwaitingApproval || len(cp.Work) != 1 ||
		cp.Work[0].Kind != goalAsyncWorkCrawlProposal {
		t.Fatalf("unexpected awaiting-approval checkpoint: %+v", cp)
	}

	operation := testAsyncOperationWork()
	external := goalAsyncTestAwaitExternalRequest(t, cp, "op-store-await-external", operation, operation.AcceptedAt)
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, external)
	if cp.State != goalAsyncAwaitingExternalWork || len(cp.Work) != 1 ||
		cp.Work[0].Kind != goalAsyncWorkCrawlOperation {
		t.Fatalf("unexpected awaiting-external checkpoint: %+v", cp)
	}

	if err := handler.Close(); err != nil {
		t.Fatalf("close SQLite handler: %v", err)
	}
	handler = openGoalAsyncSQLiteHandler(t, path)
	reopened, err := newGoalAsyncStore(context.Background(), &handler)
	if err != nil {
		t.Fatalf("reconstruct goal store: %v", err)
	}
	loaded, err := reopened.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after lifecycle restart: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("lifecycle checkpoint changed after restart")
	}
	if loaded.Used.ScheduledWorkRequests != 1 || loaded.Slices != 1 {
		t.Fatalf("budget counters reset after restart: %+v", loaded.Used)
	}
	if !loaded.Deadline.Equal(cp.Deadline) {
		t.Fatalf("deadline reset after restart")
	}
}

func TestGoalAsyncStoreSQLiteReplayAndConflicts(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	start := goalAsyncTestStartRequest(cp, "op-store-replay-start")
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, start)
	proposal := goalAsyncTestAwaitApprovalRequest(cp, "op-store-replay-approval")
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, proposal)
	operation := testAsyncOperationWork()
	external := goalAsyncTestAwaitExternalRequest(t, cp, "op-store-replay-external", operation, operation.AcceptedAt)
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, external)

	for _, replay := range []goalAsyncTransitionRequest{start, proposal, external} {
		current, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, replay)
		if err != nil {
			t.Fatalf("replay %s: %v", replay.OperationID, err)
		}
		if outcome != goalAsyncTransitionReplayed {
			t.Fatalf("replay %s outcome = %q, want replayed", replay.OperationID, outcome)
		}
		if !reflect.DeepEqual(cp, current) {
			t.Fatalf("replay %s returned a different current checkpoint", replay.OperationID)
		}
	}

	conflict := start
	conflict.ReasonCode = goalAsyncReasonResumed
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, conflict); !errors.Is(err, ErrGoalAsyncReplayConflict) {
		t.Fatalf("different-hash replay error = %v, want replay conflict", err)
	}
	stale := goalAsyncTestStartRequest(cp, "op-store-stale-revision")
	stale.ExpectedRevision = cp.Revision - 1
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, stale); !errors.Is(err, ErrGoalAsyncRevisionConflict) {
		t.Fatalf("stale revision error = %v, want revision conflict", err)
	}
}

func TestGoalAsyncStoreSQLiteReconcilesCommittedOutcome(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	if err := store.reconcileCreateAfterError(context.Background(), cp, errors.New("simulated create acknowledgement loss")); err != nil {
		t.Fatalf("reconcile create: %v", err)
	}
	request := goalAsyncTestStartRequest(cp, "op-store-reconcile-start")
	next := applyAndStoreGoalAsyncTransition(t, store, cp, request)
	reconciled, outcome, err := store.reconcileTransitionAfterError(context.Background(), cp.RunID, request, errors.New("simulated commit acknowledgement loss"))
	if err != nil {
		t.Fatalf("reconcile transition: %v", err)
	}
	if outcome != goalAsyncTransitionReplayed || !reflect.DeepEqual(next, reconciled) {
		t.Fatalf("reconciled transition outcome=%q", outcome)
	}
}

func TestGoalAsyncStoreSQLiteCancellationBlocksTransitions(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-cancel-start"))
	cancelAt := cp.UpdatedAt.Add(time.Second)
	if _, err := handler.Exec(`UPDATE AgentGoalRuns
		SET cancel_requested_at=?, cancel_requested_by_user_id=7
		WHERE goal_run_id=?`, cancelAt, cp.RunID); err != nil {
		t.Fatalf("set cancellation marker: %v", err)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load pending-cancellation checkpoint: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("audit load changed checkpoint")
	}

	request := goalAsyncTestAwaitApprovalRequest(cp, "op-store-cancel-blocked")
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request); !errors.Is(err, ErrGoalAsyncCancellationPending) {
		t.Fatalf("cancelled transition error = %v, want cancellation pending", err)
	}
	terminal := goalAsyncTransitionRequest{
		OperationID:      "op-store-cancel-terminal",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionCancelled,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonCancelled,
	}
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, terminal); !errors.Is(err, ErrGoalAsyncCancellationPending) {
		t.Fatalf("terminal transition error = %v, want cancellation pending", err)
	}
	after, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after blocked transitions: %v", err)
	}
	if !reflect.DeepEqual(cp, after) {
		t.Fatalf("blocked transitions changed checkpoint")
	}
	var transitions, work int
	if err := handler.QueryRow(`SELECT
		(SELECT COUNT(*) FROM AgentGoalRunTransitions WHERE goal_run_id=?),
		(SELECT COUNT(*) FROM AgentGoalRunWork WHERE goal_run_id=?)`, cp.RunID, cp.RunID).Scan(&transitions, &work); err != nil {
		t.Fatalf("count blocked transition rows: %v", err)
	}
	if transitions != 1 || work != 0 {
		t.Fatalf("blocked cancellation wrote transitions=%d work=%d", transitions, work)
	}
	var stillCanceled bool
	if err := handler.QueryRow(`SELECT cancel_requested_at IS NOT NULL FROM AgentGoalRuns WHERE goal_run_id=?`, cp.RunID).Scan(&stillCanceled); err != nil {
		t.Fatalf("read cancellation marker: %v", err)
	}
	if !stillCanceled {
		t.Fatal("blocked transition erased cancellation marker")
	}
}

func TestGoalAsyncStoreSQLiteCancellationCASFence(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	request := goalAsyncTestStartRequest(cp, "op-store-cas-start")
	next, _, err := applyGoalAsyncTransition(cp, request)
	if err != nil {
		t.Fatalf("apply transition: %v", err)
	}
	if _, err := handler.Exec(`UPDATE AgentGoalRuns
		SET cancel_requested_at=?, cancel_requested_by_user_id=7
		WHERE goal_run_id=?`, cp.UpdatedAt.Add(time.Second), cp.RunID); err != nil {
		t.Fatalf("set cancellation marker: %v", err)
	}
	tx, err := handler.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin CAS transaction: %v", err)
	}
	err = store.updateRoot(context.Background(), tx, cp, next)
	_ = tx.Rollback()
	if !errors.Is(err, ErrGoalAsyncCancellationPending) {
		t.Fatalf("CAS fence error = %v, want cancellation pending", err)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after CAS fence: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("CAS fence changed checkpoint")
	}
}

func TestGoalAsyncStoreSQLiteAtomicRollback(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-atomic-start"))

	if _, err := handler.Exec(`CREATE TRIGGER goal_async_store_fail_transition
		BEFORE INSERT ON AgentGoalRunTransitions
		WHEN NEW.operation_id = 'op-store-atomic-fail'
		BEGIN
			SELECT RAISE(ABORT, 'injected transition failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	request := goalAsyncTestAwaitApprovalRequest(cp, "op-store-atomic-fail")
	if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request); err == nil {
		t.Fatal("transition unexpectedly succeeded")
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after failed transition: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("failed transition left partial state")
	}
	if len(loaded.Work) != 0 {
		t.Fatalf("failed transition left work rows: %+v", loaded.Work)
	}
}

func TestGoalAsyncStoreSQLiteConcurrentWriter(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	start := make(chan struct{})
	type result struct {
		cp      goalAsyncCheckpoint
		outcome goalAsyncTransitionOutcome
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, operationID := range []string{"op-store-concurrent-a", "op-store-concurrent-b"} {
		operationID := operationID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, goalAsyncTestStartRequest(cp, operationID))
			results <- result{cp: stored, outcome: outcome, err: err}
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
				t.Fatalf("successful concurrent transition outcome = %q", result.outcome)
			}
			applied++
		case errors.Is(result.err, ErrGoalAsyncRevisionConflict), errors.Is(result.err, errGoalAsyncStoreConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent transition error: %v", result.err)
		}
	}
	if applied != 1 || conflicted != 1 {
		t.Fatalf("concurrent transitions applied=%d conflicted=%d, want 1/1", applied, conflicted)
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load after concurrent transitions: %v", err)
	}
	if loaded.Revision != cp.Revision+1 || loaded.Slices != 1 {
		t.Fatalf("concurrent transition lost state: revision=%d slices=%d", loaded.Revision, loaded.Slices)
	}
	var transitions int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM AgentGoalRunTransitions WHERE goal_run_id=?`, cp.RunID).Scan(&transitions); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if transitions != 1 {
		t.Fatalf("transition count = %d, want 1", transitions)
	}
}

func TestGoalAsyncStoreSQLiteRejectsCorruptState(t *testing.T) {
	t.Run("revision without ledger", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		if _, err := handler.Exec(`UPDATE AgentGoalRuns
			SET revision=99, last_transition_id='op-fake-ledger-row', last_transition_hash=?
			WHERE goal_run_id=?`, strings.Repeat("f", 64), cp.RunID); err != nil {
			t.Fatalf("corrupt revision: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load corrupt revision error = %v, want store corrupt", err)
		}
	})

	t.Run("last transition hash mismatch", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-start"))
		if _, err := handler.Exec(`UPDATE AgentGoalRuns SET last_transition_hash=? WHERE goal_run_id=?`, strings.Repeat("f", 64), cp.RunID); err != nil {
			t.Fatalf("corrupt last transition hash: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load corrupt transition hash error = %v, want store corrupt", err)
		}
	})

	t.Run("work contradicts root state", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-work-start"))
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitApprovalRequest(cp, "op-store-corrupt-work-approval"))
		acceptedAt := cp.UpdatedAt.Add(-time.Second)
		submitBy := cp.UpdatedAt
		approvalExpires := cp.UpdatedAt.Add(time.Hour)
		if _, err := handler.Exec(`UPDATE AgentGoalRunWork
			SET kind='crawl_operation', state='completed', approval_id=?, approval_sha256=?,
				operation_id=?, approval_expires_at=?, submit_by=?, accepted_at=?,
				observed_at=?, updated_at=?
			WHERE goal_run_id=?`,
			strings.Repeat("8", 64), strings.Repeat("9", 64), strings.Repeat("a", 64),
			approvalExpires, submitBy, acceptedAt, cp.UpdatedAt, cp.UpdatedAt, cp.RunID); err != nil {
			t.Fatalf("corrupt work row: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load contradictory work error = %v, want store corrupt", err)
		}
	})

	t.Run("last transition reason mismatch", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-reason"))
		if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET reason_code='wrong_reason' WHERE goal_run_id=? AND revision=?`, cp.RunID, cp.Revision); err != nil {
			t.Fatalf("corrupt last transition reason: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load corrupt reason error = %v, want store corrupt", err)
		}
	})

	t.Run("last transition timestamp mismatch", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-time"))
		if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET created_at=? WHERE goal_run_id=? AND revision=?`, cp.UpdatedAt.Add(-time.Microsecond), cp.RunID, cp.Revision); err != nil {
			t.Fatalf("corrupt last transition timestamp: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load corrupt timestamp error = %v, want store corrupt", err)
		}
	})

	t.Run("earlier transition timestamp outside interval", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-earlier-start"))
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitApprovalRequest(cp, "op-store-corrupt-earlier-approval"))
		operation := testAsyncOperationWork()
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitExternalRequest(t, cp, "op-store-corrupt-earlier-external", operation, operation.AcceptedAt))
		if _, err := handler.Exec(`UPDATE AgentGoalRunTransitions SET created_at=? WHERE goal_run_id=? AND revision=2`, cp.CreatedAt.Add(-time.Second), cp.RunID); err != nil {
			t.Fatalf("corrupt earlier transition timestamp: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load earlier timestamp error = %v, want store corrupt", err)
		}
	})

	t.Run("malformed evidence child", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-corrupt-evidence"))
		if _, err := handler.Exec(`INSERT INTO AgentGoalRunEvidence
			(goal_run_id, evidence_id, kind, trust_class, producer, source_ids,
			 observed_at, expires_at, payload_sha256, record_sha256, size_bytes, created_at)
			VALUES (?, ?, 'tool_observation', 'tool_observation', 'tool:test', '["not-a-number"]',
			 ?, ?, ?, ?, 128, ?)`,
			cp.RunID, "ev_"+strings.Repeat("a", 32), cp.CreatedAt,
			cp.CreatedAt.Add(time.Minute), strings.Repeat("b", 64),
			strings.Repeat("c", 64), cp.UpdatedAt); err != nil {
			t.Fatalf("insert malformed evidence: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load malformed evidence error = %v, want store corrupt", err)
		}
	})

	t.Run("unsupported checkpoint version", func(t *testing.T) {
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp := testAsyncGoalBaseCheckpoint(t)
		if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
			t.Fatalf("create checkpoint: %v", err)
		}
		if _, err := handler.Exec(`PRAGMA ignore_check_constraints=ON;
			UPDATE AgentGoalRuns SET checkpoint_version=3 WHERE goal_run_id=?;
			PRAGMA ignore_check_constraints=OFF`, cp.RunID); err != nil {
			t.Fatalf("corrupt checkpoint version: %v", err)
		}
		if _, err := store.LoadCheckpoint(context.Background(), cp.RunID); !errors.Is(err, errGoalAsyncStoreCorrupt) {
			t.Fatalf("load unsupported version error = %v, want store corrupt", err)
		}
	})
}

func goalAsyncLateCompletionScenario(t *testing.T, store *goalAsyncStore) {
	t.Helper()
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-expiry-start"))
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitApprovalRequest(cp, "op-store-expiry-approval"))

	acceptedAt := testAsyncGoalNow.Add(2 * time.Second)
	approvalExpiresAt := testAsyncGoalNow.Add(4 * time.Second)
	submitBy := testAsyncGoalNow.Add(3 * time.Second)
	operation := testAsyncOperationWorkAt(acceptedAt)
	operation.ProposalExpiresAt = testAsyncGoalNow.Add(5 * time.Minute)
	operation.ApprovalExpiresAt = approvalExpiresAt
	operation.SubmitBy = submitBy
	cp = applyAndStoreGoalAsyncTransition(t, store, cp,
		goalAsyncTestAwaitExternalRequest(t, cp, "op-store-expiry-external", operation, acceptedAt))

	completed := operation
	completed.State = goalAsyncWorkCompleted
	completed.ObservedAt = testAsyncGoalNow.Add(6 * time.Minute)
	grant := testAsyncResumeGrant(t, cp, goalAsyncWorkCompleted)
	resumeNow := testAsyncGoalNow.Add(7 * time.Minute)
	resume := goalAsyncTransitionRequest{
		OperationID:      "op-store-expiry-complete",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionResume,
		Now:              resumeNow,
		ReasonCode:       goalAsyncReasonResumed,
		Work:             &completed,
		Resume:           &grant,
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, resume)

	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load accepted/late-completion checkpoint: %v", err)
	}
	if !loaded.Deadline.Equal(testAsyncGoalNow.Add(10 * time.Minute)) {
		t.Fatalf("logical deadline changed: %s", loaded.Deadline)
	}
	if loaded.State != goalAsyncRunning || len(loaded.Work) != 1 ||
		loaded.Work[0].State != goalAsyncWorkCompleted {
		t.Fatalf("late completion not represented: %+v", loaded)
	}
	if !loaded.Work[0].AcceptedAt.Before(loaded.Work[0].ApprovalExpiresAt) {
		t.Fatalf("accepted_at is not before approval expiry")
	}
	if !loaded.Work[0].ObservedAt.After(loaded.Work[0].ApprovalExpiresAt) {
		t.Fatalf("observed_at did not follow approval expiry")
	}
}

func TestGoalAsyncStoreSQLiteAllowsEqualTransitionTimestamps(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-equal-start"))
	work := testAsyncProposalWork()
	request := goalAsyncTransitionRequest{
		OperationID:      "op-store-equal-approval",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt,
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &goalAsyncUsed{ScheduledWorkRequests: 1},
		Work:             &work,
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, request)
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load equal-timestamp checkpoint: %v", err)
	}
	if !reflect.DeepEqual(cp, loaded) {
		t.Fatalf("equal-timestamp checkpoint changed")
	}
}

func TestGoalAsyncStoreSQLitePreservesAcceptanceAndLateCompletion(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	goalAsyncLateCompletionScenario(t, store)
}

func TestGoalAsyncStoreSQLiteEvidenceWithoutSources(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-no-source-start"))
	_, request := goalAsyncCriteriaMetRequest(t, cp, testAsyncGoalSpec(), "no-source-payload", []uint64{42})
	request.Evidence[0].SourceIDs = nil
	if _, _, err := applyGoalAsyncTransition(cp, request); err != nil {
		t.Fatalf("apply evidence without source IDs: %v", err)
	}
	stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request)
	if err != nil {
		t.Fatalf("store evidence without source IDs: %v", err)
	}
	if outcome != goalAsyncTransitionApplied || stored.State != goalAsyncCriteriaMet || len(stored.Evidence) != 1 {
		t.Fatalf("evidence without source IDs outcome=%q state=%s evidence=%d", outcome, stored.State, len(stored.Evidence))
	}
	loaded, err := store.LoadCheckpoint(context.Background(), cp.RunID)
	if err != nil {
		t.Fatalf("load evidence without source IDs: %v", err)
	}
	if len(loaded.Evidence) != 1 || len(loaded.Evidence[0].SourceIDs) != 0 {
		t.Fatalf("empty evidence source IDs were not preserved: %+v", loaded.Evidence)
	}
}

func TestGoalAsyncStoreSQLiteNeverPersistsRawContent(t *testing.T) {
	store, handler, path := openGoalAsyncSQLiteStore(t)
	spec := testAsyncGoalSpec()
	spec.Auth = "goal-store-secret-credential"
	spec.Prompt = "goal-store-secret-prompt"
	spec.Messages = []any{map[string]any{"role": "user", "content": "goal-store-secret-message"}}
	spec.SpecHash = computeGoalSpecHash(spec)
	cp, err := newGoalAsyncCheckpoint(
		spec, testAsyncGoalIdentity(), testAsyncGoalPrincipal(),
		"run-goal-store-secrets", "trace-goal-store-secrets", "",
		"ledger-goal-store-secrets", strings.Repeat("b", 64),
		testAsyncGoalNow, testAsyncGoalNow.Add(10*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new secret checkpoint: %v", err)
	}
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create secret checkpoint: %v", err)
	}

	secretPayload := "goal-store-secret-evidence-payload"
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-store-secret-start"))
	expected, criteria := goalAsyncCriteriaMetRequest(t, cp, spec, secretPayload, []uint64{42})
	stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, criteria)
	if err != nil {
		t.Fatalf("store criteria-met checkpoint: %v", err)
	}
	if outcome != goalAsyncTransitionApplied || stored.State != goalAsyncCriteriaMet {
		t.Fatalf("criteria-met transition outcome=%q state=%s", outcome, stored.State)
	}
	if !reflect.DeepEqual(expected, stored) {
		t.Fatalf("stored criteria-met checkpoint differs from pure transition")
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close SQLite handler: %v", err)
	}

	matches, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatalf("glob SQLite files: %v", err)
	}
	for _, file := range matches {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, secret := range []string{
			spec.Auth, spec.Prompt, "goal-store-secret-message", secretPayload,
		} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("%s persisted raw content %q", file, secret)
			}
		}
	}
}

func goalAsyncCriteriaMetRequest(t *testing.T, cp goalAsyncCheckpoint, spec *AgentGoalSpec, payloadSecret string, sourceIDs []uint64) (goalAsyncCheckpoint, goalAsyncTransitionRequest) {
	t.Helper()
	observedAt := cp.UpdatedAt.Add(time.Second)
	record := testAsyncEvidenceRecord(cp, observedAt)
	record.SourceIDs = append([]uint64(nil), sourceIDs...)
	record.Payload = map[string]any{"raw": payloadSecret}
	encoded, err := json.Marshal(record.Payload)
	if err != nil {
		t.Fatalf("encode evidence payload: %v", err)
	}
	sum := sha256.Sum256(encoded)
	record.PayloadSHA256 = hex.EncodeToString(sum[:])
	record.RecordSHA256 = recordHash(record)
	input := GoalEvalInput{
		Spec: spec, Records: []EvidenceRecord{record}, Now: observedAt,
		LedgerID: cp.LedgerID, RunID: cp.RunID, GoalID: cp.GoalID,
		GoalSpecHash: cp.GoalSpecHash, RunNonceHash: cp.RunNonceHash,
		AgentID: cp.AgentID, Owner: cp.Owner,
	}
	decision, err := newGoalAsyncVerifierDecision(cp, input)
	if err != nil {
		t.Fatalf("verifier decision: %v", err)
	}
	ref := testAsyncEvidenceRef(record)
	used := cp.Used
	used.ModelRounds = 1
	used.ToolAttempts = 1
	used.ResultBytes = 128
	used.EvidenceRecords = 1
	used.EvidenceBytes = 128
	request := goalAsyncTransitionRequest{
		OperationID:      "op-store-criteria-met",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              observedAt,
		Used:             &used,
		Evidence:         []goalAsyncEvidenceRef{ref},
		Verifier:         &decision,
	}
	next, _, err := applyGoalAsyncTransition(cp, request)
	if err != nil {
		t.Fatalf("criteria-met transition: %v", err)
	}
	return next, request
}

type goalAsyncMySQLHandler struct{ cdb.Handler }

func (goalAsyncMySQLHandler) DBMS() string { return cdb.DBMySQLStr }

func TestGoalAsyncStoreRejectsUnsupportedBackend(t *testing.T) {
	var handler cdb.Handler = goalAsyncMySQLHandler{}
	if _, err := newGoalAsyncStore(context.Background(), &handler); !errors.Is(err, cdb.ErrAsyncGoalPersistenceUnsupported) {
		t.Fatalf("MySQL store error = %v, want unsupported", err)
	}
}

func TestGoalAsyncStoreRejectsMissingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	handler := openGoalAsyncSQLiteHandler(t, path)
	if _, err := newGoalAsyncStore(context.Background(), &handler); !errors.Is(err, cdb.ErrAsyncGoalPersistenceSchemaUnavailable) {
		t.Fatalf("missing schema error = %v, want schema unavailable", err)
	}
}

func TestGoalAsyncStoreSQLiteCreateIdempotency(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	conflict := cp
	conflict.TraceID = "trace-goal-store-conflict"
	if err := store.CreateCheckpoint(context.Background(), conflict); !errors.Is(err, errGoalAsyncStoreConflict) {
		t.Fatalf("incompatible create error = %v, want store conflict", err)
	}
}

func TestGoalAsyncStoreSQLiteRejectsMalformedCreate(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	cp := testAsyncGoalBaseCheckpoint(t)
	cp.AllowedTools = []string{"duplicate", "duplicate"}
	if err := store.CreateCheckpoint(context.Background(), cp); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("malformed create error = %v, want checkpoint invalid", err)
	}
}

func TestGoalAsyncStoreSQLiteNotFound(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	if _, err := store.LoadCheckpoint(context.Background(), "missing-run"); !errors.Is(err, errGoalAsyncStoreNotFound) {
		t.Fatalf("missing run error = %v, want not found", err)
	}
	if _, _, err := store.TransitionCheckpoint(context.Background(), "missing-run", goalAsyncTestStartRequest(testAsyncGoalBaseCheckpoint(t), "op-store-missing")); !errors.Is(err, errGoalAsyncStoreNotFound) {
		t.Fatalf("missing transition run error = %v, want not found", err)
	}
}

func TestGoalAsyncStoreSQLiteRejectsInvalidRunID(t *testing.T) {
	store, _, _ := openGoalAsyncSQLiteStore(t)
	if _, err := store.LoadCheckpoint(context.Background(), "bad run id"); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("invalid run ID error = %v, want checkpoint invalid", err)
	}
	if _, _, err := store.TransitionCheckpoint(context.Background(), "bad run id", goalAsyncTransitionRequest{}); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("invalid transition run ID error = %v, want checkpoint invalid", err)
	}
}

func TestGoalAsyncStoreHasNoExecutionImports(t *testing.T) {
	data, err := os.ReadFile("goal_async_store.go")
	if err != nil {
		t.Fatalf("read goal store: %v", err)
	}
	source := string(data)
	start := strings.Index(source, "import (")
	end := strings.Index(source[start:], "\n)")
	if start < 0 || end < 0 {
		t.Fatal("goal store import block not found")
	}
	imports := source[start : start+end]
	for _, forbidden := range []string{
		"net/http", "os/exec", "pkg/crawler", "pkg/browser", "pkg/vdi",
		"services/", "github.com/modelcontextprotocol",
	} {
		if strings.Contains(imports, forbidden) {
			t.Fatalf("goal store imports execution package %q", forbidden)
		}
	}
}
