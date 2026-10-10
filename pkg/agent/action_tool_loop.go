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
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// AgentToolLoopAction runs an explicitly enabled, bounded, synchronous
// model/tool loop over vetted read-only tools. Default AIInteraction output
// stays inert; only this opt-in action executes tool handlers, and only
// after registry membership, explicit allowlisting, capability, trust,
// contract, argument-schema, scope, and budget checks.
type AgentToolLoopAction struct{}

// Name returns the name of the action.
func (a *AgentToolLoopAction) Name() string { return "AgentToolLoop" }

// Execute runs the tool loop for one job-group attempt.
func (a *AgentToolLoopAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := map[string]interface{}{StrResponse: nil, StrConfig: nil}

	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	if _, err := getInput(params); err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	ictx := NewInputContext(params)

	// Tool execution requires identity enforcement: without it, proposals
	// stay inert and this action refuses, even for `all` identities.
	identity, hasIdentity := parseRuntimeIdentity(params)
	runtimeFlags := runtimeFlagsFromConfig(config)
	if !hasIdentity || !runtimeFlags.IdentityEnforcement {
		err := fmt.Errorf("tool loop denied: identity enforcement is required for tool execution")
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	loopConfig, err := normalizeToolLoopConfig(params, config, ictx, identity)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	result, err := RunToolLoop(loopConfig)
	rval[StrResponse] = toolLoopResultMap(result)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "tool loop completed successfully"
	return rval, nil
}

// toolLoopResultMap renders the typed terminal result JSON-compatibly.
func toolLoopResultMap(result ToolLoopResult) map[string]any {
	calls := make([]any, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		calls = append(calls, map[string]any{
			"name": call.Name, "id": call.ID, "index": call.Index,
			"status": call.Status, "reason_code": call.ReasonCode,
			"result_bytes": call.ResultBytes,
		})
	}
	return map[string]any{
		"status": result.Status, "content": result.Content,
		"tool_calls": calls, "model_rounds": result.ModelRounds,
		"tool_call_count": result.ToolCallCount, "reason": result.Reason,
		"run_id": result.RunID, "trace_id": result.TraceID,
	}
}

// normalizeToolLoopConfig builds trusted loop configuration from manifest
// and operator config. Policy-bearing fields (allowlist, limits, scopes)
// must be statically declared: any input-reference marker in their raw
// values is rejected so model-influenced data can never widen authority.
func normalizeToolLoopConfig(params, config map[string]interface{}, ictx InputContext, identity AgentIdentity) (ToolLoopConfig, error) {
	loop := ToolLoopConfig{
		Identity:    identity,
		Enforcement: true,
		Limits:      DefaultToolLoopLimits(),
	}
	if runID, ok := toolLoopRunField(params, "run_id"); ok {
		loop.RunID = runID
	}
	if traceID, ok := toolLoopRunField(params, "trace_id"); ok {
		loop.TraceID = traceID
	}
	if loop.RunID == "" {
		loop.RunID = uuid.NewString()
	}
	if loop.TraceID == "" {
		loop.TraceID = uuid.NewString()
	}

	// Provider endpoint triple, resolved with normal dataflow (never from
	// tool results by construction: the loop has not run yet).
	provider, err := resolveLoopString(params, config, ictx, "provider", "provider")
	if err != nil {
		return loop, err
	}
	if provider == "" {
		provider = defaultLLMProvider
	}
	loop.ProviderName = provider
	providerImpl, ok := getLLMProvider(provider)
	if !ok {
		return loop, fmt.Errorf("tool loop denied: unsupported AI provider %q", provider)
	}
	loop.Provider = providerImpl
	if loop.URL, err = resolveLoopString(params, config, ictx, "url", "url"); err != nil {
		return loop, err
	}
	if strings.TrimSpace(loop.URL) == "" {
		return loop, fmt.Errorf("tool loop denied: missing url")
	}
	if loop.Auth, err = resolveLoopString(params, config, ictx, "auth", "auth"); err != nil {
		return loop, err
	}
	if loop.Model, err = resolveLoopString(params, config, ictx, "model", "model"); err != nil {
		return loop, err
	}
	if strings.TrimSpace(loop.Model) == "" {
		return loop, fmt.Errorf("tool loop denied: missing model")
	}

	// Opening prompt: explicit messages win, else one user message.
	if rawMessages, ok := params["messages"]; ok && rawMessages != nil {
		resolved, err := ResolveValue(ictx, rawMessages)
		if err != nil {
			return loop, err
		}
		messages, ok := resolved.([]any)
		if !ok || len(messages) == 0 {
			return loop, fmt.Errorf("tool loop denied: invalid messages")
		}
		loop.Messages = messages
	} else {
		prompt, err := resolveLoopString(params, config, ictx, "prompt", "")
		if err != nil {
			return loop, err
		}
		if prompt == "" {
			if req, ok := ictx.Response.(string); ok {
				prompt = strings.TrimSpace(req)
			}
		}
		if prompt == "" {
			return loop, fmt.Errorf("tool loop denied: missing prompt or messages")
		}
		loop.Prompt = prompt
	}

	// Explicit trusted allowlist: step params win over config.ai.tools.
	allowlist, err := normalizeLoopAllowlist(params, config)
	if err != nil {
		return loop, err
	}
	loop.Allowlist = allowlist

	// Optional source scope: static integers only.
	sources, err := normalizeLoopSources(params, config)
	if err != nil {
		return loop, err
	}
	loop.AllowedSources = sources

	// Optional limit tightening within hard ceilings.
	limits, err := normalizeLoopLimits(params)
	if err != nil {
		return loop, err
	}
	loop.Limits = limits

	if raw, ok := params["stop_on_tool_error"]; ok && raw != nil {
		switch v := raw.(type) {
		case bool:
			loop.StopOnToolError = v
		default:
			return loop, fmt.Errorf("tool loop denied: invalid stop_on_tool_error")
		}
	} else if limitsRaw, ok := params["limits"]; ok && limitsRaw != nil {
		if limitsMap, ok := normalizeStringMap(limitsRaw); ok && limitsMap != nil {
			if flag, ok := limitsMap["stop_on_tool_error"]; ok && flag != nil {
				stop, ok := flag.(bool)
				if !ok {
					return loop, fmt.Errorf("tool loop denied: invalid stop_on_tool_error")
				}
				loop.StopOnToolError = stop
			}
		}
	}

	// Tools run against the caller's database handler.
	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok || dbHandler == nil {
		return loop, fmt.Errorf("tool loop denied: missing db_handler")
	}
	loop.DB = dbHandler

	// Inherit the remaining job-group time budget when propagated.
	if deadline, ok := groupDeadline(params); ok {
		loop.GroupDeadline = deadline
	}

	registry, err := DefaultToolRegistry()
	if err != nil {
		return loop, err
	}
	loop.Registry = registry
	return loop, nil
}

// toolLoopRunField reads run/trace correlation IDs from the runtime map.
func toolLoopRunField(params map[string]interface{}, key string) (string, bool) {
	configMap, ok := params[StrConfig].(map[string]interface{})
	if !ok {
		return "", false
	}
	runtimeMap, ok := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	if !ok {
		return "", false
	}
	value, _ := runtimeMap[key].(string)
	return strings.TrimSpace(value), strings.TrimSpace(value) != ""
}

// resolveLoopString resolves one scalar field with step-over-config.ai
// precedence, mirroring AIInteraction conventions.
func resolveLoopString(params, config map[string]interface{}, ictx InputContext, paramKey, configKey string) (string, error) {
	if raw, ok := params[paramKey]; ok && raw != nil {
		if text, ok := raw.(string); ok {
			resolved, err := ResolveString(ictx, text)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(resolved) != "" {
				return strings.TrimSpace(resolved), nil
			}
		} else if text := strings.TrimSpace(strings.Trim(stringifyLoopScalar(raw), " ")); text != "" {
			return text, nil
		}
	}
	if configKey != "" {
		if aiSection := mapStringAny(config["ai"]); len(aiSection) > 0 {
			if raw, ok := aiSection[configKey]; ok && raw != nil {
				if text, ok := raw.(string); ok {
					resolved, err := ResolveString(ictx, text)
					if err != nil {
						return "", err
					}
					return strings.TrimSpace(resolved), nil
				}
			}
		}
	}
	return "", nil
}

func stringifyLoopScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// rejectInputMarkers refuses policy-bearing values that reference step
// input: authority must be statically declared, never model-influenced.
func rejectInputMarkers(value any) error {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "$response") || strings.Contains(v, "$event") || strings.Contains(v, "{{") {
			return fmt.Errorf("tool loop denied: policy fields must be statically declared")
		}
		return nil
	case []any:
		for _, item := range v {
			if err := rejectInputMarkers(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range v {
			if err := rejectInputMarkers(item); err != nil {
				return err
			}
		}
		return nil
	case map[interface{}]interface{}:
		for _, item := range v {
			if err := rejectInputMarkers(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// normalizeLoopAllowlist reads the explicit trusted tool allowlist.
func normalizeLoopAllowlist(params, config map[string]interface{}) ([]string, error) {
	raw, ok := params["allowlist"]
	if !ok || raw == nil {
		if aiSection := mapStringAny(config["ai"]); len(aiSection) > 0 {
			raw, ok = aiSection["allowlist"]
		}
	}
	if !ok || raw == nil {
		return nil, fmt.Errorf("tool loop denied: missing explicit allowlist")
	}
	if err := rejectInputMarkers(raw); err != nil {
		return nil, err
	}
	entries, ok := raw.([]any)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("tool loop denied: allowlist must be a non-empty array")
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("tool loop denied: allowlist must list tool names")
		}
		out = append(out, strings.TrimSpace(name))
	}
	return out, nil
}

// normalizeLoopSources reads the optional static source scope.
func normalizeLoopSources(params, config map[string]interface{}) ([]uint64, error) {
	raw, ok := params["allowed_sources"]
	if !ok || raw == nil {
		if aiSection := mapStringAny(config["ai"]); len(aiSection) > 0 {
			raw, ok = aiSection["allowed_sources"]
		}
	}
	if !ok || raw == nil {
		return nil, nil
	}
	if err := rejectInputMarkers(raw); err != nil {
		return nil, err
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tool loop denied: allowed_sources must be an array")
	}
	out := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		id, err := toSourceIDArg(entry)
		if err != nil {
			return nil, fmt.Errorf("tool loop denied: %s", err.Error())
		}
		out = append(out, id)
	}
	return out, nil
}

// loopLimitKeys maps manifest limit names to hard ceilings.
func loopLimitKeys() map[string]int {
	return map[string]int{
		"max_model_rounds":       hardMaxModelRounds,
		"max_tool_calls":         hardMaxToolCalls,
		"max_result_bytes":       hardMaxResultBytes,
		"max_total_result_bytes": hardMaxTotalResultBytes,
	}
}

// normalizeLoopLimits reads optional tightening within hard ceilings.
// Unknown limit keys are rejected; timeout accepts Go durations or seconds.
func normalizeLoopLimits(params map[string]interface{}) (ToolLoopLimits, error) {
	limits := DefaultToolLoopLimits()
	raw, ok := params["limits"]
	if !ok || raw == nil {
		return limits, nil
	}
	if err := rejectInputMarkers(raw); err != nil {
		return limits, err
	}
	settings, ok := normalizeStringMap(raw)
	if !ok || settings == nil {
		return limits, fmt.Errorf("tool loop denied: invalid limits")
	}
	setInt := func(key string, ceiling int, apply func(int)) error {
		value, present := settings[key]
		if !present || value == nil {
			return nil
		}
		var n int
		switch v := value.(type) {
		case float64:
			if v < 1 || v != float64(int(v)) {
				return fmt.Errorf("tool loop denied: invalid %s", key)
			}
			n = int(v)
		case int:
			n = v
		default:
			return fmt.Errorf("tool loop denied: invalid %s", key)
		}
		if n <= 0 || n > ceiling {
			return fmt.Errorf("tool loop denied: %s exceeds ceiling %d", key, ceiling)
		}
		apply(n)
		return nil
	}
	for key, ceiling := range loopLimitKeys() {
		var apply func(int)
		switch key {
		case "max_model_rounds":
			apply = func(n int) { limits.MaxModelRounds = n }
		case "max_tool_calls":
			apply = func(n int) { limits.MaxToolCalls = n }
		case "max_result_bytes":
			apply = func(n int) { limits.MaxResultBytes = n }
		case "max_total_result_bytes":
			apply = func(n int) { limits.MaxTotalResultBytes = n }
		}
		if err := setInt(key, ceiling, apply); err != nil {
			return limits, err
		}
	}
	known := map[string]bool{
		"max_model_rounds": true, "max_tool_calls": true,
		"max_result_bytes": true, "max_total_result_bytes": true,
		"timeout": true, "stop_on_tool_error": true,
	}
	for key := range settings {
		if key == "stop_on_tool_error" {
			continue
		}
		if !known[key] {
			return limits, fmt.Errorf("tool loop denied: unknown limit %q", key)
		}
	}
	if rawTimeout, present := settings["timeout"]; present && rawTimeout != nil {
		timeout, err := parseLoopTimeout(rawTimeout)
		if err != nil {
			return limits, err
		}
		if timeout <= 0 || timeout > hardLoopTimeout {
			return limits, fmt.Errorf("tool loop denied: timeout exceeds ceiling %s", hardLoopTimeout)
		}
		limits.Timeout = timeout
	}
	if err := ValidateLimits(limits); err != nil {
		return limits, fmt.Errorf("tool loop denied: %v", err)
	}
	return limits, nil
}

// parseLoopTimeout accepts Go durations ("10s") or plain seconds numbers.
func parseLoopTimeout(raw any) (duration time.Duration, err error) {
	switch v := raw.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return 0, fmt.Errorf("tool loop denied: invalid timeout")
		}
		parsed, parseErr := time.ParseDuration(text)
		if parseErr != nil {
			return 0, fmt.Errorf("tool loop denied: invalid timeout")
		}
		return parsed, nil
	case float64:
		if v <= 0 {
			return 0, fmt.Errorf("tool loop denied: invalid timeout")
		}
		return time.Duration(v * float64(time.Second)), nil
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("tool loop denied: invalid timeout")
		}
		return time.Duration(v) * time.Second, nil
	default:
		return 0, fmt.Errorf("tool loop denied: invalid timeout")
	}
}
