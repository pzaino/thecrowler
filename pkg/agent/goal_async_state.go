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

// Internal, side-effect-free asynchronous goal lifecycle and checkpoint
// foundation. This file deliberately has no database, network, crawler, model,
// MCP or scheduler dependency. It models ownership and progress only; a future
// durable store and resume engine must be separately approved.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	goalAsyncCheckpointVersion = 2
	goalAsyncContractVersion   = "goal-async-v2"

	maxGoalAsyncCheckpointBytes = 32 << 10
	maxGoalAsyncJSONDepth       = 64
	maxGoalAsyncWorkRefs        = 16
	maxGoalAsyncEvidenceRefs    = 32
	maxGoalAsyncAuditRefs       = 32

	defaultGoalAsyncMaxSlices                = 16
	hardGoalAsyncMaxSlices                   = 64
	defaultGoalAsyncMaxRetries               = 4
	hardGoalAsyncMaxRetries                  = 16
	defaultGoalAsyncMaxScheduledWorkRequests = 8
	hardGoalAsyncMaxScheduledWorkRequests    = 32

	// The logical goal lifetime is deliberately separate from the synchronous
	// model/tool slice ceiling. The provisional upper bound is internal only;
	// deployed/public TTL policy requires a separate maintainer decision.
	defaultGoalAsyncLogicalLifetime = 15 * time.Minute
	hardGoalAsyncLogicalLifetime    = 24 * time.Hour
)

const (
	goalAsyncReasonCreated               = "goal_async_created"
	goalAsyncReasonStarted               = "goal_async_started"
	goalAsyncReasonAwaitingApproval      = "goal_async_awaiting_approval"
	goalAsyncReasonAwaitingExternalWork  = "goal_async_awaiting_external_work"
	goalAsyncReasonResumed               = "goal_async_resumed"
	goalAsyncReasonIncomplete            = "goal_async_incomplete"
	goalAsyncReasonBudgetExhausted       = "goal_async_budget_exhausted"
	goalAsyncReasonCancelled             = "goal_async_cancelled"
	goalAsyncReasonDenied                = "goal_async_denied"
	goalAsyncReasonFailed                = "goal_async_failed"
	goalAsyncReasonDeadlineExceeded      = "deadline_exceeded"
	goalAsyncReasonDeterministicVerifier = "deterministic_verifier"
	goalAsyncReasonCriteriaAllMet        = "criteria_all_met"
	goalAsyncReasonCriteriaAnyMet        = "criteria_any_met"
)

var (
	goalAsyncIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	goalAsyncOwnerPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,255}$`)
	goalAsyncIssuerPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,511}$`)
	goalAsyncProducerPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,255}$`)
	goalAsyncHashPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	goalAsyncEvidenceIDPattern = regexp.MustCompile(`^ev_[a-f0-9]{32}$`)
	goalAsyncReasonPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	goalAsyncOperationID       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

var errGoalAsyncJSONDuplicateKey = errors.New("duplicate JSON key")

var (
	// ErrGoalAsyncCheckpointInvalid means the internal checkpoint is malformed,
	// out of bounds or internally inconsistent.
	ErrGoalAsyncCheckpointInvalid = errors.New("goal async checkpoint is invalid")
	// ErrGoalAsyncTransitionInvalid means the requested transition is not in the
	// closed lifecycle table or its prerequisite is not authoritative.
	ErrGoalAsyncTransitionInvalid = errors.New("goal async transition is invalid")
	// ErrGoalAsyncRevisionConflict means optimistic concurrency did not match.
	ErrGoalAsyncRevisionConflict = errors.New("goal async checkpoint revision conflict")
	// ErrGoalAsyncReplayConflict means the same operation ID was reused with a
	// different request.
	ErrGoalAsyncReplayConflict = errors.New("goal async transition replay conflict")
	// ErrGoalAsyncBudget means a cumulative ceiling would be exceeded.
	ErrGoalAsyncBudget = errors.New("goal async budget exhausted")
	// ErrGoalAsyncDeadline means the fixed total-life deadline has elapsed.
	ErrGoalAsyncDeadline = errors.New("goal async deadline elapsed")
	// ErrGoalAsyncVerification means completion was not backed by the trusted
	// deterministic verifier.
	ErrGoalAsyncVerification = errors.New("goal async verification is not authoritative")
	// ErrGoalAsyncResumeDenied means the supplied current-authority snapshot
	// does not match the checkpoint's frozen identity or policy.
	ErrGoalAsyncResumeDenied = errors.New("goal async resume authorization denied")
	// ErrGoalAsyncSerialization means the checkpoint encoding was rejected.
	ErrGoalAsyncSerialization = errors.New("goal async checkpoint serialization failed")
)

// goalAsyncState is the internal goal lifecycle. It is deliberately distinct
// from GoalTerminalStatus, which describes one synchronous goal run result.
type goalAsyncState string

const (
	goalAsyncCreated              goalAsyncState = "created"
	goalAsyncRunning              goalAsyncState = "running"
	goalAsyncAwaitingApproval     goalAsyncState = "awaiting_approval"
	goalAsyncAwaitingExternalWork goalAsyncState = "awaiting_external_work"
	goalAsyncCriteriaMet          goalAsyncState = "criteria_met"
	goalAsyncIncomplete           goalAsyncState = "incomplete"
	goalAsyncBudgetExhausted      goalAsyncState = "budget_exhausted"
	goalAsyncCancelled            goalAsyncState = "cancelled"
	goalAsyncDenied               goalAsyncState = "denied"
	goalAsyncFailed               goalAsyncState = "failed"
)

func (s goalAsyncState) valid() bool {
	switch s {
	case goalAsyncCreated, goalAsyncRunning, goalAsyncAwaitingApproval,
		goalAsyncAwaitingExternalWork, goalAsyncCriteriaMet, goalAsyncIncomplete,
		goalAsyncBudgetExhausted, goalAsyncCancelled, goalAsyncDenied, goalAsyncFailed:
		return true
	default:
		return false
	}
}

func (s goalAsyncState) terminal() bool {
	switch s {
	case goalAsyncCriteriaMet, goalAsyncIncomplete, goalAsyncBudgetExhausted,
		goalAsyncCancelled, goalAsyncDenied, goalAsyncFailed:
		return true
	default:
		return false
	}
}

// goalAsyncTransitionKind is the closed transition vocabulary. The target
// state is derived from this value; callers cannot name an arbitrary target.
type goalAsyncTransitionKind string

const (
	goalAsyncTransitionStart             goalAsyncTransitionKind = "start"
	goalAsyncTransitionAwaitApproval     goalAsyncTransitionKind = "await_approval"
	goalAsyncTransitionAwaitExternalWork goalAsyncTransitionKind = "await_external_work"
	goalAsyncTransitionResume            goalAsyncTransitionKind = "resume"
	goalAsyncTransitionCriteriaMet       goalAsyncTransitionKind = "criteria_met"
	goalAsyncTransitionIncomplete        goalAsyncTransitionKind = "incomplete"
	goalAsyncTransitionBudgetExhausted   goalAsyncTransitionKind = "budget_exhausted"
	goalAsyncTransitionCancelled         goalAsyncTransitionKind = "cancelled"
	goalAsyncTransitionDenied            goalAsyncTransitionKind = "denied"
	goalAsyncTransitionFailed            goalAsyncTransitionKind = "failed"
)

func (k goalAsyncTransitionKind) valid() bool {
	switch k {
	case goalAsyncTransitionStart, goalAsyncTransitionAwaitApproval,
		goalAsyncTransitionAwaitExternalWork, goalAsyncTransitionResume,
		goalAsyncTransitionCriteriaMet, goalAsyncTransitionIncomplete,
		goalAsyncTransitionBudgetExhausted, goalAsyncTransitionCancelled,
		goalAsyncTransitionDenied, goalAsyncTransitionFailed:
		return true
	default:
		return false
	}
}

func (k goalAsyncTransitionKind) target() goalAsyncState {
	switch k {
	case goalAsyncTransitionStart, goalAsyncTransitionResume:
		return goalAsyncRunning
	case goalAsyncTransitionAwaitApproval:
		return goalAsyncAwaitingApproval
	case goalAsyncTransitionAwaitExternalWork:
		return goalAsyncAwaitingExternalWork
	case goalAsyncTransitionCriteriaMet:
		return goalAsyncCriteriaMet
	case goalAsyncTransitionIncomplete:
		return goalAsyncIncomplete
	case goalAsyncTransitionBudgetExhausted:
		return goalAsyncBudgetExhausted
	case goalAsyncTransitionCancelled:
		return goalAsyncCancelled
	case goalAsyncTransitionDenied:
		return goalAsyncDenied
	case goalAsyncTransitionFailed:
		return goalAsyncFailed
	default:
		return ""
	}
}

var goalAsyncTransitionTable = map[goalAsyncState]map[goalAsyncTransitionKind]goalAsyncState{
	goalAsyncCreated: {
		goalAsyncTransitionStart:           goalAsyncRunning,
		goalAsyncTransitionCancelled:       goalAsyncCancelled,
		goalAsyncTransitionDenied:          goalAsyncDenied,
		goalAsyncTransitionBudgetExhausted: goalAsyncBudgetExhausted,
		goalAsyncTransitionFailed:          goalAsyncFailed,
	},
	goalAsyncRunning: {
		goalAsyncTransitionAwaitApproval:     goalAsyncAwaitingApproval,
		goalAsyncTransitionAwaitExternalWork: goalAsyncAwaitingExternalWork,
		goalAsyncTransitionCriteriaMet:       goalAsyncCriteriaMet,
		goalAsyncTransitionIncomplete:        goalAsyncIncomplete,
		goalAsyncTransitionBudgetExhausted:   goalAsyncBudgetExhausted,
		goalAsyncTransitionCancelled:         goalAsyncCancelled,
		goalAsyncTransitionDenied:            goalAsyncDenied,
		goalAsyncTransitionFailed:            goalAsyncFailed,
	},
	goalAsyncAwaitingApproval: {
		goalAsyncTransitionAwaitExternalWork: goalAsyncAwaitingExternalWork,
		goalAsyncTransitionCancelled:         goalAsyncCancelled,
		goalAsyncTransitionDenied:            goalAsyncDenied,
		goalAsyncTransitionBudgetExhausted:   goalAsyncBudgetExhausted,
		goalAsyncTransitionFailed:            goalAsyncFailed,
	},
	goalAsyncAwaitingExternalWork: {
		goalAsyncTransitionResume:          goalAsyncRunning,
		goalAsyncTransitionCancelled:       goalAsyncCancelled,
		goalAsyncTransitionDenied:          goalAsyncDenied,
		goalAsyncTransitionBudgetExhausted: goalAsyncBudgetExhausted,
		goalAsyncTransitionFailed:          goalAsyncFailed,
	},
}

func goalAsyncTransitionAllowed(from goalAsyncState, kind goalAsyncTransitionKind) bool {
	targets, ok := goalAsyncTransitionTable[from]
	if !ok {
		return false
	}
	_, ok = targets[kind]
	return ok
}

// goalAsyncPrincipalKind distinguishes an authenticated operator from a
// principal-less system/event run. It is a reference, not a stored grant.
type goalAsyncPrincipalKind string

const (
	goalAsyncPrincipalUser   goalAsyncPrincipalKind = "user"
	goalAsyncPrincipalSystem goalAsyncPrincipalKind = "system"
)

type goalAsyncPrincipal struct {
	Kind     goalAsyncPrincipalKind
	UserID   uint64
	Username string
	Issuer   string
}

func (p goalAsyncPrincipal) validateForOwner(owner string) error {
	switch p.Kind {
	case goalAsyncPrincipalUser:
		if p.UserID == 0 {
			return goalAsyncInvalid("principal_user_id")
		}
		if !goalAsyncOwnerPattern.MatchString(strings.TrimSpace(p.Username)) {
			return goalAsyncInvalid("principal_username")
		}
		if !goalAsyncIssuerPattern.MatchString(strings.TrimSpace(p.Issuer)) {
			return goalAsyncInvalid("principal_issuer")
		}
		if strings.TrimSpace(p.Username) != owner {
			return goalAsyncInvalid("principal_owner_binding")
		}
	case goalAsyncPrincipalSystem:
		if p.UserID != 0 || p.Username != "" || p.Issuer != "" {
			return goalAsyncInvalid("principal_system_binding")
		}
	default:
		return goalAsyncInvalid("principal_kind")
	}
	return nil
}

func (p goalAsyncPrincipal) equal(other goalAsyncPrincipal) bool {
	return p.Kind == other.Kind && p.UserID == other.UserID &&
		p.Username == other.Username && p.Issuer == other.Issuer
}

func (p goalAsyncPrincipal) copyPrincipal() goalAsyncPrincipal {
	return p
}

// goalAsyncCeilings are the immutable initial ceilings. They are copied from
// the frozen goal spec plus explicit internal limits.
type goalAsyncCeilings struct {
	MaxSlices                int           `json:"max_slices"`
	MaxModelRounds           int           `json:"max_model_rounds"`
	MaxToolAttempts          int           `json:"max_tool_attempts"`
	MaxResultBytes           int           `json:"max_result_bytes"`
	MaxEvidenceRecords       int           `json:"max_evidence_records"`
	MaxEvidenceBytes         int           `json:"max_evidence_bytes"`
	MaxRetries               int           `json:"max_retries"`
	MaxScheduledWorkRequests int           `json:"max_scheduled_work_requests"`
	MaxConsecutiveDenials    int           `json:"max_consecutive_denials"`
	MaxSliceElapsed          time.Duration `json:"max_slice_elapsed_ns"`
}

type goalAsyncUsed struct {
	ModelRounds           int `json:"model_rounds"`
	ToolAttempts          int `json:"tool_attempts"`
	ResultBytes           int `json:"result_bytes"`
	EvidenceRecords       int `json:"evidence_records"`
	EvidenceBytes         int `json:"evidence_bytes"`
	Retries               int `json:"retries"`
	ScheduledWorkRequests int `json:"scheduled_work_requests"`
	ConsecutiveDenials    int `json:"consecutive_denials"`
}

func (c goalAsyncCeilings) validate() error {
	if c.MaxSlices < 1 || c.MaxSlices > hardGoalAsyncMaxSlices {
		return goalAsyncInvalid("ceilings.max_slices")
	}
	if c.MaxModelRounds < 1 || c.MaxModelRounds > hardGoalModelRounds {
		return goalAsyncInvalid("ceilings.max_model_rounds")
	}
	if c.MaxToolAttempts < 1 || c.MaxToolAttempts > hardGoalToolCalls {
		return goalAsyncInvalid("ceilings.max_tool_attempts")
	}
	if c.MaxResultBytes < 1024 || c.MaxResultBytes > hardGoalTotalResultBytes {
		return goalAsyncInvalid("ceilings.max_result_bytes")
	}
	if c.MaxEvidenceRecords < 1 || c.MaxEvidenceRecords > hardGoalEvidenceRecords {
		return goalAsyncInvalid("ceilings.max_evidence_records")
	}
	if c.MaxEvidenceBytes < 1024 || c.MaxEvidenceBytes > hardGoalEvidenceBytes {
		return goalAsyncInvalid("ceilings.max_evidence_bytes")
	}
	if c.MaxRetries < 0 || c.MaxRetries > hardGoalAsyncMaxRetries {
		return goalAsyncInvalid("ceilings.max_retries")
	}
	if c.MaxScheduledWorkRequests < 0 || c.MaxScheduledWorkRequests > hardGoalAsyncMaxScheduledWorkRequests {
		return goalAsyncInvalid("ceilings.max_scheduled_work_requests")
	}
	if c.MaxConsecutiveDenials < 1 || c.MaxConsecutiveDenials > hardGoalConsecutiveDenials {
		return goalAsyncInvalid("ceilings.max_consecutive_denials")
	}
	if c.MaxSliceElapsed < time.Second || c.MaxSliceElapsed > hardGoalMaxElapsed {
		return goalAsyncInvalid("ceilings.max_slice_elapsed")
	}
	return nil
}

func (u goalAsyncUsed) validateAgainst(c goalAsyncCeilings) error {
	if u.ModelRounds < 0 || u.ModelRounds > c.MaxModelRounds {
		return goalAsyncInvalid("used.model_rounds")
	}
	if u.ToolAttempts < 0 || u.ToolAttempts > c.MaxToolAttempts {
		return goalAsyncInvalid("used.tool_attempts")
	}
	if u.ResultBytes < 0 || u.ResultBytes > c.MaxResultBytes {
		return goalAsyncInvalid("used.result_bytes")
	}
	if u.EvidenceRecords < 0 || u.EvidenceRecords > c.MaxEvidenceRecords {
		return goalAsyncInvalid("used.evidence_records")
	}
	if u.EvidenceBytes < 0 || u.EvidenceBytes > c.MaxEvidenceBytes {
		return goalAsyncInvalid("used.evidence_bytes")
	}
	if u.Retries < 0 || u.Retries > c.MaxRetries {
		return goalAsyncInvalid("used.retries")
	}
	if u.ScheduledWorkRequests < 0 || u.ScheduledWorkRequests > c.MaxScheduledWorkRequests {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	if u.ConsecutiveDenials < 0 || u.ConsecutiveDenials > c.MaxConsecutiveDenials {
		return goalAsyncInvalid("used.consecutive_denials")
	}
	return nil
}

func (u goalAsyncUsed) monotonicFrom(previous goalAsyncUsed) error {
	if u.ModelRounds < previous.ModelRounds ||
		u.ToolAttempts < previous.ToolAttempts ||
		u.ResultBytes < previous.ResultBytes ||
		u.EvidenceRecords < previous.EvidenceRecords ||
		u.EvidenceBytes < previous.EvidenceBytes ||
		u.Retries < previous.Retries ||
		u.ScheduledWorkRequests < previous.ScheduledWorkRequests {
		return goalAsyncInvalid("used.regression")
	}
	// ConsecutiveDenials is a streak, not a cumulative total. It may reset to
	// zero after a successful call, but it may never exceed its ceiling.

	return nil
}

func (u goalAsyncUsed) reachesAnyCeiling(c goalAsyncCeilings) bool {
	return u.ModelRounds >= c.MaxModelRounds ||
		u.ToolAttempts >= c.MaxToolAttempts ||
		u.ResultBytes >= c.MaxResultBytes ||
		u.EvidenceRecords >= c.MaxEvidenceRecords ||
		u.EvidenceBytes >= c.MaxEvidenceBytes ||
		(c.MaxRetries > 0 && u.Retries >= c.MaxRetries) ||
		(c.MaxScheduledWorkRequests > 0 && u.ScheduledWorkRequests >= c.MaxScheduledWorkRequests)
}

// goalAsyncWorkKind and goalAsyncWorkState distinguish a proposal awaiting a
// real operator decision from an approved/scheduled operation and from an
// observed completed operation. They are not interchangeable.
type goalAsyncWorkKind string

const (
	goalAsyncWorkCrawlProposal  goalAsyncWorkKind = "crawl_proposal"
	goalAsyncWorkCrawlOperation goalAsyncWorkKind = "crawl_operation"
)

type goalAsyncWorkState string

const (
	goalAsyncWorkAwaitingApproval goalAsyncWorkState = "awaiting_approval"
	goalAsyncWorkScheduled        goalAsyncWorkState = "scheduled"
	goalAsyncWorkProcessing       goalAsyncWorkState = "processing"
	goalAsyncWorkCompleted        goalAsyncWorkState = "completed"
)

type goalAsyncWorkRef struct {
	Kind              goalAsyncWorkKind  `json:"kind"`
	State             goalAsyncWorkState `json:"state"`
	RequestID         string             `json:"request_id"`
	ProposalSHA256    string             `json:"proposal_sha256"`
	ApprovalID        string             `json:"approval_id,omitempty"`
	ApprovalSHA256    string             `json:"approval_sha256,omitempty"`
	OperationID       string             `json:"operation_id,omitempty"`
	SourceID          uint64             `json:"source_id"`
	ProposalExpiresAt time.Time          `json:"proposal_expires_at"`
	ApprovalExpiresAt time.Time          `json:"approval_expires_at,omitempty"`
	SubmitBy          time.Time          `json:"submit_by,omitempty"`
	AcceptedAt        time.Time          `json:"accepted_at,omitempty"`
	ObservedAt        time.Time          `json:"observed_at,omitempty"`
}

func (w goalAsyncWorkRef) sortKey() string {
	return string(w.Kind) + "|" + w.RequestID + "|" + w.OperationID + "|" + string(w.State)
}

func (w goalAsyncWorkRef) validate(allowedSources []uint64, createdAt, updatedAt time.Time) error {
	if !containsUint64(allowedSources, w.SourceID) || w.SourceID == 0 {
		return goalAsyncInvalid("work.source_id")
	}
	if !goalAsyncOperationID.MatchString(w.RequestID) {
		return goalAsyncInvalid("work.request_id")
	}
	if !goalAsyncHashPattern.MatchString(w.ProposalSHA256) {
		return goalAsyncInvalid("work.proposal_sha256")
	}
	if !validGoalAsyncTime(w.ProposalExpiresAt) || !w.ProposalExpiresAt.After(createdAt) {
		return goalAsyncInvalid("work.proposal_expires_at")
	}
	switch w.Kind {
	case goalAsyncWorkCrawlProposal:
		if w.State != goalAsyncWorkAwaitingApproval {
			return goalAsyncInvalid("work.state")
		}
		if w.ApprovalID != "" || w.ApprovalSHA256 != "" || w.OperationID != "" {
			return goalAsyncInvalid("work.proposal_identity")
		}
		if !w.ApprovalExpiresAt.IsZero() || !w.SubmitBy.IsZero() || !w.AcceptedAt.IsZero() || !w.ObservedAt.IsZero() {
			return goalAsyncInvalid("work.proposal_timestamps")
		}
	case goalAsyncWorkCrawlOperation:
		if w.State != goalAsyncWorkScheduled && w.State != goalAsyncWorkProcessing && w.State != goalAsyncWorkCompleted {
			return goalAsyncInvalid("work.state")
		}
		if !goalAsyncOperationID.MatchString(w.ApprovalID) {
			return goalAsyncInvalid("work.approval_id")
		}
		if !goalAsyncHashPattern.MatchString(w.ApprovalSHA256) {
			return goalAsyncInvalid("work.approval_sha256")
		}
		if !goalAsyncOperationID.MatchString(w.OperationID) {
			return goalAsyncInvalid("work.operation_id")
		}
		if !validGoalAsyncTime(w.ApprovalExpiresAt) || !w.ApprovalExpiresAt.After(createdAt) {
			return goalAsyncInvalid("work.approval_expires_at")
		}
		if !validGoalAsyncTime(w.SubmitBy) || !w.SubmitBy.After(createdAt) {
			return goalAsyncInvalid("work.submit_by")
		}
		if w.SubmitBy.After(w.ProposalExpiresAt) || w.SubmitBy.After(w.ApprovalExpiresAt) {
			return goalAsyncInvalid("work.submit_by")
		}
		if !validGoalAsyncTime(w.AcceptedAt) || w.AcceptedAt.Before(createdAt) {
			return goalAsyncInvalid("work.accepted_at")
		}
		if !w.AcceptedAt.Before(w.SubmitBy) ||
			!w.AcceptedAt.Before(w.ProposalExpiresAt) ||
			!w.AcceptedAt.Before(w.ApprovalExpiresAt) {
			return goalAsyncInvalid("work.accepted_at")
		}
		if w.State == goalAsyncWorkCompleted {
			if !validGoalAsyncTime(w.ObservedAt) || w.ObservedAt.Before(w.AcceptedAt) || w.ObservedAt.After(updatedAt) {
				return goalAsyncInvalid("work.observed_at")
			}
			// Proposal/approval expiry bounds submission, not later completion.
			// The logical goal deadline is enforced by the checkpoint transition.
		} else if !w.ObservedAt.IsZero() {
			return goalAsyncInvalid("work.observed_at")
		}
	default:
		return goalAsyncInvalid("work.kind")
	}
	return nil
}

// goalAsyncSchedulingObservation is a trusted test/dev fixture for a scheduling
// admission. It is not a database query and must not be populated from model
// text, tool arguments/results or event payloads.
type goalAsyncSchedulingObservation struct {
	RunID         string
	GoalID        string
	GoalSpecHash  string
	AgentID       string
	Owner         string
	Principal     goalAsyncPrincipal
	Work          goalAsyncWorkRef
	ApprovalValid bool
	ObservedAt    time.Time
}

// goalAsyncSchedulingPrerequisite is the unexported, non-serializable proof
// that a trusted caller observed an already-accepted operation for this exact
// run, proposal, approval and source. It is not a stored grant.
type goalAsyncSchedulingPrerequisite struct {
	runID              string
	goalID             string
	goalSpecHash       string
	agentID            string
	owner              string
	principal          goalAsyncPrincipal
	work               goalAsyncWorkRef
	approvalValid      bool
	observedAt         time.Time
	prerequisiteSHA256 string
}

func newGoalAsyncSchedulingPrerequisite(cp goalAsyncCheckpoint, obs goalAsyncSchedulingObservation) (goalAsyncSchedulingPrerequisite, error) {
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncSchedulingPrerequisite{}, err
	}
	if cp.State != goalAsyncRunning && cp.State != goalAsyncAwaitingApproval {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if obs.RunID != cp.RunID || obs.GoalID != cp.GoalID || obs.GoalSpecHash != cp.GoalSpecHash ||
		obs.AgentID != cp.AgentID || obs.Owner != cp.Owner {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if !obs.Principal.equal(goalAsyncPrincipal{
		Kind: cp.PrincipalKind, UserID: cp.PrincipalUserID,
		Username: cp.PrincipalUsername, Issuer: cp.PrincipalIssuer,
	}) {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if !obs.ApprovalValid {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if obs.Work.Kind != goalAsyncWorkCrawlOperation ||
		(obs.Work.State != goalAsyncWorkScheduled && obs.Work.State != goalAsyncWorkProcessing) {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if err := obs.Work.validate(cp.AllowedSources, cp.CreatedAt, cp.UpdatedAt); err != nil {
		return goalAsyncSchedulingPrerequisite{}, err
	}
	if !validGoalAsyncTime(obs.ObservedAt) || obs.ObservedAt.Before(obs.Work.AcceptedAt) ||
		!obs.ObservedAt.Before(cp.Deadline) {
		return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
	}
	if cp.State == goalAsyncAwaitingApproval {
		if _, ok := findGoalAsyncProposal(cp.Work, obs.Work.RequestID, obs.Work.ProposalSHA256, obs.Work.SourceID); !ok {
			return goalAsyncSchedulingPrerequisite{}, ErrGoalAsyncTransitionInvalid
		}
	}
	prerequisite := goalAsyncSchedulingPrerequisite{
		runID: cp.RunID, goalID: cp.GoalID, goalSpecHash: cp.GoalSpecHash,
		agentID: cp.AgentID, owner: cp.Owner,
		principal: obs.Principal.copyPrincipal(), work: obs.Work,
		approvalValid: obs.ApprovalValid,
		observedAt:    normalizeGoalAsyncTime(obs.ObservedAt),
	}
	prerequisite.prerequisiteSHA256 = goalAsyncSchedulingPrerequisiteHash(prerequisite)
	return prerequisite, nil
}

func goalAsyncSchedulingPrerequisiteHash(p goalAsyncSchedulingPrerequisite) string {
	payload := struct {
		RunID, GoalID, GoalSpecHash, AgentID, Owner string
		Principal                                   goalAsyncPrincipal
		Work                                        goalAsyncWorkRef
		ApprovalValid                               bool
		ObservedAt                                  time.Time
	}{
		p.runID, p.goalID, p.goalSpecHash, p.agentID, p.owner,
		p.principal, p.work, p.approvalValid, normalizeGoalAsyncTime(p.observedAt),
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("crowler-goal-async-scheduling-v2\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func (p goalAsyncSchedulingPrerequisite) validate(cp goalAsyncCheckpoint) error {
	if cp.State != goalAsyncRunning && cp.State != goalAsyncAwaitingApproval {
		return ErrGoalAsyncTransitionInvalid
	}
	if p.runID != cp.RunID || p.goalID != cp.GoalID || p.goalSpecHash != cp.GoalSpecHash ||
		p.agentID != cp.AgentID || p.owner != cp.Owner {
		return ErrGoalAsyncTransitionInvalid
	}
	if !p.principal.equal(goalAsyncPrincipal{
		Kind: cp.PrincipalKind, UserID: cp.PrincipalUserID,
		Username: cp.PrincipalUsername, Issuer: cp.PrincipalIssuer,
	}) {
		return ErrGoalAsyncTransitionInvalid
	}
	if !p.approvalValid || p.work.Kind != goalAsyncWorkCrawlOperation ||
		(p.work.State != goalAsyncWorkScheduled && p.work.State != goalAsyncWorkProcessing) {
		return ErrGoalAsyncTransitionInvalid
	}
	if err := p.work.validate(cp.AllowedSources, cp.CreatedAt, cp.UpdatedAt); err != nil {
		return err
	}
	if !validGoalAsyncTime(p.observedAt) || p.observedAt.Before(p.work.AcceptedAt) ||
		!p.observedAt.Before(cp.Deadline) {
		return ErrGoalAsyncTransitionInvalid
	}
	if cp.State == goalAsyncAwaitingApproval {
		if _, ok := findGoalAsyncProposal(cp.Work, p.work.RequestID, p.work.ProposalSHA256, p.work.SourceID); !ok {
			return ErrGoalAsyncTransitionInvalid
		}
	}
	if p.prerequisiteSHA256 != goalAsyncSchedulingPrerequisiteHash(p) {
		return ErrGoalAsyncTransitionInvalid
	}
	return nil
}

type goalAsyncEvidenceRef struct {
	EvidenceID    string       `json:"evidence_id"`
	Kind          EvidenceKind `json:"kind"`
	TrustClass    TrustClass   `json:"trust_class"`
	Producer      string       `json:"producer"`
	SourceIDs     []uint64     `json:"source_ids,omitempty"`
	ObservedAt    time.Time    `json:"observed_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	PayloadSHA256 string       `json:"payload_sha256"`
	RecordSHA256  string       `json:"record_sha256"`
	SizeBytes     int          `json:"size_bytes"`
}

func (e goalAsyncEvidenceRef) validate(allowedSources []uint64, createdAt, updatedAt time.Time) error {
	if !goalAsyncEvidenceIDPattern.MatchString(e.EvidenceID) {
		return goalAsyncInvalid("evidence.evidence_id")
	}
	if !goalEvidenceKinds[e.Kind] {
		return goalAsyncInvalid("evidence.kind")
	}
	if e.TrustClass != TrustSystemVerified && e.TrustClass != TrustToolObservation {
		return goalAsyncInvalid("evidence.trust_class")
	}
	if !goalAsyncProducerPattern.MatchString(e.Producer) {
		return goalAsyncInvalid("evidence.producer")
	}
	if !isSortedUniqueUint64(e.SourceIDs) {
		return goalAsyncInvalid("evidence.source_ids")
	}
	for _, sourceID := range e.SourceIDs {
		if sourceID == 0 || !containsUint64(allowedSources, sourceID) {
			return goalAsyncInvalid("evidence.source_scope")
		}
	}
	if !validGoalAsyncTime(e.ObservedAt) || e.ObservedAt.Before(createdAt) || e.ObservedAt.After(updatedAt) {
		return goalAsyncInvalid("evidence.observed_at")
	}
	if !validGoalAsyncTime(e.ExpiresAt) || !e.ExpiresAt.After(e.ObservedAt) {
		return goalAsyncInvalid("evidence.expires_at")
	}
	if !goalAsyncHashPattern.MatchString(e.PayloadSHA256) {
		return goalAsyncInvalid("evidence.payload_sha256")
	}
	if !goalAsyncHashPattern.MatchString(e.RecordSHA256) {
		return goalAsyncInvalid("evidence.record_sha256")
	}
	if e.SizeBytes <= 0 || e.SizeBytes > hardGoalPayloadBytes {
		return goalAsyncInvalid("evidence.size_bytes")
	}
	return nil
}

type goalAsyncVerificationRef struct {
	Verifier         string    `json:"verifier"`
	EvaluationSHA256 string    `json:"evaluation_sha256"`
	VerifiedAt       time.Time `json:"verified_at"`
	EvidenceIDs      []string  `json:"evidence_ids,omitempty"`
}

func (v goalAsyncVerificationRef) validate(createdAt, updatedAt, deadline time.Time) error {
	if v.Verifier != goalAsyncReasonDeterministicVerifier {
		return goalAsyncInvalid("verification.verifier")
	}
	if !goalAsyncHashPattern.MatchString(v.EvaluationSHA256) {
		return goalAsyncInvalid("verification.evaluation_sha256")
	}
	if !isSortedUniqueString(v.EvidenceIDs) || len(v.EvidenceIDs) > maxGoalAsyncEvidenceRefs {
		return goalAsyncInvalid("verification.evidence_ids")
	}
	for _, evidenceID := range v.EvidenceIDs {
		if !goalAsyncEvidenceIDPattern.MatchString(evidenceID) {
			return goalAsyncInvalid("verification.evidence_ids")
		}
	}
	if !validGoalAsyncTime(v.VerifiedAt) || v.VerifiedAt.Before(createdAt) || v.VerifiedAt.After(updatedAt) {
		return goalAsyncInvalid("verification.verified_at")
	}
	if !v.VerifiedAt.Before(deadline) {
		return goalAsyncInvalid("verification.deadline")
	}
	return nil
}

// goalAsyncCheckpoint is the versioned internal snapshot. The type is
// unexported; exported fields exist only so the stable v1 JSON representation
// can be decoded strictly. No field contains a prompt, model message, tool
// argument, result payload, token or secret.
type goalAsyncCheckpoint struct {
	CheckpointVersion  int                       `json:"checkpoint_version"`
	ContractVersion    string                    `json:"contract_version"`
	Revision           uint64                    `json:"revision"`
	RunID              string                    `json:"run_id"`
	TraceID            string                    `json:"trace_id"`
	CorrelationID      string                    `json:"correlation_id,omitempty"`
	AgentID            string                    `json:"agent_id"`
	Owner              string                    `json:"owner"`
	PrincipalKind      goalAsyncPrincipalKind    `json:"principal_kind"`
	PrincipalUserID    uint64                    `json:"principal_user_id,omitempty"`
	PrincipalUsername  string                    `json:"principal_username,omitempty"`
	PrincipalIssuer    string                    `json:"principal_issuer,omitempty"`
	GoalID             string                    `json:"goal_id"`
	GoalSpecHash       string                    `json:"goal_spec_hash"`
	PolicySHA256       string                    `json:"policy_sha256"`
	AllowedTools       []string                  `json:"allowed_tools,omitempty"`
	AllowedSources     []uint64                  `json:"allowed_sources"`
	State              goalAsyncState            `json:"state"`
	Slices             int                       `json:"slices"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	Deadline           time.Time                 `json:"deadline"`
	Ceilings           goalAsyncCeilings         `json:"ceilings"`
	Used               goalAsyncUsed             `json:"used"`
	Work               []goalAsyncWorkRef        `json:"work,omitempty"`
	Evidence           []goalAsyncEvidenceRef    `json:"evidence,omitempty"`
	Verification       *goalAsyncVerificationRef `json:"verification,omitempty"`
	ReasonCode         string                    `json:"reason_code"`
	AuditRefs          []string                  `json:"audit_refs,omitempty"`
	LedgerID           string                    `json:"ledger_id"`
	RunNonceHash       string                    `json:"run_nonce_hash"`
	LastTransitionID   string                    `json:"last_transition_id,omitempty"`
	LastTransitionHash string                    `json:"last_transition_hash,omitempty"`
}

// goalAsyncLimits are trusted internal limits that are not part of the public
// agent manifest. They are explicit so the future store cannot silently grant
// a longer logical lifetime, more slices, retries or work requests. The zero
// value selects safe defaults; once any field is set, a zero retry or
// scheduled-work value means exactly zero rather than a default. A zero
// LogicalLifetime still selects the provisional default; negative, sub-second
// or over-hard-bound values are rejected.
type goalAsyncLimits struct {
	MaxSlices                int
	MaxRetries               int
	MaxScheduledWorkRequests int
	LogicalLifetime          time.Duration
}

func newGoalAsyncCheckpoint(spec *AgentGoalSpec, identity AgentIdentity, principal goalAsyncPrincipal,
	runID, traceID, correlationID, ledgerID, runNonceHash string,
	now, deadline time.Time, limits goalAsyncLimits) (goalAsyncCheckpoint, error) {
	if spec == nil {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("spec")
	}
	if (spec.Acceptance.Mode != AcceptanceAll && spec.Acceptance.Mode != AcceptanceAny) ||
		len(spec.Acceptance.Criteria) == 0 {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("acceptance")
	}
	if !goalAsyncIDPattern.MatchString(spec.GoalID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("goal_id")
	}
	if !goalAsyncHashPattern.MatchString(spec.SpecHash) ||
		computeGoalSpecHash(spec) != spec.SpecHash {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("goal_spec_hash")
	}
	if !goalAsyncIDPattern.MatchString(identity.AgentID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("agent_id")
	}
	owner := strings.TrimSpace(identity.Owner)
	if !goalAsyncOwnerPattern.MatchString(owner) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("owner")
	}
	if err := principal.validateForOwner(owner); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if !goalAsyncIDPattern.MatchString(runID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("run_id")
	}
	if !goalAsyncIDPattern.MatchString(traceID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("trace_id")
	}
	if correlationID != "" && !goalAsyncIDPattern.MatchString(correlationID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("correlation_id")
	}
	if !goalAsyncIDPattern.MatchString(ledgerID) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("ledger_id")
	}
	if !goalAsyncHashPattern.MatchString(runNonceHash) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("run_nonce_hash")
	}
	now = normalizeGoalAsyncTime(now)
	if now.IsZero() {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("created_at")
	}

	tools, err := normalizeGoalAsyncTools(spec.Allowlist)
	if err != nil {
		return goalAsyncCheckpoint{}, err
	}
	sources, err := normalizeGoalAsyncSources(spec.AllowedSources)
	if err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if len(sources) == 0 {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("allowed_sources")
	}
	ceilings, err := goalAsyncCeilingsFromSpec(spec, limits)
	if err != nil {
		return goalAsyncCheckpoint{}, err
	}
	logicalLifetime := limits.LogicalLifetime
	if logicalLifetime == 0 {
		logicalLifetime = defaultGoalAsyncLogicalLifetime
	}
	if logicalLifetime < time.Second || logicalLifetime > hardGoalAsyncLogicalLifetime {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("logical_lifetime")
	}
	deadline = normalizeGoalAsyncTime(deadline)
	if deadline.IsZero() {
		deadline = now.Add(logicalLifetime)
	}
	if !deadline.After(now) {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("deadline")
	}
	if deadline.Sub(now) > logicalLifetime {
		return goalAsyncCheckpoint{}, goalAsyncInvalid("deadline_extension")
	}

	cp := goalAsyncCheckpoint{
		CheckpointVersion: goalAsyncCheckpointVersion,
		ContractVersion:   goalAsyncContractVersion,
		Revision:          1,
		RunID:             runID,
		TraceID:           traceID,
		CorrelationID:     correlationID,
		AgentID:           identity.AgentID,
		Owner:             owner,
		PrincipalKind:     principal.Kind,
		PrincipalUserID:   principal.UserID,
		PrincipalUsername: principal.Username,
		PrincipalIssuer:   principal.Issuer,
		GoalID:            spec.GoalID,
		GoalSpecHash:      spec.SpecHash,
		PolicySHA256:      goalAsyncPolicyHash(spec),
		AllowedTools:      tools,
		AllowedSources:    sources,
		State:             goalAsyncCreated,
		Slices:            0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Deadline:          deadline,
		Ceilings:          ceilings,
		Used:              goalAsyncUsed{},
		ReasonCode:        goalAsyncReasonCreated,
		LedgerID:          ledgerID,
		RunNonceHash:      runNonceHash,
	}
	cp = normalizeGoalAsyncCheckpoint(cp)
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	return cp, nil
}

func goalAsyncCeilingsFromSpec(spec *AgentGoalSpec, limits goalAsyncLimits) (goalAsyncCeilings, error) {
	budgets := spec.Budgets
	useDefaults := limits == (goalAsyncLimits{})
	if limits.MaxSlices < 0 {
		return goalAsyncCeilings{}, goalAsyncInvalid("max_slices")
	}
	maxSlices := limits.MaxSlices
	if maxSlices == 0 {
		maxSlices = defaultGoalAsyncMaxSlices
	}
	maxRetries := limits.MaxRetries
	maxScheduled := limits.MaxScheduledWorkRequests
	if useDefaults {
		maxRetries = defaultGoalAsyncMaxRetries
		maxScheduled = defaultGoalAsyncMaxScheduledWorkRequests
	}
	if maxRetries < 0 {
		return goalAsyncCeilings{}, goalAsyncInvalid("max_retries")
	}
	if maxScheduled < 0 {
		return goalAsyncCeilings{}, goalAsyncInvalid("max_scheduled_work_requests")
	}
	c := goalAsyncCeilings{
		MaxSlices:                maxSlices,
		MaxModelRounds:           budgets.MaxModelRounds,
		MaxToolAttempts:          budgets.MaxToolCalls,
		MaxResultBytes:           budgets.MaxTotalResultBytes,
		MaxEvidenceRecords:       budgets.MaxEvidenceRecords,
		MaxEvidenceBytes:         budgets.MaxEvidenceBytes,
		MaxRetries:               maxRetries,
		MaxScheduledWorkRequests: maxScheduled,
		MaxConsecutiveDenials:    budgets.MaxConsecutiveDenials,
		MaxSliceElapsed:          budgets.MaxElapsed,
	}
	if c.MaxModelRounds <= 0 {
		c.MaxModelRounds = defaultGoalModelRounds
	}
	if c.MaxToolAttempts <= 0 {
		c.MaxToolAttempts = defaultGoalToolCalls
	}
	if c.MaxResultBytes <= 0 {
		c.MaxResultBytes = defaultGoalTotalResultBytes
	}
	if c.MaxEvidenceRecords <= 0 {
		c.MaxEvidenceRecords = defaultGoalEvidenceRecords
	}
	if c.MaxEvidenceBytes <= 0 {
		c.MaxEvidenceBytes = defaultGoalEvidenceBytes
	}
	if c.MaxConsecutiveDenials <= 0 {
		c.MaxConsecutiveDenials = defaultGoalConsecutiveDenials
	}
	if c.MaxSliceElapsed <= 0 {
		c.MaxSliceElapsed = defaultGoalMaxElapsed
	}
	if err := c.validate(); err != nil {
		return goalAsyncCeilings{}, err
	}
	return c, nil
}

func normalizeGoalAsyncTools(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !toolNamePattern.MatchString(value) {
			return nil, goalAsyncInvalid("allowed_tools")
		}
		if seen[value] {
			return nil, goalAsyncInvalid("allowed_tools")
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) > 32 {
		return nil, goalAsyncInvalid("allowed_tools")
	}
	sort.Strings(out)
	return out, nil
}

func normalizeGoalAsyncSources(values []uint64) ([]uint64, error) {
	out := make([]uint64, 0, len(values))
	seen := map[uint64]bool{}
	for _, value := range values {
		if value == 0 || seen[value] {
			return nil, goalAsyncInvalid("allowed_sources")
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 || len(out) > 32 {
		return nil, goalAsyncInvalid("allowed_sources")
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func goalAsyncPolicyHash(spec *AgentGoalSpec) string {
	if spec == nil {
		return ""
	}
	operations := []string(nil)
	enabled := false
	issuer := ""
	var proposalTTL time.Duration
	maxRequests := 0
	var budgetWindow time.Duration
	if spec.MutationPolicy != nil {
		enabled = spec.MutationPolicy.Enabled
		issuer = spec.MutationPolicy.Issuer
		proposalTTL = spec.MutationPolicy.ProposalTTL
		maxRequests = spec.MutationPolicy.MaxRequests
		budgetWindow = spec.MutationPolicy.BudgetWindow
		operations = append([]string(nil), spec.MutationPolicy.AllowedOperations...)
		sort.Strings(operations)
	}
	evidence := make([]string, 0, len(spec.Evidence.Allow))
	for _, kind := range spec.Evidence.Allow {
		evidence = append(evidence, string(kind))
	}
	sort.Strings(evidence)
	payload := struct {
		AllowedTools          []string
		AllowedSources        []uint64
		MutationEnabled       bool
		MutationOperations    []string
		MutationIssuer        string
		MutationProposalTTL   time.Duration
		MutationMaxRequests   int
		MutationBudgetWindow  time.Duration
		EvidenceAllow         []string
		EvidenceDefaultMaxAge time.Duration
		EvidenceMaxPayload    int
		Budgets               GoalBudgets
	}{
		AllowedTools:          append([]string(nil), spec.Allowlist...),
		AllowedSources:        append([]uint64(nil), spec.AllowedSources...),
		MutationEnabled:       enabled,
		MutationOperations:    operations,
		MutationIssuer:        issuer,
		MutationProposalTTL:   proposalTTL,
		MutationMaxRequests:   maxRequests,
		MutationBudgetWindow:  budgetWindow,
		EvidenceAllow:         evidence,
		EvidenceDefaultMaxAge: spec.Evidence.DefaultMaxAge,
		EvidenceMaxPayload:    spec.Evidence.MaxPayloadBytes,
		Budgets:               spec.Budgets,
	}
	sort.Strings(payload.AllowedTools)
	sort.Slice(payload.AllowedSources, func(i, j int) bool { return payload.AllowedSources[i] < payload.AllowedSources[j] })
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("crowler-goal-async-policy-v1\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func normalizeGoalAsyncCheckpoint(cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	cp.CreatedAt = normalizeGoalAsyncTime(cp.CreatedAt)
	cp.UpdatedAt = normalizeGoalAsyncTime(cp.UpdatedAt)
	cp.Deadline = normalizeGoalAsyncTime(cp.Deadline)
	cp.AllowedTools = sortedUniqueStrings(cp.AllowedTools)
	cp.AllowedSources = sortedUniqueUint64(cp.AllowedSources)
	if len(cp.Work) > 0 {
		work := make([]goalAsyncWorkRef, len(cp.Work))
		for i, ref := range cp.Work {
			work[i] = ref
			work[i].ProposalExpiresAt = normalizeGoalAsyncTime(ref.ProposalExpiresAt)
			work[i].ApprovalExpiresAt = normalizeGoalAsyncTime(ref.ApprovalExpiresAt)
			work[i].SubmitBy = normalizeGoalAsyncTime(ref.SubmitBy)
			work[i].AcceptedAt = normalizeGoalAsyncTime(ref.AcceptedAt)
			work[i].ObservedAt = normalizeGoalAsyncTime(ref.ObservedAt)
		}
		sort.Slice(work, func(i, j int) bool { return work[i].sortKey() < work[j].sortKey() })
		cp.Work = work
	} else {
		cp.Work = nil
	}
	if len(cp.Evidence) > 0 {
		evidence := make([]goalAsyncEvidenceRef, len(cp.Evidence))
		for i, ref := range cp.Evidence {
			evidence[i] = ref
			evidence[i].SourceIDs = sortedUniqueUint64(ref.SourceIDs)
			evidence[i].ObservedAt = normalizeGoalAsyncTime(ref.ObservedAt)
			evidence[i].ExpiresAt = normalizeGoalAsyncTime(ref.ExpiresAt)
		}
		sort.Slice(evidence, func(i, j int) bool { return evidence[i].EvidenceID < evidence[j].EvidenceID })
		cp.Evidence = evidence
	} else {
		cp.Evidence = nil
	}
	cp.AuditRefs = sortedUniqueStrings(cp.AuditRefs)
	if cp.Verification != nil {
		verification := *cp.Verification
		verification.VerifiedAt = normalizeGoalAsyncTime(verification.VerifiedAt)
		verification.EvidenceIDs = sortedUniqueStrings(verification.EvidenceIDs)
		cp.Verification = &verification
	}
	return cp
}

func cloneGoalAsyncCheckpoint(cp goalAsyncCheckpoint) goalAsyncCheckpoint {
	out := cp
	if cp.AllowedTools != nil {
		out.AllowedTools = append([]string(nil), cp.AllowedTools...)
	}
	if cp.AllowedSources != nil {
		out.AllowedSources = append([]uint64(nil), cp.AllowedSources...)
	}
	if cp.Work != nil {
		out.Work = make([]goalAsyncWorkRef, len(cp.Work))
		copy(out.Work, cp.Work)
	}
	if cp.Evidence != nil {
		out.Evidence = make([]goalAsyncEvidenceRef, len(cp.Evidence))
		for i, ref := range cp.Evidence {
			out.Evidence[i] = ref
			out.Evidence[i].SourceIDs = append([]uint64(nil), ref.SourceIDs...)
		}
	}
	if cp.AuditRefs != nil {
		out.AuditRefs = append([]string(nil), cp.AuditRefs...)
	}
	if cp.Verification != nil {
		verification := *cp.Verification
		verification.EvidenceIDs = append([]string(nil), cp.Verification.EvidenceIDs...)
		out.Verification = &verification
	}
	return out
}

func validateGoalAsyncCheckpoint(cp goalAsyncCheckpoint) error {
	if cp.CheckpointVersion != goalAsyncCheckpointVersion {
		return goalAsyncInvalid("checkpoint_version")
	}
	if cp.ContractVersion != goalAsyncContractVersion {
		return goalAsyncInvalid("contract_version")
	}
	if cp.Revision == 0 {
		return goalAsyncInvalid("revision")
	}
	if !goalAsyncIDPattern.MatchString(cp.RunID) {
		return goalAsyncInvalid("run_id")
	}
	if !goalAsyncIDPattern.MatchString(cp.TraceID) {
		return goalAsyncInvalid("trace_id")
	}
	if cp.CorrelationID != "" && !goalAsyncIDPattern.MatchString(cp.CorrelationID) {
		return goalAsyncInvalid("correlation_id")
	}
	if !goalAsyncIDPattern.MatchString(cp.AgentID) {
		return goalAsyncInvalid("agent_id")
	}
	if !goalAsyncOwnerPattern.MatchString(cp.Owner) {
		return goalAsyncInvalid("owner")
	}
	principal := goalAsyncPrincipal{
		Kind: cp.PrincipalKind, UserID: cp.PrincipalUserID,
		Username: cp.PrincipalUsername, Issuer: cp.PrincipalIssuer,
	}
	if err := principal.validateForOwner(cp.Owner); err != nil {
		return err
	}
	if !goalAsyncIDPattern.MatchString(cp.GoalID) {
		return goalAsyncInvalid("goal_id")
	}
	if !goalAsyncHashPattern.MatchString(cp.GoalSpecHash) {
		return goalAsyncInvalid("goal_spec_hash")
	}
	if !goalAsyncHashPattern.MatchString(cp.PolicySHA256) {
		return goalAsyncInvalid("policy_sha256")
	}
	if !isSortedUniqueString(cp.AllowedTools) {
		return goalAsyncInvalid("allowed_tools")
	}
	if len(cp.AllowedTools) > 32 {
		return goalAsyncInvalid("allowed_tools")
	}
	for _, tool := range cp.AllowedTools {
		if !toolNamePattern.MatchString(tool) {
			return goalAsyncInvalid("allowed_tools")
		}
	}
	if !isSortedUniqueUint64(cp.AllowedSources) || len(cp.AllowedSources) == 0 || len(cp.AllowedSources) > 32 {
		return goalAsyncInvalid("allowed_sources")
	}
	for _, sourceID := range cp.AllowedSources {
		if sourceID == 0 {
			return goalAsyncInvalid("allowed_sources")
		}
	}
	if !cp.State.valid() {
		return goalAsyncInvalid("state")
	}
	if cp.Slices < 0 {
		return goalAsyncInvalid("slices")
	}
	if !validGoalAsyncTime(cp.CreatedAt) || !validGoalAsyncTime(cp.UpdatedAt) || !validGoalAsyncTime(cp.Deadline) {
		return goalAsyncInvalid("timestamps")
	}
	if cp.UpdatedAt.Before(cp.CreatedAt) {
		return goalAsyncInvalid("updated_at")
	}
	if !cp.Deadline.After(cp.CreatedAt) {
		return goalAsyncInvalid("deadline")
	}
	if cp.Deadline.Sub(cp.CreatedAt) > hardGoalAsyncLogicalLifetime {
		return goalAsyncInvalid("deadline")
	}
	if err := cp.Ceilings.validate(); err != nil {
		return err
	}
	if err := cp.Used.validateAgainst(cp.Ceilings); err != nil {
		return err
	}
	if cp.Slices > cp.Ceilings.MaxSlices {
		return goalAsyncInvalid("slices")
	}
	if len(cp.Work) > maxGoalAsyncWorkRefs || len(cp.Work) > cp.Ceilings.MaxScheduledWorkRequests {
		return goalAsyncInvalid("work")
	}
	if cp.Used.ScheduledWorkRequests < len(cp.Work) {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	if err := validateGoalAsyncWorkSet(cp.Work, cp.AllowedSources, cp.CreatedAt, cp.UpdatedAt, cp.Ceilings); err != nil {
		return err
	}
	if len(cp.Evidence) > maxGoalAsyncEvidenceRefs || len(cp.Evidence) > cp.Ceilings.MaxEvidenceRecords {
		return goalAsyncInvalid("evidence")
	}
	if cp.Used.EvidenceRecords < len(cp.Evidence) {
		return goalAsyncInvalid("used.evidence_records")
	}
	evidenceBytes := 0
	for _, ref := range cp.Evidence {
		evidenceBytes += ref.SizeBytes
	}
	if evidenceBytes > cp.Used.EvidenceBytes {
		return goalAsyncInvalid("used.evidence_bytes")
	}
	if err := validateGoalAsyncEvidenceSet(cp.Evidence, cp.AllowedSources, cp.CreatedAt, cp.UpdatedAt); err != nil {
		return err
	}
	if len(cp.AuditRefs) > maxGoalAsyncAuditRefs || !isSortedUniqueString(cp.AuditRefs) {
		return goalAsyncInvalid("audit_refs")
	}
	for _, ref := range cp.AuditRefs {
		if !goalAsyncIDPattern.MatchString(ref) {
			return goalAsyncInvalid("audit_refs")
		}
	}
	if cp.State == goalAsyncCriteriaMet {
		if cp.Verification == nil {
			return goalAsyncInvalid("verification")
		}
		if err := cp.Verification.validate(cp.CreatedAt, cp.UpdatedAt, cp.Deadline); err != nil {
			return err
		}
		if !hasAllGoalAsyncEvidence(cp.Evidence, cp.Verification.EvidenceIDs) {
			return goalAsyncInvalid("verification.evidence_ids")
		}
	} else if cp.Verification != nil {
		return goalAsyncInvalid("verification")
	}
	if !goalAsyncReasonPattern.MatchString(cp.ReasonCode) {
		return goalAsyncInvalid("reason_code")
	}
	if cp.State == goalAsyncCreated && cp.ReasonCode != goalAsyncReasonCreated {
		return goalAsyncInvalid("reason_code")
	}
	if cp.Revision == 1 {
		if cp.LastTransitionID != "" || cp.LastTransitionHash != "" {
			return goalAsyncInvalid("last_transition")
		}
	} else {
		if !goalAsyncIDPattern.MatchString(cp.LastTransitionID) || !goalAsyncHashPattern.MatchString(cp.LastTransitionHash) {
			return goalAsyncInvalid("last_transition")
		}
	}
	if cp.State != goalAsyncCreated && cp.State != goalAsyncBudgetExhausted &&
		cp.State != goalAsyncCancelled && cp.State != goalAsyncDenied && cp.State != goalAsyncFailed &&
		cp.Slices < 1 {
		return goalAsyncInvalid("slices")
	}
	switch cp.State {
	case goalAsyncCreated, goalAsyncRunning, goalAsyncAwaitingApproval,
		goalAsyncAwaitingExternalWork, goalAsyncCriteriaMet, goalAsyncIncomplete:
		if !cp.UpdatedAt.Before(cp.Deadline) {
			return goalAsyncInvalid("deadline")
		}
	}
	if cp.State == goalAsyncCriteriaMet &&
		cp.ReasonCode != goalAsyncReasonCriteriaAllMet && cp.ReasonCode != goalAsyncReasonCriteriaAnyMet {
		return goalAsyncInvalid("reason_code")
	}
	if cp.State == goalAsyncAwaitingApproval &&
		!hasGoalAsyncWorkState(cp.Work, goalAsyncWorkCrawlProposal, goalAsyncWorkAwaitingApproval) {
		return goalAsyncInvalid("work.proposal_required")
	}
	if cp.State == goalAsyncAwaitingExternalWork &&
		!hasGoalAsyncWorkState(cp.Work, goalAsyncWorkCrawlOperation, goalAsyncWorkScheduled, goalAsyncWorkProcessing) {
		return goalAsyncInvalid("work.operation_required")
	}
	if cp.State == goalAsyncIncomplete && hasActiveGoalAsyncWork(cp.Work) {
		return goalAsyncInvalid("work.incomplete")
	}
	if cp.State == goalAsyncCreated {
		if cp.Revision != 1 || cp.Slices != 0 || cp.Used != (goalAsyncUsed{}) ||
			len(cp.Work) != 0 || len(cp.Evidence) != 0 || len(cp.AuditRefs) != 0 {
			return goalAsyncInvalid("created_state")
		}
	}
	if !goalAsyncIDPattern.MatchString(cp.LedgerID) {
		return goalAsyncInvalid("ledger_id")
	}
	if !goalAsyncHashPattern.MatchString(cp.RunNonceHash) {
		return goalAsyncInvalid("run_nonce_hash")
	}
	return nil
}

func validateGoalAsyncWorkSet(work []goalAsyncWorkRef, allowedSources []uint64, createdAt, updatedAt time.Time, ceilings goalAsyncCeilings) error {
	if !sort.SliceIsSorted(work, func(i, j int) bool { return work[i].sortKey() < work[j].sortKey() }) {
		return goalAsyncInvalid("work.order")
	}
	requests := map[string]goalAsyncWorkRef{}
	operations := map[string]goalAsyncWorkRef{}
	approvals := map[string]goalAsyncWorkRef{}
	for _, ref := range work {
		if err := ref.validate(allowedSources, createdAt, updatedAt); err != nil {
			return err
		}
		if ref.Kind == goalAsyncWorkCrawlOperation && ref.AcceptedAt.After(updatedAt) {
			return goalAsyncInvalid("work.accepted_at")
		}
		if existing, ok := requests[ref.RequestID]; ok {
			if existing.Kind != ref.Kind || existing.SourceID != ref.SourceID ||
				existing.ProposalSHA256 != ref.ProposalSHA256 {
				return goalAsyncInvalid("work.identity_conflict")
			}
			return goalAsyncInvalid("work.duplicate")
		}
		requests[ref.RequestID] = ref
		if ref.OperationID != "" {
			if existing, ok := operations[ref.OperationID]; ok {
				if existing.RequestID != ref.RequestID || existing.SourceID != ref.SourceID ||
					existing.ApprovalID != ref.ApprovalID {
					return goalAsyncInvalid("work.identity_conflict")
				}
				return goalAsyncInvalid("work.duplicate")
			}
			operations[ref.OperationID] = ref
		}
		if ref.ApprovalID != "" {
			if existing, ok := approvals[ref.ApprovalID]; ok {
				if existing.RequestID != ref.RequestID || existing.SourceID != ref.SourceID ||
					existing.OperationID != ref.OperationID {
					return goalAsyncInvalid("work.identity_conflict")
				}
				return goalAsyncInvalid("work.duplicate")
			}
			approvals[ref.ApprovalID] = ref
		}
	}
	if len(work) > ceilings.MaxScheduledWorkRequests {
		return goalAsyncInvalid("work.ceiling")
	}
	return nil
}

func validateGoalAsyncEvidenceSet(evidence []goalAsyncEvidenceRef, allowedSources []uint64, createdAt, updatedAt time.Time) error {
	if !sort.SliceIsSorted(evidence, func(i, j int) bool { return evidence[i].EvidenceID < evidence[j].EvidenceID }) {
		return goalAsyncInvalid("evidence.order")
	}
	ids := map[string]goalAsyncEvidenceRef{}
	hashes := map[string]bool{}
	for _, ref := range evidence {
		if err := ref.validate(allowedSources, createdAt, updatedAt); err != nil {
			return err
		}
		if existing, ok := ids[ref.EvidenceID]; ok {
			if existing.RecordSHA256 != ref.RecordSHA256 {
				return goalAsyncInvalid("evidence.identity_conflict")
			}
			return goalAsyncInvalid("evidence.duplicate")
		}
		ids[ref.EvidenceID] = ref
		if hashes[ref.RecordSHA256] {
			return goalAsyncInvalid("evidence.duplicate")
		}
		hashes[ref.RecordSHA256] = true
	}
	return nil
}

// goalAsyncSliceDeadline bounds one execution slice by both the synchronous
// slice ceiling and the logical goal deadline. It does not start a timer or
// perform I/O; a future executor supplies the trusted clock and uses the
// returned deadline for one model/tool slice.
func goalAsyncSliceDeadline(cp goalAsyncCheckpoint, now time.Time) (time.Time, error) {
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return time.Time{}, err
	}
	now = normalizeGoalAsyncTime(now)
	if now.IsZero() || now.Before(cp.CreatedAt) {
		return time.Time{}, goalAsyncInvalid("now")
	}
	if !now.Before(cp.Deadline) {
		return time.Time{}, ErrGoalAsyncDeadline
	}
	sliceDeadline := now.Add(cp.Ceilings.MaxSliceElapsed)
	if sliceDeadline.After(cp.Deadline) {
		sliceDeadline = cp.Deadline
	}
	return sliceDeadline, nil
}

// goalAsyncTransitionRequest is a trusted internal request. Prerequisite
// objects are deliberately unexported and non-serializable; a stored record
// cannot become a current grant or verifier decision.
type goalAsyncTransitionRequest struct {
	OperationID      string
	ExpectedRevision uint64
	Kind             goalAsyncTransitionKind
	Now              time.Time
	ReasonCode       string
	Used             *goalAsyncUsed
	Work             *goalAsyncWorkRef
	Evidence         []goalAsyncEvidenceRef
	AuditRefs        []string
	Verifier         *goalAsyncVerifierDecision
	Resume           *goalAsyncResumeGrant
	Scheduling       *goalAsyncSchedulingPrerequisite
}

type goalAsyncTransitionOutcome string

const (
	goalAsyncTransitionApplied  goalAsyncTransitionOutcome = "applied"
	goalAsyncTransitionReplayed goalAsyncTransitionOutcome = "replayed"
)

type goalAsyncVerifierDecision struct {
	runID            string
	goalID           string
	goalSpecHash     string
	ledgerID         string
	runNonceHash     string
	agentID          string
	owner            string
	evaluationSHA256 string
	reasonCode       string
	verifiedAt       time.Time
	evidenceIDs      []string
	decisionSHA256   string
	criteriaMet      bool
}

type goalAsyncCriterionSummary struct {
	ID          string   `json:"id"`
	Result      string   `json:"result"`
	ReasonCode  string   `json:"reason_code"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type goalAsyncEvaluationSummary struct {
	State       string                      `json:"state"`
	EvaluatedAt time.Time                   `json:"evaluated_at"`
	Criteria    []goalAsyncCriterionSummary `json:"criteria"`
}

func newGoalAsyncVerifierDecision(cp goalAsyncCheckpoint, input GoalEvalInput) (goalAsyncVerifierDecision, error) {
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncVerifierDecision{}, err
	}
	if input.Spec == nil || input.Spec.GoalID != cp.GoalID || input.Spec.SpecHash != cp.GoalSpecHash ||
		computeGoalSpecHash(input.Spec) != input.Spec.SpecHash ||
		(input.Spec.Acceptance.Mode != AcceptanceAll && input.Spec.Acceptance.Mode != AcceptanceAny) ||
		len(input.Spec.Acceptance.Criteria) == 0 {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.spec")
	}
	if input.RunID != cp.RunID || input.GoalID != cp.GoalID || input.GoalSpecHash != cp.GoalSpecHash ||
		input.LedgerID != cp.LedgerID || input.RunNonceHash != cp.RunNonceHash ||
		input.AgentID != cp.AgentID || input.Owner != cp.Owner {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.identity")
	}
	now := normalizeGoalAsyncTime(input.Now)
	if now.IsZero() {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.now")
	}
	if now.Before(cp.UpdatedAt) || !now.Before(cp.Deadline) {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.time")
	}
	input.Now = now
	evaluation, err := EvaluateGoal(input)
	if err != nil {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.evaluation")
	}
	if evaluation.State != "met" {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.not_met")
	}
	summary := goalAsyncEvaluationSummary{State: evaluation.State, EvaluatedAt: normalizeGoalAsyncTime(evaluation.EvaluatedAt)}
	var evidenceIDs []string
	for _, criterion := range evaluation.Criteria {
		evidence := append([]string(nil), criterion.EvidenceIDs...)
		sort.Strings(evidence)
		evidenceIDs = append(evidenceIDs, evidence...)
		summary.Criteria = append(summary.Criteria, goalAsyncCriterionSummary{
			ID: criterion.ID, Result: criterion.Result,
			ReasonCode: criterion.ReasonCode, EvidenceIDs: evidence,
		})
	}
	evidenceIDs = sortedUniqueStrings(evidenceIDs)
	if len(evidenceIDs) > maxGoalAsyncEvidenceRefs {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.evidence")
	}
	for _, evidenceID := range evidenceIDs {
		if !goalAsyncEvidenceIDPattern.MatchString(evidenceID) {
			return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.evidence")
		}
	}
	sort.Slice(summary.Criteria, func(i, j int) bool { return summary.Criteria[i].ID < summary.Criteria[j].ID })
	data, err := json.Marshal(summary)
	if err != nil {
		return goalAsyncVerifierDecision{}, goalAsyncInvalid("verifier.evaluation")
	}
	sum := sha256.Sum256(append([]byte("crowler-goal-async-evaluation-v1\x00"), data...))
	decision := goalAsyncVerifierDecision{
		runID: cp.RunID, goalID: cp.GoalID, goalSpecHash: cp.GoalSpecHash,
		ledgerID: cp.LedgerID, runNonceHash: cp.RunNonceHash,
		agentID: cp.AgentID, owner: cp.Owner,
		evaluationSHA256: hex.EncodeToString(sum[:]),
		reasonCode:       criteriaReason(input.Spec.Acceptance.Mode),
		verifiedAt:       now,
		evidenceIDs:      evidenceIDs,
		criteriaMet:      true,
	}
	decision.decisionSHA256 = goalAsyncVerifierDecisionHash(decision)
	return decision, nil
}

func goalAsyncVerifierDecisionHash(d goalAsyncVerifierDecision) string {
	payload := struct {
		RunID, GoalID, GoalSpecHash, LedgerID, RunNonceHash, AgentID, Owner string
		EvaluationSHA256, ReasonCode                                        string
		VerifiedAt                                                          time.Time
		EvidenceIDs                                                         []string
		CriteriaMet                                                         bool
	}{
		d.runID, d.goalID, d.goalSpecHash, d.ledgerID, d.runNonceHash,
		d.agentID, d.owner, d.evaluationSHA256, d.reasonCode,
		normalizeGoalAsyncTime(d.verifiedAt), sortedUniqueStrings(d.evidenceIDs),
		d.criteriaMet,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("crowler-goal-async-verifier-v2\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func (d goalAsyncVerifierDecision) validate(cp goalAsyncCheckpoint) error {
	if !d.criteriaMet || d.runID != cp.RunID || d.goalID != cp.GoalID ||
		d.goalSpecHash != cp.GoalSpecHash || d.ledgerID != cp.LedgerID ||
		d.runNonceHash != cp.RunNonceHash || d.agentID != cp.AgentID || d.owner != cp.Owner {
		return ErrGoalAsyncVerification
	}
	if !goalAsyncHashPattern.MatchString(d.evaluationSHA256) || !goalAsyncReasonPattern.MatchString(d.reasonCode) {
		return ErrGoalAsyncVerification
	}
	if !isSortedUniqueString(d.evidenceIDs) || len(d.evidenceIDs) > maxGoalAsyncEvidenceRefs {
		return ErrGoalAsyncVerification
	}
	for _, evidenceID := range d.evidenceIDs {
		if !goalAsyncEvidenceIDPattern.MatchString(evidenceID) {
			return ErrGoalAsyncVerification
		}
	}
	if !validGoalAsyncTime(d.verifiedAt) || d.verifiedAt.Before(cp.UpdatedAt) || !d.verifiedAt.Before(cp.Deadline) {
		return ErrGoalAsyncVerification
	}
	if d.decisionSHA256 != goalAsyncVerifierDecisionHash(d) {
		return ErrGoalAsyncVerification
	}
	return nil
}

// goalAsyncCurrentAuthority is supplied by a future resume adapter after live
// account, source ownership, agent and policy checks. Approval validity is a
// scheduling-time fact; it is bound by the scheduling prerequisite and is not
// required for later evidence evaluation. This value is not serialized into
// the checkpoint and is not a new grant.
type goalAsyncCurrentAuthority struct {
	AgentID         string
	Owner           string
	Principal       goalAsyncPrincipal
	PolicySHA256    string
	AllowedTools    []string
	AllowedSources  []uint64
	VerifiedSources []uint64
	AgentEnabled    bool
}

type goalAsyncResumeGrant struct {
	runID          string
	goalID         string
	goalSpecHash   string
	agentID        string
	owner          string
	principal      goalAsyncPrincipal
	policySHA256   string
	allowedTools   []string
	allowedSources []uint64
	operationID    string
	workState      goalAsyncWorkState
	checkedAt      time.Time
	grantSHA256    string
}

func newGoalAsyncResumeGrant(cp goalAsyncCheckpoint, current goalAsyncCurrentAuthority,
	operationID string, workState goalAsyncWorkState, now time.Time) (goalAsyncResumeGrant, error) {
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncResumeGrant{}, err
	}
	if cp.State != goalAsyncAwaitingExternalWork {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if !current.AgentEnabled || current.AgentID != cp.AgentID || current.Owner != cp.Owner {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if !current.Principal.equal(goalAsyncPrincipal{
		Kind: cp.PrincipalKind, UserID: cp.PrincipalUserID,
		Username: cp.PrincipalUsername, Issuer: cp.PrincipalIssuer,
	}) {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if current.PolicySHA256 != cp.PolicySHA256 {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if !equalStringSets(current.AllowedTools, cp.AllowedTools) ||
		!equalUint64Sets(current.AllowedSources, cp.AllowedSources) {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if !containsAllUint64(current.VerifiedSources, cp.AllowedSources) {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if !goalAsyncOperationID.MatchString(operationID) {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if workState != goalAsyncWorkProcessing && workState != goalAsyncWorkCompleted {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	work, ok := findGoalAsyncWork(cp.Work, operationID)
	if !ok || work.Kind != goalAsyncWorkCrawlOperation {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if work.State != goalAsyncWorkScheduled && work.State != goalAsyncWorkProcessing && work.State != goalAsyncWorkCompleted {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	if work.State == goalAsyncWorkCompleted && workState != goalAsyncWorkCompleted {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	now = normalizeGoalAsyncTime(now)
	if now.IsZero() || now.Before(cp.UpdatedAt) || !now.Before(cp.Deadline) {
		return goalAsyncResumeGrant{}, ErrGoalAsyncResumeDenied
	}
	grant := goalAsyncResumeGrant{
		runID: cp.RunID, goalID: cp.GoalID, goalSpecHash: cp.GoalSpecHash,
		agentID: cp.AgentID, owner: cp.Owner,
		principal:      current.Principal.copyPrincipal(),
		policySHA256:   current.PolicySHA256,
		allowedTools:   append([]string(nil), current.AllowedTools...),
		allowedSources: append([]uint64(nil), current.AllowedSources...),
		operationID:    operationID,
		workState:      workState,
		checkedAt:      now,
	}
	grant.grantSHA256 = goalAsyncResumeGrantHash(grant)
	return grant, nil
}

func goalAsyncResumeGrantHash(g goalAsyncResumeGrant) string {
	payload := struct {
		RunID, GoalID, GoalSpecHash, AgentID, Owner string
		Principal                                   goalAsyncPrincipal
		PolicySHA256                                string
		AllowedTools                                []string
		AllowedSources                              []uint64
		OperationID                                 string
		WorkState                                   goalAsyncWorkState
		CheckedAt                                   time.Time
	}{
		g.runID, g.goalID, g.goalSpecHash, g.agentID, g.owner,
		g.principal, g.policySHA256, sortedUniqueStrings(g.allowedTools),
		sortedUniqueUint64(g.allowedSources), g.operationID, g.workState,
		normalizeGoalAsyncTime(g.checkedAt),
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("crowler-goal-async-resume-v2\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func (g goalAsyncResumeGrant) validate(cp goalAsyncCheckpoint) error {
	if cp.State != goalAsyncAwaitingExternalWork || g.runID != cp.RunID || g.goalID != cp.GoalID ||
		g.goalSpecHash != cp.GoalSpecHash || g.agentID != cp.AgentID || g.owner != cp.Owner {
		return ErrGoalAsyncResumeDenied
	}
	if !g.principal.equal(goalAsyncPrincipal{
		Kind: cp.PrincipalKind, UserID: cp.PrincipalUserID,
		Username: cp.PrincipalUsername, Issuer: cp.PrincipalIssuer,
	}) {
		return ErrGoalAsyncResumeDenied
	}
	if g.policySHA256 != cp.PolicySHA256 ||
		!equalStringSets(g.allowedTools, cp.AllowedTools) ||
		!equalUint64Sets(g.allowedSources, cp.AllowedSources) {
		return ErrGoalAsyncResumeDenied
	}
	if !goalAsyncOperationID.MatchString(g.operationID) ||
		(g.workState != goalAsyncWorkProcessing && g.workState != goalAsyncWorkCompleted) {
		return ErrGoalAsyncResumeDenied
	}
	work, ok := findGoalAsyncWork(cp.Work, g.operationID)
	if !ok || work.Kind != goalAsyncWorkCrawlOperation {
		return ErrGoalAsyncResumeDenied
	}
	if work.State == goalAsyncWorkCompleted && g.workState != goalAsyncWorkCompleted {
		return ErrGoalAsyncResumeDenied
	}
	if !validGoalAsyncTime(g.checkedAt) || g.checkedAt.Before(cp.UpdatedAt) || !g.checkedAt.Before(cp.Deadline) {
		return ErrGoalAsyncResumeDenied
	}
	if g.grantSHA256 != goalAsyncResumeGrantHash(g) {
		return ErrGoalAsyncResumeDenied
	}
	return nil
}

func cloneGoalAsyncTransitionRequest(request goalAsyncTransitionRequest) goalAsyncTransitionRequest {
	out := request
	if request.Used != nil {
		used := *request.Used
		out.Used = &used
	}
	if request.Work != nil {
		work := *request.Work
		out.Work = &work
	}
	if request.Evidence != nil {
		out.Evidence = make([]goalAsyncEvidenceRef, len(request.Evidence))
		for i, ref := range request.Evidence {
			out.Evidence[i] = ref
			out.Evidence[i].SourceIDs = append([]uint64(nil), ref.SourceIDs...)
		}
	}
	if request.AuditRefs != nil {
		out.AuditRefs = append([]string(nil), request.AuditRefs...)
	}
	if request.Verifier != nil {
		verifier := *request.Verifier
		verifier.evidenceIDs = append([]string(nil), request.Verifier.evidenceIDs...)
		out.Verifier = &verifier
	}
	if request.Resume != nil {
		resume := *request.Resume
		resume.allowedTools = append([]string(nil), request.Resume.allowedTools...)
		resume.allowedSources = append([]uint64(nil), request.Resume.allowedSources...)
		out.Resume = &resume
	}
	if request.Scheduling != nil {
		scheduling := *request.Scheduling
		scheduling.work = request.Scheduling.work
		out.Scheduling = &scheduling
	}
	return out
}

// applyGoalAsyncTransition is the pure transition kernel. It never mutates the
// previous snapshot or the request, never performs I/O and never invokes an
// untrusted callback.
func applyGoalAsyncTransition(previous goalAsyncCheckpoint, request goalAsyncTransitionRequest) (goalAsyncCheckpoint, goalAsyncTransitionOutcome, error) {
	if err := validateGoalAsyncCheckpoint(previous); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	request = cloneGoalAsyncTransitionRequest(request)
	if !goalAsyncIDPattern.MatchString(request.OperationID) {
		return goalAsyncCheckpoint{}, "", goalAsyncInvalid("operation_id")
	}
	if !request.Kind.valid() {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	now := normalizeGoalAsyncTime(request.Now)
	if request.OperationID == previous.LastTransitionID {
		if previous.LastTransitionHash != "" && previous.LastTransitionHash == goalAsyncTransitionRequestHash(request) {
			return cloneGoalAsyncCheckpoint(previous), goalAsyncTransitionReplayed, nil
		}
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncReplayConflict
	}
	if now.IsZero() {
		return goalAsyncCheckpoint{}, "", goalAsyncInvalid("now")
	}
	if request.ExpectedRevision != previous.Revision {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncRevisionConflict
	}
	if !goalAsyncTransitionAllowed(previous.State, request.Kind) {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	if now.Before(previous.UpdatedAt) {
		return goalAsyncCheckpoint{}, "", goalAsyncInvalid("now")
	}
	target := request.Kind.target()
	used := previous.Used
	if request.Used != nil {
		used = *request.Used
	}
	if err := used.monotonicFrom(previous.Used); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	if err := used.validateAgainst(previous.Ceilings); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	if target == goalAsyncRunning || target == goalAsyncAwaitingApproval ||
		target == goalAsyncAwaitingExternalWork || target == goalAsyncCriteriaMet ||
		target == goalAsyncIncomplete {
		if !now.Before(previous.Deadline) {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncDeadline
		}
	}
	if target == goalAsyncBudgetExhausted {
		if now.Before(previous.Deadline) && !used.reachesAnyCeiling(previous.Ceilings) &&
			previous.Slices < previous.Ceilings.MaxSlices {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncBudget
		}
	}
	if target == goalAsyncIncomplete &&
		(used.reachesAnyCeiling(previous.Ceilings) || previous.Slices >= previous.Ceilings.MaxSlices) {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncBudget
	}
	slices := previous.Slices
	if request.Kind == goalAsyncTransitionStart || request.Kind == goalAsyncTransitionResume {
		if slices >= previous.Ceilings.MaxSlices {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncBudget
		}
		slices++
	}
	if !goalAsyncReasonPattern.MatchString(request.ReasonCode) && request.Kind != goalAsyncTransitionCriteriaMet {
		return goalAsyncCheckpoint{}, "", goalAsyncInvalid("reason_code")
	}
	if request.Kind != goalAsyncTransitionCriteriaMet && request.Verifier != nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	if request.Kind != goalAsyncTransitionResume && request.Resume != nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	if request.Kind != goalAsyncTransitionAwaitExternalWork && request.Scheduling != nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	if request.Kind == goalAsyncTransitionAwaitExternalWork && request.Scheduling == nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}
	if request.Kind == goalAsyncTransitionCriteriaMet && request.Verifier == nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncVerification
	}
	if request.Kind == goalAsyncTransitionResume && request.Resume == nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncResumeDenied
	}
	if request.Kind != goalAsyncTransitionAwaitApproval &&
		request.Kind != goalAsyncTransitionResume && request.Work != nil {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
	}

	next := cloneGoalAsyncCheckpoint(previous)
	next.State = target
	next.Revision++
	if next.Revision == 0 {
		return goalAsyncCheckpoint{}, "", goalAsyncInvalid("revision")
	}
	next.UpdatedAt = now
	next.Used = used
	next.Slices = slices
	next.ReasonCode = request.ReasonCode

	switch request.Kind {
	case goalAsyncTransitionAwaitApproval:
		if request.Work == nil {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
		}
		if err := appendGoalAsyncWork(&next, previous, *request.Work, used); err != nil {
			return goalAsyncCheckpoint{}, "", err
		}
	case goalAsyncTransitionAwaitExternalWork:
		if request.Scheduling == nil {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncTransitionInvalid
		}
		if err := appendGoalAsyncScheduledWork(&next, previous, *request.Scheduling, used); err != nil {
			return goalAsyncCheckpoint{}, "", err
		}
	case goalAsyncTransitionResume:
		if err := applyGoalAsyncResume(&next, previous, request, used); err != nil {
			return goalAsyncCheckpoint{}, "", err
		}
	case goalAsyncTransitionCriteriaMet:
		if request.Verifier == nil {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncVerification
		}
		if err := request.Verifier.validate(previous); err != nil {
			return goalAsyncCheckpoint{}, "", err
		}
		if request.Verifier.verifiedAt.After(now) {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncVerification
		}
		if request.ReasonCode != "" && request.ReasonCode != request.Verifier.reasonCode {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncVerification
		}
		next.ReasonCode = request.Verifier.reasonCode
		next.Verification = &goalAsyncVerificationRef{
			Verifier:         goalAsyncReasonDeterministicVerifier,
			EvaluationSHA256: request.Verifier.evaluationSHA256,
			VerifiedAt:       request.Verifier.verifiedAt,
			EvidenceIDs:      append([]string(nil), request.Verifier.evidenceIDs...),
		}
	}
	if len(request.Evidence) > 0 {
		if err := appendGoalAsyncEvidence(&next, previous, request.Evidence, used); err != nil {
			return goalAsyncCheckpoint{}, "", err
		}
	}
	if request.Kind == goalAsyncTransitionCriteriaMet {
		if !hasAllGoalAsyncEvidence(next.Evidence, request.Verifier.evidenceIDs) {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncVerification
		}
	}
	if len(request.AuditRefs) > 0 {
		for _, ref := range request.AuditRefs {
			if !goalAsyncIDPattern.MatchString(ref) {
				return goalAsyncCheckpoint{}, "", goalAsyncInvalid("audit_refs")
			}
			next.AuditRefs = append(next.AuditRefs, ref)
		}
		next.AuditRefs = sortedUniqueStrings(next.AuditRefs)
		if len(next.AuditRefs) > maxGoalAsyncAuditRefs {
			return goalAsyncCheckpoint{}, "", goalAsyncInvalid("audit_refs")
		}
	}
	next.LastTransitionID = request.OperationID
	next.LastTransitionHash = goalAsyncTransitionRequestHash(request)
	next = normalizeGoalAsyncCheckpoint(next)
	if err := validateGoalAsyncCheckpoint(next); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	return next, goalAsyncTransitionApplied, nil
}

func appendGoalAsyncWork(next *goalAsyncCheckpoint, previous goalAsyncCheckpoint, work goalAsyncWorkRef, used goalAsyncUsed) error {
	if work.Kind != goalAsyncWorkCrawlProposal || next.State != goalAsyncAwaitingApproval {
		return ErrGoalAsyncTransitionInvalid
	}
	if err := work.validate(next.AllowedSources, next.CreatedAt, next.UpdatedAt); err != nil {
		return err
	}
	for _, existing := range next.Work {
		if existing.sortKey() == work.sortKey() {
			return goalAsyncInvalid("work.duplicate")
		}
	}
	next.Work = append(next.Work, work)
	sort.Slice(next.Work, func(i, j int) bool { return next.Work[i].sortKey() < next.Work[j].sortKey() })
	if len(next.Work) > maxGoalAsyncWorkRefs || len(next.Work) > next.Ceilings.MaxScheduledWorkRequests {
		return goalAsyncInvalid("work")
	}
	if len(next.Work) > len(previous.Work) && used.ScheduledWorkRequests <= previous.Used.ScheduledWorkRequests {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	if used.ScheduledWorkRequests < len(next.Work) {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	return validateGoalAsyncWorkSet(next.Work, next.AllowedSources, next.CreatedAt, next.UpdatedAt, next.Ceilings)
}

func appendGoalAsyncScheduledWork(next *goalAsyncCheckpoint, previous goalAsyncCheckpoint, scheduling goalAsyncSchedulingPrerequisite, used goalAsyncUsed) error {
	if err := scheduling.validate(previous); err != nil {
		return err
	}
	if next.UpdatedAt.Before(scheduling.observedAt) {
		return ErrGoalAsyncTransitionInvalid
	}
	work := scheduling.work
	if previous.State == goalAsyncAwaitingApproval {
		proposalIndex := -1
		for i, existing := range next.Work {
			if existing.Kind == goalAsyncWorkCrawlProposal &&
				existing.RequestID == work.RequestID &&
				existing.ProposalSHA256 == work.ProposalSHA256 &&
				existing.SourceID == work.SourceID {
				proposalIndex = i
				break
			}
		}
		if proposalIndex < 0 {
			return ErrGoalAsyncTransitionInvalid
		}
		next.Work = append(next.Work[:proposalIndex], next.Work[proposalIndex+1:]...)
	}
	for _, existing := range next.Work {
		if existing.sortKey() == work.sortKey() {
			return goalAsyncInvalid("work.duplicate")
		}
	}
	next.Work = append(next.Work, work)
	sort.Slice(next.Work, func(i, j int) bool { return next.Work[i].sortKey() < next.Work[j].sortKey() })
	if len(next.Work) > maxGoalAsyncWorkRefs || len(next.Work) > next.Ceilings.MaxScheduledWorkRequests {
		return goalAsyncInvalid("work")
	}
	if len(next.Work) > len(previous.Work) && used.ScheduledWorkRequests <= previous.Used.ScheduledWorkRequests {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	if used.ScheduledWorkRequests < len(next.Work) {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	return validateGoalAsyncWorkSet(next.Work, next.AllowedSources, next.CreatedAt, next.UpdatedAt, next.Ceilings)
}

func applyGoalAsyncResume(next *goalAsyncCheckpoint, previous goalAsyncCheckpoint, request goalAsyncTransitionRequest, used goalAsyncUsed) error {
	if request.Resume == nil {
		return ErrGoalAsyncResumeDenied
	}
	if err := request.Resume.validate(previous); err != nil {
		return err
	}
	if next.UpdatedAt.Before(request.Resume.checkedAt) {
		return ErrGoalAsyncResumeDenied
	}
	index := -1
	for i, work := range next.Work {
		if work.OperationID == request.Resume.operationID && work.Kind == goalAsyncWorkCrawlOperation {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrGoalAsyncResumeDenied
	}
	switch request.Resume.workState {
	case goalAsyncWorkCompleted:
		if request.Work == nil {
			if next.Work[index].State != goalAsyncWorkCompleted {
				return ErrGoalAsyncResumeDenied
			}
		} else {
			work := *request.Work
			if work.Kind != goalAsyncWorkCrawlOperation || work.State != goalAsyncWorkCompleted ||
				!sameGoalAsyncWorkIdentity(work, next.Work[index]) {
				return ErrGoalAsyncResumeDenied
			}
			if err := work.validate(next.AllowedSources, next.CreatedAt, next.UpdatedAt); err != nil {
				return err
			}
			next.Work[index] = work
		}
	case goalAsyncWorkProcessing:
		if next.Work[index].State == goalAsyncWorkScheduled {
			if request.Work == nil {
				return ErrGoalAsyncResumeDenied
			}
			work := *request.Work
			if work.Kind != goalAsyncWorkCrawlOperation || work.State != goalAsyncWorkProcessing ||
				!sameGoalAsyncWorkIdentity(work, next.Work[index]) {
				return ErrGoalAsyncResumeDenied
			}
			if err := work.validate(next.AllowedSources, next.CreatedAt, next.UpdatedAt); err != nil {
				return err
			}
			next.Work[index] = work
		} else if next.Work[index].State == goalAsyncWorkCompleted {
			return ErrGoalAsyncResumeDenied
		} else if request.Work != nil {
			return ErrGoalAsyncResumeDenied
		}
	default:
		return ErrGoalAsyncResumeDenied
	}
	if used.ScheduledWorkRequests < len(next.Work) {
		return goalAsyncInvalid("used.scheduled_work_requests")
	}
	sort.Slice(next.Work, func(i, j int) bool { return next.Work[i].sortKey() < next.Work[j].sortKey() })
	return validateGoalAsyncWorkSet(next.Work, next.AllowedSources, next.CreatedAt, next.UpdatedAt, next.Ceilings)
}

func sameGoalAsyncWorkIdentity(a, b goalAsyncWorkRef) bool {
	return a.Kind == b.Kind && a.RequestID == b.RequestID &&
		a.ProposalSHA256 == b.ProposalSHA256 && a.ApprovalID == b.ApprovalID &&
		a.ApprovalSHA256 == b.ApprovalSHA256 && a.OperationID == b.OperationID &&
		a.SourceID == b.SourceID &&
		a.ProposalExpiresAt.Equal(b.ProposalExpiresAt) &&
		a.ApprovalExpiresAt.Equal(b.ApprovalExpiresAt) &&
		a.SubmitBy.Equal(b.SubmitBy) && a.AcceptedAt.Equal(b.AcceptedAt)
}

func appendGoalAsyncEvidence(next *goalAsyncCheckpoint, previous goalAsyncCheckpoint, added []goalAsyncEvidenceRef, used goalAsyncUsed) error {
	if len(next.Evidence)+len(added) > maxGoalAsyncEvidenceRefs ||
		len(next.Evidence)+len(added) > next.Ceilings.MaxEvidenceRecords {
		return goalAsyncInvalid("evidence")
	}
	seen := map[string]goalAsyncEvidenceRef{}
	for _, ref := range next.Evidence {
		seen[ref.EvidenceID] = ref
	}
	addedBytes := 0
	for _, ref := range added {
		if err := ref.validate(next.AllowedSources, next.CreatedAt, next.UpdatedAt); err != nil {
			return err
		}
		if existing, ok := seen[ref.EvidenceID]; ok {
			if existing.RecordSHA256 != ref.RecordSHA256 {
				return goalAsyncInvalid("evidence.identity_conflict")
			}
			return goalAsyncInvalid("evidence.duplicate")
		}
		seen[ref.EvidenceID] = ref
		next.Evidence = append(next.Evidence, ref)
		addedBytes += ref.SizeBytes
	}
	sort.Slice(next.Evidence, func(i, j int) bool { return next.Evidence[i].EvidenceID < next.Evidence[j].EvidenceID })
	if len(added) > 0 && used.EvidenceRecords <= previous.Used.EvidenceRecords {
		return goalAsyncInvalid("used.evidence_records")
	}
	if used.EvidenceRecords < len(next.Evidence) {
		return goalAsyncInvalid("used.evidence_records")
	}
	if addedBytes > 0 && used.EvidenceBytes < previous.Used.EvidenceBytes+addedBytes {
		return goalAsyncInvalid("used.evidence_bytes")
	}
	return validateGoalAsyncEvidenceSet(next.Evidence, next.AllowedSources, next.CreatedAt, next.UpdatedAt)
}

func goalAsyncTransitionRequestHash(request goalAsyncTransitionRequest) string {
	payload := struct {
		OperationID   string
		Kind          goalAsyncTransitionKind
		ReasonCode    string
		Used          *goalAsyncUsed
		Work          *goalAsyncWorkRef
		Evidence      []goalAsyncEvidenceRef
		AuditRefs     []string
		VerifierSHA   string
		ResumeSHA     string
		SchedulingSHA string
	}{
		OperationID: request.OperationID,
		Kind:        request.Kind,
		ReasonCode:  request.ReasonCode,
		Used:        request.Used,
		Work:        request.Work,
		Evidence:    append([]goalAsyncEvidenceRef(nil), request.Evidence...),
		AuditRefs:   sortedUniqueStrings(request.AuditRefs),
	}
	if request.Verifier != nil {
		payload.VerifierSHA = request.Verifier.decisionSHA256
	}
	if request.Resume != nil {
		payload.ResumeSHA = request.Resume.grantSHA256
	}
	if request.Scheduling != nil {
		payload.SchedulingSHA = request.Scheduling.prerequisiteSHA256
	}
	sort.Slice(payload.Evidence, func(i, j int) bool { return payload.Evidence[i].EvidenceID < payload.Evidence[j].EvidenceID })
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("crowler-goal-async-transition-v2\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func marshalGoalAsyncCheckpoint(cp goalAsyncCheckpoint) ([]byte, error) {
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return nil, err
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed checkpoint", ErrGoalAsyncSerialization)
	}
	if len(data) > maxGoalAsyncCheckpointBytes {
		return nil, fmt.Errorf("%w: checkpoint too large", ErrGoalAsyncSerialization)
	}
	return data, nil
}

func validateGoalAsyncJSONShape(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanGoalAsyncJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing checkpoint data", ErrGoalAsyncSerialization)
	}
	return nil
}

func scanGoalAsyncJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxGoalAsyncJSONDepth {
		return errors.New("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[key] {
				return errGoalAsyncJSONDuplicateKey
			}
			seen[key] = true
			if err := scanGoalAsyncJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanGoalAsyncJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func parseGoalAsyncCheckpoint(data []byte) (goalAsyncCheckpoint, error) {
	if len(data) == 0 || len(data) > maxGoalAsyncCheckpointBytes {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: checkpoint size", ErrGoalAsyncSerialization)
	}
	if !utf8.Valid(data) {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: malformed checkpoint", ErrGoalAsyncSerialization)
	}
	if err := validateGoalAsyncJSONShape(data); err != nil {
		if errors.Is(err, errGoalAsyncJSONDuplicateKey) {
			return goalAsyncCheckpoint{}, fmt.Errorf("%w: duplicate JSON key", ErrGoalAsyncSerialization)
		}
		if errors.Is(err, ErrGoalAsyncSerialization) {
			return goalAsyncCheckpoint{}, err
		}
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: malformed checkpoint", ErrGoalAsyncSerialization)
	}
	var cp goalAsyncCheckpoint
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cp); err != nil {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: malformed checkpoint", ErrGoalAsyncSerialization)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: trailing checkpoint data", ErrGoalAsyncSerialization)
	}
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	return cp, nil
}

func goalAsyncCheckpointDigest(cp goalAsyncCheckpoint) (string, error) {
	data, err := marshalGoalAsyncCheckpoint(cp)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("crowler-goal-async-checkpoint-v2\x00"), data...))
	return hex.EncodeToString(sum[:]), nil
}

func hasAllGoalAsyncEvidence(evidence []goalAsyncEvidenceRef, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, ref := range evidence {
		seen[ref.EvidenceID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			return false
		}
	}
	return true
}

func hasGoalAsyncWorkState(work []goalAsyncWorkRef, kind goalAsyncWorkKind, states ...goalAsyncWorkState) bool {
	for _, ref := range work {
		if ref.Kind != kind {
			continue
		}
		for _, state := range states {
			if ref.State == state {
				return true
			}
		}
	}
	return false
}

func hasActiveGoalAsyncWork(work []goalAsyncWorkRef) bool {
	for _, ref := range work {
		if ref.Kind == goalAsyncWorkCrawlProposal && ref.State == goalAsyncWorkAwaitingApproval {
			return true
		}
		if ref.Kind == goalAsyncWorkCrawlOperation &&
			(ref.State == goalAsyncWorkScheduled || ref.State == goalAsyncWorkProcessing) {
			return true
		}
	}
	return false
}

func findGoalAsyncProposal(work []goalAsyncWorkRef, requestID, proposalSHA256 string, sourceID uint64) (goalAsyncWorkRef, bool) {
	for _, ref := range work {
		if ref.Kind == goalAsyncWorkCrawlProposal &&
			ref.State == goalAsyncWorkAwaitingApproval &&
			ref.RequestID == requestID &&
			ref.ProposalSHA256 == proposalSHA256 &&
			ref.SourceID == sourceID {
			return ref, true
		}
	}
	return goalAsyncWorkRef{}, false
}

func findGoalAsyncWork(work []goalAsyncWorkRef, operationID string) (goalAsyncWorkRef, bool) {
	for _, ref := range work {
		if ref.OperationID == operationID && ref.Kind == goalAsyncWorkCrawlOperation {
			return ref, true
		}
	}
	return goalAsyncWorkRef{}, false
}

func equalStringSets(a, b []string) bool {
	left := sortedUniqueStrings(a)
	right := sortedUniqueStrings(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalUint64Sets(a, b []uint64) bool {
	left := sortedUniqueUint64(a)
	right := sortedUniqueUint64(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsAllUint64(haystack, needles []uint64) bool {
	values := map[uint64]bool{}
	for _, value := range haystack {
		values[value] = true
	}
	for _, value := range needles {
		if !values[value] {
			return false
		}
	}
	return true
}

func isSortedUniqueString(values []string) bool {
	for i, value := range values {
		if i > 0 && values[i-1] >= value {
			return false
		}
	}
	return true
}

func isSortedUniqueUint64(values []uint64) bool {
	for i, value := range values {
		if i > 0 && values[i-1] >= value {
			return false
		}
	}
	return true
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedUniqueUint64(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	seen := map[uint64]bool{}
	out := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func normalizeGoalAsyncTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Microsecond)
}

func validGoalAsyncTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Nanosecond()%1000 == 0
}

func goalAsyncInvalid(field string) error {
	return fmt.Errorf("%w: %s", ErrGoalAsyncCheckpointInvalid, field)
}
