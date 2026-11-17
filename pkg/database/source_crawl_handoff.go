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

package database

// Internal, pure-Go contract for the future authenticated crawler handoff and
// fenced terminal-result protocol. Nothing in this file reads or writes the
// database, starts a crawler, or authorizes a mutation. The returned values
// describe an identity that a future database transaction must revalidate
// under its own lock before any privileged write.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// sourceCrawlClaimRequestFingerprint is a pure canonical digest of every
// request field that influences a claim. It is not persisted in this package:
// a durable digest and an explicit empty-batch acknowledgement would require a
// separately approved additive schema change.
func sourceCrawlClaimRequestFingerprint(req SourceCrawlClaimRequest) (string, error) {
	if err := validateSourceCrawlClaimRequest(req); err != nil {
		return "", err
	}
	payload := struct {
		ClaimRequestID   string
		Limit            int
		Priority         string
		EngineID         string
		LeaseNS          int64
		LastOKUpdateNS   int64
		LastErrorNS      int64
		RegularCrawlNS   int64
		ProcessingTimeNS int64
	}{
		ClaimRequestID:   strings.TrimSpace(req.ClaimRequestID),
		Limit:            req.Limit,
		Priority:         strings.ToLower(strings.TrimSpace(req.Priority)),
		EngineID:         strings.TrimSpace(req.EngineID),
		LeaseNS:          int64(req.Lease.Truncate(time.Microsecond)),
		LastOKUpdateNS:   int64(req.LastOKUpdate.Truncate(time.Microsecond)),
		LastErrorNS:      int64(req.LastErrorInterval.Truncate(time.Microsecond)),
		RegularCrawlNS:   int64(req.RegularCrawling.Truncate(time.Microsecond)),
		ProcessingTimeNS: int64(req.ProcessingTimeout.Truncate(time.Microsecond)),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: claim request fingerprint", errSourceCrawlAttemptInvalid)
	}
	sum := sha256.Sum256(append([]byte("crowler-source-crawl-claim-request-v1\x00"), encoded...))
	return hex.EncodeToString(sum[:]), nil
}

type sourceCrawlHandoffAttribution string

const (
	sourceCrawlHandoffBound           sourceCrawlHandoffAttribution = "bound"
	sourceCrawlHandoffUnattributed    sourceCrawlHandoffAttribution = "unattributed"
	sourceCrawlHandoffAmbiguous       sourceCrawlHandoffAttribution = "ambiguous"
	sourceCrawlHandoffNotAttributable sourceCrawlHandoffAttribution = "not_attributable"
)

const (
	// A heartbeat may extend a lease by at most one hour at a time and never
	// beyond 24 hours after the attempt was originally claimed.
	sourceCrawlHandoffMaxLeaseExtension = time.Hour
	sourceCrawlHandoffMaxLifetime       = 24 * time.Hour
)

var (
	errSourceCrawlTerminalConflict    = errors.New("source crawl terminal receipt conflicts with stored result")
	errSourceCrawlTerminalUnsupported = errors.New("source crawl terminal receipt is unsupported")
)

// sourceCrawlCrawlerHandoff is the trusted identity a future crawler worker
// would receive from ClaimSourceCrawlAttempts. It is not execution authority:
// the database must recheck the current claim, lease and policy before any
// terminal mutation.
type sourceCrawlCrawlerHandoff struct {
	Attempt     sourceCrawlAttempt
	Attribution sourceCrawlHandoffAttribution
}

// newSourceCrawlCrawlerHandoff converts one claim result into the handoff shape.
// Stale, conflicting or legacy outcomes are rejected. An unbound outcome may
// never carry an operation binding.
func newSourceCrawlCrawlerHandoff(claim SourceCrawlClaim) (sourceCrawlCrawlerHandoff, error) {
	attempt := sourceCrawlAttempt{
		AttemptID:      claim.AttemptID,
		ClaimRequestID: claim.ClaimRequestID,
		SourceID:       claim.SourceID,
		Generation:     claim.Generation,
		EngineID:       claim.EngineID,
		State:          sourceCrawlAttemptClaimed,
		ClaimedAt:      claim.ClaimedAt.UTC().Truncate(time.Microsecond),
		LeaseExpiresAt: claim.LeaseExpiresAt.UTC().Truncate(time.Microsecond),
	}
	var attribution sourceCrawlHandoffAttribution
	switch claim.Outcome {
	case SourceCrawlClaimed:
		attribution = sourceCrawlHandoffBound
		binding := &sourceCrawlOperationBinding{
			OperationID:     claim.OperationID,
			RequestID:       claim.RequestID,
			RunID:           claim.RunID,
			AgentID:         claim.AgentID,
			Owner:           claim.Owner,
			PrincipalUserID: claim.PrincipalUserID,
			ApprovalID:      claim.ApprovalID,
			ApprovalSHA256:  claim.ApprovalSHA256,
		}
		attempt.Binding = binding
	case SourceCrawlUnattributed:
		attribution = sourceCrawlHandoffUnattributed
	case SourceCrawlAmbiguous:
		attribution = sourceCrawlHandoffAmbiguous
	case SourceCrawlNotAttributable:
		attribution = sourceCrawlHandoffNotAttributable
	default:
		return sourceCrawlCrawlerHandoff{}, fmt.Errorf("%w: claim outcome %q is not executable",
			errSourceCrawlAttemptStale, claim.Outcome)
	}
	if claim.Outcome != SourceCrawlClaimed &&
		(claim.OperationID != "" || claim.RequestID != "" || claim.ApprovalID != "" ||
			claim.PrincipalUserID != 0) {
		return sourceCrawlCrawlerHandoff{}, fmt.Errorf("%w: unbound claim carries operation fields",
			errSourceCrawlAttemptInvalid)
	}
	handoff := sourceCrawlCrawlerHandoff{Attempt: attempt, Attribution: attribution}
	if err := handoff.Attempt.validate(); err != nil {
		return sourceCrawlCrawlerHandoff{}, err
	}
	return handoff, nil
}

// validate checks that the handoff is a currently executable identity at the
// supplied trusted time. It does not prove current database ownership or that
// the operator policy is still fenced.
func (h sourceCrawlCrawlerHandoff) validate(now time.Time) error {
	if !validSourceCrawlAttemptTime(now) {
		return fmt.Errorf("%w: trusted handoff clock", errSourceCrawlAttemptInvalid)
	}
	if err := h.Attempt.validate(); err != nil {
		return err
	}
	if h.Attempt.State != sourceCrawlAttemptClaimed {
		return errSourceCrawlAttemptStale
	}
	if now.Before(h.Attempt.ClaimedAt) {
		return fmt.Errorf("%w: trusted clock precedes claim", errSourceCrawlAttemptInvalid)
	}
	if !now.Before(h.Attempt.LeaseExpiresAt) {
		return fmt.Errorf("%w: attempt lease expired", errSourceCrawlAttemptStale)
	}
	switch h.Attribution {
	case sourceCrawlHandoffBound:
		if h.Attempt.Binding == nil {
			return fmt.Errorf("%w: bound handoff lacks an operation", errSourceCrawlAttemptInvalid)
		}
		if CrawlOperationKey(h.Attempt.Binding.Owner, h.Attempt.Binding.AgentID,
			h.Attempt.SourceID, string(CrawlOperationType),
			h.Attempt.Binding.RequestID) != h.Attempt.Binding.OperationID {
			return fmt.Errorf("%w: bound handoff operation identity", errSourceCrawlAttemptInvalid)
		}
	case sourceCrawlHandoffUnattributed, sourceCrawlHandoffAmbiguous,
		sourceCrawlHandoffNotAttributable:
		if h.Attempt.Binding != nil {
			return fmt.Errorf("%w: unbound handoff carries operation authority",
				errSourceCrawlAttemptInvalid)
		}
	default:
		return fmt.Errorf("%w: handoff attribution", errSourceCrawlAttemptInvalid)
	}
	return nil
}

// extendSourceCrawlHandoffLease returns a bounded extension. The caller still
// has to apply it through a future database transaction that verifies the
// attempt is current. The original claim time is immutable.
func extendSourceCrawlHandoffLease(h sourceCrawlCrawlerHandoff, now time.Time,
	extension time.Duration) (sourceCrawlCrawlerHandoff, error) {
	if extension <= 0 || extension > sourceCrawlHandoffMaxLeaseExtension {
		return sourceCrawlCrawlerHandoff{}, fmt.Errorf("%w: lease extension",
			errSourceCrawlAttemptInvalid)
	}
	if err := h.validate(now); err != nil {
		return sourceCrawlCrawlerHandoff{}, err
	}
	maximum := h.Attempt.ClaimedAt.Add(sourceCrawlHandoffMaxLifetime)
	next := now.Add(extension)
	if next.After(maximum) {
		next = maximum
	}
	if !next.After(h.Attempt.LeaseExpiresAt) {
		return h, nil
	}
	h.Attempt.LeaseExpiresAt = next
	return h, nil
}

type sourceCrawlTerminalOutcome string

const (
	sourceCrawlTerminalCompletedOutcome sourceCrawlTerminalOutcome = "completed"
	sourceCrawlTerminalFailedOutcome    sourceCrawlTerminalOutcome = "failed"
	sourceCrawlTerminalCancelledOutcome sourceCrawlTerminalOutcome = "cancelled"
)

// The initial terminal reason vocabulary is deliberately closed. A future
// implementation may extend it only with an explicit reviewed contract.
var sourceCrawlTerminalReasonCodes = map[sourceCrawlTerminalOutcome]string{
	sourceCrawlTerminalCompletedOutcome: "crawl_completed",
	sourceCrawlTerminalFailedOutcome:    "crawl_failed",
	sourceCrawlTerminalCancelledOutcome: "crawl_cancelled",
}

type sourceCrawlTerminalOperationResult struct {
	Binding       sourceCrawlOperationBinding
	ExpectedState CrawlOperationState
	ResultState   CrawlOperationState
}

// sourceCrawlTerminalReceipt is the future crawler-to-database terminal report.
// For a bound attempt the operation result must repeat the immutable binding
// exactly. For an unbound attempt it must be absent.
type sourceCrawlTerminalReceipt struct {
	Attempt         sourceCrawlAttempt
	Attribution     sourceCrawlHandoffAttribution
	Outcome         sourceCrawlTerminalOutcome
	ReasonCode      string
	ObservedAt      time.Time
	OperationResult *sourceCrawlTerminalOperationResult
}

func sourceCrawlTerminalOperationEligible(state CrawlOperationState) bool {
	return state == CrawlOperationAccepted || state == CrawlOperationProcessing
}

func sourceCrawlTerminalOperationTarget(outcome sourceCrawlTerminalOutcome) (CrawlOperationState, bool) {
	switch outcome {
	case sourceCrawlTerminalCompletedOutcome:
		return CrawlOperationCompleted, true
	case sourceCrawlTerminalFailedOutcome:
		return CrawlOperationFailed, true
	case sourceCrawlTerminalCancelledOutcome:
		return CrawlOperationCancelled, true
	default:
		return "", false
	}
}

func sourceCrawlAttemptTerminalTransitionAllowed(current sourceCrawlAttemptState,
	outcome sourceCrawlTerminalOutcome) bool {
	if current != sourceCrawlAttemptClaimed {
		return false
	}
	_, ok := sourceCrawlTerminalOperationTarget(outcome)
	return ok
}

func sourceCrawlTerminalOperationTransitionAllowed(expected, result CrawlOperationState) bool {
	if !sourceCrawlTerminalOperationEligible(expected) {
		return false
	}
	switch result {
	case CrawlOperationCompleted, CrawlOperationFailed, CrawlOperationCancelled:
		return true
	default:
		return false
	}
}

func (r sourceCrawlTerminalReceipt) validate(now time.Time) error {
	if !validSourceCrawlAttemptTime(now) || !validSourceCrawlAttemptTime(r.ObservedAt) {
		return fmt.Errorf("%w: terminal receipt timestamps", errSourceCrawlAttemptInvalid)
	}
	if r.ObservedAt.Before(r.Attempt.ClaimedAt) || r.ObservedAt.After(now) {
		return fmt.Errorf("%w: terminal observation time", errSourceCrawlAttemptInvalid)
	}
	if err := (sourceCrawlCrawlerHandoff{Attempt: r.Attempt, Attribution: r.Attribution}).validate(now); err != nil {
		return err
	}
	if !r.ObservedAt.Before(r.Attempt.LeaseExpiresAt) {
		return fmt.Errorf("%w: terminal receipt after lease expiry", errSourceCrawlAttemptStale)
	}
	expectedReason, ok := sourceCrawlTerminalReasonCodes[r.Outcome]
	if !ok || r.ReasonCode != expectedReason {
		return fmt.Errorf("%w: terminal outcome or reason code", errSourceCrawlAttemptInvalid)
	}
	switch r.Attribution {
	case sourceCrawlHandoffBound:
		if r.OperationResult == nil {
			return fmt.Errorf("%w: bound terminal receipt lacks an operation result",
				errSourceCrawlAttemptInvalid)
		}
		if err := r.OperationResult.Binding.validate(); err != nil {
			return err
		}
		if r.OperationResult.Binding != *r.Attempt.Binding {
			return fmt.Errorf("%w: terminal operation binding differs from the attempt",
				errSourceCrawlAttemptInvalid)
		}
		if !sourceCrawlTerminalOperationTransitionAllowed(r.OperationResult.ExpectedState,
			r.OperationResult.ResultState) {
			return fmt.Errorf("%w: terminal operation state transition",
				errSourceCrawlAttemptInvalid)
		}
		if target, ok := sourceCrawlTerminalOperationTarget(r.Outcome); !ok ||
			target != r.OperationResult.ResultState {
			return fmt.Errorf("%w: terminal outcome and operation result disagree",
				errSourceCrawlAttemptInvalid)
		}
	case sourceCrawlHandoffUnattributed, sourceCrawlHandoffAmbiguous,
		sourceCrawlHandoffNotAttributable:
		if r.OperationResult != nil {
			return fmt.Errorf("%w: unbound terminal receipt claims an operation result",
				errSourceCrawlAttemptInvalid)
		}
	default:
		return fmt.Errorf("%w: terminal attribution", errSourceCrawlAttemptInvalid)
	}
	return nil
}

// sourceCrawlTerminalAuthority is the trusted current-state snapshot a future
// transaction must establish under its locks. It is deliberately explicit so
// ownership, disablement, revocation and fence decisions remain testable and
// are never inferred from model text or a source-level event.
type sourceCrawlTerminalAuthority struct {
	Fenced                          bool
	CurrentAttemptID                string
	CurrentGeneration               uint64
	CurrentClaimRequestID           string
	CurrentEngineID                 string
	AttemptState                    sourceCrawlAttemptState
	SourceDeleted                   bool
	SourceDisabled                  bool
	CurrentSourceOwnerUserID        uint64
	ApprovalRevokedBeforeAcceptance bool
}

func (a sourceCrawlTerminalAuthority) validate(receipt sourceCrawlTerminalReceipt,
	now time.Time) error {
	if !a.Fenced {
		return errSourceCrawlTerminalUnsupported
	}
	if a.SourceDeleted {
		return fmt.Errorf("%w: source is deleted", errSourceCrawlAttemptStale)
	}
	if a.AttemptState != sourceCrawlAttemptClaimed {
		return fmt.Errorf("%w: attempt is not claimed", errSourceCrawlAttemptStale)
	}
	if a.CurrentAttemptID != receipt.Attempt.AttemptID ||
		a.CurrentGeneration != receipt.Attempt.Generation ||
		a.CurrentClaimRequestID != receipt.Attempt.ClaimRequestID ||
		a.CurrentEngineID != receipt.Attempt.EngineID {
		return fmt.Errorf("%w: attempt is not the current claim", errSourceCrawlAttemptStale)
	}
	if a.ApprovalRevokedBeforeAcceptance {
		return errSourceCrawlTerminalConflict
	}
	return receipt.validate(now)
}

// sourceCrawlTerminalSourceStatusEligible reports whether a terminal receipt
// may also update the source-level status. A disabled or deleted source, a
// changed owner for a bound operation, or a lost fence preserves the existing
// source row; the operation-specific result is still recorded separately.
func (a sourceCrawlTerminalAuthority) sourceCrawlTerminalSourceStatusEligible(
	receipt sourceCrawlTerminalReceipt) bool {
	if !a.Fenced || a.SourceDeleted || a.SourceDisabled ||
		a.AttemptState != sourceCrawlAttemptClaimed {
		return false
	}
	if a.CurrentAttemptID != receipt.Attempt.AttemptID ||
		a.CurrentGeneration != receipt.Attempt.Generation ||
		a.CurrentClaimRequestID != receipt.Attempt.ClaimRequestID ||
		a.CurrentEngineID != receipt.Attempt.EngineID {
		return false
	}
	if receipt.Attribution == sourceCrawlHandoffBound && receipt.Attempt.Binding != nil &&
		a.CurrentSourceOwnerUserID != receipt.Attempt.Binding.PrincipalUserID {
		return false
	}
	return true
}

type sourceCrawlTerminalReportResult string

const (
	sourceCrawlTerminalApplied          sourceCrawlTerminalReportResult = "applied"
	sourceCrawlTerminalIdempotentReplay sourceCrawlTerminalReportResult = "idempotent_replay"
	sourceCrawlTerminalStaleFence       sourceCrawlTerminalReportResult = "stale_fence"
	sourceCrawlTerminalConflict         sourceCrawlTerminalReportResult = "conflict"
	sourceCrawlTerminalUnavailable      sourceCrawlTerminalReportResult = "unavailable"
	sourceCrawlTerminalUnsupported      sourceCrawlTerminalReportResult = "unsupported"
)

// classifySourceCrawlTerminalReplay implements the closed replay decision for
// a durable terminal receipt. A retry with the same attempt, outcome, reason
// and operation result is idempotent; any identity or result change conflicts.
func classifySourceCrawlTerminalReplay(stored *sourceCrawlTerminalReceipt,
	incoming sourceCrawlTerminalReceipt) sourceCrawlTerminalReportResult {
	if stored == nil {
		return sourceCrawlTerminalConflict
	}
	if stored.Attempt.AttemptID != incoming.Attempt.AttemptID ||
		stored.Attempt.Generation != incoming.Attempt.Generation ||
		stored.Attempt.SourceID != incoming.Attempt.SourceID ||
		stored.Attribution != incoming.Attribution ||
		stored.Outcome != incoming.Outcome ||
		stored.ReasonCode != incoming.ReasonCode {
		return sourceCrawlTerminalConflict
	}
	if (stored.OperationResult == nil) != (incoming.OperationResult == nil) {
		return sourceCrawlTerminalConflict
	}
	if stored.OperationResult != nil {
		if stored.OperationResult.Binding != incoming.OperationResult.Binding ||
			stored.OperationResult.ExpectedState != incoming.OperationResult.ExpectedState ||
			stored.OperationResult.ResultState != incoming.OperationResult.ResultState {
			return sourceCrawlTerminalConflict
		}
	}
	return sourceCrawlTerminalIdempotentReplay
}
