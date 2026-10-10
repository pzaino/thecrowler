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
	"encoding/json"
	"fmt"
	"strings"
)

// Phase 2 contract: declarative tool schemas travel to the model and
// structured tool-call proposals return as INERT DATA. Nothing in this file
// executes a tool call, touches databases, or performs follow-up model
// calls. A model-supplied function name, arguments, URL, or SQL fragment is
// untrusted input; execution belongs to a future policy-gated phase.

// Tool-call declaration limits bound untrusted manifest input.
const (
	// maxLLMToolsPerRequest caps function declarations per AI request.
	maxLLMToolsPerRequest = 16
	// maxLLMToolsBytes caps the serialized tool declarations per request.
	maxLLMToolsBytes = 64 << 10
	// maxLLMResponseBytes caps the retained provider envelope processed by
	// the AI transport. The shared HTTP client additionally bounds
	// in-flight I/O with its own timeouts.
	maxLLMResponseBytes = 8 << 20
)

// Output modes for AIInteraction results. Raw preserves the historical
// provider response map byte-for-byte. Normalized returns the stable
// LLMNormalizedResponse shape. The default is raw; model-side
// OpenAI response_format parameters pass through untouched and are a
// separate concern.
const (
	LLMOutputRaw        = "raw"
	LLMOutputNormalized = "normalized"
)

// LLMToolDefinition declares one callable function to the model. Only the
// "function" tool type is supported in this phase.
type LLMToolDefinition struct {
	Type     string          `json:"type"`
	Function LLMFunctionSpec `json:"function"`
}

// LLMFunctionSpec describes a single function: a unique name, an optional
// human description, and a JSON Schema object constraining arguments.
type LLMFunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// LLMToolChoice selects how the model may use declared tools.
type LLMToolChoice struct {
	// Mode is one of "auto", "none", "required", or "function".
	Mode string `json:"mode"`
	// Name carries the function name when Mode is "function".
	Name string `json:"name,omitempty"`
}

// toolChoiceWire renders the choice for OpenAI-compatible transport.
func (c LLMToolChoice) toolChoiceWire() any {
	switch c.Mode {
	case "auto", "none", "required":
		return c.Mode
	case "function":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": c.Name},
		}
	default:
		return nil
	}
}

// LLMToolCall is one normalized, inert tool-call proposal. Index preserves
// provider order; ID is the provider-issued call identifier, or empty when
// the dialect supplies none (the index then disambiguates).
type LLMToolCall struct {
	ID        string         `json:"id,omitempty"`
	Index     int            `json:"index"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToMap renders the call as plain JSON-compatible maps for $response flow.
func (c LLMToolCall) ToMap() map[string]any {
	args := c.Arguments
	if args == nil {
		args = map[string]any{}
	}
	return map[string]any{
		"id":        c.ID,
		"index":     c.Index,
		"type":      c.Type,
		"name":      c.Name,
		"arguments": args,
	}
}

// LLMNormalizedResponse is the stable opt-in shape for AIInteraction output.
type LLMNormalizedResponse struct {
	Content      string         `json:"content"`
	ToolCalls    []LLMToolCall  `json:"tool_calls"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Model        string         `json:"model,omitempty"`
	Usage        map[string]any `json:"usage,omitempty"`
	// Raw keeps the untouched provider envelope accessible for audit and
	// debugging. It is always included in normalized mode.
	Raw map[string]any `json:"raw"`
}

// ToMap renders the normalized response as plain JSON-compatible maps.
func (r LLMNormalizedResponse) ToMap() map[string]any {
	calls := make([]any, 0, len(r.ToolCalls))
	for _, call := range r.ToolCalls {
		m := call.ToMap()
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		calls = append(calls, out)
	}
	out := map[string]any{
		"content":    r.Content,
		"tool_calls": calls,
	}
	if r.FinishReason != "" {
		out["finish_reason"] = r.FinishReason
	}
	if r.Model != "" {
		out["model"] = r.Model
	}
	if r.Usage != nil {
		out["usage"] = r.Usage
	}
	out["raw"] = r.Raw
	return out
}

// parseToolChoice converts step/config input into a typed choice. It accepts
// "auto", "none", "required", or {"type":"function","function":{"name":...}}.
// Anything else is an explicit error; absence means unset.
func parseToolChoice(raw any) (LLMToolChoice, bool, error) {
	if raw == nil {
		return LLMToolChoice{}, false, nil
	}
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "auto", "none", "required":
			return LLMToolChoice{Mode: strings.ToLower(strings.TrimSpace(v))}, true, nil
		case "":
			return LLMToolChoice{}, false, nil
		default:
			return LLMToolChoice{}, false, fmt.Errorf("invalid tool_choice %q: want auto, none, required, or a function selector", v)
		}
	case map[string]any:
		typeName, _ := v["type"].(string)
		if !strings.EqualFold(strings.TrimSpace(typeName), "function") {
			return LLMToolChoice{}, false, fmt.Errorf("invalid tool_choice: only function selectors are supported")
		}
		fn, _ := v["function"].(map[string]any)
		if fn == nil {
			if fnII, ok := v["function"].(map[interface{}]interface{}); ok {
				if converted, ok := normalizeStringMap(fnII); ok {
					fn = converted
				}
			}
		}
		name, _ := fn["name"].(string)
		if strings.TrimSpace(name) == "" {
			return LLMToolChoice{}, false, fmt.Errorf("invalid tool_choice: function selector needs a name")
		}
		return LLMToolChoice{Mode: "function", Name: strings.TrimSpace(name)}, true, nil
	case map[interface{}]interface{}:
		if converted, ok := normalizeStringMap(v); ok {
			return parseToolChoice(converted)
		}
		return LLMToolChoice{}, false, fmt.Errorf("invalid tool_choice: expected mapping")
	default:
		return LLMToolChoice{}, false, fmt.Errorf("invalid tool_choice: expected string or mapping")
	}
}

// validateToolDefinitions checks tool declarations without mutating them.
// Errors identify the tool index and field; prompts and secrets are never
// included.
func validateToolDefinitions(rawTools []any) ([]LLMToolDefinition, error) {
	if len(rawTools) > maxLLMToolsPerRequest {
		return nil, fmt.Errorf("invalid tools: at most %d function declarations per request, got %d",
			maxLLMToolsPerRequest, len(rawTools))
	}
	defs := make([]LLMToolDefinition, 0, len(rawTools))
	seen := map[string]int{}
	for i, raw := range rawTools {
		toolMap, ok := normalizeStringMap(raw)
		if !ok || toolMap == nil {
			return nil, fmt.Errorf("invalid tools[%d]: expected mapping", i)
		}
		toolType, _ := toolMap["type"].(string)
		if !strings.EqualFold(strings.TrimSpace(toolType), "function") {
			return nil, fmt.Errorf("invalid tools[%d].type: only \"function\" is supported", i)
		}
		fnMap, ok := normalizeStringMap(toolMap["function"])
		if !ok || fnMap == nil {
			return nil, fmt.Errorf("invalid tools[%d].function: expected mapping", i)
		}
		name, _ := fnMap["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("invalid tools[%d].function.name: empty", i)
		}
		if prev, dup := seen[strings.ToLower(name)]; dup {
			return nil, fmt.Errorf("invalid tools[%d].function.name: duplicate of tools[%d] %q", i, prev, name)
		}
		seen[strings.ToLower(name)] = i
		params, ok := normalizeStringMap(fnMap["parameters"])
		if !ok || params == nil {
			return nil, fmt.Errorf("invalid tools[%d].function.parameters: expected object", i)
		}
		paramType, _ := params["type"].(string)
		if !strings.EqualFold(strings.TrimSpace(paramType), "object") {
			return nil, fmt.Errorf("invalid tools[%d].function.parameters.type: must be \"object\"", i)
		}
		if rawRequired, present := params["required"]; present && rawRequired != nil {
			requiredList, ok := rawRequired.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid tools[%d].function.parameters.required: expected array of strings", i)
			}
			for _, entry := range requiredList {
				if _, ok := entry.(string); !ok {
					return nil, fmt.Errorf("invalid tools[%d].function.parameters.required: expected array of strings", i)
				}
			}
		}
		// Structural Draft-07 validation with required-property
		// consistency and local-only $ref integrity.
		if err := checkParameterSchema(params, i); err != nil {
			return nil, err
		}
		description, _ := fnMap["description"].(string)
		defs = append(defs, LLMToolDefinition{
			Type: "function",
			Function: LLMFunctionSpec{
				Name:        name,
				Description: description,
				Parameters:  map[string]any(params),
			},
		})
	}
	// Bound the serialized declaration size.
	encoded, err := json.Marshal(defs)
	if err != nil {
		return nil, fmt.Errorf("invalid tools: cannot serialize declarations")
	}
	if len(encoded) > maxLLMToolsBytes {
		return nil, fmt.Errorf("invalid tools: declarations exceed %d bytes", maxLLMToolsBytes)
	}
	return defs, nil
}

// ExtractChatPayload pulls the decoded chat-completion object out of a
// provider response. Envelope form ({status_code, body}) unwraps body,
// parsing text bodies as JSON; a map without a body key is treated as the
// payload itself so direct (non-HTTP) providers keep working. Either way a
// missing or undecodable payload is an explicit error.
func ExtractChatPayload(responseMap map[string]any) (map[string]any, error) {
	if responseMap == nil {
		return nil, fmt.Errorf("invalid provider response: missing object")
	}
	body, hasBody := responseMap["body"]
	if !hasBody || body == nil {
		return responseMap, nil
	}
	switch v := body.(type) {
	case map[string]any:
		return v, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, fmt.Errorf("invalid provider response: empty body")
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, fmt.Errorf("invalid provider response: body is not JSON")
		}
		payload, ok := decoded.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid provider response: body is not an object")
		}
		return payload, nil
	default:
		if converted, ok := normalizeStringMap(body); ok {
			return converted, nil
		}
		return nil, fmt.Errorf("invalid provider response: body is not an object")
	}
}

// NormalizeChatCompletion converts a decoded OpenAI-compatible chat
// completion object into the stable normalized shape. Only the first choice
// is used (documented first-choice behavior); competing choices are never
// merged. Ordinary text replies (no tool calls) are valid. Anything
// structurally malformed is an explicit error; free-form model prose is
// never parsed as tool calls.
func NormalizeChatCompletion(response map[string]any) (LLMNormalizedResponse, error) {
	out := LLMNormalizedResponse{ToolCalls: []LLMToolCall{}}
	if response == nil {
		return out, fmt.Errorf("invalid provider response: missing object")
	}
	rawChoices, present := response["choices"]
	if !present || rawChoices == nil {
		return out, fmt.Errorf("invalid provider response: missing choices")
	}
	choices, ok := rawChoices.([]any)
	if !ok || len(choices) == 0 {
		return out, fmt.Errorf("invalid provider response: empty choices")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		if converted, ok := normalizeStringMap(choices[0]); ok {
			choice = converted
		} else {
			return out, fmt.Errorf("invalid provider response: malformed choice")
		}
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		if converted, mapOK := normalizeStringMap(choice["message"]); mapOK {
			message = converted
		} else {
			return out, fmt.Errorf("invalid provider response: missing message")
		}
	}

	switch content := message["content"].(type) {
	case nil:
		out.Content = ""
	case string:
		out.Content = content
	default:
		return out, fmt.Errorf("invalid provider response: content must be text")
	}

	rawCalls, hasCalls := message["tool_calls"]
	if hasCalls && rawCalls != nil {
		calls, ok := rawCalls.([]any)
		if !ok {
			return out, fmt.Errorf("invalid provider response: tool_calls must be an array")
		}
		for i, raw := range calls {
			call, err := normalizeToolCall(raw, i)
			if err != nil {
				return out, err
			}
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	if out.Content == "" && len(out.ToolCalls) == 0 {
		return out, fmt.Errorf("invalid provider response: missing content and tool calls")
	}

	if reason, ok := choice["finish_reason"].(string); ok {
		out.FinishReason = reason
	}
	if model, ok := response["model"].(string); ok {
		out.Model = model
	}
	if usage, ok := response["usage"].(map[string]any); ok && usage != nil {
		out.Usage = usage
	}
	out.Raw = response
	return out, nil
}

// normalizeToolCall converts one provider tool-call entry. Index defaults to
// the entry position; ID defaults to empty when the dialect supplies none
// (the index then disambiguates); a missing type defaults to "function".
// A missing function name is an explicit error.
func normalizeToolCall(raw any, index int) (LLMToolCall, error) {
	call := LLMToolCall{Index: index, Type: "function", Arguments: map[string]any{}}
	entry, ok := normalizeStringMap(raw)
	if !ok || entry == nil {
		return call, fmt.Errorf("invalid tool call at index %d: expected object", index)
	}
	if id, present := entry["id"]; present && id != nil {
		idStr, ok := id.(string)
		if !ok {
			return call, fmt.Errorf("invalid tool call at index %d: id must be text", index)
		}
		call.ID = idStr
	}
	if idx, present := entry["index"]; present && idx != nil {
		switch n := idx.(type) {
		case int:
			call.Index = n
		case int64:
			call.Index = int(n)
		case float64:
			call.Index = int(n)
		default:
			return call, fmt.Errorf("invalid tool call at index %d: index must be numeric", index)
		}
	}
	if callType, present := entry["type"]; present && callType != nil {
		typeStr, ok := callType.(string)
		if !ok {
			return call, fmt.Errorf("invalid tool call at index %d: type must be text", index)
		}
		call.Type = typeStr
	}
	fn, ok := normalizeStringMap(entry["function"])
	if !ok || fn == nil {
		return call, fmt.Errorf("invalid tool call at index %d: missing function", index)
	}
	name, _ := fn["name"].(string)
	if strings.TrimSpace(name) == "" {
		return call, fmt.Errorf("invalid tool call at index %d: missing function name", index)
	}
	call.Name = strings.TrimSpace(name)
	args, err := normalizeToolArguments(fn["arguments"])
	if err != nil {
		return call, fmt.Errorf("invalid tool call at index %d: %s", index, toolArgumentErrorDetail(err))
	}
	call.Arguments = args
	return call, nil
}

// toolArgumentErrorDetail keeps argument errors fixed and index-scoped
// without echoing untrusted argument content.
func toolArgumentErrorDetail(err error) string {
	if err == nil {
		return "invalid arguments"
	}
	switch {
	case strings.Contains(err.Error(), "malformed JSON"):
		return "malformed arguments JSON"
	default:
		return "arguments must be an object"
	}
}

// checkToolChoiceConsistency validates a tool_choice against the effective
// declared tool set. Absent choice preserves legacy wire behavior (the field
// is omitted). With no tools, only "none" passes; auto/required/named
// selectors are rejected before HTTP. With tools, auto/none/required pass
// and a named selector must match exactly one declared function with
// case-sensitive wire identity. A named choice never authorizes execution;
// it only constrains the provider request. Errors are field-scoped and
// carry no prompts, secrets, or response bodies.
func checkToolChoiceConsistency(tools []LLMToolDefinition, choice LLMToolChoice, hasChoice bool) error {
	if !hasChoice {
		return nil
	}
	if len(tools) == 0 {
		if choice.Mode == "none" {
			return nil
		}
		return fmt.Errorf("invalid tool_choice %q: no tools declared", choice.Mode)
	}
	switch choice.Mode {
	case "none", "auto", "required":
		return nil
	case "function":
		for _, tool := range tools {
			if tool.Function.Name == choice.Name {
				return nil
			}
		}
		return fmt.Errorf("invalid tool_choice: function %q matches no declared tool", choice.Name)
	default:
		return fmt.Errorf("invalid tool_choice %q: want auto, none, required, or a function selector", choice.Mode)
	}
}

// normalizeToolArguments converts provider-supplied arguments to an object:
// JSON strings are parsed, objects are copied, and anything else (including
// malformed JSON) is an explicit error. Missing or null arguments yield an
// empty object. Nothing is silently dropped or defaulted.
func normalizeToolArguments(raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	switch v := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = item
		}
		return out, nil
	case map[interface{}]interface{}:
		if converted, ok := normalizeStringMap(v); ok {
			return converted, nil
		}
		return nil, fmt.Errorf("invalid tool arguments: expected object")
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return map[string]any{}, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, fmt.Errorf("invalid tool arguments: malformed JSON")
		}
		obj, ok := decoded.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid tool arguments: expected object")
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("invalid tool arguments: expected object")
	}
}
