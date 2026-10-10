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
	"fmt"
	"strconv"
	"strings"
)

// AIInteractionAction interacts with an AI API
type AIInteractionAction struct{}

// Name returns the name of the action
func (a *AIInteractionAction) Name() string {
	return "AIInteraction"
}

// Execute sends a request to an AI provider.
func (a *AIInteractionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
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

	// Canonical input context: $response is the previous step's payload.
	ictx := NewInputContext(params)

	resolved, err := normalizeLLMRequest(params, config, ictx)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Enforce provider/model policy against the same identity snapshot the
	// capability/trust/contract gates use. The runtime stores the snapshot as
	// an AgentIdentity struct; legacy map-form snapshots are still honored.
	if identity, hasIdentity := parseRuntimeIdentity(params); hasIdentity {
		if err := enforceAIUsagePolicyForIdentity(identity, resolved); err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = err.Error()
			return rval, err
		}
	} else if err := enforceAIUsagePolicy(config, resolved); err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	provider, ok := getLLMProvider(resolved.Provider)
	if !ok {
		err = fmt.Errorf("unsupported AI provider: %s", resolved.Provider)
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	responseMap, err := provider.Execute(resolved)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	rval[StrResponse] = responseMap
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "AI interaction successful"
	return rval, nil
}

func normalizeLLMRequest(params, config map[string]interface{}, ictx InputContext) (LLMRequest, error) {
	getResolvedString := func(key string) (string, error) {
		v, ok := params[key]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", nil
		}
		resolved, err := ResolveString(ictx, s)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resolved), nil
	}
	resolveField := func(key, nestedKey string) (string, error) {
		raw, err := getResolvedString(key)
		if err != nil {
			return "", err
		}
		if raw != "" {
			return raw, nil
		}
		nested, err := nestedConfigString(config, ictx, "ai", nestedKey)
		if err != nil {
			return "", err
		}
		if nested != "" {
			return nested, nil
		}
		return resolveConfigString(config, ictx, nestedKey)
	}

	provider, err := resolveField("provider", "provider")
	if err != nil {
		return LLMRequest{}, err
	}
	if provider == "" {
		provider = defaultLLMProvider
	}
	url, err := resolveField("url", "url")
	if err != nil {
		return LLMRequest{}, err
	}
	auth, err := resolveField("auth", "auth")
	if err != nil {
		return LLMRequest{}, err
	}
	model, err := resolveField("model", "model")
	if err != nil {
		return LLMRequest{}, err
	}

	messages, err := normalizeMessages(params, ictx)
	if err != nil {
		return LLMRequest{}, err
	}
	promptRaw, err := getResolvedString("prompt")
	if err != nil {
		return LLMRequest{}, err
	}
	messageRaw, err := getResolvedString(StrMessage)
	if err != nil {
		return LLMRequest{}, err
	}
	prompt := firstString(promptRaw, messageRaw)
	if prompt == "" {
		if req, ok := ictx.Response.(string); ok {
			prompt = strings.TrimSpace(req)
		} else if req, ok := params[StrRequest].(string); ok {
			prompt = strings.TrimSpace(req)
		}
	}
	if len(messages) == 0 && prompt == "" {
		return LLMRequest{}, fmt.Errorf("missing 'prompt' or 'message' parameter")
	}
	if url == "" {
		return LLMRequest{}, fmt.Errorf(ErrMissingURL)
	}

	temperature, err := parseOptionalFloat(params, ictx, "temperature")
	if err != nil {
		return LLMRequest{}, err
	}
	maxTokens, err := parseOptionalInt(params, ictx, "max_tokens")
	if err != nil {
		return LLMRequest{}, err
	}
	topP, err := parseOptionalFloat(params, ictx, "top_p")
	if err != nil {
		return LLMRequest{}, err
	}

	extras := map[string]interface{}{}
	for _, key := range []string{"presence_penalty", "frequency_penalty", "stop", "echo", "logprobs", "n", "logit_bias", "stream"} {
		val, ok, err := resolveOptionalParam(params, ictx, key)
		if err != nil {
			return LLMRequest{}, err
		}
		if ok {
			extras[key] = val
		}
	}

	return LLMRequest{
		Provider:    provider,
		URL:         url,
		Auth:        auth,
		Model:       model,
		Messages:    messages,
		Prompt:      prompt,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		TopP:        topP,
		Extras:      extras,
	}, nil
}

// enforceAIUsagePolicyForIdentity applies the AI provider/model policy to a
// parsed identity snapshot (the struct form the runtime stores).
func enforceAIUsagePolicyForIdentity(identity AgentIdentity, req LLMRequest) error {
	if trustLevelRank(identity.TrustLevel) < trustLevelRank("trusted") && disallowHighTrustModel(req.Model) {
		return fmt.Errorf("AI policy denied model %q for trust_level %q", req.Model, identity.TrustLevel)
	}

	if identity.Contract != nil {
		for _, token := range identity.Contract.ForbiddenActions {
			if err := matchAIContractToken(token, req); err != nil {
				return err
			}
		}
	}

	return nil
}

// matchAIContractToken matches one forbidden_actions entry against the request.
func matchAIContractToken(token string, req LLMRequest) error {
	normalized := strings.ToLower(strings.TrimSpace(token))
	switch {
	case normalized == "aiinteraction":
		return fmt.Errorf("AI policy denied: agent contract forbids AIInteraction")
	case strings.HasPrefix(normalized, "provider:"):
		if matchesPolicyPattern(strings.TrimPrefix(normalized, "provider:"), strings.ToLower(req.Provider)) {
			return fmt.Errorf("AI policy denied provider %q by contract", req.Provider)
		}
	case strings.HasPrefix(normalized, "model:"):
		if matchesPolicyPattern(strings.TrimPrefix(normalized, "model:"), strings.ToLower(req.Model)) {
			return fmt.Errorf("AI policy denied model %q by contract", req.Model)
		}
	}
	return nil
}

func enforceAIUsagePolicy(config map[string]interface{}, req LLMRequest) error {
	runtimeMap := mapStringAny(config[cfgKeyAgentRuntime])
	identityMap := mapStringAny(runtimeMap["identity_snapshot"])
	if len(identityMap) == 0 {
		return nil
	}

	trustLevel, _ := identityMap["trust_level"].(string)
	if trustLevelRank(trustLevel) < trustLevelRank("trusted") && disallowHighTrustModel(req.Model) {
		return fmt.Errorf("AI policy denied model %q for trust_level %q", req.Model, trustLevel)
	}

	contractMap := mapStringAny(identityMap["agent_contract"])
	for _, token := range toStringSlice(contractMap["forbidden_actions"]) {
		normalized := strings.ToLower(strings.TrimSpace(token))
		switch {
		case normalized == "aiinteraction":
			return fmt.Errorf("AI policy denied: agent contract forbids AIInteraction")
		case strings.HasPrefix(normalized, "provider:"):
			if matchesPolicyPattern(strings.TrimPrefix(normalized, "provider:"), strings.ToLower(req.Provider)) {
				return fmt.Errorf("AI policy denied provider %q by contract", req.Provider)
			}
		case strings.HasPrefix(normalized, "model:"):
			if matchesPolicyPattern(strings.TrimPrefix(normalized, "model:"), strings.ToLower(req.Model)) {
				return fmt.Errorf("AI policy denied model %q by contract", req.Model)
			}
		}
	}

	return nil
}

func disallowHighTrustModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	if strings.Contains(m, "mini") || strings.Contains(m, "small") || strings.Contains(m, "nano") {
		return false
	}
	return strings.Contains(m, "gpt-4") || strings.HasPrefix(m, "o")
}

func matchesPolicyPattern(pattern, actual string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(actual, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == actual
}

func normalizeMessages(params map[string]interface{}, ictx InputContext) ([]interface{}, error) {
	if msgs, ok := params["messages"].([]interface{}); ok && len(msgs) > 0 {
		resolved, err := ResolveValue(ictx, msgs)
		if err != nil {
			return nil, err
		}
		if out, ok := resolved.([]interface{}); ok {
			return out, nil
		}
		return nil, fmt.Errorf("invalid 'messages' parameter")
	}
	return nil, nil
}

func parseOptionalFloat(params map[string]interface{}, ictx InputContext, key string) (*float64, error) {
	val, ok, err := resolveOptionalParam(params, ictx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	switch v := val.(type) {
	case float64:
		return &v, nil
	case float32:
		f := float64(v)
		return &f, nil
	case int:
		f := float64(v)
		return &f, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return nil, fmt.Errorf("%s '%v' parameter doesn't appear to be a valid float", key, v)
		}
		return &f, nil
	default:
		return nil, fmt.Errorf("%s '%v' parameter doesn't appear to be a valid float", key, v)
	}
}

func parseOptionalInt(params map[string]interface{}, ictx InputContext, key string) (*int, error) {
	val, ok, err := resolveOptionalParam(params, ictx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	switch v := val.(type) {
	case int:
		return &v, nil
	case int64:
		i := int(v)
		return &i, nil
	case float64:
		i := int(v)
		return &i, nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%s '%v' parameter doesn't appear to be a valid integer", key, v)
		}
		return &i, nil
	default:
		return nil, fmt.Errorf("%s '%v' parameter doesn't appear to be a valid integer", key, v)
	}
}

func resolveOptionalParam(params map[string]interface{}, ictx InputContext, key string) (interface{}, bool, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	if s, ok := raw.(string); ok {
		resolved, err := ResolveString(ictx, s)
		if err != nil {
			return nil, false, err
		}
		return resolved, true, nil
	}
	resolved, err := ResolveValue(ictx, raw)
	if err != nil {
		return nil, false, err
	}
	return resolved, true, nil
}

func nestedConfigString(config map[string]interface{}, ictx InputContext, key, nested string) (string, error) {
	cfgSection := mapStringAny(config[key])
	if len(cfgSection) == 0 {
		return "", nil
	}
	v, _ := cfgSection[nested].(string)
	if strings.TrimSpace(v) == "" {
		return "", nil
	}
	resolved, err := ResolveString(ictx, v)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func resolveConfigString(config map[string]interface{}, ictx InputContext, key string) (string, error) {
	v, _ := config[key].(string)
	if strings.TrimSpace(v) == "" {
		return "", nil
	}
	resolved, err := ResolveString(ictx, v)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mapStringAny(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	if m, ok := v.(map[string]any); ok {
		return map[string]interface{}(m)
	}
	return map[string]interface{}{}
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	if values, ok := v.([]string); ok {
		return values
	}
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
