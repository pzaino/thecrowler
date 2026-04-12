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
	"fmt"
	"strings"
)

// AgentSourceMetadata tracks where an agent definition came from.
type AgentSourceMetadata struct {
	Location string `yaml:"location,omitempty" json:"location,omitempty"`
	Format   string `yaml:"format,omitempty" json:"format,omitempty"`
}

// AgentDefinition is the runtime domain model for a first-class agent.
type AgentDefinition struct {
	FormatVersion string              `yaml:"format_version,omitempty" json:"format_version,omitempty"`
	Identity      AgentIdentity       `yaml:"agent_identity" json:"agent_identity"`
	Jobs          []Job               `yaml:"jobs" json:"jobs"`
	Source        AgentSourceMetadata `yaml:"source,omitempty" json:"source,omitempty"`
}

// NormalizeToAgentDefinition converts an input JobConfig into a runtime AgentDefinition.
func (jc *JobConfig) NormalizeToAgentDefinition(source AgentSourceMetadata) (*AgentDefinition, error) {
	if jc == nil {
		return nil, fmt.Errorf("nil job config")
	}
	if err := jc.normalizeAgentIdentity(); err != nil {
		return nil, err
	}
	if jc.AgentIdentity == nil {
		return nil, fmt.Errorf("missing agent identity")
	}

	out := &AgentDefinition{
		FormatVersion: jc.FormatVersion,
		Identity:      *jc.AgentIdentity,
		Jobs:          make([]Job, len(jc.Jobs)),
		Source:        source,
	}
	copy(out.Jobs, jc.Jobs)
	return out, nil
}

func (ad *AgentDefinition) toJobConfig() *JobConfig {
	if ad == nil {
		return nil
	}
	out := NewJobConfig()
	id := ad.Identity
	out.FormatVersion = ad.FormatVersion
	out.AgentIdentity = &id
	out.Jobs = make([]Job, len(ad.Jobs))
	copy(out.Jobs, ad.Jobs)
	return out
}

// AgentRegistry is the runtime index for registered agents.
type AgentRegistry struct {
	ordered      []*AgentDefinition
	byID         map[string]*AgentDefinition
	byName       map[string]*AgentDefinition
	byEvent      map[string][]*AgentDefinition
	byManual     map[string][]*AgentDefinition
	byAnyTrigger map[string][]*AgentDefinition
}

// NewAgentRegistry creates a registry with all indexes initialized.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		ordered:      []*AgentDefinition{},
		byID:         map[string]*AgentDefinition{},
		byName:       map[string]*AgentDefinition{},
		byEvent:      map[string][]*AgentDefinition{},
		byManual:     map[string][]*AgentDefinition{},
		byAnyTrigger: map[string][]*AgentDefinition{},
	}
}

func triggerKey(triggerType, triggerName string) string {
	return strings.ToLower(strings.TrimSpace(triggerType)) + ":" + strings.TrimSpace(triggerName)
}

// Register stores an agent and updates all indexes.
// Conflict strategy:
//   - duplicate agent_id: reject with deterministic error
//   - duplicate name: reject with deterministic error
//   - duplicate trigger selectors: allowed; all matching agents remain indexed
func (ar *AgentRegistry) Register(agent *AgentDefinition) error {
	if ar == nil {
		return fmt.Errorf("nil registry")
	}
	if agent == nil {
		return fmt.Errorf("nil agent definition")
	}

	agentID := strings.TrimSpace(agent.Identity.AgentID)
	agentName := strings.TrimSpace(agent.Identity.Name)
	if agentID == "" {
		return fmt.Errorf("missing agent_id")
	}
	if agentName == "" {
		return fmt.Errorf("missing agent name")
	}
	if _, exists := ar.byID[agentID]; exists {
		return fmt.Errorf("duplicate agent_id: %s", agentID)
	}
	if _, exists := ar.byName[agentName]; exists {
		return fmt.Errorf("duplicate agent name: %s", agentName)
	}

	ar.byID[agentID] = agent
	ar.byName[agentName] = agent
	ar.ordered = append(ar.ordered, agent)

	seenInAgent := map[string]struct{}{}
	for _, job := range agent.Jobs {
		name := strings.TrimSpace(job.TriggerName)
		if name == "" {
			continue
		}
		tType := strings.ToLower(strings.TrimSpace(job.TriggerType))
		key := triggerKey(tType, name)
		if _, seen := seenInAgent[key]; seen {
			continue
		}
		seenInAgent[key] = struct{}{}
		ar.byAnyTrigger[key] = append(ar.byAnyTrigger[key], agent)
		switch tType {
		case "event":
			ar.byEvent[name] = append(ar.byEvent[name], agent)
		case "manual":
			ar.byManual[name] = append(ar.byManual[name], agent)
		}
	}
	return nil
}

func (ar *AgentRegistry) GetByID(id string) (*AgentDefinition, bool) {
	if ar == nil {
		return nil, false
	}
	a, ok := ar.byID[strings.TrimSpace(id)]
	return a, ok
}

func (ar *AgentRegistry) GetByName(name string) (*AgentDefinition, bool) {
	if ar == nil {
		return nil, false
	}
	a, ok := ar.byName[strings.TrimSpace(name)]
	return a, ok
}

func (ar *AgentRegistry) GetByTrigger(triggerType, triggerName string) []*AgentDefinition {
	if ar == nil {
		return nil
	}
	key := triggerKey(triggerType, triggerName)
	agents := ar.byAnyTrigger[key]
	out := make([]*AgentDefinition, len(agents))
	copy(out, agents)
	return out
}
