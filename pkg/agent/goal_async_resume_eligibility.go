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

// Internal, read-only resume eligibility for asynchronous goals.
//
// This service answers whether a paused goal could be considered for a future
// resume attempt using current trusted authority. It never resumes a goal,
// issues a resume grant, schedules work, claims a lease, executes a model,
// crawler, tool or MCP call, or marks acceptance criteria satisfied. An
// "eligible" result is a diagnostic decision, not an executable grant: any
// future executor must revalidate current authority and cancellation under its
// own claim/lease transaction.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
)

// goalAsyncResumeEligibilityOutcome is the closed result vocabulary.
type goalAsyncResumeEligibilityOutcome string

const (
	// goalAsyncResumeEligible means the current operation is reported
	// completed and every required current-authority check passed. It is not a
	// grant and does not prove goal acceptance criteria.
	goalAsyncResumeEligible goalAsyncResumeEligibilityOutcome = "eligible"
	// goalAsyncResumeWaiting means the goal is otherwise sound but the
	// operation is still accepted, pending or processing.
	goalAsyncResumeWaiting goalAsyncResumeEligibilityOutcome = "waiting"
	// goalAsyncResumeDenied means current authority, cancellation, deadline or
	// budget forbids a future resume attempt.
	goalAsyncResumeDenied goalAsyncResumeEligibilityOutcome = "denied"
	// goalAsyncResumeEligibilityUncertain means identity, chronology or
	// authoritative evidence cannot be established consistently.
	goalAsyncResumeEligibilityUncertain goalAsyncResumeEligibilityOutcome = "uncertain"
	// goalAsyncResumeUnavailable means a required trusted store or authority
	// source could not be read. It is never permission to continue.
	goalAsyncResumeUnavailable goalAsyncResumeEligibilityOutcome = "unavailable"
)

// Stable, bounded reason codes for eligibility decisions.
const (
	goalAsyncResumeReasonEligible                    = "goal_resume_eligible"
	goalAsyncResumeReasonWaitingPending              = "goal_resume_waiting_operation_pending"
	goalAsyncResumeReasonWaitingProcessing           = "goal_resume_waiting_operation_processing"
	goalAsyncResumeReasonDeniedInvalidRequest        = "goal_resume_denied_invalid_request"
	goalAsyncResumeReasonDeniedState                 = "goal_resume_denied_not_awaiting_external_work"
	goalAsyncResumeReasonDeniedCancelled             = "goal_resume_denied_cancelled"
	goalAsyncResumeReasonDeniedDeadline              = "goal_resume_denied_deadline_expired"
	goalAsyncResumeReasonDeniedBudget                = "goal_resume_denied_budget_exhausted"
	goalAsyncResumeReasonDeniedSystemPrincipal       = "goal_resume_denied_system_principal_unsupported"
	goalAsyncResumeReasonDeniedPrincipalInactive     = "goal_resume_denied_principal_inactive"
	goalAsyncResumeReasonDeniedPrincipalMismatch     = "goal_resume_denied_principal_mismatch"
	goalAsyncResumeReasonDeniedSourceOwnership       = "goal_resume_denied_source_ownership"
	goalAsyncResumeReasonDeniedAgentUnavailable      = "goal_resume_denied_agent_unavailable"
	goalAsyncResumeReasonDeniedAgentIdentity         = "goal_resume_denied_agent_identity_changed"
	goalAsyncResumeReasonDeniedAgentCapability       = "goal_resume_denied_agent_capability_missing"
	goalAsyncResumeReasonDeniedAgentTrust            = "goal_resume_denied_agent_trust_insufficient"
	goalAsyncResumeReasonDeniedAgentContract         = "goal_resume_denied_agent_contract_forbidden"
	goalAsyncResumeReasonDeniedGoalSpec              = "goal_resume_denied_goal_spec_changed"
	goalAsyncResumeReasonDeniedPolicy                = "goal_resume_denied_policy_changed"
	goalAsyncResumeReasonDeniedScope                 = "goal_resume_denied_scope_changed"
	goalAsyncResumeReasonDeniedOperationTerminal     = "goal_resume_denied_operation_terminal"
	goalAsyncResumeReasonUncertainGoalMissing        = "goal_resume_uncertain_goal_missing"
	goalAsyncResumeReasonUncertainGoalCorrupt        = "goal_resume_uncertain_goal_corrupt"
	goalAsyncResumeReasonUncertainOperationReference = "goal_resume_uncertain_operation_reference"
	goalAsyncResumeReasonUncertainOperationNotFound  = "goal_resume_uncertain_operation_not_found"
	goalAsyncResumeReasonUncertainOperationConflict  = "goal_resume_uncertain_operation_conflict"
	goalAsyncResumeReasonUncertainOperation          = "goal_resume_uncertain_operation"
	goalAsyncResumeReasonUncertainStateChanged       = "goal_resume_uncertain_state_changed"
	goalAsyncResumeReasonUncertainClock              = "goal_resume_uncertain_clock_regression"
	goalAsyncResumeReasonUnavailableDatabase         = "goal_resume_unavailable_database"
	goalAsyncResumeReasonUnavailableContext          = "goal_resume_unavailable_context"
	goalAsyncResumeReasonUnavailableAgentAuthority   = "goal_resume_unavailable_agent_authority"
	goalAsyncResumeReasonUnavailableSourceAuthority  = "goal_resume_unavailable_source_authority"
	goalAsyncResumeReasonUnavailableOperation        = "goal_resume_unavailable_operation"
)

var (
	// errGoalAsyncAgentAuthorityUnavailable means a provider could not prove
	// the current agent authority. It is never a permission grant.
	errGoalAsyncAgentAuthorityUnavailable = errors.New("goal async current agent authority is unavailable")
)

// goalAsyncAgentAuthority is the trusted current view of one agent goal.
// A provider must derive it from the deployment's authoritative manifest
// source, never from the checkpoint being evaluated.
type goalAsyncAgentAuthority struct {
	Enabled        bool
	AgentID        string
	Owner          string
	GoalID         string
	GoalSpecHash   string
	PolicySHA256   string
	AllowedTools   []string
	AllowedSources []uint64
	TrustLevel     string
	Capabilities   []string
	Contract       *AgentContract
}

// goalAsyncAgentAuthorityProvider is the narrow trusted interface for current
// agent authority. The zero/nil provider fails closed. No package-global
// production provider is installed by this work package.
type goalAsyncAgentAuthorityProvider interface {
	CurrentGoalAsyncAgentAuthority(ctx context.Context, agentID, goalID, goalSpecHash string) (goalAsyncAgentAuthority, bool, error)
}

// goalAsyncRegistryAgentAuthorityProvider reads the current process agent
// registry. It is an explicit construction helper for trusted internal callers
// and tests, not a production default: a previously loaded registry snapshot is
// not proof that an on-disk manifest has not changed. Production activation
// must supply a provider backed by the deployment's trusted reload source.
type goalAsyncRegistryAgentAuthorityProvider struct {
	registry *AgentRegistry
}

func newGoalAsyncRegistryAgentAuthorityProvider(registry *AgentRegistry) goalAsyncAgentAuthorityProvider {
	return goalAsyncRegistryAgentAuthorityProvider{registry: registry}
}

// CurrentGoalAsyncAgentAuthority resolves one frozen goal specification from
// the current registry snapshot. A missing agent or goal fails closed.
func (p goalAsyncRegistryAgentAuthorityProvider) CurrentGoalAsyncAgentAuthority(ctx context.Context, agentID, goalID, goalSpecHash string) (goalAsyncAgentAuthority, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return goalAsyncAgentAuthority{}, false, err
		}
	}
	if p.registry == nil {
		return goalAsyncAgentAuthority{}, false, errGoalAsyncAgentAuthorityUnavailable
	}
	def, ok := p.registry.GetByID(strings.TrimSpace(agentID))
	if !ok || def == nil {
		return goalAsyncAgentAuthority{}, false, nil
	}
	identity := cloneAgentIdentityValue(def.Identity)
	for jobIndex := range def.Jobs {
		job := &def.Jobs[jobIndex]
		for _, spec := range job.goalSpecs {
			if spec == nil || spec.GoalID != goalID || spec.SpecHash != goalSpecHash {
				continue
			}
			return goalAsyncAgentAuthority{
				Enabled:        true,
				AgentID:        identity.AgentID,
				Owner:          identity.Owner,
				GoalID:         spec.GoalID,
				GoalSpecHash:   spec.SpecHash,
				PolicySHA256:   goalAsyncPolicyHash(spec),
				AllowedTools:   sortedUniqueStrings(spec.Allowlist),
				AllowedSources: sortedUniqueUint64(spec.AllowedSources),
				TrustLevel:     identity.TrustLevel,
				Capabilities:   append([]string(nil), identity.Capabilities...),
				Contract:       identity.Contract,
			}, true, nil
		}
	}
	return goalAsyncAgentAuthority{}, false, nil
}

// goalAsyncResumeEligibilityDecision is a bounded, sanitized eligibility
// description. It never contains raw checkpoint data, policy text, credentials
// or operation details.
type goalAsyncResumeEligibilityDecision struct {
	Outcome        goalAsyncResumeEligibilityOutcome
	ReasonCode     string
	RunID          string
	OperationID    string
	State          goalAsyncState
	OperationClass goalAsyncObservationClass
	OperationState string
	CheckedAt      time.Time
}

// goalAsyncResumeEligibility is an explicitly constructed internal service.
type goalAsyncResumeEligibility struct {
	store           *goalAsyncStore
	agentAuthority  goalAsyncAgentAuthorityProvider
	sourceAuthority CrawlSourceAuthorizer
}

// newGoalAsyncResumeEligibility constructs the service. A nil store or nil
// agent-authority provider fails closed at evaluation time; there is no
// default provider and no in-memory fallback.
func newGoalAsyncResumeEligibility(store *goalAsyncStore, agentAuthority goalAsyncAgentAuthorityProvider) *goalAsyncResumeEligibility {
	return &goalAsyncResumeEligibility{store: store, agentAuthority: agentAuthority}
}

// Evaluate reads the current persisted checkpoint and returns a read-only
// eligibility decision. It accepts only a run ID and an operation ID; it never
// accepts a caller-supplied checkpoint.
func (e *goalAsyncResumeEligibility) Evaluate(ctx context.Context, runID, operationID string) (goalAsyncResumeEligibilityDecision, error) {
	return e.evaluateAt(ctx, runID, operationID, time.Now().UTC())
}

func (e *goalAsyncResumeEligibility) evaluateAt(ctx context.Context, runID, operationID string, now time.Time) (goalAsyncResumeEligibilityDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	decision := goalAsyncResumeEligibilityDecision{
		Outcome:     goalAsyncResumeUnavailable,
		ReasonCode:  goalAsyncResumeReasonUnavailableDatabase,
		RunID:       runID,
		OperationID: operationID,
		CheckedAt:   normalizeGoalAsyncTime(now),
	}
	if e == nil || e.store == nil || e.store.db == nil {
		decision.Outcome = goalAsyncResumeUnavailable
		decision.ReasonCode = goalAsyncResumeReasonUnavailableDatabase
		return decision, nil
	}
	if !goalAsyncIDPattern.MatchString(runID) || !goalAsyncIDPattern.MatchString(operationID) {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedInvalidRequest
		return decision, nil
	}
	now = normalizeGoalAsyncTime(now)
	if now.IsZero() {
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainClock
		return decision, nil
	}

	cp, cancelled, err := e.store.LoadCheckpointWithCancellation(ctx, runID)
	if err != nil {
		return goalAsyncEligibilityLoadDecision(ctx, decision, err)
	}
	decision.State = cp.State
	if cancelled {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedCancelled
		return decision, nil
	}
	if cp.State != goalAsyncAwaitingExternalWork {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedState
		return decision, nil
	}
	if now.Before(cp.UpdatedAt) {
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainClock
		return decision, nil
	}
	if !now.Before(cp.Deadline) {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedDeadline
		return decision, nil
	}
	if cp.Used.reachesAnyCeiling(cp.Ceilings) || cp.Slices >= cp.Ceilings.MaxSlices {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedBudget
		return decision, nil
	}
	if _, ok := findGoalAsyncWork(cp.Work, operationID); !ok {
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainOperationReference
		return decision, nil
	}

	principal, outcome, reason := e.currentPrincipal(ctx, cp)
	if outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}
	if outcome, reason := e.verifyAgentAuthority(ctx, cp); outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}
	if outcome, reason := e.verifySourceAuthority(ctx, principal, cp); outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}

	observation, observationErr := e.store.observeCrawlOperationAt(ctx, cp, operationID, now)
	decision.OperationClass = observation.Class
	decision.OperationState = observation.OperationState

	// Re-run the current-authority checks after correlation so a change that
	// happened during the read is observed. This narrows the time-of-check
	// window but does not eliminate it: a future claim/lease transaction must
	// repeat the checks at the execution boundary.
	principal, outcome, reason = e.currentPrincipal(ctx, cp)
	if outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}
	if outcome, reason := e.verifyAgentAuthority(ctx, cp); outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}
	if outcome, reason := e.verifySourceAuthority(ctx, principal, cp); outcome != "" {
		decision.Outcome = outcome
		decision.ReasonCode = reason
		return decision, nil
	}

	current, currentCancelled, err := e.store.LoadCheckpointWithCancellation(ctx, runID)
	if err != nil {
		return goalAsyncEligibilityLoadDecision(ctx, decision, err)
	}
	if currentCancelled {
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedCancelled
		return decision, nil
	}
	if current.Revision != cp.Revision || current.State != cp.State || !current.UpdatedAt.Equal(cp.UpdatedAt) {
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainStateChanged
		decision.State = current.State
		return decision, nil
	}

	switch observation.Class {
	case goalAsyncObservationPending:
		decision.Outcome = goalAsyncResumeWaiting
		decision.ReasonCode = goalAsyncResumeReasonWaitingPending
	case goalAsyncObservationProcessing:
		decision.Outcome = goalAsyncResumeWaiting
		decision.ReasonCode = goalAsyncResumeReasonWaitingProcessing
	case goalAsyncObservationReportedCompleted:
		decision.Outcome = goalAsyncResumeEligible
		decision.ReasonCode = goalAsyncResumeReasonEligible
	case goalAsyncObservationTerminalNonSuccess:
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedOperationTerminal
	case goalAsyncObservationNotFound:
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainOperationNotFound
	case goalAsyncObservationConflict:
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainOperationConflict
	case goalAsyncObservationUnavailable:
		decision.Outcome = goalAsyncResumeUnavailable
		decision.ReasonCode = goalAsyncResumeReasonUnavailableOperation
	default:
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainOperation
	}
	if observationErr != nil {
		switch {
		case errors.Is(observationErr, errGoalAsyncObservationUnavailable):
			decision.Outcome = goalAsyncResumeUnavailable
			decision.ReasonCode = goalAsyncResumeReasonUnavailableOperation
		case errors.Is(observationErr, errGoalAsyncObservationConflict):
			decision.Outcome = goalAsyncResumeEligibilityUncertain
			decision.ReasonCode = goalAsyncResumeReasonUncertainOperationConflict
		case errors.Is(observationErr, errGoalAsyncObservationUncertain):
			decision.Outcome = goalAsyncResumeEligibilityUncertain
			decision.ReasonCode = goalAsyncResumeReasonUncertainOperation
		}
	}
	return decision, nil
}

func goalAsyncEligibilityLoadDecision(ctx context.Context, decision goalAsyncResumeEligibilityDecision, err error) (goalAsyncResumeEligibilityDecision, error) {
	switch {
	case errors.Is(err, errGoalAsyncStoreNotFound):
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainGoalMissing
	case errors.Is(err, errGoalAsyncStoreCorrupt):
		decision.Outcome = goalAsyncResumeEligibilityUncertain
		decision.ReasonCode = goalAsyncResumeReasonUncertainGoalCorrupt
	case errors.Is(err, ErrGoalAsyncCheckpointInvalid):
		decision.Outcome = goalAsyncResumeDenied
		decision.ReasonCode = goalAsyncResumeReasonDeniedInvalidRequest
	case ctx != nil && ctx.Err() != nil:
		decision.Outcome = goalAsyncResumeUnavailable
		decision.ReasonCode = goalAsyncResumeReasonUnavailableContext
	default:
		decision.Outcome = goalAsyncResumeUnavailable
		decision.ReasonCode = goalAsyncResumeReasonUnavailableDatabase
	}
	return decision, nil
}

func (e *goalAsyncResumeEligibility) currentPrincipal(ctx context.Context, cp goalAsyncCheckpoint) (CrawlPrincipal, goalAsyncResumeEligibilityOutcome, string) {
	switch cp.PrincipalKind {
	case goalAsyncPrincipalUser:
	case goalAsyncPrincipalSystem:
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedSystemPrincipal
	default:
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPrincipalMismatch
	}
	if cp.PrincipalUserID == 0 || strings.TrimSpace(cp.PrincipalUsername) == "" {
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPrincipalMismatch
	}
	identity := auth.Identity{
		Subject:  strconv.FormatUint(cp.PrincipalUserID, 10),
		Username: cp.PrincipalUsername,
		Issuer:   cp.PrincipalIssuer,
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, *e.store.db, identity)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return CrawlPrincipal{}, goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableContext
		}
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPrincipalInactive
	}
	if strings.TrimSpace(resolved.Subject) != identity.Subject ||
		strings.TrimSpace(resolved.Username) != cp.PrincipalUsername ||
		strings.TrimSpace(resolved.Issuer) != strings.TrimSpace(cp.PrincipalIssuer) {
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPrincipalMismatch
	}
	principal := CrawlPrincipal{
		UserID:   cp.PrincipalUserID,
		Username: resolved.Username,
		Issuer:   resolved.Issuer,
		Roles:    append([]string(nil), resolved.Roles...),
		Scopes:   append([]string(nil), resolved.Scopes...),
	}
	if err := principal.Validate(); err != nil {
		return CrawlPrincipal{}, goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPrincipalMismatch
	}
	return principal, "", ""
}

func (e *goalAsyncResumeEligibility) verifyAgentAuthority(ctx context.Context, cp goalAsyncCheckpoint) (goalAsyncResumeEligibilityOutcome, string) {
	if e.agentAuthority == nil {
		return goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableAgentAuthority
	}
	authority, found, err := e.agentAuthority.CurrentGoalAsyncAgentAuthority(ctx, cp.AgentID, cp.GoalID, cp.GoalSpecHash)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableContext
		}
		return goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableAgentAuthority
	}
	if !found || !authority.Enabled {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentUnavailable
	}
	if strings.TrimSpace(authority.AgentID) != cp.AgentID ||
		strings.TrimSpace(authority.Owner) != cp.Owner {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentIdentity
	}
	if authority.GoalID != cp.GoalID || authority.GoalSpecHash != cp.GoalSpecHash ||
		!goalAsyncHashPattern.MatchString(authority.GoalSpecHash) {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedGoalSpec
	}
	if authority.PolicySHA256 != cp.PolicySHA256 ||
		!goalAsyncHashPattern.MatchString(authority.PolicySHA256) {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedPolicy
	}
	if !equalStringSets(authority.AllowedTools, cp.AllowedTools) ||
		!equalUint64Sets(authority.AllowedSources, cp.AllowedSources) {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedScope
	}
	identity := AgentIdentity{
		AgentID:      authority.AgentID,
		Owner:        authority.Owner,
		TrustLevel:   authority.TrustLevel,
		Capabilities: append([]string(nil), authority.Capabilities...),
		Contract:     authority.Contract,
	}
	if !identityGrants(identity, "goal_execution") {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentCapability
	}
	if trustLevelRank(identity.TrustLevel) < minToolTrustRank {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentTrust
	}
	if contractForbidsAction(&identity, "AgentGoalRun") {
		return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedAgentContract
	}
	return "", ""
}

func (e *goalAsyncResumeEligibility) verifySourceAuthority(ctx context.Context, principal CrawlPrincipal, cp goalAsyncCheckpoint) (goalAsyncResumeEligibilityOutcome, string) {
	authorizer := e.sourceAuthority
	if authorizer == nil {
		authorizer = DatabaseCrawlSourceAuthorizer{
			Lookup:     DatabaseSourceOwnerLookup{DB: e.store.db},
			AllowAdmin: false,
		}
	}
	for _, sourceID := range cp.AllowedSources {
		if err := authorizer.AuthorizeCrawlSource(ctx, principal, sourceID); err != nil {
			switch {
			case errors.Is(err, ErrCrawlSourceOwnershipMissing), errors.Is(err, ErrCrawlSourceForbidden):
				return goalAsyncResumeDenied, goalAsyncResumeReasonDeniedSourceOwnership
			case ctx != nil && ctx.Err() != nil:
				return goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableContext
			default:
				return goalAsyncResumeUnavailable, goalAsyncResumeReasonUnavailableSourceAuthority
			}
		}
	}
	return "", ""
}
