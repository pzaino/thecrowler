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

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	auditOutcomeAllowed = "allowed"
	auditOutcomeDenied  = "denied"
	auditOutcomeError   = "error"
)

// AuditEvent captures deterministic execution and governance telemetry.
type AuditEvent struct {
	Sequence           int      `json:"sequence"`
	RunID              string   `json:"run_id,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
	AgentID            string   `json:"agent_id,omitempty"`
	AgentName          string   `json:"agent_name,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	Action             string   `json:"action,omitempty"`
	RequiredCapability string   `json:"required_capability,omitempty"`
	CapabilitiesUsed   []string `json:"capabilities_used,omitempty"`
	Outcome            string   `json:"outcome"`
	Reason             string   `json:"reason,omitempty"`
	DelegationTarget   string   `json:"delegation_target,omitempty"`
	FailurePolicy      string   `json:"failure_policy,omitempty"`
	// WP-02 goal correlation fields. Empty for legacy actions.
	GoalID      string   `json:"goal_id,omitempty"`
	CriterionID string   `json:"criterion_id,omitempty"`
	ReasonCode  string   `json:"reason_code,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// WP-03B1 scheduling correlation fields. Empty for non-scheduling actions.
	SourceID                uint64     `json:"source_id,omitempty"`
	RequestID               string     `json:"request_id,omitempty"`
	OperationType           string     `json:"operation_type,omitempty"`
	IdempotencyKey          string     `json:"idempotency_key,omitempty"`
	AuthorizationOutcome    string     `json:"authorization_outcome,omitempty"`
	AuthorizationReasonCode string     `json:"authorization_reason_code,omitempty"`
	SchedulingState         string     `json:"scheduling_state,omitempty"`
	Timestamp               *time.Time `json:"timestamp,omitempty"`
}

func (je *JobEngine) startGovernanceRun() {
	if je == nil {
		return
	}
	je.auditMu.Lock()
	defer je.auditMu.Unlock()
	je.auditEvents = nil
	je.traceLogs = nil
	je.auditSeq = 0
}

func (je *JobEngine) appendAudit(event AuditEvent) {
	if je == nil {
		return
	}
	je.auditMu.Lock()
	defer je.auditMu.Unlock()
	je.auditSeq++
	event.Sequence = je.auditSeq
	je.auditEvents = append(je.auditEvents, event)
	trace := fmt.Sprintf("%04d|outcome=%s|agent=%s|action=%s|reason=%s|target=%s|policy=%s", event.Sequence, event.Outcome, event.AgentID, event.Action, event.Reason, event.DelegationTarget, event.FailurePolicy)
	je.traceLogs = append(je.traceLogs, trace)
}

// AuditCrawlSchedulingDecision records one crawl proposal/scheduling decision
// through the existing governance audit. Reason values are stable codes; model
// text and database details are never copied into the audit.
func (je *JobEngine) AuditCrawlSchedulingDecision(identity AgentIdentity, proposal CrawlRequestProposal, decision CrawlRequestDecision, result *cdb.CrawlScheduleResult, runID, traceID string) {
	outcome := auditOutcomeDenied
	reason := decision.ReasonCode
	operationType := proposal.OperationType
	requestID := proposal.RequestID
	sourceID := proposal.SourceID
	operationID := ""
	if decision.Allowed {
		outcome = auditOutcomeAllowed
	}
	if result != nil {
		reason = result.ReasonCode
		operationType = result.OperationType
		requestID = result.RequestID
		sourceID = result.SourceID
		operationID = result.OperationID
		switch result.State {
		case cdb.CrawlOperationAccepted, cdb.CrawlOperationAlreadyPending, cdb.CrawlOperationProcessing, cdb.CrawlOperationCompleted:
			outcome = auditOutcomeAllowed
		case cdb.CrawlOperationFailed, cdb.CrawlOperationCancelled, cdb.CrawlOperationUnsupported:
			outcome = auditOutcomeError
		default:
			outcome = auditOutcomeDenied
		}
	}
	capabilities := []string(nil)
	authorizationOutcome := auditOutcomeDenied
	if decision.Allowed {
		capabilities = append([]string(nil), decision.RequiredCapabilities...)
		authorizationOutcome = auditOutcomeAllowed
	}
	authorizationReason := decision.ReasonCode
	schedulingState := ""
	if result != nil {
		schedulingState = string(result.State)
	}
	timestamp := time.Now().UTC()
	je.appendAudit(AuditEvent{
		RunID: runID, TraceID: traceID,
		AgentID: identity.AgentID, AgentName: identity.Name, Owner: identity.Owner,
		Action: "CrawlRequest", RequiredCapability: "tool_execution+db_write",
		CapabilitiesUsed: capabilities, Outcome: outcome, Reason: reason,
		ReasonCode: reason, SourceID: sourceID, RequestID: requestID,
		OperationType: operationType, IdempotencyKey: operationID,
		AuthorizationOutcome: authorizationOutcome, AuthorizationReasonCode: authorizationReason,
		SchedulingState: schedulingState, Timestamp: &timestamp,
	})
}

func (je *JobEngine) AuditEvents() []AuditEvent {
	if je == nil {
		return nil
	}
	je.auditMu.Lock()
	defer je.auditMu.Unlock()
	out := make([]AuditEvent, len(je.auditEvents))
	copy(out, je.auditEvents)
	return out
}

func (je *JobEngine) TraceLogs() []string {
	if je == nil {
		return nil
	}
	je.auditMu.Lock()
	defer je.auditMu.Unlock()
	out := make([]string, len(je.traceLogs))
	copy(out, je.traceLogs)
	return out
}

func contractForbidsAction(identity *AgentIdentity, actionName string) bool {
	if identity == nil || identity.Contract == nil {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(actionName))
	for _, forbidden := range identity.Contract.ForbiddenActions {
		normalized := strings.ToLower(strings.TrimSpace(forbidden))
		if normalized == action {
			return true
		}
		if (normalized == "delegate" || normalized == "delegation") && action == "decision" {
			return true
		}
	}
	return false
}

func effectiveFailurePolicy(identity *AgentIdentity) string {
	if identity == nil || identity.Contract == nil {
		return "abort"
	}
	p := strings.ToLower(strings.TrimSpace(identity.Contract.FailurePolicy))
	switch p {
	case "continue", "fallback", "abort":
		return p
	default:
		return "abort"
	}
}

func capabilitiesUsed(identity *AgentIdentity, actionName string) []string {
	if identity == nil {
		return nil
	}
	req := requiredCapabilityForActionName(actionName)
	if req == "" {
		return nil
	}
	for _, c := range identity.Capabilities {
		nc := strings.ToLower(strings.TrimSpace(c))
		if nc == "all" || nc == req || (req == "ai_reasoning" && nc == "ai_interaction") {
			return []string{nc}
		}
	}
	return nil
}

func emitDelegationAudit(params map[string]interface{}, target string, outcome string, reason string) {
	if AgentsEngine == nil {
		return
	}
	identity, _ := parseRuntimeIdentity(params)
	runtime := getAgentRuntimeMap(params)
	e := AuditEvent{
		RunID:              toString(runtime["run_id"]),
		TraceID:            toString(runtime["trace_id"]),
		AgentID:            identity.AgentID,
		AgentName:          identity.Name,
		Owner:              identity.Owner,
		Action:             "Decision",
		RequiredCapability: requiredCapabilityForActionName("Decision"),
		CapabilitiesUsed:   capabilitiesUsed(&identity, "Decision"),
		Outcome:            outcome,
		Reason:             reason,
		DelegationTarget:   target,
	}
	AgentsEngine.appendAudit(e)
}

func toString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func snapshotAuditEvents(events []AuditEvent) string {
	b, _ := json.MarshalIndent(events, "", "  ")
	return string(b)
}
