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
	"sync"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	defaultLLMProvider = "openai-compatible"
)

// LLMRequest is a normalized provider-agnostic AI request used by AIInteraction.
// Tools/ToolChoice carry inert declarations (Phase 2 never executes them).
// OutputMode is CROWler-side ("raw" default, "normalized" opt-in) and is
// never serialized to the provider.
type LLMRequest struct {
	Provider      string
	URL           string
	Auth          string
	Model         string
	Messages      []interface{}
	Prompt        string
	Temperature   *float64
	MaxTokens     *int
	TopP          *float64
	Extras        map[string]interface{}
	Tools         []LLMToolDefinition
	ToolChoice    LLMToolChoice
	HasToolChoice bool
	OutputMode    string
}

// LLMProvider abstracts an AI backend provider implementation.
type LLMProvider interface {
	Name() string
	Execute(req LLMRequest) (map[string]interface{}, error)
}

// OpenAICompatibleProvider supports OpenAI-style REST payloads and endpoints.
type OpenAICompatibleProvider struct{}

func (p *OpenAICompatibleProvider) Name() string {
	return defaultLLMProvider
}

func (p *OpenAICompatibleProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("missing 'url' parameter")
	}
	if !cmn.IsURLValid(req.URL) {
		return nil, fmt.Errorf("invalid URL: %s", cmn.SafeEscapeJSONString(req.URL))
	}

	requestBody := map[string]interface{}{}
	if strings.TrimSpace(req.Model) != "" {
		requestBody["model"] = req.Model
	}
	if len(req.Messages) > 0 {
		requestBody["messages"] = req.Messages
	} else {
		requestBody["prompt"] = req.Prompt
	}
	if req.Temperature != nil {
		requestBody["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		requestBody["max_tokens"] = *req.MaxTokens
	}
	if req.TopP != nil {
		requestBody["top_p"] = *req.TopP
	}
	// Inert tool declarations ride the same OpenAI-compatible payload.
	// They are serialized only when configured; nothing executes them.
	if len(req.Tools) > 0 {
		encoded := make([]interface{}, 0, len(req.Tools))
		for _, tool := range req.Tools {
			encoded = append(encoded, map[string]interface{}{
				"type": tool.Type,
				"function": map[string]interface{}{
					"name":        tool.Function.Name,
					"description": tool.Function.Description,
					"parameters":  tool.Function.Parameters,
				},
			})
		}
		requestBody["tools"] = encoded
	}
	if req.HasToolChoice {
		if wire := req.ToolChoice.toolChoiceWire(); wire != nil {
			requestBody["tool_choice"] = wire
		}
	}
	for k, v := range req.Extras {
		if _, exists := requestBody[k]; !exists {
			requestBody[k] = v
		}
	}

	headers := map[string]interface{}{"Content-Type": jsonAppType}
	if strings.TrimSpace(req.Auth) != "" {
		headers["Authorization"] = req.Auth
	}

	response, err := cmn.GenericAPIRequest(map[string]string{
		"url":     req.URL,
		"body":    string(cmn.ConvertMapToJSON(requestBody)),
		"method":  "POST",
		"headers": string(cmn.ConvertMapToJSON(headers)),
	})
	if err != nil {
		return nil, fmt.Errorf("AI interaction failed: %v", err)
	}
	// Bound retained provider output at the AI transport. The shared HTTP
	// client already bounds in-flight I/O with its own timeouts; this cap
	// stops pathological bodies from becoming retained maps. Auth tokens,
	// prompts, and bodies never appear in the resulting error.
	if len(response) > maxLLMResponseBytes {
		return nil, fmt.Errorf("AI response exceeded %d bytes", maxLLMResponseBytes)
	}

	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %v", err)
	}
	// Surface provider-side failures explicitly. GenericAPIRequest reports
	// transport only, so a non-2xx envelope would otherwise read as success.
	if status := providerStatusCode(responseMap); status < 200 || status >= 300 {
		return nil, fmt.Errorf("AI provider returned status %d: %s", status, providerBodyExcerpt(responseMap))
	}

	return responseMap, nil
}

// providerStatusCode reads the transport envelope status. Missing or
// malformed codes fail open to 200 so legacy string-only envelopes keep
// working; explicit non-2xx codes are enforced above.
func providerStatusCode(responseMap map[string]interface{}) int {
	raw, ok := responseMap["status_code"]
	if !ok || raw == nil {
		return 200
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 200
	}
}

// providerBodyExcerpt returns a short, secret-free slice of a provider error
// body for actionable failures.
func providerBodyExcerpt(responseMap map[string]interface{}) string {
	body, _ := responseMap["body"].(string)
	if body == "" {
		if raw, ok := responseMap["error"]; ok && raw != nil {
			if text, err := json.Marshal(raw); err == nil {
				body = string(text)
			}
		}
	}
	body = strings.TrimSpace(body)
	const maxExcerpt = 200
	if len(body) > maxExcerpt {
		body = body[:maxExcerpt] + "..."
	}
	if body == "" {
		return "empty error body"
	}
	return body
}

var (
	llmProvidersMu sync.RWMutex
	llmProviders   = map[string]LLMProvider{}
)

func init() {
	RegisterLLMProvider(&OpenAICompatibleProvider{})
}

// RegisterLLMProvider adds or replaces a provider implementation.
func RegisterLLMProvider(provider LLMProvider) {
	if provider == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(provider.Name()))
	if name == "" {
		return
	}
	llmProvidersMu.Lock()
	defer llmProvidersMu.Unlock()
	llmProviders[name] = provider
}

func getLLMProvider(name string) (LLMProvider, bool) {
	providerName := strings.ToLower(strings.TrimSpace(name))
	if providerName == "" {
		providerName = defaultLLMProvider
	}
	llmProvidersMu.RLock()
	defer llmProvidersMu.RUnlock()
	provider, ok := llmProviders[providerName]
	return provider, ok
}

func resetLLMProvidersForTest() {
	llmProvidersMu.Lock()
	defer llmProvidersMu.Unlock()
	llmProviders = map[string]LLMProvider{}
}
