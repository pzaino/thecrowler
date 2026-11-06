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

import "strings"

// Provider capability reporting is additive. The legacy LLMProvider
// interface is unchanged, so third-party providers compiled against it keep
// working. Providers that do not implement either reporter are treated as
// "unknown" for every capability and retain the historical permissive
// behaviour.
//
// Capability state is deliberately tri-state. A provider that knows a model
// lacks tool calling can say so and have the tool loop fail before any HTTP
// call. A provider that cannot know (for example an OpenAI-compatible gateway
// in front of an arbitrary model) reports unknown instead of guessing from
// the provider or model name.

// LLMCapabilityState declares whether a provider or model supports a feature.
type LLMCapabilityState string

const (
	// LLMCapabilityUnknown means the transport cannot determine support.
	// Callers must preserve their historical behaviour.
	LLMCapabilityUnknown LLMCapabilityState = "unknown"
	// LLMCapabilitySupported means the operator or provider attested support.
	LLMCapabilitySupported LLMCapabilityState = "supported"
	// LLMCapabilityUnsupported means support is explicitly denied.
	LLMCapabilityUnsupported LLMCapabilityState = "unsupported"
)

// LLMProviderCapabilities is the normalized feature descriptor used by
// CROWler policy and diagnostics. Empty states normalize to unknown.
type LLMProviderCapabilities struct {
	Provider string `json:"provider"`
	// NativeProtocol names the wire dialect the adapter speaks. Adapters that
	// translate to OpenAI-compatible chat completions report that dialect,
	// even when the product endpoint is Ollama's compatible API.
	NativeProtocol string `json:"native_protocol"`

	Chat               LLMCapabilityState `json:"chat"`
	ToolCalls          LLMCapabilityState `json:"tool_calls"`
	StructuredOutput   LLMCapabilityState `json:"structured_output"`
	Streaming          LLMCapabilityState `json:"streaming"`
	ToolResultMessages LLMCapabilityState `json:"tool_result_messages"`
	Cancellation       LLMCapabilityState `json:"cancellation"`

	// MaxContextTokens and MaxOutputTokens are 0 when unknown.
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	MaxOutputTokens  int `json:"max_output_tokens,omitempty"`

	// Notes carries short operator-facing explanations. It must never
	// include credentials, prompts or endpoint URLs.
	Notes []string `json:"notes,omitempty"`
}

// ProviderCapabilityReporter is implemented by providers that can describe
// their transport-level capabilities. It is optional.
type ProviderCapabilityReporter interface {
	Capabilities() LLMProviderCapabilities
}

// ModelCapabilityReporter is implemented by providers that can report
// model-specific capabilities. It takes precedence over
// ProviderCapabilityReporter.
type ModelCapabilityReporter interface {
	CapabilitiesForModel(model string) LLMProviderCapabilities
}

// ProviderCapabilitiesFor returns normalized capabilities for one provider
// and model. Unknown providers and legacy providers without reporters yield
// an all-unknown descriptor so existing code paths are unaffected.
func ProviderCapabilitiesFor(provider LLMProvider, model string) LLMProviderCapabilities {
	caps := LLMProviderCapabilities{}
	if provider != nil {
		switch reporter := provider.(type) {
		case ModelCapabilityReporter:
			caps = reporter.CapabilitiesForModel(model)
		case ProviderCapabilityReporter:
			caps = reporter.Capabilities()
		}
		if strings.TrimSpace(caps.Provider) == "" {
			caps.Provider = provider.Name()
		}
	}
	return normalizeLLMCapabilities(caps)
}

// normalizeLLMCapabilities fills empty states with unknown and drops
// impossible negative token limits.
func normalizeLLMCapabilities(caps LLMProviderCapabilities) LLMProviderCapabilities {
	caps.Chat = normalizeLLMCapabilityState(caps.Chat)
	caps.ToolCalls = normalizeLLMCapabilityState(caps.ToolCalls)
	caps.StructuredOutput = normalizeLLMCapabilityState(caps.StructuredOutput)
	caps.Streaming = normalizeLLMCapabilityState(caps.Streaming)
	caps.ToolResultMessages = normalizeLLMCapabilityState(caps.ToolResultMessages)
	caps.Cancellation = normalizeLLMCapabilityState(caps.Cancellation)
	if caps.MaxContextTokens < 0 {
		caps.MaxContextTokens = 0
	}
	if caps.MaxOutputTokens < 0 {
		caps.MaxOutputTokens = 0
	}
	if caps.Notes == nil {
		caps.Notes = []string{}
	}
	return caps
}

func normalizeLLMCapabilityState(state LLMCapabilityState) LLMCapabilityState {
	switch LLMCapabilityState(strings.ToLower(strings.TrimSpace(string(state)))) {
	case LLMCapabilitySupported:
		return LLMCapabilitySupported
	case LLMCapabilityUnsupported:
		return LLMCapabilityUnsupported
	default:
		return LLMCapabilityUnknown
	}
}
