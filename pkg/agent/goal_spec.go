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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Goal specification parsing, validation and load-time freezing. Authority is
// read once from the manifest; execution uses the frozen *AgentGoalSpec.

var (
	goalIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	goalCriterionPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	goalPathPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}(\.[A-Za-z0-9_-]{1,64}|\[[0-9]{1,3}\])*$`)
	goalHexPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

var goalAllowedTopKeys = map[string]bool{
	"goal_id": true, "objective": true, "provider": true, "url": true,
	"auth": true, "model": true, "prompt": true, "messages": true,
	"allowlist": true, "allowed_sources": true, "stop_on_tool_error": true,
	"evidence": true, "budgets": true, "acceptance": true,
}

var goalEvidenceKinds = map[EvidenceKind]bool{
	EvidenceSourceStatus:    true,
	EvidenceSourceMetadata:  true,
	EvidenceIndexedPage:     true,
	EvidenceScrapedField:    true,
	EvidenceArtifact:        true,
	EvidenceToolObservation: true,
}

var goalVerifiedFields = map[EvidenceKind]map[string]bool{
	EvidenceSourceStatus: {
		"source_id": true, "uid": true, "status": true, "priority": true,
		"restricted": true, "disabled": true, "flags": true,
		"created_at": true, "last_updated_at": true, "last_crawled_at": true,
	},
	EvidenceSourceMetadata: {
		"source_id": true, "uid": true, "name": true, "url": true,
		"priority": true, "category": true, "restricted": true, "flags": true,
	},
	EvidenceIndexedPage: {
		"index_id": true, "source_uid": true, "page_url": true,
		"created_at": true, "last_updated_at": true, "rank": true,
	},
	EvidenceScrapedField: {
		"source_uid": true, "page_url": true, "json_field": true,
		"created_at": true, "last_updated_at": true, "rank": true,
	},
	EvidenceArtifact: {
		"source_type": true, "artifact_id": true, "source_uid": true,
		"page_url": true, "created_at": true, "last_updated_at": true, "rank": true,
	},
}

var goalToolVerifiedFields = map[string]map[string]bool{
	"get_source_status": {
		"source_id": true, "uid": true, "name": true, "url": true,
		"priority": true, "category": true, "restricted": true, "flags": true,
	},
	"search_indexed_pages": {"count": true, "query": true},
}

// freezeGoalSpecs parses and freezes every AgentGoalRun step in this config.
// It is idempotent and is called by the manifest loader and normalization
// path. A direct action invocation without a frozen spec fails closed.
func (jc *JobConfig) freezeGoalSpecs() error {
	if jc == nil {
		return nil
	}
	alreadyFrozen := false
	for i := range jc.Jobs {
		if len(jc.Jobs[i].goalSpecs) > 0 {
			alreadyFrozen = true
			break
		}
	}
	if alreadyFrozen {
		return nil
	}

	seenGoalIDs := map[string]bool{}
	for jobIndex := range jc.Jobs {
		job := &jc.Jobs[jobIndex]
		specs := make([]*AgentGoalSpec, len(job.Steps))
		hasGoal := false
		for stepIndex, step := range job.Steps {
			action, _ := step["action"].(string)
			if !strings.EqualFold(strings.TrimSpace(action), "AgentGoalRun") {
				continue
			}
			if strings.TrimSpace(jc.FormatVersion) != AgentFormatVersionV2 || jc.AgentIdentity == nil {
				return fmt.Errorf("job %q step %d: AgentGoalRun requires format_version v2 and agent_identity", job.Name, stepIndex)
			}
			params, ok := normalizeStringMap(step["params"])
			if !ok {
				return fmt.Errorf("job %q step %d: AgentGoalRun params must be an object", job.Name, stepIndex)
			}
			spec, err := parseAgentGoalSpec(params, jc.AgentIdentity, job.Name, stepIndex)
			if err != nil {
				return fmt.Errorf("job %q step %d: %w", job.Name, stepIndex, err)
			}
			if !identityGrants(*jc.AgentIdentity, "goal_execution") {
				return fmt.Errorf("job %q step %d: identity requires capability %q", job.Name, stepIndex, "goal_execution")
			}
			if trustLevelRank(jc.AgentIdentity.TrustLevel) < minToolTrustRank {
				return fmt.Errorf("job %q step %d: AgentGoalRun requires trusted standing", job.Name, stepIndex)
			}
			if err := validateGoalTools(spec); err != nil {
				return fmt.Errorf("job %q step %d: %w", job.Name, stepIndex, err)
			}
			if seenGoalIDs[spec.GoalID] {
				return fmt.Errorf("job %q step %d: duplicate goal_id %q", job.Name, stepIndex, spec.GoalID)
			}
			seenGoalIDs[spec.GoalID] = true
			if err := rejectGoalInFallback(step); err != nil {
				return fmt.Errorf("job %q step %d: %w", job.Name, stepIndex, err)
			}
			specs[stepIndex] = spec
			hasGoal = true
		}
		if hasGoal {
			job.goalSpecs = specs
		}
	}
	return nil
}

// rejectGoalInFallback keeps v1 goal authority simple: goal runs cannot live
// inside an inline fallback chain, where the engine's frozen-spec lookup does
// not apply.
func rejectGoalInFallback(step map[string]any) error {
	raw, ok := step["fallback"]
	if !ok || raw == nil {
		return nil
	}
	var entries []map[string]any
	switch v := raw.(type) {
	case []map[string]any:
		entries = v
	case []any:
		for _, item := range v {
			if m, ok := normalizeStringMap(item); ok {
				entries = append(entries, m)
			}
		}
	}
	for _, entry := range entries {
		action, _ := entry["action"].(string)
		if strings.EqualFold(strings.TrimSpace(action), "AgentGoalRun") {
			return fmt.Errorf("AgentGoalRun is not allowed inside fallback steps")
		}
	}
	return nil
}

// parseAgentGoalSpec parses and validates one goal spec. It never mutates the
// input map.
func parseAgentGoalSpec(params map[string]any, identity *AgentIdentity, jobName string, stepIndex int) (*AgentGoalSpec, error) {
	if params == nil {
		return nil, fmt.Errorf("goal params are missing")
	}
	for key := range params {
		if !goalAllowedTopKeys[key] {
			return nil, fmt.Errorf("unknown AgentGoalRun param %q", key)
		}
	}
	for _, key := range []string{"goal_id", "objective", "provider", "url", "auth", "model", "allowlist", "allowed_sources", "evidence", "budgets", "acceptance"} {
		raw, ok := params[key]
		if !ok || raw == nil {
			continue
		}
		if err := rejectInputMarkers(raw); err != nil {
			return nil, fmt.Errorf("%s must be statically declared", key)
		}
	}

	objective, err := requireString(params, "objective", 1, 1024)
	if err != nil {
		return nil, err
	}
	provider, err := requireString(params, "provider", 1, 64)
	if err != nil {
		return nil, err
	}
	model, err := requireString(params, "model", 1, 256)
	if err != nil {
		return nil, err
	}
	urlValue, err := requireString(params, "url", 1, 2048)
	if err != nil {
		return nil, err
	}
	if err := ValidateLLMEndpoint(urlValue); err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	auth, err := optionalString(params, "auth", 4096)
	if err != nil {
		return nil, err
	}

	goalID := ""
	if raw, ok := params["goal_id"]; ok && raw != nil {
		goalID, err = optionalString(params, "goal_id", 128)
		if err != nil {
			return nil, err
		}
		if !goalIDPattern.MatchString(goalID) {
			return nil, fmt.Errorf("invalid goal_id %q", goalID)
		}
	}
	if goalID == "" {
		agentID := "agent"
		if identity != nil && strings.TrimSpace(identity.AgentID) != "" {
			agentID = identity.AgentID
		}
		goalID = deriveGoalID(agentID, jobName, stepIndex)
	}

	messages, prompt, err := parseGoalMessages(params)
	if err != nil {
		return nil, err
	}
	allowlist, err := parseStringList(params["allowlist"], "allowlist", 1, 32, maxToolNameLen, true)
	if err != nil {
		return nil, err
	}
	for _, name := range allowlist {
		if !toolNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid tool name %q in allowlist", name)
		}
	}
	allowedSources, err := parseSourceList(params["allowed_sources"])
	if err != nil {
		return nil, err
	}

	evidencePolicy, err := parseGoalEvidencePolicy(params["evidence"])
	if err != nil {
		return nil, err
	}
	budgets, err := parseGoalBudgets(params["budgets"])
	if err != nil {
		return nil, err
	}
	stopOnToolError := false
	if raw, ok := params["stop_on_tool_error"]; ok && raw != nil {
		flag, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("stop_on_tool_error must be a boolean")
		}
		stopOnToolError = flag
	}

	allowSet := map[EvidenceKind]bool{}
	for _, kind := range evidencePolicy.Allow {
		allowSet[kind] = true
	}
	acceptance, err := parseGoalAcceptance(params["acceptance"], allowSet, allowedSources, allowlist)
	if err != nil {
		return nil, err
	}

	spec := &AgentGoalSpec{
		GoalID:          goalID,
		Objective:       objective,
		Provider:        strings.ToLower(strings.TrimSpace(provider)),
		URL:             strings.TrimSpace(urlValue),
		Auth:            strings.TrimSpace(auth),
		AuthPresent:     strings.TrimSpace(auth) != "",
		Model:           strings.TrimSpace(model),
		Prompt:          prompt,
		Messages:        messages,
		Allowlist:       allowlist,
		AllowedSources:  allowedSources,
		Evidence:        evidencePolicy,
		Budgets:         budgets,
		Acceptance:      acceptance,
		StopOnToolError: stopOnToolError,
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	return spec, nil
}

func deriveGoalID(agentID, jobName string, stepIndex int) string {
	base := normalizeAgentID(agentID + "-" + jobName)
	if base == "" {
		base = "goal"
	}
	base = strings.Trim(base, "-._:")
	var safe strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('-')
		}
	}
	base = strings.Trim(safe.String(), "-._:")
	if base == "" {
		base = "goal"
	}
	if len(base) > 100 {
		base = base[:100]
	}
	return base + "-step-" + strconv.Itoa(stepIndex)
}

func computeGoalSpecHash(spec *AgentGoalSpec) string {
	if spec == nil {
		return ""
	}
	copySpec := *spec
	copySpec.Auth = ""
	copySpec.AuthPresent = spec.Auth != ""
	copySpec.SpecHash = ""
	data, err := json.Marshal(copySpec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseGoalMessages(params map[string]any) ([]any, string, error) {
	prompt := ""
	if raw, ok := params["prompt"]; ok && raw != nil {
		value, ok := raw.(string)
		if !ok {
			return nil, "", fmt.Errorf("prompt must be a string")
		}
		prompt = strings.TrimSpace(value)
		if len(prompt) > 16384 {
			return nil, "", fmt.Errorf("prompt exceeds 16384 characters")
		}
	}
	var messages []any
	if raw, ok := params["messages"]; ok && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			if converted, ok := raw.([]interface{}); ok {
				items = converted
			} else {
				return nil, "", fmt.Errorf("messages must be an array")
			}
		}
		if len(items) == 0 || len(items) > 32 {
			return nil, "", fmt.Errorf("messages must contain 1..32 entries")
		}
		for i, item := range items {
			m, ok := normalizeStringMap(item)
			if !ok {
				return nil, "", fmt.Errorf("messages[%d] must be an object", i)
			}
			role, _ := m["role"].(string)
			role = strings.ToLower(strings.TrimSpace(role))
			if role != "system" && role != "user" && role != "assistant" {
				return nil, "", fmt.Errorf("messages[%d].role must be system, user or assistant", i)
			}
			content, ok := m["content"].(string)
			if !ok || len(content) > 16384 {
				return nil, "", fmt.Errorf("messages[%d].content is invalid", i)
			}
			for key := range m {
				if key != "role" && key != "content" {
					return nil, "", fmt.Errorf("messages[%d] has unknown field %q", i, key)
				}
			}
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	}
	if prompt == "" && len(messages) == 0 {
		return nil, "", fmt.Errorf("at least one of prompt or messages is required")
	}
	return messages, prompt, nil
}

func parseStringList(raw any, field string, min, max, maxLen int, unique bool) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		if converted, ok := raw.([]interface{}); ok {
			items = converted
		} else if list, ok := raw.([]string); ok {
			items = make([]any, len(list))
			for i, v := range list {
				items[i] = v
			}
		} else {
			return nil, fmt.Errorf("%s must be an array", field)
		}
	}
	if len(items) < min || len(items) > max {
		return nil, fmt.Errorf("%s must contain %d..%d entries", field, min, max)
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s entries must be strings", field)
		}
		text = strings.TrimSpace(text)
		if text == "" || len(text) > maxLen {
			return nil, fmt.Errorf("%s entries must be 1..%d characters", field, maxLen)
		}
		if unique && seen[text] {
			return nil, fmt.Errorf("%s entries must be unique", field)
		}
		seen[text] = true
		out = append(out, text)
	}
	return out, nil
}

func parseSourceList(raw any) ([]uint64, error) {
	items, ok := raw.([]any)
	if !ok {
		if converted, ok := raw.([]interface{}); ok {
			items = converted
		} else {
			return nil, fmt.Errorf("allowed_sources must be an array")
		}
	}
	if len(items) == 0 || len(items) > 32 {
		return nil, fmt.Errorf("allowed_sources must contain 1..32 entries")
	}
	out := make([]uint64, 0, len(items))
	seen := map[uint64]bool{}
	for _, item := range items {
		id, err := toSourceIDArg(item)
		if err != nil {
			return nil, fmt.Errorf("allowed_sources: %v", err)
		}
		if seen[id] {
			return nil, fmt.Errorf("allowed_sources entries must be unique")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func parseGoalEvidencePolicy(raw any) (EvidencePolicy, error) {
	policy := EvidencePolicy{
		DefaultMaxAge:   defaultGoalEvidenceMaxAge,
		MaxPayloadBytes: defaultGoalPayloadBytes,
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return policy, fmt.Errorf("evidence must be an object")
	}
	for key := range m {
		if key != "allow" && key != "default_max_age" && key != "max_payload_bytes" {
			return policy, fmt.Errorf("unknown evidence field %q", key)
		}
	}
	allowRaw, ok := m["allow"]
	if !ok {
		return policy, fmt.Errorf("evidence.allow is required")
	}
	items, ok := allowRaw.([]any)
	if !ok {
		if converted, ok := allowRaw.([]interface{}); ok {
			items = converted
		} else if list, ok := allowRaw.([]string); ok {
			items = make([]any, len(list))
			for i, v := range list {
				items[i] = v
			}
		} else {
			return policy, fmt.Errorf("evidence.allow must be an array")
		}
	}
	if len(items) == 0 || len(items) > 8 {
		return policy, fmt.Errorf("evidence.allow must contain 1..8 kinds")
	}
	seen := map[EvidenceKind]bool{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return policy, fmt.Errorf("evidence.allow entries must be strings")
		}
		kind := EvidenceKind(strings.TrimSpace(text))
		if !goalEvidenceKinds[kind] {
			return policy, fmt.Errorf("unsupported evidence kind %q", text)
		}
		if seen[kind] {
			return policy, fmt.Errorf("evidence.allow entries must be unique")
		}
		seen[kind] = true
		policy.Allow = append(policy.Allow, kind)
	}
	if rawAge, ok := m["default_max_age"]; ok && rawAge != nil {
		age, err := parseGoalDuration(rawAge)
		if err != nil || age < time.Second || age > hardGoalEvidenceMaxAge {
			return policy, fmt.Errorf("default_max_age must be 1s..24h")
		}
		policy.DefaultMaxAge = age
	}
	if rawBytes, ok := m["max_payload_bytes"]; ok && rawBytes != nil {
		n, err := toInt(rawBytes)
		if err != nil || n < 256 || n > hardGoalPayloadBytes {
			return policy, fmt.Errorf("max_payload_bytes must be 256..16384")
		}
		policy.MaxPayloadBytes = n
	}
	return policy, nil
}

func parseGoalBudgets(raw any) (GoalBudgets, error) {
	budgets := GoalBudgets{
		MaxModelRounds:        defaultGoalModelRounds,
		MaxToolCalls:          defaultGoalToolCalls,
		MaxEvidenceRecords:    defaultGoalEvidenceRecords,
		MaxEvidenceBytes:      defaultGoalEvidenceBytes,
		MaxTotalResultBytes:   defaultGoalTotalResultBytes,
		MaxElapsed:            defaultGoalMaxElapsed,
		MaxConsecutiveDenials: defaultGoalConsecutiveDenials,
	}
	if raw == nil {
		return budgets, nil
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return budgets, fmt.Errorf("budgets must be an object")
	}
	known := map[string]bool{
		"max_model_rounds": true, "max_tool_calls": true,
		"max_evidence_records": true, "max_evidence_bytes": true,
		"max_total_result_bytes": true, "max_elapsed": true,
		"max_consecutive_denials": true,
	}
	for key := range m {
		if !known[key] {
			return budgets, fmt.Errorf("unknown budget %q", key)
		}
	}
	setInt := func(key string, min, max int, target *int) error {
		value, ok := m[key]
		if !ok || value == nil {
			return nil
		}
		n, err := toInt(value)
		if err != nil || n < min || n > max {
			return fmt.Errorf("%s must be %d..%d", key, min, max)
		}
		*target = n
		return nil
	}
	if err := setInt("max_model_rounds", 1, hardGoalModelRounds, &budgets.MaxModelRounds); err != nil {
		return budgets, err
	}
	if err := setInt("max_tool_calls", 1, hardGoalToolCalls, &budgets.MaxToolCalls); err != nil {
		return budgets, err
	}
	if err := setInt("max_evidence_records", 1, hardGoalEvidenceRecords, &budgets.MaxEvidenceRecords); err != nil {
		return budgets, err
	}
	if err := setInt("max_evidence_bytes", 1024, hardGoalEvidenceBytes, &budgets.MaxEvidenceBytes); err != nil {
		return budgets, err
	}
	if err := setInt("max_total_result_bytes", 1024, hardGoalTotalResultBytes, &budgets.MaxTotalResultBytes); err != nil {
		return budgets, err
	}
	if err := setInt("max_consecutive_denials", 1, hardGoalConsecutiveDenials, &budgets.MaxConsecutiveDenials); err != nil {
		return budgets, err
	}
	if rawElapsed, ok := m["max_elapsed"]; ok && rawElapsed != nil {
		elapsed, err := parseGoalDuration(rawElapsed)
		if err != nil || elapsed < time.Second || elapsed > hardGoalMaxElapsed {
			return budgets, fmt.Errorf("max_elapsed must be 1s..2m")
		}
		budgets.MaxElapsed = elapsed
	}
	return budgets, nil
}

func parseGoalAcceptance(raw any, allow map[EvidenceKind]bool, allowedSources []uint64, allowlist []string) (AcceptanceSpec, error) {
	acceptance := AcceptanceSpec{}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return acceptance, fmt.Errorf("acceptance must be an object")
	}
	for key := range m {
		if key != "mode" && key != "criteria" {
			return acceptance, fmt.Errorf("unknown acceptance field %q", key)
		}
	}
	mode, _ := m["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != string(AcceptanceAll) && mode != string(AcceptanceAny) {
		return acceptance, fmt.Errorf("acceptance.mode must be all or any")
	}
	acceptance.Mode = AcceptanceMode(mode)
	rawCriteria, ok := m["criteria"]
	if !ok {
		return acceptance, fmt.Errorf("acceptance.criteria is required")
	}
	items, ok := rawCriteria.([]any)
	if !ok {
		if converted, ok := rawCriteria.([]interface{}); ok {
			items = converted
		} else {
			return acceptance, fmt.Errorf("acceptance.criteria must be an array")
		}
	}
	if len(items) == 0 || len(items) > 16 {
		return acceptance, fmt.Errorf("acceptance.criteria must contain 1..16 entries")
	}
	seen := map[string]bool{}
	nodes := 0
	for i, item := range items {
		criterion, err := parseGoalCriterion(item, allow, allowedSources, allowlist, 0, &nodes)
		if err != nil {
			return acceptance, fmt.Errorf("criteria[%d]: %w", i, err)
		}
		if seen[criterion.ID] {
			return acceptance, fmt.Errorf("duplicate criterion id %q", criterion.ID)
		}
		seen[criterion.ID] = true
		acceptance.Criteria = append(acceptance.Criteria, criterion)
	}
	if nodes > 32 {
		return acceptance, fmt.Errorf("predicate node count exceeds 32")
	}
	return acceptance, nil
}

func parseGoalCriterion(raw any, allow map[EvidenceKind]bool, allowedSources []uint64, allowlist []string, depth int, nodes *int) (CriterionSpec, error) {
	criterion := CriterionSpec{}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return criterion, fmt.Errorf("criterion must be an object")
	}
	for key := range m {
		if key != "id" && key != "description" && key != "predicate" && key != "freshness" {
			return criterion, fmt.Errorf("unknown criterion field %q", key)
		}
	}
	id, _ := m["id"].(string)
	id = strings.TrimSpace(id)
	if !goalCriterionPattern.MatchString(id) {
		return criterion, fmt.Errorf("invalid criterion id %q", id)
	}
	criterion.ID = id
	description, _ := m["description"].(string)
	description = strings.TrimSpace(description)
	if description == "" || len(description) > 512 {
		return criterion, fmt.Errorf("criterion description must be 1..512 characters")
	}
	criterion.Description = description
	predicate, err := parseGoalPredicate(m["predicate"], allow, allowedSources, allowlist, depth, nodes)
	if err != nil {
		return criterion, err
	}
	criterion.Predicate = predicate
	if rawFreshness, ok := m["freshness"]; ok && rawFreshness != nil {
		fm, ok := normalizeStringMap(rawFreshness)
		if !ok {
			return criterion, fmt.Errorf("freshness must be an object")
		}
		for key := range fm {
			if key != "max_age" {
				return criterion, fmt.Errorf("unknown freshness field %q", key)
			}
		}
		age, err := parseGoalDuration(fm["max_age"])
		if err != nil || age < time.Second || age > hardGoalEvidenceMaxAge {
			return criterion, fmt.Errorf("freshness.max_age must be 1s..24h")
		}
		criterion.Freshness = &FreshnessSpec{MaxAge: age}
	}
	return criterion, nil
}

func parseGoalPredicate(raw any, allow map[EvidenceKind]bool, allowedSources []uint64, allowlist []string, depth int, nodes *int) (Predicate, error) {
	predicate := Predicate{}
	if depth > 4 {
		return predicate, fmt.Errorf("predicate depth exceeds 4")
	}
	*nodes = *nodes + 1
	if *nodes > 32 {
		return predicate, fmt.Errorf("predicate node count exceeds 32")
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return predicate, fmt.Errorf("predicate must be an object")
	}
	kindText, _ := m["kind"].(string)
	kind := PredicateKind(strings.ToLower(strings.TrimSpace(kindText)))
	predicate.Kind = kind
	switch kind {
	case PredicateAll, PredicateAny:
		if err := checkPredicateKeys(m, "kind", "predicates"); err != nil {
			return predicate, err
		}
		items, ok := m["predicates"].([]any)
		if !ok {
			if converted, ok := m["predicates"].([]interface{}); ok {
				items = converted
			} else {
				return predicate, fmt.Errorf("%s.predicates must be an array", kind)
			}
		}
		if len(items) == 0 || len(items) > 8 {
			return predicate, fmt.Errorf("%s.predicates must contain 1..8 entries", kind)
		}
		for i, item := range items {
			child, err := parseGoalPredicate(item, allow, allowedSources, allowlist, depth+1, nodes)
			if err != nil {
				return predicate, fmt.Errorf("%s.predicates[%d]: %w", kind, i, err)
			}
			predicate.Predicates = append(predicate.Predicates, child)
		}
	case PredicateEvidenceExists:
		if err := checkPredicateKeys(m, "kind", "evidence", "where"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
	case PredicateEvidenceCount:
		if err := checkPredicateKeys(m, "kind", "evidence", "where", "op", "value"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
		op, err := parseCompareOp(m["op"])
		if err != nil {
			return predicate, err
		}
		predicate.Op = op
		value, err := toInt(m["value"])
		if err != nil || value < 0 || value > 100000 {
			return predicate, fmt.Errorf("evidence_count.value must be 0..100000")
		}
		predicate.Value = value
	case PredicateFieldEquals:
		if err := checkPredicateKeys(m, "kind", "evidence", "where", "path", "quantifier", "value"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
		if err := parseFieldPath(m, &predicate); err != nil {
			return predicate, err
		}
		value, err := parseGoalScalar(m["value"])
		if err != nil {
			return predicate, err
		}
		predicate.Value = value
	case PredicateFieldIn:
		if err := checkPredicateKeys(m, "kind", "evidence", "where", "path", "quantifier", "values"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
		if err := parseFieldPath(m, &predicate); err != nil {
			return predicate, err
		}
		values, err := parseGoalScalarList(m["values"])
		if err != nil {
			return predicate, err
		}
		predicate.Values = values
	case PredicateFieldNumber:
		if err := checkPredicateKeys(m, "kind", "evidence", "where", "path", "quantifier", "op", "value"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
		if err := parseFieldPath(m, &predicate); err != nil {
			return predicate, err
		}
		op, err := parseCompareOp(m["op"])
		if err != nil {
			return predicate, err
		}
		predicate.Op = op
		number, ok := goalToFloat(m["value"])
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return predicate, fmt.Errorf("evidence_field_number.value must be a finite number")
		}
		predicate.Value = number
	case PredicateEvidenceScope:
		if err := checkPredicateKeys(m, "kind", "evidence", "where", "source_id"); err != nil {
			return predicate, err
		}
		if err := parseEvidenceLeaf(m, &predicate, allow); err != nil {
			return predicate, err
		}
		if raw, ok := m["source_id"]; ok && raw != nil {
			id, err := toSourceIDArg(raw)
			if err != nil {
				return predicate, fmt.Errorf("evidence_scope.source_id must be a positive integer")
			}
			if !containsSource(allowedSources, id) {
				return predicate, fmt.Errorf("evidence_scope.source_id %d is outside allowed_sources", id)
			}
			predicate.SourceID = &id
		}
	case PredicateResourceStatus:
		if err := checkPredicateKeys(m, "kind", "resource", "resource_id", "status"); err != nil {
			return predicate, err
		}
		resource, _ := m["resource"].(string)
		resource = strings.ToLower(strings.TrimSpace(resource))
		if resource != "source" && resource != "information_seed" {
			return predicate, fmt.Errorf("resource_status.resource must be source or information_seed")
		}
		id, err := toSourceIDArg(m["resource_id"])
		if err != nil {
			return predicate, fmt.Errorf("resource_status.resource_id must be a positive integer")
		}
		status, _ := m["status"].(string)
		status = strings.TrimSpace(status)
		if status == "" || len(status) > 64 {
			return predicate, fmt.Errorf("resource_status.status must be 1..64 characters")
		}
		if resource == "source" && !containsSource(allowedSources, id) {
			return predicate, fmt.Errorf("resource_status.resource_id %d is outside allowed_sources", id)
		}
		predicate.Resource = resource
		predicate.ResourceID = id
		predicate.Status = status
	case PredicateEventObserved:
		return predicate, fmt.Errorf("event_provenance_unavailable: event_observed is disabled until trusted producer provenance is approved")
	default:
		return predicate, fmt.Errorf("unsupported predicate kind %q", kindText)
	}
	return predicate, nil
}

func parseEvidenceLeaf(m map[string]any, predicate *Predicate, allow map[EvidenceKind]bool) error {
	kindText, _ := m["evidence"].(string)
	kind := EvidenceKind(strings.ToLower(strings.TrimSpace(kindText)))
	if !goalEvidenceKinds[kind] {
		return fmt.Errorf("unsupported evidence kind %q", kindText)
	}
	if !allow[kind] {
		return fmt.Errorf("evidence kind %q is not in evidence.allow", kind)
	}
	predicate.Evidence = kind
	if rawWhere, ok := m["where"]; ok && rawWhere != nil {
		selector, err := parseGoalSelector(rawWhere)
		if err != nil {
			return err
		}
		predicate.Where = selector
	}
	if err := validateSelectorForKind(kind, predicate.Where); err != nil {
		return err
	}
	if predicate.Quantifier == "" {
		predicate.Quantifier = "any"
	}
	if predicate.Quantifier != "any" && predicate.Quantifier != "all" {
		return fmt.Errorf("quantifier must be any or all")
	}
	return nil
}

func parseGoalSelector(raw any) (*EvidenceSelector, error) {
	m, ok := normalizeStringMap(raw)
	if !ok {
		return nil, fmt.Errorf("where must be an object")
	}
	known := map[string]bool{
		"source_id": true, "page_url": true, "status": true, "field": true,
		"value": true, "tool": true, "arguments_sha256": true,
	}
	for key := range m {
		if !known[key] {
			return nil, fmt.Errorf("unknown where field %q", key)
		}
	}
	selector := &EvidenceSelector{}
	if rawID, ok := m["source_id"]; ok && rawID != nil {
		id, err := toSourceIDArg(rawID)
		if err != nil {
			return nil, fmt.Errorf("where.source_id must be a positive integer")
		}
		selector.SourceID = &id
	}
	for key, target := range map[string]*string{
		"page_url": &selector.PageURL, "status": &selector.Status,
		"field": &selector.Field, "value": &selector.Value,
		"tool": &selector.Tool, "arguments_sha256": &selector.ArgumentsSHA256,
	} {
		if rawValue, ok := m[key]; ok && rawValue != nil {
			text, ok := rawValue.(string)
			if !ok {
				return nil, fmt.Errorf("where.%s must be a string", key)
			}
			*target = strings.TrimSpace(text)
		}
	}
	if len(selector.PageURL) > 2048 || len(selector.Status) > 64 ||
		len(selector.Field) > 128 || len(selector.Value) > 512 {
		return nil, fmt.Errorf("where selector value exceeds bounds")
	}
	if selector.Field != "" && !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`).MatchString(selector.Field) {
		return nil, fmt.Errorf("where.field has an invalid format")
	}
	if selector.Tool != "" && !toolNamePattern.MatchString(selector.Tool) {
		return nil, fmt.Errorf("where.tool has an invalid format")
	}
	if selector.ArgumentsSHA256 != "" && !goalHexPattern.MatchString(selector.ArgumentsSHA256) {
		return nil, fmt.Errorf("where.arguments_sha256 must be a lowercase SHA-256 hex digest")
	}
	return selector, nil
}

func validateSelectorForKind(kind EvidenceKind, selector *EvidenceSelector) error {
	if selector == nil {
		selector = &EvidenceSelector{}
	}
	if kind == EvidenceToolObservation {
		if selector.Tool == "" || selector.ArgumentsSHA256 == "" {
			return fmt.Errorf("tool_observation requires where.tool and where.arguments_sha256")
		}
		if selector.SourceID != nil || selector.PageURL != "" || selector.Status != "" ||
			selector.Field != "" || selector.Value != "" {
			return fmt.Errorf("tool_observation selector has unsupported fields")
		}
		return nil
	}
	if selector.Tool != "" || selector.ArgumentsSHA256 != "" {
		return fmt.Errorf("%s selector cannot use tool/arguments_sha256", kind)
	}
	switch kind {
	case EvidenceSourceMetadata:
		if selector.SourceID == nil {
			return fmt.Errorf("source_metadata requires where.source_id")
		}
	case EvidenceScrapedField, EvidenceArtifact:
		if selector.Field == "" || selector.Value == "" {
			return fmt.Errorf("%s requires where.field and where.value", kind)
		}
		if selector.SourceID == nil {
			return fmt.Errorf("%s requires where.source_id", kind)
		}
	}
	return nil
}

func parseFieldPath(m map[string]any, predicate *Predicate) error {
	path, _ := m["path"].(string)
	path = strings.TrimSpace(path)
	if !goalPathPattern.MatchString(path) {
		return fmt.Errorf("path has an invalid format")
	}
	kind := predicate.Evidence
	root := strings.SplitN(path, ".", 2)[0]
	if idx := strings.Index(root, "["); idx >= 0 {
		root = root[:idx]
	}
	if kind == EvidenceToolObservation {
		tool := ""
		if predicate.Where != nil {
			tool = predicate.Where.Tool
		}
		fields := goalToolVerifiedFields[tool]
		if fields == nil || !fields[root] {
			return fmt.Errorf("untrusted_path: %s.%s is not a verified field", kind, path)
		}
	} else {
		fields := goalVerifiedFields[kind]
		if fields == nil || !fields[root] {
			return fmt.Errorf("untrusted_path: %s.%s is not a verified field", kind, path)
		}
	}
	if rawQuantifier, ok := m["quantifier"]; ok && rawQuantifier != nil {
		text, ok := rawQuantifier.(string)
		if !ok {
			return fmt.Errorf("quantifier must be any or all")
		}
		text = strings.ToLower(strings.TrimSpace(text))
		if text != "any" && text != "all" {
			return fmt.Errorf("quantifier must be any or all")
		}
		predicate.Quantifier = text
	}
	predicate.Path = path
	return nil
}

func parseCompareOp(raw any) (CompareOp, error) {
	text, _ := raw.(string)
	switch CompareOp(strings.ToLower(strings.TrimSpace(text))) {
	case CompareEq:
		return CompareEq, nil
	case CompareLt:
		return CompareLt, nil
	case CompareLte:
		return CompareLte, nil
	case CompareGt:
		return CompareGt, nil
	case CompareGte:
		return CompareGte, nil
	default:
		return "", fmt.Errorf("invalid comparison operator")
	}
}

func parseGoalScalar(raw any) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if len(v) > 512 {
			return nil, fmt.Errorf("string value exceeds 512 characters")
		}
		return v, nil
	case bool:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("numeric value must be finite")
		}
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return nil, fmt.Errorf("value must be a string, number, boolean or null")
	}
}

func parseGoalScalarList(raw any) ([]any, error) {
	items, ok := raw.([]any)
	if !ok {
		if converted, ok := raw.([]interface{}); ok {
			items = converted
		} else {
			return nil, fmt.Errorf("values must be an array")
		}
	}
	if len(items) == 0 || len(items) > 16 {
		return nil, fmt.Errorf("values must contain 1..16 entries")
	}
	out := make([]any, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		value, err := parseGoalScalar(item)
		if err != nil {
			return nil, err
		}
		keyBytes, _ := json.Marshal(value)
		key := string(keyBytes)
		if seen[key] {
			return nil, fmt.Errorf("values must be unique")
		}
		seen[key] = true
		out = append(out, value)
	}
	return out, nil
}

func checkPredicateKeys(m map[string]any, allowed ...string) error {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key := range m {
		if !set[key] {
			return fmt.Errorf("unknown predicate field %q", key)
		}
	}
	return nil
}

func requireString(m map[string]any, key string, min, max int) (string, error) {
	raw, ok := m[key]
	if !ok || raw == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text = strings.TrimSpace(text)
	if len(text) < min || len(text) > max {
		return "", fmt.Errorf("%s must be %d..%d characters", key, min, max)
	}
	return text, nil
}

func optionalString(m map[string]any, key string, max int) (string, error) {
	raw, ok := m[key]
	if !ok || raw == nil {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text = strings.TrimSpace(text)
	if len(text) > max {
		return "", fmt.Errorf("%s exceeds %d characters", key, max)
	}
	return text, nil
}

func parseGoalDuration(raw any) (time.Duration, error) {
	switch v := raw.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return 0, fmt.Errorf("duration is empty")
		}
		return time.ParseDuration(text)
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return time.Duration(v) * time.Second, nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return time.Duration(v) * time.Second, nil
	case float64:
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("duration must be positive")
		}
		return time.Duration(v * float64(time.Second)), nil
	default:
		return 0, fmt.Errorf("duration must be a string or number")
	}
}

func toInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != math.Trunc(v) || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func goalToFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func containsSource(ids []uint64, target uint64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// setGoalRuntimeSpec injects the frozen goal authority and trusted identity
// into the step runtime after all prior-step merges. It overwrites any
// policy-shaped values a prior action may have tried to inject. The action
// fails closed when spec is nil.
func setGoalRuntimeSpec(params map[string]any, spec *AgentGoalSpec, stepIndex int, identity *AgentIdentity, execCtx AgentExecutionContext) {
	if params == nil {
		return
	}
	config, ok := normalizeStringMap(params[StrConfig])
	if !ok || config == nil {
		config = map[string]any{}
	}
	params[StrConfig] = config
	runtime, ok := normalizeStringMap(config[cfgKeyAgentRuntime])
	if !ok || runtime == nil {
		runtime = map[string]any{}
	}
	config[cfgKeyAgentRuntime] = runtime
	if spec != nil {
		runtime["goal_spec"] = spec
	} else {
		delete(runtime, "goal_spec")
	}
	runtime["goal_step_index"] = stepIndex
	if identity != nil {
		id := *identity
		runtime["goal_identity"] = &id
	} else {
		delete(runtime, "goal_identity")
	}
	runtime["goal_owner"] = execCtx.Owner
	runtime["goal_source"] = execCtx.Source
	runtime["goal_correlation_id"] = execCtx.RunID
	runtime["goal_trace_id"] = execCtx.TraceID
}

// validateGoalTools checks the allowlist against the trusted registry and
// verifies that criteria-bearing tool observations name a read-only,
// evidence-capable tool that is explicitly allowlisted.
func validateGoalTools(spec *AgentGoalSpec) error {
	registry, err := DefaultToolRegistry()
	if err != nil {
		return fmt.Errorf("tool registry unavailable: %w", err)
	}
	allow := map[string]bool{}
	for _, name := range spec.Allowlist {
		if _, ok := registry.Get(name); !ok {
			return fmt.Errorf("allowlisted tool %q is not registered", name)
		}
		allow[name] = true
	}
	var selectors []EvidenceSelector
	for _, criterion := range spec.Acceptance.Criteria {
		collectToolSelectors(criterion.Predicate, &selectors)
	}
	for _, selector := range selectors {
		if !allow[selector.Tool] {
			return fmt.Errorf("tool_observation tool %q is not in allowlist", selector.Tool)
		}
		meta, ok := registry.EvidenceMetadata(selector.Tool)
		if !ok || !meta.Supported || !meta.ReadOnly {
			return fmt.Errorf("tool_observation tool %q is not read-only evidence-capable", selector.Tool)
		}
	}
	return nil
}

func collectToolSelectors(predicate Predicate, out *[]EvidenceSelector) {
	if predicate.Kind == PredicateAll || predicate.Kind == PredicateAny {
		for _, child := range predicate.Predicates {
			collectToolSelectors(child, out)
		}
		return
	}
	if predicate.Evidence == EvidenceToolObservation && predicate.Where != nil {
		*out = append(*out, *predicate.Where)
	}
}

// sortedEvidenceKinds returns a deterministic list for diagnostics.
func sortedEvidenceKinds(allow []EvidenceKind) []string {
	out := make([]string, 0, len(allow))
	for _, kind := range allow {
		out = append(out, string(kind))
	}
	sort.Strings(out)
	return out
}
