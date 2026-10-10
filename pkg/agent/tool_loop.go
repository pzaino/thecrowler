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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Phase 3 executes explicitly enabled, bounded, synchronous model/tool
// loops. One provider call yields one response; tool proposals run
// sequentially in provider order with no retries, no nested loops, and no
// dispatch beyond vetted registry handlers. Model names and arguments stay
// untrusted throughout.

// Loop hard ceilings. Configured limits must be positive and at or below
// these values.
const (
	hardMaxModelRounds      = 4
	hardMaxToolCalls        = 8
	hardMaxResultBytes      = 16 << 10
	hardMaxTotalResultBytes = 64 << 10
	hardLoopTimeout         = 30 * time.Second
	// maxConsecutiveToolDenials stops loops that keep proposing denied calls.
	maxConsecutiveToolDenials = 3
)

// Loop terminal statuses.
const (
	ToolLoopCompleted       = "completed"
	ToolLoopDenied          = "denied"
	ToolLoopBudgetExhausted = "budget_exhausted"
	ToolLoopCancelled       = "cancelled"
	ToolLoopProviderError   = "provider_error"
	ToolLoopToolError       = "tool_error"
)

// ToolLoopLimits bounds one run. Every field must be positive and within
// the hard ceilings above.
type ToolLoopLimits struct {
	MaxModelRounds      int
	MaxToolCalls        int
	MaxResultBytes      int
	MaxTotalResultBytes int
	Timeout             time.Duration
}

// DefaultToolLoopLimits returns the ceiling values.
func DefaultToolLoopLimits() ToolLoopLimits {
	return ToolLoopLimits{
		MaxModelRounds:      hardMaxModelRounds,
		MaxToolCalls:        hardMaxToolCalls,
		MaxResultBytes:      hardMaxResultBytes,
		MaxTotalResultBytes: hardMaxTotalResultBytes,
		Timeout:             hardLoopTimeout,
	}
}

// ValidateLimits checks positivity and ceilings.
func ValidateLimits(limits ToolLoopLimits) error {
	if limits.MaxModelRounds <= 0 || limits.MaxModelRounds > hardMaxModelRounds {
		return fmt.Errorf("invalid max_model_rounds: want 1..%d", hardMaxModelRounds)
	}
	if limits.MaxToolCalls <= 0 || limits.MaxToolCalls > hardMaxToolCalls {
		return fmt.Errorf("invalid max_tool_calls: want 1..%d", hardMaxToolCalls)
	}
	if limits.MaxResultBytes <= 0 || limits.MaxResultBytes > hardMaxResultBytes {
		return fmt.Errorf("invalid max_result_bytes: want 1..%d", hardMaxResultBytes)
	}
	if limits.MaxTotalResultBytes <= 0 || limits.MaxTotalResultBytes > hardMaxTotalResultBytes {
		return fmt.Errorf("invalid max_total_result_bytes: want 1..%d", hardMaxTotalResultBytes)
	}
	if limits.Timeout <= 0 || limits.Timeout > hardLoopTimeout {
		return fmt.Errorf("invalid timeout: want 1..%s", hardLoopTimeout)
	}
	return nil
}

// ToolLoopConfig drives one bounded run. Everything authorization-relevant
// (registry, allowlist, identity, enforcement, scopes, provider endpoint)
// comes from trusted operator configuration, never from model output.
type ToolLoopConfig struct {
	Provider        LLMProvider
	ProviderName    string
	Model           string
	URL             string
	Auth            string
	Prompt          string
	Messages        []any
	Registry        *AgentToolRegistry
	Allowlist       []string
	Identity        AgentIdentity
	Enforcement     bool
	AllowedSources  []uint64
	DB              cdb.Handler
	Limits          ToolLoopLimits
	StopOnToolError bool
	RunID           string
	TraceID         string
	GroupDeadline   time.Time
	Ctx             context.Context
}

// ToolCallSummary is the sanitized per-call record: names, IDs, and reason
// codes only, never arguments or results.
type ToolCallSummary struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Index       int    `json:"index"`
	Status      string `json:"status"`
	ReasonCode  string `json:"reason_code,omitempty"`
	ResultBytes int    `json:"result_bytes,omitempty"`
}

// ToolLoopResult is the typed terminal outcome.
type ToolLoopResult struct {
	Status        string            `json:"status"`
	Content       string            `json:"content"`
	ToolCalls     []ToolCallSummary `json:"tool_calls"`
	ModelRounds   int               `json:"model_rounds"`
	ToolCallCount int               `json:"tool_call_count"`
	Reason        string            `json:"reason"`
	RunID         string            `json:"run_id"`
	TraceID       string            `json:"trace_id"`
}

// ContextAwareProvider is an optional interface for model transports that
// honor cancellation. Providers without it keep legacy behavior.
type ContextAwareProvider interface {
	ExecuteWithContext(ctx context.Context, req LLMRequest) (map[string]interface{}, error)
}

// redactSensitiveKeys replaces values under secret-adjacent keys with a
// marker, recursively. Matching is case-insensitive substring on the key.
func redactSensitiveKeys(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSensitiveKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactSensitiveKeys(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactSensitiveKeys(item)
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "api_key", "apikey",
		"auth", "credential", "private_key", "privatekey", "session_key",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// emitToolAudit records lifecycle events with IDs and reason codes only.
func emitToolAudit(runID, traceID, agentID, agentName, owner, tool, outcome, reason string) {
	if AgentsEngine == nil {
		return
	}
	AgentsEngine.appendAudit(AuditEvent{
		RunID: runID, TraceID: traceID,
		AgentID: agentID, AgentName: agentName, Owner: owner,
		Action:             "AgentToolLoop",
		RequiredCapability: "tool_execution",
		Outcome:            outcome,
		Reason:             reason,
		DelegationTarget:   tool,
	})
}

// RunToolLoop executes the bounded synchronous loop.
func RunToolLoop(config ToolLoopConfig) (ToolLoopResult, error) {
	result := ToolLoopResult{
		ToolCalls: []ToolCallSummary{},
		RunID:     config.RunID,
		TraceID:   config.TraceID,
	}
	if strings.TrimSpace(result.RunID) == "" {
		result.RunID = uuid.NewString()
	}
	if strings.TrimSpace(result.TraceID) == "" {
		result.TraceID = uuid.NewString()
	}
	var state *toolLoopState
	fail := func(status, reason string, err error) (ToolLoopResult, error) {
		result.Status = status
		result.Reason = reason
		if state != nil {
			result.ToolCalls = append([]ToolCallSummary(nil), state.summaries...)
			result.ToolCallCount = state.toolCalls
		}
		return result, err
	}

	if config.Provider == nil {
		return fail(ToolLoopDenied, "no provider configured", fmt.Errorf("tool loop denied: no provider"))
	}
	if config.Registry == nil || config.Registry.Len() == 0 {
		return fail(ToolLoopDenied, "no tool registry", fmt.Errorf("tool loop denied: no tool registry"))
	}
	if len(config.Allowlist) == 0 {
		return fail(ToolLoopDenied, "no tool allowlist", fmt.Errorf("tool loop denied: no tool allowlist"))
	}
	if err := ValidateLimits(config.Limits); err != nil {
		return fail(ToolLoopDenied, err.Error(), fmt.Errorf("tool loop denied: %v", err))
	}
	allowlist := map[string]bool{}
	for _, name := range config.Allowlist {
		if strings.TrimSpace(name) != "" {
			allowlist[strings.TrimSpace(name)] = true
		}
	}
	if len(allowlist) == 0 {
		return fail(ToolLoopDenied, "no tool allowlist", fmt.Errorf("tool loop denied: no tool allowlist"))
	}
	// Every allowlisted name must resolve in the trusted registry.
	for name := range allowlist {
		if _, ok := config.Registry.Get(name); !ok {
			return fail(ToolLoopDenied, "unknown allowlisted tool",
				fmt.Errorf("tool loop denied: allowlisted tool %q is not registered", name))
		}
	}

	auth := ToolAuthContext{
		Identity:       config.Identity,
		Enforcement:    config.Enforcement,
		Allowlist:      allowlist,
		AllowedSources: sliceToSourceSet(config.AllowedSources),
		RunID:          result.RunID,
		TraceID:        result.TraceID,
	}
	agentID, agentName, owner := config.Identity.AgentID, config.Identity.Name, config.Identity.Owner
	emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeAllowed, "tool_loop_started")

	// Effective deadline: the shorter of the loop timeout and any inherited
	// group deadline. Past deadlines fail before any transport.
	effectiveTimeout := config.Limits.Timeout
	if !config.GroupDeadline.IsZero() {
		remaining := time.Until(config.GroupDeadline)
		if remaining <= 0 {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, "group deadline exceeded")
			return fail(ToolLoopDenied, "group deadline exceeded", fmt.Errorf("tool loop denied: group deadline exceeded"))
		}
		if remaining < effectiveTimeout {
			effectiveTimeout = remaining
		}
	}
	parentCtx := config.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if err := parentCtx.Err(); err != nil {
		emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, "run context expired")
		return fail(ToolLoopCancelled, "run context expired", fmt.Errorf("tool loop cancelled: run context expired"))
	}
	loopCtx, loopCancel := context.WithDeadline(parentCtx, time.Now().Add(effectiveTimeout))
	defer loopCancel()

	advertised := advertiseTools(config.Registry, allowlist)
	state = &toolLoopState{
		messages: initialLoopMessages(config),
		seenIDs:  map[string]bool{},
		deadline: time.Now().Add(effectiveTimeout),
		runID:    result.RunID,
		traceID:  result.TraceID,
	}

	for round := 1; ; round++ {
		if round > config.Limits.MaxModelRounds {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, "model round budget exhausted")
			return fail(ToolLoopBudgetExhausted, "model round budget exhausted",
				fmt.Errorf("tool loop budget exhausted: model rounds"))
		}
		if err := loopCtx.Err(); err != nil {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, "loop deadline exceeded")
			return fail(ToolLoopCancelled, "loop deadline exceeded", fmt.Errorf("tool loop cancelled: deadline exceeded"))
		}

		req := LLMRequest{
			Provider:      config.ProviderName,
			URL:           config.URL,
			Auth:          config.Auth,
			Model:         config.Model,
			Messages:      append([]any(nil), state.messages...),
			Tools:         advertised,
			ToolChoice:    LLMToolChoice{Mode: "auto"},
			HasToolChoice: true,
		}
		rawResponse, err := executeProviderCall(config.Provider, loopCtx, req)
		if err != nil {
			// A failure racing an expired loop deadline reports
			// cancellation: the deadline, not the provider, ended the run.
			if loopCtx.Err() != nil {
				emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, "loop deadline exceeded")
				return fail(ToolLoopCancelled, "loop deadline exceeded",
					fmt.Errorf("tool loop cancelled: deadline exceeded"))
			}
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeError, "provider call failed")
			return fail(ToolLoopProviderError, "provider call failed",
				fmt.Errorf("tool loop provider error"))
		}
		result.ModelRounds = round
		payload, err := ExtractChatPayload(rawResponse)
		if err != nil {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeError, "malformed provider reply")
			return fail(ToolLoopProviderError, "malformed provider reply",
				fmt.Errorf("tool loop provider error: malformed reply"))
		}
		normalized, err := NormalizeChatCompletion(payload)
		if err != nil {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeError, "malformed provider reply")
			return fail(ToolLoopProviderError, "malformed provider reply",
				fmt.Errorf("tool loop provider error: malformed reply"))
		}
		if len(normalized.ToolCalls) == 0 {
			result.Status = ToolLoopCompleted
			result.Content = normalized.Content
			result.Reason = "final response"
			result.ToolCalls = append([]ToolCallSummary(nil), state.summaries...)
			result.ToolCallCount = state.toolCalls
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeAllowed, "tool_loop_completed")
			return result, nil
		}

		// Assign effective correlation IDs for the whole batch BEFORE
		// executing anything: any ambiguity denies the batch with zero
		// handler invocations.
		effectiveIDs, err := assignEffectiveIDs(normalized.ToolCalls, round, state)
		if err != nil {
			emitToolAudit(result.RunID, result.TraceID, agentID, agentName, owner, "", auditOutcomeDenied, ToolDenyDuplicateCallID)
			return fail(ToolLoopDenied, "duplicate tool call ID", err)
		}

		// Echo the assistant message with its tool-call structures before
		// any result message, using the same effective IDs.
		state.messages = append(state.messages, buildAssistantEcho(normalized, effectiveIDs))

		for i, call := range normalized.ToolCalls {
			msgs, _, terminal, terminalErr := processToolCall(
				loopCtx, config, auth, call, effectiveIDs[i], state)
			state.messages = msgs
			if terminal != nil {
				terminal.ToolCallCount = state.toolCalls
				return *terminal, terminalErr
			}
		}
	}
}

// assignEffectiveIDs computes one unambiguous correlation ID per call and
// records them in the run-scoped ledger. Valid provider IDs are preserved;
// blank IDs synthesize deterministic run-local values carrying round and
// position. Any repeat — explicit-explicit, synthetic-synthetic across
// rounds, or explicit-synthetic — denies the batch before any execution.
func assignEffectiveIDs(calls []LLMToolCall, round int, state *toolLoopState) ([]string, error) {
	ids := make([]string, len(calls))
	batch := map[string]bool{}
	for i, call := range calls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("local-r%d-c%d", round, i)
		}
		if state.seenIDs[id] || batch[id] {
			return nil, denyTool(ToolDenyDuplicateCallID, "duplicate tool call ID")
		}
		batch[id] = true
		ids[i] = id
	}
	for _, id := range ids {
		state.seenIDs[id] = true
	}
	return ids, nil
}

// processToolCall authorizes, executes, and records one proposed call. It
// returns the extended message history, the call summary for the terminal
// record, and a terminal result when the run must stop.
func processToolCall(
	loopCtx context.Context,
	config ToolLoopConfig,
	auth ToolAuthContext,
	call LLMToolCall,
	callID string,
	state *toolLoopState,
) (messages []any, summary ToolCallSummary, terminal *ToolLoopResult, terminalErr error) {
	agentID, agentName, owner := auth.Identity.AgentID, auth.Identity.Name, auth.Identity.Owner
	deny := func(code string) ([]any, ToolCallSummary, *ToolLoopResult, error) {
		state.consecutiveDenials++
		emitToolAudit(auth.RunID, auth.TraceID, agentID, agentName, owner, call.Name, auditOutcomeDenied, code)
		denied := ToolCallSummary{Name: call.Name, ID: callID, Index: call.Index, Status: "denied", ReasonCode: code}
		state.summaries = append(state.summaries, denied)
		msgs := append(state.messages, toolResultMessage(callID, map[string]any{"error": "denied: " + code}))
		if state.consecutiveDenials >= maxConsecutiveToolDenials {
			result := state.terminal(ToolLoopDenied, "repeated denied proposals")
			return msgs, denied, &result, denyTool(code, "repeated denied proposals")
		}
		return msgs, denied, nil, nil
	}

	tool, ok := config.Registry.Get(call.Name)
	if !ok {
		return deny(ToolDenyUnknownTool)
	}
	// Re-resolve arguments through the Phase 2 normalizer (string forms,
	// object copies, explicit errors) so wire shapes stay uniform.
	args, err := normalizeToolArguments(call.Arguments)
	if err != nil {
		return deny(ToolDenyArguments)
	}
	if state.toolCalls+1 > config.Limits.MaxToolCalls {
		return deny(ToolDenyBudget)
	}
	if err := AuthorizeToolCall(auth, tool, args); err != nil {
		code := ToolDenyArguments
		if authErr, ok := err.(*ToolAuthError); ok {
			code = authErr.Code
		}
		return deny(code)
	}
	if err := loopCtx.Err(); err != nil {
		return deny(ToolDenyContext)
	}

	state.consecutiveDenials = 0
	runtime := ToolRuntime{
		DB:       toolRuntimeDB(config),
		Auth:     auth,
		Deadline: state.deadline,
	}
	callCtx, callCancel := context.WithCancel(loopCtx)
	defer callCancel()
	handlerResult, err := tool.Execute(ContextWithToolRuntime(callCtx, runtime), args)
	var observation map[string]any
	status := "executed"
	if err != nil {
		observation = map[string]any{"error": sanitizeToolError(err)}
		status = "failed"
	} else {
		observation = sanitizeToolResult(handlerResult)
	}
	emitToolAudit(auth.RunID, auth.TraceID, agentID, agentName, owner, call.Name, auditOutcomeFor(status), status)
	serialized, serErr := json.Marshal(observation)
	if serErr != nil || len(serialized) > config.Limits.MaxResultBytes {
		observation = map[string]any{"error": "result_too_large", "truncated": true}
		serialized, _ = json.Marshal(observation)
		status = "failed"
	}
	state.totalResultBytes += len(serialized)
	if state.totalResultBytes > config.Limits.MaxTotalResultBytes {
		result := state.terminal(ToolLoopBudgetExhausted, "tool output budget exhausted")
		return state.messages, ToolCallSummary{}, &result,
			denyTool(ToolDenyBudget, "tool output budget exhausted")
	}
	state.toolCalls++
	state.messages = append(state.messages, toolResultMessage(callID, observation))
	summary = ToolCallSummary{Name: call.Name, ID: callID, Index: call.Index,
		Status: status, ResultBytes: len(serialized)}
	state.summaries = append(state.summaries, summary)
	if status == "failed" && config.StopOnToolError {
		result := state.terminal(ToolLoopToolError, "tool execution failed")
		return state.messages, summary, &result,
			fmt.Errorf("tool loop stopped: tool execution failed")
	}
	return state.messages, summary, nil, nil
}

// toolLoopState carries mutable run accounting across rounds.
type toolLoopState struct {
	messages           []any
	summaries          []ToolCallSummary
	toolCalls          int
	totalResultBytes   int
	consecutiveDenials int
	seenIDs            map[string]bool
	deadline           time.Time
	runID              string
	traceID            string
}

func (s *toolLoopState) terminal(status, reason string) ToolLoopResult {
	return ToolLoopResult{
		Status: status, Reason: reason,
		RunID: s.runID, TraceID: s.traceID,
		ToolCalls: append([]ToolCallSummary(nil), s.summaries...),
	}
}

// advertiseTools exports the registry intersected with the allowlist in
// deterministic order for provider advertisement.
func advertiseTools(registry *AgentToolRegistry, allowlist map[string]bool) []LLMToolDefinition {
	defs := []LLMToolDefinition{}
	if registry == nil {
		return defs
	}
	for _, tool := range registry.Snapshot() {
		if !allowlist[tool.Name()] {
			continue
		}
		params := registry.InputSchemaCopy(tool.Name())
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		defs = append(defs, LLMToolDefinition{
			Type: "function",
			Function: LLMFunctionSpec{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  params,
			},
		})
	}
	return defs
}

// initialLoopMessages seeds the conversation from explicit messages or one
// user message synthesized from the legacy prompt.
func initialLoopMessages(config ToolLoopConfig) []any {
	if len(config.Messages) > 0 {
		out := make([]any, 0, len(config.Messages))
		return append(out, config.Messages...)
	}
	return []any{map[string]any{"role": "user", "content": config.Prompt}}
}

// buildAssistantEcho preserves the assistant message including its
// tool-call structures. Arguments re-serialize from normalized form;
// provider order and IDs are preserved verbatim.
// buildAssistantEcho preserves the assistant message including its
// tool-call structures, keyed by the batch effective IDs assigned up front
// so echo, tool messages, summaries, and audits all share one value.
func buildAssistantEcho(normalized LLMNormalizedResponse, effectiveIDs []string) map[string]any {
	calls := make([]any, 0, len(normalized.ToolCalls))
	for i, call := range normalized.ToolCalls {
		id := ""
		if i < len(effectiveIDs) {
			id = effectiveIDs[i]
		}
		args, err := json.Marshal(call.Arguments)
		if err != nil {
			args = []byte("{}")
		}
		calls = append(calls, map[string]any{
			"id":   id,
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": string(args),
			},
		})
	}
	return map[string]any{
		"role":       "assistant",
		"content":    normalized.Content,
		"tool_calls": calls,
	}
}

// toolResultMessage builds one role:tool message with bounded JSON content.
func toolResultMessage(callID string, observation map[string]any) map[string]any {
	content, err := json.Marshal(observation)
	if err != nil {
		content = []byte(`{"error":"unserializable result"}`)
	}
	return map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      string(content),
	}
}

// executeProviderCall runs one model round. Context-aware providers receive
// the cancellable context directly through their own contract (no watchdog
// goroutines); legacy providers keep working with their built-in timeouts.
func executeProviderCall(provider LLMProvider, ctx context.Context, req LLMRequest) (map[string]any, error) {
	if aware, ok := provider.(ContextAwareProvider); ok {
		return aware.ExecuteWithContext(ctx, req)
	}
	return provider.Execute(req)
}

// sanitizeToolError reduces handler errors to fixed-shape messages.
func sanitizeToolError(err error) string {
	if err == nil {
		return "tool execution failed"
	}
	if authErr, ok := err.(*ToolAuthError); ok {
		return "denied: " + authErr.Code
	}
	return "tool execution failed"
}

// sanitizeToolResult redacts sensitive keys from handler output.
func sanitizeToolResult(result map[string]any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	redacted, ok := redactSensitiveKeys(result).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return redacted
}

// auditOutcomeFor maps call status to an audit outcome.
func auditOutcomeFor(status string) string {
	if status == "executed" {
		return auditOutcomeAllowed
	}
	return auditOutcomeError
}

// sliceToSourceSet converts an allowlist slice to a lookup set.
func sliceToSourceSet(ids []uint64) map[uint64]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// toolRuntimeDB extracts the database handler from loop configuration.
func toolRuntimeDB(config ToolLoopConfig) cdb.Handler {
	return config.DB
}
