package crawler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

type recordingRotator struct {
	mu       sync.Mutex
	calls    int
	requests []proxycontrol.RotateRequest
	response proxycontrol.RotateResponse
	err      error
	onRotate func()
}

type recordingCDPRotator struct {
	mu       sync.Mutex
	calls    int
	apiURLs  []string
	requests []proxycontrol.RotateRequest
	response proxycontrol.RotateResponse
	err      error
	onRotate func()
}

func (r *recordingCDPRotator) Rotate(_ context.Context, apiURL string, request proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error) {
	r.mu.Lock()
	r.calls++
	r.apiURLs = append(r.apiURLs, apiURL)
	r.requests = append(r.requests, request)
	onRotate := r.onRotate
	r.mu.Unlock()
	if onRotate != nil {
		onRotate()
	}
	if r.err != nil {
		return proxycontrol.RotateResponse{}, r.err
	}
	return r.response, nil
}

func (r *recordingCDPRotator) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRotator) Rotate(_ context.Context, request proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error) {
	r.mu.Lock()
	r.calls++
	r.requests = append(r.requests, request)
	onRotate := r.onRotate
	r.mu.Unlock()
	if onRotate != nil {
		onRotate()
	}
	if r.err != nil {
		return proxycontrol.RotateResponse{}, r.err
	}
	return r.response, nil
}

func (r *recordingRotator) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func stickyTestContext(t *testing.T) (*ProcessContext, vdi.SeleniumInstance) {
	t.Helper()
	stickyProfile := cfg.StickySessionConfig{
		ProxyURL: "http://microproxy-vdi1:8081",
		APIURL:   "http://microproxy-vdi1:8094",
		TokenEnv: "MICROPROXY_VDI1_SESSION_API_KEY",
		Provider: "residential-sticky",
	}
	sel := vdi.SeleniumInstance{Config: cfg.Selenium{
		Name:          "vdi-1",
		Type:          "chrome",
		Host:          "selenium-vdi1",
		Port:          4444,
		ProxyURL:      "http://microproxy-vdi1:8080",
		StickySession: stickyProfile,
	}}
	crawlCtx, crawlCancel := context.WithCancel(context.Background())
	ctx := &ProcessContext{
		source:      &cdb.Source{ID: 42, URL: "https://example.test/"},
		config:      cfg.Config{Selenium: []cfg.Selenium{sel.Config}},
		crawlCtx:    crawlCtx,
		crawlCancel: crawlCancel,
	}
	t.Cleanup(crawlCancel)
	ctx.config.Crawler.Workers = 1
	return ctx, sel
}

func stickySourceConfig() cfg.SourceConfig {
	enabled := true
	return cfg.SourceConfig{CrawlingConfig: cfg.CrawlingConfig{StickySession: &enabled}}
}

func TestStartBrowserCrawlNonStickySkipsRotation(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{}
	cdp := &recordingCDPRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyCDPRotator = cdp
	ctx.stickyGetenv = func(string) string { return "token" }

	connected := false
	err := ctx.startBrowserCrawl(sel, cfg.SourceConfig{}, nil, func() error {
		connected = true
		return nil
	})
	if err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if rotator.count() != 0 || cdp.count() != 0 {
		t.Fatalf("non-sticky source performed direct=%d cdp=%d rotations", rotator.count(), cdp.count())
	}
	if !connected {
		t.Fatal("browser connect callback was not invoked")
	}
	if got, ok := ctx.GetEffectiveProxy(); !ok || got != "http://microproxy-vdi1:8080" {
		t.Fatalf("effective proxy = (%q,%t), want the VDI default", got, ok)
	}
}

func TestStartBrowserCrawlStickyRotatesBeforeConnect(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	var order []string
	var mu sync.Mutex
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	rotator := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 3},
		onRotate: func() { record("rotate") },
	}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(name string) string {
		if name != "MICROPROXY_VDI1_SESSION_API_KEY" {
			t.Errorf("unexpected token environment name %q", name)
		}
		return "secret-token"
	}
	ctx.stickyKeyGenerate = func() (string, error) { return "test-idempotency-key", nil }

	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		record("connect")
		return nil
	})
	if err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	mu.Lock()
	gotOrder := append([]string(nil), order...)
	mu.Unlock()
	if len(gotOrder) != 2 || gotOrder[0] != "rotate" || gotOrder[1] != "connect" {
		t.Fatalf("order = %v, want [rotate connect]", gotOrder)
	}
	if rotator.count() != 1 {
		t.Fatalf("rotations = %d, want 1", rotator.count())
	}
	request := rotator.requests[0]
	if request.APIURL != "http://microproxy-vdi1:8094" ||
		request.Token != "secret-token" ||
		request.IdempotencyKey != "test-idempotency-key" ||
		request.Provider != "residential-sticky" {
		t.Fatalf("request = %+v", request)
	}
	if got, ok := ctx.GetEffectiveProxy(); !ok || got != "http://microproxy-vdi1:8081" {
		t.Fatalf("effective proxy = (%q,%t), want the sticky listener", got, ok)
	}
}

func TestStartBrowserCrawlWithRealRotatorHTTPClient(t *testing.T) {
	var (
		mu     sync.Mutex
		path   string
		auth   string
		key    string
		called bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		key = r.Header.Get("Idempotency-Key")
		called = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"rotated","provider":"residential-sticky","generation":4}`)
	}))
	defer server.Close()

	ctx, sel := stickyTestContext(t)
	sel.Config.StickySession.APIURL = server.URL
	ctx.config.Selenium[0] = sel.Config
	ctx.stickyRotator = proxycontrol.NewRotator()
	t.Setenv("MICROPROXY_VDI1_SESSION_API_KEY", "real-client-token")
	ctx.stickyKeyGenerate = func() (string, error) { return "real-client-key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if !connected {
		t.Fatal("browser connect callback was not invoked")
	}
	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("microproxy HTTP fixture was not called")
	}
	if path != proxycontrol.RotatePath {
		t.Fatalf("path = %q", path)
	}
	if auth != "Bearer real-client-token" {
		t.Fatalf("authorization = %q", auth)
	}
	if key != "real-client-key" {
		t.Fatalf("idempotency key = %q", key)
	}
}

func TestStartBrowserCrawlCDPSuccessSkipsDirect(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	// The engine-direct origin is deliberately unreachable in this test: a
	// validated CDP success must not need it.
	sel.Config.StickySession.APIURL = "http://127.0.0.1:1"
	sel.Config.StickySession.VDIAPIURL = "http://127.0.0.1:8094"
	ctx.config.Selenium[0] = sel.Config
	cdp := &recordingCDPRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 5},
	}
	direct := &recordingRotator{}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "secret-token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "cdp-key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if !connected {
		t.Fatal("browser connect callback was not invoked")
	}
	if cdp.count() != 1 || direct.count() != 0 {
		t.Fatalf("cdp=%d direct=%d, want 1/0", cdp.count(), direct.count())
	}
	request := cdp.requests[0]
	if request.APIURL != "http://127.0.0.1:8094" || cdp.apiURLs[0] != "http://127.0.0.1:8094" {
		t.Fatalf("CDP API URL = %q / %q", request.APIURL, cdp.apiURLs[0])
	}
	if request.Token != "secret-token" || request.IdempotencyKey != "cdp-key" || request.Provider != "residential-sticky" {
		t.Fatalf("CDP request = %+v", request)
	}
}

func TestStartBrowserCrawlCDPUnavailableFallsBackToEngineURL(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	sel.Config.StickySession.APIURL = "http://microproxy-vdi1:8094"
	sel.Config.StickySession.VDIAPIURL = "http://127.0.0.1:8094"
	ctx.config.Selenium[0] = sel.Config
	cdp := &recordingCDPRotator{err: fmt.Errorf("%w: bootstrap failed", vdi.ErrCDPStickyUnavailable)}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 2},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "secret-token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "same-key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if !connected {
		t.Fatal("browser connect callback was not invoked")
	}
	if cdp.count() != 1 || direct.count() != 1 {
		t.Fatalf("cdp=%d direct=%d, want 1/1", cdp.count(), direct.count())
	}
	if cdp.apiURLs[0] != "http://127.0.0.1:8094" {
		t.Fatalf("CDP origin = %q, want vdi_api_url", cdp.apiURLs[0])
	}
	request := direct.requests[0]
	if request.APIURL != "http://microproxy-vdi1:8094" {
		t.Fatalf("direct fallback API URL = %q", request.APIURL)
	}
	if request.Token != "secret-token" || request.IdempotencyKey != "same-key" || request.Provider != "residential-sticky" {
		t.Fatalf("fallback request = %+v", request)
	}
}

func TestStartBrowserCrawlCDPUncertainFallbackReusesSameKey(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{err: fmt.Errorf("%w: response lost", vdi.ErrCDPStickyUncertain)}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{
			Status: "rotated", Provider: "residential-sticky", Generation: 3, Replayed: true,
		},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "secret-token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "replay-key", nil }

	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error { return nil }); err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if cdp.count() != 1 || direct.count() != 1 {
		t.Fatalf("cdp=%d direct=%d, want 1/1", cdp.count(), direct.count())
	}
	if cdp.requests[0].IdempotencyKey != "replay-key" || direct.requests[0].IdempotencyKey != "replay-key" {
		t.Fatalf("idempotency key changed: cdp=%q direct=%q",
			cdp.requests[0].IdempotencyKey, direct.requests[0].IdempotencyKey)
	}
	if cdp.apiURLs[0] != "http://microproxy-vdi1:8094" {
		t.Fatalf("CDP default origin = %q, want api_url", cdp.apiURLs[0])
	}
}

func TestStartBrowserCrawlCDPDefinitiveFailureDoesNotFallback(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{err: &proxycontrol.Error{Kind: proxycontrol.FailureAuth, HTTPStatus: 401}}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected definitive CDP failure")
	}
	if direct.count() != 0 {
		t.Fatal("definitive API rejection triggered a second transport")
	}
	if connected {
		t.Fatal("browser started after definitive rejection")
	}
}

func TestStartBrowserCrawlCDPUnsafeCleanupQuarantinesWithoutFallback(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{err: fmt.Errorf("%w: quit failed", vdi.ErrCDPStickyUnsafeCleanup)}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected unsafe cleanup failure")
	}
	if direct.count() != 0 || connected {
		t.Fatal("unsafe cleanup must not fall back or start the source browser")
	}
	if !ctx.sessionIsQuarantined() {
		t.Fatal("VDI session was not quarantined after unsafe cleanup")
	}
}

func TestStartBrowserCrawlCDPUnknownFailureDoesNotFallback(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{err: errors.New("unexpected CDP failure")}
	direct := &recordingRotator{}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		t.Fatal("connect must not run")
		return nil
	}); err == nil {
		t.Fatal("expected unknown CDP failure")
	}
	if direct.count() != 0 {
		t.Fatal("unknown CDP failure was silently treated as fallback")
	}
}

func TestStartBrowserCrawlCDPCancellationDoesNotFallback(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{
		err:      fmt.Errorf("%w: source cancelled", vdi.ErrCDPStickyUncertain),
		onRotate: func() { ctx.crawlCancel() },
	}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	connected := false
	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	}); err == nil {
		t.Fatal("expected cancellation/fence failure")
	}
	if direct.count() != 0 || connected {
		t.Fatal("cancelled CDP attempt fell back or started the source browser")
	}
}

func TestStartBrowserCrawlCDPSuccessThenCancellationBlocksSourceBrowser(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	cdp := &recordingCDPRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
		onRotate: func() { ctx.crawlCancel() },
	}
	direct := &recordingRotator{}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	connected := false
	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	}); !errors.Is(err, errCrawlFenced) {
		t.Fatalf("error = %v, want errCrawlFenced", err)
	}
	if connected {
		t.Fatal("source browser started after cancellation following a 200 rotation")
	}
	if direct.count() != 0 {
		t.Fatal("successful CDP rotation triggered direct fallback")
	}
}

func TestStartBrowserCrawlFirefoxSkipsCDP(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	sel.Config.Type = "firefox"
	ctx.config.Selenium[0] = sel.Config
	cdp := &recordingCDPRotator{}
	direct := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyCDPRotator = cdp
	ctx.stickyRotator = direct
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error { return nil }); err != nil {
		t.Fatalf("startBrowserCrawl: %v", err)
	}
	if cdp.count() != 0 || direct.count() != 1 {
		t.Fatalf("cdp=%d direct=%d, want 0/1", cdp.count(), direct.count())
	}
}

func TestStartBrowserCrawlRotationFailureBlocksConnect(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{err: &proxycontrol.Error{Kind: proxycontrol.FailureAuth, HTTPStatus: 401}}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "secret-token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "test-key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected rotation failure")
	}
	if connected {
		t.Fatal("browser connect callback ran after a failed rotation")
	}
	if !strings.Contains(err.Error(), "auth_failure") {
		t.Fatalf("error = %v, want sanitized auth_failure category", err)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("error leaked credential material: %v", err)
	}
}

func TestStartBrowserCrawlMissingTokenBlocksBeforeAPI(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "" }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "MICROPROXY_VDI1_SESSION_API_KEY") {
		t.Fatalf("error = %v, want missing token environment name", err)
	}
	if rotator.count() != 0 {
		t.Fatalf("rotation attempted without a token: %d", rotator.count())
	}
	if connected {
		t.Fatal("browser connect callback ran without a token")
	}
}

func TestStartBrowserCrawlStickyProxyConflictBlocksBeforeRotation(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	source := stickySourceConfig()
	source.CrawlingConfig.Proxy = "http://microproxy-vdi1:8080"

	connected := false
	err := ctx.startBrowserCrawl(sel, source, nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected a conflicting proxy error")
	}
	if rotator.count() != 0 {
		t.Fatalf("rotation attempted with a conflicting proxy: %d", rotator.count())
	}
	if connected {
		t.Fatal("browser connect callback ran with a conflicting proxy")
	}
}

func TestStartBrowserCrawlFirefoxHTTPSStickyProfileFailsBeforeRotation(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	sel.Config.Type = "firefox"
	sel.Config.StickySession.ProxyURL = "https://microproxy-vdi1:8081"
	ctx.config.Selenium[0] = sel.Config
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected Firefox HTTPS sticky listener rejection")
	}
	if rotator.count() != 0 {
		t.Fatal("Firefox HTTPS listener must be rejected before rotation")
	}
	if connected {
		t.Fatal("browser connected after Firefox HTTPS listener rejection")
	}
}

func TestStartBrowserCrawlMalformedCustomStickyOverrideFailsClosed(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	source := stickySourceConfig()
	source.Custom = map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": 8081}},
	}

	connected := false
	err := ctx.startBrowserCrawl(sel, source, nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected a malformed custom.vdi override error")
	}
	if rotator.count() != 0 || connected {
		t.Fatal("malformed custom.vdi override must not rotate or start a browser")
	}
}

func TestStartBrowserCrawlStickyWithoutProfileFailsClosed(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	sel.Config.StickySession = cfg.StickySessionConfig{}
	ctx.config.Selenium[0].StickySession = cfg.StickySessionConfig{}
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected a missing sticky profile error")
	}
	if rotator.count() != 0 || connected {
		t.Fatal("missing profile must not rotate or start a browser")
	}
}

func TestStartBrowserCrawlAmbiguousVDIAssociationFailsClosed(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	ctx.config.Selenium = append(ctx.config.Selenium, ctx.config.Selenium[0])
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }

	if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		t.Fatal("connect must not run for an ambiguous VDI name")
		return nil
	}); err == nil {
		t.Fatal("expected an ambiguous VDI association error")
	}
	if rotator.count() != 0 {
		t.Fatal("ambiguous VDI association must not rotate")
	}
}

func TestStartBrowserCrawlCancelledBeforeRotation(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	crawlCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.crawlCtx = crawlCtx
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }

	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		t.Fatal("connect must not run for a cancelled crawl")
		return nil
	})
	if !errors.Is(err, errCrawlFenced) {
		t.Fatalf("error = %v, want errCrawlFenced", err)
	}
	if rotator.count() != 0 {
		t.Fatal("cancelled crawl must not rotate")
	}
}

type blockingRotator struct {
	started chan struct{}
}

func (b *blockingRotator) Rotate(ctx context.Context, _ proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error) {
	close(b.started)
	<-ctx.Done()
	return proxycontrol.RotateResponse{}, ctx.Err()
}

func TestStartBrowserCrawlCancellationDuringRotationAbortsBeforeBrowser(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &blockingRotator{started: make(chan struct{})}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	type result struct {
		err       error
		connected bool
	}
	done := make(chan result, 1)
	go func() {
		connected := false
		err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
			connected = true
			return nil
		})
		done <- result{err: err, connected: connected}
	}()

	<-rotator.started
	ctx.crawlCancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("expected a cancellation error")
		}
		if got.connected {
			t.Fatal("browser connected after in-flight cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rotation did not abort promptly")
	}
}

func TestStartBrowserCrawlFenceLossDuringRotationBlocksConnect(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
		onRotate: func() { ctx.abortCrawl("superseded_during_rotation") },
	}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if !errors.Is(err, errCrawlFenced) {
		t.Fatalf("error = %v, want errCrawlFenced", err)
	}
	if connected {
		t.Fatal("browser started after the fence was lost during rotation")
	}
}

func TestStartBrowserCrawlStaleClaimBeforeRotationBlocksAPI(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
	var db cdb.Handler
	ctx.db = &db
	ctx.sourceCrawl = &orderSourceCrawlCoordinator{
		status: cdb.SourceCrawlFenceStatus{Result: cdb.SourceCrawlFenceStale, Reason: "superseded"},
	}

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected a stale-claim error")
	}
	if rotator.count() != 0 {
		t.Fatalf("stale attempt performed %d rotations", rotator.count())
	}
	if connected {
		t.Fatal("stale attempt started a browser")
	}
}

func TestStartBrowserCrawlPostRotationClaimValidationFailureBlocksConnect(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
	var db cdb.Handler
	ctx.db = &db
	coordinator := &orderSourceCrawlCoordinator{
		status: cdb.SourceCrawlFenceStatus{Result: cdb.SourceCrawlFenceStale, Reason: "superseded"},
	}
	coordinator.statuses = []cdb.SourceCrawlFenceStatus{
		{Result: cdb.SourceCrawlFenceActive},
		{Result: cdb.SourceCrawlFenceStale, Reason: "superseded"},
	}
	ctx.sourceCrawl = coordinator

	connected := false
	err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("expected claim validation failure")
	}
	if connected {
		t.Fatal("browser started after the claim was superseded")
	}
	if coordinator.validateCalls != 2 {
		t.Fatalf("validate calls = %d, want 2", coordinator.validateCalls)
	}
	if rotator.count() != 1 {
		t.Fatalf("rotations = %d, want 1", rotator.count())
	}
}

func TestStartBrowserCrawlCustomVDIProxyProvenanceAcceptsMatchingStickyListener(t *testing.T) {
	ctx, sel := stickyTestContext(t)
	rotator := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	ctx.stickyRotator = rotator
	ctx.stickyGetenv = func(string) string { return "token" }
	ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
	source := stickySourceConfig()
	source.Custom = map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": "http://microproxy-vdi1:8081"}},
	}

	if err := ctx.startBrowserCrawl(sel, source, nil, func() error { return nil }); err != nil {
		t.Fatalf("matching custom.vdi sticky listener rejected: %v", err)
	}
	if rotator.count() != 1 {
		t.Fatalf("rotations = %d, want 1", rotator.count())
	}
}

func TestStickySourceProtocolValidation(t *testing.T) {
	if err := validateStickySourceProtocol(true, SourceProtocolWeb); err != nil {
		t.Fatalf("web source rejected: %v", err)
	}
	for _, protocol := range []SourceProtocol{SourceProtocolEmail, SourceProtocolNetwork} {
		if err := validateStickySourceProtocol(true, protocol); err == nil {
			t.Fatalf("%s source accepted", protocol)
		}
	}
	for _, protocol := range []SourceProtocol{SourceProtocolWeb, SourceProtocolEmail, SourceProtocolNetwork} {
		if err := validateStickySourceProtocol(false, protocol); err != nil {
			t.Fatalf("non-sticky %s source rejected: %v", protocol, err)
		}
	}
}

func TestStickyRotationUsesAcquiredVDILease(t *testing.T) {
	// Slot serialization itself is covered by pkg/vdi pool tests. This proves
	// the source boundary consumes the acquired VDI and can rotate again only
	// after the previous lease has been released.
	pool := vdi.NewPool(1)
	_, profile := stickyTestContext(t)
	if err := pool.Add(profile); err != nil {
		t.Fatal(err)
	}
	rotator := &recordingRotator{
		response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
	}
	for i := 0; i < 2; i++ {
		lease, instance, err := pool.AcquireContext(context.Background(), "vdi-1")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		ctx, _ := stickyTestContext(t)
		ctx.config.Selenium = []cfg.Selenium{instance.Config}
		ctx.stickyRotator = rotator
		ctx.stickyGetenv = func(string) string { return "token" }
		ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
		if err := ctx.startBrowserCrawl(instance, stickySourceConfig(), nil, func() error { return nil }); err != nil {
			t.Fatalf("crawl %d: %v", i, err)
		}
		if err := lease.Release(); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
	if rotator.count() != 2 {
		t.Fatalf("rotations = %d, want 2 source boundaries", rotator.count())
	}
}

func TestStickyRotationUsesPerVDIProfile(t *testing.T) {
	pool := vdi.NewPool(2)
	profiles := []cfg.Selenium{
		{
			Name: "vdi-1", Type: "chrome", Host: "selenium-1", Port: 4444,
			StickySession: cfg.StickySessionConfig{
				ProxyURL: "http://microproxy-1:8081", APIURL: "http://microproxy-1:8094",
				TokenEnv: "TOKEN_1", Provider: "provider-1",
			},
		},
		{
			Name: "vdi-2", Type: "chrome", Host: "selenium-2", Port: 4445,
			StickySession: cfg.StickySessionConfig{
				ProxyURL: "http://microproxy-2:8081", APIURL: "http://microproxy-2:8094",
				TokenEnv: "TOKEN_2", Provider: "provider-2",
			},
		},
	}
	for _, profile := range profiles {
		if err := pool.Add(vdi.SeleniumInstance{Config: profile}); err != nil {
			t.Fatal(err)
		}
	}
	rotators := []*recordingRotator{
		{response: proxycontrol.RotateResponse{Status: "rotated", Provider: "provider-1", Generation: 1}},
		{response: proxycontrol.RotateResponse{Status: "rotated", Provider: "provider-2", Generation: 1}},
	}
	leases := make([]*vdi.Lease, 0, 2)
	contexts := make([]*ProcessContext, 0, 2)
	instances := make([]vdi.SeleniumInstance, 0, 2)
	for i, name := range []string{"vdi-1", "vdi-2"} {
		lease, instance, err := pool.AcquireContext(context.Background(), name)
		if err != nil {
			t.Fatalf("acquire %s: %v", name, err)
		}
		leases = append(leases, lease)
		instances = append(instances, instance)
		ctx, _ := stickyTestContext(t)
		ctx.config.Selenium = profiles
		ctx.stickyRotator = rotators[i]
		token := profiles[i].StickySession.TokenEnv
		ctx.stickyGetenv = func(name string) string {
			if name != token {
				t.Errorf("token env = %q, want %q", name, token)
			}
			return "token"
		}
		ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
		contexts = append(contexts, ctx)
	}
	for i := range contexts {
		if err := contexts[i].startBrowserCrawl(instances[i], stickySourceConfig(), nil, func() error { return nil }); err != nil {
			t.Fatalf("crawl %d: %v", i, err)
		}
	}
	if got := rotators[0].requests[0].APIURL; got != "http://microproxy-1:8094" {
		t.Fatalf("vdi-1 API = %q", got)
	}
	if got := rotators[1].requests[0].APIURL; got != "http://microproxy-2:8094" {
		t.Fatalf("vdi-2 API = %q", got)
	}
	for _, lease := range leases {
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStickySourceBoundaryRejectsNonBrowserSite(t *testing.T) {
	web := cfg.CrawlingConfig{Site: "https://example.test/", SourceType: "website"}
	if err := validateStickySourceBoundary(true, SourceProtocolWeb, web); err != nil {
		t.Fatalf("browser-backed sticky source rejected: %v", err)
	}
	for _, site := range []string{
		"imaps://mail.example.test:993",
		"maildir:///var/mail/archive",
		"null://example.test",
		"",
	} {
		crawling := web
		crawling.Site = site
		if err := validateStickySourceBoundary(true, SourceProtocolWeb, crawling); err == nil {
			t.Errorf("site %q accepted for sticky_session", site)
		}
	}
	emailType := web
	emailType.SourceType = "email"
	if err := validateStickySourceBoundary(true, SourceProtocolWeb, emailType); err == nil {
		t.Fatal("email source_type accepted for sticky_session")
	}
	if err := validateStickySourceBoundary(false, SourceProtocolWeb, cfg.CrawlingConfig{
		Site: "imaps://mail.example.test:993", SourceType: "email",
	}); err != nil {
		t.Fatalf("non-sticky source site was rejected: %v", err)
	}
}

func TestStartBrowserCrawlConcurrentContextsAreIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, sel := stickyTestContext(t)
			rotator := &recordingRotator{
				response: proxycontrol.RotateResponse{Status: "rotated", Provider: "residential-sticky", Generation: 1},
			}
			ctx.stickyRotator = rotator
			ctx.stickyGetenv = func(string) string { return "token" }
			ctx.stickyKeyGenerate = func() (string, error) { return "key", nil }
			if err := ctx.startBrowserCrawl(sel, stickySourceConfig(), nil, func() error { return nil }); err != nil {
				t.Errorf("startBrowserCrawl: %v", err)
				return
			}
			if rotator.count() != 1 {
				t.Errorf("rotations = %d, want 1", rotator.count())
			}
			if got, ok := ctx.GetEffectiveProxy(); !ok || got != "http://microproxy-vdi1:8081" {
				t.Errorf("effective proxy = (%q,%t)", got, ok)
			}
		}()
	}
	wg.Wait()
}

// orderSourceCrawlCoordinator is a minimal fence validator for ordering tests.
type orderSourceCrawlCoordinator struct {
	status        cdb.SourceCrawlFenceStatus
	statuses      []cdb.SourceCrawlFenceStatus
	err           error
	validateCalls int
}

func (f *orderSourceCrawlCoordinator) SourceID() uint64     { return 42 }
func (f *orderSourceCrawlCoordinator) AttemptID() string    { return "attempt-1" }
func (f *orderSourceCrawlCoordinator) Generation() uint64   { return 1 }
func (f *orderSourceCrawlCoordinator) ClaimedAt() time.Time { return time.Now() }
func (f *orderSourceCrawlCoordinator) LeaseExpiresAt() time.Time {
	return time.Now().Add(time.Hour)
}
func (f *orderSourceCrawlCoordinator) Bound() bool { return true }
func (f *orderSourceCrawlCoordinator) RenewLease(context.Context, *cdb.Handler, time.Duration) (cdb.SourceCrawlLeaseResult, error) {
	return cdb.SourceCrawlLeaseResult{Result: cdb.SourceCrawlResultApplied}, nil
}
func (f *orderSourceCrawlCoordinator) ReportTerminal(context.Context, *cdb.Handler, cdb.SourceCrawlTerminalOutcome, time.Time) (cdb.SourceCrawlTerminalResult, error) {
	return cdb.SourceCrawlTerminalResult{Result: cdb.SourceCrawlResultApplied}, nil
}
func (f *orderSourceCrawlCoordinator) ValidateCurrent(context.Context, *cdb.Handler) (cdb.SourceCrawlFenceStatus, error) {
	status := f.status
	if len(f.statuses) > 0 {
		index := f.validateCalls
		if index >= len(f.statuses) {
			index = len(f.statuses) - 1
		}
		status = f.statuses[index]
	}
	f.validateCalls++
	return status, f.err
}
func (f *orderSourceCrawlCoordinator) ValidateTx(context.Context, *sql.Tx, *cdb.Handler) error {
	return nil
}
