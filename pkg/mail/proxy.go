package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/oauth2"
)

func contextWithMailProxy(ctx context.Context, rawProxyURL string) (context.Context, error) {
	proxyURL, err := parseMailProxyURL(rawProxyURL)
	if err != nil || proxyURL == nil {
		return ctx, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL.Scheme == "socks5" {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialMailProxyContext(ctx, proxyURL, 30*time.Second, network, address)
		}
	} else {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client := &http.Client{Transport: transport}
	return context.WithValue(ctx, oauth2.HTTPClient, client), nil
}

func dialMailContext(ctx context.Context, rawProxyURL string, timeout time.Duration, network, address string) (net.Conn, error) {
	proxyURL, err := parseMailProxyURL(rawProxyURL)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, address)
	}
	return dialMailProxyContext(ctx, proxyURL, timeout, network, address)
}

func dialMailProxyContext(ctx context.Context, proxyURL *url.URL, timeout time.Duration, network, address string) (net.Conn, error) {
	switch proxyURL.Scheme {
	case "socks5":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyAddress(proxyURL), auth, &net.Dialer{Timeout: timeout})
		if err != nil {
			return nil, fmt.Errorf("mail: configure SOCKS5 proxy: %w", err)
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			return contextDialer.DialContext(ctx, network, address)
		}
		return dialWithContext(ctx, dialer, network, address)
	case "http", "https":
		return dialHTTPConnectProxy(ctx, proxyURL, timeout, address)
	default:
		return nil, fmt.Errorf("mail: unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func dialHTTPConnectProxy(ctx context.Context, proxyURL *url.URL, timeout time.Duration, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddress(proxyURL))
	if err != nil {
		return nil, fmt.Errorf("mail: connect to proxy: %w", err)
	}
	setupDeadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(setupDeadline) {
		setupDeadline = contextDeadline
	}
	if err := conn.SetDeadline(setupDeadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mail: set proxy setup deadline: %w", err)
	}
	if proxyURL.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: proxyURL.Hostname()})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("mail: establish TLS with proxy: %w", err)
		}
		conn = tlsConn
	}

	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: address},
		Host:   address,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credential := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
		request.Header.Set("Proxy-Authorization", "Basic "+credential)
	}
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mail: write proxy CONNECT request: %w", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mail: read proxy CONNECT response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		_ = conn.Close()
		return nil, fmt.Errorf("mail: proxy CONNECT failed with status %s", response.Status)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mail: clear proxy setup deadline: %w", err)
	}
	if reader.Buffered() > 0 {
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (conn *bufferedProxyConn) Read(buffer []byte) (int, error) {
	return conn.reader.Read(buffer)
}

func dialWithContext(ctx context.Context, dialer proxy.Dialer, network, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	resultChannel := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial(network, address)
		resultChannel <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			result := <-resultChannel
			if result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case result := <-resultChannel:
		return result.conn, result.err
	}
}

func parseMailProxyURL(rawProxyURL string) (*url.URL, error) {
	rawProxyURL = strings.TrimSpace(rawProxyURL)
	if rawProxyURL == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(rawProxyURL)
	if err != nil {
		return nil, fmt.Errorf("mail: parse proxy URL: %w", err)
	}
	proxyURL.Scheme = strings.ToLower(proxyURL.Scheme)
	switch proxyURL.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("mail: unsupported proxy scheme %q", proxyURL.Scheme)
	}
	if proxyURL.Host == "" || proxyURL.Hostname() == "" {
		return nil, errors.New("mail: proxy URL requires a host")
	}
	if proxyURL.Path != "" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return nil, errors.New("mail: proxy URL must not contain a path, query, or fragment")
	}
	if proxyURL.Port() != "" {
		port, err := strconv.Atoi(proxyURL.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("mail: proxy URL contains an invalid port")
		}
	}
	return proxyURL, nil
}

func proxyAddress(proxyURL *url.URL) string {
	if proxyURL.Port() != "" {
		return proxyURL.Host
	}
	port := "80"
	if proxyURL.Scheme == "https" {
		port = "443"
	} else if proxyURL.Scheme == "socks5" {
		port = "1080"
	}
	return net.JoinHostPort(proxyURL.Hostname(), port)
}
