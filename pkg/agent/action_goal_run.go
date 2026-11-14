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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// AgentGoalRunAction runs a bounded, evidence-verified goal. It reuses the
// existing provider transport, tool registry, AuthorizeToolCall policy and
// budget helpers. Success is emitted only by the deterministic evaluator.
type AgentGoalRunAction struct{}

// Name returns the action name.
func (a *AgentGoalRunAction) Name() string { return "AgentGoalRun" }

// Execute runs one goal step without a caller context. Framework callers use
// ExecuteContext so HTTP cancellation reaches MCP and model operations.
func (a *AgentGoalRunAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	return a.ExecuteContext(context.Background(), params)
}

// ExecuteContext runs one goal step. Non-criteria_met terminals are action
// errors so the engine's retry/fallback policy cannot treat them as success.
func (a *AgentGoalRunAction) ExecuteContext(ctx context.Context, params map[string]interface{}) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rval := map[string]interface{}{StrResponse: nil, StrConfig: nil}
	if err := ctx.Err(); err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "goal run cancelled: " + err.Error()
		return rval, err
	}
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	spec := goalSpecFromRuntime(params)
	if spec == nil {
		err := fmt.Errorf("goal run denied: missing frozen goal spec")
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	identity := goalIdentityFromRuntime(params)
	if identity.AgentID == "" {
		err := fmt.Errorf("goal run denied: missing trusted identity")
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	if !runtimeFlagsFromConfig(config).IdentityEnforcement {
		err := fmt.Errorf("goal run denied: identity enforcement is required")
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	if !identityGrants(identity, "goal_execution") || trustLevelRank(identity.TrustLevel) < minToolTrustRank {
		err := fmt.Errorf("goal run denied: identity lacks goal_execution capability or trusted standing")
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	principal := mcpMutationPrincipalFromRuntimeConfig(config)
	if err := denyMCPMutationAliasesWithoutPrincipal(spec.Allowlist, principal); err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "goal run denied: " + err.Error()
		return rval, err
	}

	dbHandler, _ := config["db_handler"].(cdb.Handler)
	registry, err := DefaultToolRegistry()
	if custom, ok := config["goal_tool_registry"].(*AgentToolRegistry); ok && custom != nil {
		registry = custom
	}
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "goal run denied: tool registry unavailable"
		return rval, err
	}

	// One authoritative deadline covers MCP initialization, evidence checks,
	// provider calls, tool calls and cleanup. The goal loop receives the same
	// absolute deadline rather than a fresh MaxElapsed allowance.
	inheritedDeadline, _ := groupDeadline(params)
	runID := uuid.NewString()
	traceID := toolLoopRunFieldValue(params, "trace_id")
	if strings.TrimSpace(traceID) == "" {
		traceID = uuid.NewString()
	}
	runDeadline := goalExecutionDeadline(spec, inheritedDeadline, time.Now())
	runCtx, runCancel := context.WithDeadline(ctx, runDeadline)
	defer runCancel()
	failInitialization := func(err error) (map[string]interface{}, error) {
		if runCtx.Err() == nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "goal run denied: " + err.Error()
			return rval, err
		}
		status, code, reason := goalDeadlineTerminal(inheritedDeadline, time.Now())
		result := goalInitializationFailureResult(spec, runID, traceID, status, code, reason)
		rval[StrStatus] = StatusError
		rval[StrMessage] = result.Reason
		rval[StrResponse] = goalRunResultMap(result)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return rval, fmt.Errorf("goal run terminated: %s", result.Status)
		}
		return rval, fmt.Errorf("goal run cancelled: %w", runCtx.Err())
	}
	if err := runCtx.Err(); err != nil {
		return failInitialization(fmt.Errorf("goal execution deadline elapsed before initialization"))
	}

	var mcpManager *MCPToolManager
	if hasMCPAllowlistEntry(stringSet(spec.Allowlist)) {
		mcpManager, err = NewMCPToolManager(runCtx, CurrentMCPOperatorConfig(), identity, spec.Allowlist, spec.AllowedSources)
		if err != nil {
			return failInitialization(err)
		}
		if mcpManager != nil {
			defer func() { _ = mcpManager.Close() }()
			if err := mcpManager.Register(registry); err != nil {
				_ = mcpManager.Close()
				return failInitialization(err)
			}
		}
	}
	mcpAllowed, err := registerMCPMutationProposalTools(
		runCtx, mcpManager, registry, CurrentMCPMutationPolicy(),
		identity, principal, spec.Allowlist, spec.AllowedSources, dbHandler,
		runID, traceID,
	)
	if err != nil {
		return failInitialization(err)
	}
	var crawlRuntime *CrawlRequestToolRuntime
	if spec.MutationPolicy.EnabledOperations(crawlRequestToolName) {
		if !containsString(spec.Allowlist, crawlRequestToolName) {
			err := fmt.Errorf("goal run denied: %s must be in the explicit tool allowlist when mutation is enabled", crawlRequestToolName)
			rval[StrStatus] = StatusError
			rval[StrMessage] = err.Error()
			return rval, err
		}
		crawlRuntime, err = BuildCrawlRequestToolRuntime(runCtx, CrawlRequestToolRuntimeConfig{
			Enabled:           true,
			DB:                dbHandler,
			Identity:          identity,
			AllowedOperations: spec.MutationPolicy.AllowedOperations,
			AllowedSources:    spec.AllowedSources,
			MaxRequests:       spec.MutationPolicy.MaxRequests,
			BudgetWindow:      spec.MutationPolicy.BudgetWindow,
			Issuer:            spec.MutationPolicy.Issuer,
			ProposalTTL:       spec.MutationPolicy.ProposalTTL,
		})
		if err != nil {
			return failInitialization(err)
		}
		if err := RegisterCrawlRequestTool(registry, crawlRuntime); err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "goal run denied: " + err.Error()
			return rval, err
		}
	}
	for _, name := range spec.Allowlist {
		if _, ok := registry.Get(name); !ok {
			err := fmt.Errorf("goal run denied: allowlisted tool %q is not registered", name)
			rval[StrStatus] = StatusError
			rval[StrMessage] = err.Error()
			return rval, err
		}
	}
	evidenceRegistry, _ := config["goal_evidence_registry"].(*GoalEvidenceRegistry)
	if evidenceRegistry == nil {
		evidenceRegistry = NewDefaultGoalEvidenceRegistry(dbHandler)
	}

	provider, ok := getLLMProvider(spec.Provider)
	if !ok {
		result := GoalRunResult{
			Status: GoalProviderError, ReasonCode: "provider_unavailable",
			Reason: "unsupported AI provider", GoalID: spec.GoalID,
			GoalSpecHash: spec.SpecHash, RequestedGoal: goalSummary(spec),
			ToolCalls: []ToolCallSummary{}, Evidence: []EvidenceSummary{},
			CompletionDecision: CompletionDecision{DecidedBy: "deterministic_verifier"},
		}
		rval[StrStatus] = StatusError
		rval[StrMessage] = result.Reason
		rval[StrResponse] = goalRunResultMap(result)
		return rval, fmt.Errorf("goal run provider error")
	}

	runtimeSpec, err := resolveGoalRuntimeSpec(params, spec)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "goal run denied: prompt/messages could not be resolved"
		return rval, err
	}
	runConfig := goalRunConfig{
		Spec: runtimeSpec, Identity: identity, DB: dbHandler,
		Principal: principal,
		Registry:  registry, EvidenceRegistry: evidenceRegistry,
		AllowedOperations:    spec.MutationPolicy.OperationsSet(),
		MutationAllowed:      crawlRuntime != nil,
		MCPMutationAllowed:   len(mcpAllowed) > 0,
		MCPAllowedOperations: mcpAllowed,
		CrawlRuntime:         crawlRuntime,
		MCPManager:           mcpManager,
		Provider:             provider,
		RunID:                runID,
		TraceID:              traceID,
		CorrelationID:        toolLoopRunFieldValue(params, "run_id"),
		GroupDeadline:        inheritedDeadline,
		ExecutionDeadline:    runDeadline,
		Ctx:                  runCtx,
	}
	result, err := RunGoal(runConfig)
	rval[StrResponse] = goalRunResultMap(result)
	rval[StrMessage] = result.Reason
	if result.Status == GoalCriteriaMet {
		rval[StrStatus] = StatusSuccess
		return rval, nil
	}
	rval[StrStatus] = StatusError
	if err != nil {
		return rval, err
	}
	return rval, fmt.Errorf("goal run terminated: %s", result.Status)
}

// resolveGoalRuntimeSpec resolves prompt/messages context only. Authority
// fields are already frozen and are not re-read from params.
func resolveGoalRuntimeSpec(params map[string]interface{}, spec *AgentGoalSpec) (*AgentGoalSpec, error) {
	if spec == nil {
		return nil, fmt.Errorf("missing goal spec")
	}
	copySpec := *spec
	ictx := NewInputContext(params)
	if len(spec.Messages) > 0 {
		resolved, err := ResolveValue(ictx, spec.Messages)
		if err != nil {
			return nil, err
		}
		messages, ok := resolved.([]any)
		if !ok {
			return nil, fmt.Errorf("resolved messages are not an array")
		}
		copySpec.Messages = messages
	} else if strings.TrimSpace(spec.Prompt) != "" {
		resolved, err := ResolveString(ictx, spec.Prompt)
		if err != nil {
			return nil, err
		}
		copySpec.Prompt = resolved
	}
	return &copySpec, nil
}

func toolLoopRunFieldValue(params map[string]interface{}, key string) string {
	config, _ := params[StrConfig].(map[string]interface{})
	runtime, _ := config[cfgKeyAgentRuntime].(map[string]interface{})
	value, _ := runtime[key].(string)
	return strings.TrimSpace(value)
}

// goalExecutionDeadline computes the absolute deadline for the whole goal
// lifecycle. It never grants a fresh MaxElapsed window after initialization.
func goalExecutionDeadline(spec *AgentGoalSpec, inherited time.Time, now time.Time) time.Time {
	elapsed := defaultGoalMaxElapsed
	if spec != nil && spec.Budgets.MaxElapsed > 0 {
		elapsed = spec.Budgets.MaxElapsed
	}
	deadline := now.Add(elapsed)
	if !inherited.IsZero() && inherited.Before(deadline) {
		deadline = inherited
	}
	return deadline
}

func goalDeadlineTerminal(inherited time.Time, now time.Time) (GoalTerminalStatus, string, string) {
	if !inherited.IsZero() && !now.Before(inherited) {
		return GoalCancelled, "deadline_exceeded", "group deadline exceeded"
	}
	return GoalBudgetExhausted, "budget_elapsed", "goal run budget elapsed"
}

func goalInitializationFailureResult(spec *AgentGoalSpec, runID, traceID string, status GoalTerminalStatus, code, reason string) GoalRunResult {
	return GoalRunResult{
		Status: status, ReasonCode: code, Reason: reason,
		RunID: runID, TraceID: traceID,
		GoalID: spec.GoalID, GoalSpecHash: spec.SpecHash,
		RequestedGoal:      goalSummary(spec),
		ToolCalls:          []ToolCallSummary{},
		Evidence:           []EvidenceSummary{},
		CompletionDecision: CompletionDecision{DecidedBy: "deterministic_verifier"},
	}
}

func goalSpecFromRuntime(params map[string]interface{}) *AgentGoalSpec {
	if params == nil {
		return nil
	}
	config, _ := params[StrConfig].(map[string]interface{})
	runtime, _ := config[cfgKeyAgentRuntime].(map[string]interface{})
	spec, _ := runtime["goal_spec"].(*AgentGoalSpec)
	return spec
}

func goalIdentityFromRuntime(params map[string]interface{}) AgentIdentity {
	config, _ := params[StrConfig].(map[string]interface{})
	runtime, _ := config[cfgKeyAgentRuntime].(map[string]interface{})
	if identity, ok := runtime["goal_identity"].(*AgentIdentity); ok && identity != nil {
		return *identity
	}
	// No fallback to parseRuntimeIdentity: the engine must install the
	// trusted identity pointer for a goal step. A forged map in runtime
	// config cannot become agent identity.
	return AgentIdentity{}
}

// goalRunConfig carries only trusted inputs.
type goalRunConfig struct {
	Spec                 *AgentGoalSpec
	Identity             AgentIdentity
	DB                   cdb.Handler
	Registry             *AgentToolRegistry
	EvidenceRegistry     *GoalEvidenceRegistry
	AllowedOperations    map[string]bool
	MutationAllowed      bool
	MCPMutationAllowed   bool
	MCPAllowedOperations map[string]bool
	CrawlRuntime         *CrawlRequestToolRuntime
	MCPManager           *MCPToolManager
	Principal            MCPMutationPrincipal
	Provider             LLMProvider
	RunID                string
	TraceID              string
	CorrelationID        string
	GroupDeadline        time.Time
	ExecutionDeadline    time.Time
	Ctx                  context.Context
}

type goalRunState struct {
	messages           []any
	summaries          []ToolCallSummary
	toolAttempts       int
	toolCalls          int
	totalResultBytes   int
	consecutiveDenials int
	seenIDs            map[string]bool
	modelRounds        int
	deadline           time.Time
	modelClaim         bool
}

// RunGoal executes the bounded synchronous goal loop.
func RunGoal(config goalRunConfig) (GoalRunResult, error) {
	if config.MCPManager != nil {
		defer func() { _ = config.MCPManager.Close() }()
	}
	if config.Spec == nil {
		return GoalRunResult{}, fmt.Errorf("goal run denied: missing spec")
	}
	run := &goalRunner{
		config: config,
		result: GoalRunResult{
			Status: GoalIncomplete, ReasonCode: "goal_not_started",
			RunID: config.RunID, TraceID: config.TraceID,
			CorrelationID: config.CorrelationID, GoalID: config.Spec.GoalID,
			GoalSpecHash: config.Spec.SpecHash, RequestedGoal: goalSummary(config.Spec),
			ToolCalls: []ToolCallSummary{}, Evidence: []EvidenceSummary{},
		},
	}
	if strings.TrimSpace(run.result.RunID) == "" {
		run.result.RunID = uuid.NewString()
	}
	if strings.TrimSpace(run.result.TraceID) == "" {
		run.result.TraceID = uuid.NewString()
	}
	if !identityGrants(config.Identity, "goal_execution") || trustLevelRank(config.Identity.TrustLevel) < minToolTrustRank {
		return run.finish(GoalDenied, "goal_execution_required", "goal_execution capability and trusted standing are required"), nil
	}
	if goalSpecRequiresDBEvidence(config.Spec) && !identityGrants(config.Identity, "db_read") {
		return run.finish(GoalDenied, "missing_db_read", "db_read capability is required for database-backed evidence"), nil
	}
	nonce, err := newGoalRunNonce()
	if err != nil {
		return run.finish(GoalVerificationError, "run_nonce_failed", "could not initialize run"), nil
	}
	timeout := config.Spec.Budgets.MaxElapsed
	if timeout <= 0 {
		timeout = defaultGoalMaxElapsed
	}
	if !config.GroupDeadline.IsZero() {
		remaining := time.Until(config.GroupDeadline)
		if remaining <= 0 {
			return run.finish(GoalCancelled, "deadline_exceeded", "group deadline exceeded"), nil
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	parent := config.Ctx
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		if !config.ExecutionDeadline.IsZero() && !time.Now().Before(config.ExecutionDeadline) {
			return run.finish(run.timeoutStatus(), run.timeoutReason(), "goal loop expired"), nil
		}
		return run.finish(GoalCancelled, "cancelled_by_caller", "run context expired"), nil
	}
	deadline := time.Now().Add(timeout)
	if inherited, ok := parent.Deadline(); ok && inherited.Before(deadline) {
		deadline = inherited
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	actualDeadline, _ := ctx.Deadline()
	run.ctx = ctx
	run.deadline = actualDeadline
	run.ledger = NewGoalEvidenceLedger(
		run.result.RunID, nonce, run.result.TraceID, config.Spec.GoalID,
		config.Spec.SpecHash, config.Identity.AgentID, config.Identity.Owner,
		config.Spec.AllowedSources, config.Spec.Evidence, config.Spec.Budgets, actualDeadline)
	run.queries = map[string]EvidenceQueryResult{}
	run.state = &goalRunState{
		messages: initialGoalMessages(config.Spec),
		seenIDs:  map[string]bool{},
		deadline: actualDeadline,
	}
	auth := ToolAuthContext{
		Identity: config.Identity, Enforcement: true,
		Allowlist:            stringSet(config.Spec.Allowlist),
		AllowedSources:       sliceToSourceSet(config.Spec.AllowedSources),
		AllowedOperations:    config.AllowedOperations,
		MutationAllowed:      config.MutationAllowed,
		MCPMutationAllowed:   config.MCPMutationAllowed,
		MCPAllowedOperations: config.MCPAllowedOperations,
		RunID:                run.result.RunID, TraceID: run.result.TraceID,
	}
	run.auth = auth
	emitGoalAudit(run.result.RunID, run.result.TraceID, config.Identity.AgentID, config.Identity.Name, config.Identity.Owner, config.Spec.GoalID, "", auditOutcomeAllowed, "goal_run_started", "goal run started", nil)

	if terminal, done := run.refreshAndEvaluate(false); done {
		return terminal, nil
	}
	if !identityGrants(config.Identity, "ai_reasoning") {
		return run.finish(GoalDenied, "missing_ai_reasoning", "ai_reasoning capability is required before model execution"), nil
	}

	// Provider capability and AI policy gates run after the initial trusted
	// evidence evaluation (which may prove the goal without a model call) and
	// before any provider HTTP request.
	if ProviderCapabilitiesFor(config.Provider, config.Spec.Model).ToolCalls == LLMCapabilityUnsupported {
		emitGoalAudit(run.result.RunID, run.result.TraceID, config.Identity.AgentID, config.Identity.Name, config.Identity.Owner, config.Spec.GoalID, "", auditOutcomeDenied, "provider_tool_calls_unsupported", "provider does not support tool calls", nil)
		return run.finish(GoalDenied, "provider_tool_calls_unsupported", "provider does not support tool calls"), nil
	}
	if err := enforceAIUsagePolicyForIdentity(config.Identity, LLMRequest{Provider: config.Spec.Provider, Model: config.Spec.Model}); err != nil {
		emitGoalAudit(run.result.RunID, run.result.TraceID, config.Identity.AgentID, config.Identity.Name, config.Identity.Owner, config.Spec.GoalID, "", auditOutcomeDenied, "ai_policy_denied", "provider or model denied by policy", nil)
		return run.finish(GoalDenied, "ai_policy_denied", "provider or model denied by policy"), nil
	}

	advertised := advertiseTools(config.Registry, auth.Allowlist)
	providerName := config.Spec.Provider
	for round := 1; ; round++ {
		if round > config.Spec.Budgets.MaxModelRounds {
			return run.finish(GoalBudgetExhausted, "budget_model_rounds", "model round budget exhausted"), nil
		}
		if err := ctx.Err(); err != nil {
			return run.finish(run.timeoutStatus(), run.timeoutReason(), "goal loop expired"), nil
		}
		request := LLMRequest{
			Provider: providerName, URL: config.Spec.URL, Auth: config.Spec.Auth,
			Model:      config.Spec.Model,
			Messages:   append([]any(nil), run.state.messages...),
			Tools:      advertised,
			ToolChoice: LLMToolChoice{Mode: "auto"}, HasToolChoice: true,
		}
		rawResponse, err := executeProviderCall(config.Provider, ctx, request)
		if err != nil {
			if ctx.Err() != nil {
				return run.finish(run.timeoutStatus(), run.timeoutReason(), "goal loop expired"), nil
			}
			return run.finish(GoalProviderError, "provider_error", "provider request failed"), nil
		}
		run.state.modelRounds = round
		payload, err := ExtractChatPayload(rawResponse)
		if err != nil {
			return run.finish(GoalProviderError, "malformed_provider_reply", "provider reply was malformed"), nil
		}
		normalized, err := NormalizeChatCompletion(payload)
		if err != nil {
			return run.finish(GoalProviderError, "malformed_provider_reply", "provider reply was malformed"), nil
		}
		if strings.TrimSpace(normalized.Content) != "" {
			run.state.modelClaim = true
		}
		if len(normalized.ToolCalls) == 0 {
			return run.evaluateTerminal(true), nil
		}
		ids, err := assignCallIDs(normalized.ToolCalls, round, run.state.seenIDs)
		if err != nil {
			return run.finish(GoalDenied, ToolDenyDuplicateCallID, "duplicate tool call ID"), nil
		}
		for _, id := range ids {
			run.state.seenIDs[id] = true
		}
		run.state.messages = append(run.state.messages, buildAssistantEcho(normalized, ids))
		for i, call := range normalized.ToolCalls {
			terminal, done := run.processToolCall(ctx, call, ids[i])
			if done {
				return terminal, nil
			}
		}
		if terminal, done := run.refreshAndEvaluate(true); done {
			return terminal, nil
		}
		if run.state.toolAttempts >= config.Spec.Budgets.MaxToolCalls {
			return run.finish(GoalBudgetExhausted, "budget_tool_calls", "tool call budget exhausted"), nil
		}
	}
}

func (r *goalRunner) processToolCall(ctx context.Context, call LLMToolCall, callID string) (GoalRunResult, bool) {
	r.state.toolAttempts++
	if r.state.toolAttempts > r.config.Spec.Budgets.MaxToolCalls {
		return r.finish(GoalBudgetExhausted, "budget_tool_calls", "tool call budget exhausted"), true
	}
	tool, ok := r.config.Registry.Get(call.Name)
	if !ok {
		return r.denyCall(call, callID, ToolDenyUnknownTool)
	}
	args, err := normalizeToolArguments(call.Arguments)
	if err != nil {
		return r.denyCall(call, callID, ToolDenyArguments)
	}
	if err := AuthorizeToolCall(r.auth, tool, args); err != nil {
		code := ToolDenyArguments
		var authErr *ToolAuthError
		if errors.As(err, &authErr) {
			code = authErr.Code
		}
		return r.denyCall(call, callID, code)
	}
	if err := ctx.Err(); err != nil {
		return r.finish(r.timeoutStatus(), r.timeoutReason(), "goal loop expired"), true
	}
	runtime := ToolRuntime{DB: r.config.DB, Auth: r.auth, Deadline: r.state.deadline, CallID: callID, Crawl: r.config.CrawlRuntime, Principal: r.config.Principal}
	callCtx, cancel := context.WithDeadline(ctx, r.state.deadline)
	defer cancel()
	handlerResult, err := tool.Execute(ContextWithToolRuntime(callCtx, runtime), args)
	if err != nil {
		var authErr *ToolAuthError
		if errors.As(err, &authErr) {
			// Propose already recorded a durable audit for its own denial.
			return r.denyCallWithAudit(call, callID, authErr.Code, false)
		}
	}
	status := "executed"
	reasonCode := ""
	var observation map[string]any
	if failure, failed := asToolExecutionFailure(err); failed {
		observation = sanitizeToolResult(failure.Observation)
		if len(observation) == 0 {
			observation = map[string]any{"error": "tool execution failed"}
		}
		status = "failed"
		reasonCode = failure.Code
		if strings.TrimSpace(reasonCode) == "" {
			reasonCode = "tool_execution_failed"
		}
	} else if err != nil {
		observation = map[string]any{"error": sanitizeToolError(err)}
		status = "failed"
		reasonCode = "tool_execution_failed"
	} else {
		observation = sanitizeToolResult(handlerResult)
	}
	serialized, serErr := json.Marshal(observation)
	if serErr != nil || len(serialized) > hardMaxResultBytes {
		observation = map[string]any{"error": "result_too_large", "truncated": true}
		serialized, _ = json.Marshal(observation)
		status = "failed"
		reasonCode = "result_too_large"
	}
	r.state.totalResultBytes += len(serialized)
	if r.state.totalResultBytes > r.config.Spec.Budgets.MaxTotalResultBytes {
		return r.finish(GoalBudgetExhausted, "budget_result_bytes", "tool result budget exhausted"), true
	}
	r.state.messages = append(r.state.messages, toolResultMessage(callID, observation))
	r.state.summaries = append(r.state.summaries, ToolCallSummary{
		Name: call.Name, ID: callID, Index: call.Index, Status: status,
		ReasonCode: reasonCode, ResultBytes: len(serialized),
	})
	outcome := auditOutcomeFor(status)
	emitGoalAudit(r.result.RunID, r.result.TraceID, r.config.Identity.AgentID, r.config.Identity.Name, r.config.Identity.Owner, r.config.Spec.GoalID, call.Name, outcome, auditReasonFor(status, reasonCode), "tool execution finished", nil)
	if status == "executed" {
		r.state.toolCalls++
		r.state.consecutiveDenials = 0
		if terminal, done := r.recordToolEvidence(ctx, call.Name, args, observation); done {
			return terminal, true
		}
	} else if r.config.Spec.StopOnToolError {
		return r.finish(GoalToolError, "tool_execution_failed", "tool execution failed"), true
	}
	return GoalRunResult{}, false
}

func (r *goalRunner) denyCall(call LLMToolCall, callID, code string) (GoalRunResult, bool) {
	return r.denyCallWithAudit(call, callID, code, true)
}

func (r *goalRunner) denyCallWithAudit(call LLMToolCall, callID, code string, audit bool) (GoalRunResult, bool) {
	r.state.consecutiveDenials++
	emitGoalAudit(r.result.RunID, r.result.TraceID, r.config.Identity.AgentID, r.config.Identity.Name, r.config.Identity.Owner, r.config.Spec.GoalID, call.Name, auditOutcomeDenied, code, "tool proposal denied", nil)
	if audit {
		record := func(serverID, alias, policyID string) {
			recordMCPAdmissionDeniedDB(r.ctx, r.config.DB, MCPAdmissionDeniedEvent{
				EventType:  MCPAuditEventAdmissionDenied,
				Stage:      MCPAuditStageGoal,
				RunID:      r.result.RunID,
				TraceID:    r.result.TraceID,
				CallID:     callID,
				Identity:   r.config.Identity,
				Principal:  r.config.Principal,
				ServerID:   serverID,
				ToolAlias:  alias,
				PolicyID:   policyID,
				ReasonCode: code,
			})
		}
		if tool, ok := r.config.Registry.Get(call.Name); ok {
			if info, isMCPProposal := mcpMutationProposalAuditIdentity(tool); isMCPProposal {
				record(info.ServerID, info.ToolAlias, info.PolicyID)
			}
		} else {
			// A model can name an operator-policy alias that was never
			// registered. Keep the unknown-tool denial, but record the known
			// policy identity when one exists.
			policy := CurrentMCPMutationPolicy()
			if operation, found := policy.OperationByAlias(call.Name); found {
				record(operation.ServerID, operation.ToolAlias, policy.PolicyID)
			}
		}
	}
	r.state.summaries = append(r.state.summaries, ToolCallSummary{
		Name: call.Name, ID: callID, Index: call.Index, Status: "denied", ReasonCode: code,
	})
	r.state.messages = append(r.state.messages, toolResultMessage(callID, map[string]any{"error": "denied: " + code}))
	if r.state.consecutiveDenials >= r.config.Spec.Budgets.MaxConsecutiveDenials {
		return r.finish(GoalDenied, "consecutive_denials", "repeated denied proposals"), true
	}
	return GoalRunResult{}, false
}

func (r *goalRunner) recordToolEvidence(ctx context.Context, toolName string, args map[string]any, payload map[string]any) (GoalRunResult, bool) {
	if !kindAllowed(r.config.Spec.Evidence.Allow, EvidenceToolObservation) {
		return GoalRunResult{}, false
	}
	meta, ok := r.config.Registry.EvidenceMetadata(toolName)
	if !ok || !meta.Supported || !meta.ReadOnly {
		return GoalRunResult{}, false
	}
	selector := EvidenceSelector{Tool: toolName, ArgumentsSHA256: argumentsHash(args)}
	sourceIDs := sourceIDsFromArgs(args)
	obs := EvidenceObservation{
		Kind: EvidenceToolObservation, Producer: "tool:" + toolName,
		SourceIDs: sourceIDs, Operation: "read", Payload: payload,
		Tool: toolName, ArgumentsSHA256: selector.ArgumentsSHA256,
		SelectorSHA256: selectorHash(selector), ObservedAt: time.Now().UTC(),
	}
	evidenceID, err := r.ledger.Record(ctx, obs)
	if err == nil {
		emitGoalAudit(r.result.RunID, r.result.TraceID, r.config.Identity.AgentID, r.config.Identity.Name, r.config.Identity.Owner, r.config.Spec.GoalID, toolName, auditOutcomeAllowed, "evidence_recorded", "trusted evidence recorded", []string{evidenceID})
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrEvidenceBudget):
			return r.finish(GoalBudgetExhausted, "budget_evidence", "evidence budget exhausted"), true
		case errors.Is(err, ErrEvidenceDenied):
			return r.finish(GoalDenied, "evidence_out_of_scope", "evidence outside run scope"), true
		default:
			return r.finish(GoalVerificationError, "evidence_record_failed", "evidence could not be recorded"), true
		}
	}
	return GoalRunResult{}, false
}

func sourceIDsFromArgs(args map[string]any) []uint64 {
	if args == nil {
		return nil
	}
	raw, ok := args["source_id"]
	if !ok || raw == nil {
		return nil
	}
	id, err := toSourceIDArg(raw)
	if err != nil {
		return nil
	}
	return []uint64{id}
}

func (r *goalRunner) refreshAndEvaluate(modelRound bool) (GoalRunResult, bool) {
	if err := r.ctx.Err(); err != nil {
		return r.finish(r.timeoutStatus(), r.timeoutReason(), "goal loop expired"), true
	}
	if err := refreshGoalEvidence(r.ctx, r.config.Spec, r.config.EvidenceRegistry, r.ledger, r.queries, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, ErrEvidenceBudget):
			return r.finish(GoalBudgetExhausted, "budget_evidence", "evidence budget exhausted"), true
		case errors.Is(err, ErrEvidenceDenied):
			return r.finish(GoalDenied, "evidence_out_of_scope", "evidence outside run scope"), true
		case errors.Is(err, ErrEvidenceUnavailable):
			// No observation is available; evaluation stays incomplete.
		default:
			return r.finish(GoalVerificationError, "evidence_refresh_failed", "evidence could not be verified"), true
		}
	}
	resources := readGoalResources(r.ctx, r.config.DB, r.config.Spec, r.result.RunID)
	evaluation, err := EvaluateGoal(GoalEvalInput{
		Spec: r.config.Spec, Records: r.ledger.Snapshot(),
		Resources: resources, Queries: r.queries, Now: time.Now().UTC(),
		LedgerID: r.ledger.ID(), RunID: r.result.RunID, GoalID: r.config.Spec.GoalID,
		GoalSpecHash: r.config.Spec.SpecHash, RunNonceHash: r.ledger.RunNonceHash(),
		AgentID: r.config.Identity.AgentID, Owner: r.config.Identity.Owner,
	})
	if err != nil {
		return r.finish(GoalVerificationError, "verification_error", "criteria could not be evaluated"), true
	}
	r.evaluation = evaluation
	if evaluation.State == "met" {
		return r.finish(GoalCriteriaMet, criteriaReason(r.config.Spec.Acceptance.Mode), "acceptance criteria met"), true
	}
	if modelRound {
		if evaluation.State == "error" {
			return r.finish(GoalVerificationError, "verification_error", "criteria could not be evaluated"), true
		}
		if hasDeniedCriterion(evaluation) {
			return r.finish(GoalDenied, "evidence_out_of_scope", "evidence outside run scope"), true
		}
	}
	return GoalRunResult{}, false
}

func (r *goalRunner) evaluateTerminal(modelFinal bool) GoalRunResult {
	if r.ctx.Err() != nil {
		return r.finish(r.timeoutStatus(), r.timeoutReason(), "goal loop expired")
	}
	if err := refreshGoalEvidence(r.ctx, r.config.Spec, r.config.EvidenceRegistry, r.ledger, r.queries, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, ErrEvidenceBudget):
			return r.finish(GoalBudgetExhausted, "budget_evidence", "evidence budget exhausted")
		case errors.Is(err, ErrEvidenceDenied):
			return r.finish(GoalDenied, "evidence_out_of_scope", "evidence outside run scope")
		default:
			return r.finish(GoalVerificationError, "evidence_refresh_failed", "evidence could not be verified")
		}
	}
	resources := readGoalResources(r.ctx, r.config.DB, r.config.Spec, r.result.RunID)
	evaluation, err := EvaluateGoal(GoalEvalInput{
		Spec: r.config.Spec, Records: r.ledger.Snapshot(),
		Resources: resources, Queries: r.queries, Now: time.Now().UTC(),
		LedgerID: r.ledger.ID(), RunID: r.result.RunID, GoalID: r.config.Spec.GoalID,
		GoalSpecHash: r.config.Spec.SpecHash, RunNonceHash: r.ledger.RunNonceHash(),
		AgentID: r.config.Identity.AgentID, Owner: r.config.Identity.Owner,
	})
	if err != nil {
		return r.finish(GoalVerificationError, "verification_error", "criteria could not be evaluated")
	}
	r.evaluation = evaluation
	if evaluation.State == "met" {
		return r.finish(GoalCriteriaMet, criteriaReason(r.config.Spec.Acceptance.Mode), "acceptance criteria met")
	}
	if evaluation.State == "error" {
		return r.finish(GoalVerificationError, "verification_error", "criteria could not be evaluated")
	}
	if hasDeniedCriterion(evaluation) {
		return r.finish(GoalDenied, "evidence_out_of_scope", "evidence outside scope")
	}
	reason := "criteria_not_met"
	if evaluation.State == "incomplete" {
		reason = "no_matching_evidence"
	}
	return r.finish(GoalIncomplete, reason, "acceptance criteria were not proven")
}

func (r *goalRunner) finish(status GoalTerminalStatus, code, reason string) GoalRunResult {
	r.result.Status = status
	r.result.ReasonCode = code
	r.result.Reason = reason
	if r.state != nil {
		r.result.ModelRounds = r.state.modelRounds
		r.result.ToolCalls = append([]ToolCallSummary(nil), r.state.summaries...)
		r.result.ToolCallCount = r.state.toolCalls
	}
	if r.result.ToolCalls == nil {
		r.result.ToolCalls = []ToolCallSummary{}
	}
	if r.evaluation.Criteria != nil {
		r.result.Evaluation = r.evaluation
	}
	if r.ledger != nil {
		records := r.ledger.Snapshot()
		for _, record := range records {
			r.result.Evidence = append(r.result.Evidence, EvidenceSummary{
				EvidenceID: record.EvidenceID, Kind: record.Kind,
				TrustClass: record.TrustClass, Producer: record.Producer,
				SourceIDs:  append([]uint64(nil), record.SourceIDs...),
				ObservedAt: record.ObservedAt, ExpiresAt: record.ExpiresAt,
				PayloadSHA256: record.PayloadSHA256,
			})
		}
	}
	modelClaim := false
	if r.state != nil {
		modelClaim = r.state.modelClaim
	}
	r.result.CompletionDecision = CompletionDecision{
		DecidedBy: "deterministic_verifier", ModelClaimObserved: modelClaim,
		ModelClaimUsed: false, CriteriaMet: status == GoalCriteriaMet,
	}
	evidenceIDs := make([]string, 0, len(r.result.Evidence))
	for _, evidence := range r.result.Evidence {
		evidenceIDs = append(evidenceIDs, evidence.EvidenceID)
	}
	outcome := auditOutcomeDenied
	if status == GoalCriteriaMet {
		outcome = auditOutcomeAllowed
	} else if status != GoalDenied {
		outcome = auditOutcomeError
	}
	emitGoalAudit(r.result.RunID, r.result.TraceID, r.config.Identity.AgentID, r.config.Identity.Name, r.config.Identity.Owner, r.result.GoalID, "", outcome, code, reason, evidenceIDs)
	return r.result
}

func (r *goalRunner) timeoutStatus() GoalTerminalStatus {
	if r.config.GroupDeadline.IsZero() || time.Now().Before(r.config.GroupDeadline) {
		return GoalBudgetExhausted
	}
	return GoalCancelled
}

func (r *goalRunner) timeoutReason() string {
	if r.timeoutStatus() == GoalCancelled {
		return "deadline_exceeded"
	}
	return "budget_elapsed"
}

type goalRunner struct {
	config     goalRunConfig
	result     GoalRunResult
	state      *goalRunState
	ledger     *goalEvidenceLedger
	queries    map[string]EvidenceQueryResult
	auth       ToolAuthContext
	evaluation EvaluationResult
	ctx        context.Context
	deadline   time.Time
}

func newGoalRunNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[strings.TrimSpace(value)] = true
	}
	return out
}

func initialGoalMessages(spec *AgentGoalSpec) []any {
	system := "You are a CROWler goal agent. Objective: " + spec.Objective +
		"\nUse only the permitted tools. Your final text is not proof of completion; a deterministic verifier decides based on trusted evidence."
	out := []any{map[string]any{"role": "system", "content": system}}
	if len(spec.Messages) > 0 {
		out = append(out, spec.Messages...)
	} else if strings.TrimSpace(spec.Prompt) != "" {
		out = append(out, map[string]any{"role": "user", "content": spec.Prompt})
	}
	return out
}

func goalSummary(spec *AgentGoalSpec) GoalSummary {
	if spec == nil {
		return GoalSummary{}
	}
	summary := GoalSummary{
		GoalID: spec.GoalID, Objective: spec.Objective,
		AcceptanceMode: spec.Acceptance.Mode,
	}
	for _, criterion := range spec.Acceptance.Criteria {
		summary.Criteria = append(summary.Criteria, GoalCriterionSummary{
			ID: criterion.ID, Description: criterion.Description,
		})
	}
	return summary
}

func criteriaReason(mode AcceptanceMode) string {
	if mode == AcceptanceAny {
		return "criteria_any_met"
	}
	return "criteria_all_met"
}

func hasDeniedCriterion(evaluation EvaluationResult) bool {
	for _, criterion := range evaluation.Criteria {
		if criterion.Result == string(CriterionDenied) {
			return true
		}
	}
	return false
}

func goalRunResultMap(result GoalRunResult) map[string]any {
	criteria := make([]any, 0, len(result.Evaluation.Criteria))
	for _, criterion := range result.Evaluation.Criteria {
		criteria = append(criteria, map[string]any{
			"id": criterion.ID, "result": criterion.Result,
			"reason_code":     criterion.ReasonCode,
			"evidence_ids":    append([]string(nil), criterion.EvidenceIDs...),
			"evidence_source": criterion.EvidenceSource,
			"observed_value":  criterion.ObservedValue,
			"observed_at":     criterion.ObservedAt,
		})
	}
	evidence := make([]any, 0, len(result.Evidence))
	for _, record := range result.Evidence {
		evidence = append(evidence, map[string]any{
			"evidence_id": record.EvidenceID, "kind": string(record.Kind),
			"trust_class": string(record.TrustClass), "producer": record.Producer,
			"source_ids":  append([]uint64(nil), record.SourceIDs...),
			"observed_at": record.ObservedAt, "expires_at": record.ExpiresAt,
			"payload_sha256": record.PayloadSHA256,
		})
	}
	requested := map[string]any{
		"goal_id": result.RequestedGoal.GoalID, "objective": result.RequestedGoal.Objective,
		"acceptance_mode": string(result.RequestedGoal.AcceptanceMode),
	}
	goalCriteria := make([]any, 0, len(result.RequestedGoal.Criteria))
	for _, criterion := range result.RequestedGoal.Criteria {
		goalCriteria = append(goalCriteria, map[string]any{
			"id": criterion.ID, "description": criterion.Description,
		})
	}
	requested["criteria"] = goalCriteria
	toolCalls := make([]any, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		toolCalls = append(toolCalls, map[string]any{
			"name": call.Name, "id": call.ID, "index": call.Index,
			"status": call.Status, "reason_code": call.ReasonCode,
			"result_bytes": call.ResultBytes,
		})
	}
	return map[string]any{
		"status": string(result.Status), "reason_code": result.ReasonCode,
		"reason": result.Reason, "run_id": result.RunID,
		"trace_id": result.TraceID, "correlation_id": result.CorrelationID,
		"goal_id": result.GoalID, "goal_spec_hash": result.GoalSpecHash,
		"requested_goal": requested,
		"evaluation": map[string]any{
			"state": result.Evaluation.State, "criteria": criteria,
			"evaluated_at": result.Evaluation.EvaluatedAt,
		},
		"evidence": evidence,
		"completion_decision": map[string]any{
			"decided_by":           result.CompletionDecision.DecidedBy,
			"model_claim_observed": result.CompletionDecision.ModelClaimObserved,
			"model_claim_used":     result.CompletionDecision.ModelClaimUsed,
			"criteria_met":         result.CompletionDecision.CriteriaMet,
		},
		"tool_calls":      toolCalls,
		"model_rounds":    result.ModelRounds,
		"tool_call_count": result.ToolCallCount,
	}
}

// emitGoalAudit records a goal lifecycle event with IDs and reason codes only.
func emitGoalAudit(runID, traceID, agentID, agentName, owner, goalID, tool, outcome, reasonCode, reason string, evidenceIDs []string) {
	if AgentsEngine == nil {
		return
	}
	AgentsEngine.appendAudit(AuditEvent{
		RunID: runID, TraceID: traceID,
		AgentID: agentID, AgentName: agentName, Owner: owner,
		Action: "AgentGoalRun", RequiredCapability: "goal_execution",
		CapabilitiesUsed: []string{"goal_execution"},
		Outcome:          outcome, Reason: reason, DelegationTarget: tool,
		GoalID: goalID, ReasonCode: reasonCode, EvidenceIDs: evidenceIDs,
	})
}
