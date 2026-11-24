//go:build integration

package crawler

// Opt-in real-Selenium tests for the CDP-first source boundary fallback.
//
// THECROWLER_E2E_WEBDRIVER=1 THECROWLER_E2E_WEBDRIVER_URL=http://127.0.0.1:4444/wd/hub \
//   go test -tags=integration ./pkg/crawler -run TestStickyCDP -v

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

func integrationSeleniumInstance(t *testing.T) vdi.SeleniumInstance {
	t.Helper()
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}
	parsed, err := url.Parse(webdriverURL)
	if err != nil {
		t.Fatalf("parse THECROWLER_E2E_WEBDRIVER_URL: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("THECROWLER_E2E_WEBDRIVER_URL must contain a host and port")
	}
	sslMode := "disable"
	if strings.EqualFold(parsed.Scheme, "https") {
		sslMode = "enable"
	}
	return vdi.SeleniumInstance{Config: cfg.Selenium{
		Name: "e2e-vdi", Type: "chrome", Host: parsed.Hostname(), Port: port, SSLMode: sslMode,
	}}
}

func integrationRotationContext(sel vdi.SeleniumInstance, apiURL, vdiAPIURL string) (*ProcessContext, vdi.SeleniumInstance) {
	crawlCtx, crawlCancel := context.WithCancel(context.Background())
	sel.Config.StickySession = cfg.StickySessionConfig{
		ProxyURL:  "http://127.0.0.1:8081",
		APIURL:    apiURL,
		VDIAPIURL: vdiAPIURL,
		TokenEnv:  "CROWLER_E2E_STICKY_TOKEN",
		Provider:  "e2e-provider",
	}
	return &ProcessContext{
		source:            &cdb.Source{ID: 1, URL: "https://example.test/"},
		config:            cfg.Config{Selenium: []cfg.Selenium{sel.Config}},
		crawlCtx:          crawlCtx,
		crawlCancel:       crawlCancel,
		stickyRotator:     proxycontrol.NewRotator(),
		stickyCDPRotator:  &recordingRealCDPRotator{inner: vdi.NewCDPStickyRotator(sel)},
		stickyGetenv:      func(string) string { return "e2e-token" },
		stickyKeyGenerate: func() (string, error) { return "e2e-fallback-key", nil },
	}, sel
}

func TestStickyCDPFallbackToEngineDirectIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the real-Selenium fallback test")
	}
	var (
		mu   sync.Mutex
		auth string
		key  string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != proxycontrol.RotatePath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		auth = r.Header.Get("Authorization")
		key = r.Header.Get("Idempotency-Key")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"rotated","provider":"e2e-provider","generation":2,"replayed":true}`)
	}))
	defer server.Close()

	sel := integrationSeleniumInstance(t)
	ctx, sel := integrationRotationContext(sel, server.URL, "http://127.0.0.1:1")
	defer ctx.crawlCancel()
	enabled := true
	sourceCfg := cfg.SourceConfig{CrawlingConfig: cfg.CrawlingConfig{StickySession: &enabled}}

	connected := false
	err := ctx.startBrowserCrawl(sel, sourceCfg, nil, func() error {
		connected = true
		return nil
	})
	if err != nil {
		t.Fatalf("fallback source boundary failed: %v", err)
	}
	if !connected {
		t.Fatal("source browser connection did not start after direct fallback")
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer e2e-token" || key != "e2e-fallback-key" {
		t.Fatalf("fallback request auth/key = %q / %q", auth, key)
	}
}

func TestStickyCDPBothTransportsInaccessibleIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the real-Selenium failure test")
	}
	sel := integrationSeleniumInstance(t)
	ctx, sel := integrationRotationContext(sel, "http://127.0.0.1:1", "http://127.0.0.1:1")
	defer ctx.crawlCancel()
	enabled := true
	sourceCfg := cfg.SourceConfig{CrawlingConfig: cfg.CrawlingConfig{StickySession: &enabled}}

	connected := false
	started := time.Now()
	err := ctx.startBrowserCrawl(sel, sourceCfg, nil, func() error {
		connected = true
		return nil
	})
	if err == nil {
		t.Fatal("both transports inaccessible should fail the source")
	}
	if connected {
		t.Fatal("source browser started without a validated rotation")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Minute {
		t.Fatalf("both-transports failure exceeded the rotation budget: %v", elapsed)
	}
}

type recordingRealCDPRotator struct {
	inner    *vdi.CDPStickyRotator
	response proxycontrol.RotateResponse
}

func (r *recordingRealCDPRotator) Rotate(ctx context.Context, apiURL string, request proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error) {
	response, err := r.inner.Rotate(ctx, apiURL, request)
	r.response = response
	return response, err
}

func TestStickyCDPGlobalDefaultAndNonStickyIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the global sticky default test")
	}
	apiURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_CDP_API_URL"))
	if apiURL == "" {
		t.Skip("set THECROWLER_E2E_CDP_API_URL to a fixture inside the VDI namespace")
	}
	expectedGeneration := int64(1)
	if raw := strings.TrimSpace(os.Getenv("THECROWLER_E2E_CDP_EXPECTED_GENERATION")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			t.Fatalf("THECROWLER_E2E_CDP_EXPECTED_GENERATION must be a positive integer")
		}
		expectedGeneration = value
	}

	sel := integrationSeleniumInstance(t)
	profile := cfg.StickySessionConfig{
		ProxyURL:  "http://127.0.0.1:8081",
		APIURL:    apiURL,
		VDIAPIURL: apiURL,
		TokenEnv:  "CROWLER_E2E_STICKY_TOKEN",
		Provider:  "e2e-provider",
	}
	sel.Config.StickySession = profile
	sel.Config.ProxyURL = profile.ProxyURL

	crawlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &ProcessContext{
		source:            &cdb.Source{ID: 1, URL: "https://example.test/"},
		config:            cfg.Config{Selenium: []cfg.Selenium{sel.Config}},
		crawlCtx:          crawlCtx,
		crawlCancel:       cancel,
		stickyRotator:     &recordingRotator{err: errors.New("engine-direct fallback must not run")},
		stickyCDPRotator:  &recordingRealCDPRotator{inner: vdi.NewCDPStickyRotator(sel)},
		stickyGetenv:      func(string) string { return "e2e-token" },
		stickyKeyGenerate: func() (string, error) { return "e2e-idempotency-key", nil },
	}
	connected := false
	if err := ctx.startBrowserCrawl(sel, cfg.SourceConfig{}, nil, func() error {
		connected = true
		return nil
	}); err != nil {
		t.Fatalf("global sticky default source: %v", err)
	}
	if !connected {
		t.Fatal("global sticky default did not connect the source browser")
	}
	if ctx.SelInstance.Config.StickySession.Provider != "e2e-provider" {
		t.Fatalf("effective profile = %+v", ctx.SelInstance.Config.StickySession)
	}
	if cdp, ok := ctx.stickyCDPRotator.(*recordingRealCDPRotator); !ok || cdp.response.Generation != expectedGeneration {
		t.Fatalf("CDP response = %+v, want generation %d", cdp, expectedGeneration)
	}

	// A second source on the same VDI config selects a conventional proxy. It
	// must not inherit the first source's sticky profile or rotate.
	secondCtx := &ProcessContext{
		source:      &cdb.Source{ID: 2, URL: "https://example.test/other"},
		config:      cfg.Config{Selenium: []cfg.Selenium{sel.Config}},
		crawlCtx:    crawlCtx,
		crawlCancel: cancel,
		stickyRotator: &recordingRotator{
			err: errors.New("non-sticky source must not rotate"),
		},
		stickyCDPRotator: vdi.NewCDPStickyRotator(sel),
	}
	source := cfg.SourceConfig{CrawlingConfig: cfg.CrawlingConfig{Proxy: "http://conventional-proxy:3128"}}
	if err := secondCtx.startBrowserCrawl(sel, source, nil, func() error {
		return nil
	}); err != nil {
		t.Fatalf("non-sticky source after sticky: %v", err)
	}
	if secondCtx.SelInstance.Config.StickySession != (cfg.StickySessionConfig{}) {
		t.Fatalf("non-sticky source inherited profile: %+v", secondCtx.SelInstance.Config.StickySession)
	}
	if got, ok := secondCtx.GetEffectiveProxy(); !ok || got != "http://conventional-proxy:3128" {
		t.Fatalf("non-sticky effective proxy = (%q,%t)", got, ok)
	}
}
