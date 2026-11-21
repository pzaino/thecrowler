package agent

import (
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
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// The MCP boundary is operator-controlled. WP-04A/WP-04B admit read-only tools;
// WP-04C1 adds explicit mutation classification and inert proposal descriptors.
// Agent manifests may only reference the resulting namespaced tool names.

const (
	mcpTransportStdio          = "stdio"
	mcpTransportStreamableHTTP = "streamable_http"
	mcpModernProtocolVersion   = "2026-07-28"
	mcpToolPrefix              = cfg.MCPToolAliasPrefix
	mcpMaxToolCount            = 64
	mcpMaxServerIDLen          = cfg.MCPMaxServerIDBytes
	mcpMaxRawToolLen           = cfg.MCPMaxToolNameBytes
	// mcpMaxContentBlocks bounds the number of text blocks retained from one
	// result. The SDK already bounds a decoded frame; this keeps our own
	// result-shaping allocation bounded as well.
	mcpMaxContentBlocks = 64
)

var (
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
	mcpConfigMu          sync.RWMutex
	mcpConfigValue       cfg.MCPConfig
	mcpMutationPolicyVal MCPMutationPolicy
)

// SetMCPOperatorConfig validates and freezes the operator-controlled MCP
// inventory. Passing an empty config disables MCP. The setter is intended to
// be called once during service startup, not from model/tool input.
func SetMCPOperatorConfig(config cfg.MCPConfig) error {
	return setMCPOperatorConfig(config, true)
}

// SetMCPOperatorConfigForApproval installs the same normalized policy and
// inventory as SetMCPOperatorConfig, but does not require stdio command
// executables to exist in this process. The API approval service compares
// configured transport identities and never launches MCP servers, so requiring
// the binaries in the API container would only break deployment.
func SetMCPOperatorConfigForApproval(config cfg.MCPConfig) error {
	return setMCPOperatorConfig(config, false)
}

func setMCPOperatorConfig(config cfg.MCPConfig, requireLocalFiles bool) error {
	normalized, err := normalizeMCPOperatorConfigWithFileCheck(config, requireLocalFiles)
	if err != nil {
		return err
	}
	policy, err := buildMCPMutationPolicy(normalized)
	if err != nil {
		return err
	}
	mcpConfigMu.Lock()
	mcpConfigValue = normalized
	mcpMutationPolicyVal = policy
	mcpConfigMu.Unlock()
	return nil
}

// CurrentMCPOperatorConfig returns a defensive copy of the frozen inventory.
func CurrentMCPOperatorConfig() cfg.MCPConfig {
	mcpConfigMu.RLock()
	defer mcpConfigMu.RUnlock()
	return cloneMCPConfig(mcpConfigValue)
}

// CurrentMCPMutationPolicy returns a defensive copy of the normalized runtime
// policy snapshot. It is empty when the operator policy is absent or disabled.
// B2/B3 production registration remains disabled; this accessor exists for
// validation and future approved wiring.
func CurrentMCPMutationPolicy() MCPMutationPolicy {
	mcpConfigMu.RLock()
	defer mcpConfigMu.RUnlock()
	return cloneMCPMutationPolicy(mcpMutationPolicyVal)
}

// MCPMutationPolicyFromConfig validates and converts one operator MCP config
// into an immutable runtime policy without installing it globally.
func MCPMutationPolicyFromConfig(config cfg.MCPConfig) (MCPMutationPolicy, error) {
	normalized, err := normalizeMCPOperatorConfig(config)
	if err != nil {
		return MCPMutationPolicy{}, err
	}
	return buildMCPMutationPolicy(normalized)
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
			if MCPToolAlias(server.ID, tool.Name) == alias && mcpToolIsReadOnly(tool) {
				return MCPApprovedToolLookup{ServerID: server.ID, Tool: cloneMCPApprovedTool(tool)}, true
			}
		}
	}
	return MCPApprovedToolLookup{}, false
}

// CurrentMCPApprovedMutationTool returns an explicitly classified mutation
// tool approved for proposal-only handling. It never grants execution.
func CurrentMCPApprovedMutationTool(alias string) (MCPApprovedToolLookup, bool) {
	mcpConfigMu.RLock()
	defer mcpConfigMu.RUnlock()
	for _, server := range mcpConfigValue.Servers {
		if !server.Enabled || (server.Transport != mcpTransportStdio && server.Transport != mcpTransportStreamableHTTP) {
			continue
		}
		for _, tool := range server.ApprovedTools {
			if MCPToolAlias(server.ID, tool.Name) == alias && tool.Effect == "mutation" && tool.AllowMutationProposal {
				return MCPApprovedToolLookup{ServerID: server.ID, Tool: cloneMCPApprovedTool(tool)}, true
			}
		}
	}
	return MCPApprovedToolLookup{}, false
}

func mcpToolIsReadOnly(tool cfg.MCPApprovedTool) bool {
	return tool.Effect == "" && tool.ReadOnly || tool.Effect == "read_only"
}

// MCPToolAlias returns the stable model-facing identifier for a server/tool
// pair. Server IDs cannot contain underscores, so the first underscore after
// the prefix is an unambiguous server delimiter. If the readable name would
// exceed the registry limit, a deterministic hash suffix disambiguates it.
// MCPToolAlias derives the model-facing tool name. The derivation lives in
// pkg/config so operator validation and runtime binding use one implementation.
func MCPToolAlias(serverID, rawTool string) string {
	return cfg.MCPToolAlias(serverID, rawTool)
}

func normalizeMCPOperatorConfig(config cfg.MCPConfig) (cfg.MCPConfig, error) {
	return normalizeMCPOperatorConfigWithFileCheck(config, true)
}

func normalizeMCPOperatorConfigWithFileCheck(config cfg.MCPConfig, requireLocalFiles bool) (cfg.MCPConfig, error) {
	out := cfg.MCPConfig{
		Servers:        make([]cfg.MCPServerConfig, 0, len(config.Servers)),
		MutationPolicy: config.MutationPolicy.Clone(),
	}
	seenServers := map[string]bool{}
	seenAliases := map[string]string{}
	for index, server := range config.Servers {
		normalized, err := normalizeMCPServerConfigWithFileCheck(server, requireLocalFiles)
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
		out.Servers = append(out.Servers, cloneMCPServerConfig(normalized))
	}
	if err := out.MutationPolicy.Validate(); err != nil {
		return cfg.MCPConfig{}, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	if err := out.MutationPolicy.ValidateInventory(out.Servers); err != nil {
		return cfg.MCPConfig{}, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	return out, nil
}

func normalizeMCPServerConfig(server cfg.MCPServerConfig) (cfg.MCPServerConfig, error) {
	return normalizeMCPServerConfigWithFileCheck(server, true)
}

func normalizeMCPServerConfigWithFileCheck(server cfg.MCPServerConfig, requireLocalFiles bool) (cfg.MCPServerConfig, error) {
	// Never mutate caller-owned slices or maps while normalizing. A startup
	// caller may still hold the source configuration, and concurrent validation
	// must not observe writes to shared backing arrays.
	server = cloneMCPServerConfig(server)
	server.ID = strings.TrimSpace(server.ID)
	if !cfg.ValidMCPServerID(server.ID) {
		return cfg.MCPServerConfig{}, fmt.Errorf("id must match %s", cfg.MCPServerIDGrammar)
	}
	if !server.Enabled {
		return server, nil
	}
	if strings.TrimSpace(server.Transport) == "" {
		server.Transport = mcpTransportStdio
	}
	switch server.Transport {
	case mcpTransportStdio:
		if err := validateMCPStdioConfig(&server, requireLocalFiles); err != nil {
			return cfg.MCPServerConfig{}, err
		}
	case mcpTransportStreamableHTTP:
		if err := validateMCPHTTPConfig(&server, requireLocalFiles); err != nil {
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
		if !cfg.ValidMCPToolName(tool.Name) {
			return cfg.MCPServerConfig{}, fmt.Errorf("approved_tools[%d].name is invalid", index)
		}
		if toolNames[tool.Name] {
			return cfg.MCPServerConfig{}, fmt.Errorf("duplicate approved tool %q", tool.Name)
		}
		toolNames[tool.Name] = true
		tool.Effect = strings.ToLower(strings.TrimSpace(tool.Effect))
		switch tool.Effect {
		case "":
			if !tool.ReadOnly {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q must declare effect read_only or mutation; read_only:false alone does not authorize mutation", tool.Name)
			}
			tool.Effect = "read_only"
		case "read_only":
			if !tool.ReadOnly {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q has contradictory effect read_only and read_only:false", tool.Name)
			}
			if tool.AllowMutationProposal {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q allow_mutation_proposal is only valid for effect mutation", tool.Name)
			}
		case "mutation":
			if tool.ReadOnly {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q has contradictory effect mutation and read_only:true", tool.Name)
			}
			if !tool.AllowMutationProposal {
				return cfg.MCPServerConfig{}, fmt.Errorf("approved mutation tool %q requires allow_mutation_proposal:true", tool.Name)
			}
		default:
			return cfg.MCPServerConfig{}, fmt.Errorf("approved tool %q has unknown effect %q", tool.Name, tool.Effect)
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

func validateMCPStdioConfig(server *cfg.MCPServerConfig, requireLocalFiles bool) error {
	if strings.TrimSpace(server.Endpoint) != "" || server.Auth.Mode != "" ||
		server.Auth.TokenEnv != "" || server.Auth.TokenFile != "" ||
		server.TLS.CAFile != "" || len(server.Network.AllowedPrivateCIDRs) > 0 ||
		server.Network.AllowInsecureLoopback || server.Protocol.AllowLegacy {
		return fmt.Errorf("streamable HTTP connection fields are only valid for transport %q", mcpTransportStreamableHTTP)
	}
	if err := validateMCPCommand(server.Command, requireLocalFiles); err != nil {
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
		if requireLocalFiles {
			info, err := os.Stat(server.WorkingDirectory)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("working_directory is not a readable directory")
			}
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

func validateMCPHTTPConfig(server *cfg.MCPServerConfig, requireLocalFiles bool) error {
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
			if requireLocalFiles {
				info, err := os.Stat(server.Auth.TokenFile)
				if err != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("auth.token_file is not a readable regular file")
				}
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
		if requireLocalFiles {
			info, err := os.Stat(server.TLS.CAFile)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("tls.ca_file is not a readable regular file")
			}
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

func validateMCPCommand(command string, requireLocalFiles bool) error {
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
	if !requireLocalFiles {
		return nil
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
	out := cfg.MCPConfig{
		Servers:        make([]cfg.MCPServerConfig, len(config.Servers)),
		MutationPolicy: config.MutationPolicy.Clone(),
	}
	for i, server := range config.Servers {
		out.Servers[i] = cloneMCPServerConfig(server)
	}
	return out
}

func cloneMCPServerConfig(server cfg.MCPServerConfig) cfg.MCPServerConfig {
	out := server
	out.Args = append([]string(nil), server.Args...)
	out.EnvironmentAllowlist = append([]string(nil), server.EnvironmentAllowlist...)
	out.AllowedAgents = append([]string(nil), server.AllowedAgents...)
	out.AllowedSources = append([]uint64(nil), server.AllowedSources...)
	out.Network.AllowedPrivateCIDRs = append([]string(nil), server.Network.AllowedPrivateCIDRs...)
	out.Environment = cloneStringMap(server.Environment)
	out.ApprovedTools = make([]cfg.MCPApprovedTool, len(server.ApprovedTools))
	for index, tool := range server.ApprovedTools {
		out.ApprovedTools[index] = cloneMCPApprovedTool(tool)
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

// buildMCPMutationPolicy converts a normalized operator config into the
// runtime policy snapshot. It is intentionally not connected to production
// proposal registration; B2/B3 remain separate approvals.
func buildMCPMutationPolicy(config cfg.MCPConfig) (MCPMutationPolicy, error) {
	if !config.MutationPolicy.Enabled {
		return MCPMutationPolicy{}, nil
	}
	if err := config.MutationPolicy.Validate(); err != nil {
		return MCPMutationPolicy{}, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	if err := config.MutationPolicy.ValidateInventory(config.Servers); err != nil {
		return MCPMutationPolicy{}, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	policy := MCPMutationPolicy{
		Enabled:  true,
		PolicyID: config.MutationPolicy.PolicyID,
		Version:  config.MutationPolicy.Version,
	}
	policy.Operations = make([]MCPMutationOperationPolicy, 0, len(config.MutationPolicy.Operations))
	for _, operation := range config.MutationPolicy.Operations {
		ttl, err := time.ParseDuration(operation.ProposalTTL)
		if err != nil {
			return MCPMutationPolicy{}, fmt.Errorf("mcp.mutation_policy: operation %s/%s: %w", operation.ServerID, operation.ToolName, err)
		}
		policy.Operations = append(policy.Operations, MCPMutationOperationPolicy{
			ServerID:               operation.ServerID,
			ToolName:               operation.ToolName,
			ToolAlias:              MCPToolAlias(operation.ServerID, operation.ToolName),
			AllowedAgents:          append([]string(nil), operation.AllowedAgents...),
			RequiredCapabilities:   append([]string(nil), operation.RequiredCapabilities...),
			MinTrustLevel:          operation.MinTrustLevel,
			AllowedSources:         append([]uint64(nil), operation.AllowedSources...),
			AllowedResources:       append([]string(nil), operation.AllowedResources...),
			RequiredArgumentFields: append([]string(nil), operation.RequiredArgumentFields...),
			AllowedArgumentFields:  append([]string(nil), operation.AllowedArgumentFields...),
			Destructive:            operation.Destructive,
			ProposalTTL:            ttl,
			MaxProposalsPerRun:     operation.MaxProposalsPerRun,
			MaxPendingProposals:    operation.MaxPendingProposals,
			MaxInputBytes:          operation.MaxInputBytes,
			MaxOutputBytes:         operation.MaxOutputBytes,
			RequireApproval:        operation.RequireApproval,
			ExecutionBackend:       operation.ExecutionBackend,
			AllowedTransports:      append([]string(nil), operation.AllowedTransports...),
			AuditRequired:          operation.AuditRequired,
		})
	}
	if err := policy.Validate(); err != nil {
		return MCPMutationPolicy{}, fmt.Errorf("mcp.mutation_policy: %w", err)
	}
	return policy, nil
}

func cloneMCPMutationPolicy(policy MCPMutationPolicy) MCPMutationPolicy {
	out := policy
	out.Operations = make([]MCPMutationOperationPolicy, len(policy.Operations))
	for index, operation := range policy.Operations {
		out.Operations[index] = operation
		out.Operations[index].AllowedAgents = append([]string(nil), operation.AllowedAgents...)
		out.Operations[index].RequiredCapabilities = append([]string(nil), operation.RequiredCapabilities...)
		out.Operations[index].AllowedSources = append([]uint64(nil), operation.AllowedSources...)
		out.Operations[index].AllowedResources = append([]string(nil), operation.AllowedResources...)
		out.Operations[index].RequiredArgumentFields = append([]string(nil), operation.RequiredArgumentFields...)
		out.Operations[index].AllowedArgumentFields = append([]string(nil), operation.AllowedArgumentFields...)
		out.Operations[index].AllowedTransports = append([]string(nil), operation.AllowedTransports...)
	}
	return out
}
