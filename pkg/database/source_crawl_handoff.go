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
		(claim.OperationID != "" || claim.RequestID != "" || claim.RunID != "" ||
			claim.AgentID != "" || claim.Owner != "" || claim.PrincipalUserID != 0 ||
			claim.ApprovalID != "" || claim.ApprovalSHA256 != "") {
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

// sourceCrawlTerminalOperationTransitionAllowed validates an optional worker
// precondition against the result. An empty expected state means "the
// authoritative operation row decides": schema 1.17 permits a terminal CAS
// from either accepted or processing, and the database repository already
// reads that row under its own locks. A worker-supplied state is never
// authority; when present it must match the state the database observed.
func sourceCrawlTerminalOperationTransitionAllowed(expected, result CrawlOperationState) bool {
	if expected != "" && !sourceCrawlTerminalOperationEligible(expected) {
		return false
	}
	switch result {
	case CrawlOperationCompleted, CrawlOperationFailed, CrawlOperationCancelled:
		return true
	default:
		return false
	}
}

// validateStructure checks the receipt shape without evaluating current lease
// eligibility. It is used by first application with live time and by the
// read-only replay path after expiry.
func (r sourceCrawlTerminalReceipt) validateStructure() error {
	if !validSourceCrawlAttemptTime(r.ObservedAt) {
		return fmt.Errorf("%w: terminal observation time", errSourceCrawlAttemptInvalid)
	}
	if err := r.Attempt.validate(); err != nil {
		return err
	}
	if r.Attempt.State != sourceCrawlAttemptClaimed ||
		!r.Attempt.TerminalAt.IsZero() || r.Attempt.SupersededBy != 0 {
		return fmt.Errorf("%w: terminal receipt attempt is not a live claim shape",
			errSourceCrawlAttemptInvalid)
	}
	if r.ObservedAt.Before(r.Attempt.ClaimedAt) {
		return fmt.Errorf("%w: terminal observation precedes claim", errSourceCrawlAttemptInvalid)
	}
	if !r.ObservedAt.Before(r.Attempt.LeaseExpiresAt) {
		// Stale, not malformed: the value is plausible but outside the
		// original attempt's execution window. The read-only replay classifier
		// maps this to a conflict.
		return fmt.Errorf("%w: terminal observation at or after the original lease expiry",
			errSourceCrawlAttemptStale)
	}
	expectedReason, ok := sourceCrawlTerminalReasonCodes[r.Outcome]
	if !ok || r.ReasonCode != expectedReason {
		return fmt.Errorf("%w: terminal outcome or reason code", errSourceCrawlAttemptInvalid)
	}
	switch r.Attribution {
	case sourceCrawlHandoffBound:
		if r.Attempt.Binding == nil || r.OperationResult == nil {
			return fmt.Errorf("%w: bound terminal receipt lacks an operation result",
				errSourceCrawlAttemptInvalid)
		}
		if err := r.Attempt.Binding.validate(); err != nil {
			return err
		}
		if CrawlOperationKey(r.Attempt.Binding.Owner, r.Attempt.Binding.AgentID,
			r.Attempt.SourceID, string(CrawlOperationType),
			r.Attempt.Binding.RequestID) != r.Attempt.Binding.OperationID {
			return fmt.Errorf("%w: terminal operation identity", errSourceCrawlAttemptInvalid)
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
		if r.Attempt.Binding != nil || r.OperationResult != nil {
			return fmt.Errorf("%w: unbound terminal receipt claims operation identity",
				errSourceCrawlAttemptInvalid)
		}
	default:
		return fmt.Errorf("%w: terminal attribution", errSourceCrawlAttemptInvalid)
	}
	return nil
}

func (r sourceCrawlTerminalReceipt) validate(now time.Time) error {
	if !validSourceCrawlAttemptTime(now) {
		return fmt.Errorf("%w: terminal receipt clock", errSourceCrawlAttemptInvalid)
	}
	if err := r.validateStructure(); err != nil {
		return err
	}
	if r.ObservedAt.After(now) {
		return fmt.Errorf("%w: terminal observation is in the future", errSourceCrawlAttemptInvalid)
	}
	if !now.Before(r.Attempt.LeaseExpiresAt) {
		return fmt.Errorf("%w: terminal receipt after lease expiry", errSourceCrawlAttemptStale)
	}
	return nil
}

type sourceCrawlTerminalAuthorityState string

const (
	sourceCrawlTerminalAuthorityCurrentLive    sourceCrawlTerminalAuthorityState = "current_live"
	sourceCrawlTerminalAuthorityCurrentExpired sourceCrawlTerminalAuthorityState = "current_expired"
	sourceCrawlTerminalAuthoritySuperseded     sourceCrawlTerminalAuthorityState = "superseded"
	sourceCrawlTerminalAuthorityTerminal       sourceCrawlTerminalAuthorityState = "terminal"
	sourceCrawlTerminalAuthorityMissing        sourceCrawlTerminalAuthorityState = "missing"
	sourceCrawlTerminalAuthorityCorrupt        sourceCrawlTerminalAuthorityState = "corrupt"
	sourceCrawlTerminalAuthorityUnavailable    sourceCrawlTerminalAuthorityState = "unavailable"
	sourceCrawlTerminalAuthorityLegacyMode     sourceCrawlTerminalAuthorityState = "legacy_mode"
)

// sourceCrawlTerminalAuthority is the trusted current-state snapshot a future
// transaction must establish under its locks. It joins the current claim row
// and its exact history row. A worker receipt never supplies these values; the
// database reads them under a transaction and evaluates fresh database time
// after the relevant locks are held.
type sourceCrawlTerminalAuthority struct {
	Available bool
	Fenced    bool
	Found     bool

	SourceID       uint64
	AttemptID      string
	Generation     uint64
	ClaimRequestID string
	EngineID       string
	ClaimedAt      time.Time

	HistorySourceID       uint64
	HistoryAttemptID      string
	HistoryGeneration     uint64
	HistoryClaimRequestID string
	HistoryEngineID       string
	HistoryClaimedAt      time.Time

	CurrentLeaseExpiresAt time.Time
	HistoryLeaseExpiresAt time.Time
	AttemptState          sourceCrawlAttemptState
	Binding               *sourceCrawlOperationBinding

	SourceDeleted                     bool
	SourceDisabled                    bool
	SourceOwnershipVerified           bool
	SourceOwnershipContinuityVerified bool
	CurrentSourceOwnerUserID          uint64
	ApprovalHistory                   sourceCrawlApprovalHistoryState
}

var (
	errSourceCrawlTerminalUnavailable = errors.New("source crawl terminal authority is unavailable")
	errSourceCrawlTerminalCorrupt     = errors.New("source crawl terminal authority is corrupt")
)

type sourceCrawlTerminalReportResult string

const (
	sourceCrawlTerminalApplied          sourceCrawlTerminalReportResult = "applied"
	sourceCrawlTerminalIdempotentReplay sourceCrawlTerminalReportResult = "idempotent_replay"
	sourceCrawlTerminalStaleFence       sourceCrawlTerminalReportResult = "stale_fence"
	sourceCrawlTerminalConflict         sourceCrawlTerminalReportResult = "conflict"
	sourceCrawlTerminalUnavailable      sourceCrawlTerminalReportResult = "unavailable"
	sourceCrawlTerminalUnsupported      sourceCrawlTerminalReportResult = "unsupported"
)

func (a sourceCrawlTerminalAuthority) validateSnapshot() error {
	if !a.Found {
		return fmt.Errorf("%w: authority is missing", errSourceCrawlTerminalCorrupt)
	}
	if a.SourceID == 0 {
		return fmt.Errorf("%w: source_id", errSourceCrawlTerminalCorrupt)
	}
	if !sourceCrawlAttemptIDPattern.MatchString(a.AttemptID) ||
		a.Generation == 0 || !sourceCrawlHashPattern.MatchString(a.ClaimRequestID) {
		return fmt.Errorf("%w: attempt identity", errSourceCrawlTerminalCorrupt)
	}
	if strings.TrimSpace(a.EngineID) == "" || len(a.EngineID) > 256 {
		return fmt.Errorf("%w: engine", errSourceCrawlTerminalCorrupt)
	}
	if a.HistorySourceID != a.SourceID || a.HistoryAttemptID != a.AttemptID ||
		a.HistoryGeneration != a.Generation || a.HistoryClaimRequestID != a.ClaimRequestID ||
		a.HistoryEngineID != a.EngineID || !a.HistoryClaimedAt.Equal(a.ClaimedAt) {
		return fmt.Errorf("%w: current and history identity disagree", errSourceCrawlTerminalCorrupt)
	}
	if !sourceCrawlAttemptIDPattern.MatchString(a.HistoryAttemptID) ||
		a.HistoryGeneration == 0 || !sourceCrawlHashPattern.MatchString(a.HistoryClaimRequestID) ||
		strings.TrimSpace(a.HistoryEngineID) == "" || len(a.HistoryEngineID) > 256 ||
		!validSourceCrawlAttemptTime(a.HistoryClaimedAt) {
		return fmt.Errorf("%w: history identity", errSourceCrawlTerminalCorrupt)
	}
	if !a.AttemptState.valid() || !a.ApprovalHistory.valid() {
		return fmt.Errorf("%w: attempt or approval state", errSourceCrawlTerminalCorrupt)
	}
	if !validSourceCrawlAttemptTime(a.ClaimedAt) ||
		!validSourceCrawlAttemptTime(a.CurrentLeaseExpiresAt) ||
		!validSourceCrawlAttemptTime(a.HistoryLeaseExpiresAt) ||
		!a.HistoryLeaseExpiresAt.After(a.ClaimedAt) ||
		!a.CurrentLeaseExpiresAt.Equal(a.HistoryLeaseExpiresAt) {
		return fmt.Errorf("%w: claim and history lease", errSourceCrawlTerminalCorrupt)
	}
	if a.Binding != nil {
		if err := a.Binding.validate(); err != nil {
			return fmt.Errorf("%w: binding", errSourceCrawlTerminalCorrupt)
		}
		if CrawlOperationKey(a.Binding.Owner, a.Binding.AgentID, a.SourceID,
			string(CrawlOperationType), a.Binding.RequestID) != a.Binding.OperationID {
			return fmt.Errorf("%w: deterministic operation identity", errSourceCrawlTerminalCorrupt)
		}
	}
	return nil
}

// classify is the closed authority result model. The caller must pass fresh
// database time obtained after its locks, not the worker's clock and not a
// transaction-start timestamp.
func (a sourceCrawlTerminalAuthority) classify(now time.Time) sourceCrawlTerminalAuthorityState {
	if !a.Available {
		return sourceCrawlTerminalAuthorityUnavailable
	}
	if !a.Fenced {
		return sourceCrawlTerminalAuthorityLegacyMode
	}
	if !a.Found {
		return sourceCrawlTerminalAuthorityMissing
	}
	if !validSourceCrawlAttemptTime(now) || a.validateSnapshot() != nil {
		return sourceCrawlTerminalAuthorityCorrupt
	}
	switch a.AttemptState {
	case sourceCrawlAttemptClaimed:
		if now.Before(a.HistoryLeaseExpiresAt) {
			return sourceCrawlTerminalAuthorityCurrentLive
		}
		return sourceCrawlTerminalAuthorityCurrentExpired
	case sourceCrawlAttemptSuperseded:
		return sourceCrawlTerminalAuthoritySuperseded
	case sourceCrawlAttemptExpired, sourceCrawlAttemptAbandoned,
		sourceCrawlAttemptCompleted, sourceCrawlAttemptFailed, sourceCrawlAttemptCancelled:
		return sourceCrawlTerminalAuthorityTerminal
	default:
		return sourceCrawlTerminalAuthorityCorrupt
	}
}

// validate is the pure preflight for a new terminal mutation. It compares every
// receipt identity and timestamp with the authoritative snapshot. It does not
// acquire locks; the future repository must classify again under its own
// transaction after re-reading these values.
func (a sourceCrawlTerminalAuthority) validate(receipt sourceCrawlTerminalReceipt,
	now time.Time) error {
	switch a.classify(now) {
	case sourceCrawlTerminalAuthorityCurrentLive:
	case sourceCrawlTerminalAuthorityUnavailable:
		return errSourceCrawlTerminalUnavailable
	case sourceCrawlTerminalAuthorityLegacyMode:
		return errSourceCrawlTerminalUnsupported
	case sourceCrawlTerminalAuthorityMissing:
		return fmt.Errorf("%w: current attempt is missing", errSourceCrawlAttemptStale)
	case sourceCrawlTerminalAuthorityCorrupt:
		return errSourceCrawlTerminalCorrupt
	case sourceCrawlTerminalAuthorityCurrentExpired:
		return fmt.Errorf("%w: attempt lease expired", errSourceCrawlAttemptStale)
	case sourceCrawlTerminalAuthoritySuperseded:
		return fmt.Errorf("%w: attempt is superseded", errSourceCrawlAttemptStale)
	case sourceCrawlTerminalAuthorityTerminal:
		return fmt.Errorf("%w: attempt is terminal", errSourceCrawlAttemptStale)
	default:
		return errSourceCrawlTerminalCorrupt
	}
	if a.SourceDeleted {
		return fmt.Errorf("%w: source is deleted", errSourceCrawlAttemptStale)
	}
	if receipt.Attempt.SourceID != a.SourceID ||
		receipt.Attempt.AttemptID != a.AttemptID ||
		receipt.Attempt.Generation != a.Generation ||
		receipt.Attempt.ClaimRequestID != a.ClaimRequestID ||
		receipt.Attempt.EngineID != a.EngineID {
		return fmt.Errorf("%w: receipt attempt is not the current claim", errSourceCrawlAttemptStale)
	}
	if !receipt.Attempt.ClaimedAt.Equal(a.ClaimedAt) ||
		!receipt.Attempt.LeaseExpiresAt.Equal(a.HistoryLeaseExpiresAt) {
		return fmt.Errorf("%w: receipt timestamps differ from the authoritative claim",
			errSourceCrawlTerminalConflict)
	}
	if (a.Binding == nil) != (receipt.Attempt.Binding == nil) {
		return fmt.Errorf("%w: receipt binding presence differs from the attempt",
			errSourceCrawlTerminalConflict)
	}
	if a.Binding != nil && *a.Binding != *receipt.Attempt.Binding {
		return fmt.Errorf("%w: receipt binding differs from the attempt",
			errSourceCrawlTerminalConflict)
	}
	if a.ApprovalHistory == sourceCrawlApprovalRevokedBeforeAcceptance {
		return errSourceCrawlTerminalConflict
	}
	return receipt.validate(now)
}

// sourceCrawlTerminalSourceStatusEligible reports whether a terminal receipt
// may also update the source-level status. A disabled or deleted source, a
// changed owner for a bound operation, or a lost fence preserves the existing
// source row; the operation-specific result may still be recorded separately.
func (a sourceCrawlTerminalAuthority) sourceCrawlTerminalSourceStatusEligible(
	receipt sourceCrawlTerminalReceipt, now time.Time) bool {
	if err := a.validate(receipt, now); err != nil {
		return false
	}
	if a.SourceDeleted || a.SourceDisabled || !a.SourceOwnershipVerified {
		return false
	}
	if receipt.Attempt.SourceID != a.SourceID ||
		receipt.Attempt.AttemptID != a.AttemptID ||
		receipt.Attempt.Generation != a.Generation ||
		receipt.Attempt.ClaimRequestID != a.ClaimRequestID ||
		receipt.Attempt.EngineID != a.EngineID ||
		!receipt.Attempt.ClaimedAt.Equal(a.ClaimedAt) ||
		!receipt.Attempt.LeaseExpiresAt.Equal(a.HistoryLeaseExpiresAt) {
		return false
	}
	if receipt.Attribution != sourceCrawlHandoffBound {
		// An unbound attempt has no recorded operation principal. Schema 1.17
		// can prove owner continuity only through the verified SourceOwnership
		// provenance row: it must still name the current Sources.usr_id owner
		// and must not have been written after the claim. Otherwise the update
		// is suppressed and the source is recovered through the bounded
		// processing-timeout/lease reclaim path.
		return a.SourceOwnershipVerified && a.SourceOwnershipContinuityVerified &&
			a.CurrentSourceOwnerUserID != 0
	}
	if receipt.Attempt.Binding == nil || a.Binding == nil ||
		*a.Binding != *receipt.Attempt.Binding || receipt.OperationResult == nil ||
		!sourceCrawlTerminalOperationTransitionAllowed(receipt.OperationResult.ExpectedState,
			receipt.OperationResult.ResultState) ||
		a.CurrentSourceOwnerUserID != receipt.Attempt.Binding.PrincipalUserID {
		return false
	}
	return true
}

type sourceCrawlApprovalHistoryState string

const (
	sourceCrawlApprovalValidAtAcceptance       sourceCrawlApprovalHistoryState = "valid_at_acceptance"
	sourceCrawlApprovalRevokedBeforeAcceptance sourceCrawlApprovalHistoryState = "revoked_before_acceptance"
	sourceCrawlApprovalRevokedAfterAcceptance  sourceCrawlApprovalHistoryState = "revoked_after_acceptance"
	sourceCrawlApprovalExpiredAfterAcceptance  sourceCrawlApprovalHistoryState = "expired_after_acceptance"
)

func (s sourceCrawlApprovalHistoryState) valid() bool {
	switch s {
	case sourceCrawlApprovalValidAtAcceptance, sourceCrawlApprovalRevokedBeforeAcceptance,
		sourceCrawlApprovalRevokedAfterAcceptance, sourceCrawlApprovalExpiredAfterAcceptance:
		return true
	default:
		return false
	}
}

type sourceCrawlPersistedAttribution string

const (
	sourceCrawlPersistedBound   sourceCrawlPersistedAttribution = "bound"
	sourceCrawlPersistedUnbound sourceCrawlPersistedAttribution = "unbound"
)

// sourceCrawlPersistedTerminal is the subset of a committed terminal result
// that existing schema 1.17 can reconstruct. ExpectedState and the original
// ambiguous/not_attributable distinction are not persisted; unbound history is
// normalized to the single "unbound" class.
type sourceCrawlPersistedTerminal struct {
	Attempt         sourceCrawlAttempt
	Attribution     sourceCrawlPersistedAttribution
	Outcome         sourceCrawlTerminalOutcome
	OperationResult CrawlOperationState
}

func sourceCrawlTerminalOutcomeForAttemptState(state sourceCrawlAttemptState) (sourceCrawlTerminalOutcome, bool) {
	switch state {
	case sourceCrawlAttemptCompleted:
		return sourceCrawlTerminalCompletedOutcome, true
	case sourceCrawlAttemptFailed:
		return sourceCrawlTerminalFailedOutcome, true
	case sourceCrawlAttemptCancelled:
		return sourceCrawlTerminalCancelledOutcome, true
	default:
		return "", false
	}
}

func newSourceCrawlPersistedTerminal(attempt sourceCrawlAttempt,
	operationResult CrawlOperationState) (sourceCrawlPersistedTerminal, error) {
	if err := attempt.validate(); err != nil {
		return sourceCrawlPersistedTerminal{}, err
	}
	outcome, ok := sourceCrawlTerminalOutcomeForAttemptState(attempt.State)
	if !ok || !validSourceCrawlAttemptTime(attempt.TerminalAt) ||
		attempt.ReasonCode != sourceCrawlTerminalReasonCodes[outcome] {
		return sourceCrawlPersistedTerminal{}, fmt.Errorf("%w: persisted terminal outcome",
			errSourceCrawlTerminalCorrupt)
	}
	persisted := sourceCrawlPersistedTerminal{Attempt: attempt, Outcome: outcome}
	if attempt.Binding == nil {
		if operationResult != "" {
			return sourceCrawlPersistedTerminal{}, fmt.Errorf("%w: unbound terminal result",
				errSourceCrawlTerminalCorrupt)
		}
		persisted.Attribution = sourceCrawlPersistedUnbound
		return persisted, nil
	}
	if err := attempt.Binding.validate(); err != nil {
		return sourceCrawlPersistedTerminal{}, err
	}
	if CrawlOperationKey(attempt.Binding.Owner, attempt.Binding.AgentID, attempt.SourceID,
		string(CrawlOperationType), attempt.Binding.RequestID) != attempt.Binding.OperationID {
		return sourceCrawlPersistedTerminal{}, fmt.Errorf("%w: persisted operation identity",
			errSourceCrawlTerminalCorrupt)
	}
	target, ok := sourceCrawlTerminalOperationTarget(outcome)
	if !ok || operationResult != target {
		return sourceCrawlPersistedTerminal{}, fmt.Errorf("%w: persisted operation result",
			errSourceCrawlTerminalCorrupt)
	}
	persisted.Attribution = sourceCrawlPersistedBound
	persisted.OperationResult = operationResult
	return persisted, nil
}

// classifySourceCrawlPersistedTerminalReplay compares a retry with the subset of
// terminal identity that schema 1.17 actually persisted. It is a read-only
// classification: it does not check the current lease, renew it, or mutate the
// attempt. It therefore remains usable after lease expiry or supersession.
//
// ExpectedState is a first-write precondition and is deliberately not part of
// replay identity. ObservedAt is not authoritative; the stored TerminalAt is.
func classifySourceCrawlPersistedTerminalReplay(stored *sourceCrawlPersistedTerminal,
	incoming sourceCrawlTerminalReceipt) sourceCrawlTerminalReportResult {
	if stored == nil {
		return sourceCrawlTerminalConflict
	}
	if err := incoming.validateStructure(); err != nil {
		return sourceCrawlTerminalConflict
	}
	reconstructed, err := newSourceCrawlPersistedTerminal(stored.Attempt, stored.OperationResult)
	if err != nil || reconstructed.Attribution != stored.Attribution ||
		reconstructed.Outcome != stored.Outcome {
		return sourceCrawlTerminalConflict
	}
	if stored.Attempt.AttemptID != incoming.Attempt.AttemptID ||
		stored.Attempt.ClaimRequestID != incoming.Attempt.ClaimRequestID ||
		stored.Attempt.SourceID != incoming.Attempt.SourceID ||
		stored.Attempt.Generation != incoming.Attempt.Generation ||
		stored.Attempt.EngineID != incoming.Attempt.EngineID ||
		!stored.Attempt.ClaimedAt.Equal(incoming.Attempt.ClaimedAt) ||
		!stored.Attempt.LeaseExpiresAt.Equal(incoming.Attempt.LeaseExpiresAt) ||
		stored.Outcome != incoming.Outcome ||
		stored.Attempt.ReasonCode != incoming.ReasonCode {
		return sourceCrawlTerminalConflict
	}
	switch stored.Attribution {
	case sourceCrawlPersistedUnbound:
		if incoming.Attempt.Binding != nil || incoming.OperationResult != nil ||
			(incoming.Attribution != sourceCrawlHandoffUnattributed &&
				incoming.Attribution != sourceCrawlHandoffAmbiguous &&
				incoming.Attribution != sourceCrawlHandoffNotAttributable) {
			return sourceCrawlTerminalConflict
		}
	case sourceCrawlPersistedBound:
		if incoming.Attribution != sourceCrawlHandoffBound ||
			incoming.Attempt.Binding == nil || incoming.OperationResult == nil ||
			!sourceCrawlTerminalOperationTransitionAllowed(incoming.OperationResult.ExpectedState,
				incoming.OperationResult.ResultState) ||
			stored.Attempt.Binding == nil ||
			*stored.Attempt.Binding != *incoming.Attempt.Binding ||
			*stored.Attempt.Binding != incoming.OperationResult.Binding ||
			stored.OperationResult != incoming.OperationResult.ResultState {
			return sourceCrawlTerminalConflict
		}
	default:
		return sourceCrawlTerminalConflict
	}
	return sourceCrawlTerminalIdempotentReplay
}
