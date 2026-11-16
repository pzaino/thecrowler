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

// Read-only correlation of one specific durable crawl scheduling operation.
//
// This service never resumes a goal, issues a resume grant, schedules a crawl,
// calls ReconcileCrawlProposalOperation, or executes a model/tool/MCP action.
// The result describes the authoritative operation record and its uncertainty.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const maxGoalAsyncOperationDetailsBytes = 64 << 10

type goalAsyncObservationClass string

const (
	goalAsyncObservationNotFound           goalAsyncObservationClass = "not_found"
	goalAsyncObservationPending            goalAsyncObservationClass = "pending"
	goalAsyncObservationProcessing         goalAsyncObservationClass = "processing"
	goalAsyncObservationReportedCompleted  goalAsyncObservationClass = "reported_completed"
	goalAsyncObservationTerminalNonSuccess goalAsyncObservationClass = "terminal_non_success"
	goalAsyncObservationUncertain          goalAsyncObservationClass = "uncertain"
	goalAsyncObservationConflict           goalAsyncObservationClass = "conflict"
	goalAsyncObservationUnavailable        goalAsyncObservationClass = "unavailable"
)

const (
	goalAsyncObservationReasonProposalMissing      = "goal_observation_proposal_missing"
	goalAsyncObservationReasonProposalUnsubmitted  = "goal_observation_proposal_unsubmitted"
	goalAsyncObservationReasonOperationMissing     = "goal_observation_operation_missing"
	goalAsyncObservationReasonApprovalMissing      = "goal_observation_approval_missing"
	goalAsyncObservationReasonMetadataInvalid      = "goal_observation_metadata_invalid"
	goalAsyncObservationReasonIdentityConflict     = "goal_observation_identity_conflict"
	goalAsyncObservationReasonOperationDeleted     = "goal_observation_operation_deleted"
	goalAsyncObservationReasonApprovalBeforeAccept = "goal_observation_approval_revoked_before_acceptance"
	goalAsyncObservationReasonInvalidState         = "goal_observation_state_invalid"
)

var (
	errGoalAsyncObservationUnavailable = errors.New("goal async crawl observation is unavailable")
	errGoalAsyncObservationConflict    = errors.New("goal async crawl observation conflicts with stored identity")
	errGoalAsyncObservationUncertain   = errors.New("goal async crawl observation is uncertain")
)

// goalAsyncCrawlObservation is a bounded, sanitized description of one
// operation. OperationState preserves the exact authoritative Status value;
// Class is the stable observation classification.
type goalAsyncCrawlObservation struct {
	Class              goalAsyncObservationClass
	OperationID        string
	RequestID          string
	SourceID           uint64
	ProposalState      string
	ApprovalState      string
	OperationState     string
	ReasonCode         string
	AcceptedAt         time.Time
	OperationUpdatedAt time.Time
}

type goalAsyncOperationDetails struct {
	RequestID         string `json:"request_id"`
	RunID             string `json:"run_id"`
	PrincipalUserID   uint64 `json:"principal_user_id"`
	AgentID           string `json:"agent_id"`
	Owner             string `json:"owner"`
	SourceID          uint64 `json:"source_id"`
	OperationType     string `json:"operation_type"`
	ParametersSHA256  string `json:"parameters_sha256"`
	ApprovalID        string `json:"approval_id,omitempty"`
	ApprovalSHA256    string `json:"approval_sha256"`
	ApprovedBy        string `json:"approved_by,omitempty"`
	ApprovedByUserID  uint64 `json:"approved_by_user_id,omitempty"`
	ApprovedAt        string `json:"approved_at,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	SourceStateBefore string `json:"source_state_before,omitempty"`
	ReasonCode        string `json:"reason_code,omitempty"`
}

type goalAsyncStatusOperation struct {
	StatusKey     string
	AgentID       sql.NullString
	SourceID      sql.NullInt64
	OperationType sql.NullString
	Status        string
	Details       []byte
	CreatedAt     time.Time
	LastUpdatedAt time.Time
	DeletedAt     sql.NullTime
}

// ObserveCrawlOperation reads the authoritative proposal, approval and Status
// records for one operation referenced by cp. It does not write or execute.
func (s *goalAsyncStore) ObserveCrawlOperation(ctx context.Context, cp goalAsyncCheckpoint, operationID string) (goalAsyncCrawlObservation, error) {
	return s.observeCrawlOperationAt(ctx, cp, operationID, time.Now().UTC())
}

func (s *goalAsyncStore) observeCrawlOperationAt(ctx context.Context, cp goalAsyncCheckpoint, operationID string, now time.Time) (goalAsyncCrawlObservation, error) {
	result := goalAsyncCrawlObservation{OperationID: operationID}
	if s == nil || s.db == nil {
		return result, errGoalAsyncObservationUnavailable
	}
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		result.Class = goalAsyncObservationConflict
		return result, fmt.Errorf("%w: checkpoint", errGoalAsyncObservationConflict)
	}
	work, ok := findGoalAsyncWork(cp.Work, operationID)
	if !ok {
		result.Class = goalAsyncObservationConflict
		return result, fmt.Errorf("%w: operation reference", errGoalAsyncObservationConflict)
	}
	result.RequestID = work.RequestID
	result.SourceID = work.SourceID
	if !containsUint64(cp.AllowedSources, work.SourceID) {
		result.Class = goalAsyncObservationConflict
		return result, fmt.Errorf("%w: source scope", errGoalAsyncObservationConflict)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = normalizeGoalAsyncTime(now)

	txCtx, cancel := cdb.TransactionContextForHandler(ctx, s.db)
	defer cancel()
	opts := &sql.TxOptions{ReadOnly: true}
	if s.postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := (*s.db).BeginTx(txCtx, opts)
	if err != nil {
		result.Class = goalAsyncObservationUnavailable
		return result, fmt.Errorf("%w: begin observation", errGoalAsyncObservationUnavailable)
	}
	defer func() { _ = tx.Rollback() }()

	proposal, proposalFound, err := s.loadCrawlProposalTx(txCtx, tx, work.RequestID)
	if err != nil {
		result.Class = goalAsyncObservationUnavailable
		return result, fmt.Errorf("%w: proposal", errGoalAsyncObservationUnavailable)
	}
	approval, approvalFound, err := s.loadCrawlApprovalTx(txCtx, tx, work.ApprovalID)
	if err != nil {
		result.Class = goalAsyncObservationUnavailable
		return result, fmt.Errorf("%w: approval", errGoalAsyncObservationUnavailable)
	}
	status, statusFound, err := s.loadCrawlStatusOperationTx(txCtx, tx, operationID)
	if err != nil {
		result.Class = goalAsyncObservationUnavailable
		return result, fmt.Errorf("%w: operation", errGoalAsyncObservationUnavailable)
	}

	if !proposalFound {
		if approvalFound || statusFound {
			result.Class = goalAsyncObservationConflict
			result.ReasonCode = goalAsyncObservationReasonProposalMissing
			return result, fmt.Errorf("%w: proposal missing for existing operation rows", errGoalAsyncObservationConflict)
		}
		result.Class = goalAsyncObservationNotFound
		result.ReasonCode = goalAsyncObservationReasonProposalMissing
		return result, nil
	}
	result.ProposalState = proposal.State

	if conflict := goalAsyncObservationProposalIdentityMismatch(cp, work, proposal); conflict {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: proposal identity", errGoalAsyncObservationConflict)
	}
	if !proposal.OperationID.Valid {
		if statusFound || approvalFound {
			result.Class = goalAsyncObservationConflict
			result.ReasonCode = goalAsyncObservationReasonProposalUnsubmitted
			return result, fmt.Errorf("%w: unsubmitted proposal has operation or approval rows", errGoalAsyncObservationConflict)
		}
		result.Class = goalAsyncObservationNotFound
		result.ReasonCode = goalAsyncObservationReasonProposalUnsubmitted
		return result, nil
	}
	if proposal.OperationID.String != operationID {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: proposal operation identity", errGoalAsyncObservationConflict)
	}
	if proposal.State != cdb.CrawlProposalStatePending &&
		proposal.State != cdb.CrawlProposalStateSubmitted &&
		proposal.State != cdb.CrawlProposalStateExpired {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: proposal lifecycle state", errGoalAsyncObservationConflict)
	}
	if proposal.State == cdb.CrawlProposalStateSubmitted && !proposal.SubmittedAt.Valid {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: submitted proposal lacks submission time", errGoalAsyncObservationConflict)
	}
	if !work.AcceptedAt.Before(proposal.ExpiresAt) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: proposal expiry precedes acceptance", errGoalAsyncObservationConflict)
	}

	if !approvalFound {
		result.Class = goalAsyncObservationUncertain
		result.ApprovalState = "unknown"
		result.ReasonCode = goalAsyncObservationReasonApprovalMissing
		return result, fmt.Errorf("%w: approval missing", errGoalAsyncObservationUncertain)
	}
	result.ApprovalState = goalAsyncObservationApprovalState(approval, now)
	if conflict := goalAsyncObservationApprovalIdentityMismatch(cp, work, proposal, approval); conflict {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: approval identity", errGoalAsyncObservationConflict)
	}
	if !work.AcceptedAt.Before(approval.ExpiresAt) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: approval expiry precedes acceptance", errGoalAsyncObservationConflict)
	}
	if approval.RevokedAt.Valid && !approval.RevokedAt.Time.After(work.AcceptedAt) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonApprovalBeforeAccept
		return result, fmt.Errorf("%w: approval was revoked before acceptance", errGoalAsyncObservationConflict)
	}
	if !approval.ProposalExpiresAt.Equal(proposal.ExpiresAt) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: approval proposal expiry", errGoalAsyncObservationConflict)
	}

	if !statusFound {
		result.Class = goalAsyncObservationUncertain
		result.ReasonCode = goalAsyncObservationReasonOperationMissing
		return result, fmt.Errorf("%w: operation Status row missing", errGoalAsyncObservationUncertain)
	}
	result.OperationState = status.Status
	result.AcceptedAt = normalizeGoalAsyncTime(status.CreatedAt)
	result.OperationUpdatedAt = normalizeGoalAsyncTime(status.LastUpdatedAt)
	if status.DeletedAt.Valid {
		result.Class = goalAsyncObservationUncertain
		result.ReasonCode = goalAsyncObservationReasonOperationDeleted
		return result, fmt.Errorf("%w: operation Status row is deleted", errGoalAsyncObservationUncertain)
	}
	if status.StatusKey != operationID ||
		!status.AgentID.Valid || status.AgentID.String != cp.AgentID ||
		!status.SourceID.Valid || status.SourceID.Int64 <= 0 ||
		uint64(status.SourceID.Int64) != work.SourceID ||
		!status.OperationType.Valid || status.OperationType.String != cdb.CrawlOperationType {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: Status columns", errGoalAsyncObservationConflict)
	}
	if !status.CreatedAt.Equal(work.AcceptedAt) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: Status acceptance time", errGoalAsyncObservationConflict)
	}

	details, err := decodeGoalAsyncOperationDetails(status.Details)
	if err != nil {
		result.Class = goalAsyncObservationUncertain
		result.ReasonCode = goalAsyncObservationReasonMetadataInvalid
		return result, fmt.Errorf("%w: operation details", errGoalAsyncObservationUncertain)
	}
	detailsApprovedAt, detailsExpiresAt, err := goalAsyncObservationDetailsMatches(cp, work, proposal, approval, details)
	if err != nil {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: operation details identity", errGoalAsyncObservationConflict)
	}
	if !detailsApprovedAt.Equal(approval.ApprovedAt) || !detailsExpiresAt.Equal(work.SubmitBy) {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: operation details timestamps", errGoalAsyncObservationConflict)
	}
	if expectedKey := cdb.CrawlOperationKey(cp.Owner, cp.AgentID, work.SourceID, cdb.CrawlOperationType, work.RequestID); expectedKey != operationID {
		result.Class = goalAsyncObservationConflict
		result.ReasonCode = goalAsyncObservationReasonIdentityConflict
		return result, fmt.Errorf("%w: deterministic operation key", errGoalAsyncObservationConflict)
	}
	if details.ReasonCode != "" {
		result.ReasonCode = details.ReasonCode
	}

	class, recognized := goalAsyncObservationClassForState(status.Status)
	if !recognized {
		result.Class = goalAsyncObservationUncertain
		result.ReasonCode = goalAsyncObservationReasonInvalidState
		return result, fmt.Errorf("%w: unrecognized operation state", errGoalAsyncObservationUncertain)
	}
	result.Class = class
	switch class {
	case goalAsyncObservationConflict:
		return result, fmt.Errorf("%w: operation state", errGoalAsyncObservationConflict)
	case goalAsyncObservationUncertain:
		return result, fmt.Errorf("%w: operation state is uncertain", errGoalAsyncObservationUncertain)
	}
	if err := tx.Commit(); err != nil {
		result.Class = goalAsyncObservationUnavailable
		return result, fmt.Errorf("%w: commit observation", errGoalAsyncObservationUnavailable)
	}
	return result, nil
}

func (s *goalAsyncStore) loadCrawlProposalTx(ctx context.Context, q goalAsyncStoreQueryer, requestID string) (cdb.CrawlProposalRow, bool, error) {
	query := `SELECT request_id, run_id, tool_call_id, agent_id, owner,
		principal_user_id, source_id, operation_type, proposal_sha256, trace_id,
		max_requests, budget_window_seconds, policy_json, state, expires_at,
		submitted_at, operation_id, created_at, last_updated_at
		FROM CrawlProposals WHERE request_id = ` + s.placeholder(1)
	var row cdb.CrawlProposalRow
	err := q.QueryRowContext(ctx, query, requestID).Scan(
		&row.RequestID, &row.RunID, &row.ToolCallID, &row.AgentID, &row.Owner,
		&row.PrincipalUserID, &row.SourceID, &row.OperationType, &row.ProposalSHA256,
		&row.TraceID, &row.MaxRequests, &row.BudgetWindowSeconds, &row.PolicyJSON,
		&row.State, &row.ExpiresAt, &row.SubmittedAt, &row.OperationID,
		&row.CreatedAt, &row.LastUpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cdb.CrawlProposalRow{}, false, nil
	}
	if err != nil {
		return cdb.CrawlProposalRow{}, false, err
	}
	row.ExpiresAt = normalizeGoalAsyncTime(row.ExpiresAt)
	row.CreatedAt = normalizeGoalAsyncTime(row.CreatedAt)
	row.LastUpdatedAt = normalizeGoalAsyncTime(row.LastUpdatedAt)
	if row.SubmittedAt.Valid {
		row.SubmittedAt.Time = normalizeGoalAsyncTime(row.SubmittedAt.Time)
	}
	return row, true, nil
}

func (s *goalAsyncStore) loadCrawlApprovalTx(ctx context.Context, q goalAsyncStoreQueryer, approvalID string) (cdb.CrawlApprovalRow, bool, error) {
	if strings.TrimSpace(approvalID) == "" {
		return cdb.CrawlApprovalRow{}, false, nil
	}
	query := `SELECT approval_id, proposal_sha256, request_id, run_id, agent_id,
		owner, principal_user_id, source_id, operation_type, issuer,
		issued_by_user_id, issued_by, issued_by_issuer, approved_by_user_id,
		approved_by, approved_by_issuer, approval_sha256, proposal_expires_at,
		approved_at, expires_at, revoked_at, revoked_by_user_id, revoked_by,
		revocation_reason, created_at, last_updated_at
		FROM CrawlApprovals WHERE approval_id = ` + s.placeholder(1)
	var row cdb.CrawlApprovalRow
	err := q.QueryRowContext(ctx, query, approvalID).Scan(
		&row.ApprovalID, &row.ProposalSHA256, &row.RequestID, &row.RunID,
		&row.AgentID, &row.Owner, &row.PrincipalUserID, &row.SourceID,
		&row.OperationType, &row.Issuer, &row.IssuedByUserID, &row.IssuedBy,
		&row.IssuedByIssuer, &row.ApprovedByUserID, &row.ApprovedBy,
		&row.ApprovedByIssuer, &row.ApprovalSHA256, &row.ProposalExpiresAt,
		&row.ApprovedAt, &row.ExpiresAt, &row.RevokedAt, &row.RevokedByUserID,
		&row.RevokedBy, &row.RevocationReason, &row.CreatedAt, &row.LastUpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cdb.CrawlApprovalRow{}, false, nil
	}
	if err != nil {
		return cdb.CrawlApprovalRow{}, false, err
	}
	row.ProposalExpiresAt = normalizeGoalAsyncTime(row.ProposalExpiresAt)
	row.ApprovedAt = normalizeGoalAsyncTime(row.ApprovedAt)
	row.ExpiresAt = normalizeGoalAsyncTime(row.ExpiresAt)
	row.CreatedAt = normalizeGoalAsyncTime(row.CreatedAt)
	row.LastUpdatedAt = normalizeGoalAsyncTime(row.LastUpdatedAt)
	if row.RevokedAt.Valid {
		row.RevokedAt.Time = normalizeGoalAsyncTime(row.RevokedAt.Time)
	}
	return row, true, nil
}

func (s *goalAsyncStore) loadCrawlStatusOperationTx(ctx context.Context, q goalAsyncStoreQueryer, operationID string) (goalAsyncStatusOperation, bool, error) {
	query := `SELECT status_sha256, agent_id, source_id, operation_type, status,
		details, created_at, last_updated_at, deleted_at
		FROM Status WHERE status_sha256 = ` + s.placeholder(1)
	var row goalAsyncStatusOperation
	err := q.QueryRowContext(ctx, query, operationID).Scan(
		&row.StatusKey, &row.AgentID, &row.SourceID, &row.OperationType,
		&row.Status, &row.Details, &row.CreatedAt, &row.LastUpdatedAt, &row.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return goalAsyncStatusOperation{}, false, nil
	}
	if err != nil {
		return goalAsyncStatusOperation{}, false, err
	}
	row.CreatedAt = normalizeGoalAsyncTime(row.CreatedAt)
	row.LastUpdatedAt = normalizeGoalAsyncTime(row.LastUpdatedAt)
	if row.DeletedAt.Valid {
		row.DeletedAt.Time = normalizeGoalAsyncTime(row.DeletedAt.Time)
	}
	return row, true, nil
}

func decodeGoalAsyncOperationDetails(raw []byte) (goalAsyncOperationDetails, error) {
	if len(raw) == 0 || len(raw) > maxGoalAsyncOperationDetailsBytes {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	if err := validateGoalAsyncJSONShape(raw); err != nil {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var details goalAsyncOperationDetails
	if err := decoder.Decode(&details); err != nil {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	if strings.TrimSpace(details.RequestID) == "" ||
		strings.TrimSpace(details.RunID) == "" ||
		strings.TrimSpace(details.AgentID) == "" ||
		strings.TrimSpace(details.Owner) == "" ||
		strings.TrimSpace(details.OperationType) == "" ||
		details.PrincipalUserID == 0 || details.SourceID == 0 ||
		!goalAsyncOperationID.MatchString(details.RequestID) ||
		!goalAsyncHashPattern.MatchString(details.ParametersSHA256) ||
		!goalAsyncOperationID.MatchString(details.ApprovalID) ||
		!goalAsyncHashPattern.MatchString(details.ApprovalSHA256) ||
		strings.TrimSpace(details.ApprovedBy) == "" || details.ApprovedByUserID == 0 ||
		strings.TrimSpace(details.ApprovedAt) == "" ||
		strings.TrimSpace(details.ExpiresAt) == "" ||
		details.OperationType != cdb.CrawlOperationType {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	if details.ReasonCode != "" && !goalAsyncReasonPattern.MatchString(details.ReasonCode) {
		return goalAsyncOperationDetails{}, errGoalAsyncObservationUncertain
	}
	return details, nil
}

func goalAsyncObservationProposalIdentityMismatch(cp goalAsyncCheckpoint, work goalAsyncWorkRef, proposal cdb.CrawlProposalRow) bool {
	return proposal.RequestID != work.RequestID ||
		proposal.RunID != cp.RunID ||
		proposal.AgentID != cp.AgentID ||
		proposal.Owner != cp.Owner ||
		proposal.PrincipalUserID != cp.PrincipalUserID ||
		proposal.SourceID != work.SourceID ||
		proposal.OperationType != cdb.CrawlOperationType ||
		proposal.ProposalSHA256 != work.ProposalSHA256 ||
		!proposal.ExpiresAt.Equal(work.ProposalExpiresAt)
}

func goalAsyncObservationApprovalIdentityMismatch(cp goalAsyncCheckpoint, work goalAsyncWorkRef, proposal cdb.CrawlProposalRow, approval cdb.CrawlApprovalRow) bool {
	return approval.ApprovalID != work.ApprovalID ||
		approval.ApprovalSHA256 != work.ApprovalSHA256 ||
		approval.ProposalSHA256 != work.ProposalSHA256 ||
		approval.RequestID != work.RequestID ||
		approval.RunID != cp.RunID ||
		approval.AgentID != cp.AgentID ||
		approval.Owner != cp.Owner ||
		approval.PrincipalUserID != cp.PrincipalUserID ||
		approval.SourceID != work.SourceID ||
		approval.OperationType != cdb.CrawlOperationType ||
		!approval.ExpiresAt.Equal(work.ApprovalExpiresAt)
}

func goalAsyncObservationDetailsMatches(cp goalAsyncCheckpoint, work goalAsyncWorkRef, proposal cdb.CrawlProposalRow, approval cdb.CrawlApprovalRow, details goalAsyncOperationDetails) (time.Time, time.Time, error) {
	if details.RequestID != work.RequestID ||
		details.RunID != cp.RunID ||
		details.AgentID != cp.AgentID ||
		details.Owner != cp.Owner ||
		details.PrincipalUserID != cp.PrincipalUserID ||
		details.SourceID != work.SourceID ||
		details.OperationType != cdb.CrawlOperationType ||
		details.ParametersSHA256 != work.ProposalSHA256 ||
		details.ParametersSHA256 != proposal.ProposalSHA256 ||
		details.ApprovalID != work.ApprovalID ||
		details.ApprovalSHA256 != work.ApprovalSHA256 ||
		details.ApprovedBy != approval.ApprovedBy ||
		details.ApprovedByUserID != approval.ApprovedByUserID {
		return time.Time{}, time.Time{}, errGoalAsyncObservationConflict
	}
	approvedAt, err := time.Parse(time.RFC3339Nano, details.ApprovedAt)
	if err != nil || approvedAt.IsZero() {
		return time.Time{}, time.Time{}, errGoalAsyncObservationConflict
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, details.ExpiresAt)
	if err != nil || expiresAt.IsZero() {
		return time.Time{}, time.Time{}, errGoalAsyncObservationConflict
	}
	return normalizeGoalAsyncTime(approvedAt), normalizeGoalAsyncTime(expiresAt), nil
}

func goalAsyncObservationApprovalState(approval cdb.CrawlApprovalRow, now time.Time) string {
	switch {
	case approval.RevokedAt.Valid:
		return "revoked"
	case !approval.ExpiresAt.After(now):
		return "expired"
	default:
		return "active"
	}
}

func goalAsyncObservationClassForState(state string) (goalAsyncObservationClass, bool) {
	switch cdb.CrawlOperationState(state) {
	case cdb.CrawlOperationAccepted, cdb.CrawlOperationAlreadyPending:
		return goalAsyncObservationPending, true
	case cdb.CrawlOperationProcessing:
		return goalAsyncObservationProcessing, true
	case cdb.CrawlOperationCompleted:
		return goalAsyncObservationReportedCompleted, true
	case cdb.CrawlOperationDenied, cdb.CrawlOperationFailed,
		cdb.CrawlOperationCancelled, cdb.CrawlOperationExpired,
		cdb.CrawlOperationDisabled, cdb.CrawlOperationNotFound,
		cdb.CrawlOperationUnsupported:
		return goalAsyncObservationTerminalNonSuccess, true
	case cdb.CrawlOperationConflict:
		return goalAsyncObservationConflict, true
	case cdb.CrawlOperationUncertain:
		return goalAsyncObservationUncertain, true
	default:
		return goalAsyncObservationUncertain, false
	}
}
