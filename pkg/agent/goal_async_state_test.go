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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var testAsyncGoalNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func testAsyncGoalSpec() *AgentGoalSpec {
	spec := &AgentGoalSpec{
		GoalID:         "goal-async-test",
		SpecHash:       strings.Repeat("a", 64),
		AllowedSources: []uint64{42},
		Allowlist:      []string{"get_source_status"},
		Evidence: EvidencePolicy{
			Allow:           []EvidenceKind{EvidenceToolObservation},
			DefaultMaxAge:   time.Hour,
			MaxPayloadBytes: 4096,
		},
		Budgets: GoalBudgets{
			MaxModelRounds:        4,
			MaxToolCalls:          8,
			MaxEvidenceRecords:    4,
			MaxEvidenceBytes:      4096,
			MaxTotalResultBytes:   4096,
			MaxElapsed:            2 * time.Minute,
			MaxConsecutiveDenials: 3,
		},
		Acceptance: AcceptanceSpec{
			Mode: AcceptanceAll,
			Criteria: []CriterionSpec{
				{
					ID:        "evidence",
					Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceToolObservation},
				},
			},
		},
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	return spec
}

func testAsyncGoalIdentity() AgentIdentity {
	return AgentIdentity{
		AgentID: "agent-async-test",
		Name:    "Async Test Agent",
		Owner:   "owner",
	}
}

func testAsyncGoalPrincipal() goalAsyncPrincipal {
	return goalAsyncPrincipal{
		Kind:     goalAsyncPrincipalUser,
		UserID:   7,
		Username: "owner",
		Issuer:   "local",
	}
}

func testAsyncGoalBaseCheckpoint(t *testing.T) goalAsyncCheckpoint {
	t.Helper()
	cp, err := newGoalAsyncCheckpoint(
		testAsyncGoalSpec(), testAsyncGoalIdentity(), testAsyncGoalPrincipal(),
		"run-async-test", "trace-async-test", "corr-async-test",
		"ledger-async-test", strings.Repeat("b", 64),
		testAsyncGoalNow, testAsyncGoalNow.Add(10*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new checkpoint: %v", err)
	}
	return cp
}

func testAsyncGoalTransition(t *testing.T, cp goalAsyncCheckpoint, req goalAsyncTransitionRequest) (goalAsyncCheckpoint, goalAsyncTransitionOutcome) {
	t.Helper()
	next, outcome, err := applyGoalAsyncTransition(cp, req)
	if err != nil {
		t.Fatalf("transition %s: %v", req.Kind, err)
	}
	return next, outcome
}

func testAsyncGoalStart(t *testing.T, cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	t.Helper()
	next, outcome := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-start",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionStart,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonStarted,
	})
	if outcome != goalAsyncTransitionApplied {
		t.Fatalf("start outcome = %q, want applied", outcome)
	}
	return next
}

func testAsyncProposalWork() goalAsyncWorkRef {
	return goalAsyncWorkRef{
		Kind:              goalAsyncWorkCrawlProposal,
		State:             goalAsyncWorkAwaitingApproval,
		RequestID:         strings.Repeat("1", 64),
		ProposalSHA256:    strings.Repeat("2", 64),
		SourceID:          42,
		ProposalExpiresAt: testAsyncGoalNow.Add(30 * time.Minute),
	}
}

func testAsyncOperationWork() goalAsyncWorkRef {
	return testAsyncOperationWorkAt(testAsyncGoalNow.Add(time.Second))
}

func testAsyncOperationWorkAt(acceptedAt time.Time) goalAsyncWorkRef {
	return goalAsyncWorkRef{
		Kind:              goalAsyncWorkCrawlOperation,
		State:             goalAsyncWorkScheduled,
		RequestID:         strings.Repeat("1", 64),
		ProposalSHA256:    strings.Repeat("2", 64),
		ApprovalID:        strings.Repeat("3", 64),
		ApprovalSHA256:    strings.Repeat("4", 64),
		OperationID:       strings.Repeat("5", 64),
		SourceID:          42,
		ProposalExpiresAt: testAsyncGoalNow.Add(30 * time.Minute),
		ApprovalExpiresAt: testAsyncGoalNow.Add(30 * time.Minute),
		SubmitBy:          testAsyncGoalNow.Add(20 * time.Minute),
		AcceptedAt:        acceptedAt,
	}
}

func testAsyncGoalAwaitApproval(t *testing.T, cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	t.Helper()
	work := testAsyncProposalWork()
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-await-approval",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &used,
		Work:             &work,
	})
	return next
}

func testAsyncSchedulingObservation(cp goalAsyncCheckpoint, work goalAsyncWorkRef, observedAt time.Time) goalAsyncSchedulingObservation {
	return goalAsyncSchedulingObservation{
		RunID: cp.RunID, GoalID: cp.GoalID, GoalSpecHash: cp.GoalSpecHash,
		AgentID: cp.AgentID, Owner: cp.Owner,
		Principal: testAsyncGoalPrincipal(), Work: work,
		ApprovalValid: true, ObservedAt: observedAt,
	}
}

func testAsyncSchedulingPrerequisite(t *testing.T, cp goalAsyncCheckpoint, work goalAsyncWorkRef, observedAt time.Time) goalAsyncSchedulingPrerequisite {
	t.Helper()
	scheduling, err := newGoalAsyncSchedulingPrerequisite(cp, testAsyncSchedulingObservation(cp, work, observedAt))
	if err != nil {
		t.Fatalf("scheduling prerequisite: %v", err)
	}
	return scheduling
}

func testAsyncGoalAwaitExternal(t *testing.T, cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	t.Helper()
	work := testAsyncOperationWork()
	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, cp.UpdatedAt)
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-await-external",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitExternalWork,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingExternalWork,
		Used:             &used,
		Scheduling:       &scheduling,
	})
	return next
}

func testAsyncGoalRunningWithWork(t *testing.T) goalAsyncCheckpoint {
	t.Helper()
	cp := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	cp = testAsyncGoalAwaitApproval(t, cp)
	return testAsyncGoalAwaitExternal(t, cp)
}

func testAsyncEvidenceRecord(cp goalAsyncCheckpoint, observedAt time.Time) EvidenceRecord {
	payload := map[string]any{"ok": true}
	encoded, _ := json.Marshal(payload)
	payloadSum := sha256.Sum256(encoded)
	record := EvidenceRecord{
		EvidenceID:     "ev_" + strings.Repeat("a", 32),
		LedgerID:       cp.LedgerID,
		RunID:          cp.RunID,
		RunNonceHash:   cp.RunNonceHash,
		TraceID:        cp.TraceID,
		GoalID:         cp.GoalID,
		GoalSpecHash:   cp.GoalSpecHash,
		AgentID:        cp.AgentID,
		Owner:          cp.Owner,
		Kind:           EvidenceToolObservation,
		Producer:       "tool:test",
		TrustClass:     TrustToolObservation,
		SourceIDs:      []uint64{42},
		ObservedAt:     observedAt,
		ExpiresAt:      observedAt.Add(time.Minute),
		Payload:        payload,
		PayloadSHA256:  hex.EncodeToString(payloadSum[:]),
		SelectorSHA256: selectorHash(EvidenceSelector{}),
	}
	record.RecordSHA256 = recordHash(record)
	return record
}

func testAsyncEvidenceRef(record EvidenceRecord) goalAsyncEvidenceRef {
	return goalAsyncEvidenceRef{
		EvidenceID:    record.EvidenceID,
		Kind:          record.Kind,
		TrustClass:    record.TrustClass,
		Producer:      record.Producer,
		SourceIDs:     append([]uint64(nil), record.SourceIDs...),
		ObservedAt:    record.ObservedAt,
		ExpiresAt:     record.ExpiresAt,
		PayloadSHA256: record.PayloadSHA256,
		RecordSHA256:  record.RecordSHA256,
		SizeBytes:     128,
	}
}

func testAsyncVerifierDecision(t *testing.T, cp goalAsyncCheckpoint) (goalAsyncVerifierDecision, EvidenceRecord) {
	t.Helper()
	observedAt := cp.UpdatedAt.Add(time.Second)
	record := testAsyncEvidenceRecord(cp, observedAt)
	input := GoalEvalInput{
		Spec:         testAsyncGoalSpec(),
		Records:      []EvidenceRecord{record},
		Now:          observedAt,
		LedgerID:     cp.LedgerID,
		RunID:        cp.RunID,
		GoalID:       cp.GoalID,
		GoalSpecHash: cp.GoalSpecHash,
		RunNonceHash: cp.RunNonceHash,
		AgentID:      cp.AgentID,
		Owner:        cp.Owner,
	}
	decision, err := newGoalAsyncVerifierDecision(cp, input)
	if err != nil {
		t.Fatalf("verifier decision: %v", err)
	}
	return decision, record
}

func testAsyncGoalCriteriaMet(t *testing.T, cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	t.Helper()
	decision, record := testAsyncVerifierDecision(t, cp)
	ref := testAsyncEvidenceRef(record)
	used := cp.Used
	used.ModelRounds = 1
	used.ToolAttempts = 1
	used.ResultBytes = 128
	used.EvidenceRecords = 1
	used.EvidenceBytes = 128
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-criteria-met",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              cp.UpdatedAt.Add(time.Second),
		Used:             &used,
		Evidence:         []goalAsyncEvidenceRef{ref},
		Verifier:         &decision,
	})
	return next
}

func testAsyncResumeGrant(t *testing.T, cp goalAsyncCheckpoint, workState goalAsyncWorkState) goalAsyncResumeGrant {
	t.Helper()
	operation := testAsyncOperationWork()
	current := goalAsyncCurrentAuthority{
		AgentID:         cp.AgentID,
		Owner:           cp.Owner,
		Principal:       testAsyncGoalPrincipal(),
		PolicySHA256:    cp.PolicySHA256,
		AllowedTools:    append([]string(nil), cp.AllowedTools...),
		AllowedSources:  append([]uint64(nil), cp.AllowedSources...),
		VerifiedSources: []uint64{42},
		AgentEnabled:    true,
	}
	grant, err := newGoalAsyncResumeGrant(cp, current, operation.OperationID, workState, cp.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("resume grant: %v", err)
	}
	return grant
}

func testAsyncGoalResumeProcessing(t *testing.T, cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	t.Helper()
	grant := testAsyncResumeGrant(t, cp, goalAsyncWorkProcessing)
	work := testAsyncOperationWork()
	work.State = goalAsyncWorkProcessing
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-resume-processing",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionResume,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonResumed,
		Work:             &work,
		Resume:           &grant,
	})
	return next
}

func TestAsyncGoalLifecycleApprovalAndExternalWork(t *testing.T) {
	cp := testAsyncGoalBaseCheckpoint(t)
	if cp.State != goalAsyncCreated || cp.Revision != 1 || cp.Slices != 0 {
		t.Fatalf("unexpected base checkpoint: %+v", cp)
	}

	cp = testAsyncGoalStart(t, cp)
	if cp.State != goalAsyncRunning || cp.Slices != 1 {
		t.Fatalf("unexpected running checkpoint: state=%s slices=%d", cp.State, cp.Slices)
	}

	cp = testAsyncGoalAwaitApproval(t, cp)
	if cp.State != goalAsyncAwaitingApproval || len(cp.Work) != 1 || cp.Work[0].Kind != goalAsyncWorkCrawlProposal {
		t.Fatalf("unexpected awaiting approval checkpoint: %+v", cp)
	}
	if cp.Used.ScheduledWorkRequests != 1 {
		t.Fatalf("scheduled work count = %d, want 1", cp.Used.ScheduledWorkRequests)
	}

	cp = testAsyncGoalAwaitExternal(t, cp)
	if cp.State != goalAsyncAwaitingExternalWork || len(cp.Work) != 1 || cp.Work[0].Kind != goalAsyncWorkCrawlOperation {
		t.Fatalf("unexpected awaiting external checkpoint: %+v", cp)
	}
	if cp.Work[0].State != goalAsyncWorkScheduled {
		t.Fatalf("work state = %s, want scheduled", cp.Work[0].State)
	}

	// Scheduling alone cannot become completion.
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-illegal-complete",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              cp.UpdatedAt.Add(time.Second),
	}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
		t.Fatalf("scheduling-only completion error = %v, want transition invalid", err)
	}

	cp = testAsyncGoalResumeProcessing(t, cp)
	if cp.State != goalAsyncRunning || cp.Slices != 2 {
		t.Fatalf("unexpected resumed checkpoint: state=%s slices=%d", cp.State, cp.Slices)
	}
	if cp.Work[0].State != goalAsyncWorkProcessing {
		t.Fatalf("work state after resume = %s, want processing", cp.Work[0].State)
	}

	cp = testAsyncGoalCriteriaMet(t, cp)
	if cp.State != goalAsyncCriteriaMet || cp.Verification == nil {
		t.Fatalf("unexpected completed checkpoint: %+v", cp)
	}
	if cp.Verification.Verifier != goalAsyncReasonDeterministicVerifier {
		t.Fatalf("verifier = %q", cp.Verification.Verifier)
	}
	if cp.ReasonCode != "criteria_all_met" {
		t.Fatalf("completion reason = %q", cp.ReasonCode)
	}
}

func TestAsyncGoalCompletedWorkRepresentation(t *testing.T) {
	cp := testAsyncGoalRunningWithWork(t)
	grant := testAsyncResumeGrant(t, cp, goalAsyncWorkCompleted)
	work := testAsyncOperationWork()
	work.State = goalAsyncWorkCompleted
	work.ObservedAt = cp.UpdatedAt.Add(time.Second)
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID:      "op-resume-completed",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionResume,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonResumed,
		Work:             &work,
		Resume:           &grant,
	})
	if next.State != goalAsyncRunning || len(next.Work) != 1 || next.Work[0].State != goalAsyncWorkCompleted {
		t.Fatalf("unexpected completed-work resume: %+v", next)
	}
}

func TestAsyncGoalReplayAndRevision(t *testing.T) {
	cp := testAsyncGoalBaseCheckpoint(t)
	start := goalAsyncTransitionRequest{
		OperationID:      "op-start-replay",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionStart,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonStarted,
	}
	first, outcome, err := applyGoalAsyncTransition(cp, start)
	if err != nil || outcome != goalAsyncTransitionApplied {
		t.Fatalf("first start: outcome=%s err=%v", outcome, err)
	}
	replay, outcome, err := applyGoalAsyncTransition(first, start)
	if err != nil || outcome != goalAsyncTransitionReplayed {
		t.Fatalf("replay: outcome=%s err=%v", outcome, err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay changed checkpoint")
	}
	noClock := start
	noClock.Now = time.Time{}
	if replay, outcome, err := applyGoalAsyncTransition(first, noClock); err != nil || outcome != goalAsyncTransitionReplayed || !reflect.DeepEqual(replay, first) {
		t.Fatalf("clock-free replay: outcome=%s err=%v", outcome, err)
	}

	changed := start
	changed.ReasonCode = "different_reason"
	if _, _, err := applyGoalAsyncTransition(first, changed); !errors.Is(err, ErrGoalAsyncReplayConflict) {
		t.Fatalf("changed replay error = %v, want replay conflict", err)
	}

	stale := start
	stale.OperationID = "op-stale"
	if _, _, err := applyGoalAsyncTransition(first, stale); !errors.Is(err, ErrGoalAsyncRevisionConflict) {
		t.Fatalf("stale revision error = %v, want revision conflict", err)
	}

	incomplete := goalAsyncTransitionRequest{
		OperationID:      "op-incomplete",
		ExpectedRevision: first.Revision,
		Kind:             goalAsyncTransitionIncomplete,
		Now:              first.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonIncomplete,
	}
	next, outcome, err := applyGoalAsyncTransition(first, incomplete)
	if err != nil || outcome != goalAsyncTransitionApplied || next.State != goalAsyncIncomplete {
		t.Fatalf("incomplete transition: state=%s outcome=%s err=%v", next.State, outcome, err)
	}
}

func TestAsyncGoalClosedTransitionTable(t *testing.T) {
	states := []goalAsyncState{
		goalAsyncCreated, goalAsyncRunning, goalAsyncAwaitingApproval,
		goalAsyncAwaitingExternalWork, goalAsyncCriteriaMet, goalAsyncIncomplete,
		goalAsyncBudgetExhausted, goalAsyncCancelled, goalAsyncDenied, goalAsyncFailed,
	}
	kinds := []goalAsyncTransitionKind{
		goalAsyncTransitionStart, goalAsyncTransitionAwaitApproval,
		goalAsyncTransitionAwaitExternalWork, goalAsyncTransitionResume,
		goalAsyncTransitionCriteriaMet, goalAsyncTransitionIncomplete,
		goalAsyncTransitionBudgetExhausted, goalAsyncTransitionCancelled,
		goalAsyncTransitionDenied, goalAsyncTransitionFailed,
	}
	for _, state := range states {
		cp := testAsyncGoalCheckpointInState(t, state)
		for _, kind := range kinds {
			if goalAsyncTransitionAllowed(state, kind) {
				continue
			}
			before := cloneGoalAsyncCheckpoint(cp)
			_, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
				OperationID:      "op-illegal-table",
				ExpectedRevision: cp.Revision,
				Kind:             kind,
				Now:              cp.UpdatedAt.Add(time.Second),
				ReasonCode:       "illegal_transition",
			})
			if !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
				t.Fatalf("%s -> %s error = %v, want transition invalid", state, kind, err)
			}
			if !reflect.DeepEqual(cp, before) {
				t.Fatalf("%s -> %s mutated the previous checkpoint", state, kind)
			}
		}
	}
}

func testAsyncGoalCheckpointInState(t *testing.T, state goalAsyncState) goalAsyncCheckpoint {
	t.Helper()
	cp := testAsyncGoalBaseCheckpoint(t)
	switch state {
	case goalAsyncCreated:
		return cp
	case goalAsyncRunning:
		return testAsyncGoalStart(t, cp)
	case goalAsyncAwaitingApproval:
		return testAsyncGoalAwaitApproval(t, testAsyncGoalStart(t, cp))
	case goalAsyncAwaitingExternalWork:
		return testAsyncGoalRunningWithWork(t)
	case goalAsyncCriteriaMet:
		return testAsyncGoalCriteriaMet(t, testAsyncGoalStart(t, cp))
	case goalAsyncIncomplete:
		start := testAsyncGoalStart(t, cp)
		next, _ := testAsyncGoalTransition(t, start, goalAsyncTransitionRequest{
			OperationID: "op-state-incomplete", ExpectedRevision: start.Revision,
			Kind: goalAsyncTransitionIncomplete, Now: start.UpdatedAt.Add(time.Second),
			ReasonCode: goalAsyncReasonIncomplete,
		})
		return next
	case goalAsyncBudgetExhausted:
		start := testAsyncGoalStart(t, cp)
		next, _ := testAsyncGoalTransition(t, start, goalAsyncTransitionRequest{
			OperationID: "op-state-budget", ExpectedRevision: start.Revision,
			Kind: goalAsyncTransitionBudgetExhausted, Now: start.Deadline,
			ReasonCode: goalAsyncReasonBudgetExhausted,
		})
		return next
	case goalAsyncCancelled:
		next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
			OperationID: "op-state-cancelled", ExpectedRevision: cp.Revision,
			Kind: goalAsyncTransitionCancelled, Now: cp.UpdatedAt.Add(time.Second),
			ReasonCode: goalAsyncReasonCancelled,
		})
		return next
	case goalAsyncDenied:
		next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
			OperationID: "op-state-denied", ExpectedRevision: cp.Revision,
			Kind: goalAsyncTransitionDenied, Now: cp.UpdatedAt.Add(time.Second),
			ReasonCode: goalAsyncReasonDenied,
		})
		return next
	case goalAsyncFailed:
		next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
			OperationID: "op-state-failed", ExpectedRevision: cp.Revision,
			Kind: goalAsyncTransitionFailed, Now: cp.UpdatedAt.Add(time.Second),
			ReasonCode: goalAsyncReasonFailed,
		})
		return next
	default:
		t.Fatalf("unsupported state %s", state)
		return goalAsyncCheckpoint{}
	}
}

func TestAsyncGoalSerializationRoundTripAndStrictness(t *testing.T) {
	cp := testAsyncGoalRunningWithWork(t)
	cp = testAsyncGoalResumeProcessing(t, cp)
	cp = testAsyncGoalCriteriaMet(t, cp)
	data, err := marshalGoalAsyncCheckpoint(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := parseGoalAsyncCheckpoint(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(cp, parsed) {
		t.Fatalf("round trip mismatch")
	}
	digest1, err := goalAsyncCheckpointDigest(cp)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	digest2, err := goalAsyncCheckpointDigest(parsed)
	if err != nil || digest1 != digest2 {
		t.Fatalf("digest mismatch: %s %s %v", digest1, digest2, err)
	}

	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	object["unknown_field"] = true
	unknown, _ := json.Marshal(object)
	if _, err := parseGoalAsyncCheckpoint(unknown); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("unknown field error = %v, want serialization error", err)
	}

	badVersion := cloneGoalAsyncCheckpoint(cp)
	badVersion.CheckpointVersion = 3
	badData, _ := json.Marshal(badVersion)
	if _, err := parseGoalAsyncCheckpoint(badData); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("bad version error = %v, want checkpoint invalid", err)
	}

	oversized := make([]byte, maxGoalAsyncCheckpointBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if _, err := parseGoalAsyncCheckpoint(oversized); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("oversized error = %v, want serialization error", err)
	}

	if _, err := parseGoalAsyncCheckpoint(append(data, []byte("{}")...)); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("trailing data error = %v, want serialization error", err)
	}
}

func TestAsyncGoalDeepCopyAndSnapshotImmutability(t *testing.T) {
	spec := testAsyncGoalSpec()
	cp := testAsyncGoalBaseCheckpoint(t)
	spec.AllowedSources[0] = 99
	if cp.AllowedSources[0] != 42 {
		t.Fatalf("constructor retained caller source slice")
	}

	running := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	original := cloneGoalAsyncCheckpoint(running)
	clone := cloneGoalAsyncCheckpoint(running)
	clone.AllowedSources[0] = 99
	clone.AuditRefs = append(clone.AuditRefs, "audit-mutated")
	if !reflect.DeepEqual(running, original) {
		t.Fatalf("clone mutation changed original checkpoint")
	}

	decision, record := testAsyncVerifierDecision(t, running)
	ref := testAsyncEvidenceRef(record)
	req := goalAsyncTransitionRequest{
		OperationID:      "op-copy-met",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		Evidence:         []goalAsyncEvidenceRef{ref},
		Used:             &goalAsyncUsed{ModelRounds: 1, ToolAttempts: 1, ResultBytes: 128, EvidenceRecords: 1, EvidenceBytes: 128},
		Verifier:         &decision,
	}
	next, _ := testAsyncGoalTransition(t, running, req)
	req.Evidence[0].SourceIDs[0] = 99
	if next.Evidence[0].SourceIDs[0] != 42 {
		t.Fatalf("checkpoint retained request evidence source slice")
	}
	if !reflect.DeepEqual(running, original) {
		t.Fatalf("transition mutated previous checkpoint")
	}
}

func TestAsyncGoalConcurrentSnapshots(t *testing.T) {
	cp := testAsyncGoalBaseCheckpoint(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
				OperationID:      "op-concurrent",
				ExpectedRevision: cp.Revision,
				Kind:             goalAsyncTransitionStart,
				Now:              cp.UpdatedAt.Add(time.Second),
				ReasonCode:       goalAsyncReasonStarted,
			})
			if err != nil {
				errs <- err
				return
			}
			if _, err := marshalGoalAsyncCheckpoint(next); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent snapshot: %v", err)
	}
}

func TestAsyncGoalKernelHasNoSideEffectImports(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "goal_async_state.go"))
	if err != nil {
		t.Fatalf("read kernel: %v", err)
	}
	source := string(data)
	start := strings.Index(source, "import (")
	end := strings.Index(source[start:], "\n)")
	if start < 0 || end < 0 {
		t.Fatal("kernel import block not found")
	}
	imports := source[start : start+end]
	for _, forbidden := range []string{"pkg/database", "pkg/crawler", "services/", "net/http", "os/exec", "github.com/"} {
		if strings.Contains(imports, forbidden) {
			t.Fatalf("kernel imports forbidden side-effect package %q", forbidden)
		}
	}
}

func TestAsyncGoalErrorsAreSanitized(t *testing.T) {
	spec := testAsyncGoalSpec()
	spec.Auth = "secret-token-value"
	spec.Prompt = "secret-prompt-value"
	spec.Messages = []any{map[string]any{"role": "user", "content": "secret-message-value"}}
	spec.SpecHash = computeGoalSpecHash(spec)
	cp, err := newGoalAsyncCheckpoint(
		spec, testAsyncGoalIdentity(), testAsyncGoalPrincipal(),
		"run-async-test", "trace-async-test", "",
		"ledger-async-test", strings.Repeat("b", 64),
		testAsyncGoalNow, testAsyncGoalNow.Add(10*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute},
	)
	if err != nil {
		t.Fatalf("new checkpoint: %v", err)
	}
	data, err := marshalGoalAsyncCheckpoint(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"secret-token-value", "secret-prompt-value", "secret-message-value"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("serialized checkpoint leaked %q", secret)
		}
	}

	running := testAsyncGoalStart(t, cp)
	work := testAsyncProposalWork()
	work.RequestID = "secret-token-value"
	_, _, err = applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID: "op-invalid-work", ExpectedRevision: running.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: running.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1},
		Work:       &work,
	})
	if err == nil || strings.Contains(err.Error(), "secret-token-value") {
		t.Fatalf("error leaked or accepted secret work: %v", err)
	}
	_, _, err = applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID: "op-invalid-reason", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionStart, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: "secret-token-value",
	})
	if err == nil || strings.Contains(err.Error(), "secret-token-value") {
		t.Fatalf("error leaked or accepted secret reason: %v", err)
	}
}

func testAsyncGoalBaseCheckpointWithLimits(t *testing.T, limits goalAsyncLimits) goalAsyncCheckpoint {
	t.Helper()
	cp, err := newGoalAsyncCheckpoint(
		testAsyncGoalSpec(), testAsyncGoalIdentity(), testAsyncGoalPrincipal(),
		"run-async-test", "trace-async-test", "",
		"ledger-async-test", strings.Repeat("b", 64),
		testAsyncGoalNow, testAsyncGoalNow.Add(2*time.Minute), limits,
	)
	if err != nil {
		t.Fatalf("new checkpoint: %v", err)
	}
	return cp
}

func TestAsyncGoalConstructionBindings(t *testing.T) {
	limits := goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4}

	cases := []struct {
		name      string
		mutate    func(*AgentGoalSpec, *AgentIdentity, *goalAsyncPrincipal)
		runID     string
		ledgerID  string
		nonce     string
		wantError bool
	}{
		{name: "valid", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64)},
		{name: "missing agent", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(_ *AgentGoalSpec, id *AgentIdentity, _ *goalAsyncPrincipal) { id.AgentID = "" }, wantError: true},
		{name: "missing owner", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(_ *AgentGoalSpec, id *AgentIdentity, _ *goalAsyncPrincipal) { id.Owner = "" }, wantError: true},
		{name: "principal owner mismatch", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(_ *AgentGoalSpec, _ *AgentIdentity, p *goalAsyncPrincipal) { p.Username = "other" }, wantError: true},
		{name: "missing principal user", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(_ *AgentGoalSpec, _ *AgentIdentity, p *goalAsyncPrincipal) { p.UserID = 0 }, wantError: true},
		{name: "empty goal", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.GoalID = "" }, wantError: true},
		{name: "bad spec hash", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.SpecHash = "nothex" }, wantError: true},
		{name: "zero source", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.AllowedSources = []uint64{0} }, wantError: true},
		{name: "duplicate source", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.AllowedSources = []uint64{42, 42} }, wantError: true},
		{name: "bad tool", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.Allowlist = []string{"bad tool"} }, wantError: true},
		{name: "empty acceptance", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64),
			mutate: func(s *AgentGoalSpec, _ *AgentIdentity, _ *goalAsyncPrincipal) { s.Acceptance.Criteria = nil }, wantError: true},
		{name: "missing run", runID: "", ledgerID: "ledger-async-test", nonce: strings.Repeat("b", 64), wantError: true},
		{name: "missing ledger", runID: "run-async-test", ledgerID: "", nonce: strings.Repeat("b", 64), wantError: true},
		{name: "missing nonce", runID: "run-async-test", ledgerID: "ledger-async-test", nonce: "", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testAsyncGoalSpec()
			id := testAsyncGoalIdentity()
			p := testAsyncGoalPrincipal()
			if tc.mutate != nil {
				tc.mutate(s, &id, &p)
			}
			if tc.name != "bad spec hash" {
				s.SpecHash = computeGoalSpecHash(s)
			}
			_, err := newGoalAsyncCheckpoint(s, id, p, tc.runID, "trace-async-test", "",
				tc.ledgerID, tc.nonce, testAsyncGoalNow, testAsyncGoalNow.Add(2*time.Minute), limits)
			if tc.wantError != (err != nil) {
				t.Fatalf("error = %v, wantError=%t", err, tc.wantError)
			}
		})
	}
}

func TestAsyncGoalVerifierBindingFailures(t *testing.T) {
	cp := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	decision, record := testAsyncVerifierDecision(t, cp)
	if err := decision.validate(cp); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	input := GoalEvalInput{
		Spec: testAsyncGoalSpec(), Records: []EvidenceRecord{record},
		Now: cp.UpdatedAt.Add(time.Second), LedgerID: cp.LedgerID,
		RunID: cp.RunID, GoalID: cp.GoalID, GoalSpecHash: cp.GoalSpecHash,
		RunNonceHash: cp.RunNonceHash, AgentID: cp.AgentID, Owner: cp.Owner,
	}
	for name, mutate := range map[string]func(*GoalEvalInput){
		"run":    func(i *GoalEvalInput) { i.RunID = "run-other" },
		"goal":   func(i *GoalEvalInput) { i.GoalID = "goal-other" },
		"spec":   func(i *GoalEvalInput) { i.GoalSpecHash = strings.Repeat("c", 64) },
		"ledger": func(i *GoalEvalInput) { i.LedgerID = "ledger-other" },
		"agent":  func(i *GoalEvalInput) { i.AgentID = "agent-other" },
		"owner":  func(i *GoalEvalInput) { i.Owner = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := input
			mutate(&bad)
			if _, err := newGoalAsyncVerifierDecision(cp, bad); err == nil {
				t.Fatal("expected verifier identity failure")
			}
		})
	}
}

func TestAsyncGoalWorkReferenceFailures(t *testing.T) {
	cp := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	cases := []struct {
		name string
		kind goalAsyncTransitionKind
		work goalAsyncWorkRef
	}{
		{name: "zero source", kind: goalAsyncTransitionAwaitApproval, work: func() goalAsyncWorkRef {
			w := testAsyncProposalWork()
			w.SourceID = 0
			return w
		}()},
		{name: "out of scope", kind: goalAsyncTransitionAwaitApproval, work: func() goalAsyncWorkRef {
			w := testAsyncProposalWork()
			w.SourceID = 99
			return w
		}()},
		{name: "bad request id", kind: goalAsyncTransitionAwaitApproval, work: func() goalAsyncWorkRef {
			w := testAsyncProposalWork()
			w.RequestID = "not-a-hash"
			return w
		}()},
		{name: "proposal with operation", kind: goalAsyncTransitionAwaitApproval, work: func() goalAsyncWorkRef {
			w := testAsyncProposalWork()
			w.OperationID = strings.Repeat("5", 64)
			return w
		}()},
		{name: "operation as proposal", kind: goalAsyncTransitionAwaitApproval, work: testAsyncOperationWork()},
		{name: "operation without approval", kind: goalAsyncTransitionAwaitExternalWork, work: func() goalAsyncWorkRef {
			w := testAsyncOperationWork()
			w.ApprovalID = ""
			return w
		}()},
		{name: "completed operation at scheduling", kind: goalAsyncTransitionAwaitExternalWork, work: func() goalAsyncWorkRef {
			w := testAsyncOperationWork()
			w.State = goalAsyncWorkCompleted
			w.ObservedAt = testAsyncGoalNow
			return w
		}()},
		{name: "approval without operation", kind: goalAsyncTransitionAwaitExternalWork, work: func() goalAsyncWorkRef {
			w := testAsyncOperationWork()
			w.OperationID = ""
			return w
		}()},
		{name: "unknown work kind", kind: goalAsyncTransitionAwaitApproval, work: func() goalAsyncWorkRef {
			w := testAsyncProposalWork()
			w.Kind = goalAsyncWorkKind("crawl_event")
			return w
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.kind == goalAsyncTransitionAwaitExternalWork {
				observation := testAsyncSchedulingObservation(cp, tc.work, cp.UpdatedAt)
				_, err = newGoalAsyncSchedulingPrerequisite(cp, observation)
			} else {
				_, _, err = applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
					OperationID:      "op-work-invalid",
					ExpectedRevision: cp.Revision,
					Kind:             tc.kind,
					Now:              cp.UpdatedAt.Add(time.Second),
					ReasonCode:       goalAsyncReasonAwaitingApproval,
					Used:             &used,
					Work:             &tc.work,
				})
			}
			if err == nil {
				t.Fatal("expected invalid work reference")
			}
		})
	}
}

func TestAsyncGoalDuplicateWorkIdentity(t *testing.T) {
	cp := testAsyncGoalResumeProcessing(t, testAsyncGoalRunningWithWork(t))
	// Running after a completed/processing operation may request a new proposal with
	// the same request ID but different identity. That is contradictory and must fail.
	work := testAsyncProposalWork()
	work.ProposalSHA256 = strings.Repeat("9", 64)
	used := cp.Used
	used.ScheduledWorkRequests++
	_, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-duplicate-work",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitApproval,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingApproval,
		Used:             &used,
		Work:             &work,
	})
	if err == nil {
		t.Fatal("expected duplicate work identity failure")
	}
}

func TestAsyncGoalNoVerifierNoCompletion(t *testing.T) {
	running := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-no-verifier",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("no verifier error = %v, want verification", err)
	}
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-zero-verifier",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		Verifier:         &goalAsyncVerifierDecision{},
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("zero verifier error = %v, want verification", err)
	}
	decision, _ := testAsyncVerifierDecision(t, running)
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-missing-evidence",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		Verifier:         &decision,
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("missing verifier evidence error = %v, want verification", err)
	}
	foreign := decision
	foreign.runID = "run-other"
	foreign.decisionSHA256 = goalAsyncVerifierDecisionHash(foreign)
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-foreign-verifier",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		Verifier:         &foreign,
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("foreign verifier error = %v, want verification", err)
	}
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-wrong-reason",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		ReasonCode:       "model_says_done",
		Verifier:         &decision,
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("wrong reason error = %v, want verification", err)
	}
}

func TestAsyncGoalBudgetsAndDeadline(t *testing.T) {
	base := testAsyncGoalBaseCheckpoint(t)
	start := testAsyncGoalStart(t, base)
	negative := goalAsyncUsed{ModelRounds: -1, ConsecutiveDenials: -1}
	if _, _, err := applyGoalAsyncTransition(base, goalAsyncTransitionRequest{
		OperationID: "op-negative", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted, Used: &negative,
	}); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("negative counter error = %v, want checkpoint invalid", err)
	}
	exceeded := goalAsyncUsed{
		ModelRounds:        base.Ceilings.MaxModelRounds + 1,
		ConsecutiveDenials: base.Ceilings.MaxConsecutiveDenials + 1,
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-exceeded", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionIncomplete, Now: start.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonIncomplete, Used: &exceeded,
	}); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("exceeded counter error = %v, want checkpoint invalid", err)
	}
	regressed := goalAsyncUsed{}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-regressed", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionIncomplete, Now: start.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonIncomplete, Used: &regressed,
	}); err != nil {
		// Zero is not a regression from zero; this is a valid terminal transition.
		t.Fatalf("zero regression transition failed: %v", err)
	}

	limited := testAsyncGoalBaseCheckpointWithLimits(t, goalAsyncLimits{MaxSlices: 1, MaxRetries: 1, MaxScheduledWorkRequests: 2})
	limited = testAsyncGoalStart(t, limited)
	limited = testAsyncGoalAwaitApproval(t, limited)
	limited = testAsyncGoalAwaitExternal(t, limited)
	grant := testAsyncResumeGrant(t, limited, goalAsyncWorkProcessing)
	work := testAsyncOperationWork()
	work.State = goalAsyncWorkProcessing
	if _, _, err := applyGoalAsyncTransition(limited, goalAsyncTransitionRequest{
		OperationID: "op-slice-budget", ExpectedRevision: limited.Revision,
		Kind: goalAsyncTransitionResume, Now: limited.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonResumed, Work: &work, Resume: &grant,
	}); !errors.Is(err, ErrGoalAsyncBudget) {
		t.Fatalf("slice budget error = %v, want budget", err)
	}

	if _, _, err := applyGoalAsyncTransition(base, goalAsyncTransitionRequest{
		OperationID: "op-deadline-start", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.Deadline,
		ReasonCode: goalAsyncReasonStarted,
	}); !errors.Is(err, ErrGoalAsyncDeadline) {
		t.Fatalf("deadline start error = %v, want deadline", err)
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-deadline-incomplete", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionIncomplete, Now: start.Deadline,
		ReasonCode: goalAsyncReasonIncomplete,
	}); !errors.Is(err, ErrGoalAsyncDeadline) {
		t.Fatalf("deadline incomplete error = %v, want deadline", err)
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-premature-budget", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionBudgetExhausted, Now: start.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonBudgetExhausted,
	}); !errors.Is(err, ErrGoalAsyncBudget) {
		t.Fatalf("premature budget error = %v, want budget", err)
	}
	atCeiling := start.Used
	atCeiling.ModelRounds = start.Ceilings.MaxModelRounds
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-incomplete-at-ceiling", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionIncomplete, Now: start.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonIncomplete, Used: &atCeiling,
	}); !errors.Is(err, ErrGoalAsyncBudget) {
		t.Fatalf("incomplete at ceiling error = %v, want budget", err)
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-budget-at-ceiling", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionBudgetExhausted, Now: start.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonBudgetExhausted, Used: &atCeiling,
	}); err != nil {
		t.Fatalf("budget at ceiling: %v", err)
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-deadline-budget", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionBudgetExhausted, Now: start.Deadline,
		ReasonCode: goalAsyncReasonBudgetExhausted,
	}); err != nil {
		t.Fatalf("deadline budget transition: %v", err)
	}
	if _, _, err := applyGoalAsyncTransition(start, goalAsyncTransitionRequest{
		OperationID: "op-deadline-cancel", ExpectedRevision: start.Revision,
		Kind: goalAsyncTransitionCancelled, Now: start.Deadline,
		ReasonCode: goalAsyncReasonCancelled,
	}); err != nil {
		t.Fatalf("deadline cancellation: %v", err)
	}

	zeroCeilings := testAsyncGoalBaseCheckpointWithLimits(t, goalAsyncLimits{MaxSlices: 4, MaxRetries: 0, MaxScheduledWorkRequests: 0})
	if zeroCeilings.Ceilings.MaxRetries != 0 || zeroCeilings.Ceilings.MaxScheduledWorkRequests != 0 {
		t.Fatalf("explicit zero ceilings were defaulted: %+v", zeroCeilings.Ceilings)
	}
	zeroCeilings = testAsyncGoalStart(t, zeroCeilings)
	zeroWork := testAsyncProposalWork()
	if _, _, err := applyGoalAsyncTransition(zeroCeilings, goalAsyncTransitionRequest{
		OperationID: "op-zero-work", ExpectedRevision: zeroCeilings.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: zeroCeilings.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1},
		Work:       &zeroWork,
	}); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("zero work ceiling error = %v, want checkpoint invalid", err)
	}

	denialStart, _ := testAsyncGoalTransition(t, base, goalAsyncTransitionRequest{
		OperationID: "op-denial-start", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted,
		Used:       &goalAsyncUsed{ConsecutiveDenials: 1},
	})
	if denialStart.Used.ConsecutiveDenials != 1 {
		t.Fatalf("consecutive denial count = %d, want 1", denialStart.Used.ConsecutiveDenials)
	}
	if _, _, err := applyGoalAsyncTransition(denialStart, goalAsyncTransitionRequest{
		OperationID: "op-denial-reset", ExpectedRevision: denialStart.Revision,
		Kind: goalAsyncTransitionIncomplete, Now: denialStart.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonIncomplete,
		Used:       &goalAsyncUsed{ConsecutiveDenials: 0},
	}); err != nil {
		t.Fatalf("consecutive denial reset: %v", err)
	}

	// Counters are cumulative across slices.
	startWithCounters, _ := testAsyncGoalTransition(t, base, goalAsyncTransitionRequest{
		OperationID: "op-start-counters", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted,
		Used:       &goalAsyncUsed{ModelRounds: 1, ToolAttempts: 2},
	})
	afterApproval, _ := testAsyncGoalTransition(t, startWithCounters, goalAsyncTransitionRequest{
		OperationID: "op-approval-counters", ExpectedRevision: startWithCounters.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: startWithCounters.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval,
		Used:       &goalAsyncUsed{ModelRounds: 1, ToolAttempts: 2, ScheduledWorkRequests: 1},
		Work:       func() *goalAsyncWorkRef { w := testAsyncProposalWork(); return &w }(),
	})
	if afterApproval.Used.ModelRounds != 1 || afterApproval.Used.ToolAttempts != 2 {
		t.Fatalf("cumulative counters reset: %+v", afterApproval.Used)
	}
}

func TestAsyncGoalResumeAuthorizationFailures(t *testing.T) {
	cp := testAsyncGoalRunningWithWork(t)
	operation := testAsyncOperationWork()
	baseCurrent := goalAsyncCurrentAuthority{
		AgentID: cp.AgentID, Owner: cp.Owner, Principal: testAsyncGoalPrincipal(),
		PolicySHA256: cp.PolicySHA256, AllowedTools: append([]string(nil), cp.AllowedTools...),
		AllowedSources:  append([]uint64(nil), cp.AllowedSources...),
		VerifiedSources: []uint64{42}, AgentEnabled: true,
	}
	cases := map[string]func(*goalAsyncCurrentAuthority){
		"agent":            func(c *goalAsyncCurrentAuthority) { c.AgentID = "agent-other" },
		"owner":            func(c *goalAsyncCurrentAuthority) { c.Owner = "other" },
		"principal":        func(c *goalAsyncCurrentAuthority) { c.Principal.UserID = 8 },
		"policy":           func(c *goalAsyncCurrentAuthority) { c.PolicySHA256 = strings.Repeat("c", 64) },
		"tools":            func(c *goalAsyncCurrentAuthority) { c.AllowedTools = append(c.AllowedTools, "other") },
		"sources":          func(c *goalAsyncCurrentAuthority) { c.AllowedSources = []uint64{43} },
		"verified sources": func(c *goalAsyncCurrentAuthority) { c.VerifiedSources = []uint64{43} },
		"agent disabled":   func(c *goalAsyncCurrentAuthority) { c.AgentEnabled = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			current := baseCurrent
			current.AllowedTools = append([]string(nil), baseCurrent.AllowedTools...)
			current.AllowedSources = append([]uint64(nil), baseCurrent.AllowedSources...)
			current.VerifiedSources = append([]uint64(nil), baseCurrent.VerifiedSources...)
			mutate(&current)
			if _, err := newGoalAsyncResumeGrant(cp, current, operation.OperationID, goalAsyncWorkProcessing, cp.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrGoalAsyncResumeDenied) {
				t.Fatalf("resume error = %v, want denied", err)
			}
		})
	}
}

func TestAsyncGoalResumeWrongOperation(t *testing.T) {
	cp := testAsyncGoalRunningWithWork(t)
	operation := testAsyncOperationWork()
	current := goalAsyncCurrentAuthority{
		AgentID: cp.AgentID, Owner: cp.Owner, Principal: testAsyncGoalPrincipal(),
		PolicySHA256: cp.PolicySHA256, AllowedTools: append([]string(nil), cp.AllowedTools...),
		AllowedSources:  append([]uint64(nil), cp.AllowedSources...),
		VerifiedSources: []uint64{42}, AgentEnabled: true,
	}
	if _, err := newGoalAsyncResumeGrant(cp, current, strings.Repeat("6", 64), goalAsyncWorkProcessing, cp.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrGoalAsyncResumeDenied) {
		t.Fatalf("wrong operation error = %v, want denied", err)
	}
	if _, err := newGoalAsyncResumeGrant(cp, current, operation.OperationID, goalAsyncWorkScheduled, cp.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrGoalAsyncResumeDenied) {
		t.Fatalf("scheduled resume error = %v, want denied", err)
	}
}

func TestAsyncGoalUnknownStateAndRevision(t *testing.T) {
	cp := testAsyncGoalBaseCheckpoint(t)
	badState := cloneGoalAsyncCheckpoint(cp)
	badState.State = "unknown"
	if err := validateGoalAsyncCheckpoint(badState); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("unknown state error = %v", err)
	}
	badRevision := cloneGoalAsyncCheckpoint(cp)
	badRevision.Revision = 0
	if err := validateGoalAsyncCheckpoint(badRevision); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("zero revision error = %v", err)
	}
	badTransition := cloneGoalAsyncCheckpoint(cp)
	badTransition.Revision = 2
	badTransition.LastTransitionID = "op-without-hash"
	if err := validateGoalAsyncCheckpoint(badTransition); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("missing transition hash error = %v", err)
	}
	badCreated := cloneGoalAsyncCheckpoint(cp)
	badCreated.Revision = 2
	badCreated.LastTransitionID = "op-created"
	badCreated.LastTransitionHash = strings.Repeat("c", 64)
	if err := validateGoalAsyncCheckpoint(badCreated); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("created revision error = %v", err)
	}
}

func TestAsyncGoalNoTenantOrSecretFields(t *testing.T) {
	typ := reflect.TypeOf(goalAsyncCheckpoint{})
	for _, forbidden := range []string{"Tenant", "Token", "Password", "Prompt", "Messages", "Arguments", "Payload", "Auth"} {
		if _, ok := typ.FieldByName(forbidden); ok {
			t.Fatalf("checkpoint exposes forbidden field %q", forbidden)
		}
	}
}

func TestAsyncGoalEvidenceReferenceFailures(t *testing.T) {
	running := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	decision, record := testAsyncVerifierDecision(t, running)
	validRef := testAsyncEvidenceRef(record)
	used := goalAsyncUsed{ModelRounds: 1, ToolAttempts: 1, ResultBytes: 128, EvidenceRecords: 1, EvidenceBytes: 128}

	cases := []struct {
		name     string
		evidence []goalAsyncEvidenceRef
		used     goalAsyncUsed
	}{
		{name: "out of scope", evidence: []goalAsyncEvidenceRef{func() goalAsyncEvidenceRef {
			r := validRef
			r.SourceIDs = []uint64{99}
			return r
		}()}, used: used},
		{name: "count not advanced", evidence: []goalAsyncEvidenceRef{validRef}, used: func() goalAsyncUsed {
			u := used
			u.EvidenceRecords = 0
			return u
		}()},
		{name: "duplicate evidence", evidence: []goalAsyncEvidenceRef{validRef, validRef}, used: func() goalAsyncUsed {
			u := used
			u.EvidenceRecords = 2
			return u
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := goalAsyncTransitionRequest{
				OperationID:      "op-evidence-invalid",
				ExpectedRevision: running.Revision,
				Kind:             goalAsyncTransitionCriteriaMet,
				Now:              running.UpdatedAt.Add(time.Second),
				Used:             &tc.used,
				Evidence:         tc.evidence,
				Verifier:         &decision,
			}
			if _, _, err := applyGoalAsyncTransition(running, req); err == nil {
				t.Fatal("expected evidence failure")
			}
		})
	}

	highUsed := goalAsyncUsed{EvidenceRecords: 4, EvidenceBytes: 512}
	baseHigh := testAsyncGoalBaseCheckpoint(t)
	high, _ := testAsyncGoalTransition(t, baseHigh, goalAsyncTransitionRequest{
		OperationID: "op-high-evidence-start", ExpectedRevision: baseHigh.Revision,
		Kind: goalAsyncTransitionStart, Now: testAsyncGoalNow.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted, Used: &highUsed,
	})
	highDecision, highRecord := testAsyncVerifierDecision(t, high)
	highRef := testAsyncEvidenceRef(highRecord)
	if _, _, err := applyGoalAsyncTransition(high, goalAsyncTransitionRequest{
		OperationID: "op-high-evidence-met", ExpectedRevision: high.Revision,
		Kind: goalAsyncTransitionCriteriaMet, Now: high.UpdatedAt.Add(time.Second),
		Used: &highUsed, Evidence: []goalAsyncEvidenceRef{highRef}, Verifier: &highDecision,
	}); err == nil {
		t.Fatal("expected undercounted evidence failure")
	}
}

func TestAsyncGoalCheckpointDeadlineAndReason(t *testing.T) {
	running := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	late := cloneGoalAsyncCheckpoint(running)
	late.UpdatedAt = late.Deadline
	if err := validateGoalAsyncCheckpoint(late); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("late active checkpoint error = %v, want invalid", err)
	}
	lateCreated := testAsyncGoalBaseCheckpoint(t)
	lateCreated.UpdatedAt = lateCreated.Deadline
	if err := validateGoalAsyncCheckpoint(lateCreated); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("late created checkpoint error = %v, want invalid", err)
	}
	met := testAsyncGoalCriteriaMet(t, running)
	badReason := cloneGoalAsyncCheckpoint(met)
	badReason.ReasonCode = goalAsyncReasonCreated
	if err := validateGoalAsyncCheckpoint(badReason); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("bad criteria reason error = %v, want invalid", err)
	}
	missingEvidence := cloneGoalAsyncCheckpoint(met)
	missingEvidence.Evidence = nil
	if err := validateGoalAsyncCheckpoint(missingEvidence); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("missing verification evidence error = %v, want invalid", err)
	}
}

func TestAsyncGoalTerminalPredicate(t *testing.T) {
	terminal := []goalAsyncState{
		goalAsyncCriteriaMet, goalAsyncIncomplete, goalAsyncBudgetExhausted,
		goalAsyncCancelled, goalAsyncDenied, goalAsyncFailed,
	}
	active := []goalAsyncState{goalAsyncCreated, goalAsyncRunning, goalAsyncAwaitingApproval, goalAsyncAwaitingExternalWork}
	for _, state := range terminal {
		if !state.terminal() {
			t.Fatalf("%s should be terminal", state)
		}
		if goalAsyncTransitionAllowed(state, goalAsyncTransitionStart) {
			t.Fatalf("terminal state %s permits a start transition", state)
		}
	}
	for _, state := range active {
		if state.terminal() {
			t.Fatalf("%s should not be terminal", state)
		}
	}
	if goalAsyncState("unknown").terminal() || goalAsyncState("unknown").valid() {
		t.Fatal("unknown state was accepted")
	}
}

func TestAsyncGoalUnknownTransitionKind(t *testing.T) {
	cp := testAsyncGoalBaseCheckpoint(t)
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID: "op-unknown-kind", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionKind("unknown"), Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted,
	}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
		t.Fatalf("unknown kind error = %v, want transition invalid", err)
	}
}

func TestAsyncGoalFutureProofsRejected(t *testing.T) {
	running := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	observedAt := running.UpdatedAt.Add(2 * time.Second)
	record := testAsyncEvidenceRecord(running, observedAt)
	input := GoalEvalInput{
		Spec: testAsyncGoalSpec(), Records: []EvidenceRecord{record}, Now: observedAt,
		LedgerID: running.LedgerID, RunID: running.RunID, GoalID: running.GoalID,
		GoalSpecHash: running.GoalSpecHash, RunNonceHash: running.RunNonceHash,
		AgentID: running.AgentID, Owner: running.Owner,
	}
	decision, err := newGoalAsyncVerifierDecision(running, input)
	if err != nil {
		t.Fatalf("verifier decision: %v", err)
	}
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID:      "op-future-verifier",
		ExpectedRevision: running.Revision,
		Kind:             goalAsyncTransitionCriteriaMet,
		Now:              running.UpdatedAt.Add(time.Second),
		Verifier:         &decision,
	}); !errors.Is(err, ErrGoalAsyncVerification) {
		t.Fatalf("future verifier error = %v, want verification", err)
	}

	cp := testAsyncGoalRunningWithWork(t)
	grant := testAsyncResumeGrant(t, cp, goalAsyncWorkProcessing)
	work := testAsyncOperationWork()
	work.State = goalAsyncWorkProcessing
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-future-resume",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionResume,
		Now:              cp.UpdatedAt,
		ReasonCode:       goalAsyncReasonResumed,
		Work:             &work,
		Resume:           &grant,
	}); !errors.Is(err, ErrGoalAsyncResumeDenied) {
		t.Fatalf("future resume error = %v, want denied", err)
	}
}

func TestAsyncGoalTransitionTableMatchesTargets(t *testing.T) {
	for from, targets := range goalAsyncTransitionTable {
		if !from.valid() {
			t.Fatalf("transition table has invalid source state %q", from)
		}
		for kind, target := range targets {
			if !kind.valid() {
				t.Fatalf("transition table has invalid kind %q", kind)
			}
			if kind.target() != target {
				t.Fatalf("%s -> %s target = %s, table says %s", from, kind, kind.target(), target)
			}
			if target.terminal() && target != goalAsyncCriteriaMet && target != goalAsyncIncomplete &&
				target != goalAsyncBudgetExhausted && target != goalAsyncCancelled &&
				target != goalAsyncDenied && target != goalAsyncFailed {
				t.Fatalf("unexpected terminal target %s", target)
			}
		}
	}
}

func TestAsyncGoalImmutableBindings(t *testing.T) {
	base := testAsyncGoalBaseCheckpoint(t)
	cp := testAsyncGoalStart(t, base)
	cp = testAsyncGoalAwaitApproval(t, cp)
	cp = testAsyncGoalAwaitExternal(t, cp)
	cp = testAsyncGoalResumeProcessing(t, cp)
	cp = testAsyncGoalCriteriaMet(t, cp)

	if !cp.CreatedAt.Equal(base.CreatedAt) || !cp.Deadline.Equal(base.Deadline) {
		t.Fatalf("timestamps changed: %+v", cp)
	}
	if cp.Ceilings != base.Ceilings {
		t.Fatalf("ceilings changed: %+v", cp.Ceilings)
	}
	if cp.RunID != base.RunID || cp.TraceID != base.TraceID || cp.CorrelationID != base.CorrelationID ||
		cp.AgentID != base.AgentID || cp.Owner != base.Owner || cp.GoalID != base.GoalID ||
		cp.GoalSpecHash != base.GoalSpecHash || cp.PolicySHA256 != base.PolicySHA256 ||
		cp.LedgerID != base.LedgerID || cp.RunNonceHash != base.RunNonceHash ||
		cp.PrincipalKind != base.PrincipalKind || cp.PrincipalUserID != base.PrincipalUserID ||
		cp.PrincipalUsername != base.PrincipalUsername || cp.PrincipalIssuer != base.PrincipalIssuer {
		t.Fatalf("identity binding changed")
	}
	if !reflect.DeepEqual(cp.AllowedTools, base.AllowedTools) ||
		!reflect.DeepEqual(cp.AllowedSources, base.AllowedSources) {
		t.Fatalf("frozen scope changed")
	}
	if cp.Used.ModelRounds < base.Used.ModelRounds || cp.Used.ToolAttempts < base.Used.ToolAttempts ||
		cp.Used.EvidenceRecords < base.Used.EvidenceRecords || cp.Used.EvidenceBytes < base.Used.EvidenceBytes ||
		cp.Used.ScheduledWorkRequests < base.Used.ScheduledWorkRequests {
		t.Fatalf("cumulative counters regressed: %+v", cp.Used)
	}
}

func testAsyncCurrentAuthority(cp goalAsyncCheckpoint) goalAsyncCurrentAuthority {
	return goalAsyncCurrentAuthority{
		AgentID: cp.AgentID, Owner: cp.Owner, Principal: testAsyncGoalPrincipal(),
		PolicySHA256:    cp.PolicySHA256,
		AllowedTools:    append([]string(nil), cp.AllowedTools...),
		AllowedSources:  append([]uint64(nil), cp.AllowedSources...),
		VerifiedSources: []uint64{42}, AgentEnabled: true,
	}
}

func TestAsyncGoalLogicalLifetimeAndSliceCeiling(t *testing.T) {
	base := testAsyncGoalBaseCheckpoint(t)
	if base.Deadline.Sub(base.CreatedAt) != 10*time.Minute {
		t.Fatalf("logical lifetime = %s, want 10m", base.Deadline.Sub(base.CreatedAt))
	}
	if base.Ceilings.MaxSliceElapsed != 2*time.Minute {
		t.Fatalf("slice ceiling = %s, want 2m", base.Ceilings.MaxSliceElapsed)
	}
	sliceDeadline, err := goalAsyncSliceDeadline(base, testAsyncGoalNow.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("slice deadline: %v", err)
	}
	if sliceDeadline.Sub(testAsyncGoalNow.Add(3*time.Minute)) != 2*time.Minute {
		t.Fatalf("slice deadline = %s, want 2m", sliceDeadline.Sub(testAsyncGoalNow.Add(3*time.Minute)))
	}
	lateSliceDeadline, err := goalAsyncSliceDeadline(base, testAsyncGoalNow.Add(9*time.Minute))
	if err != nil || !lateSliceDeadline.Equal(base.Deadline) {
		t.Fatalf("slice deadline did not cap at logical deadline: %s %v", lateSliceDeadline, err)
	}
	if _, err := goalAsyncSliceDeadline(base, base.Deadline); !errors.Is(err, ErrGoalAsyncDeadline) {
		t.Fatalf("deadline slice error = %v, want deadline", err)
	}

	cp := testAsyncGoalStart(t, base)
	cp = testAsyncGoalAwaitApproval(t, cp)

	acceptedAt := testAsyncGoalNow.Add(3 * time.Minute)
	work := testAsyncOperationWorkAt(acceptedAt)
	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, acceptedAt)
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-long-schedule", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: acceptedAt,
		ReasonCode: goalAsyncReasonAwaitingExternalWork,
		Used:       &used, Scheduling: &scheduling,
	})
	if cp.State != goalAsyncAwaitingExternalWork || !cp.Deadline.Equal(base.Deadline) {
		t.Fatalf("unexpected long-wait checkpoint: %+v", cp)
	}
	awaitingExternal := cp

	grant, err := newGoalAsyncResumeGrant(cp, testAsyncCurrentAuthority(cp), work.OperationID, goalAsyncWorkProcessing, testAsyncGoalNow.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("long-wait resume grant: %v", err)
	}
	processing := work
	processing.State = goalAsyncWorkProcessing
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-long-resume", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionResume, Now: testAsyncGoalNow.Add(4 * time.Minute),
		ReasonCode: goalAsyncReasonResumed, Work: &processing, Resume: &grant,
	})
	if cp.Slices != 2 || cp.Deadline.Sub(cp.CreatedAt) != 10*time.Minute ||
		cp.Used.ScheduledWorkRequests != 1 {
		t.Fatalf("long wait reset progress: %+v", cp)
	}
	cp = testAsyncGoalCriteriaMet(t, cp)
	if cp.State != goalAsyncCriteriaMet {
		t.Fatalf("long wait completion state = %s", cp.State)
	}

	// A completion at the logical deadline is refused.
	running := testAsyncGoalStart(t, base)
	decision, record := testAsyncVerifierDecision(t, running)
	ref := testAsyncEvidenceRef(record)
	used = running.Used
	used.ModelRounds = 1
	used.ToolAttempts = 1
	used.ResultBytes = 128
	used.EvidenceRecords = 1
	used.EvidenceBytes = 128
	if _, _, err := applyGoalAsyncTransition(running, goalAsyncTransitionRequest{
		OperationID: "op-deadline-met", ExpectedRevision: running.Revision,
		Kind: goalAsyncTransitionCriteriaMet, Now: running.Deadline,
		Used: &used, Evidence: []goalAsyncEvidenceRef{ref}, Verifier: &decision,
	}); !errors.Is(err, ErrGoalAsyncDeadline) {
		t.Fatalf("deadline completion error = %v, want deadline", err)
	}

	// A resume at the logical deadline is refused even with a previously valid grant.
	if _, _, err := applyGoalAsyncTransition(awaitingExternal, goalAsyncTransitionRequest{
		OperationID: "op-deadline-resume", ExpectedRevision: awaitingExternal.Revision,
		Kind: goalAsyncTransitionResume, Now: awaitingExternal.Deadline,
		ReasonCode: goalAsyncReasonResumed, Work: &processing, Resume: &grant,
	}); !errors.Is(err, ErrGoalAsyncDeadline) {
		t.Fatalf("deadline resume error = %v, want deadline", err)
	}
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID: "op-terminal-resume", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionResume, Now: cp.Deadline,
		ReasonCode: goalAsyncReasonResumed,
	}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
		// A completed goal is terminal; the table must reject before any grant.
		t.Fatalf("terminal resume error = %v, want transition invalid", err)
	}
}

func TestAsyncGoalLogicalLifetimeBounds(t *testing.T) {
	spec := testAsyncGoalSpec()
	identity := testAsyncGoalIdentity()
	principal := testAsyncGoalPrincipal()
	baseArgs := func() (goalAsyncLimits, time.Time) {
		return goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 10 * time.Minute}, testAsyncGoalNow.Add(10 * time.Minute)
	}
	cases := []struct {
		name     string
		limits   goalAsyncLimits
		deadline time.Time
		mutate   func(*AgentGoalSpec)
	}{
		{name: "negative lifetime", limits: goalAsyncLimits{LogicalLifetime: -time.Minute}, deadline: testAsyncGoalNow.Add(10 * time.Minute)},
		{name: "oversized lifetime", limits: goalAsyncLimits{LogicalLifetime: 25 * time.Hour}, deadline: testAsyncGoalNow.Add(25 * time.Hour)},
		{name: "deadline extension", limits: func() goalAsyncLimits { l, _ := baseArgs(); return l }(), deadline: testAsyncGoalNow.Add(11 * time.Minute)},
		{name: "deadline in past", limits: func() goalAsyncLimits { l, _ := baseArgs(); return l }(), deadline: testAsyncGoalNow},
		{name: "oversized slice", limits: func() goalAsyncLimits { l, _ := baseArgs(); return l }(), deadline: testAsyncGoalNow.Add(10 * time.Minute),
			mutate: func(s *AgentGoalSpec) { s.Budgets.MaxElapsed = 3 * time.Minute }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := spec
			specCopy := *spec
			s = &specCopy
			if tc.mutate != nil {
				tc.mutate(s)
				s.SpecHash = computeGoalSpecHash(s)
			}
			if _, err := newGoalAsyncCheckpoint(s, identity, principal, "run-async-test", "trace-async-test", "",
				"ledger-async-test", strings.Repeat("b", 64), testAsyncGoalNow, tc.deadline, tc.limits); err == nil {
				t.Fatal("expected lifetime/slice validation failure")
			}
		})
	}

	base := testAsyncGoalBaseCheckpoint(t)
	if _, _, err := applyGoalAsyncTransition(base, goalAsyncTransitionRequest{
		OperationID: "op-backwards", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.CreatedAt.Add(-time.Second),
		ReasonCode: goalAsyncReasonStarted,
	}); err == nil {
		t.Fatal("expected backwards clock failure")
	}
}

func TestAsyncGoalSchedulingAdmission(t *testing.T) {
	cp := testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t))
	work := testAsyncOperationWork()
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID: "op-bare-work", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork, Used: &used, Work: &work,
	}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
		t.Fatalf("bare work error = %v, want transition invalid", err)
	}

	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, cp.UpdatedAt)
	next, _ := testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-scheduled", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork, Used: &used, Scheduling: &scheduling,
	})
	if next.State != goalAsyncAwaitingExternalWork {
		t.Fatalf("scheduled state = %s", next.State)
	}

	awaiting := testAsyncGoalAwaitApproval(t, testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t)))
	mutations := map[string]func(*goalAsyncSchedulingObservation){
		"run":       func(o *goalAsyncSchedulingObservation) { o.RunID = "run-other" },
		"goal":      func(o *goalAsyncSchedulingObservation) { o.GoalID = "goal-other" },
		"spec":      func(o *goalAsyncSchedulingObservation) { o.GoalSpecHash = strings.Repeat("c", 64) },
		"agent":     func(o *goalAsyncSchedulingObservation) { o.AgentID = "agent-other" },
		"owner":     func(o *goalAsyncSchedulingObservation) { o.Owner = "other" },
		"principal": func(o *goalAsyncSchedulingObservation) { o.Principal.UserID = 8 },
		"source":    func(o *goalAsyncSchedulingObservation) { o.Work.SourceID = 99 },
		"request":   func(o *goalAsyncSchedulingObservation) { o.Work.RequestID = strings.Repeat("6", 64) },
		"proposal":  func(o *goalAsyncSchedulingObservation) { o.Work.ProposalSHA256 = strings.Repeat("6", 64) },
		"completed": func(o *goalAsyncSchedulingObservation) {
			o.Work.State = goalAsyncWorkCompleted
			o.Work.ObservedAt = o.ObservedAt
		},
		"approval invalid":  func(o *goalAsyncSchedulingObservation) { o.ApprovalValid = false },
		"observed before":   func(o *goalAsyncSchedulingObservation) { o.ObservedAt = o.Work.AcceptedAt.Add(-time.Second) },
		"observed deadline": func(o *goalAsyncSchedulingObservation) { o.ObservedAt = awaiting.Deadline },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			obs := testAsyncSchedulingObservation(awaiting, testAsyncOperationWork(), awaiting.UpdatedAt)
			mutate(&obs)
			if _, err := newGoalAsyncSchedulingPrerequisite(awaiting, obs); err == nil {
				t.Fatal("expected scheduling admission failure")
			}
		})
	}

	// A proposal result with no accepted operation is not a scheduling proof.
	proposalOnly := testAsyncProposalWork()
	if _, err := newGoalAsyncSchedulingPrerequisite(cp, testAsyncSchedulingObservation(cp, proposalOnly, cp.UpdatedAt)); err == nil {
		t.Fatal("proposal-only scheduling proof was accepted")
	}

	// An observation from the future cannot be used to advance the checkpoint early.
	futureWork := testAsyncOperationWork()
	future := testAsyncSchedulingPrerequisite(t, cp, futureWork, cp.UpdatedAt.Add(10*time.Second))
	if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID: "op-future-schedule", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork, Used: &used, Scheduling: &future,
	}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
		t.Fatalf("future scheduling observation error = %v, want transition invalid", err)
	}
}

func TestAsyncGoalCompletionAfterApprovalExpiry(t *testing.T) {
	cp := testAsyncGoalAwaitApproval(t, testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t)))
	work := testAsyncOperationWorkAt(testAsyncGoalNow.Add(time.Minute))
	work.ApprovalExpiresAt = testAsyncGoalNow.Add(3 * time.Minute)
	work.SubmitBy = testAsyncGoalNow.Add(2 * time.Minute)
	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, testAsyncGoalNow.Add(time.Minute))
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-expiry-schedule", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: testAsyncGoalNow.Add(time.Minute),
		ReasonCode: goalAsyncReasonAwaitingExternalWork, Used: &used, Scheduling: &scheduling,
	})

	grant, err := newGoalAsyncResumeGrant(cp, testAsyncCurrentAuthority(cp), work.OperationID, goalAsyncWorkCompleted, testAsyncGoalNow.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("completion grant: %v", err)
	}
	completed := work
	completed.State = goalAsyncWorkCompleted
	completed.ObservedAt = testAsyncGoalNow.Add(6 * time.Minute)
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-expiry-complete", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionResume, Now: testAsyncGoalNow.Add(6 * time.Minute),
		ReasonCode: goalAsyncReasonResumed, Work: &completed, Resume: &grant,
	})
	if cp.State != goalAsyncRunning || cp.Work[0].State != goalAsyncWorkCompleted {
		t.Fatalf("completion after approval expiry not represented: %+v", cp)
	}
	cp = testAsyncGoalCriteriaMet(t, cp)
	if cp.State != goalAsyncCriteriaMet {
		t.Fatalf("completion after approval expiry state = %s", cp.State)
	}

	// ObservedAt before acceptedAt, and mismatched provenance, are rejected.
	badCases := map[string]func(*goalAsyncWorkRef){
		"fabricated observed time": func(w *goalAsyncWorkRef) { w.ObservedAt = work.AcceptedAt.Add(-time.Second) },
		"wrong source":             func(w *goalAsyncWorkRef) { w.SourceID = 43 },
		"wrong proposal":           func(w *goalAsyncWorkRef) { w.ProposalSHA256 = strings.Repeat("6", 64) },
		"wrong operation":          func(w *goalAsyncWorkRef) { w.OperationID = strings.Repeat("6", 64) },
		"wrong accepted time":      func(w *goalAsyncWorkRef) { w.AcceptedAt = work.AcceptedAt.Add(time.Second) },
	}
	for name, mutate := range badCases {
		t.Run(name, func(t *testing.T) {
			fresh := testAsyncGoalRunningWithWork(t)
			bad := completed
			mutate(&bad)
			if _, _, err := applyGoalAsyncTransition(fresh, goalAsyncTransitionRequest{
				OperationID: "op-bad-completion", ExpectedRevision: fresh.Revision,
				Kind: goalAsyncTransitionResume, Now: testAsyncGoalNow.Add(6 * time.Minute),
				ReasonCode: goalAsyncReasonResumed, Work: &bad, Resume: &grant,
			}); err == nil {
				t.Fatal("mismatched completion provenance was accepted")
			}
		})
	}
}

func TestAsyncGoalExpiredApprovalFreshSchedule(t *testing.T) {
	cp := testAsyncGoalAwaitApproval(t, testAsyncGoalStart(t, testAsyncGoalBaseCheckpoint(t)))
	work := testAsyncOperationWorkAt(testAsyncGoalNow.Add(4 * time.Minute))
	work.ApprovalExpiresAt = testAsyncGoalNow.Add(3 * time.Minute)
	work.SubmitBy = testAsyncGoalNow.Add(2 * time.Minute)
	if _, err := newGoalAsyncSchedulingPrerequisite(cp, testAsyncSchedulingObservation(cp, work, testAsyncGoalNow.Add(4*time.Minute))); err == nil {
		t.Fatal("expired approval scheduling proof was accepted")
	}
	work = testAsyncOperationWork()
	obs := testAsyncSchedulingObservation(cp, work, cp.UpdatedAt)
	obs.ApprovalValid = false
	if _, err := newGoalAsyncSchedulingPrerequisite(cp, obs); err == nil {
		t.Fatal("invalid approval scheduling proof was accepted")
	}
}

func TestAsyncGoalStrictCheckpointDecoding(t *testing.T) {
	cp := testAsyncGoalRunningWithWork(t)
	cp = testAsyncGoalResumeProcessing(t, cp)
	cp = testAsyncGoalCriteriaMet(t, cp)
	cp.AuditRefs = []string{"audit-1"}
	data, err := marshalGoalAsyncCheckpoint(cp)
	if err != nil {
		t.Fatalf("marshal canonical checkpoint: %v", err)
	}
	parsed, err := parseGoalAsyncCheckpoint(data)
	if err != nil {
		t.Fatalf("parse canonical checkpoint: %v", err)
	}
	second, err := marshalGoalAsyncCheckpoint(parsed)
	if err != nil || !bytes.Equal(data, second) {
		t.Fatalf("canonical round trip changed bytes: %v", err)
	}
	digest, err := goalAsyncCheckpointDigest(cp)
	if err != nil {
		t.Fatalf("checkpoint digest: %v", err)
	}
	tampered := cloneGoalAsyncCheckpoint(cp)
	tampered.TraceID = "trace-other"
	tamperedDigest, err := goalAsyncCheckpointDigest(tampered)
	if err != nil || tamperedDigest == digest {
		t.Fatalf("digest did not change for bound field: %v", err)
	}
	noncanonical := cloneGoalAsyncCheckpoint(cp)
	noncanonical.AllowedSources = []uint64{43, 42}
	if _, err := marshalGoalAsyncCheckpoint(noncanonical); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
		t.Fatalf("noncanonical marshal error = %v, want checkpoint invalid", err)
	}

	decode := func() map[string]any {
		var object map[string]any
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatalf("unmarshal canonical object: %v", err)
		}
		return object
	}
	encode := func(object map[string]any) []byte {
		encoded, err := json.Marshal(object)
		if err != nil {
			t.Fatalf("marshal mutated object: %v", err)
		}
		return encoded
	}

	cases := map[string]func(map[string]any){
		"zero duplicate sources": func(o map[string]any) { o["allowed_sources"] = []any{0, 42, 42} },
		"unsorted sources":       func(o map[string]any) { o["allowed_sources"] = []any{43, 42} },
		"duplicate tools":        func(o map[string]any) { o["allowed_tools"] = []any{"get_source_status", "get_source_status"} },
		"duplicate evidence":     func(o map[string]any) { o["evidence"].([]any)[0].(map[string]any)["source_ids"] = []any{42, 42} },
		"duplicate verifier IDs": func(o map[string]any) {
			id := cp.Evidence[0].EvidenceID
			o["verification"].(map[string]any)["evidence_ids"] = []any{id, id}
		},
		"duplicate audit IDs": func(o map[string]any) { o["audit_refs"] = []any{"audit-1", "audit-1"} },
		"overlong sources": func(o map[string]any) {
			values := make([]any, 33)
			for i := range values {
				values[i] = i + 1
			}
			o["allowed_sources"] = values
		},
		"invalid timestamp": func(o map[string]any) { o["created_at"] = "not-a-time" },
		"foreign verifier evidence": func(o map[string]any) {
			o["verification"].(map[string]any)["evidence_ids"] = []any{"ev_" + strings.Repeat("f", 32)}
		},
		"unknown field": func(o map[string]any) { o["unknown_field"] = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			object := decode()
			mutate(object)
			if _, err := parseGoalAsyncCheckpoint(encode(object)); err == nil {
				t.Fatal("malformed checkpoint was accepted")
			}
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"version 1":   func(o map[string]any) { o["checkpoint_version"] = float64(1) },
		"version 3":   func(o map[string]any) { o["checkpoint_version"] = float64(3) },
		"contract v1": func(o map[string]any) { o["contract_version"] = "goal-async-v1" },
	} {
		t.Run(name, func(t *testing.T) {
			object := decode()
			mutate(object)
			if _, err := parseGoalAsyncCheckpoint(encode(object)); !errors.Is(err, ErrGoalAsyncCheckpointInvalid) {
				t.Fatalf("version error = %v, want checkpoint invalid", err)
			}
		})
	}

	duplicateState := strings.Replace(string(data), `"state":"criteria_met"`, `"state":"criteria_met","state":"criteria_met"`, 1)
	if _, err := parseGoalAsyncCheckpoint([]byte(duplicateState)); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("duplicate state key error = %v, want serialization", err)
	}
	evidenceKey := `"evidence_id":"` + cp.Evidence[0].EvidenceID + `"`
	duplicateEvidence := strings.Replace(string(data), evidenceKey, evidenceKey+","+evidenceKey, 1)
	if _, err := parseGoalAsyncCheckpoint([]byte(duplicateEvidence)); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("duplicate nested key error = %v, want serialization", err)
	}
	if _, err := parseGoalAsyncCheckpoint(append(data, []byte("{}")...)); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("trailing data error = %v, want serialization", err)
	}
	oversized := make([]byte, maxGoalAsyncCheckpointBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if _, err := parseGoalAsyncCheckpoint(oversized); !errors.Is(err, ErrGoalAsyncSerialization) {
		t.Fatalf("oversized error = %v, want serialization", err)
	}
}

func TestAsyncGoalCountersSurviveSlices(t *testing.T) {
	base := testAsyncGoalBaseCheckpoint(t)
	counters := goalAsyncUsed{
		ModelRounds: 1, ToolAttempts: 2, ResultBytes: 128,
		Retries: 1, ConsecutiveDenials: 1,
	}
	cp, _ := testAsyncGoalTransition(t, base, goalAsyncTransitionRequest{
		OperationID: "op-counter-start", ExpectedRevision: base.Revision,
		Kind: goalAsyncTransitionStart, Now: base.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted, Used: &counters,
	})
	proposal := testAsyncProposalWork()
	counters.ScheduledWorkRequests = 1
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-counter-approval", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval, Used: &counters, Work: &proposal,
	})
	operation := testAsyncOperationWork()
	scheduling := testAsyncSchedulingPrerequisite(t, cp, operation, cp.UpdatedAt)
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-counter-schedule", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork, Used: &counters, Scheduling: &scheduling,
	})
	grant := testAsyncResumeGrant(t, cp, goalAsyncWorkProcessing)
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-counter-resume", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionResume, Now: cp.UpdatedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonResumed, Work: func() *goalAsyncWorkRef {
			w := operation
			w.State = goalAsyncWorkProcessing
			return &w
		}(), Resume: &grant,
	})
	if cp.Used.ModelRounds != 1 || cp.Used.ToolAttempts != 2 || cp.Used.ResultBytes != 128 ||
		cp.Used.Retries != 1 || cp.Used.ScheduledWorkRequests != 1 || cp.Used.ConsecutiveDenials != 1 {
		t.Fatalf("counters changed across slices: %+v", cp.Used)
	}
	decision, record := testAsyncVerifierDecision(t, cp)
	ref := testAsyncEvidenceRef(record)
	counters.EvidenceRecords = 1
	counters.EvidenceBytes = 128
	cp, _ = testAsyncGoalTransition(t, cp, goalAsyncTransitionRequest{
		OperationID: "op-counter-met", ExpectedRevision: cp.Revision,
		Kind: goalAsyncTransitionCriteriaMet, Now: cp.UpdatedAt.Add(time.Second),
		Used: &counters, Evidence: []goalAsyncEvidenceRef{ref}, Verifier: &decision,
	})
	if cp.Used.ModelRounds != 1 || cp.Used.ToolAttempts != 2 || cp.Used.ResultBytes != 128 ||
		cp.Used.Retries != 1 || cp.Used.ScheduledWorkRequests != 1 || cp.Used.ConsecutiveDenials != 1 ||
		cp.Used.EvidenceRecords != 1 || cp.Used.EvidenceBytes != 128 {
		t.Fatalf("completion counters changed: %+v", cp.Used)
	}
}

func TestAsyncGoalTerminalCannotResumeWithCompletion(t *testing.T) {
	completed := testAsyncOperationWork()
	completed.State = goalAsyncWorkCompleted
	completed.ObservedAt = testAsyncGoalNow.Add(2 * time.Minute)
	for _, state := range []goalAsyncState{goalAsyncCriteriaMet, goalAsyncCancelled, goalAsyncDenied, goalAsyncFailed} {
		cp := testAsyncGoalCheckpointInState(t, state)
		if _, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
			OperationID: "op-terminal-completion", ExpectedRevision: cp.Revision,
			Kind: goalAsyncTransitionResume, Now: cp.UpdatedAt.Add(time.Second),
			ReasonCode: goalAsyncReasonResumed, Work: &completed,
		}); !errors.Is(err, ErrGoalAsyncTransitionInvalid) {
			t.Fatalf("%s resume error = %v, want transition invalid", state, err)
		}
	}
}
