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

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Phase 3 authorization boundary. Every tool call proposed by a model is
// authorized IN THIS ORDER before any handler runs:
//
//  1. explicit tool-loop opt-in with unexpired run/group contexts;
//  2. valid identity snapshot with identity enforcement enabled
//     (no anonymous execution);
//  3. tool present in the trusted registry;
//  4. tool present in the trusted explicit run allowlist (`all` never
//     satisfies this);
//  5. every tool-required capability present (or Phase 1 `all` wildcard);
//  6. trust floor and contract (no forbidden "tools"/"tool:<name>");
//  7. arguments are a bounded JSON object validating as an INSTANCE of the
//     registered Draft-07 schema;
//  8. tool-specific resource scope;
//  9. time/count/result budgets remain.
//
// Denials carry stable reason codes, never argument values or secrets.

// Tool authorization reason codes (stable, audited, safe to expose).
const (
	ToolDenyNoOptIn         = "no_tool_loop_opt_in"
	ToolDenyNoIdentity      = "missing_identity"
	ToolDenyEnforcementOff  = "identity_enforcement_required"
	ToolDenyUnknownTool     = "unknown_tool"
	ToolDenyNotAllowlisted  = "not_allowlisted"
	ToolDenyCapability      = "missing_capability"
	ToolDenyTrust           = "insufficient_trust"
	ToolDenyContract        = "forbidden_by_contract"
	ToolDenyArguments       = "invalid_arguments"
	ToolDenyScope           = "scope_denied"
	ToolDenyBudget          = "budget_exhausted"
	ToolDenyContext         = "context_expired"
	ToolDenyDuplicateCallID = "duplicate_call_id"
)

// minToolTrustRank is the Phase 3 execution floor: callers need at least
// "trusted" standing however their capabilities read.
const minToolTrustRank = 2

// maxToolArgsBytes bounds one call's serialized arguments.
const maxToolArgsBytes = 4 << 10

// maxToolArgsDepth bounds argument nesting.
const maxToolArgsDepth = 16

// ToolAuthContext is the trusted authorization input for one call. It is
// built by the loop from run configuration and identity, never from model
// output.
type ToolAuthContext struct {
	Identity       AgentIdentity
	Enforcement    bool
	Allowlist      map[string]bool
	AllowedSources map[uint64]bool
	RunID          string
	TraceID        string
}

// ToolAuthError is a policy denial with a stable machine-readable code.
type ToolAuthError struct {
	Code   string
	Reason string
}

func (e *ToolAuthError) Error() string {
	return fmt.Sprintf("tool denied (%s): %s", e.Code, e.Reason)
}

func denyTool(code, format string, args ...any) *ToolAuthError {
	return &ToolAuthError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// AuthorizeToolCall enforces the full authorization order for one proposed
// call. args must already be a JSON object (see normalizeToolArguments).
func AuthorizeToolCall(auth ToolAuthContext, tool AgentTool, args map[string]any) error {
	if tool == nil {
		return denyTool(ToolDenyUnknownTool, "unknown tool")
	}
	name := tool.Name()
	if strings.TrimSpace(auth.RunID) == "" {
		return denyTool(ToolDenyNoOptIn, "tool loop run not initialized")
	}
	if strings.TrimSpace(auth.Identity.AgentID) == "" {
		return denyTool(ToolDenyNoIdentity, "missing caller identity")
	}
	if !auth.Enforcement {
		return denyTool(ToolDenyEnforcementOff, "identity enforcement is required for tool execution")
	}
	// The explicit run allowlist is mandatory: an empty or missing entry
	// denies, and the Phase 1 `all` wildcard never satisfies it.
	if !auth.Allowlist[name] {
		return denyTool(ToolDenyNotAllowlisted, "tool %q is not in the run allowlist", name)
	}
	for _, required := range tool.RequiredCapabilities() {
		if !identityGrants(auth.Identity, required) {
			return denyTool(ToolDenyCapability, "tool %q requires capability %q", name, required)
		}
	}
	if trustLevelRank(auth.Identity.TrustLevel) < minToolTrustRank {
		return denyTool(ToolDenyTrust, "tool %q requires trusted standing", name)
	}
	if toolForbiddenByContract(auth.Identity.Contract, name) {
		return denyTool(ToolDenyContract, "tool %q is forbidden by contract", name)
	}
	if err := checkToolArguments(name, tool, args); err != nil {
		return err
	}
	return nil
}

// identityGrants reports capability coverage honoring the Phase 1 `all`
// wildcard for capability matching only (never for allowlisting).
func identityGrants(identity AgentIdentity, required string) bool {
	required = strings.ToLower(strings.TrimSpace(required))
	if required == "" {
		return true
	}
	for _, capability := range identity.Capabilities {
		normalized := strings.ToLower(strings.TrimSpace(capability))
		if normalized == "all" || normalized == required {
			return true
		}
	}
	return false
}

// toolForbiddenByContract matches "tools" (every tool) and "tool:<name>".
func toolForbiddenByContract(contract *AgentContract, name string) bool {
	if contract == nil {
		return false
	}
	for _, token := range contract.ForbiddenActions {
		normalized := strings.ToLower(strings.TrimSpace(token))
		if normalized == "tools" {
			return true
		}
		if strings.HasPrefix(normalized, "tool:") {
			if strings.TrimSpace(strings.TrimPrefix(normalized, "tool:")) == strings.ToLower(strings.TrimSpace(name)) {
				return true
			}
		}
	}
	return false
}

// checkToolArguments enforces argument bounds and Draft-07 instance
// validation against the registered schema.
func checkToolArguments(name string, tool AgentTool, args map[string]any) error {
	if args == nil {
		return denyTool(ToolDenyArguments, "tool %q arguments must be an object", name)
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > maxToolArgsBytes {
		return denyTool(ToolDenyArguments, "tool %q arguments exceed size bounds", name)
	}
	if argsDepth(args, 0) > maxToolArgsDepth {
		return denyTool(ToolDenyArguments, "tool %q arguments exceed nesting bounds", name)
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		return denyTool(ToolDenyArguments, "tool %q has no input schema", name)
	}
	if err := validateArgsAgainstSchema(name, schema, args); err != nil {
		return err
	}
	return nil
}

// argsDepth measures JSON nesting depth of decoded arguments.
func argsDepth(v any, depth int) int {
	max := depth
	switch t := v.(type) {
	case map[string]any:
		for _, item := range t {
			if d := argsDepth(item, depth+1); d > max {
				max = d
			}
		}
	case []any:
		for _, item := range t {
			if d := argsDepth(item, depth+1); d > max {
				max = d
			}
		}
	}
	return max
}

// validateArgsAgainstSchema validates an argument INSTANCE against the
// registered schema. Local $refs are expanded first because the instance
// validator cannot resolve in-document references; external references were
// already rejected at registration.
func validateArgsAgainstSchema(toolName string, schema map[string]any, args map[string]any) error {
	expanded, err := expandLocalRefs(schema)
	if err != nil {
		return denyTool(ToolDenyArguments, "tool %q schema failed to expand: %s", toolName, shortSchemaError(err))
	}
	schemaDoc, err := json.Marshal(expanded)
	if err != nil {
		return denyTool(ToolDenyArguments, "tool %q schema is not serializable", toolName)
	}
	compiled, err := compileJSONSchema(schemaDoc)
	if err != nil {
		return denyTool(ToolDenyArguments, "tool %q schema failed to compile", toolName)
	}
	argsDoc, err := json.Marshal(args)
	if err != nil {
		return denyTool(ToolDenyArguments, "tool %q arguments are not serializable", toolName)
	}
	faults, err := compiled.ValidateBytes(context.Background(), argsDoc)
	if err != nil {
		return denyTool(ToolDenyArguments, "tool %q arguments failed validation", toolName)
	}
	if len(faults) > 0 {
		return denyTool(ToolDenyArguments, "tool %q arguments failed validation", toolName)
	}
	return nil
}

// expandLocalRefs replaces {"$ref": "#/..."} nodes with the pointed
// subschema. Cycles and dangling pointers are explicit errors; only local
// references are accepted (registration already rejects external ones).
func expandLocalRefs(schema map[string]any) (map[string]any, error) {
	expanded, err := expandLocalRefsAt(schema, schema, map[string]bool{}, 0)
	if err != nil {
		return nil, err
	}
	out, ok := expanded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema root must expand to an object")
	}
	return out, nil
}

func expandLocalRefsAt(node, root any, stack map[string]bool, depth int) (any, error) {
	if depth > maxToolArgsDepth*4 {
		return nil, fmt.Errorf("reference expansion too deep")
	}
	obj, ok := normalizeStringMap(node)
	if !ok {
		if list, ok := node.([]any); ok {
			out := make([]any, len(list))
			for i, item := range list {
				expanded, err := expandLocalRefsAt(item, root, stack, depth+1)
				if err != nil {
					return nil, err
				}
				out[i] = expanded
			}
			return out, nil
		}
		return node, nil
	}
	if rawRef, present := obj["$ref"]; present && rawRef != nil {
		target, ok := rawRef.(string)
		if !ok || !strings.HasPrefix(strings.TrimSpace(target), "#") {
			return nil, fmt.Errorf("unsupported reference")
		}
		target = strings.TrimSpace(target)
		if stack[target] {
			return nil, fmt.Errorf("cyclic reference %q", target)
		}
		var resolved any
		var found bool
		if target == "#" {
			resolved, found = root, true
		} else {
			resolved, found = resolveLocalRef(asStringMap(root), strings.TrimPrefix(target, "#/"))
		}
		if !found {
			return nil, fmt.Errorf("dangling reference %q", target)
		}
		stack[target] = true
		expanded, err := expandLocalRefsAt(resolved, root, stack, depth+1)
		delete(stack, target)
		if err != nil {
			return nil, err
		}
		return expanded, nil
	}
	out := make(map[string]any, len(obj))
	for key, item := range obj {
		expanded, err := expandLocalRefsAt(item, root, stack, depth+1)
		if err != nil {
			return nil, err
		}
		out[key] = expanded
	}
	return out, nil
}

func asStringMap(v any) map[string]any {
	if m, ok := normalizeStringMap(v); ok {
		return m
	}
	return map[string]any{}
}

// toolRuntimeKey carries trusted execution dependencies through contexts.
type toolRuntimeKey struct{}

// ToolRuntime bundles what handlers need beyond model arguments: the
// database handler, the authorizing context, and the absolute call
// deadline. Adapters obtain it via ToolRuntimeFromContext; model arguments
// never carry these values.
type ToolRuntime struct {
	DB       cdb.Handler
	Auth     ToolAuthContext
	Deadline time.Time
}

// ToolRuntimeFromContext extracts trusted execution dependencies. The second
// return is false when the context carries none (direct unit calls).
func ToolRuntimeFromContext(ctx context.Context) (ToolRuntime, bool) {
	if ctx == nil {
		return ToolRuntime{}, false
	}
	runtime, ok := ctx.Value(toolRuntimeKey{}).(ToolRuntime)
	return runtime, ok
}

// ContextWithToolRuntime attaches trusted execution dependencies.
func ContextWithToolRuntime(ctx context.Context, runtime ToolRuntime) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolRuntimeKey{}, runtime)
}
