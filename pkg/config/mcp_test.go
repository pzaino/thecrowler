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

func TestMCPHTTPConfigYAMLParsing(t *testing.T) {
	raw := []byte(`
mcp:
  servers:
    - id: remoteread
      enabled: true
      transport: streamable_http
      endpoint: https://mcp.example.com/mcp
      auth:
        mode: bearer
        token_env: MCP_REMOTE_TOKEN
      tls:
        ca_file: /etc/crowler/mcp-ca.pem
      network:
        allowed_private_cidrs: ["127.0.0.1/32"]
        allow_insecure_loopback: false
      protocol:
        allow_legacy: false
      approved_tools:
        - name: echo
          read_only: true
          required_capabilities: [tool_execution]
`)
	var config Config
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parse HTTP MCP config: %v", err)
	}
	if len(config.MCP.Servers) != 1 {
		t.Fatalf("server count = %d", len(config.MCP.Servers))
	}
	server := config.MCP.Servers[0]
	if server.Transport != "streamable_http" || server.Endpoint != "https://mcp.example.com/mcp" ||
		server.Auth.Mode != "bearer" || server.Auth.TokenEnv != "MCP_REMOTE_TOKEN" ||
		server.TLS.CAFile != "/etc/crowler/mcp-ca.pem" ||
		len(server.Network.AllowedPrivateCIDRs) != 1 || server.Protocol.AllowLegacy {
		t.Fatalf("unexpected HTTP MCP config: %+v", server)
	}
	for _, text := range []string{fmt.Sprintf("%#v", config.MCP), config.MCP.String()} {
		if strings.Contains(text, "mcp.example.com") || strings.Contains(text, "MCP_REMOTE_TOKEN") ||
			strings.Contains(text, "/etc/crowler/mcp-ca.pem") {
			t.Fatalf("HTTP MCP config formatting exposed connection or credential references: %s", text)
		}
	}
}
