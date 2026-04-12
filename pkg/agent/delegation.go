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

const (
	delegationCapability = "delegate"
)

type delegationTarget struct {
	AgentID   string
	AgentName string
}

type delegationGraph struct {
	Path  []string `json:"path"`
	Edges []string `json:"edges"`
}

func resolveDelegationTarget(step map[string]interface{}, input map[string]interface{}) (delegationTarget, error) {
	if step == nil {
		return delegationTarget{}, fmt.Errorf("missing decision branch")
	}
	target := delegationTarget{}
	if agentID, _ := step["agent_id"].(string); strings.TrimSpace(agentID) != "" {
		target.AgentID = strings.TrimSpace(resolveResponseString(input, agentID))
	}
	if agentName, _ := step["agent_name"].(string); strings.TrimSpace(agentName) != "" {
		target.AgentName = strings.TrimSpace(resolveResponseString(input, agentName))
	}
	if target.AgentID == "" {
		if callAgent, _ := step["call_agent"].(string); strings.TrimSpace(callAgent) != "" {
			target.AgentName = strings.TrimSpace(resolveResponseString(input, callAgent))
		}
	}
	if target.AgentID == "" && target.AgentName == "" {
		return delegationTarget{}, fmt.Errorf("missing 'call_agent', 'agent_id', or 'agent_name' in next step")
	}
	return target, nil
}

func (je *JobEngine) resolveAgentDefinitionByTarget(target delegationTarget) (*AgentDefinition, string, error) {
	if AgentsRegistry == nil {
		missing := target.AgentID
		if missing == "" {
			missing = target.AgentName
		}
		return nil, "", fmt.Errorf("agent '%s' not found", missing)
	}
	AgentsRegistry.ensureRegistry()

	if target.AgentID != "" {
		if def, ok := AgentsRegistry.registry.GetByID(target.AgentID); ok {
			return def, "id", nil
		}
		if target.AgentName != "" {
			return nil, "", fmt.Errorf("delegation target unavailable: agent_id '%s' not found", target.AgentID)
		}
	}
	if target.AgentName != "" {
		if def, ok := AgentsRegistry.registry.GetByName(target.AgentName); ok {
			return def, "name", nil
		}
		return nil, "", fmt.Errorf("delegation target unavailable: agent_name '%s' not found", target.AgentName)
	}
	return nil, "", fmt.Errorf("delegation target unavailable")
}

func getAgentRuntimeMap(params map[string]interface{}) map[string]interface{} {
	if params == nil {
		return nil
	}
	configMap, _ := params[StrConfig].(map[string]interface{})
	if configMap == nil {
		return nil
	}
	runtimeMap, _ := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	if runtimeMap == nil {
		return nil
	}
	return runtimeMap
}

func parseRuntimeIdentity(params map[string]interface{}) (AgentIdentity, bool) {
	runtimeMap := getAgentRuntimeMap(params)
	if runtimeMap == nil {
		return AgentIdentity{}, false
	}
	rawIdentity, ok := runtimeMap["identity_snapshot"]
	if !ok {
		return AgentIdentity{}, false
	}
	switch v := rawIdentity.(type) {
	case AgentIdentity:
		return v, true
	case *AgentIdentity:
		if v != nil {
			return *v, true
		}
	case map[string]interface{}:
		id := AgentIdentity{}
		if s, ok := v["agent_id"].(string); ok {
			id.AgentID = s
		}
		if s, ok := v["name"].(string); ok {
			id.Name = s
		}
		if s, ok := v["trust_level"].(string); ok {
			id.TrustLevel = s
		}
		if s, ok := v["owner"].(string); ok {
			id.Owner = s
		}
		if rawCaps, ok := v["capabilities"].([]interface{}); ok {
			caps := make([]string, 0, len(rawCaps))
			for _, rawCap := range rawCaps {
				if capStr, ok := rawCap.(string); ok {
					caps = append(caps, capStr)
				}
			}
			id.Capabilities = caps
		}
		if rawContract, ok := v["agent_contract"].(map[string]interface{}); ok {
			contract := &AgentContract{}
			if rawForbidden, ok := rawContract["forbidden_actions"].([]interface{}); ok {
				forbidden := make([]string, 0, len(rawForbidden))
				for _, rawAction := range rawForbidden {
					if action, ok := rawAction.(string); ok {
						forbidden = append(forbidden, action)
					}
				}
				contract.ForbiddenActions = forbidden
			}
			id.Contract = contract
		}
		return id, true
	}
	return AgentIdentity{}, false
}

func hasCapability(identity AgentIdentity, required string) bool {
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

func contractForbidsDelegation(contract *AgentContract) bool {
	if contract == nil {
		return false
	}
	for _, action := range contract.ForbiddenActions {
		normalized := strings.ToLower(strings.TrimSpace(action))
		if normalized == "delegate" || normalized == "delegation" || normalized == "decision" {
			return true
		}
	}
	return false
}

func delegationPolicyCheck(caller AgentIdentity, callee AgentIdentity) error {
	if !hasCapability(caller, delegationCapability) {
		return fmt.Errorf("delegation denied: caller capability missing '%s'", delegationCapability)
	}
	if trustLevelRank(caller.TrustLevel) < trustLevelRank(callee.TrustLevel) {
		return fmt.Errorf("delegation denied: caller trust_level %q is lower than callee trust_level %q", caller.TrustLevel, callee.TrustLevel)
	}
	if contractForbidsDelegation(caller.Contract) {
		return fmt.Errorf("delegation denied: caller contract forbids delegation")
	}
	if contractForbidsDelegation(callee.Contract) {
		return fmt.Errorf("delegation denied: callee contract forbids delegation")
	}
	return nil
}

func getDelegationGraph(params map[string]interface{}) delegationGraph {
	runtimeMap := getAgentRuntimeMap(params)
	if runtimeMap == nil {
		return delegationGraph{}
	}
	raw, ok := runtimeMap["delegation_graph"].(map[string]interface{})
	if !ok {
		return delegationGraph{}
	}
	out := delegationGraph{}
	if rawPath, ok := raw["path"].([]interface{}); ok {
		for _, p := range rawPath {
			if s, ok := p.(string); ok {
				out.Path = append(out.Path, s)
			}
		}
	}
	if rawEdges, ok := raw["edges"].([]interface{}); ok {
		for _, e := range rawEdges {
			if s, ok := e.(string); ok {
				out.Edges = append(out.Edges, s)
			}
		}
	}
	return out
}

func setDelegationGraph(params map[string]interface{}, graph delegationGraph) {
	if params == nil {
		return
	}
	configMap, _ := params[StrConfig].(map[string]interface{})
	if configMap == nil {
		configMap = map[string]interface{}{}
		params[StrConfig] = configMap
	}
	runtimeMap, _ := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	if runtimeMap == nil {
		runtimeMap = map[string]interface{}{}
		configMap[cfgKeyAgentRuntime] = runtimeMap
	}
	path := make([]interface{}, 0, len(graph.Path))
	for _, p := range graph.Path {
		path = append(path, p)
	}
	edges := make([]interface{}, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edges = append(edges, e)
	}
	runtimeMap["delegation_graph"] = map[string]interface{}{
		"path":  path,
		"edges": edges,
	}
}

func delegationNodeKey(identity AgentIdentity) string {
	if strings.TrimSpace(identity.AgentID) != "" {
		return strings.TrimSpace(identity.AgentID)
	}
	if strings.TrimSpace(identity.Name) != "" {
		return strings.TrimSpace(identity.Name)
	}
	return "anonymous-agent"
}

func detectDelegationCycle(graph delegationGraph, nextNode string) error {
	for _, node := range graph.Path {
		if strings.TrimSpace(node) == strings.TrimSpace(nextNode) {
			return fmt.Errorf("delegation cycle detected at agent %q", nextNode)
		}
	}
	return nil
}
