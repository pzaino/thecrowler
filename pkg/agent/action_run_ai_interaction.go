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

	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	resolved, err := normalizeLLMRequest(params, config, inputRaw)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	if err := enforceAIUsagePolicy(config, resolved); err != nil {
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

func normalizeLLMRequest(params, config, inputRaw map[string]interface{}) (LLMRequest, error) {
	getResolvedString := func(key string) string {
		v, ok := params[key]
		if !ok || v == nil {
			return ""
		}
		s, ok := v.(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(resolveResponseString(inputRaw, s))
	}

	provider := firstString(getResolvedString("provider"), nestedConfigString(config, inputRaw, "ai", "provider"), defaultLLMProvider)
	url := firstString(getResolvedString("url"), nestedConfigString(config, inputRaw, "ai", "url"), resolveConfigString(config, inputRaw, "url"))
	auth := firstString(getResolvedString("auth"), nestedConfigString(config, inputRaw, "ai", "auth"), resolveConfigString(config, inputRaw, "auth"))
	model := firstString(getResolvedString("model"), nestedConfigString(config, inputRaw, "ai", "model"), resolveConfigString(config, inputRaw, "model"))

	messages := normalizeMessages(params, inputRaw)
	prompt := firstString(getResolvedString("prompt"), getResolvedString(StrMessage))
	if prompt == "" {
		if req, ok := inputRaw[StrRequest].(string); ok {
			prompt = strings.TrimSpace(req)
		}
	}
	if len(messages) == 0 && prompt == "" {
		return LLMRequest{}, fmt.Errorf("missing 'prompt' or 'message' parameter")
	}
	if url == "" {
		return LLMRequest{}, fmt.Errorf(ErrMissingURL)
	}

	temperature, err := parseOptionalFloat(params, inputRaw, "temperature")
	if err != nil {
		return LLMRequest{}, err
	}
	maxTokens, err := parseOptionalInt(params, inputRaw, "max_tokens")
	if err != nil {
		return LLMRequest{}, err
	}
	topP, err := parseOptionalFloat(params, inputRaw, "top_p")
	if err != nil {
		return LLMRequest{}, err
	}

	extras := map[string]interface{}{}
	for _, key := range []string{"presence_penalty", "frequency_penalty", "stop", "echo", "logprobs", "n", "logit_bias", "stream"} {
		if val, ok := resolveOptionalParam(params, inputRaw, key); ok {
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

func normalizeMessages(params, inputRaw map[string]interface{}) []interface{} {
	if msgs, ok := params["messages"].([]interface{}); ok && len(msgs) > 0 {
		resolved := resolveValue(inputRaw, msgs)
		if out, ok := resolved.([]interface{}); ok {
			return out
		}
	}
	return nil
}

func parseOptionalFloat(params, inputRaw map[string]interface{}, key string) (*float64, error) {
	val, ok := resolveOptionalParam(params, inputRaw, key)
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

func parseOptionalInt(params, inputRaw map[string]interface{}, key string) (*int, error) {
	val, ok := resolveOptionalParam(params, inputRaw, key)
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

func resolveOptionalParam(params, inputRaw map[string]interface{}, key string) (interface{}, bool) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, false
	}
	if s, ok := raw.(string); ok {
		return resolveResponseString(inputRaw, s), true
	}
	return resolveValue(inputRaw, raw), true
}

func nestedConfigString(config map[string]interface{}, inputRaw map[string]interface{}, key, nested string) string {
	cfgSection := mapStringAny(config[key])
	if len(cfgSection) == 0 {
		return ""
	}
	v, _ := cfgSection[nested].(string)
	return strings.TrimSpace(resolveResponseString(inputRaw, v))
}

func resolveConfigString(config map[string]interface{}, inputRaw map[string]interface{}, key string) string {
	v, _ := config[key].(string)
	return strings.TrimSpace(resolveResponseString(inputRaw, v))
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
