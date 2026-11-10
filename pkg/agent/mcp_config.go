package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// WP-04A is deliberately a read-only stdio MCP boundary. The operator owns the
// server executable, arguments, environment, inventory and limits. Agent
// manifests may only reference the resulting namespaced tool names.

const (
	mcpTransportStdio          = "stdio"
	mcpTransportStreamableHTTP = "streamable_http"
	mcpModernProtocolVersion   = "2026-07-28"
	mcpToolPrefix              = "mcp_"
	mcpMaxToolCount            = 64
	mcpMaxServerIDLen          = 32
	mcpMaxRawToolLen           = 128
	// mcpMaxContentBlocks bounds the number of text blocks retained from one
	// result. The SDK already bounds a decoded frame; this keeps our own
	// result-shaping allocation bounded as well.
	mcpMaxContentBlocks = 64
)

var (
	mcpServerIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	mcpEnvNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	mcpSecretEnvPrefixes = []string{
		"CROWLER_", "DB_", "POSTGRES_", "MYSQL_", "AWS_", "AZURE_",
		"GOOGLE_", "OPENAI_", "ANTHROPIC_", "OLLAMA_", "GITHUB_",
	}
	mcpSecretEnvSuffixes = []string{
		"_PASSWORD", "_PASSWD", "_SECRET", "_TOKEN", "_API_KEY",
		"_PRIVATE_KEY", "_CREDENTIALS", "_AUTH_KEY",
	}
)

var (
	mcpConfigMu    sync.RWMutex
	mcpConfigValue cfg.MCPConfig
)

// SetMCPOperatorConfig validates and freezes the operator-controlled MCP
// inventory. Passing an empty config disables MCP. The setter is intended to
// be called once during service startup, not from model/tool input.
func SetMCPOperatorConfig(config cfg.MCPConfig) error {
	normalized, err := normalizeMCPOperatorConfig(config)
	if err != nil {
		return err
	}
	mcpConfigMu.Lock()
	mcpConfigValue = normalized
	mcpConfigMu.Unlock()
	return nil
}

// CurrentMCPOperatorConfig returns a defensive copy of the frozen inventory.
func CurrentMCPOperatorConfig() cfg.MCPConfig {
	mcpConfigMu.RLock()
	defer mcpConfigMu.RUnlock()
	return cloneMCPConfig(mcpConfigValue)
}

// MCPApprovedToolLookup returns the approved read-only tool metadata for a
// namespaced alias, if the operator inventory contains it. It is used while
// validating goal specs before per-run MCP sessions exist.
type MCPApprovedToolLookup struct {
	ServerID string
	Tool     cfg.MCPApprovedTool
}

func CurrentMCPApprovedTool(alias string) (MCPApprovedToolLookup, bool) {
	mcpConfigMu.RLock()
	defer mcpConfigMu.RUnlock()
	for _, server := range mcpConfigValue.Servers {
		if !server.Enabled || (server.Transport != mcpTransportStdio && server.Transport != mcpTransportStreamableHTTP) {
			continue
		}
		for _, tool := range server.ApprovedTools {
			if MCPToolAlias(server.ID, tool.Name) == alias && tool.ReadOnly {
				return MCPApprovedToolLookup{ServerID: server.ID, Tool: cloneMCPApprovedTool(tool)}, true
			}
		}
	}
	return MCPApprovedToolLookup{}, false
}

// MCPToolAlias returns the stable model-facing identifier for a server/tool
// pair. Server IDs cannot contain underscores, so the first underscore after
// the prefix is an unambiguous server delimiter. If the readable name would
// exceed the registry limit, a deterministic hash suffix disambiguates it.
func MCPToolAlias(serverID, rawTool string) string {
	serverID = strings.TrimSpace(serverID)
	sanitized := sanitizeMCPToolName(rawTool)
	base := mcpToolPrefix + serverID + "_" + sanitized
	if len(base) <= maxToolNameLen {
		return base
	}
	sum := sha256.Sum256([]byte(serverID + "\x00" + rawTool))
	suffix := hex.EncodeToString(sum[:])[:10]
	limit := maxToolNameLen - len(suffix) - 1
	if limit < len(mcpToolPrefix)+len(serverID)+1 {
		limit = len(mcpToolPrefix) + len(serverID) + 1
	}
	if limit > len(base) {
		limit = len(base)
	}
	return strings.TrimRight(base[:limit], "_-") + "_" + suffix
}

func sanitizeMCPToolName(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		out = "tool"
	}
	return out
}

func normalizeMCPOperatorConfig(config cfg.MCPConfig) (cfg.MCPConfig, error) {
	out := cfg.MCPConfig{Servers: make([]cfg.MCPServerConfig, 0, len(config.Servers))}
	seenServers := map[string]bool{}
	seenAliases := map[string]string{}
	for index, server := range config.Servers {
		normalized, err := normalizeMCPServerConfig(server)
		if err != nil {
			return cfg.MCPConfig{}, fmt.Errorf("mcp.servers[%d]: %w", index, err)
		}
		if seenServers[normalized.ID] {
			return cfg.MCPConfig{}, fmt.Errorf("mcp.servers[%d]: duplicate server ID %q", index, normalized.ID)
		}
		seenServers[normalized.ID] = true
		for _, tool := range normalized.ApprovedTools {
			alias := MCPToolAlias(normalized.ID, tool.Name)
			if previous, exists := seenAliases[alias]; exists {
				return cfg.MCPConfig{}, fmt.Errorf("mcp.servers[%d]: alias %q collides with %s", index, alias, previous)
			}
			seenAliases[alias] = normalized.ID + "/" + tool.Name
		}
		out.Servers = append(out.Servers, normalized)
	}
	return out, nil
}

func normalizeMCPServerConfig(server cfg.MCPServerConfig) (cfg.MCPServerConfig, error) {
	server.ID = strings.TrimSpace(server.ID)
	if !mcpServerIDPattern.MatchString(server.ID) {
		return cfg.MCPServerConfig{}, fmt.Errorf("id must match %s", mcpServerIDPattern.String())
	}
	if !server.Enabled {
		return server, nil
	}
	if strings.TrimSpace(server.Transport) == "" {
		server.Transport = mcpTransportStdio
	}
	switch server.Transport {
	case mcpTransportStdio:
		if err := validateMCPStdioConfig(&server); err != nil {
			return cfg.MCPServerConfig{}, err
		}
	case mcpTransportStreamableHTTP:
		if err := validateMCPHTTPConfig(&server); err != nil {
			return cfg.MCPServerConfig{}, err
		}
	default:
		return cfg.MCPServerConfig{}, fmt.Errorf("transport %q is not supported (use %q or %q)",
			server.Transport, mcpTransportStdio, mcpTransportStreamableHTTP)
	}
	if len(server.ApprovedTools) == 0 {
		return cfg.MCPServerConfig{}, fmt.Errorf("approved_tools must not be empty")
	}
	if len(server.ApprovedTools) > mcpMaxToolCount {
		return cfg.MCPServerConfig{}, fmt.Errorf("approved_tools exceeds %d", mcpMaxToolCount)
	}
	toolNames := map[string]bool{}
	for index, tool := range server.ApprovedTools {
		tool.Name = strings.TrimSpace(tool.Name)
		if tool.Name == "" || len(tool.Name) > mcpMaxRawToolLen {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved_tools[%d].name is invalid", index)
		}
		if containsMCPControl(tool.Name) {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved_tools[%d].name contains a control character", index)
		}
		if toolNames[tool.Name] {
			return cfg.MCPServerConfig{}, fmt.Errorf("duplicate approved tool %q", tool.Name)
		}
		toolNames[tool.Name] = true
		if !tool.ReadOnly {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q is not read-only; mutation MCP tools are disabled in WP-04A", tool.Name)
		}
		if len(tool.Description) > 2048 {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q description exceeds 2048 bytes", tool.Name)
		}
		if tool.RequiredCapabilities == nil {
			tool.RequiredCapabilities = []string{"tool_execution"}
		}
		if !containsString(tool.RequiredCapabilities, "tool_execution") {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q must require tool_execution", tool.Name)
		}
		for _, sourceID := range tool.AllowedSources {
			if sourceID == 0 {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q has invalid source scope", tool.Name)
			}
		}
		server.ApprovedTools[index] = tool
	}
	for _, sourceID := range server.AllowedSources {
		if sourceID == 0 {
			return cfg.MCPServerConfig{}, fmt.Errorf("allowed_sources contains zero")
		}
	}
	applyMCPDefaults(&server)
	if err := validateMCPLimits(server); err != nil {
		return cfg.MCPServerConfig{}, err
	}
	return server, nil
}

func validateMCPStdioConfig(server *cfg.MCPServerConfig) error {
	if strings.TrimSpace(server.Endpoint) != "" || server.Auth.Mode != "" ||
		server.Auth.TokenEnv != "" || server.Auth.TokenFile != "" ||
		server.TLS.CAFile != "" || len(server.Network.AllowedPrivateCIDRs) > 0 ||
		server.Network.AllowInsecureLoopback || server.Protocol.AllowLegacy {
		return fmt.Errorf("streamable HTTP connection fields are only valid for transport %q", mcpTransportStreamableHTTP)
	}
	if err := validateMCPCommand(server.Command); err != nil {
		return err
	}
	if len(server.Args) > 64 {
		return fmt.Errorf("args exceeds 64 entries")
	}
	for _, arg := range server.Args {
		if len(arg) > 4096 || strings.IndexByte(arg, 0) >= 0 {
			return fmt.Errorf("arg is oversized or contains NUL")
		}
		for _, r := range arg {
			if r < 0x20 && r != '\t' || r == 0x7f {
				return fmt.Errorf("arg contains a control character")
			}
		}
	}
	if server.WorkingDirectory != "" {
		if !filepath.IsAbs(server.WorkingDirectory) {
			return fmt.Errorf("working_directory must be absolute")
		}
		info, err := os.Stat(server.WorkingDirectory)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("working_directory is not a readable directory")
		}
	}
	envSeen := map[string]bool{}
	for index, name := range server.EnvironmentAllowlist {
		name = strings.TrimSpace(name)
		if !mcpEnvNamePattern.MatchString(name) {
			return fmt.Errorf("invalid environment allowlist name %q", name)
		}
		if mcpSecretEnvironmentName(name) {
			return fmt.Errorf("environment allowlist may not inherit sensitive variable %q", name)
		}
		if envSeen[name] {
			return fmt.Errorf("duplicate environment allowlist name %q", name)
		}
		envSeen[name] = true
		server.EnvironmentAllowlist[index] = name
	}
	for name, value := range server.Environment {
		if !mcpEnvNamePattern.MatchString(name) {
			return fmt.Errorf("invalid environment name %q", name)
		}
		if len(value) > 8192 || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("environment value for %q is oversized or contains NUL", name)
		}
	}
	return nil
}

func validateMCPHTTPConfig(server *cfg.MCPServerConfig) error {
	if server.Command != "" || len(server.Args) > 0 || server.WorkingDirectory != "" ||
		len(server.EnvironmentAllowlist) > 0 || len(server.Environment) > 0 {
		return fmt.Errorf("stdio process fields are only valid for transport %q", mcpTransportStdio)
	}
	endpoint, err := normalizeMCPHTTPEndpoint(server.Endpoint)
	if err != nil {
		return err
	}
	server.Endpoint = endpoint
	parsed, _ := url.Parse(endpoint)
	if parsed.Scheme == "http" && !server.Network.AllowInsecureLoopback {
		return fmt.Errorf("remote MCP endpoints must use HTTPS; plain HTTP is only allowed with allow_insecure_loopback for a loopback fixture")
	}
	if parsed.Scheme == "http" && !mcpLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("plain HTTP MCP endpoints are restricted to loopback hosts")
	}

	server.Auth.Mode = strings.ToLower(strings.TrimSpace(server.Auth.Mode))
	if server.Auth.Mode == "" {
		server.Auth.Mode = "none"
	}
	switch server.Auth.Mode {
	case "none":
		if server.Auth.TokenEnv != "" || server.Auth.TokenFile != "" {
			return fmt.Errorf("auth.token_env and auth.token_file require auth.mode bearer")
		}
	case "bearer":
		if (server.Auth.TokenEnv == "") == (server.Auth.TokenFile == "") {
			return fmt.Errorf("bearer authentication requires exactly one of token_env or token_file")
		}
		if server.Auth.TokenEnv != "" {
			server.Auth.TokenEnv = strings.TrimSpace(server.Auth.TokenEnv)
			if !mcpEnvNamePattern.MatchString(server.Auth.TokenEnv) {
				return fmt.Errorf("invalid auth.token_env name")
			}
		}
		if server.Auth.TokenFile != "" {
			server.Auth.TokenFile = strings.TrimSpace(server.Auth.TokenFile)
			if !filepath.IsAbs(server.Auth.TokenFile) {
				return fmt.Errorf("auth.token_file must be absolute")
			}
			info, err := os.Stat(server.Auth.TokenFile)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("auth.token_file is not a readable regular file")
			}
		}
	default:
		return fmt.Errorf("auth.mode %q is not supported (use none or bearer)", server.Auth.Mode)
	}
	if server.TLS.CAFile != "" {
		server.TLS.CAFile = strings.TrimSpace(server.TLS.CAFile)
		if !filepath.IsAbs(server.TLS.CAFile) {
			return fmt.Errorf("tls.ca_file must be absolute")
		}
		info, err := os.Stat(server.TLS.CAFile)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("tls.ca_file is not a readable regular file")
		}
	}
	seenCIDRs := map[string]bool{}
	normalizedCIDRs := make([]string, 0, len(server.Network.AllowedPrivateCIDRs))
	for _, raw := range server.Network.AllowedPrivateCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid network.allowed_private_cidrs entry %q", raw)
		}
		addr := prefix.Addr()
		last := mcpPrefixLastAddr(prefix)
		if addr.IsUnspecified() || last.IsUnspecified() ||
			(!mcpPrivateRangeAddr(addr) && !mcpPrivateRangeAddr(last)) {
			return fmt.Errorf("network.allowed_private_cidrs entry %q must be a private, loopback or link-local range", raw)
		}
		if !mcpPrivateRangeAddr(addr) || !mcpPrivateRangeAddr(last) {
			return fmt.Errorf("network.allowed_private_cidrs entry %q spans a non-private address", raw)
		}
		normalized := prefix.Masked().String()
		if seenCIDRs[normalized] {
			continue
		}
		seenCIDRs[normalized] = true
		normalizedCIDRs = append(normalizedCIDRs, normalized)
	}
	server.Network.AllowedPrivateCIDRs = normalizedCIDRs
	return nil
}

func normalizeMCPHTTPEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("endpoint is required for streamable_http")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("endpoint scheme must be https (http is allowed only for an explicitly approved loopback fixture)")
	}
	if parsed.Opaque != "" || parsed.User != nil {
		return "", fmt.Errorf("endpoint must not contain opaque or userinfo components")
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" || parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("endpoint must not contain a query or fragment")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" || strings.Contains(host, "%") || strings.HasSuffix(host, ".") {
		return "", fmt.Errorf("endpoint host is invalid")
	}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 253 {
			return "", fmt.Errorf("endpoint host is too long")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", fmt.Errorf("endpoint host is not a valid DNS name")
			}
			for _, r := range label {
				if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
					return "", fmt.Errorf("endpoint host is not a valid DNS name")
				}
			}
		}
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("endpoint port is invalid")
	}
	urlPath := parsed.EscapedPath()
	if urlPath == "" {
		urlPath = "/"
	}
	if !strings.HasPrefix(urlPath, "/") || strings.Contains(urlPath, "%") ||
		strings.Contains(urlPath, "\\") || path.Clean(urlPath) != urlPath {
		return "", fmt.Errorf("endpoint path is ambiguous or invalid")
	}
	normalized := url.URL{
		Scheme: parsed.Scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   urlPath,
	}
	return normalized.String(), nil
}

func mcpPrivateRangeAddr(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()
}

func mcpPrefixLastAddr(prefix netip.Prefix) netip.Addr {
	addr := prefix.Addr()
	bits := prefix.Bits()
	if addr.Is4() {
		raw := addr.As4()
		for bit := bits; bit < 32; bit++ {
			raw[bit/8] |= 1 << (7 - uint(bit%8))
		}
		return netip.AddrFrom4(raw)
	}
	raw := addr.As16()
	for bit := bits; bit < 128; bit++ {
		raw[bit/8] |= 1 << (7 - uint(bit%8))
	}
	return netip.AddrFrom16(raw)
}

func mcpLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func validateMCPCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	if !filepath.IsAbs(command) {
		return fmt.Errorf("command must be an absolute path")
	}
	base := strings.ToLower(filepath.Base(command))
	switch base {
	case "sh", "bash", "dash", "zsh", "ksh", "csh", "tcsh", "fish", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return fmt.Errorf("shell executables are not permitted for stdio MCP servers")
	}
	info, err := os.Stat(command)
	if err != nil {
		return fmt.Errorf("command is not accessible")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("command is not a regular file")
	}
	return nil
}
func mcpSecretEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range mcpSecretEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, suffix := range mcpSecretEnvSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

func applyMCPDefaults(server *cfg.MCPServerConfig) {
	if server.MaxTools <= 0 {
		server.MaxTools = 32
	}
	if server.MaxToolMetadataBytes <= 0 {
		server.MaxToolMetadataBytes = 64 << 10
	}
	if server.MaxStartupSeconds <= 0 {
		server.MaxStartupSeconds = 5
	}
	if server.MaxDiscoverySeconds <= 0 {
		server.MaxDiscoverySeconds = 10
	}
	if server.MaxCallSeconds <= 0 {
		server.MaxCallSeconds = 15
	}
	if server.MaxConcurrentCalls <= 0 {
		server.MaxConcurrentCalls = 1
	}
	if server.MaxRequestBytes <= 0 {
		server.MaxRequestBytes = 32 << 10
	}
	if server.MaxResponseBytes <= 0 {
		server.MaxResponseBytes = 64 << 10
	}
	if server.MaxToolCalls <= 0 {
		server.MaxToolCalls = 8
	}
}

func validateMCPLimits(server cfg.MCPServerConfig) error {
	checks := []struct {
		name string
		val  int
		min  int
		max  int
	}{
		{"max_tools", server.MaxTools, 1, mcpMaxToolCount},
		{"max_tool_metadata_bytes", server.MaxToolMetadataBytes, 1024, 256 << 10},
		{"max_startup_seconds", server.MaxStartupSeconds, 1, 30},
		{"max_discovery_seconds", server.MaxDiscoverySeconds, 1, 60},
		{"max_call_seconds", server.MaxCallSeconds, 1, 120},
		{"max_concurrent_calls", server.MaxConcurrentCalls, 1, 8},
		{"max_request_bytes", server.MaxRequestBytes, 1024, 1 << 20},
		{"max_response_bytes", server.MaxResponseBytes, 1024, 2 << 20},
		{"max_tool_calls", server.MaxToolCalls, 1, 32},
	}
	for _, check := range checks {
		if check.val < check.min || check.val > check.max {
			return fmt.Errorf("%s must be %d..%d", check.name, check.min, check.max)
		}
	}
	return nil
}

func cloneMCPConfig(config cfg.MCPConfig) cfg.MCPConfig {
	out := cfg.MCPConfig{Servers: make([]cfg.MCPServerConfig, len(config.Servers))}
	for i, server := range config.Servers {
		out.Servers[i] = server
		out.Servers[i].Args = append([]string(nil), server.Args...)
		out.Servers[i].EnvironmentAllowlist = append([]string(nil), server.EnvironmentAllowlist...)
		out.Servers[i].AllowedAgents = append([]string(nil), server.AllowedAgents...)
		out.Servers[i].AllowedSources = append([]uint64(nil), server.AllowedSources...)
		out.Servers[i].Network.AllowedPrivateCIDRs = append([]string(nil), server.Network.AllowedPrivateCIDRs...)
		out.Servers[i].Environment = cloneStringMap(server.Environment)
		out.Servers[i].ApprovedTools = make([]cfg.MCPApprovedTool, len(server.ApprovedTools))
		for j, tool := range server.ApprovedTools {
			out.Servers[i].ApprovedTools[j] = cloneMCPApprovedTool(tool)
		}
	}
	return out
}

func cloneMCPApprovedTool(tool cfg.MCPApprovedTool) cfg.MCPApprovedTool {
	tool.AllowedSources = append([]uint64(nil), tool.AllowedSources...)
	tool.RequiredCapabilities = append([]string(nil), tool.RequiredCapabilities...)
	return tool
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortedMCPEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
