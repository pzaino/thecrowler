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
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Phase 3 trust boundary: tools are vetted Go implementations registered
// explicitly by the operator. Model-supplied names and arguments are
// untrusted data and never confer registration. The registry is
// instance-scoped (never a mutable process-wide namespace) so independent
// runs and tests stay isolated.

// AgentTool is one trusted, read-only tool implementation.
type AgentTool interface {
	// Name is the stable wire identifier (letters, digits, _ and -).
	Name() string
	// Description documents the tool for model advertisement.
	Description() string
	// InputSchema is the Draft-07 parameters schema for arguments.
	InputSchema() map[string]any
	// RequiredCapabilities lists identity capabilities the caller must hold.
	RequiredCapabilities() []string
	// Execute runs the tool. It must be read-only, bounded, and
	// context-aware; identity and resource scope arrive separately (see
	// ToolRuntimeFromContext), never through model arguments.
	Execute(ctx context.Context, args map[string]any) (map[string]any, error)
}

// Tool registry admission bounds.
const (
	// maxToolNameLen caps tool identifiers.
	maxToolNameLen = 64
	// maxToolDescriptionLen caps advertised descriptions.
	maxToolDescriptionLen = 2048
)

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// AgentToolRegistry is an instance-scoped, concurrency-safe tool index.
// Declaration order is preserved for deterministic advertisement.
type AgentToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]AgentTool
	order []string
}

// NewAgentToolRegistry creates an empty registry.
func NewAgentToolRegistry() *AgentToolRegistry {
	return &AgentToolRegistry{tools: map[string]AgentTool{}}
}

// Register vets and stores one tool. Schemas are deep-copied on the way in
// so later caller mutation cannot change the registered contract.
func (r *AgentToolRegistry) Register(tool AgentTool) error {
	if r == nil {
		return fmt.Errorf("nil tool registry")
	}
	if tool == nil {
		return fmt.Errorf("invalid tool: nil implementation")
	}
	name := strings.TrimSpace(tool.Name())
	if !toolNamePattern.MatchString(name) || len(name) > maxToolNameLen {
		return fmt.Errorf("invalid tool name %q: letters, digits, _ and -, up to %d chars", tool.Name(), maxToolNameLen)
	}
	if strings.TrimSpace(tool.Description()) == "" {
		return fmt.Errorf("invalid tool %q: empty description", name)
	}
	if len(tool.Description()) > maxToolDescriptionLen {
		return fmt.Errorf("invalid tool %q: description exceeds %d bytes", name, maxToolDescriptionLen)
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		return fmt.Errorf("invalid tool %q: empty input schema", name)
	}
	schemaType, _ := schema["type"].(string)
	if !strings.EqualFold(strings.TrimSpace(schemaType), "object") {
		return fmt.Errorf("invalid tool %q: input schema type must be object", name)
	}
	if err := checkParameterSchema(schema, 0); err != nil {
		return fmt.Errorf("invalid tool %q input schema: %v", name, err)
	}
	encoded, err := json.Marshal(schema)
	if err != nil || len(encoded) > maxLLMToolsBytes {
		return fmt.Errorf("invalid tool %q: input schema exceeds %d bytes", name, maxLLMToolsBytes)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = map[string]AgentTool{}
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("duplicate tool %q", name)
	}
	// Snapshot admission-time metadata: later caller mutation of the
	// submitted schema, description, or capabilities must not alter the
	// registered contract.
	schemaCopy, _ := deepCloneStepValue(schema).(map[string]any)
	if schemaCopy == nil {
		schemaCopy = map[string]any{}
	}
	capsCopy := append([]string(nil), tool.RequiredCapabilities()...)
	r.tools[name] = &frozenTool{
		name:         name,
		description:  tool.Description(),
		schema:       schemaCopy,
		capabilities: capsCopy,
		inner:        tool,
	}
	r.order = append(r.order, name)
	return nil
}

// frozenTool is the registry's admission snapshot: metadata reads serve
// frozen copies while execution delegates to the vetted implementation.
type frozenTool struct {
	name         string
	description  string
	schema       map[string]any
	capabilities []string
	inner        AgentTool
}

func (f *frozenTool) Name() string { return f.name }

func (f *frozenTool) Description() string { return f.description }

func (f *frozenTool) InputSchema() map[string]any {
	if cloned, ok := deepCloneStepValue(f.schema).(map[string]any); ok && cloned != nil {
		return cloned
	}
	return map[string]any{}
}

func (f *frozenTool) RequiredCapabilities() []string {
	return append([]string(nil), f.capabilities...)
}

func (f *frozenTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	return f.inner.Execute(ctx, args)
}

// Get returns the tool implementation for a name.
func (r *AgentToolRegistry) Get(name string) (AgentTool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[strings.TrimSpace(name)]
	return tool, ok
}

// Len reports the number of registered tools.
func (r *AgentToolRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Names returns registered tool names in deterministic (sorted) order.
func (r *AgentToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

// Snapshot returns the registered tools in deterministic (sorted) order.
func (r *AgentToolRegistry) Snapshot() []AgentTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	out := make([]AgentTool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

// InputSchemaCopy returns a deep copy of a tool's input schema, or nil.
func (r *AgentToolRegistry) InputSchemaCopy(name string) map[string]any {
	tool, ok := r.Get(name)
	if !ok {
		return nil
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		return map[string]any{}
	}
	if cloned, ok := deepCloneStepValue(schema).(map[string]any); ok {
		return cloned
	}
	return map[string]any{}
}

// ToolDefinitions exports the registry in Phase 2 transport shape for
// provider advertisement, in deterministic order with defensive copies.
func (r *AgentToolRegistry) ToolDefinitions() []LLMToolDefinition {
	tools := r.Snapshot()
	defs := make([]LLMToolDefinition, 0, len(tools))
	for _, tool := range tools {
		params := r.InputSchemaCopy(tool.Name())
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		defs = append(defs, LLMToolDefinition{
			Type: "function",
			Function: LLMFunctionSpec{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  params,
			},
		})
	}
	return defs
}
