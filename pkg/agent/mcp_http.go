package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const mcpHTTPBodyOverhead = 64 << 10 // bounded JSON-RPC/SSE envelope allowance

var (
	errMCPHTTPRedirect          = errors.New("MCP HTTP redirect is not permitted")
	errMCPHTTPResponseTooLarge  = errors.New("MCP HTTP response exceeds configured limit")
	errMCPHTTPDestinationDenied = errors.New("MCP HTTP destination is not permitted")
	errMCPHTTPTooManyEvents     = errors.New("MCP HTTP stream exceeds event limit")
)

const mcpHTTPMaxEvents = 256

var mcpHTTPReservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

// newMCPHTTPClient builds one per-run HTTP client for an exact approved
// endpoint. It deliberately does not inherit ambient proxies, redirects,
// credentials or TLS verification bypasses.
func newMCPHTTPClient(server cfg.MCPServerConfig) (*http.Client, error) {
	endpoint, err := url.Parse(server.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse approved MCP endpoint: %w", err)
	}
	token, err := loadMCPHTTPToken(server)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if server.TLS.CAFile != "" {
		pem, err := os.ReadFile(server.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read MCP TLS CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("MCP TLS CA file contains no usable certificates")
		}
		tlsConfig.RootCAs = pool
	}
	allowed, err := parseMCPHTTPPrivateCIDRs(server.Network.AllowedPrivateCIDRs)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                  nil, // never inherit HTTP_PROXY/HTTPS_PROXY/ALL_PROXY
		DialContext:            mcpHTTPDialContext(dialer, allowed, net.DefaultResolver.LookupIPAddr),
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		ExpectContinueTimeout:  time.Second,
		IdleConnTimeout:        30 * time.Second,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    2,
		DisableCompression:     true,
		ForceAttemptHTTP2:      true,
	}
	maxBody := int64(server.MaxResponseBytes) + mcpHTTPBodyOverhead
	if maxBody <= 0 {
		maxBody = 64<<10 + mcpHTTPBodyOverhead
	}
	rt := &mcpHTTPRoundTripper{
		base:     transport,
		endpoint: endpoint,
		token:    token,
		maxBody:  maxBody,
	}
	client := &http.Client{
		Transport: rt,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client, nil
}

func loadMCPHTTPToken(server cfg.MCPServerConfig) (string, error) {
	switch strings.ToLower(strings.TrimSpace(server.Auth.Mode)) {
	case "", "none":
		return "", nil
	case "bearer":
		var raw []byte
		if server.Auth.TokenEnv != "" {
			value, ok := os.LookupEnv(server.Auth.TokenEnv)
			if !ok {
				return "", fmt.Errorf("MCP bearer token environment variable %q is not set", server.Auth.TokenEnv)
			}
			raw = []byte(value)
		} else {
			info, err := os.Stat(server.Auth.TokenFile)
			if err != nil || info.Size() > 8192 {
				return "", fmt.Errorf("MCP bearer token file is unavailable or oversized")
			}
			raw, err = os.ReadFile(server.Auth.TokenFile)
			if err != nil {
				return "", fmt.Errorf("read MCP bearer token file: %w", err)
			}
		}
		token := strings.TrimSpace(string(raw))
		if token == "" || len(token) > 4096 {
			return "", fmt.Errorf("MCP bearer token is empty or oversized")
		}
		if strings.ContainsAny(token, "\r\n\x00") {
			return "", fmt.Errorf("MCP bearer token contains invalid control characters")
		}
		return token, nil
	default:
		return "", fmt.Errorf("unsupported MCP authentication mode %q", server.Auth.Mode)
	}
}

func parseMCPHTTPPrivateCIDRs(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("parse MCP network exception %q: %w", raw, err)
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}

type mcpHTTPLookupIPFunc func(context.Context, string) ([]net.IPAddr, error)

func mcpHTTPDialContext(dialer *net.Dialer, allowed []netip.Prefix, lookup mcpHTTPLookupIPFunc) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid MCP HTTP destination: %w", err)
		}
		ips, err := lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve MCP HTTP destination: %w", err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: no addresses resolved", errMCPHTTPDestinationDenied)
		}
		permitted := make([]netip.Addr, 0, len(ips))
		for _, resolved := range ips {
			if resolved.Zone != "" {
				return nil, fmt.Errorf("%w: IPv6 zone identifiers are not permitted", errMCPHTTPDestinationDenied)
			}
			addr, ok := netip.AddrFromSlice(resolved.IP)
			if !ok {
				return nil, fmt.Errorf("%w: invalid resolved address", errMCPHTTPDestinationDenied)
			}
			addr = addr.Unmap()
			if mcpHTTPAddressDenied(addr) && !mcpHTTPPrefixAllowed(addr, allowed) {
				return nil, fmt.Errorf("%w: %s is a private, loopback, link-local or reserved destination", errMCPHTTPDestinationDenied, addr)
			}
			permitted = append(permitted, addr)
		}
		if len(permitted) == 0 {
			return nil, fmt.Errorf("%w: no permitted destination address", errMCPHTTPDestinationDenied)
		}
		var lastErr error
		for _, addr := range permitted {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func mcpHTTPAddressDenied(addr netip.Addr) bool {
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range mcpHTTPReservedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func mcpHTTPPrefixAllowed(addr netip.Addr, allowed []netip.Prefix) bool {
	for _, prefix := range allowed {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type mcpHTTPRoundTripper struct {
	base     http.RoundTripper
	endpoint *url.URL
	token    string
	maxBody  int64
}

func (rt *mcpHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || !mcpHTTPURLMatches(req.URL, rt.endpoint) {
		return nil, fmt.Errorf("MCP HTTP request does not match the approved endpoint")
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	clone.Header.Del("Authorization")
	clone.Header.Del("Proxy-Authorization")
	clone.Header.Del("Cookie")
	if rt.token != "" {
		clone.Header.Set("Authorization", "Bearer "+rt.token)
	}
	resp, err := rt.base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, errMCPHTTPRedirect
	}
	if resp.ContentLength > rt.maxBody {
		_ = resp.Body.Close()
		return nil, errMCPHTTPResponseTooLarge
	}
	maxEvents := 0
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		maxEvents = mcpHTTPMaxEvents
	}
	resp.Body = &mcpHTTPBodyLimit{body: resp.Body, remaining: rt.maxBody, maxEvents: maxEvents}
	return resp, nil
}

func mcpHTTPURLMatches(got, approved *url.URL) bool {
	if got == nil || approved == nil {
		return false
	}
	if got.Scheme != approved.Scheme || !strings.EqualFold(got.Host, approved.Host) {
		return false
	}
	if got.User != nil || approved.User != nil || got.Fragment != "" || got.RawQuery != "" || got.ForceQuery || approved.ForceQuery {
		return false
	}
	return got.EscapedPath() == approved.EscapedPath()
}

type mcpHTTPBodyLimit struct {
	body      io.ReadCloser
	remaining int64
	maxEvents int
	events    int
	lastByte  byte
	prevByte  byte
}

func (b *mcpHTTPBodyLimit) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errMCPHTTPResponseTooLarge
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	if b.maxEvents > 0 {
		for _, c := range p[:n] {
			if c == '\n' && (b.lastByte == '\n' || (b.lastByte == '\r' && b.prevByte == '\n')) {
				b.events++
				if b.events > b.maxEvents {
					return n, errMCPHTTPTooManyEvents
				}
			}
			b.prevByte, b.lastByte = b.lastByte, c
		}
	}
	return n, err
}

func (b *mcpHTTPBodyLimit) Close() error { return b.body.Close() }
