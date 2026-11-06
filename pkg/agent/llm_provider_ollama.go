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
	"fmt"
	"os"
	"strings"
)

// Ollama is the first-class local provider. It deliberately reuses the
// OpenAI-compatible chat-completions transport because Ollama exposes the
// same tool-call message shapes (/v1/chat/completions). Implementing the
// native /api/chat dialect would duplicate request/response normalization for
// no additional tool-call capability; the native dialect is rejected with an
// actionable error instead of being silently mis-sent.
//
// Tool support in Ollama is model-dependent, so it is never inferred from the
// provider name. Operators attest it explicitly through
// CROWLER_OLLAMA_TOOL_MODELS (comma-separated model names, or "*" for all) or
// by registering NewOllamaProvider with an explicit list. Unset means
// unknown and preserves the historical permissive tool-loop behaviour; an
// explicitly empty list means "no tool-capable model" and fails tool loops
// before any HTTP call.

const (
	// ollamaProviderName is the registered provider identifier.
	ollamaProviderName = "ollama"
	// ollamaDefaultChatPath is Ollama's OpenAI-compatible chat endpoint.
	ollamaDefaultChatPath = "/v1/chat/completions"
	// ollamaToolModelsEnv is the operator-only tool-capability attestation.
	ollamaToolModelsEnv = "CROWLER_OLLAMA_TOOL_MODELS"
)

// ollamaToolPolicy records how the operator described tool support.
type ollamaToolPolicy int

const (
	// ollamaToolsUnknown keeps legacy behaviour: tools may work.
	ollamaToolsUnknown ollamaToolPolicy = iota
	// ollamaToolsUnsupported denies tool loops before transport.
	ollamaToolsUnsupported
	// ollamaToolsDeclared enables tools only for listed models (or all with *).
	ollamaToolsDeclared
)

// OllamaProvider talks to an Ollama server through its OpenAI-compatible
// chat completions endpoint.
type OllamaProvider struct {
	transport *OpenAICompatibleProvider
	policy    ollamaToolPolicy
	models    map[string]bool
	allModels bool
}

// NewOllamaProvider builds an Ollama provider.
//
// toolModels semantics:
//   - nil: tool capability unknown (legacy permissive behaviour);
//   - empty non-nil slice: tool calls explicitly unsupported;
//   - non-empty slice: only these models are attested as tool-capable;
//   - a slice containing "*": every model is attested as tool-capable.
func NewOllamaProvider(toolModels []string) *OllamaProvider {
	provider := &OllamaProvider{
		transport: &OpenAICompatibleProvider{},
		policy:    ollamaToolsUnknown,
	}
	provider.setToolModels(toolModels)
	return provider
}

func (p *OllamaProvider) setToolModels(toolModels []string) {
	if toolModels == nil {
		p.policy = ollamaToolsUnknown
		p.models = nil
		p.allModels = false
		return
	}
	if len(toolModels) == 0 {
		p.policy = ollamaToolsUnsupported
		p.models = nil
		p.allModels = false
		return
	}
	p.policy = ollamaToolsDeclared
	p.models = make(map[string]bool, len(toolModels))
	for _, model := range toolModels {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			p.allModels = true
			continue
		}
		p.models[trimmed] = true
	}
	if !p.allModels && len(p.models) == 0 {
		p.policy = ollamaToolsUnsupported
	}
}

// RegisterOllamaProvider installs an Ollama provider with an explicit
// tool-model attestation. Passing nil leaves tool capability unknown.
func RegisterOllamaProvider(toolModels []string) {
	RegisterLLMProvider(NewOllamaProvider(toolModels))
}

// ollamaToolModelsFromEnv reads the operator-only attestation.
// It distinguishes unset (nil, unknown) from set-but-empty (unsupported).
func ollamaToolModelsFromEnv() []string {
	raw, present := os.LookupEnv(ollamaToolModelsEnv)
	if !present {
		return nil
	}
	parts := strings.Split(raw, ",")
	models := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			models = append(models, trimmed)
		}
	}
	if len(models) == 0 {
		return []string{}
	}
	return models
}

// Name returns the registered provider name.
func (p *OllamaProvider) Name() string { return ollamaProviderName }

// Execute performs one non-context provider call.
func (p *OllamaProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	return p.ExecuteWithContext(context.Background(), req)
}

// ExecuteWithContext normalizes the Ollama endpoint and delegates to the
// shared OpenAI-compatible transport. Cancellation and error handling are
// exactly the transport's.
func (p *OllamaProvider) ExecuteWithContext(ctx context.Context, req LLMRequest) (map[string]interface{}, error) {
	endpoint, err := NormalizeOllamaEndpoint(req.URL)
	if err != nil {
		return nil, err
	}
	req.URL = endpoint
	// Ollama's compatible endpoint is a chat API: it has no completion-style
	// `prompt` field. A legacy prompt-only request therefore becomes one
	// explicit user message. Explicit messages always take precedence, and
	// prompt+tool requests were already converted by normalizeLLMRequest.
	if len(req.Messages) == 0 && strings.TrimSpace(req.Prompt) != "" {
		req.Messages = []interface{}{map[string]interface{}{
			"role":    "user",
			"content": req.Prompt,
		}}
		req.Prompt = ""
	}
	return p.transport.ExecuteWithContext(ctx, req)
}

// Capabilities reports the transport-level features of Ollama's
// OpenAI-compatible API. Tool-call support remains model-dependent.
func (p *OllamaProvider) Capabilities() LLMProviderCapabilities {
	return normalizeLLMCapabilities(LLMProviderCapabilities{
		Provider:           ollamaProviderName,
		NativeProtocol:     "openai-compatible",
		Chat:               LLMCapabilitySupported,
		ToolCalls:          p.baseToolCapability(),
		StructuredOutput:   LLMCapabilityUnknown,
		Streaming:          LLMCapabilitySupported,
		ToolResultMessages: LLMCapabilitySupported,
		Cancellation:       LLMCapabilitySupported,
		Notes: []string{
			"Uses Ollama's OpenAI-compatible /v1/chat/completions endpoint; native /api/chat is not implemented.",
			"Tool support is model-dependent and must be attested by the operator.",
		},
	})
}

// CapabilitiesForModel reports model-level tool capability using the
// operator attestation.
func (p *OllamaProvider) CapabilitiesForModel(model string) LLMProviderCapabilities {
	caps := p.Capabilities()
	caps.ToolCalls = p.toolCapability(model)
	return caps
}

// baseToolCapability is the provider-level answer when no model is supplied.
// A declared model list is model-dependent, so it reports unknown rather than
// claiming that every model is unsupported.
func (p *OllamaProvider) baseToolCapability() LLMCapabilityState {
	switch p.policy {
	case ollamaToolsUnsupported:
		return LLMCapabilityUnsupported
	case ollamaToolsDeclared:
		if p.allModels {
			return LLMCapabilitySupported
		}
		return LLMCapabilityUnknown
	default:
		return LLMCapabilityUnknown
	}
}

func (p *OllamaProvider) toolCapability(model string) LLMCapabilityState {
	switch p.policy {
	case ollamaToolsUnsupported:
		return LLMCapabilityUnsupported
	case ollamaToolsDeclared:
		if p.allModels {
			return LLMCapabilitySupported
		}
		trimmed := strings.TrimSpace(model)
		if p.models[trimmed] {
			return LLMCapabilitySupported
		}
		// Model tags are conventionally lowercase; accept a case-insensitive
		// match without treating unrelated models as supported.
		for declared := range p.models {
			if strings.EqualFold(declared, trimmed) {
				return LLMCapabilitySupported
			}
		}
		return LLMCapabilityUnsupported
	default:
		return LLMCapabilityUnknown
	}
}

// NormalizeOllamaEndpoint accepts an operator-configured Ollama base URL or
// chat endpoint and returns the OpenAI-compatible chat completions URL.
// Empty and /v1 paths receive the standard suffix; an explicit
// /chat/completions path (including a proxy prefix) is preserved. The native
// /api/chat path is rejected because this adapter does not speak that dialect.
func NormalizeOllamaEndpoint(raw string) (string, error) {
	parsed, err := parseLLMEndpoint(raw)
	if err != nil {
		return "", fmt.Errorf("invalid ollama endpoint: %v", err)
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowered := strings.ToLower(path)
	switch {
	case path == "":
		parsed.Path = ollamaDefaultChatPath
	case strings.HasSuffix(lowered, "/api/chat"):
		return "", fmt.Errorf("invalid ollama endpoint: native /api/chat is not supported; use the OpenAI-compatible /v1/chat/completions endpoint")
	case strings.HasSuffix(lowered, "/chat/completions"):
		parsed.Path = path
	case lowered == "/v1":
		parsed.Path = ollamaDefaultChatPath
	default:
		return "", fmt.Errorf("invalid ollama endpoint: path must be empty, /v1, or end with /chat/completions")
	}
	parsed.RawPath = ""
	return parsed.String(), nil
}

func init() {
	RegisterOllamaProvider(ollamaToolModelsFromEnv())
}
