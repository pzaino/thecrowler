package mail

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestDialMailContextUsesHTTPConnectProxy(t *testing.T) {
	target := listenTCP(t)
	targetDone := make(chan error, 1)
	go func() {
		conn, err := target.Accept()
		if err != nil {
			targetDone <- err
			return
		}
		defer conn.Close()
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(conn, buffer); err != nil {
			targetDone <- err
			return
		}
		_, err = conn.Write(buffer)
		targetDone <- err
	}()

	proxyListener := listenTCP(t)
	connectRequest := make(chan *http.Request, 1)
	proxyDone := make(chan error, 1)
	go serveSingleConnectProxy(proxyListener, connectRequest, proxyDone)

	proxyURL := "http://reader:secret@" + proxyListener.Addr().String()
	conn, err := dialMailContext(context.Background(), proxyURL, time.Second, "tcp", target.Addr().String())
	if err != nil {
		t.Fatalf("dialMailContext() error = %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write tunneled connection: %v", err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read tunneled connection: %v", err)
	}
	if string(buffer) != "ping" {
		t.Fatalf("tunneled response = %q, want ping", buffer)
	}

	request := <-connectRequest
	if request.Method != http.MethodConnect || request.Host != target.Addr().String() {
		t.Fatalf("proxy request = %s %s, want CONNECT %s", request.Method, request.Host, target.Addr())
	}
	if authorization := request.Header.Get("Proxy-Authorization"); authorization != "Basic cmVhZGVyOnNlY3JldA==" {
		t.Fatalf("proxy authorization = %q", authorization)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close tunneled connection: %v", err)
	}
	if err := <-targetDone; err != nil {
		t.Fatalf("target server error: %v", err)
	}
	if err := <-proxyDone; err != nil {
		t.Fatalf("proxy server error: %v", err)
	}
}

func TestContextWithMailProxyConfiguresOAuthHTTPClient(t *testing.T) {
	ctx, err := contextWithMailProxy(context.Background(), "http://proxy.example.test:8080")
	if err != nil {
		t.Fatalf("contextWithMailProxy() error = %v", err)
	}
	client, ok := ctx.Value(oauth2.HTTPClient).(*http.Client)
	if !ok || client == nil {
		t.Fatal("OAuth HTTP client was not installed in context")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP transport type = %T", client.Transport)
	}
	proxyURL, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "mail.example.test"}})
	if err != nil {
		t.Fatalf("transport proxy lookup error = %v", err)
	}
	if proxyURL.String() != "http://proxy.example.test:8080" {
		t.Fatalf("transport proxy = %q", proxyURL)
	}
}

func listenTCP(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func serveSingleConnectProxy(listener net.Listener, requests chan<- *http.Request, done chan<- error) {
	client, err := listener.Accept()
	if err != nil {
		done <- err
		return
	}
	defer client.Close()
	request, err := http.ReadRequest(bufio.NewReader(client))
	if err != nil {
		done <- err
		return
	}
	requests <- request
	upstream, err := net.Dial("tcp", request.Host)
	if err != nil {
		done <- err
		return
	}
	defer upstream.Close()
	if _, err := fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		done <- err
		return
	}
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(upstream, client)
		copyDone <- err
	}()
	_, err = io.Copy(client, upstream)
	if err == nil {
		err = <-copyDone
	}
	done <- err
}
