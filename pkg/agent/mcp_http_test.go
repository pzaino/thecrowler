package agent

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func mcpHTTPFixtureServer(t *testing.T, mode string) *mcp.Server {
	t.Helper()
	options := &mcp.ServerOptions{PageSize: 2}
	server := mcp.NewServer(&mcp.Implementation{Name: "crowler-mcp-http-fixture", Version: "1.0.0"}, options)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo approved text."}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpEchoInput) (*mcp.CallToolResult, any, error) {
		return mcpText("echo: " + in.Text), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "source_status", Description: "Return a fixture source observation."}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpSourceInput) (*mcp.CallToolResult, any, error) {
		return mcpText(fmt.Sprintf("fixture source %d status: observed", in.SourceID)), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "Return a tool-level error."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fixture HTTP tool error"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "fail_structured", Description: "Return a structured tool-level error."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{
			"is_error": true, "detail": "fixture HTTP structured failure",
		}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "success_text_flag", Description: "Return successful text with an is_error field."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return mcpText(`{"is_error":true,"status":"remote HTTP text data"}`), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "success_structured_flag", Description: "Return successful structured data with an is_error field."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"is_error": true, "status": "remote HTTP structured data",
		}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "large", Description: "Return an oversized observation."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return mcpText(strings.Repeat("x", 100<<10)), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "slow", Description: "Return after a bounded delay."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		select {
		case <-time.After(2 * time.Second):
			return mcpText("slow HTTP fixture result"), nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	})
	if mode == "legacy_protocol" {
		options = &mcp.ServerOptions{PageSize: 2, SupportedProtocolVersions: []string{"2025-11-25"}}
		server = mcp.NewServer(&mcp.Implementation{Name: "crowler-mcp-http-fixture", Version: "1.0.0"}, options)
		mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo approved text."}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpEchoInput) (*mcp.CallToolResult, any, error) {
			return mcpText("echo: " + in.Text), nil, nil
		})
	}
	return server
}

func mcpHTTPFixtureHandler(t *testing.T, mode, authToken string) http.Handler {
	t.Helper()
	server := mcpHTTPFixtureServer(t, mode)
	opts := &mcp.StreamableHTTPOptions{Stateless: true}
	if mode == "json" {
		opts.JSONResponse = true
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, opts)
	if authToken == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+authToken {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func mcpHTTPFixtureTLS(t *testing.T, mode, authToken string) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewTLSServer(mcpHTTPFixtureHandler(t, mode, authToken))
	t.Cleanup(server.Close)
	cert := server.Certificate()
	if cert == nil {
		t.Fatalf("HTTP fixture did not provide a certificate")
	}
	path := filepath.Join(t.TempDir(), "mcp-http-ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write MCP HTTP CA fixture: %v", err)
	}
	return server, path
}

func mcpHTTPFixtureConfig(t *testing.T, endpoint, caFile string, tools ...cfg.MCPApprovedTool) cfg.MCPServerConfig {
	t.Helper()
	if len(tools) == 0 {
		tools = []cfg.MCPApprovedTool{{Name: "echo", ReadOnly: true}}
	}
	return cfg.MCPServerConfig{
		ID: "remote", Enabled: true, Transport: mcpTransportStreamableHTTP,
		Endpoint: endpoint,
		TLS:      cfg.MCPHTTPTLSConfig{CAFile: caFile},
		Network: cfg.MCPHTTPNetworkConfig{
			AllowedPrivateCIDRs: []string{"127.0.0.1/32", "::1/128"},
		},
		ApprovedTools:     tools,
		MaxStartupSeconds: 5, MaxDiscoverySeconds: 5, MaxCallSeconds: 2,
		MaxConcurrentCalls: 2, MaxRequestBytes: 32 << 10, MaxResponseBytes: 64 << 10,
		MaxToolCalls: 8, MaxTools: 32, MaxToolMetadataBytes: 64 << 10,
	}
}

func mcpHTTPRuntime(t *testing.T, server cfg.MCPServerConfig, allowlist []string, sources []uint64) (*MCPToolManager, *AgentToolRegistry, AgentIdentity) {
	t.Helper()
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, allowlist, sources)
	if err != nil {
		t.Fatalf("new HTTP MCP manager: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		_ = manager.Close()
		t.Fatalf("register HTTP MCP tools: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, registry, identity
}

func TestMCPStreamableHTTPJSONAndSSEResults(t *testing.T) {
	for _, mode := range []string{"json", "sse"} {
		t.Run(mode, func(t *testing.T) {
			server, caFile := mcpHTTPFixtureTLS(t, mode, "")
			cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
			_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "echo")}, nil)
			result, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "echo"), map[string]any{"text": "http"}, nil)
			if err != nil {
				t.Fatalf("HTTP %s MCP call: %v", mode, err)
			}
			if result["server_id"] != "remote" || result["content"] != "echo: http" {
				t.Fatalf("HTTP %s result attribution/content wrong: %+v", mode, result)
			}
		})
	}
}

func TestMCPStreamableHTTPAuthenticationAndCredentialIsolation(t *testing.T) {
	previous := AgentsEngine
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = previous })
	server, caFile := mcpHTTPFixtureTLS(t, "json", "fixture-token")
	t.Setenv("MCP_HTTP_TEST_TOKEN", "fixture-token")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	cfgServer.Auth = cfg.MCPHTTPAuthConfig{Mode: "bearer", TokenEnv: "MCP_HTTP_TEST_TOKEN"}
	_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "echo")}, nil)
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "echo"), map[string]any{"text": "authenticated"}, nil); err != nil {
		t.Fatalf("authenticated HTTP MCP call: %v", err)
	}
	for _, event := range AgentsEngine.AuditEvents() {
		if strings.Contains(fmt.Sprint(event), "fixture-token") {
			t.Fatalf("audit exposed MCP bearer token: %+v", event)
		}
	}

	bad := cfgServer
	bad.Auth.TokenEnv = "MCP_HTTP_TEST_MISSING_TOKEN"
	setMCPTestConfig(t, mcpTestConfigFor(bad))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(bad), identity, []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("missing MCP token was accepted")
	}
	t.Setenv("MCP_HTTP_TEST_WRONG_TOKEN", "wrong-token")
	wrong := cfgServer
	wrong.Auth.TokenEnv = "MCP_HTTP_TEST_WRONG_TOKEN"
	setMCPTestConfig(t, mcpTestConfigFor(wrong))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(wrong), identity, []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("invalid MCP token was accepted")
	}
}

func TestMCPStreamableHTTPRejectsRedirectsAndUnexpectedStatus(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("redirect_%d", status), func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/elsewhere", status)
			})
			server := httptest.NewTLSServer(handler)
			t.Cleanup(server.Close)
			caFile := writeMCPHTTPCA(t, server)
			cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
			setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
			if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
				t.Fatalf("HTTP redirect %d was accepted", status)
			}
		})
	}
	t.Run("unexpected status", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fixture failure", http.StatusBadGateway)
		})
		server := httptest.NewTLSServer(handler)
		t.Cleanup(server.Close)
		caFile := writeMCPHTTPCA(t, server)
		cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
		setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
			t.Fatalf("unexpected HTTP status was accepted")
		}
	})
}

func writeMCPHTTPCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	cert := server.Certificate()
	if cert == nil {
		t.Fatalf("HTTP fixture did not provide a certificate")
	}
	path := filepath.Join(t.TempDir(), "mcp-http-ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write MCP HTTP CA fixture: %v", err)
	}
	return path
}

func TestMCPStreamableHTTPRejectsUntrustedCertificate(t *testing.T) {
	server, _ := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, "")
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("untrusted MCP TLS certificate was accepted")
	}
}

func TestMCPStreamableHTTPToolErrorAndSuccessClassification(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile,
		cfg.MCPApprovedTool{Name: "fail", ReadOnly: true},
		cfg.MCPApprovedTool{Name: "success_text_flag", ReadOnly: true},
	)
	_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "fail"), MCPToolAlias("remote", "success_text_flag")}, nil)
	failureResult, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "fail"), map[string]any{}, nil)
	failure, ok := asToolExecutionFailure(err)
	if !ok || failure.Code != "mcp_tool_error" || failureResult["is_error"] != true {
		t.Fatalf("HTTP MCP tool failure not classified: result=%+v err=%v", failureResult, err)
	}
	successResult, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "success_text_flag"), map[string]any{}, nil)
	if err != nil || successResult["is_error"] != false {
		t.Fatalf("HTTP successful MCP result misclassified: %+v err=%v", successResult, err)
	}
}

func TestMCPHTTPResponseBodyLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(strings.Repeat("x", 128<<10)))
	}))
	t.Cleanup(server.Close)
	caFile := writeMCPHTTPCA(t, server)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	cfgServer.MaxResponseBytes = 1024
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	client, err := newMCPHTTPClient(cfgServer)
	if err != nil {
		t.Fatalf("new bounded HTTP client: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("bounded HTTP GET: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, errMCPHTTPResponseTooLarge) {
		t.Fatalf("oversized HTTP body error = %v", err)
	}
}

func TestMCPHTTPAddressPolicy(t *testing.T) {
	denied := []string{
		"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"fc00::1", "fe80::1", "169.254.169.254", "::ffff:127.0.0.1",
		"224.0.0.1", "0.0.0.0", "::", "100.64.0.1",
		"64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::808:808",
		"64:ff9b:1::7f00:1", "2002:7f00:1::1", "2002:808:808::1",
		"2001::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2",
		"100::1", "3fff::1", "2001:db8::1", "::ffff:169.254.169.254",
	}
	for _, raw := range denied {
		if addr := netip.MustParseAddr(raw); !mcpHTTPAddressDenied(addr) {
			t.Errorf("address %s was not denied", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if addr := netip.MustParseAddr(raw); mcpHTTPAddressDenied(addr) {
			t.Errorf("public address %s was denied", raw)
		}
	}
	if !mcpHTTPPrefixAllowed(netip.MustParseAddr("127.0.0.1"), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}) {
		t.Fatalf("explicit loopback prefix was not honoured")
	}
}

func TestMCPHTTPDialRejectsMixedDNSAnswers(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("8.8.8.8")},
			{IP: net.ParseIP("127.0.0.1")},
		}, nil
	}
	dial := mcpHTTPDialContext(&net.Dialer{}, nil, lookup)
	if _, err := dial(context.Background(), "tcp", "example.com:443"); !errors.Is(err, errMCPHTTPDestinationDenied) {
		t.Fatalf("mixed DNS answer error = %v, want destination denial", err)
	}
}

func TestMCPHTTPURLExactBinding(t *testing.T) {
	approved, _ := url.Parse("https://mcp.example.com:443/mcp")
	for _, raw := range []string{
		"https://mcp.example.com:443/mcp",
		"https://MCP.EXAMPLE.COM:443/mcp",
	} {
		got, _ := url.Parse(raw)
		if !mcpHTTPURLMatches(got, approved) {
			t.Errorf("equivalent endpoint %q did not match", raw)
		}
	}
	for _, raw := range []string{
		"https://mcp.example.com:443/other",
		"https://mcp.example.com:8443/mcp",
		"http://mcp.example.com:443/mcp",
		"https://mcp.example.com:443/mcp?x=1",
		"https://user@mcp.example.com:443/mcp",
	} {
		got, _ := url.Parse(raw)
		if mcpHTTPURLMatches(got, approved) {
			t.Errorf("non-approved endpoint %q matched", raw)
		}
	}
}

func TestMCPHTTPClientDisablesAmbientProxy(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	client, err := newMCPHTTPClient(cfgServer)
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	roundTripper, ok := client.Transport.(*mcpHTTPRoundTripper)
	if !ok {
		t.Fatalf("unexpected HTTP transport type %T", client.Transport)
	}
	base, ok := roundTripper.base.(*http.Transport)
	if !ok || base.Proxy != nil {
		t.Fatalf("ambient HTTP proxy policy is not disabled")
	}
}

func TestMCPStreamableHTTPProtocolPolicy(t *testing.T) {
	t.Run("incompatible", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(mcpHTTPUnsupportedProtocolHandler))
		t.Cleanup(server.Close)
		caFile := writeMCPHTTPCA(t, server)
		cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
		setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
			t.Fatalf("incompatible HTTP protocol was accepted")
		}
	})
	t.Run("legacy requires explicit policy", func(t *testing.T) {
		server, caFile := mcpHTTPFixtureTLS(t, "legacy_protocol", "")
		cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
		setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
			t.Fatalf("legacy HTTP protocol was accepted without allow_legacy")
		}
		cfgServer.Protocol.AllowLegacy = true
		_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "echo")}, nil)
		if _, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "echo"), map[string]any{"text": "legacy"}, nil); err != nil {
			t.Fatalf("explicit legacy HTTP protocol failed: %v", err)
		}
	})
}

func mcpHTTPUnsupportedProtocolHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&msg)
	w.Header().Set("Content-Type", "application/json")
	switch msg.Method {
	case "initialize":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": msg.ID,
			"result": map[string]any{
				"protocolVersion": "2099-01-01",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "incompatible", "version": "1"},
			},
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": msg.ID,
			"error": map[string]any{"code": -32601, "message": "method not found"},
		})
	}
}

func TestMCPStreamableHTTPGoalEvidenceAndToolError(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile,
		cfg.MCPApprovedTool{Name: "fail", ReadOnly: true},
		cfg.MCPApprovedTool{Name: "echo", ReadOnly: true},
	)
	manager, registry, identity := mcpHTTPRuntime(t, cfgServer,
		[]string{MCPToolAlias("remote", "fail"), MCPToolAlias("remote", "echo")}, nil)
	t.Cleanup(func() { _ = manager.Close() })

	t.Run("failed HTTP observation is not evidence", func(t *testing.T) {
		alias := MCPToolAlias("remote", "fail")
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("http-fail-call", alias, `{}`)),
			chatWithText("The remote tool succeeded; goal complete."),
		}}
		result, err := RunGoal(goalRunConfig{
			Spec: mcpGoalSpecFor(t, alias, map[string]any{}, false), Identity: identity, Registry: registry,
			EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
			RunID: "http-fail-goal", TraceID: "http-fail-trace",
		})
		if err != nil {
			t.Fatalf("HTTP failed-tool goal: %v", err)
		}
		if result.Status == GoalCriteriaMet || len(result.Evidence) != 0 {
			t.Fatalf("failed HTTP tool satisfied evidence: %+v", result)
		}
		if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "failed" ||
			result.ToolCalls[0].ReasonCode != "mcp_tool_error" {
			t.Fatalf("HTTP failure classification lost: %+v", result.ToolCalls)
		}
	})

	t.Run("successful HTTP observation is evidence", func(t *testing.T) {
		alias := MCPToolAlias("remote", "echo")
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("http-ok-call", alias, `{"text":"http-goal"}`)),
		}}
		result, err := RunGoal(goalRunConfig{
			Spec:     mcpGoalSpecFor(t, alias, map[string]any{"text": "http-goal"}, false),
			Identity: identity, Registry: registry,
			EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
			RunID: "http-ok-goal", TraceID: "http-ok-trace",
		})
		if err != nil {
			t.Fatalf("HTTP successful-tool goal: %v", err)
		}
		if result.Status != GoalCriteriaMet || len(result.Evidence) != 1 {
			t.Fatalf("successful HTTP observation did not satisfy evidence: %+v", result)
		}
	})
}

func TestMCPStdioAndStreamableHTTPTogether(t *testing.T) {
	httpServer, caFile := mcpHTTPFixtureTLS(t, "json", "")
	remote := mcpHTTPFixtureConfig(t, httpServer.URL, caFile)
	local := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "echo", ReadOnly: true})
	local.ID = "local"
	config := cfg.MCPConfig{Servers: []cfg.MCPServerConfig{local, remote}}
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	localAlias := MCPToolAlias("local", "echo")
	remoteAlias := MCPToolAlias("remote", "echo")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{localAlias, remoteAlias}, nil)
	if err != nil {
		t.Fatalf("mixed stdio/HTTP MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry, err := DefaultToolRegistry()
	if err != nil {
		t.Fatalf("native registry: %v", err)
	}
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register mixed MCP tools: %v", err)
	}
	if _, ok := registry.Get("get_source_status"); !ok {
		t.Fatalf("native tool was not retained in mixed registry")
	}
	for _, tc := range []struct {
		alias string
		text  string
	}{
		{localAlias, "local"},
		{remoteAlias, "remote"},
	} {
		result, err := callMCPTool(t, registry, identity, tc.alias, map[string]any{"text": tc.text}, nil)
		if err != nil {
			t.Fatalf("mixed MCP call %s: %v", tc.alias, err)
		}
		if result["content"] != "echo: "+tc.text {
			t.Fatalf("mixed MCP result %s: %+v", tc.alias, result)
		}
	}
}

func TestMCPStreamableHTTPSourceScope(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile,
		cfg.MCPApprovedTool{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	)
	_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "source_status")}, []uint64{42})
	alias := MCPToolAlias("remote", "source_status")
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"source_id": float64(42)}, []uint64{42}); err != nil {
		t.Fatalf("in-scope HTTP source call: %v", err)
	}
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"source_id": float64(43)}, []uint64{42}); err == nil {
		t.Fatalf("out-of-scope HTTP source call succeeded")
	}
}

func TestMCPStreamableHTTPConcurrentCredentialIsolation(t *testing.T) {
	alphaServer, alphaCA := mcpHTTPFixtureTLS(t, "json", "alpha-token")
	betaServer, betaCA := mcpHTTPFixtureTLS(t, "json", "beta-token")
	t.Setenv("MCP_HTTP_ALPHA_TOKEN", "alpha-token")
	t.Setenv("MCP_HTTP_BETA_TOKEN", "beta-token")
	alpha := mcpHTTPFixtureConfig(t, alphaServer.URL, alphaCA)
	alpha.ID = "alpha"
	alpha.Auth = cfg.MCPHTTPAuthConfig{Mode: "bearer", TokenEnv: "MCP_HTTP_ALPHA_TOKEN"}
	beta := mcpHTTPFixtureConfig(t, betaServer.URL, betaCA)
	beta.ID = "beta"
	beta.Auth = cfg.MCPHTTPAuthConfig{Mode: "bearer", TokenEnv: "MCP_HTTP_BETA_TOKEN"}
	setMCPTestConfig(t, cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta}})
	identity := mcpTestIdentity()
	run := func(server cfg.MCPServerConfig) error {
		manager, err := NewMCPToolManager(context.Background(), cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}},
			identity, []string{MCPToolAlias(server.ID, "echo")}, nil)
		if err != nil {
			return err
		}
		defer manager.Close()
		registry := NewAgentToolRegistry()
		if err := manager.Register(registry); err != nil {
			return err
		}
		result, err := callMCPTool(t, registry, identity, MCPToolAlias(server.ID, "echo"),
			map[string]any{"text": server.ID}, nil)
		if err != nil {
			return err
		}
		if result["server_id"] != server.ID {
			return fmt.Errorf("server attribution mismatch: %+v", result)
		}
		return nil
	}
	errs := make(chan error, 2)
	go func() { errs <- run(alpha) }()
	go func() { errs <- run(beta) }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent HTTP MCP run: %v", err)
		}
	}

	crossed := alpha
	crossed.Auth.TokenEnv = "MCP_HTTP_BETA_TOKEN"
	if _, err := NewMCPToolManager(context.Background(), cfg.MCPConfig{Servers: []cfg.MCPServerConfig{crossed}},
		identity, []string{MCPToolAlias("alpha", "echo")}, nil); err == nil {
		t.Fatalf("server A accepted server B's credential")
	}
}

func TestMCPStreamableHTTPRejectsWrongHostname(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, strings.Replace(server.URL, "127.0.0.1", "localhost", 1), caFile)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("MCP TLS hostname mismatch was accepted")
	}
}

func TestMCPStreamableHTTPCancellationAndClose(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "sse", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile, cfg.MCPApprovedTool{Name: "slow", ReadOnly: true})
	manager, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "slow")}, nil)
	tool, ok := registry.Get(MCPToolAlias("remote", "slow"))
	if !ok {
		t.Fatalf("HTTP slow tool not registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := ContextWithToolRuntime(ctx, ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist: map[string]bool{tool.Name(): true}, RunID: "http-cancel-run",
		},
	})
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(runtime, map[string]any{})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("cancelled HTTP MCP call returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled HTTP MCP call did not return")
	}
	if err := manager.Close(); err != nil {
		t.Logf("HTTP manager close returned %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Logf("repeated HTTP manager close returned %v", err)
	}
}

func TestMCPStreamableHTTPLoopbackFixtureRequiresExplicitPolicy(t *testing.T) {
	server := httptest.NewServer(mcpHTTPFixtureHandler(t, "json", ""))
	t.Cleanup(server.Close)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, "")
	cfgServer.Endpoint = strings.Replace(cfgServer.Endpoint, "https://", "http://", 1)
	cfgServer.Network.AllowInsecureLoopback = true
	cfgServer.Network.AllowedPrivateCIDRs = []string{"127.0.0.1/32"}
	_, registry, identity := mcpHTTPRuntime(t, cfgServer, []string{MCPToolAlias("remote", "echo")}, nil)
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "echo"), map[string]any{"text": "loopback"}, nil); err != nil {
		t.Fatalf("explicit loopback HTTP fixture failed: %v", err)
	}
}

func TestMCPStreamableHTTPMalformedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":`))
	}))
	t.Cleanup(server.Close)
	caFile := writeMCPHTTPCA(t, server)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("malformed MCP HTTP response was accepted")
	}
}

func TestMCPStreamableHTTPLimitsAndTimeout(t *testing.T) {
	server, caFile := mcpHTTPFixtureTLS(t, "json", "")
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile,
		cfg.MCPApprovedTool{Name: "large", ReadOnly: true},
		cfg.MCPApprovedTool{Name: "slow", ReadOnly: true},
	)
	cfgServer.MaxResponseBytes = 64 << 10
	cfgServer.MaxCallSeconds = 1
	_, registry, identity := mcpHTTPRuntime(t, cfgServer,
		[]string{MCPToolAlias("remote", "large"), MCPToolAlias("remote", "slow")}, nil)
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "large"), map[string]any{}, nil); !errors.Is(err, ErrMCPUnsupportedResult) {
		t.Fatalf("oversized HTTP MCP result error = %v", err)
	}
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("remote", "slow"), map[string]any{}, nil); !errors.Is(err, ErrMCPTimeout) {
		t.Fatalf("slow HTTP MCP result error = %v", err)
	}
}

func TestMCPStreamableHTTPRejectsExpiredCertificate(t *testing.T) {
	server, caFile := expiredMCPHTTPServer(t)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(cfgServer), mcpTestIdentity(), []string{MCPToolAlias("remote", "echo")}, nil); err == nil {
		t.Fatalf("expired MCP TLS certificate was accepted")
	}
}

func expiredMCPHTTPServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate expired cert key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    now.Add(-2 * time.Hour),
		NotAfter:     now.Add(-time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create expired cert: %v", err)
	}
	server := httptest.NewUnstartedServer(mcpHTTPFixtureHandler(t, "json", ""))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{
		Certificate: [][]byte{der}, PrivateKey: key,
	}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	caFile := filepath.Join(t.TempDir(), "expired-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write expired CA: %v", err)
	}
	return server, caFile
}

func TestMCPHTTPSSEEventCountLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < mcpHTTPMaxEvents+5; i++ {
			_, _ = w.Write([]byte("data: event\n\n"))
		}
	}))
	t.Cleanup(server.Close)
	caFile := writeMCPHTTPCA(t, server)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	client, err := newMCPHTTPClient(cfgServer)
	if err != nil {
		t.Fatalf("new bounded HTTP client: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("SSE GET: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, errMCPHTTPTooManyEvents) {
		t.Fatalf("excessive SSE events error = %v", err)
	}
}

func TestMCPHTTPBodyLimitExactBoundary(t *testing.T) {
	cases := []struct {
		name      string
		limit     int64
		body      string
		wantBytes int
		wantErr   error
	}{
		{"empty", 5, "", 0, nil},
		{"one byte", 5, "a", 1, nil},
		{"below limit", 5, "abcd", 4, nil},
		{"exact limit", 5, "abcde", 5, nil},
		{"one over", 5, "abcdef", 5, errMCPHTTPResponseTooLarge},
		{"far over", 5, strings.Repeat("x", 50), 5, errMCPHTTPResponseTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &mcpHTTPBodyLimit{body: io.NopCloser(strings.NewReader(tc.body)), remaining: tc.limit}
			data, err := io.ReadAll(reader)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if len(data) != tc.wantBytes {
				t.Fatalf("read %d bytes, want %d", len(data), tc.wantBytes)
			}
		})
	}
}

func TestMCPHTTPBodyLimitSmallReads(t *testing.T) {
	reader := &mcpHTTPBodyLimit{body: io.NopCloser(strings.NewReader("abcde")), remaining: 5}
	var out []byte
	buf := make([]byte, 2)
	for {
		n, err := reader.Read(buf)
		out = append(out, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("small-read error: %v", err)
		}
	}
	if string(out) != "abcde" {
		t.Fatalf("small-read bytes = %q", out)
	}
}

func TestMCPHTTPBodyLimitReaderEdgeCases(t *testing.T) {
	t.Run("data and EOF together at exact limit", func(t *testing.T) {
		body := &mcpHTTPDataEOFReader{data: []byte("abcde")}
		reader := &mcpHTTPBodyLimit{body: body, remaining: 5}
		data, err := io.ReadAll(reader)
		if err != nil || string(data) != "abcde" {
			t.Fatalf("data+EOF read = %q, %v", data, err)
		}
	})
	t.Run("reader error preserved", func(t *testing.T) {
		sentinel := errors.New("fixture read error")
		body := &mcpHTTPErrorReader{data: []byte("abc"), err: sentinel}
		reader := &mcpHTTPBodyLimit{body: body, remaining: 10}
		data, err := io.ReadAll(reader)
		if !errors.Is(err, sentinel) || string(data) != "abc" {
			t.Fatalf("reader error result = %q, %v", data, err)
		}
	})
}

type mcpHTTPDataEOFReader struct {
	data []byte
	done bool
}

func (r *mcpHTTPDataEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.data)
	return n, io.EOF
}
func (r *mcpHTTPDataEOFReader) Close() error { return nil }

type mcpHTTPErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *mcpHTTPErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	n := copy(p, r.data)
	return n, r.err
}
func (r *mcpHTTPErrorReader) Close() error { return nil }

func TestMCPHTTPClientExactBodyLimit(t *testing.T) {
	limit := int64(1024)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(strings.Repeat("x", int(limit+mcpHTTPBodyOverhead))))
	}))
	t.Cleanup(server.Close)
	caFile := writeMCPHTTPCA(t, server)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	cfgServer.MaxResponseBytes = int(limit)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	client, err := newMCPHTTPClient(cfgServer)
	if err != nil {
		t.Fatalf("new exact-bound HTTP client: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("exact-bound HTTP GET: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || int64(len(data)) != limit+mcpHTTPBodyOverhead {
		t.Fatalf("exact-bound HTTP body = %d bytes, %v", len(data), err)
	}
}

func TestMCPHTTPDialRejectsDeniedDestinationsWithoutDialing(t *testing.T) {
	lookups := map[string][]net.IPAddr{
		"loopback":     {{IP: net.ParseIP("127.0.0.1")}},
		"private":      {{IP: net.ParseIP("10.0.0.1")}},
		"nat64":        {{IP: net.ParseIP("64:ff9b::7f00:1")}},
		"six-to-four":  {{IP: net.ParseIP("2002:7f00:1::1")}},
		"mapped":       {{IP: net.ParseIP("::ffff:10.0.0.1")}},
		"zoned":        {{IP: net.ParseIP("fe80::1"), Zone: "eth0"}},
		"no-addresses": {},
	}
	for name, addresses := range lookups {
		t.Run(name, func(t *testing.T) {
			dials := 0
			dial := func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected dial")
			}
			lookup := func(context.Context, string) ([]net.IPAddr, error) { return addresses, nil }
			connector := mcpHTTPDialContextWithDial(dial, nil, lookup)
			if _, err := connector(context.Background(), "tcp", "example.com:443"); err == nil {
				t.Fatalf("denied destination unexpectedly succeeded")
			}
			if dials != 0 {
				t.Fatalf("underlying dialer was invoked %d times for a denied destination", dials)
			}
		})
	}
}

func TestMCPHTTPDialResolutionFailureDoesNotDial(t *testing.T) {
	dials := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("fixture DNS failure")
	}
	connector := mcpHTTPDialContextWithDial(dial, nil, lookup)
	if _, err := connector(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatalf("DNS failure unexpectedly succeeded")
	}
	if dials != 0 {
		t.Fatalf("dialer was invoked %d times after DNS failure", dials)
	}
}

func TestMCPHTTPDialRevalidatesOnNewConnection(t *testing.T) {
	lookups := [][]net.IPAddr{
		{{IP: net.ParseIP("8.8.8.8")}},
		{{IP: net.ParseIP("64:ff9b::7f00:1")}},
		{{IP: net.ParseIP("127.0.0.1")}},
	}
	calls := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errors.New("fixture dial failure")
	}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		addresses := lookups[0]
		lookups = lookups[1:]
		return addresses, nil
	}
	connector := mcpHTTPDialContextWithDial(dial, nil, lookup)
	if _, err := connector(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatalf("first public connection unexpectedly succeeded without a dial error")
	}
	if calls != 1 {
		t.Fatalf("first public connection dials = %d, want 1", calls)
	}
	if _, err := connector(context.Background(), "tcp", "example.com:443"); !errors.Is(err, errMCPHTTPDestinationDenied) {
		t.Fatalf("second translated destination error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("denied reused host invoked dialer: %d", calls)
	}
	if _, err := connector(context.Background(), "tcp", "example.com:443"); !errors.Is(err, errMCPHTTPDestinationDenied) {
		t.Fatalf("third loopback destination error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("denied new connection invoked dialer: %d", calls)
	}
}

func TestMCPHTTPDialPreservesApprovedHostnameForTLSDial(t *testing.T) {
	// The connector receives the approved URL hostname and returns a validated
	// IP connection; HTTP transport keeps the URL hostname for TLS ServerName.
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	var dialedAddress string
	dial := func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialedAddress = address
		return nil, errors.New("fixture dial failure")
	}
	connector := mcpHTTPDialContextWithDial(dial, nil, lookup)
	_, _ = connector(context.Background(), "tcp", "mcp.example.com:443")
	if dialedAddress != "8.8.8.8:443" {
		t.Fatalf("validated IP dial address = %q", dialedAddress)
	}
}

func TestMCPHTTPClientChunkedExactBodyLimit(t *testing.T) {
	limit := int64(1024)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		flusher, _ := w.(http.Flusher)
		payload := strings.Repeat("x", int(limit+mcpHTTPBodyOverhead))
		half := len(payload) / 2
		_, _ = w.Write([]byte(payload[:half]))
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write([]byte(payload[half:]))
	}))
	t.Cleanup(server.Close)
	caFile := writeMCPHTTPCA(t, server)
	cfgServer := mcpHTTPFixtureConfig(t, server.URL, caFile)
	cfgServer.MaxResponseBytes = int(limit)
	setMCPTestConfig(t, mcpTestConfigFor(cfgServer))
	client, err := newMCPHTTPClient(cfgServer)
	if err != nil {
		t.Fatalf("new chunked-bound HTTP client: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("chunked exact-bound HTTP GET: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || int64(len(data)) != limit+mcpHTTPBodyOverhead {
		t.Fatalf("chunked exact-bound body = %d bytes, %v", len(data), err)
	}
}

func TestMCPHTTPSSEEventLimitAcrossReadBoundariesAndLineEndings(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("line_ending_%q", lineEnding), func(t *testing.T) {
			body := "data: one" + lineEnding + lineEnding + "data: two" + lineEnding + lineEnding
			reader := &mcpHTTPBodyLimit{
				body:      io.NopCloser(strings.NewReader(body)),
				remaining: int64(len(body)),
				maxEvents: 1,
			}
			var out []byte
			buf := make([]byte, 3)
			for {
				n, err := reader.Read(buf)
				out = append(out, buf[:n]...)
				if err != nil {
					if !errors.Is(err, errMCPHTTPTooManyEvents) {
						t.Fatalf("SSE event limit error = %v", err)
					}
					break
				}
			}
			if !strings.Contains(string(out), "one") {
				t.Fatalf("SSE data before error = %q", out)
			}
		})
	}
}
