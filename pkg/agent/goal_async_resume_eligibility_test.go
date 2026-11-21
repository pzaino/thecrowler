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
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type goalAsyncFakeAuthorityProvider struct {
	mu        sync.Mutex
	authority goalAsyncAgentAuthority
	found     bool
	err       error
	onCall    func()
	calls     int
}

func (p *goalAsyncFakeAuthorityProvider) CurrentGoalAsyncAgentAuthority(ctx context.Context, agentID, goalID, goalSpecHash string) (goalAsyncAgentAuthority, bool, error) {
	p.mu.Lock()
	p.calls++
	authority, found, providerErr, onCall := p.authority, p.found, p.err, p.onCall
	p.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return goalAsyncAgentAuthority{}, false, err
		}
	}
	return authority, found, providerErr
}

func (p *goalAsyncFakeAuthorityProvider) set(authority goalAsyncAgentAuthority, found bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authority, p.found, p.err = authority, found, err
}

func (p *goalAsyncFakeAuthorityProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func goalAsyncEligibilityAuthorityFor(cp goalAsyncCheckpoint) goalAsyncAgentAuthority {
	return goalAsyncAgentAuthority{
		Enabled:        true,
		AgentID:        cp.AgentID,
		Owner:          cp.Owner,
		GoalID:         cp.GoalID,
		GoalSpecHash:   cp.GoalSpecHash,
		PolicySHA256:   cp.PolicySHA256,
		AllowedTools:   append([]string(nil), cp.AllowedTools...),
		AllowedSources: append([]uint64(nil), cp.AllowedSources...),
		TrustLevel:     "trusted",
		Capabilities:   []string{"goal_execution"},
	}
}

func ensureGoalAsyncEligibilityAuthTables(t *testing.T, db cdb.Handler) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Users (
		user_id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_at TIMESTAMP,
		external_subject TEXT,
		external_issuer TEXT,
		credential_version INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS AuthRoles (
		role_id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	);
	CREATE TABLE IF NOT EXISTS UserRoles (
		user_id INTEGER NOT NULL,
		role_id INTEGER NOT NULL,
		PRIMARY KEY (user_id, role_id)
	);
	CREATE TABLE IF NOT EXISTS AuthScopes (
		scope_id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	);
	CREATE TABLE IF NOT EXISTS UserScopes (
		user_id INTEGER NOT NULL,
		scope_id INTEGER NOT NULL,
		PRIMARY KEY (user_id, scope_id)
	);
	CREATE TABLE IF NOT EXISTS RoleScopes (
		role_id INTEGER NOT NULL,
		scope_id INTEGER NOT NULL,
		PRIMARY KEY (role_id, scope_id)
	);`); err != nil {
		t.Fatalf("create eligibility auth tables: %v", err)
	}
}

func insertGoalAsyncEligibilityUser(t *testing.T, db cdb.Handler, userID uint64, username string) {
	t.Helper()
	p := func(n int) string { return goalAsyncObservationPlaceholder(db, n) }
	query := `INSERT INTO Users(user_id, username, disabled) VALUES (` + p(1) + `, ` + p(2) + `, FALSE)`
	if _, err := db.Exec(query, userID, username); err != nil {
		t.Fatalf("insert eligibility user %d: %v", userID, err)
	}
}

func grantGoalAsyncEligibilityAdmin(t *testing.T, db cdb.Handler, userID uint64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin')`); err != nil {
		t.Fatalf("insert eligibility admin role: %v", err)
	}
	p := func(n int) string { return goalAsyncObservationPlaceholder(db, n) }
	if _, err := db.Exec(`INSERT INTO UserRoles(user_id, role_id) VALUES (`+p(1)+`, 1)`, userID); err != nil {
		t.Fatalf("grant eligibility admin role: %v", err)
	}
}

func insertGoalAsyncEligibilitySource(t *testing.T, db cdb.Handler, sourceID, ownerID uint64) {
	t.Helper()
	p := func(n int) string { return goalAsyncObservationPlaceholder(db, n) }
	query := `INSERT INTO Sources(source_id, source_uid, url, name, status, disabled, usr_id)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, 'eligibility fixture', 'completed', FALSE, ` + p(4) + `)`
	if _, err := db.Exec(query, sourceID, fmt.Sprintf("source-uid-%d", sourceID), fmt.Sprintf("https://fixture-%d.example/", sourceID), ownerID); err != nil {
		t.Fatalf("insert eligibility source %d: %v", sourceID, err)
	}
	if _, err := db.Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (`+p(1)+`, `+p(2)+`, CURRENT_TIMESTAMP, 9, 'operator', 'eligibility_test', 'eligibility')`,
		sourceID, ownerID); err != nil {
		t.Fatalf("insert eligibility ownership %d: %v", sourceID, err)
	}
}

func buildGoalAsyncEligibilityFixture(t *testing.T, store *goalAsyncStore, handler cdb.Handler) (goalAsyncCheckpoint, goalAsyncWorkRef) {
	t.Helper()
	return buildGoalAsyncEligibilityFixtureForSpec(t, store, handler, testAsyncGoalSpec())
}

func buildGoalAsyncEligibilityFixtureForSpec(t *testing.T, store *goalAsyncStore, handler cdb.Handler, spec *AgentGoalSpec) (goalAsyncCheckpoint, goalAsyncWorkRef) {
	t.Helper()
	ensureGoalAsyncEligibilityDataTables(t, handler)
	insertGoalAsyncEligibilityUser(t, handler, 7, "owner")
	for _, sourceID := range spec.AllowedSources {
		insertGoalAsyncEligibilitySource(t, handler, sourceID, 7)
	}

	cp, err := newGoalAsyncCheckpoint(
		spec, testAsyncGoalIdentity(), testAsyncGoalPrincipal(),
		"run-async-test", "trace-async-test", "corr-async-test",
		"ledger-async-test", strings.Repeat("b", 64),
		testAsyncGoalNow, testAsyncGoalNow.Add(10*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new eligibility checkpoint: %v", err)
	}
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create eligibility checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-elig-start"))
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitApprovalRequest(cp, "op-elig-approval"))
	work := testAsyncOperationWork()
	work.OperationID = cdb.CrawlOperationKey(cp.Owner, cp.AgentID, work.SourceID, cdb.CrawlOperationType, work.RequestID)
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestAwaitExternalRequest(t, cp, "op-elig-external", work, work.AcceptedAt))
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	return cp, work
}

func openGoalAsyncEligibilitySQLiteFixture(t *testing.T) (*goalAsyncStore, cdb.Handler, goalAsyncCheckpoint, goalAsyncWorkRef) {
	t.Helper()
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := buildGoalAsyncEligibilityFixture(t, store, handler)
	return store, handler, cp, work
}

func newGoalAsyncEligibilityForTest(store *goalAsyncStore, cp goalAsyncCheckpoint) (*goalAsyncResumeEligibility, *goalAsyncFakeAuthorityProvider) {
	provider := &goalAsyncFakeAuthorityProvider{authority: goalAsyncEligibilityAuthorityFor(cp), found: true}
	return newGoalAsyncResumeEligibility(store, provider), provider
}

func setGoalAsyncObservationCompleted(t *testing.T, handler cdb.Handler, work goalAsyncWorkRef) {
	t.Helper()
	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationCompleted), "crawl_completed", work.AcceptedAt.Add(time.Minute))
}

func TestGoalAsyncResumeEligibilitySQLitePositive(t *testing.T) {
	store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
	setGoalAsyncObservationCompleted(t, handler, work)
	eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)

	decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("evaluate eligible goal: %v", err)
	}
	if decision.Outcome != goalAsyncResumeEligible || decision.ReasonCode != goalAsyncResumeReasonEligible {
		t.Fatalf("eligibility decision = %+v", decision)
	}
	if decision.OperationClass != goalAsyncObservationReportedCompleted ||
		decision.State != goalAsyncAwaitingExternalWork {
		t.Fatalf("eligibility evidence = %+v", decision)
	}
	if provider.callCount() < 1 {
		t.Fatal("current agent authority was never consulted")
	}
	if cp.Verification != nil || len(cp.Evidence) != 0 || cp.State == goalAsyncCriteriaMet {
		t.Fatal("eligibility fabricated goal success evidence")
	}
	var goalRuns, transitions, workRows int
	if err := handler.QueryRow(`SELECT
		(SELECT COUNT(*) FROM AgentGoalRuns),
		(SELECT COUNT(*) FROM AgentGoalRunTransitions),
		(SELECT COUNT(*) FROM AgentGoalRunWork)`).Scan(&goalRuns, &transitions, &workRows); err != nil {
		t.Fatalf("count goal rows after eligibility: %v", err)
	}
	if goalRuns != 1 || transitions != 3 || workRows != 1 {
		t.Fatalf("eligibility changed goal rows: runs=%d transitions=%d work=%d", goalRuns, transitions, workRows)
	}
}

func TestGoalAsyncResumeEligibilitySQLiteWaiting(t *testing.T) {
	store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)

	decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeWaiting ||
		decision.ReasonCode != goalAsyncResumeReasonWaitingPending {
		t.Fatalf("accepted operation decision = %+v err=%v", decision, err)
	}
	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationProcessing), cdb.CrawlReasonProcessing, work.AcceptedAt.Add(time.Minute))
	decision, err = eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(2*time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeWaiting ||
		decision.ReasonCode != goalAsyncResumeReasonWaitingProcessing {
		t.Fatalf("processing operation decision = %+v err=%v", decision, err)
	}
}

func TestGoalAsyncResumeEligibilitySQLiteDeniedAndUncertain(t *testing.T) {
	t.Run("cancellation requested", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE AgentGoalRuns SET cancel_requested_at=CURRENT_TIMESTAMP, cancel_requested_by_user_id=7 WHERE goal_run_id=?`, cp.RunID); err != nil {
			t.Fatalf("set cancellation: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedCancelled {
			t.Fatalf("cancelled decision = %+v err=%v", decision, err)
		}
	})
	t.Run("deadline expired", func(t *testing.T) {
		store, _, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, cp.Deadline.Add(time.Second))
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedDeadline {
			t.Fatalf("deadline decision = %+v err=%v", decision, err)
		}
	})
	t.Run("budget exhausted", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE AgentGoalRuns SET used_model_rounds=max_model_rounds WHERE goal_run_id=?`, cp.RunID); err != nil {
			t.Fatalf("exhaust budget: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedBudget {
			t.Fatalf("budget decision = %+v err=%v", decision, err)
		}
	})
	t.Run("operation failed", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationFailed), "crawl_failed", work.AcceptedAt.Add(time.Minute))
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(2*time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedOperationTerminal {
			t.Fatalf("failed operation decision = %+v err=%v", decision, err)
		}
	})
	t.Run("operation missing", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		if _, err := handler.Exec(`DELETE FROM Status WHERE status_sha256=?`, work.OperationID); err != nil {
			t.Fatalf("delete operation: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainOperation {
			t.Fatalf("missing operation decision = %+v err=%v", decision, err)
		}
	})
	t.Run("operation accepted before approval", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		late := work.AcceptedAt.Add(time.Minute)
		if _, err := handler.Exec(`UPDATE CrawlApprovals SET approved_at=? WHERE approval_id=?`, late, work.ApprovalID); err != nil {
			t.Fatalf("late approval: %v", err)
		}
		mutateGoalAsyncObservationDetails(t, handler, work, func(details *goalAsyncOperationDetails) {
			details.ApprovedAt = late.UTC().Format(time.RFC3339Nano)
		})
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, late.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainOperationConflict {
			t.Fatalf("chronology decision = %+v err=%v", decision, err)
		}
	})
	t.Run("goal missing", func(t *testing.T) {
		store, _, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), "missing-run", work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainGoalMissing {
			t.Fatalf("missing goal decision = %+v err=%v", decision, err)
		}
	})
	t.Run("corrupt stored checkpoint", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE AgentGoalRuns SET updated_at=? WHERE goal_run_id=?`, cp.UpdatedAt.Add(time.Second), cp.RunID); err != nil {
			t.Fatalf("corrupt stored checkpoint: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainGoalCorrupt {
			t.Fatalf("corrupt goal decision = %+v err=%v", decision, err)
		}
	})
	t.Run("malformed operation details", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationRawDetails(t, handler, work, `{"request_id":"bad"}`)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainOperation {
			t.Fatalf("malformed operation decision = %+v err=%v", decision, err)
		}
	})
	t.Run("wrong proposal run", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE CrawlProposals SET run_id='wrong-run' WHERE request_id=?`, work.RequestID); err != nil {
			t.Fatalf("wrong proposal run: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainOperationConflict {
			t.Fatalf("wrong proposal run decision = %+v err=%v", decision, err)
		}
	})
	t.Run("invalid request", func(t *testing.T) {
		store, _, cp, _ := openGoalAsyncEligibilitySQLiteFixture(t)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), "bad run id", "bad op id", testAsyncGoalNow)
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedInvalidRequest {
			t.Fatalf("invalid request decision = %+v err=%v", decision, err)
		}
	})
}

func TestGoalAsyncResumeEligibilitySQLitePrincipalAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, handler cdb.Handler)
	}{
		{"user disabled", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
				t.Fatalf("disable user: %v", err)
			}
		}},
		{"user deleted", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`UPDATE Users SET deleted_at=CURRENT_TIMESTAMP WHERE user_id=7`); err != nil {
				t.Fatalf("delete user: %v", err)
			}
		}},
		{"user missing", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`DELETE FROM Users WHERE user_id=7`); err != nil {
				t.Fatalf("remove user: %v", err)
			}
		}},
		{"username changed", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`UPDATE Users SET username='renamed-owner' WHERE user_id=7`); err != nil {
				t.Fatalf("rename user: %v", err)
			}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
			setGoalAsyncObservationCompleted(t, handler, work)
			test.mutate(t, handler)
			eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
			decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
			if err != nil || decision.Outcome != goalAsyncResumeDenied {
				t.Fatalf("principal authority decision = %+v err=%v", decision, err)
			}
		})
	}
}

func TestGoalAsyncResumeEligibilitySQLiteSourceAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, handler cdb.Handler)
	}{
		{"source soft deleted", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
				t.Fatalf("delete source: %v", err)
			}
		}},
		{"ownership transferred", func(t *testing.T, handler cdb.Handler) {
			insertGoalAsyncEligibilityUser(t, handler, 8, "other-owner")
			if _, err := handler.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
				t.Fatalf("transfer source: %v", err)
			}
			if _, err := handler.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
				t.Fatalf("transfer provenance: %v", err)
			}
		}},
		{"ownership provenance missing", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
				t.Fatalf("delete provenance: %v", err)
			}
		}},
		{"legacy owner without provenance", func(t *testing.T, handler cdb.Handler) {
			if _, err := handler.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
				t.Fatalf("delete provenance: %v", err)
			}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
			setGoalAsyncObservationCompleted(t, handler, work)
			test.mutate(t, handler)
			eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
			decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
			if err != nil || decision.Outcome != goalAsyncResumeDenied ||
				decision.ReasonCode != goalAsyncResumeReasonDeniedSourceOwnership {
				t.Fatalf("source authority decision = %+v err=%v", decision, err)
			}
		})
	}
}

func TestGoalAsyncResumeEligibilitySQLiteAgentAuthority(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(authority *goalAsyncAgentAuthority) bool
		wantOut   goalAsyncResumeEligibilityOutcome
		wantReaon string
	}{
		{"agent missing", func(*goalAsyncAgentAuthority) bool { return false }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentUnavailable},
		{"agent disabled", func(a *goalAsyncAgentAuthority) bool { a.Enabled = false; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentUnavailable},
		{"owner changed", func(a *goalAsyncAgentAuthority) bool { a.Owner = "other-owner"; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentIdentity},
		{"agent id changed", func(a *goalAsyncAgentAuthority) bool { a.AgentID = "other-agent"; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentIdentity},
		{"goal specification changed", func(a *goalAsyncAgentAuthority) bool { a.GoalSpecHash = "abcdef"; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedGoalSpec},
		{"policy changed", func(a *goalAsyncAgentAuthority) bool { a.PolicySHA256 = "abcdef"; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPolicy},
		{"scope changed", func(a *goalAsyncAgentAuthority) bool { a.AllowedSources = []uint64{43}; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedScope},
		{"capability revoked", func(a *goalAsyncAgentAuthority) bool { a.Capabilities = []string{"db_read"}; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentCapability},
		{"trust insufficient", func(a *goalAsyncAgentAuthority) bool { a.TrustLevel = "restricted"; return true }, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentTrust},
		{"contract forbids goal", func(a *goalAsyncAgentAuthority) bool {
			a.Contract = &AgentContract{ForbiddenActions: []string{"AgentGoalRun"}}
			return true
		}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentContract},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
			setGoalAsyncObservationCompleted(t, handler, work)
			eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
			authority := goalAsyncEligibilityAuthorityFor(cp)
			found := test.mutate(&authority)
			provider.set(authority, found, nil)
			decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
			if err != nil || decision.Outcome != test.wantOut || decision.ReasonCode != test.wantReaon {
				t.Fatalf("agent authority decision = %+v err=%v", decision, err)
			}
		})
	}
	t.Run("authority provider unavailable", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
		provider.set(goalAsyncAgentAuthority{}, false, errors.New("authority unavailable"))
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailableAgentAuthority {
			t.Fatalf("unavailable authority decision = %+v err=%v", decision, err)
		}
	})
	t.Run("nil authority provider fails closed", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility := newGoalAsyncResumeEligibility(store, nil)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailableAgentAuthority {
			t.Fatalf("nil provider decision = %+v err=%v", decision, err)
		}
	})
}

func ensureGoalAsyncEligibilityDataTables(t *testing.T, db cdb.Handler) {
	t.Helper()
	ensureGoalAsyncEligibilityAuthTables(t, db)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Users (
		user_id BIGINT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_at TIMESTAMP,
		external_subject TEXT,
		external_issuer TEXT,
		credential_version BIGINT NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS Sources (
		source_id BIGINT PRIMARY KEY,
		source_uid TEXT NOT NULL,
		name TEXT,
		url TEXT,
		status TEXT,
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		usr_id BIGINT NOT NULL DEFAULT 0,
		deleted_at TIMESTAMP,
		last_updated_at TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS SourceOwnership (
		source_id BIGINT PRIMARY KEY,
		user_id BIGINT NOT NULL,
		verified_at TIMESTAMP NOT NULL,
		verified_by_user_id BIGINT NOT NULL,
		verified_by TEXT NOT NULL,
		verification_method TEXT NOT NULL,
		verification_reason TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`); err != nil {
		t.Fatalf("create eligibility data tables: %v", err)
	}
}

func goalAsyncEligibilityHex(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// runGoalAsyncRealSchedulerObservationScenario exercises the real durable
// scheduling repository and then the read-only observer/eligibility path using
// the authoritative rows, including the database-generated Status.created_at.
func prepareGoalAsyncRealSchedulerOperation(t *testing.T, store *goalAsyncStore, handler cdb.Handler) (goalAsyncCheckpoint, goalAsyncWorkRef, time.Time) {
	t.Helper()
	ctx := context.Background()
	ensureGoalAsyncEligibilityDataTables(t, handler)
	insertGoalAsyncEligibilityUser(t, handler, 7, "owner")
	insertGoalAsyncEligibilityUser(t, handler, 9, "operator")
	grantGoalAsyncEligibilityAdmin(t, handler, 9)
	insertGoalAsyncEligibilitySource(t, handler, 42, 7)

	createdAt := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Microsecond)
	deadline := createdAt.Add(10 * time.Minute)
	principal := testAsyncGoalPrincipal()
	cp, err := newGoalAsyncCheckpoint(
		testAsyncGoalSpec(), testAsyncGoalIdentity(), principal,
		"run-elig-real", "trace-elig-real", "",
		"ledger-elig-real", strings.Repeat("b", 64),
		createdAt, deadline,
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new real-scheduler checkpoint: %v", err)
	}
	if err := store.CreateCheckpoint(ctx, cp); err != nil {
		t.Fatalf("create real-scheduler checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-elig-real-start"))

	requestID := goalAsyncEligibilityHex("elig-real-request")
	parametersSHA256 := goalAsyncEligibilityHex("elig-real-parameters")
	approvalID := goalAsyncEligibilityHex("elig-real-approval")
	approvalSHA256 := goalAsyncEligibilityHex("elig-real-approval-hash")
	proposalExpiresAt := createdAt.Add(30 * time.Minute)
	approvalExpiresAt := createdAt.Add(25 * time.Minute)
	submitBy := createdAt.Add(20 * time.Minute)

	proposalWork := goalAsyncWorkRef{
		Kind:              goalAsyncWorkCrawlProposal,
		State:             goalAsyncWorkAwaitingApproval,
		RequestID:         requestID,
		ProposalSHA256:    parametersSHA256,
		SourceID:          42,
		ProposalExpiresAt: proposalExpiresAt,
	}
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	next, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-elig-real-approval",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &used,
		Work:             &proposalWork,
	})
	if err != nil {
		t.Fatalf("pure await-approval transition: %v", err)
	}
	stored, outcome, err := store.TransitionCheckpoint(ctx, cp.RunID, goalAsyncTransitionRequest{
		OperationID:      "op-elig-real-approval",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &used,
		Work:             &proposalWork,
	})
	if err != nil || outcome != goalAsyncTransitionApplied || !reflect.DeepEqual(next, stored) {
		t.Fatalf("store await-approval outcome=%q err=%v", outcome, err)
	}
	cp = stored

	proposal := cdb.CrawlProposalRow{
		RequestID: requestID, RunID: cp.RunID, ToolCallID: "tool-elig-real",
		AgentID: cp.AgentID, Owner: cp.Owner, PrincipalUserID: 7,
		SourceID: 42, OperationType: cdb.CrawlOperationType,
		ProposalSHA256: parametersSHA256, TraceID: cp.TraceID,
		MaxRequests: 1, BudgetWindowSeconds: 3600, PolicyJSON: "{}",
		State: cdb.CrawlProposalStatePending, ExpiresAt: proposalExpiresAt,
		CreatedAt: createdAt, LastUpdatedAt: createdAt,
	}
	if inserted, err := cdb.InsertCrawlProposal(ctx, &handler, proposal); err != nil || !inserted {
		t.Fatalf("insert real proposal inserted=%v err=%v", inserted, err)
	}
	approval := cdb.CrawlApprovalRow{
		ApprovalID: approvalID, ProposalSHA256: parametersSHA256,
		RequestID: requestID, RunID: cp.RunID, AgentID: cp.AgentID,
		Owner: cp.Owner, PrincipalUserID: 7, SourceID: 42,
		OperationType: cdb.CrawlOperationType, Issuer: "crowler",
		IssuedByUserID: 9, IssuedBy: "operator", IssuedByIssuer: "local",
		ApprovedByUserID: 9, ApprovedBy: "operator", ApprovedByIssuer: "local",
		ApprovalSHA256: approvalSHA256, ProposalExpiresAt: proposalExpiresAt,
		ApprovedAt: createdAt, ExpiresAt: approvalExpiresAt,
		CreatedAt: createdAt, LastUpdatedAt: createdAt,
	}
	if inserted, err := cdb.InsertCrawlApproval(ctx, &handler, approval); err != nil || !inserted {
		t.Fatalf("insert real approval inserted=%v err=%v", inserted, err)
	}

	request := cdb.CrawlScheduleRequest{
		RequestID: requestID, RunID: cp.RunID, PrincipalUserID: 7,
		AgentID: cp.AgentID, Owner: cp.Owner, SourceID: 42,
		AllowedSources: []uint64{42}, OperationType: cdb.CrawlOperationType,
		ParametersSHA256: parametersSHA256, ApprovalID: approvalID,
		ApprovalSHA256: approvalSHA256, ApprovedBy: "operator",
		ApprovedByUserID: 9, ApprovedAt: createdAt, ExpiresAt: submitBy,
		MaxRequests: 1, BudgetWindow: time.Hour,
	}
	result, err := cdb.ScheduleSourceCrawlRequestForProposal(ctx, &handler, request, requestID, parametersSHA256)
	if err != nil {
		t.Fatalf("real scheduler request: %v", err)
	}
	if result.State != cdb.CrawlOperationAccepted && result.State != cdb.CrawlOperationAlreadyPending {
		t.Fatalf("real scheduler state = %q reason = %q", result.State, result.ReasonCode)
	}
	if result.OperationID == "" {
		t.Fatal("real scheduler returned no operation ID")
	}

	proposalRow, found, err := cdb.GetCrawlProposalByRequest(ctx, &handler, requestID)
	if err != nil || !found {
		t.Fatalf("read real proposal found=%v err=%v", found, err)
	}
	approvalRow, found, err := cdb.GetCrawlApprovalByID(ctx, &handler, approvalID)
	if err != nil || !found {
		t.Fatalf("read real approval found=%v err=%v", found, err)
	}
	var acceptedAt time.Time
	statusQuery := `SELECT created_at FROM Status WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(handler, 1)
	if err := handler.QueryRowContext(ctx, statusQuery, result.OperationID).Scan(&acceptedAt); err != nil {
		t.Fatalf("read real operation created_at: %v", err)
	}
	acceptedAt = normalizeGoalAsyncTime(acceptedAt)

	work := goalAsyncWorkRef{
		Kind:              goalAsyncWorkCrawlOperation,
		State:             goalAsyncWorkScheduled,
		RequestID:         requestID,
		ProposalSHA256:    proposalRow.ProposalSHA256,
		ApprovalID:        approvalRow.ApprovalID,
		ApprovalSHA256:    approvalRow.ApprovalSHA256,
		OperationID:       result.OperationID,
		SourceID:          42,
		ProposalExpiresAt: proposalRow.ExpiresAt,
		ApprovalExpiresAt: approvalRow.ExpiresAt,
		SubmitBy:          submitBy,
		AcceptedAt:        acceptedAt,
	}
	scheduling, err := newGoalAsyncSchedulingPrerequisite(cp, goalAsyncSchedulingObservation{
		RunID: cp.RunID, GoalID: cp.GoalID, GoalSpecHash: cp.GoalSpecHash,
		AgentID: cp.AgentID, Owner: cp.Owner, Principal: principal,
		Work: work, ApprovalValid: true, ObservedAt: acceptedAt,
	})
	if err != nil {
		t.Fatalf("real scheduling prerequisite: %v", err)
	}
	awaitNow := cp.UpdatedAt.Add(time.Second)
	if !awaitNow.After(acceptedAt) {
		awaitNow = acceptedAt.Add(time.Second)
	}
	next, _, err = applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-elig-real-external",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitExternalWork,
		Now:              awaitNow,
		ReasonCode:       goalAsyncReasonAwaitingExternalWork,
		Used:             &goalAsyncUsed{ScheduledWorkRequests: 1},
		Scheduling:       &scheduling,
	})
	if err != nil {
		t.Fatalf("pure await-external transition: %v", err)
	}
	stored, outcome, err = store.TransitionCheckpoint(ctx, cp.RunID, goalAsyncTransitionRequest{
		OperationID:      "op-elig-real-external",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitExternalWork,
		Now:              awaitNow,
		ReasonCode:       goalAsyncReasonAwaitingExternalWork,
		Used:             &goalAsyncUsed{ScheduledWorkRequests: 1},
		Scheduling:       &scheduling,
	})
	if err != nil || outcome != goalAsyncTransitionApplied || !reflect.DeepEqual(next, stored) {
		t.Fatalf("store await-external outcome=%q err=%v", outcome, err)
	}
	cp = stored

	observation, err := store.observeCrawlOperationAt(ctx, cp, work.OperationID, acceptedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("observe real scheduled operation: %v", err)
	}
	if observation.Class != goalAsyncObservationPending {
		t.Fatalf("real scheduled operation class = %q", observation.Class)
	}
	return cp, work, acceptedAt
}

func runGoalAsyncRealSchedulerObservationScenario(t *testing.T, store *goalAsyncStore, handler cdb.Handler) {
	t.Helper()
	cp, work, acceptedAt := prepareGoalAsyncRealSchedulerOperation(t, store, handler)
	setGoalAsyncObservationCompleted(t, handler, work)
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
	decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, acceptedAt.Add(2*time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeEligible {
		t.Fatalf("real scheduler eligibility = %+v err=%v", decision, err)
	}
	if decision.OperationClass != goalAsyncObservationReportedCompleted {
		t.Fatalf("real scheduler eligibility class = %q", decision.OperationClass)
	}
}

func TestGoalAsyncResumeEligibilitySQLiteRealScheduler(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	runGoalAsyncRealSchedulerObservationScenario(t, store, handler)
}

func TestGoalAsyncResumeEligibilitySQLiteSystemPrincipal(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	ensureGoalAsyncEligibilityAuthTables(t, handler)
	createdAt := testAsyncGoalNow
	systemPrincipal := goalAsyncPrincipal{Kind: goalAsyncPrincipalSystem}
	cp, err := newGoalAsyncCheckpoint(
		testAsyncGoalSpec(), testAsyncGoalIdentity(), systemPrincipal,
		"run-elig-system", "trace-elig-system", "",
		"ledger-elig-system", strings.Repeat("c", 64),
		createdAt, createdAt.Add(10*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new system checkpoint: %v", err)
	}
	if err := store.CreateCheckpoint(context.Background(), cp); err != nil {
		t.Fatalf("create system checkpoint: %v", err)
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTestStartRequest(cp, "op-elig-system-start"))
	proposalWork := goalAsyncWorkRef{
		Kind: goalAsyncWorkCrawlProposal, State: goalAsyncWorkAwaitingApproval,
		RequestID: strings.Repeat("1", 64), ProposalSHA256: strings.Repeat("2", 64),
		SourceID: 42, ProposalExpiresAt: testAsyncGoalNow.Add(30 * time.Minute),
	}
	cp = applyAndStoreGoalAsyncTransition(t, store, cp, goalAsyncTransitionRequest{
		OperationID: "op-elig-system-approval", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1}, Work: &proposalWork,
	})
	work := testAsyncOperationWork()
	work.OperationID = cdb.CrawlOperationKey(cp.Owner, cp.AgentID, work.SourceID, cdb.CrawlOperationType, work.RequestID)
	obs := goalAsyncSchedulingObservation{
		RunID: cp.RunID, GoalID: cp.GoalID, GoalSpecHash: cp.GoalSpecHash,
		AgentID: cp.AgentID, Owner: cp.Owner, Principal: systemPrincipal,
		Work: work, ApprovalValid: true, ObservedAt: work.AcceptedAt,
	}
	scheduling, err := newGoalAsyncSchedulingPrerequisite(cp, obs)
	if err != nil {
		t.Fatalf("system scheduling prerequisite: %v", err)
	}
	await := goalAsyncTransitionRequest{
		OperationID: "op-elig-system-external", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1}, Scheduling: &scheduling,
	}
	next, _, err := applyGoalAsyncTransition(cp, await)
	if err != nil {
		t.Fatalf("system await external: %v", err)
	}
	stored, outcome, err := store.TransitionCheckpoint(context.Background(), cp.RunID, await)
	if err != nil || outcome != goalAsyncTransitionApplied || !reflect.DeepEqual(next, stored) {
		t.Fatalf("store system await external outcome=%q err=%v", outcome, err)
	}
	insertGoalAsyncObservationFixture(t, handler, stored, work)
	eligibility, _ := newGoalAsyncEligibilityForTest(store, stored)
	decision, err := eligibility.evaluateAt(context.Background(), stored.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeDenied ||
		decision.ReasonCode != goalAsyncResumeReasonDeniedSystemPrincipal {
		t.Fatalf("system principal decision = %+v err=%v", decision, err)
	}
}

type goalAsyncTransferAfterCheckAuthorizer struct {
	inner    CrawlSourceAuthorizer
	once     sync.Once
	transfer func()
}

func (a *goalAsyncTransferAfterCheckAuthorizer) AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error {
	err := a.inner.AuthorizeCrawlSource(ctx, principal, sourceID)
	if err == nil {
		a.once.Do(a.transfer)
	}
	return err
}

func TestGoalAsyncResumeEligibilitySQLiteRaces(t *testing.T) {
	t.Run("cancellation during evaluation", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
		var once sync.Once
		provider.onCall = func() {
			once.Do(func() {
				if _, err := handler.Exec(`UPDATE AgentGoalRuns SET cancel_requested_at=CURRENT_TIMESTAMP, cancel_requested_by_user_id=7 WHERE goal_run_id=?`, cp.RunID); err != nil {
					t.Errorf("set racing cancellation: %v", err)
				}
			})
		}
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied || decision.ReasonCode != goalAsyncResumeReasonDeniedCancelled {
			t.Fatalf("racing cancellation decision = %+v err=%v", decision, err)
		}
	})
	t.Run("checkpoint revision advanced", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
		var once sync.Once
		provider.onCall = func() {
			once.Do(func() {
				request := goalAsyncTransitionRequest{
					OperationID: "op-elig-race-failed", ExpectedRevision: cp.Revision,
					Kind: goalAsyncTransitionFailed, Now: cp.UpdatedAt.Add(time.Second),
					ReasonCode: goalAsyncReasonFailed,
				}
				if _, _, err := store.TransitionCheckpoint(context.Background(), cp.RunID, request); err != nil {
					t.Errorf("advance racing checkpoint: %v", err)
				}
			})
		}
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeEligibilityUncertain ||
			decision.ReasonCode != goalAsyncResumeReasonUncertainStateChanged {
			t.Fatalf("racing revision decision = %+v err=%v", decision, err)
		}
	})
	t.Run("ownership transferred after first check", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		eligibility.sourceAuthority = &goalAsyncTransferAfterCheckAuthorizer{
			inner: DatabaseCrawlSourceAuthorizer{
				Lookup: DatabaseSourceOwnerLookup{DB: store.db},
			},
			transfer: func() {
				if _, err := handler.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Errorf("transfer racing source: %v", err)
				}
				if _, err := handler.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Errorf("transfer racing provenance: %v", err)
				}
			},
		}
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedSourceOwnership {
			t.Fatalf("racing ownership decision = %+v err=%v", decision, err)
		}
	})
	t.Run("multiple readers do not write", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		var wg sync.WaitGroup
		decisions := make(chan goalAsyncResumeEligibilityDecision, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
				decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
				if err != nil {
					t.Errorf("concurrent eligibility: %v", err)
					return
				}
				decisions <- decision
			}()
		}
		wg.Wait()
		close(decisions)
		for decision := range decisions {
			if decision.Outcome != goalAsyncResumeEligible {
				t.Fatalf("concurrent decision = %+v", decision)
			}
		}
		var runs, transitions int
		if err := handler.QueryRow(`SELECT
			(SELECT COUNT(*) FROM AgentGoalRuns),
			(SELECT COUNT(*) FROM AgentGoalRunTransitions)`).Scan(&runs, &transitions); err != nil {
			t.Fatalf("count concurrent goal rows: %v", err)
		}
		if runs != 1 || transitions != 3 {
			t.Fatalf("concurrent readers changed rows: runs=%d transitions=%d", runs, transitions)
		}
	})
	t.Run("database unavailable", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		if err := handler.Close(); err != nil {
			t.Fatalf("close handler: %v", err)
		}
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable {
			t.Fatalf("closed database decision = %+v err=%v", decision, err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		store, _, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		decision, err := eligibility.evaluateAt(ctx, cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailableContext {
			t.Fatalf("cancelled context decision = %+v err=%v", decision, err)
		}
	})
}

func TestGoalAsyncResumeEligibilitySQLiteDisabledSource(t *testing.T) {
	t.Run("single source disabled", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=42`); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedSourceDisabled {
			t.Fatalf("disabled source decision = %+v err=%v", decision, err)
		}
	})
	t.Run("one of several sources disabled", func(t *testing.T) {
		spec := *testAsyncGoalSpec()
		spec.AllowedSources = []uint64{42, 43}
		spec.SpecHash = computeGoalSpecHash(&spec)
		store, handler, _ := openGoalAsyncSQLiteStore(t)
		cp, work := buildGoalAsyncEligibilityFixtureForSpec(t, store, handler, &spec)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=43`); err != nil {
			t.Fatalf("disable second source: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedSourceDisabled {
			t.Fatalf("disabled second source decision = %+v err=%v", decision, err)
		}
	})
	t.Run("source state read failure", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`DROP TABLE Sources`); err != nil {
			t.Fatalf("drop sources: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable {
			t.Fatalf("source read failure decision = %+v err=%v", decision, err)
		}
	})
}

func TestGoalAsyncResumeEligibilitySQLiteStrictPrincipalAuthority(t *testing.T) {
	t.Run("auth schema unavailable", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`DROP TABLE Users`); err != nil {
			t.Fatalf("drop users: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailablePrincipal {
			t.Fatalf("auth schema decision = %+v err=%v", decision, err)
		}
	})
	t.Run("grant store unavailable", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`DROP TABLE AuthRoles`); err != nil {
			t.Fatalf("drop roles: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeUnavailable ||
			decision.ReasonCode != goalAsyncResumeReasonUnavailablePrincipal {
			t.Fatalf("grant store decision = %+v err=%v", decision, err)
		}
	})
	t.Run("external issuer mapping", func(t *testing.T) {
		store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
		setGoalAsyncObservationCompleted(t, handler, work)
		if _, err := handler.Exec(`UPDATE Users SET external_subject='ext-sub', external_issuer='https://issuer.example/' WHERE user_id=7`); err != nil {
			t.Fatalf("map external identity: %v", err)
		}
		eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
		decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
		if err != nil || decision.Outcome != goalAsyncResumeDenied ||
			decision.ReasonCode != goalAsyncResumeReasonDeniedPrincipalExternal {
			t.Fatalf("external identity decision = %+v err=%v", decision, err)
		}
	})
}

func TestGoalAsyncResumeEligibilitySQLiteDeadlineDuringEvaluation(t *testing.T) {
	store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
	setGoalAsyncObservationCompleted(t, handler, work)
	eligibility, provider := newGoalAsyncEligibilityForTest(store, cp)
	clockValue := work.AcceptedAt.Add(time.Minute)
	eligibility.clock = func() time.Time { return clockValue }
	var once sync.Once
	provider.onCall = func() {
		once.Do(func() {
			clockValue = cp.Deadline.Add(time.Second)
		})
	}
	decision, err := eligibility.Evaluate(context.Background(), cp.RunID, work.OperationID)
	if err != nil || decision.Outcome != goalAsyncResumeDenied ||
		decision.ReasonCode != goalAsyncResumeReasonDeniedDeadline {
		t.Fatalf("deadline-during-evaluation decision = %+v err=%v", decision, err)
	}
	if decision.CheckedAt.Before(cp.Deadline) {
		t.Fatalf("checked_at = %s did not reflect the final deadline check", decision.CheckedAt)
	}
}

func TestGoalAsyncResumeEligibilitySQLiteSourceCompletionIsNotOperationCompletion(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work, acceptedAt := prepareGoalAsyncRealSchedulerOperation(t, store, handler)
	ctx := context.Background()

	if _, err := handler.Exec(`UPDATE Sources SET status='completed', last_updated_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
		t.Fatalf("mark source completed: %v", err)
	}
	eventID := goalAsyncEligibilityHex("source-level-crawl-completed")
	if _, err := handler.Exec(`INSERT INTO Events(event_sha256, source_id, event_type, event_severity, details)
		VALUES (`+goalAsyncObservationPlaceholder(handler, 1)+`, `+goalAsyncObservationPlaceholder(handler, 2)+`,
		'crawl_completed', 'info', '{}')`, eventID, 42); err != nil {
		t.Fatalf("insert source-level event: %v", err)
	}
	otherOperationID := goalAsyncEligibilityHex("other-operation")
	if _, err := handler.Exec(`INSERT INTO Status(status_sha256, agent_id, source_id, operation_type, status, details, created_at, last_updated_at)
		VALUES (`+goalAsyncObservationPlaceholder(handler, 1)+`, `+goalAsyncObservationPlaceholder(handler, 2)+`,
		`+goalAsyncObservationPlaceholder(handler, 3)+`, 'crawl', 'completed', '{}', `+
		goalAsyncObservationPlaceholder(handler, 4)+`, `+goalAsyncObservationPlaceholder(handler, 5)+`)`,
		otherOperationID, cp.AgentID, 42, acceptedAt, acceptedAt); err != nil {
		t.Fatalf("insert unrelated completed operation: %v", err)
	}

	observation, err := store.observeCrawlOperationAt(ctx, cp, work.OperationID, acceptedAt.Add(time.Second))
	if err != nil || observation.Class != goalAsyncObservationPending {
		t.Fatalf("source-level activity changed operation observation = %+v err=%v", observation, err)
	}
	var status string
	if err := handler.QueryRow(`SELECT status FROM Status WHERE status_sha256=?`, work.OperationID).Scan(&status); err != nil {
		t.Fatalf("read target operation status: %v", err)
	}
	if status != string(cdb.CrawlOperationAccepted) {
		t.Fatalf("target operation status = %q, want accepted", status)
	}
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
	decision, err := eligibility.evaluateAt(ctx, cp.RunID, work.OperationID, acceptedAt.Add(2*time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeWaiting {
		t.Fatalf("source-level activity produced eligibility = %+v err=%v", decision, err)
	}
	if decision.OperationClass != goalAsyncObservationPending {
		t.Fatalf("eligibility operation class = %q", decision.OperationClass)
	}
}

type goalAsyncDisableOnSecondCheckAuthorizer struct {
	inner    CrawlSourceAuthorizer
	handler  cdb.Handler
	sourceID uint64
	calls    int
}

func (a *goalAsyncDisableOnSecondCheckAuthorizer) AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error {
	a.calls++
	if a.calls == 2 {
		if _, err := a.handler.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=?`, a.sourceID); err != nil {
			return err
		}
	}
	return a.inner.AuthorizeCrawlSource(ctx, principal, sourceID)
}

func TestGoalAsyncResumeEligibilitySQLiteDisabledBeforeSecondCheck(t *testing.T) {
	store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
	setGoalAsyncObservationCompleted(t, handler, work)
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
	eligibility.sourceAuthority = &goalAsyncDisableOnSecondCheckAuthorizer{
		inner: DatabaseCrawlSourceAuthorizer{
			Lookup: DatabaseSourceOwnerLookup{DB: store.db},
		},
		handler:  handler,
		sourceID: 42,
	}
	decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeDenied ||
		decision.ReasonCode != goalAsyncResumeReasonDeniedSourceDisabled {
		t.Fatalf("second-check disabled source decision = %+v err=%v", decision, err)
	}
}

func TestGoalAsyncResumeEligibilitySQLiteRejectsNonLocalIssuer(t *testing.T) {
	store, handler, cp, work := openGoalAsyncEligibilitySQLiteFixture(t)
	setGoalAsyncObservationCompleted(t, handler, work)
	if _, err := handler.Exec(`UPDATE AgentGoalRuns SET principal_issuer='https://issuer.example/' WHERE goal_run_id=?`, cp.RunID); err != nil {
		t.Fatalf("change stored issuer: %v", err)
	}
	eligibility, _ := newGoalAsyncEligibilityForTest(store, cp)
	decision, err := eligibility.evaluateAt(context.Background(), cp.RunID, work.OperationID, work.AcceptedAt.Add(time.Minute))
	if err != nil || decision.Outcome != goalAsyncResumeDenied ||
		decision.ReasonCode != goalAsyncResumeReasonDeniedPrincipalExternal {
		t.Fatalf("non-local issuer decision = %+v err=%v", decision, err)
	}
}
