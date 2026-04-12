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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

// ValidationMode controls semantic validation strictness.
type ValidationMode string

const (
	// ValidationModeLenient keeps backward compatibility and skips strict semantic checks.
	ValidationModeLenient ValidationMode = "lenient"
	// ValidationModeStrict enforces semantic checks that are not expressible via JSON schema.
	ValidationModeStrict ValidationMode = "strict"
)

// ValidationIssue contains a single actionable validation issue with path-level context.
type ValidationIssue struct {
	Path    string
	Message string
}

// ValidationError aggregates multiple issues from schema and semantic validation.
type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "validation failed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, is := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", is.Path, is.Message))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func (e *ValidationError) add(path, message string) {
	if strings.TrimSpace(path) == "" {
		path = "$"
	}
	e.Issues = append(e.Issues, ValidationIssue{Path: path, Message: message})
}

func (e *ValidationError) hasIssues() bool {
	return e != nil && len(e.Issues) > 0
}

// ValidateAgentConfig validates YAML/JSON bytes against schema and semantic rules.
// Lenient mode preserves backward compatibility by enforcing schema validation only.
// Strict mode additionally enforces semantic rules like naming conventions and decision targets.
func ValidateAgentConfig(data []byte, fileType string, mode ValidationMode, registry *AgentRegistry) error {
	normalizedDoc, jsonDoc, err := decodeToJSONEquivalent(data, fileType)
	if err != nil {
		return err
	}

	schemaData, err := loadAgentSchema()
	if err != nil {
		return err
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return fmt.Errorf("failed to parse agent schema: %w", err)
	}

	keyErrs, err := schema.ValidateBytes(context.Background(), jsonDoc)
	if err != nil {
		return fmt.Errorf("failed to run schema validation: %w", err)
	}

	ve := &ValidationError{}
	for _, keyErr := range keyErrs {
		path := strings.TrimSpace(keyErr.PropertyPath)
		if path == "" {
			path = "$"
		}
		ve.add(path, strings.TrimSpace(keyErr.Message))
	}
	if ve.hasIssues() {
		return ve
	}

	if mode != ValidationModeStrict {
		return nil
	}

	strictIssues := validateSemanticRules(normalizedDoc, registry)
	if strictIssues.hasIssues() {
		return strictIssues
	}
	return nil
}

func decodeToJSONEquivalent(data []byte, fileType string) (map[string]any, []byte, error) {
	var raw any
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), ".")) {
	case "json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("invalid json: %w", err)
		}
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("invalid yaml: %w", err)
		}
		raw = toJSONCompatible(raw)
	default:
		return nil, nil, fmt.Errorf("unsupported file format: %s", fileType)
	}

	doc, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("config root must be an object")
	}
	jsonDoc, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to normalize config to json: %w", err)
	}
	return doc, jsonDoc, nil
}

func toJSONCompatible(v any) any {
	switch t := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprintf("%v", k)] = toJSONCompatible(val)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = toJSONCompatible(val)
		}
		return m
	case []any:
		arr := make([]any, len(t))
		for i := range t {
			arr[i] = toJSONCompatible(t[i])
		}
		return arr
	default:
		return v
	}
}

func loadAgentSchema() ([]byte, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to resolve validation schema path")
	}
	schemaPath := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../schemas/crowler-agent-schema.json"))
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent schema %s: %w", schemaPath, err)
	}
	return data, nil
}

var semanticNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{1,127}$`)

func validateSemanticRules(doc map[string]any, registry *AgentRegistry) *ValidationError {
	ve := &ValidationError{}

	if aiRaw, ok := doc["agent_identity"]; ok {
		if ai, ok := aiRaw.(map[string]any); ok {
			if name, _ := ai["name"].(string); strings.TrimSpace(name) != "" && !semanticNamePattern.MatchString(name) {
				ve.add("agent_identity.name", "must match ^[A-Za-z0-9][A-Za-z0-9 _.-]{1,127}$")
			}
			if memRaw, ok := ai["memory"].(map[string]any); ok {
				if ttl, _ := memRaw["ttl"].(string); strings.TrimSpace(ttl) != "" {
					if _, err := time.ParseDuration(strings.TrimSpace(ttl)); err != nil {
						ve.add("agent_identity.memory.ttl", "must be a valid Go duration (for example, '30s' or '10m')")
					}
				}
				switch ret := memRaw["retention"].(type) {
				case int:
					if ret < 0 {
						ve.add("agent_identity.memory.retention", "must be >= 0")
					}
				case float64:
					if ret < 0 {
						ve.add("agent_identity.memory.retention", "must be >= 0")
					}
				}
			}
		}
	}

	jobs, _ := doc["jobs"].([]any)
	for i, jobRaw := range jobs {
		job, ok := jobRaw.(map[string]any)
		if !ok {
			continue
		}
		jobPath := fmt.Sprintf("jobs[%d]", i)
		jobName, _ := job["name"].(string)
		if strings.TrimSpace(jobName) != "" && !semanticNamePattern.MatchString(jobName) {
			ve.add(jobPath+".name", "must match ^[A-Za-z0-9][A-Za-z0-9 _.-]{1,127}$")
		}
		triggerType, _ := job["trigger_type"].(string)
		triggerName, _ := job["trigger_name"].(string)
		if strings.TrimSpace(triggerType) == "" || strings.TrimSpace(triggerName) == "" {
			ve.add(jobPath+".trigger", "trigger_type and trigger_name must both be set")
		}

		steps, _ := job["steps"].([]any)
		for sIdx, stepRaw := range steps {
			step, ok := stepRaw.(map[string]any)
			if !ok {
				continue
			}
			action, _ := step["action"].(string)
			if action != "Decision" {
				continue
			}
			stepPath := fmt.Sprintf("%s.steps[%d]", jobPath, sIdx)
			params, _ := step["params"].(map[string]any)
			if params == nil {
				continue
			}
			cond, _ := params["condition"].(map[string]any)
			checkDecisionTarget(cond, "on_true", stepPath+".params.condition.on_true", ve, doc, registry)
			checkDecisionTarget(cond, "on_false", stepPath+".params.condition.on_false", ve, doc, registry)
		}
	}

	return ve
}

func checkDecisionTarget(condition map[string]any, branch, path string, ve *ValidationError, doc map[string]any, registry *AgentRegistry) {
	if condition == nil {
		return
	}
	branchRaw, ok := condition[branch]
	if !ok {
		return
	}
	branchStep, ok := branchRaw.(map[string]any)
	if !ok {
		return
	}
	targetID, _ := branchStep["agent_id"].(string)
	targetName, _ := branchStep["agent_name"].(string)
	if strings.TrimSpace(targetName) == "" {
		targetName, _ = branchStep["call_agent"].(string)
	}
	if strings.TrimSpace(targetID) == "" && strings.TrimSpace(targetName) == "" {
		ve.add(path, "Decision branch must include one of agent_id, agent_name, or call_agent")
		return
	}
	if strings.TrimSpace(targetID) != "" && resolvableAgentID(targetID, doc, registry) {
		return
	}
	if strings.TrimSpace(targetName) != "" && resolvableAgentName(targetName, doc, registry) {
		return
	}
	ve.add(path, "Decision target is not resolvable to a registered or local agent")
}

func resolvableAgentName(name string, doc map[string]any, registry *AgentRegistry) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if registry != nil {
		if _, ok := registry.GetByName(name); ok {
			return true
		}
	}
	if aiRaw, ok := doc["agent_identity"]; ok {
		if ai, ok := aiRaw.(map[string]any); ok {
			if localName, _ := ai["name"].(string); strings.TrimSpace(localName) == strings.TrimSpace(name) {
				return true
			}
		}
	}
	jobs, _ := doc["jobs"].([]any)
	for _, jobRaw := range jobs {
		job, ok := jobRaw.(map[string]any)
		if !ok {
			continue
		}
		if jobName, _ := job["name"].(string); strings.TrimSpace(jobName) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func resolvableAgentID(agentID string, doc map[string]any, registry *AgentRegistry) bool {
	if strings.TrimSpace(agentID) == "" {
		return false
	}
	if registry != nil {
		if _, ok := registry.GetByID(agentID); ok {
			return true
		}
	}
	if aiRaw, ok := doc["agent_identity"]; ok {
		if ai, ok := aiRaw.(map[string]any); ok {
			if localID, _ := ai["agent_id"].(string); strings.TrimSpace(localID) == strings.TrimSpace(agentID) {
				return true
			}
		}
	}
	return false
}
