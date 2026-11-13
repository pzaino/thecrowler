package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MCP operator identity and mutation-policy bounds. The agent package reuses
// these values so configuration validation and runtime validation cannot
// silently diverge.
const (
	MCPServerIDGrammar    = `^[a-z][a-z0-9-]{0,31}$`
	MCPMaxServerIDBytes   = 32
	MCPMaxToolNameBytes   = 128
	MCPMaxPolicyNameBytes = 128
	MCPMaxResourceBytes   = 256

	MCPMutationMaxOperations       = 32
	MCPMutationMaxAgents           = 32
	MCPMutationMaxCapabilities     = 16
	MCPMutationMaxSources          = 64
	MCPMutationMaxResources        = 32
	MCPMutationMaxArgumentFields   = 64
	MCPMutationMaxProposalsPerRun  = 16
	MCPMutationMaxPendingProposals = 100
	MCPMutationMaxInputBytes       = 64 << 10
	MCPMutationMaxOutputBytes      = 64 << 10
)

var mcpServerIDPattern = regexp.MustCompile(MCPServerIDGrammar)

// ValidMCPServerID reports whether id is a canonical operator server ID.
func ValidMCPServerID(id string) bool {
	return mcpServerIDPattern.MatchString(id)
}

// ValidMCPToolName reports whether name is a canonical raw MCP tool name.
// Operators must use the exact advertised name; callers may trim a discovered
// name before calling this helper.
func ValidMCPToolName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || len(name) > MCPMaxToolNameBytes {
		return false
	}
	return validMCPText(name)
}

func validMCPPolicyText(value string, maxBytes int) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxBytes {
		return false
	}
	return validMCPText(value)
}

func validMCPText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// MCPMutationPolicyConfig is the operator-owned proposal policy. It is
// deliberately part of MCPConfig rather than an agent manifest or job spec
// field. Absent or disabled policy grants no MCP mutation proposal authority.
type MCPMutationPolicyConfig struct {
	Enabled    bool                         `yaml:"enabled" json:"enabled"`
	PolicyID   string                       `yaml:"policy_id" json:"policy_id"`
	Version    string                       `yaml:"version" json:"version"`
	Operations []MCPMutationOperationConfig `yaml:"operations" json:"operations"`
}

// MCPMutationOperationConfig binds one exact server/tool pair to proposal
// authority. ToolAlias is intentionally absent: it is derived at normalization
// time with MCPToolAlias from the trusted server and tool names.
type MCPMutationOperationConfig struct {
	ServerID               string   `yaml:"server_id" json:"server_id"`
	ToolName               string   `yaml:"tool_name" json:"tool_name"`
	AllowedAgents          []string `yaml:"allowed_agents" json:"allowed_agents"`
	RequiredCapabilities   []string `yaml:"required_capabilities" json:"required_capabilities"`
	MinTrustLevel          string   `yaml:"min_trust_level" json:"min_trust_level"`
	AllowedSources         []uint64 `yaml:"allowed_sources" json:"allowed_sources"`
	AllowedResources       []string `yaml:"allowed_resources" json:"allowed_resources"`
	RequiredArgumentFields []string `yaml:"required_argument_fields" json:"required_argument_fields"`
	AllowedArgumentFields  []string `yaml:"allowed_argument_fields" json:"allowed_argument_fields"`
	Destructive            bool     `yaml:"destructive" json:"destructive"`
	ProposalTTL            string   `yaml:"proposal_ttl" json:"proposal_ttl"`
	MaxProposalsPerRun     int      `yaml:"max_proposals_per_run" json:"max_proposals_per_run"`
	MaxPendingProposals    int      `yaml:"max_pending_proposals" json:"max_pending_proposals"`
	MaxInputBytes          int      `yaml:"max_input_bytes" json:"max_input_bytes"`
	MaxOutputBytes         int      `yaml:"max_output_bytes" json:"max_output_bytes"`
	RequireApproval        bool     `yaml:"require_approval" json:"require_approval"`
	ExecutionBackend       string   `yaml:"execution_backend" json:"execution_backend"`
	AllowedTransports      []string `yaml:"allowed_transports" json:"allowed_transports"`
	AuditRequired          bool     `yaml:"audit_required" json:"audit_required"`
}

// Validate validates the policy structure. Inventory binding is validated
// separately against the normalized operator server list.
func (p MCPMutationPolicyConfig) Validate() error {
	if p.Enabled {
		if !validMCPPolicyText(p.PolicyID, MCPMaxPolicyNameBytes) {
			return fmt.Errorf("policy_id must be a non-empty canonical string of at most %d bytes", MCPMaxPolicyNameBytes)
		}
		if !validMCPPolicyText(p.Version, MCPMaxPolicyNameBytes) {
			return fmt.Errorf("version must be a non-empty canonical string of at most %d bytes", MCPMaxPolicyNameBytes)
		}
		if len(p.Operations) == 0 {
			return fmt.Errorf("operations must contain 1..%d entries when enabled", MCPMutationMaxOperations)
		}
	}
	if len(p.Operations) > MCPMutationMaxOperations {
		return fmt.Errorf("operations must contain at most %d entries", MCPMutationMaxOperations)
	}
	seen := map[string]bool{}
	seenAliases := map[string]string{}
	for index, operation := range p.Operations {
		if err := operation.Validate(); err != nil {
			return fmt.Errorf("operations[%d]: %w", index, err)
		}
		key := operation.ServerID + "\x00" + operation.ToolName
		if seen[key] {
			return fmt.Errorf("operations[%d]: duplicate server/tool operation", index)
		}
		seen[key] = true
		alias := MCPToolAlias(operation.ServerID, operation.ToolName)
		if previous, exists := seenAliases[alias]; exists {
			return fmt.Errorf("operations[%d]: derived alias %q collides with %s", index, alias, previous)
		}
		seenAliases[alias] = operation.ServerID + "/" + operation.ToolName
	}
	return nil
}

// Validate validates one operation's structural and authority-bearing fields.
func (o MCPMutationOperationConfig) Validate() error {
	if !ValidMCPServerID(o.ServerID) {
		return fmt.Errorf("server_id must match %s", MCPServerIDGrammar)
	}
	if !ValidMCPToolName(o.ToolName) {
		return fmt.Errorf("tool_name must be a non-empty canonical name of at most %d bytes", MCPMaxToolNameBytes)
	}
	if err := validateMCPPolicyList("allowed_agents", o.AllowedAgents, true, MCPMutationMaxAgents, MCPMaxPolicyNameBytes); err != nil {
		return err
	}
	if err := validateMCPPolicyList("required_capabilities", o.RequiredCapabilities, true, MCPMutationMaxCapabilities, MCPMaxPolicyNameBytes); err != nil {
		return err
	}
	if !containsMCPString(o.RequiredCapabilities, "tool_execution") {
		return fmt.Errorf("required_capabilities must include tool_execution")
	}
	switch o.MinTrustLevel {
	case "trusted", "medium", "privileged", "high", "internal", "system":
	default:
		return fmt.Errorf("min_trust_level must be a trusted-or-higher level")
	}
	if len(o.AllowedSources) == 0 && len(o.AllowedResources) == 0 {
		return fmt.Errorf("exactly one of allowed_sources or allowed_resources must be configured")
	}
	if len(o.AllowedSources) > 0 && len(o.AllowedResources) > 0 {
		return fmt.Errorf("allowed_sources and allowed_resources are mutually exclusive")
	}
	if len(o.AllowedSources) > MCPMutationMaxSources {
		return fmt.Errorf("allowed_sources must contain at most %d entries", MCPMutationMaxSources)
	}
	seenSources := map[uint64]bool{}
	for _, sourceID := range o.AllowedSources {
		if sourceID == 0 {
			return fmt.Errorf("allowed_sources contains zero")
		}
		if seenSources[sourceID] {
			return fmt.Errorf("allowed_sources contains a duplicate")
		}
		seenSources[sourceID] = true
	}
	if len(o.AllowedResources) > MCPMutationMaxResources {
		return fmt.Errorf("allowed_resources must contain at most %d entries", MCPMutationMaxResources)
	}
	seenResources := map[string]bool{}
	for index, resource := range o.AllowedResources {
		if resource == "" || resource != strings.TrimSpace(resource) || len(resource) > MCPMaxResourceBytes || !validMCPText(resource) {
			return fmt.Errorf("allowed_resources[%d] is invalid", index)
		}
		if seenResources[resource] {
			return fmt.Errorf("allowed_resources contains a duplicate")
		}
		seenResources[resource] = true
	}
	if err := validateMCPPolicyList("required_argument_fields", o.RequiredArgumentFields, false, MCPMutationMaxArgumentFields, MCPMaxPolicyNameBytes); err != nil {
		return err
	}
	if err := validateMCPPolicyList("allowed_argument_fields", o.AllowedArgumentFields, false, MCPMutationMaxArgumentFields, MCPMaxPolicyNameBytes); err != nil {
		return err
	}
	if len(o.AllowedArgumentFields) > 0 {
		allowed := map[string]bool{}
		for _, name := range o.AllowedArgumentFields {
			allowed[name] = true
		}
		for _, name := range o.RequiredArgumentFields {
			if !allowed[name] {
				return fmt.Errorf("required_argument_fields must be a subset of allowed_argument_fields")
			}
		}
	}
	ttl, err := time.ParseDuration(o.ProposalTTL)
	if err != nil || ttl < time.Second || ttl > time.Hour {
		return fmt.Errorf("proposal_ttl must be a duration within 1s..1h")
	}
	if o.MaxProposalsPerRun < 1 || o.MaxProposalsPerRun > MCPMutationMaxProposalsPerRun {
		return fmt.Errorf("max_proposals_per_run must be 1..%d", MCPMutationMaxProposalsPerRun)
	}
	if o.MaxPendingProposals < 1 || o.MaxPendingProposals > MCPMutationMaxPendingProposals {
		return fmt.Errorf("max_pending_proposals must be 1..%d", MCPMutationMaxPendingProposals)
	}
	if o.MaxInputBytes < 1 || o.MaxInputBytes > MCPMutationMaxInputBytes {
		return fmt.Errorf("max_input_bytes must be 1..%d", MCPMutationMaxInputBytes)
	}
	if o.MaxOutputBytes < 1 || o.MaxOutputBytes > MCPMutationMaxOutputBytes {
		return fmt.Errorf("max_output_bytes must be 1..%d", MCPMutationMaxOutputBytes)
	}
	if !o.RequireApproval {
		return fmt.Errorf("require_approval must be true for every enabled mutation operation")
	}
	if !o.AuditRequired {
		return fmt.Errorf("audit_required must be true for every enabled mutation operation")
	}
	if o.ExecutionBackend != "none" {
		return fmt.Errorf("execution_backend must be none")
	}
	if len(o.AllowedTransports) == 0 || len(o.AllowedTransports) > 2 {
		return fmt.Errorf("allowed_transports must contain 1..2 entries")
	}
	seenTransports := map[string]bool{}
	for _, transport := range o.AllowedTransports {
		if transport != mcpTransportStdioConfig && transport != mcpTransportStreamableHTTPConfig {
			return fmt.Errorf("unsupported allowed transport %q", transport)
		}
		if seenTransports[transport] {
			return fmt.Errorf("allowed_transports contains a duplicate")
		}
		seenTransports[transport] = true
	}
	return nil
}

var (
	mcpTransportStdioConfig          = "stdio"
	mcpTransportStreamableHTTPConfig = "streamable_http"
)

func validateMCPPolicyList(name string, values []string, required bool, max int, maxBytes int) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(values) > max {
		return fmt.Errorf("%s must contain at most %d entries", name, max)
	}
	seen := map[string]bool{}
	for index, value := range values {
		if !validMCPPolicyText(value, maxBytes) {
			return fmt.Errorf("%s[%d] is invalid", name, index)
		}
		if seen[value] {
			return fmt.Errorf("%s contains a duplicate", name)
		}
		seen[value] = true
	}
	return nil
}

func containsMCPString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// EffectiveMCPToolSources returns the source scope effective for one tool,
// narrowing server scope with tool scope and never widening either. The agent
// runtime uses this same helper during descriptor discovery.
func EffectiveMCPToolSources(server MCPServerConfig, tool MCPApprovedTool) map[uint64]bool {
	out := map[uint64]bool{}
	switch {
	case len(server.AllowedSources) > 0 && len(tool.AllowedSources) > 0:
		serverSet := map[uint64]bool{}
		for _, id := range server.AllowedSources {
			serverSet[id] = true
		}
		for _, id := range tool.AllowedSources {
			if serverSet[id] {
				out[id] = true
			}
		}
	case len(tool.AllowedSources) > 0:
		for _, id := range tool.AllowedSources {
			out[id] = true
		}
	case len(server.AllowedSources) > 0:
		for _, id := range server.AllowedSources {
			out[id] = true
		}
	}
	return out
}

// ValidateInventory binds every enabled operation to the normalized operator
// server/tool inventory. It does not start a server or discover a remote tool.
func (p MCPMutationPolicyConfig) ValidateInventory(servers []MCPServerConfig) error {
	if !p.Enabled || len(p.Operations) == 0 {
		return nil
	}
	if err := validateMCPInventoryAliases(servers); err != nil {
		return err
	}
	for index, operation := range p.Operations {
		server, found := findMCPPolicyServer(servers, operation.ServerID)
		if !found {
			return fmt.Errorf("operations[%d]: server_id %q is not in the operator inventory", index, operation.ServerID)
		}
		if !server.Enabled {
			return fmt.Errorf("operations[%d]: server %q is disabled", index, operation.ServerID)
		}
		transport := strings.ToLower(strings.TrimSpace(server.Transport))
		if transport == "" {
			transport = mcpTransportStdioConfig
		}
		if len(operation.AllowedTransports) != 1 || operation.AllowedTransports[0] != transport {
			return fmt.Errorf("operations[%d]: allowed_transports must exactly match the server transport %q", index, transport)
		}
		tool, found := findMCPPolicyTool(server.ApprovedTools, operation.ToolName)
		if !found {
			return fmt.Errorf("operations[%d]: tool_name %q is not approved on server %q", index, operation.ToolName, operation.ServerID)
		}
		effect := strings.ToLower(strings.TrimSpace(tool.Effect))
		if effect == "" && tool.ReadOnly {
			effect = "read_only"
		}
		if effect != "mutation" {
			return fmt.Errorf("operations[%d]: tool %q is not approved as effect mutation", index, operation.ToolName)
		}
		if !tool.AllowMutationProposal {
			return fmt.Errorf("operations[%d]: tool %q does not allow mutation proposals", index, operation.ToolName)
		}
		toolCapabilities := tool.RequiredCapabilities
		if len(toolCapabilities) == 0 {
			toolCapabilities = []string{"tool_execution"}
		}
		for _, capability := range toolCapabilities {
			if !containsMCPString(operation.RequiredCapabilities, capability) {
				return fmt.Errorf("operations[%d]: required_capabilities must include tool capability %q", index, capability)
			}
		}
		if len(server.AllowedAgents) > 0 {
			for _, agentID := range operation.AllowedAgents {
				if !containsMCPString(server.AllowedAgents, agentID) {
					return fmt.Errorf("operations[%d]: agent %q is not allowed by server %q", index, agentID, operation.ServerID)
				}
			}
		}
		effective := EffectiveMCPToolSources(server, tool)
		if len(operation.AllowedSources) > 0 {
			if len(effective) == 0 {
				return fmt.Errorf("operations[%d]: source scope requires an approved server/tool source scope", index)
			}
			for _, sourceID := range operation.AllowedSources {
				if !effective[sourceID] {
					return fmt.Errorf("operations[%d]: source %d is outside the approved server/tool scope", index, sourceID)
				}
			}
		}
		if len(operation.AllowedResources) > 0 && len(effective) > 0 {
			return fmt.Errorf("operations[%d]: resource scope conflicts with the server/tool source scope", index)
		}
	}
	return nil
}

func findMCPPolicyServer(servers []MCPServerConfig, id string) (MCPServerConfig, bool) {
	for _, server := range servers {
		if server.ID == id {
			return server, true
		}
	}
	return MCPServerConfig{}, false
}

func findMCPPolicyTool(tools []MCPApprovedTool, name string) (MCPApprovedTool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return MCPApprovedTool{}, false
}

// Clone returns a deep copy of the policy configuration.
func (p MCPMutationPolicyConfig) Clone() MCPMutationPolicyConfig {
	out := p
	out.Operations = make([]MCPMutationOperationConfig, len(p.Operations))
	for index, operation := range p.Operations {
		out.Operations[index] = operation.Clone()
	}
	return out
}

// Clone returns a deep copy of one operation configuration.
func (o MCPMutationOperationConfig) Clone() MCPMutationOperationConfig {
	out := o
	out.AllowedAgents = append([]string(nil), o.AllowedAgents...)
	out.RequiredCapabilities = append([]string(nil), o.RequiredCapabilities...)
	out.AllowedSources = append([]uint64(nil), o.AllowedSources...)
	out.AllowedResources = append([]string(nil), o.AllowedResources...)
	out.RequiredArgumentFields = append([]string(nil), o.RequiredArgumentFields...)
	out.AllowedArgumentFields = append([]string(nil), o.AllowedArgumentFields...)
	out.AllowedTransports = append([]string(nil), o.AllowedTransports...)
	return out
}

func validateMCPInventoryAliases(servers []MCPServerConfig) error {
	seen := map[string]string{}
	for _, server := range servers {
		for _, tool := range server.ApprovedTools {
			alias := MCPToolAlias(server.ID, tool.Name)
			identity := server.ID + "/" + tool.Name
			if previous, exists := seen[alias]; exists && previous != identity {
				return fmt.Errorf("server inventory alias %q collides between %s and %s", alias, previous, identity)
			}
			seen[alias] = identity
		}
	}
	return nil
}
