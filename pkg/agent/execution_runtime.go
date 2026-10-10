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
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	cfgKeyAgentRuntime = "agent_runtime"
	cfgKeyAgent        = "agent"
)

// AgentExecutionContext captures per-run identity metadata and correlation fields.
type AgentExecutionContext struct {
	RunID            string        `json:"run_id"`
	TraceID          string        `json:"trace_id"`
	Source           string        `json:"source,omitempty"`
	Owner            string        `json:"owner,omitempty"`
	IdentitySnapshot AgentIdentity `json:"identity_snapshot"`
	StartedAt        time.Time     `json:"started_at"`
}

// constraintBudgetManager owns one job group's step counter, elapsed-time
// deadline and cancellation context. Budgets are per job group: parallel
// groups each get their own manager, retries/fallbacks within a group are
// charged to that same group, and a delegated agent's groups receive fresh
// independently scoped managers (the delegation step itself is charged once
// to the caller's group). There is intentionally no global agent-run budget.
type constraintBudgetManager struct {
	startedAt      time.Time
	timeBudget     time.Duration
	hasTimeBudget  bool
	maxSteps       int
	eventRateLimit float64
	executedSteps  int
	eventsCreated  int
	ctx            context.Context
	cancel         context.CancelFunc
}

func newConstraintBudgetManager(identity AgentIdentity) (*constraintBudgetManager, error) {
	bm := &constraintBudgetManager{startedAt: time.Now().UTC()}
	if identity.Constraints == nil {
		bm.ctx, bm.cancel = context.WithCancel(context.Background())
		return bm, nil
	}

	bm.maxSteps = identity.Constraints.MaxSteps
	bm.eventRateLimit = identity.Constraints.EventRateLimit
	if tb := strings.TrimSpace(identity.Constraints.TimeBudget); tb != "" {
		d, err := time.ParseDuration(tb)
		if err != nil {
			return nil, fmt.Errorf("invalid time_budget %q: %w", tb, err)
		}
		bm.timeBudget = d
		bm.hasTimeBudget = true
	}
	if bm.hasTimeBudget {
		bm.ctx, bm.cancel = context.WithDeadline(context.Background(), bm.startedAt.Add(bm.timeBudget))
	} else {
		bm.ctx, bm.cancel = context.WithCancel(context.Background())
	}
	return bm, nil
}

// release frees the group context; callers defer it at group end.
func (bm *constraintBudgetManager) release() {
	if bm == nil || bm.cancel == nil {
		return
	}
	bm.cancel()
}

func (bm *constraintBudgetManager) preStepCheck(actionName string) error {
	if bm == nil {
		return nil
	}
	if bm.ctx != nil {
		if err := bm.ctx.Err(); err != nil {
			return fmt.Errorf("constraint gate denied action %s: time_budget exceeded (%s)", actionName, bm.timeBudget)
		}
	}
	if bm.maxSteps > 0 && bm.executedSteps >= bm.maxSteps {
		return fmt.Errorf("constraint gate denied action %s: max_steps exceeded (%d)", actionName, bm.maxSteps)
	}
	if bm.hasTimeBudget && time.Since(bm.startedAt) > bm.timeBudget {
		return fmt.Errorf("constraint gate denied action %s: time_budget exceeded (%s)", actionName, bm.timeBudget)
	}
	if strings.EqualFold(actionName, "CreateEvent") && bm.eventRateLimit > 0 {
		maxEvents := int(math.Ceil(bm.eventRateLimit))
		if maxEvents <= 0 {
			maxEvents = 1
		}
		if bm.eventsCreated >= maxEvents {
			return fmt.Errorf("constraint gate denied action %s: event_rate_limit exceeded (%.2f)", actionName, bm.eventRateLimit)
		}
	}
	return nil
}

func (bm *constraintBudgetManager) markActionExecuted(actionName string) {
	if bm == nil {
		return
	}
	bm.executedSteps++
	if strings.EqualFold(actionName, "CreateEvent") {
		bm.eventsCreated++
	}
}

func runtimeFlagsFromConfig(iCfg map[string]any) cfg.AgentRuntimeConfig {
	flags := cfg.AgentRuntimeConfig{}
	if iCfg == nil {
		return flags
	}

	decode := func(raw any) {
		switch v := raw.(type) {
		case cfg.AgentRuntimeConfig:
			flags = v
		case *cfg.AgentRuntimeConfig:
			if v != nil {
				flags = *v
			}
		case map[string]any:
			if b, ok := v["identity_enforcement"].(bool); ok {
				flags.IdentityEnforcement = b
			}
			if b, ok := v["contract_enforcement"].(bool); ok {
				flags.ContractEnforcement = b
			}
			if b, ok := v["memory_runtime"].(bool); ok {
				flags.MemoryRuntime = b
			}
		}
	}

	if raw, ok := iCfg[cfgKeyAgentRuntime]; ok {
		decode(raw)
	}
	if raw, ok := iCfg[cfgKeyAgent]; ok {
		decode(raw)
	}
	return flags
}

func newAgentExecutionContext(identity AgentIdentity, source string) AgentExecutionContext {
	return AgentExecutionContext{
		RunID:            uuid.NewString(),
		TraceID:          uuid.NewString(),
		Source:           source,
		Owner:            identity.Owner,
		IdentitySnapshot: identity,
		StartedAt:        time.Now().UTC(),
	}
}

// actionCapability maps registered action names to their canonical
// schema-aligned capability token.
var actionCapability = map[string]string{
	"RunCommand":      "command_execution",
	"DBQuery":         "db_read",
	"AIInteraction":   "ai_reasoning",
	"PluginExecution": "plugin_execution",
	"CreateEvent":     "emit_event",
	"APIRequest":      "api_request",
}

// delegationCapabilityName is the schema capability needed to delegate.
const delegationCapabilityName = "delegate"

// requiredCapabilityForAction returns the canonical capability for a known
// action and false for unknown/future actions (fail-closed: unknown actions
// are never implicitly authorized under enforcement).
func requiredCapabilityForAction(actionName string) (string, bool) {
	trimmed := strings.TrimSpace(actionName)
	if trimmed == "" {
		return "", false
	}
	if v, ok := actionCapability[trimmed]; ok {
		return v, true
	}
	if trimmed == "Decision" {
		// Decision local branching needs no extra grant; delegation
		// performed by Decision requires `delegate` (checked separately).
		return "", true
	}
	return "", false
}

// requiredCapabilityForActionName is a compatibility helper preserving the
// original single-return signature for callers that only need the token.
// It returns "" for unknown actions.
func requiredCapabilityForActionName(actionName string) string {
	cap, _ := requiredCapabilityForAction(actionName)
	return cap
}

// capabilityAllowed reports whether identity grants actionName.
// Unknown actions are denied (fail-closed). Decision local branching is
// allowed without an extra grant; delegation is checked via
// delegationPolicyCheck (requires `delegate`).
func capabilityAllowed(identity AgentIdentity, actionName string) bool {
	required, known := requiredCapabilityForAction(actionName)
	if !known {
		return false
	}
	if required == "" {
		// Known pure-control action (Decision local branching).
		return true
	}
	if len(identity.Capabilities) == 0 {
		return false
	}
	for _, capability := range identity.Capabilities {
		normalized := strings.ToLower(strings.TrimSpace(capability))
		if normalized == "all" || normalized == required {
			return true
		}
		// backward-compatibility aliases for pre-PR-1 runtime-only spellings,
		// honored only where previously accepted (no broadening):
		// ai_interaction → ai_reasoning
		if required == "ai_reasoning" && normalized == "ai_interaction" {
			return true
		}
		// run_command → command_execution
		if required == "command_execution" && normalized == "run_command" {
			return true
		}
		// create_event → emit_event
		if required == "emit_event" && normalized == "create_event" {
			return true
		}
		// call_plugin ↔ plugin_execution (schema carries both tokens)
		if required == "plugin_execution" && normalized == "call_plugin" {
			return true
		}
	}
	return false
}

// dbQueryCapabilityRequired classifies SQL and returns the required grant.
// It returns (capability, true) for classifiable statements and ("", false)
// for rejected/ambiguous statements (fail-closed: deny even with db_write,
// except for the pre-existing `all` wildcard path which is handled by the
// caller to preserve current wildcard semantics).
func dbQueryCapabilityRequired(sql string) (string, bool) {
	switch DBQueryClassifier.Classify(sql) {
	case SQLRead:
		return "db_read", true
	case SQLWrite:
		return "db_write", true
	default:
		return "", false
	}
}

// enforceDBQueryGate enforces the SQL-aware capability gate for DBQuery steps.
// It runs before interpolation/execution. Templated queries (containing
// $response or {{) cannot be classified yet, so they conservatively require
// db_write (or the pre-existing `all` wildcard); the resolved SQL is
// re-checked inside DBQueryAction.Execute before reaching the driver.
func enforceDBQueryGate(je *JobEngine, params map[string]interface{}, identity AgentIdentity, execCtx AgentExecutionContext, auditAgentID, auditAgentName, auditOwner string) error {
	raw, _ := params["query"].(string)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if strings.Contains(raw, "$response") || strings.Contains(raw, "{{") {
		for _, capability := range identity.Capabilities {
			normalized := strings.ToLower(strings.TrimSpace(capability))
			if normalized == "all" || normalized == "db_write" {
				return nil
			}
		}
		err := fmt.Errorf("capability gate denied action DBQuery: templated SQL requires capability %q", "db_write")
		if je != nil {
			je.appendAudit(AuditEvent{RunID: execCtx.RunID, TraceID: execCtx.TraceID, AgentID: auditAgentID, AgentName: auditAgentName, Owner: auditOwner, Action: "DBQuery", RequiredCapability: "db_write", Outcome: auditOutcomeDenied, Reason: err.Error()})
		}
		return err
	}
	if dbQueryAllowed(identity, raw) {
		return nil
	}
	required, ok := dbQueryCapabilityRequired(raw)
	if !ok {
		err := fmt.Errorf("capability gate denied action DBQuery: SQL rejected as unsupported or ambiguous")
		if je != nil {
			je.appendAudit(AuditEvent{RunID: execCtx.RunID, TraceID: execCtx.TraceID, AgentID: auditAgentID, AgentName: auditAgentName, Owner: auditOwner, Action: "DBQuery", RequiredCapability: "db_read/db_write", Outcome: auditOutcomeDenied, Reason: err.Error()})
		}
		return err
	}
	err := fmt.Errorf("capability gate denied action DBQuery: capability %q missing", required)
	if je != nil {
		je.appendAudit(AuditEvent{RunID: execCtx.RunID, TraceID: execCtx.TraceID, AgentID: auditAgentID, AgentName: auditAgentName, Owner: auditOwner, Action: "DBQuery", RequiredCapability: required, Outcome: auditOutcomeDenied, Reason: err.Error()})
	}
	return err
}

// dbQueryAllowed checks identity against the classified SQL.
func dbQueryAllowed(identity AgentIdentity, sql string) bool {
	// Preserve existing `all` wildcard semantics exactly.
	for _, capability := range identity.Capabilities {
		if strings.ToLower(strings.TrimSpace(capability)) == "all" {
			// `all` bypasses capability checks per current runtime behavior,
			// but must not mask parse errors: still reject unparseable SQL.
			if DBQueryClassifier.Classify(sql) == SQLRejected {
				return false
			}
			return true
		}
	}
	required, ok := dbQueryCapabilityRequired(sql)
	if !ok {
		return false
	}
	for _, capability := range identity.Capabilities {
		normalized := strings.ToLower(strings.TrimSpace(capability))
		if normalized == required {
			return true
		}
		// Legacy alias: pre-PR-1 `db_query` granted all DBQuery regardless
		// of statement kind; honor it for both read and write to preserve
		// behavior where previously accepted (schema rejects it for new v2
		// manifests, so this only affects legacy in-memory identities).
		if normalized == "db_query" {
			return true
		}
	}
	return false
}

// trustLevelRank orders trust levels. The schema vocabulary
// (untrusted, restricted, trusted, system) is mapped explicitly;
// privileged/high/internal/medium are long-standing runtime synonyms
// and keep their historical ranks.
func trustLevelRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "system", "privileged", "high", "internal":
		return 3
	case "trusted", "medium":
		return 2
	case "untrusted", "restricted":
		return 1
	default:
		return 1
	}
}

func minTrustRankForAction(actionName string) int {
	switch strings.TrimSpace(actionName) {
	case "RunCommand", "DBQuery", "PluginExecution":
		return 2
	default:
		return 1
	}
}

func trustAllowed(identity AgentIdentity, actionName string) bool {
	return trustLevelRank(identity.TrustLevel) >= minTrustRankForAction(actionName)
}

// applyExecutionContext attaches the run identity snapshot and the active
// enforcement flags to step params. Persisting the flags here (not just the
// snapshot) guarantees delegated agents inherit the caller's enforcement
// posture through delegationCtx instead of silently running unenforced.
func applyExecutionContext(params map[string]interface{}, ctx AgentExecutionContext, flags cfg.AgentRuntimeConfig) {
	if params == nil {
		return
	}
	configMap, _ := params[StrConfig].(map[string]interface{})
	if configMap == nil {
		configMap = map[string]interface{}{}
		params[StrConfig] = configMap
	}
	runtimeMap, _ := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	if runtimeMap == nil {
		runtimeMap = map[string]interface{}{}
		switch existing := configMap[cfgKeyAgentRuntime].(type) {
		case cfg.AgentRuntimeConfig:
			runtimeMap["identity_enforcement"] = existing.IdentityEnforcement
			runtimeMap["contract_enforcement"] = existing.ContractEnforcement
			runtimeMap["memory_runtime"] = existing.MemoryRuntime
		case *cfg.AgentRuntimeConfig:
			if existing != nil {
				runtimeMap["identity_enforcement"] = existing.IdentityEnforcement
				runtimeMap["contract_enforcement"] = existing.ContractEnforcement
				runtimeMap["memory_runtime"] = existing.MemoryRuntime
			}
		}
	}
	runtimeMap["identity_enforcement"] = flags.IdentityEnforcement
	runtimeMap["contract_enforcement"] = flags.ContractEnforcement
	runtimeMap["memory_runtime"] = flags.MemoryRuntime
	runtimeMap["run_id"] = ctx.RunID
	runtimeMap["trace_id"] = ctx.TraceID
	runtimeMap["source"] = ctx.Source
	runtimeMap["owner"] = ctx.Owner
	runtimeMap["identity_snapshot"] = ctx.IdentitySnapshot
	configMap[cfgKeyAgentRuntime] = runtimeMap
}
