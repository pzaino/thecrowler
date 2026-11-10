package config

import (
	"fmt"
	"strings"
)

// MCPConfig is the operator-controlled inventory of external MCP servers. It
// is intentionally separate from agent manifests: an agent may reference an
// approved namespaced tool, but it cannot define an executable, argument list,
// environment, scope or budget for an MCP server.
type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers" json:"servers"`
}

// MCPServerConfig describes one approved stdio MCP server. Disabled is the
// default. Only stdio is supported in WP-04A.
type MCPServerConfig struct {
	ID                   string            `yaml:"id" json:"id"`
	Enabled              bool              `yaml:"enabled" json:"enabled"`
	Transport            string            `yaml:"transport" json:"transport"`
	Command              string            `yaml:"command" json:"command"`
	Args                 []string          `yaml:"args" json:"args"`
	WorkingDirectory     string            `yaml:"working_directory" json:"working_directory"`
	EnvironmentAllowlist []string          `yaml:"environment_allowlist" json:"environment_allowlist"`
	Environment          map[string]string `yaml:"environment" json:"environment"`
	AllowedAgents        []string          `yaml:"allowed_agents" json:"allowed_agents"`
	AllowedSources       []uint64          `yaml:"allowed_sources" json:"allowed_sources"`
	ApprovedTools        []MCPApprovedTool `yaml:"approved_tools" json:"approved_tools"`

	MaxTools             int `yaml:"max_tools" json:"max_tools"`
	MaxToolMetadataBytes int `yaml:"max_tool_metadata_bytes" json:"max_tool_metadata_bytes"`
	MaxStartupSeconds    int `yaml:"max_startup_seconds" json:"max_startup_seconds"`
	MaxDiscoverySeconds  int `yaml:"max_discovery_seconds" json:"max_discovery_seconds"`
	MaxCallSeconds       int `yaml:"max_call_seconds" json:"max_call_seconds"`
	MaxConcurrentCalls   int `yaml:"max_concurrent_calls" json:"max_concurrent_calls"`
	MaxRequestBytes      int `yaml:"max_request_bytes" json:"max_request_bytes"`
	MaxResponseBytes     int `yaml:"max_response_bytes" json:"max_response_bytes"`
	MaxToolCalls         int `yaml:"max_tool_calls" json:"max_tool_calls"`
}

// MCPApprovedTool is an explicit operator attestation for one server/tool
// pair. ReadOnly must be true in WP-04A; server-provided read-only hints are
// never trusted.
type MCPApprovedTool struct {
	Name                 string   `yaml:"name" json:"name"`
	ReadOnly             bool     `yaml:"read_only" json:"read_only"`
	Description          string   `yaml:"description" json:"description"`
	AllowedSources       []uint64 `yaml:"allowed_sources" json:"allowed_sources"`
	RequiredCapabilities []string `yaml:"required_capabilities" json:"required_capabilities"`
}

// String deliberately avoids printing executable arguments and environment
// values, which may contain operator-provisioned secrets, when the full
// configuration is logged at startup.
func (c MCPConfig) String() string {
	servers := make([]string, 0, len(c.Servers))
	for _, server := range c.Servers {
		servers = append(servers, fmt.Sprintf("{ID:%q Enabled:%t Transport:%q ApprovedTools:%d}",
			server.ID, server.Enabled, server.Transport, len(server.ApprovedTools)))
	}
	return "MCPConfig{Servers:[" + strings.Join(servers, ",") + "]}"
}

// GoString implements fmt.GoStringer so %#v configuration logging also
// redacts executable arguments and environment values.
func (c MCPConfig) GoString() string { return c.String() }
