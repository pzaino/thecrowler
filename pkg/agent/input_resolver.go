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
	"regexp"
	"strconv"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// Canonical step-input dataflow contract.
//
//   - $response means the previous step's payload (params["input"]), the same
//     value in every action type. $response.foo.bar resolves only within that
//     payload; a bare $response references the whole payload.
//   - $event means the initial triggering event/request
//     (params["config"]["event"]) and is stable across all steps of a run.
//   - Whole-value references (a param that is exactly "$response" or
//     "$response.a.b") preserve the original JSON type. References embedded in
//     a larger string interpolate as text.
//   - Missing paths are explicit errors naming the path; the resolver never
//     emits magic "%!s(...)" or "<nil>" strings.
//   - Resolution is read-only: inputs are deep-copied, never mutated, so
//     repeated and parallel runs cannot corrupt the stored manifest.
//   - Deprecated alias: a leading "input"/"request" segment
//     ($response.input.x, $response.request.x) falls back to the remainder of
//     the path when the canonical path does not resolve. This preserves
//     manifests written against the pre-canonical wrapper-level accident.
//     New manifests must use canonical paths.
//
// DBQuery note: query construction here is string substitution, not
// parameterization. Interpolated values must come from trusted step payloads
// (never raw model output); model-directed SQL input is unsupported.
var (
	inputTokenPattern   = regexp.MustCompile(`\$(?:response|event)(?:\.[a-zA-Z0-9_]+|\[[0-9]+\])+`)
	inputBarePattern    = regexp.MustCompile(`^\$(?:response|event)$`)
	inputKVStorePattern = regexp.MustCompile(`{{(.*?)}}`)
)

// InputContext is the read-only resolution root for one step.
type InputContext struct {
	// Response is the previous step's payload (params["input"]).
	Response any
	// Event is the triggering event/request (params["config"]["event"]).
	Event any
}

// NewInputContext builds the canonical context root from step params.
func NewInputContext(params map[string]interface{}) InputContext {
	var ctx InputContext
	if params == nil {
		return ctx
	}
	ctx.Response = params[StrRequest]
	if configMap, ok := params[StrConfig].(map[string]interface{}); ok {
		ctx.Event = configMap[StrEvent]
	}
	return ctx
}

// ResolveValue deep-copies value while resolving every input reference it
// contains. Exact-token values keep their JSON type; embedded references
// interpolate as text. Unknown paths return an error.
func ResolveValue(ctx InputContext, value any) (any, error) {
	switch v := value.(type) {
	case string:
		return ResolveStringValue(ctx, v)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			resolved, err := ResolveValue(ctx, item)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case map[interface{}]interface{}:
		converted, ok := cmn.ConvertMapIIToSI(v).(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("input resolution failed: unsupported map shape")
		}
		return ResolveValue(ctx, converted)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			resolved, err := ResolveValue(ctx, item)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}

// ResolveStringValue resolves a string: an exact-token reference returns the
// typed value, otherwise references interpolate as text.
func ResolveStringValue(ctx InputContext, s string) (any, error) {
	trimmed := strings.TrimSpace(s)
	if inputBarePattern.MatchString(trimmed) {
		root := inputRoot(ctx, trimmed)
		return deepCopyScalar(root), nil
	}
	if exact, ok := exactInputToken(trimmed); ok {
		val, err := lookupInputPath(ctx, exact)
		if err != nil {
			return nil, err
		}
		return deepCopyScalar(val), nil
	}
	return interpolateInputString(ctx, s)
}

// ResolveString interpolates a string and requires a string result.
// Exact-token references to non-string values are stringified; unknown paths
// return an error.
func ResolveString(ctx InputContext, s string) (string, error) {
	resolved, err := ResolveStringValue(ctx, s)
	if err != nil {
		return "", err
	}
	if str, ok := resolved.(string); ok {
		return str, nil
	}
	return stringifyInputScalar(resolved)
}

// exactInputToken reports whether s is exactly one input token (no
// surrounding text), returning the token.
func exactInputToken(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "$") {
		return "", false
	}
	if inputBarePattern.MatchString(trimmed) {
		return trimmed, true
	}
	token := inputTokenPattern.FindString(trimmed)
	if token == "" || token != trimmed {
		return "", false
	}
	return token, true
}

// interpolateInputString replaces {{...}} and embedded $response/$event
// tokens inside free text.
func interpolateInputString(ctx InputContext, s string) (string, error) {
	result := inputKVStorePattern.ReplaceAllStringFunc(s, func(token string) string {
		key := strings.TrimSpace(strings.Trim(token, "{}"))
		if key == "" {
			return token
		}
		value, _, err := cmn.KVStore.Get(key, "")
		if err != nil {
			return token
		}
		valueStr, _ := value.(string)
		return valueStr
	})
	var firstErr error
	result = inputTokenPattern.ReplaceAllStringFunc(result, func(token string) string {
		if firstErr != nil {
			return token
		}
		val, err := lookupInputPath(ctx, token)
		if err != nil {
			firstErr = err
			return token
		}
		str, err := stringifyInputScalar(val)
		if err != nil {
			firstErr = err
			return token
		}
		return str
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

// inputRoot returns the document a bare $response/$event token addresses.
func inputRoot(ctx InputContext, token string) any {
	if strings.HasPrefix(token, "$event") {
		return ctx.Event
	}
	return ctx.Response
}

// lookupInputPath resolves one $response/$event token to its typed value.
func lookupInputPath(ctx InputContext, token string) (any, error) {
	rest := strings.TrimPrefix(strings.TrimPrefix(token, "$response"), "$event")
	isEvent := strings.HasPrefix(token, "$event")
	var doc any
	if isEvent {
		doc = ctx.Event
	} else {
		doc = ctx.Response
	}
	segments, err := parseInputPath(rest)
	if err != nil {
		return nil, fmt.Errorf("input resolution failed: malformed reference %q", token)
	}
	if len(segments) == 0 {
		if doc == nil && !isEvent {
			return nil, fmt.Errorf("input resolution failed: %q has no previous payload", token)
		}
		return doc, nil
	}
	val, found := walkInputPath(doc, segments)
	if found {
		return val, nil
	}
	// Deprecated alias: strip a leading input/request segment and retry.
	if len(segments) > 0 {
		if key, ok := segments[0].(string); ok && (key == "input" || key == "request") {
			if val, found := walkInputPath(doc, segments[1:]); found {
				return val, nil
			}
		}
	}
	return nil, fmt.Errorf("input resolution failed: unknown path %q", token)
}

// inputPathSegment is either a map key (string) or an array index (int).
type inputPathSegment = any

// parseInputPath parses ".a.b[0].c" / "[0].a" into segments.
func parseInputPath(rest string) ([]inputPathSegment, error) {
	var segments []inputPathSegment
	i := 0
	for i < len(rest) {
		switch rest[i] {
		case '.':
			i++
			start := i
			for i < len(rest) && rest[i] != '.' && rest[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty path segment")
			}
			segments = append(segments, rest[start:i])
		case '[':
			end := strings.IndexByte(rest[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated index")
			}
			num := rest[i+1 : i+end]
			idx, err := strconv.Atoi(strings.TrimSpace(num))
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid index")
			}
			segments = append(segments, idx)
			i += end + 1
		default:
			return nil, fmt.Errorf("unexpected path character")
		}
	}
	return segments, nil
}

// walkInputPath navigates maps and slices along segments.
func walkInputPath(doc any, segments []inputPathSegment) (any, bool) {
	current := doc
	for _, seg := range segments {
		switch key := seg.(type) {
		case string:
			m, ok := current.(map[string]interface{})
			if !ok {
				if ii, ok := current.(map[interface{}]interface{}); ok {
					if v, exists := ii[key]; exists {
						current = v
						continue
					}
				}
				return nil, false
			}
			v, exists := m[key]
			if !exists {
				return nil, false
			}
			current = v
		case int:
			arr, ok := current.([]interface{})
			if !ok || key >= len(arr) {
				return nil, false
			}
			current = arr[key]
		default:
			return nil, false
		}
	}
	return current, true
}

// stringifyInputScalar renders a resolved value as text for interpolation.
// Maps and slices become JSON; nil becomes "null". It never emits "<nil>".
func stringifyInputScalar(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case string:
		return t, nil
	case map[string]interface{}, []interface{}, map[interface{}]interface{}:
		raw, err := json.Marshal(t)
		if err != nil {
			return "", fmt.Errorf("input resolution failed: cannot render value as text")
		}
		return string(raw), nil
	default:
		return fmt.Sprintf("%v", t), nil
	}
}

// deepCopyScalar copies resolved values so later mutation cannot alias the
// stored payload.
func deepCopyScalar(v any) any {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for key, item := range t {
			out[key] = deepCopyScalar(item)
		}
		return out
	case map[interface{}]interface{}:
		if converted, ok := cmn.ConvertMapIIToSI(t).(map[string]interface{}); ok {
			return deepCopyScalar(converted)
		}
		return v
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = deepCopyScalar(item)
		}
		return out
	default:
		return v
	}
}

// Prior-step propagation contract.
//
// setup.go wires step inputs before each invocation:
//
//   - $response always denotes the immediately preceding step's response
//     payload (lastResult["output"]), never the whole action envelope.
//   - $event denotes the triggering event (params["config"]["event"]) and is
//     stable across steps.
//   - An explicit params["input"] ("request") takes precedence as the step's
//     input, but the previous payload is never silently mutated: mappings
//     merge into a fresh map, scalars stand alone.
//   - When both the explicit request and the payload are mappings they merge
//     with the payload winning collisions (historical precedence). When
//     either side is not a mapping, the explicit value stands alone if
//     present, otherwise the payload passes through unchanged in shape.
//   - Prior-step config mappings merge the same way (prior wins); a missing
//     prior config is ignored, a non-mapping prior config is a descriptive
//     error, never a panic.
//
// All merges deep-copy containers; stored manifests are never mutated.

// normalizeStringMap accepts decoded-YAML and JSON object forms.
func normalizeStringMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case map[interface{}]interface{}:
		converted := cmn.ConvertMapIIToSI(t)
		if out, ok := converted.(map[string]any); ok {
			return out, true
		}
		return nil, false
	default:
		return nil, false
	}
}

// mergeStepInput computes params["input"] from an explicit request (when the
// step author provided one) and the previous step's payload.
func mergeStepInput(params map[string]any, payload any) error {
	if params == nil {
		return fmt.Errorf("invalid step params: nil")
	}
	explicit, hasExplicit := params[StrRequest]
	if !hasExplicit {
		params[StrRequest] = deepCloneStepValue(payload)
		return nil
	}
	explicitMap, explicitIsMap := normalizeStringMap(explicit)
	payloadMap, payloadIsMap := normalizeStringMap(payload)
	if !explicitIsMap || !payloadIsMap {
		// Scalars and mismatched shapes stand alone: the explicit value is
		// complete input by itself, and an absent explicit value passes the
		// payload through untouched in shape.
		return nil
	}
	merged := make(map[string]any, len(explicitMap)+len(payloadMap))
	for k, v := range explicitMap {
		merged[k] = deepCloneStepValue(v)
	}
	for k, v := range payloadMap {
		merged[k] = deepCloneStepValue(v)
	}
	params[StrRequest] = merged
	return nil
}

// mergePriorConfig folds the previous step's config into the current params
// without mutating either side.
func mergePriorConfig(params map[string]any, prior any) error {
	if params == nil {
		return fmt.Errorf("invalid step params: nil")
	}
	if prior == nil {
		return nil
	}
	priorMap, ok := normalizeStringMap(prior)
	if !ok {
		return fmt.Errorf("invalid prior step config: expected mapping")
	}
	existing, hasExisting := params[StrConfig]
	if !hasExisting || existing == nil {
		params[StrConfig] = deepCloneStepValue(priorMap)
		return nil
	}
	existingMap, ok := normalizeStringMap(existing)
	if !ok {
		return fmt.Errorf("invalid step config: expected mapping")
	}
	merged := make(map[string]any, len(existingMap)+len(priorMap))
	for k, v := range existingMap {
		merged[k] = deepCloneStepValue(v)
	}
	for k, v := range priorMap {
		merged[k] = deepCloneStepValue(v)
	}
	params[StrConfig] = merged
	return nil
}

// mergePriorResultKey folds one non-payload, non-config previous-result key
// (status, message, custom keys) into params. Mappings merge with the prior
// value winning; anything else keeps the explicitly configured value.
// Nothing panics and neither side is mutated.
func mergePriorResultKey(params map[string]any, key string, value any) {
	if params == nil {
		return
	}
	existing, present := params[key]
	if !present {
		params[key] = deepCloneStepValue(value)
		return
	}
	existingMap, ok1 := normalizeStringMap(existing)
	valueMap, ok2 := normalizeStringMap(value)
	if !ok1 || !ok2 {
		return
	}
	merged := make(map[string]any, len(existingMap)+len(valueMap))
	for k, v := range existingMap {
		merged[k] = deepCloneStepValue(v)
	}
	for k, v := range valueMap {
		merged[k] = deepCloneStepValue(v)
	}
	params[key] = merged
}

// ActionResult is the typed envelope every action step returns: a status, a
// payload, a message and the optional step config. It mirrors the existing
// map wire format (output/status/message/config) without changing it.
type ActionResult struct {
	Status   string
	Response any
	Message  string
	Config   map[string]interface{}
}

// NewActionResult builds an envelope from step outputs.
func NewActionResult(status string, response any, message string, config map[string]interface{}) ActionResult {
	return ActionResult{Status: status, Response: response, Message: message, Config: config}
}

// ToMap renders the envelope in the historical wire format.
func (r ActionResult) ToMap() map[string]interface{} {
	return map[string]interface{}{
		StrResponse: r.Response,
		StrStatus:   r.Status,
		StrMessage:  r.Message,
		StrConfig:   r.Config,
	}
}

// ActionResultFromMap reads an envelope, reporting whether required fields
// are present.
func ActionResultFromMap(m map[string]interface{}) (ActionResult, bool) {
	if m == nil {
		return ActionResult{}, false
	}
	status, _ := m[StrStatus].(string)
	message, _ := m[StrMessage].(string)
	if status == "" {
		return ActionResult{}, false
	}
	config, _ := m[StrConfig].(map[string]interface{})
	return ActionResult{
		Status:   status,
		Response: m[StrResponse],
		Message:  message,
		Config:   config,
	}, true
}
