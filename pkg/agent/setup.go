// Copyright 2023 Paolo Fabio Zaino
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

/*
  The CROWler is the foundation; the user builds and deploys autonomous or semi-autonomous agents that leverage CROWler's capabilities to perform high-level content discovery, threat simulation, and intelligence extraction.
*/

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"gopkg.in/yaml.v2"
)

var (
	// AgentsEngine is the agent engine
	AgentsEngine *JobEngine
	// AgentsRegistry is the default agent configurations repository
	AgentsRegistry *JobConfig
)

// RetryConfig defines retry behavior
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	Backoff    float64
}

// JobConfig represents the structure of a job configuration file
type JobConfig struct {
	// FormatVersion is the compatibility contract marker for agents/job manifests.
	// - v1: legacy jobs-only config (agent_identity omitted and derived at load time)
	// - v2: identity-enabled config (agent_identity present)
	// Backward-compatibility guarantee: jobs-only manifests MUST continue to parse and run unchanged.
	FormatVersion string         `yaml:"format_version,omitempty" json:"format_version,omitempty"`
	AgentIdentity *AgentIdentity `yaml:"agent_identity,omitempty" json:"agent_identity,omitempty"`
	Jobs          []Job          `yaml:"jobs" json:"jobs"`
	registry      *AgentRegistry `yaml:"-" json:"-"`
}

const (
	// AgentFormatVersionV1 is the legacy compatibility marker for jobs-only config.
	AgentFormatVersionV1 = "v1"
	// AgentFormatVersionV2 marks identity-enabled config.
	AgentFormatVersionV2 = "v2"
)

// AgentIdentity represents optional explicit identity metadata for an agent definition.
type AgentIdentity struct {
	AgentID      string            `yaml:"agent_id,omitempty" json:"agent_id,omitempty"`
	Name         string            `yaml:"name,omitempty" json:"name,omitempty"`
	Version      string            `yaml:"version,omitempty" json:"version,omitempty"`
	AgentType    string            `yaml:"agent_type,omitempty" json:"agent_type,omitempty"`
	Owner        string            `yaml:"owner,omitempty" json:"owner,omitempty"`
	TrustLevel   string            `yaml:"trust_level,omitempty" json:"trust_level,omitempty"`
	Capabilities []string          `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Constraints  *AgentConstraints `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	Reasoning    string            `yaml:"reasoning_mode,omitempty" json:"reasoning_mode,omitempty"`
	Memory       *AgentMemory      `yaml:"memory,omitempty" json:"memory,omitempty"`
	Goal         string            `yaml:"goal,omitempty" json:"goal,omitempty"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	AuditTags    []string          `yaml:"audit_tags,omitempty" json:"audit_tags,omitempty"`
	SelfModel    *AgentSelfModel   `yaml:"self_model,omitempty" json:"self_model,omitempty"`
	Contract     *AgentContract    `yaml:"agent_contract,omitempty" json:"agent_contract,omitempty"`
}

// AgentConstraints defines safety and execution constraints for an agent identity.
type AgentConstraints struct {
	MaxSteps       int                 `yaml:"max_steps,omitempty" json:"max_steps,omitempty"`
	TimeBudget     string              `yaml:"time_budget,omitempty" json:"time_budget,omitempty"`
	ResourceLimits *AgentResourceLimit `yaml:"resource_limits,omitempty" json:"resource_limits,omitempty"`
	EventRateLimit float64             `yaml:"event_rate_limit,omitempty" json:"event_rate_limit,omitempty"`
}

// AgentResourceLimit defines CPU/memory usage limits.
type AgentResourceLimit struct {
	CPUPercent float64 `yaml:"cpu_percent,omitempty" json:"cpu_percent,omitempty"`
	MemoryMB   float64 `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`
}

// AgentMemory defines memory retention scope for an agent.
type AgentMemory struct {
	Scope     string `yaml:"scope,omitempty" json:"scope,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// AgentSelfModel defines permissions related to self-modification.
type AgentSelfModel struct {
	CanModifyIdentity bool `yaml:"can_modify_identity,omitempty" json:"can_modify_identity,omitempty"`
	CanSpawnAgents    bool `yaml:"can_spawn_agents,omitempty" json:"can_spawn_agents,omitempty"`
	CanModifyJobs     bool `yaml:"can_modify_jobs,omitempty" json:"can_modify_jobs,omitempty"`
}

// AgentContract defines boundaries and assumptions for an agent.
type AgentContract struct {
	Guarantees       []string `yaml:"guarantees,omitempty" json:"guarantees,omitempty"`
	Assumptions      []string `yaml:"assumptions,omitempty" json:"assumptions,omitempty"`
	ForbiddenActions []string `yaml:"forbidden_actions,omitempty" json:"forbidden_actions,omitempty"`
	FailurePolicy    string   `yaml:"failure_policy,omitempty" json:"failure_policy,omitempty"`
}

// Job represents a job configuration
type Job struct {
	Name           string           `yaml:"name" json:"name"`
	Process        string           `yaml:"process" json:"process"`
	TriggerType    string           `yaml:"trigger_type" json:"trigger_type"`
	TriggerName    string           `yaml:"trigger_name" json:"trigger_name"`
	Steps          []map[string]any `yaml:"steps" json:"steps"`
	AgentsTimeout  int              `yaml:"timeout" json:"timeout"`
	PluginsTimeout int              `yaml:"plugins_timeout" json:"plugins_timeout"`
}

// NewJobConfig creates a new job configuration
func NewJobConfig() *JobConfig {
	return &JobConfig{
		registry: NewAgentRegistry(),
	}
}

// LoadJob loads a job into the JobConfig
func (jc *JobConfig) LoadJob(j Job) {
	if jc == nil {
		jc = NewJobConfig()
	}
	if jc.Jobs == nil {
		jc.Jobs = make([]Job, 0)
	}
	jc.Jobs = append(jc.Jobs, j)
}

func (jc *JobConfig) ensureRegistry() {
	if jc.registry == nil {
		jc.registry = NewAgentRegistry()
	}
}

// --- helpers: parsing and normalization ---

// parseAgentsBytes decodes YAML or JSON into a JobConfig
func parseAgentsBytes(data []byte, fileType string) (JobConfig, error) {
	var cfg JobConfig
	if err := ValidateAgentConfig(data, fileType, ValidationModeLenient, nil); err != nil {
		return cfg, err
	}
	var err error
	switch strings.ToLower(strings.TrimPrefix(fileType, ".")) {
	case "yaml", "yml":
		err = yaml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	case "json":
		err = json.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	default:
		err = fmt.Errorf("unsupported file format: %s", fileType)
	}
	if err != nil {
		return cfg, err
	}
	if err = cfg.normalizeAgentIdentity(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func normalizeAgentID(name string) string {
	v := strings.ToLower(strings.TrimSpace(name))
	if v == "" {
		return "anonymous-agent"
	}
	v = strings.ReplaceAll(v, " ", "-")
	v = strings.ReplaceAll(v, "/", "-")
	v = strings.ReplaceAll(v, "_", "-")
	return v
}

func (jc *JobConfig) normalizeAgentIdentity() error {
	if jc == nil {
		return nil
	}

	derivedName := ""
	if len(jc.Jobs) > 0 {
		derivedName = strings.TrimSpace(jc.Jobs[0].Name)
	}

	if jc.AgentIdentity == nil {
		if strings.TrimSpace(jc.FormatVersion) == "" {
			jc.FormatVersion = AgentFormatVersionV1
		}
		if derivedName == "" {
			return nil
		}
		jc.AgentIdentity = &AgentIdentity{
			Name:         derivedName,
			AgentID:      normalizeAgentID(derivedName),
			Version:      "0.0.0",
			AgentType:    "executor",
			TrustLevel:   "restricted",
			Capabilities: []string{"all"},
			Reasoning:    "fixed",
			Memory:       &AgentMemory{Scope: "persistent"},
			Contract:     &AgentContract{FailurePolicy: "emit_event"},
		}
		return nil
	}

	if strings.TrimSpace(jc.FormatVersion) == "" {
		jc.FormatVersion = AgentFormatVersionV2
	}

	if derivedName != "" {
		if strings.TrimSpace(jc.AgentIdentity.Name) == "" {
			jc.AgentIdentity.Name = derivedName
		} else if strings.TrimSpace(jc.AgentIdentity.Name) != derivedName {
			return fmt.Errorf("agent_identity.name (%s) must match jobs[0].name (%s)", jc.AgentIdentity.Name, derivedName)
		}
	}

	if strings.TrimSpace(jc.AgentIdentity.AgentID) == "" {
		jc.AgentIdentity.AgentID = normalizeAgentID(jc.AgentIdentity.Name)
	}
	if strings.TrimSpace(jc.AgentIdentity.Version) == "" {
		jc.AgentIdentity.Version = "0.0.0"
	}
	if strings.TrimSpace(jc.AgentIdentity.AgentType) == "" {
		jc.AgentIdentity.AgentType = "executor"
	}
	if strings.TrimSpace(jc.AgentIdentity.TrustLevel) == "" {
		jc.AgentIdentity.TrustLevel = "restricted"
	}
	if len(jc.AgentIdentity.Capabilities) == 0 {
		jc.AgentIdentity.Capabilities = []string{"all"}
	}
	if strings.TrimSpace(jc.AgentIdentity.Reasoning) == "" {
		jc.AgentIdentity.Reasoning = "fixed"
	}
	if jc.AgentIdentity.Memory == nil {
		jc.AgentIdentity.Memory = &AgentMemory{Scope: "persistent"}
	} else if strings.TrimSpace(jc.AgentIdentity.Memory.Scope) == "" {
		jc.AgentIdentity.Memory.Scope = "persistent"
	}
	if jc.AgentIdentity.Contract == nil {
		jc.AgentIdentity.Contract = &AgentContract{FailurePolicy: "emit_event"}
	} else if strings.TrimSpace(jc.AgentIdentity.Contract.FailurePolicy) == "" {
		jc.AgentIdentity.Contract.FailurePolicy = "emit_event"
	}

	return nil
}

// applyGlobalParams merges AgentsTimeout/PluginsTimeout and GlobalParameters into every step
func applyGlobalParams(cfg *JobConfig, agt cfg.AgentsConfig) {
	globalParams := agt.GlobalParameters
	if len(cfg.Jobs) == 0 || (len(globalParams) == 0 && agt.AgentsTimeout == 0 && agt.PluginsTimeout == 0) {
		return
	}
	for i := range cfg.Jobs {
		// Defaults for timeouts
		if cfg.Jobs[i].AgentsTimeout == 0 {
			cfg.Jobs[i].AgentsTimeout = agt.AgentsTimeout
		}
		if cfg.Jobs[i].PluginsTimeout == 0 {
			cfg.Jobs[i].PluginsTimeout = agt.PluginsTimeout
		}
		// Ensure step params/config map exists, then merge global params
		for j := range cfg.Jobs[i].Steps {
			// Ensure "params" map exists
			if _, ok := cfg.Jobs[i].Steps[j]["params"]; !ok {
				cfg.Jobs[i].Steps[j]["params"] = make(map[interface{}]interface{})
			}
			// Ensure "config" inside params exists
			if _, ok := cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig]; !ok {
				cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig] = make(map[interface{}]interface{})
			}
			// Merge global parameters into params
			if paramMap, ok := cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{}); ok {
				for k, v := range globalParams {
					paramMap[k] = v
				}
			} else {
				cmn.DebugMsg(cmn.DbgLvlError, "params field is not map[interface{}]interface{} but %T", cfg.Jobs[i].Steps[j]["params"])
			}
		}
	}
}

// --- LOCAL loader (extracted from your current code) ---

func loadAgentsFromLocal(paths []string) ([]JobConfig, error) {
	if len(paths) == 0 {
		paths = []string{"./agents/*.yaml"}
	}

	var out []JobConfig
	for _, path := range paths {
		// Expand wildcards
		files, err := filepath.Glob(path)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error finding agent files: %v", err)
			return nil, err
		}
		if len(files) == 0 {
			continue
		}

		for _, filePath := range files {
			ext := cmn.GetFileExt(filePath) // no dot, e.g., "yaml"
			if ext != "" && ext != "yaml" && ext != "yml" && ext != "json" {
				continue
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "Loading agents definition file: %s", filePath)

			f, err := os.Open(filePath) //nolint:gosec
			if err != nil {
				return nil, fmt.Errorf("failed to open config file: %w", err)
			}
			data, rerr := io.ReadAll(f)
			_ = f.Close()
			if rerr != nil {
				return nil, fmt.Errorf("failed to read config file: %w", rerr)
			}

			// Interpolate env vars
			interpolated := cmn.InterpolateEnvVars(string(data))
			cfg, err := parseAgentsBytes([]byte(interpolated), filepath.Ext(filePath))
			if err != nil {
				return nil, fmt.Errorf("failed to parse config file %s: %w", filePath, err)
			}
			out = append(out, cfg)
		}
	}
	return out, nil
}

// --- REMOTE loader (mirrors your plugins/rules remote approach) ---

func loadAgentsFromRemote(agt cfg.AgentsConfig) ([]JobConfig, error) {
	if agt.Path == nil {
		return nil, fmt.Errorf("agents path is empty")
	}

	var out []JobConfig
	for _, path := range agt.Path {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if ext != "yaml" && ext != "yml" && ext != "json" {
			// ignore unsupported types
			continue
		}

		// protocol selection (http/https, ftp/ftps, s3); mirrors your existing code
		var proto string
		switch strings.ToLower(strings.TrimSpace(agt.Type)) {
		case "http":
			proto = "http"
		case "ftp":
			proto = "ftp"
		default:
			proto = "s3"
		}
		if agt.SSLMode == cmn.EnableStr && proto == "http" {
			proto = "https"
		}
		if agt.SSLMode == cmn.EnableStr && proto == "ftp" {
			proto = "ftps"
		}

		// URL compose
		var url string
		if agt.Port != "" && agt.Port != "80" && agt.Port != "443" {
			url = fmt.Sprintf("%s://%s:%s/%s", proto, agt.Host, agt.Port, path)
		} else {
			url = fmt.Sprintf("%s://%s/%s", proto, agt.Host, path)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Downloading agents definition from %s", url)

		// Download
		body, err := cmn.FetchRemoteFile(url, agt.Timeout, agt.SSLMode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch agents from %s: %w", url, err)
		}

		// Env interpolation, then parse
		interpolated := cmn.InterpolateEnvVars(body)
		cfg, err := parseAgentsBytes([]byte(interpolated), filepath.Ext(path))
		if err != nil {
			return nil, fmt.Errorf("failed to parse remote agents from %s: %w", url, err)
		}
		out = append(out, cfg)
	}
	return out, nil
}

// --- Public method with remote support ---

// LoadConfig loads YAML/JSON Agent Definitions from local paths or remote hosts (http/ftp/s3)
// and applies global parameters/timeouts from each AgentsConfig entry.
func (jc *JobConfig) LoadConfig(agtConfigs []cfg.AgentsConfig) error {
	jc.ensureRegistry()
	for _, agt := range agtConfigs {
		var chunks []JobConfig
		var err error

		if strings.TrimSpace(agt.Host) == "" {
			// LOCAL
			paths := agt.Path
			if len(paths) == 0 {
				paths = []string{"./agents/*.yaml"}
			}
			chunks, err = loadAgentsFromLocal(paths)
			if err != nil {
				return err
			}
		} else {
			// REMOTE
			chunks, err = loadAgentsFromRemote(agt)
			if err != nil {
				return err
			}
		}

		// Normalize and merge global params for each loaded chunk, then append
		for idx := range chunks {
			applyGlobalParams(&chunks[idx], agt)
			def, err := chunks[idx].NormalizeToAgentDefinition(AgentSourceMetadata{
				Location: strings.Join(agt.Path, ","),
				Format:   strings.ToLower(strings.TrimSpace(agt.Type)),
			})
			if err != nil {
				return err
			}
			if err := jc.registry.Register(def); err != nil {
				return err
			}
			jc.Jobs = append(jc.Jobs, def.Jobs...)
		}
	}
	return nil
}

// RegisterAgent registers an agent with the JobConfig
func (jc *JobConfig) RegisterAgent(agent *JobConfig) {
	jc.ensureRegistry()
	if agent == nil {
		return
	}
	def, err := agent.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "runtime"})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to normalize agent registration: %v", err)
		return
	}
	if err := jc.registry.Register(def); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to register agent: %v", err)
		return
	}
	jc.Jobs = append(jc.Jobs, def.Jobs...)
}

// GetAgentByName returns an agent by name
func (jc *JobConfig) GetAgentByName(name string) (*JobConfig, bool) {
	if jc == nil {
		return nil, false
	}
	jc.ensureRegistry()

	if a, ok := jc.registry.GetByName(name); ok {
		return a.toJobConfig(), true
	}

	// Backward-compatible fallback: lookup by legacy job-group name.
	for i := 0; i < len(jc.Jobs); i++ {
		if strings.TrimSpace(jc.Jobs[i].Name) == name {
			// Return the Job with the specified name inside a JobConfig struct
			rjc := NewJobConfig()
			rjc.Jobs = append(rjc.Jobs, jc.Jobs[i])
			return rjc, true
		}
	}
	return nil, false
}

// GetAgentsByEventType returns all agents that are triggered by a specific event type
func (jc *JobConfig) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	return jc.GetAgentsByTrigger("event", eventType)
}

// GetAgentsByTrigger returns all agents matching a trigger selector.
func (jc *JobConfig) GetAgentsByTrigger(triggerType, triggerName string) ([]*JobConfig, bool) {
	if jc == nil {
		return nil, false
	}
	jc.ensureRegistry()

	var agents []*JobConfig
	defs := jc.registry.GetByTrigger(triggerType, triggerName)
	for _, def := range defs {
		cfg := NewJobConfig()
		cfg.FormatVersion = def.FormatVersion
		id := def.Identity
		cfg.AgentIdentity = &id
		for _, job := range def.Jobs {
			if strings.ToLower(strings.TrimSpace(job.TriggerType)) == strings.ToLower(strings.TrimSpace(triggerType)) &&
				strings.TrimSpace(job.TriggerName) == strings.TrimSpace(triggerName) {
				cfg.Jobs = append(cfg.Jobs, job)
			}
		}
		if len(cfg.Jobs) > 0 {
			agents = append(agents, cfg)
		}
	}

	if len(agents) > 0 {
		return agents, true
	}

	// Backward-compatible fallback in case legacy callers only populated Jobs directly.
	for i := 0; i < len(jc.Jobs); i++ {
		if strings.ToLower(strings.TrimSpace(jc.Jobs[i].TriggerType)) == strings.ToLower(strings.TrimSpace(triggerType)) &&
			strings.TrimSpace(jc.Jobs[i].TriggerName) == strings.TrimSpace(triggerName) {
			rjc := NewJobConfig()
			rjc.Jobs = append(rjc.Jobs, jc.Jobs[i])
			agents = append(agents, rjc)
		}
	}

	return agents, len(agents) > 0
}

// GetAgentByName returns an agent by name from the JobEngine
func (je *JobEngine) GetAgentByName(name string) (*JobConfig, bool) {
	return AgentsRegistry.GetAgentByName(name)
}

// GetAgentsByEventType returns all agents that are triggered by a specific event type from the JobEngine
func (je *JobEngine) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	return AgentsRegistry.GetAgentsByEventType(eventType)
}

// GetAgentsByTrigger returns all agents matching the provided trigger selector.
func (je *JobEngine) GetAgentsByTrigger(triggerType, triggerName string) ([]*JobConfig, bool) {
	return AgentsRegistry.GetAgentsByTrigger(triggerType, triggerName)
}

// ExecuteAgent executes a registered agent by agent ID or, if missing, by agent name.
func (je *JobEngine) ExecuteAgent(agentRef string, inputCtx map[string]any) error {
	lookup := strings.TrimSpace(agentRef)
	if lookup == "" {
		return fmt.Errorf("missing agent reference")
	}
	if AgentsRegistry != nil {
		AgentsRegistry.ensureRegistry()
		if def, ok := AgentsRegistry.registry.GetByID(lookup); ok {
			return je.executeAgentDefinition(def, inputCtx)
		}
		if def, ok := AgentsRegistry.registry.GetByName(lookup); ok {
			return je.executeAgentDefinition(def, inputCtx)
		}
	}

	if agent, ok := je.GetAgentByName(lookup); ok {
		return je.ExecuteJobs(agent, inputCtx)
	}
	return fmt.Errorf("agent '%s' not found", lookup)
}

func deepCopyJob(j Job) Job {
	out := j // copy scalars

	out.Steps = make([]map[string]any, len(j.Steps))
	for i, step := range j.Steps {
		m := make(map[string]any, len(step))
		for k, v := range step {
			m[k] = v
		}
		out.Steps[i] = m
	}

	return out
}

// ExecuteJobs executes all jobs in the configuration
func (je *JobEngine) ExecuteJobs(j *JobConfig, iCfg map[string]any) error {
	if j != nil && j.AgentIdentity != nil {
		if def, err := j.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "runtime"}); err == nil {
			return je.executeAgentDefinition(def, iCfg)
		}
	}
	return je.executeJobsWithContext(j, iCfg, AgentExecutionContext{}, cfg.AgentRuntimeConfig{})
}

func (je *JobEngine) executeAgentDefinition(def *AgentDefinition, iCfg map[string]any) error {
	if def == nil {
		return fmt.Errorf("nil agent definition")
	}
	flags := runtimeFlagsFromConfig(iCfg)
	ctx := newAgentExecutionContext(def.Identity, def.Source.Location)
	return je.executeJobsWithContext(def.toJobConfig(), iCfg, ctx, flags)
}

func (je *JobEngine) executeJobsWithContext(j *JobConfig, iCfg map[string]any, execCtx AgentExecutionContext, flags cfg.AgentRuntimeConfig) error {
	// Create a waiting group for parallel group processing
	var wg sync.WaitGroup

	// Create a deep copy of j.Jobs to avoid modifying the original configuration
	localJobs := make([]Job, len(j.Jobs))
	for i, job := range j.Jobs {
		localJobs[i] = deepCopyJob(job)
	}

	// Iterate over job groups
	for _, jobGroup := range localJobs {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Executing Job Group: %s", jobGroup.Name)

		// Add iCfg to the first step as StrConfig field
		// this is the "base" configuration that will be passed to all steps
		// and contains things like *wd and *dbHandler
		if len(jobGroup.Steps) > 0 {
			params, ok := jobGroup.Steps[0]["params"]
			if !ok {
				jobGroup.Steps[0]["params"] = make(map[string]interface{})
			} else {
				jobGroup.Steps[0]["params"] = cmn.ConvertMapIIToSI(params)
			}

			paramsMap := jobGroup.Steps[0]["params"].(map[string]interface{})

			// Ensure "StrConfig" exists within "params" and is a map[string]interface{}
			if _, ok := paramsMap[StrConfig]; !ok || paramsMap[StrConfig] == nil {
				paramsMap[StrConfig] = make(map[string]interface{})
			}

			configMap := paramsMap[StrConfig].(map[string]interface{})

			// Merge iCfg into configMap
			for k, v := range iCfg {
				k = strings.ToLower(strings.TrimSpace(k))
				if k == "metadata" {
					k = "meta_data"
				}
				configMap[k] = v
				if k == "meta_data" {
					cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Agents] Merged meta_data into job config: %v", v)
				}
			}
		}

		// log the configuration for debugging purposes
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group Configuration: %v", jobGroup)

		// Check if the group should run in parallel
		if strings.ToLower(strings.TrimSpace(jobGroup.Process)) == "parallel" {
			// Increment the wait group counter
			wg.Add(1)

			// Execute the group in parallel
			go func(jg []map[string]any, identity *AgentIdentity) {
				defer wg.Done()
				if err := executeJobGroup(je, jg, identity, execCtx, flags); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Agents] Failed to execute job group '%s': %v", jobGroup.Name, err)
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group '%s' completed successfully", jobGroup.Name)
			}(jobGroup.Steps, j.AgentIdentity)

		} else {
			// Execute the group serially
			if err := executeJobGroup(je, jobGroup.Steps, j.AgentIdentity, execCtx, flags); err != nil {
				return fmt.Errorf("failed to execute job group '%s': %v", jobGroup.Name, err)
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group '%s' completed successfully", jobGroup.Name)
		}
	}

	// Wait for all parallel groups to finish
	wg.Wait()
	return nil
}

// executeJobGroup runs jobs in a group serially
func executeJobGroup(je *JobEngine, steps []map[string]any, identity *AgentIdentity, execCtx AgentExecutionContext, flags cfg.AgentRuntimeConfig) error {
	lastResult := make(map[string]any)
	var budget *constraintBudgetManager
	if flags.IdentityEnforcement && identity != nil {
		b, err := newConstraintBudgetManager(*identity)
		if err != nil {
			return err
		}
		budget = b
	}

	// Execute each job in the group
	for i := 0; i < len(steps); i++ {
		step := &steps[i]

		// Get the action name
		actionName, ok := (*step)["action"].(string)
		if !ok {
			return fmt.Errorf("missing 'action' field in job step")
		}
		params, _ := (*step)["params"].(map[string]interface{})
		if params == nil {
			params = map[string]interface{}{}
			(*step)["params"] = params
		}
		if flags.IdentityEnforcement && identity != nil {
			applyExecutionContext(params, execCtx)
			if !capabilityAllowed(*identity, actionName) {
				return fmt.Errorf("capability gate denied action %s: capability %q missing", actionName, requiredCapabilityForAction(actionName))
			}
			if !trustAllowed(*identity, actionName) {
				return fmt.Errorf("trust gate denied action %s: trust_level %q insufficient", actionName, identity.TrustLevel)
			}
			if err := budget.preStepCheck(actionName); err != nil {
				return err
			}
		}

		// If we are to a step that is not the first one, we need to transform StrResponse (from previous step) to StrRequest
		if i > 0 {
			if _, ok := params[StrRequest]; !ok {
				params[StrRequest] = lastResult[StrResponse]
			} else {
				// If yes, merge the two maps
				for k, v := range lastResult[StrResponse].(map[string]interface{}) {
					params[StrRequest].(map[string]interface{})[k] = v
				}
			}
		}

		// Inject previous result into current params (if needed)
		for k, v := range lastResult {
			// Skip key response, we have already converted it to input
			if k == StrResponse {
				continue
			}

			// Check if k == config, if so, merge the two maps
			if k == StrConfig {
				// Check if the params field has a config field
				if _, ok := params[StrConfig]; !ok {
					// If not, add the config field
					params[StrConfig] = v
					continue
				}
				// If yes, merge the two maps
				for k, v := range v.(map[string]interface{}) {
					params[StrConfig].(map[string]interface{})[k] = v
				}
				continue
			}

			// Check if the params field has a k field
			if _, ok := params[k]; !ok {
				// If not, add the k field
				params[k] = v
			} else {
				// If yes, merge the two maps
				for k, v := range v.(map[string]any) {
					params[k] = v
				}
			}
		}

		action, exists := je.actions[actionName]
		if !exists {
			return fmt.Errorf("unknown action: %s", actionName)
		}

		result, err := action.Execute(params)
		if err != nil {
			if retryConfig, hasRetry := (*step)["retry"].(RetryConfig); hasRetry {
				result, err = executeWithRetry(action, params, retryConfig)
			}
			if err != nil {
				if fallback, hasFallback := (*step)["fallback"].([]map[string]interface{}); hasFallback {
					cmn.DebugMsg(cmn.DbgLvlError, "Action %s failed, executing fallback steps", actionName)
					return executeJobGroup(je, fallback, identity, execCtx, flags)
				}
				return fmt.Errorf("action %s failed: %v", actionName, err)
			}
		}

		// Update the result for the next job in the group
		lastResult = result
		if flags.IdentityEnforcement && identity != nil {
			budget.markActionExecuted(actionName)
		}
	}
	return nil
}

// executeWithRetry executes an action with retry logic
func executeWithRetry(action Action, params map[string]any, retryConfig RetryConfig) (map[string]interface{}, error) {
	var lastError error
	var result map[string]any

	for attempt := 1; attempt <= retryConfig.MaxRetries; attempt++ {
		result, lastError = action.Execute(params)
		if lastError == nil {
			return result, nil
		}

		delay := retryConfig.BaseDelay * time.Duration(math.Pow(retryConfig.Backoff, float64(attempt-1)))
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("action failed after %d retries: %w", retryConfig.MaxRetries, lastError)
}

/*
Example of a job configuration file in YAML format:

jobs:
  - name: "Serial Group 1"
    process: "serial"
	trigger_type: event
	trigger_name: "event_name"
    steps:
      - action: "APIRequest"
        params:
          config:
            url: "http://example.com/api/data"
      - action: "AIInteraction"
        params:
          prompt: "Summarize the following data: $response"
          config:
            url: "https://api.openai.com/v1/completions"
            api_key: "your_api_key"

  - name: "Parallel Group 1"
    process: "parallel"
	trigger_type: event
	trigger_name: "event_name"
    steps:
      - action: "DBQuery"
        params:
          type: "insert"
          query: "INSERT INTO logs (message) VALUES ('Parallel job 1')"
      - action: "RunCommand"
        params:
          command: "echo 'Parallel job 2'"

  - name: "Serial Group 2"
    process: "serial"
	trigger_type: agent
	trigger_name: "agent_name"
    steps:
      - action: "PluginExecution"
        params:
          plugin: ["example_plugin"]




*/
