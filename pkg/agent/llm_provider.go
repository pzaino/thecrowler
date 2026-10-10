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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	defaultLLMProvider = "openai-compatible"
	// llmHTTPTimeout mirrors the shared client bound for the dedicated
	// provider transport.
	llmHTTPTimeout = 30 * time.Second
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
	return p.ExecuteWithContext(context.Background(), req)
}

// ExecuteWithContext implements ContextAwareProvider for the built-in
// OpenAI-compatible transport. Cancellation and deadlines bound the
// in-flight request while connecting, awaiting headers, and reading the
// body; the shared 30s transport timeout still backstops requests without
// a tighter caller deadline. No watchdog goroutines are involved.
func (p *OpenAICompatibleProvider) ExecuteWithContext(ctx context.Context, req LLMRequest) (map[string]interface{}, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("missing 'url' parameter")
	}
	if !cmn.IsURLValid(req.URL) {
		return nil, fmt.Errorf("invalid URL: %s", cmn.SafeEscapeJSONString(req.URL))
	}

	requestBody := buildLLMRequestBody(req)
	headers := buildLLMHeaders(req)

	status, body, err := postLLMRequest(ctx, req.URL, headers, string(cmn.ConvertMapToJSON(requestBody)))
	if err != nil {
		return nil, fmt.Errorf("AI interaction failed: %v", err)
	}
	// Same envelope shape as the legacy GenericAPIRequest path
	// (float64 status, string body) so downstream parsing is identical.
	responseMap := map[string]interface{}{"status_code": float64(status), "body": body}
	return finishLLMResponse(responseMap)
}

// buildLLMRequestBody serializes one OpenAI-compatible request payload,
// shared by both transports so their wire behavior stays identical.
func buildLLMRequestBody(req LLMRequest) map[string]interface{} {
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
	return requestBody
}

// buildLLMHeaders renders transport headers, shared by both paths.
func buildLLMHeaders(req LLMRequest) map[string]interface{} {
	headers := map[string]interface{}{"Content-Type": jsonAppType}
	if strings.TrimSpace(req.Auth) != "" {
		headers["Authorization"] = req.Auth
	}
	return headers
}

// finishLLMResponse applies the shared post-transport gates: retained-size
// bound, envelope decoding, and explicit non-2xx failures. Error text never
// carries auth tokens, prompts, or bodies.
func finishLLMResponse(responseMap map[string]interface{}) (map[string]interface{}, error) {
	if body, ok := responseMap["body"].(string); ok && len(body) > maxLLMResponseBytes {
		return nil, fmt.Errorf("AI response exceeded %d bytes", maxLLMResponseBytes)
	}
	// Surface provider-side failures explicitly: a non-2xx envelope would
	// otherwise read as success.
	if status := providerStatusCode(responseMap); status < 200 || status >= 300 {
		return nil, fmt.Errorf("AI provider returned status %d: %s", status, providerBodyExcerpt(responseMap))
	}
	return responseMap, nil
}

// postLLMRequest performs one bounded POST with a context-aware request.
// The transport timeout backstops requests while the caller context (loop
// deadline or cancellation) preempts earlier at connect, header, and body
// stages alike. Bodies are capped WHILE reading: at most maxLLMResponseBytes
// +1 bytes are ever buffered. Errors carry statuses and short excerpts only,
// never URLs, credentials, or request contents.
func postLLMRequest(ctx context.Context, rawURL string, headers map[string]interface{}, body string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", jsonAppType)
	for key, value := range headers {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		if strings.EqualFold(key, "Content-Type") {
			continue
		}
		request.Header.Set(key, text)
	}
	request.Header.Set("User-Agent", "theCROWler/1.0")

	client := &http.Client{
		Timeout: llmHTTPTimeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		// Same redirect contract as the shared client: cap the chain and
		// never forward credentials across hosts.
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			if len(via) > 0 && !strings.EqualFold(r.URL.Hostname(), via[0].URL.Hostname()) {
				r.Header.Del("Authorization")
				return fmt.Errorf("redirect to different host blocked")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close() //nolint:errcheck
	content, err := io.ReadAll(io.LimitReader(response.Body, maxLLMResponseBytes+1))
	if err != nil {
		return 0, "", err
	}
	if len(content) > maxLLMResponseBytes {
		return 0, "", fmt.Errorf("AI response exceeded %d bytes", maxLLMResponseBytes)
	}
	return response.StatusCode, string(content), nil
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
