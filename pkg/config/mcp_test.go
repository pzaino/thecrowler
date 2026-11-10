package config

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestMCPConfigYAMLParsing(t *testing.T) {
	raw := []byte(`
mcp:
  servers:
    - id: localread
      enabled: true
      transport: stdio
      command: /opt/crowler/bin/mcp-server
      args: ["--read-only"]
      environment_allowlist: [PATH]
      environment:
        MCP_MODE: read-only
      allowed_sources: [7]
      approved_tools:
        - name: echo
          read_only: true
          description: echo fixture
          required_capabilities: [tool_execution]
      max_call_seconds: 10
      max_response_bytes: 65536
`)
	var config Config
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	if len(config.MCP.Servers) != 1 {
		t.Fatalf("server count = %d", len(config.MCP.Servers))
	}
	server := config.MCP.Servers[0]
	if server.ID != "localread" || !server.Enabled || server.Transport != "stdio" ||
		server.Command != "/opt/crowler/bin/mcp-server" || len(server.ApprovedTools) != 1 ||
		server.ApprovedTools[0].Name != "echo" || !server.ApprovedTools[0].ReadOnly {
		t.Fatalf("unexpected MCP config: %+v", server)
	}
	for _, text := range []string{config.MCP.String(), fmt.Sprintf("%#v", config.MCP), fmt.Sprintf("%#v", config)} {
		if strings.Contains(text, "read-only") || strings.Contains(text, "--read-only") {
			t.Fatalf("MCP config formatting exposed environment or arguments: %s", text)
		}
	}
}
