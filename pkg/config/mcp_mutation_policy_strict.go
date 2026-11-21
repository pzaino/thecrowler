package config

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	yamlv3 "gopkg.in/yaml.v3"
)

// applyMCPMutationPolicyStrict extracts and validates the authorization-bearing
// mcp.mutation_policy subtree from an operator configuration document. The
// legacy whole-document parser remains permissive for unrelated sections; only
// this subtree and its immediate ancestors are strict.
func applyMCPMutationPolicyStrict(config *Config, data []byte) error {
	policy, present, err := decodeMCPMutationPolicyStrict(data)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if err := policy.ValidateInventory(config.MCP.Servers); err != nil {
		return fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	config.MCP.MutationPolicy = policy
	return nil
}

func decodeMCPMutationPolicyStrict(data []byte) (MCPMutationPolicyConfig, bool, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return MCPMutationPolicyConfig{}, false, nil
	}
	if !utf8.Valid(data) {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: configuration is not valid UTF-8")
	}
	var document yamlv3.Node
	if err := yamlv3.Unmarshal(data, &document); err != nil {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: parse configuration: %w", err)
	}
	root := &document
	if root.Kind == yamlv3.DocumentNode {
		if len(root.Content) == 0 {
			return MCPMutationPolicyConfig{}, false, nil
		}
		root = root.Content[0]
	}
	if root.Kind != yamlv3.MappingNode {
		return MCPMutationPolicyConfig{}, false, nil
	}
	mcpNode, found, err := mappingValueStrict(root, "mcp", "mcp")
	if err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	if !found {
		return MCPMutationPolicyConfig{}, false, nil
	}
	if err := validateMCPAncestorNode(mcpNode, "mcp"); err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	if mcpNode.Kind != yamlv3.MappingNode {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: mcp must be an object")
	}
	if err := validateMCPTopLevelKeys(mcpNode); err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	policyNode, found, err := mappingValueStrict(mcpNode, "mutation_policy", "mcp.mutation_policy")
	if err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	if !found {
		return MCPMutationPolicyConfig{}, false, nil
	}
	if err := validateMCPAncestorNode(policyNode, "mcp.mutation_policy"); err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	if policyNode.Kind != yamlv3.MappingNode {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: mutation_policy must be an object")
	}
	if err := validateMCPPolicyNode(policyNode, "mcp.mutation_policy"); err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	if err := validateMCPPolicyTypes(policyNode); err != nil {
		return MCPMutationPolicyConfig{}, false, err
	}
	encoded, err := yamlv3.Marshal(policyNode)
	if err != nil {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: normalize subtree: %w", err)
	}
	var policy MCPMutationPolicyConfig
	decoder := yamlv3.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return MCPMutationPolicyConfig{}, false, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	return policy, true, nil
}

func mappingValueStrict(node *yamlv3.Node, key, path string) (*yamlv3.Node, bool, error) {
	if node == nil || node.Kind != yamlv3.MappingNode {
		return nil, false, fmt.Errorf("%s: parent must be an object", path)
	}
	var value *yamlv3.Node
	count := 0
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		if keyNode.Kind == yamlv3.AliasNode || keyNode.Anchor != "" {
			return nil, false, fmt.Errorf("%s: aliases and anchors are not permitted", path)
		}
		if keyNode.Value == "<<" {
			return nil, false, fmt.Errorf("%s: YAML merge keys are not permitted", path)
		}
		if keyNode.Value == key {
			count++
			value = node.Content[index+1]
		}
	}
	if count > 1 {
		return nil, false, fmt.Errorf("%s: duplicate key %q", path, key)
	}
	return value, count == 1, nil
}

func validateMCPAncestorNode(node *yamlv3.Node, path string) error {
	if node == nil {
		return fmt.Errorf("%s: missing object", path)
	}
	if node.Kind == yamlv3.AliasNode {
		return fmt.Errorf("%s: aliases are not permitted", path)
	}
	if node.Anchor != "" {
		return fmt.Errorf("%s: anchors are not permitted", path)
	}
	if node.Kind == yamlv3.ScalarNode && node.Tag == "!!null" {
		return fmt.Errorf("%s: null is not permitted", path)
	}
	return nil
}

func validateMCPTopLevelKeys(node *yamlv3.Node) error {
	if node == nil || node.Kind != yamlv3.MappingNode {
		return fmt.Errorf("mcp.mutation_policy: mcp must be an object")
	}
	allowed := map[string]bool{"servers": true, "mutation_policy": true}
	seen := map[string]bool{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		if keyNode.Kind != yamlv3.ScalarNode || keyNode.Tag != "!!str" {
			return fmt.Errorf("mcp.mutation_policy: mcp keys must be strings")
		}
		if keyNode.Value == "<<" {
			return fmt.Errorf("mcp.mutation_policy: YAML merge keys are not permitted")
		}
		if !allowed[keyNode.Value] {
			return fmt.Errorf("mcp.mutation_policy: unknown mcp key %q", keyNode.Value)
		}
		if seen[keyNode.Value] {
			return fmt.Errorf("mcp.mutation_policy: duplicate mcp key %q", keyNode.Value)
		}
		seen[keyNode.Value] = true
	}
	return nil
}

func validateMCPPolicyNode(node *yamlv3.Node, path string) error {
	if node == nil {
		return fmt.Errorf("%s: missing value", path)
	}
	if node.Kind == yamlv3.AliasNode {
		return fmt.Errorf("%s: aliases are not permitted", path)
	}
	if node.Anchor != "" {
		return fmt.Errorf("%s: anchors are not permitted", path)
	}
	if !validMCPNodeTag(node.Tag) {
		return fmt.Errorf("%s: custom YAML tag %q is not permitted", path, node.Tag)
	}
	switch node.Kind {
	case yamlv3.ScalarNode:
		if node.Tag == "!!null" {
			return fmt.Errorf("%s: null is not permitted", path)
		}
		if !utf8.ValidString(node.Value) {
			return fmt.Errorf("%s: value is not valid UTF-8", path)
		}
		return nil
	case yamlv3.MappingNode:
		seen := map[string]bool{}
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode := node.Content[index]
			valueNode := node.Content[index+1]
			if keyNode.Kind == yamlv3.AliasNode || keyNode.Anchor != "" {
				return fmt.Errorf("%s: aliases and anchors are not permitted on keys", path)
			}
			if keyNode.Kind != yamlv3.ScalarNode || keyNode.Tag != "!!str" {
				return fmt.Errorf("%s: keys must be strings", path)
			}
			if keyNode.Value == "<<" {
				return fmt.Errorf("%s: YAML merge keys are not permitted", path)
			}
			if seen[keyNode.Value] {
				return fmt.Errorf("%s: duplicate key %q", path, keyNode.Value)
			}
			seen[keyNode.Value] = true
			if err := validateMCPPolicyNode(valueNode, path+"."+keyNode.Value); err != nil {
				return err
			}
		}
		return nil
	case yamlv3.SequenceNode:
		for index, child := range node.Content {
			if err := validateMCPPolicyNode(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s: unsupported YAML node", path)
	}
}

func validMCPNodeTag(tag string) bool {
	switch tag {
	case "", "!!map", "!!seq", "!!str", "!!int", "!!bool", "!!float", "!!null":
		return true
	default:
		return false
	}
}

func validateMCPPolicyTypes(policyNode *yamlv3.Node) error {
	if policyNode == nil || policyNode.Kind != yamlv3.MappingNode {
		return fmt.Errorf("mcp.mutation_policy must be an object")
	}
	for index := 0; index+1 < len(policyNode.Content); index += 2 {
		key := policyNode.Content[index].Value
		value := policyNode.Content[index+1]
		switch key {
		case "enabled":
			if err := requireMCPYAMLTag(value, "!!bool", "mcp.mutation_policy.enabled"); err != nil {
				return err
			}
		case "policy_id", "version":
			if err := requireMCPYAMLTag(value, "!!str", "mcp.mutation_policy."+key); err != nil {
				return err
			}
		case "operations":
			if err := requireMCPNodeTag(value, "!!seq", "mcp.mutation_policy.operations"); err != nil {
				return err
			}
			for opIndex, operationNode := range value.Content {
				if err := validateMCPOperationTypes(operationNode, fmt.Sprintf("mcp.mutation_policy.operations[%d]", opIndex)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateMCPOperationTypes(operationNode *yamlv3.Node, path string) error {
	if operationNode == nil || operationNode.Kind != yamlv3.MappingNode {
		return fmt.Errorf("%s must be an object", path)
	}
	for index := 0; index+1 < len(operationNode.Content); index += 2 {
		key := operationNode.Content[index].Value
		value := operationNode.Content[index+1]
		fieldPath := path + "." + key
		switch key {
		case "server_id", "tool_name", "min_trust_level", "proposal_ttl", "execution_backend":
			if err := requireMCPYAMLTag(value, "!!str", fieldPath); err != nil {
				return err
			}
		case "destructive", "require_approval", "audit_required":
			if err := requireMCPYAMLTag(value, "!!bool", fieldPath); err != nil {
				return err
			}
		case "max_proposals_per_run", "max_pending_proposals", "max_input_bytes", "max_output_bytes":
			if err := requireMCPYAMLTag(value, "!!int", fieldPath); err != nil {
				return err
			}
		case "allowed_agents", "required_capabilities", "allowed_resources", "required_argument_fields", "allowed_argument_fields", "allowed_transports":
			if err := requireMCPStringSequence(value, fieldPath); err != nil {
				return err
			}
		case "allowed_sources":
			if err := requireMCPIntegerSequence(value, fieldPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireMCPYAMLTag(node *yamlv3.Node, tag, path string) error {
	if node == nil || node.Kind != yamlv3.ScalarNode || node.Tag != tag {
		return fmt.Errorf("%s has the wrong scalar type", path)
	}
	return nil
}

func requireMCPNodeTag(node *yamlv3.Node, tag, path string) error {
	if node == nil || node.Tag != tag {
		return fmt.Errorf("%s has the wrong type", path)
	}
	return nil
}

func requireMCPStringSequence(node *yamlv3.Node, path string) error {
	if node == nil || node.Kind != yamlv3.SequenceNode {
		return fmt.Errorf("%s must be a sequence", path)
	}
	for index, child := range node.Content {
		if child == nil || child.Kind != yamlv3.ScalarNode || child.Tag != "!!str" {
			return fmt.Errorf("%s[%d] must be a string", path, index)
		}
	}
	return nil
}

func requireMCPIntegerSequence(node *yamlv3.Node, path string) error {
	if node == nil || node.Kind != yamlv3.SequenceNode {
		return fmt.Errorf("%s must be a sequence", path)
	}
	for index, child := range node.Content {
		if child == nil || child.Kind != yamlv3.ScalarNode || child.Tag != "!!int" {
			return fmt.Errorf("%s[%d] must be an integer", path, index)
		}
	}
	return nil
}
