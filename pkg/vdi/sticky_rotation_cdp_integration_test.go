//go:build integration

package vdi

// Opt-in real Chrome/Selenium test for the CDP-first sticky rotation path.
//
// Run with a local browser (the fixture server is started by the test):
//   THECROWLER_E2E_WEBDRIVER=1 \
//   THECROWLER_E2E_WEBDRIVER_URL=http://127.0.0.1:4444/wd/hub \
//   go test -tags=integration ./pkg/vdi -run TestStickyCDPRotationIntegration -v
//
// THECROWLER_E2E_CDP_API_HOST selects a different address for the router. It
// must be reachable from the browser running in the VDI. For a local Selenium
// process, the default 127.0.0.1 is correct. For a VDI container on a private
// network, use the host bridge/gateway address; the test server binds 0.0.0.0
// and the Go test does not assume that the browser shares its namespace.
//
// Run a true VDI-local loopback check by starting the companion fixture inside
// the VDI namespace and pointing the test at it:
//   docker cp pkg/vdi/testdata/cdp_loopback_fixture.py vdi-container:/tmp/
//   docker exec -d vdi-container python3 /tmp/cdp_loopback_fixture.py \
//     --port 18094 --generation 7
//   THECROWLER_E2E_WEBDRIVER=1 \
//   THECROWLER_E2E_WEBDRIVER_URL=http://127.0.0.1:4444/wd/hub \
//   THECROWLER_E2E_CDP_API_URL=http://127.0.0.1:18094 \
//   THECROWLER_E2E_CDP_EXPECTED_GENERATION=7 \
//   go test -tags=integration ./pkg/vdi -run TestStickyCDPRotationIntegration -v
//
// The fixture returns 200 only when the browser supplied the expected bearer
// token and idempotency key, so a passing generation check also proves that the
// credentials reached the in-VDI loopback endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
)

func TestStickyCDPRotationIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the CDP rotation integration test")
	}
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}
	apiURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_CDP_API_URL"))
	expectedGeneration := int64(1)
	if rawGeneration := strings.TrimSpace(os.Getenv("THECROWLER_E2E_CDP_EXPECTED_GENERATION")); rawGeneration != "" {
		parsedGeneration, parseErr := strconv.ParseInt(rawGeneration, 10, 64)
		if parseErr != nil || parsedGeneration < 1 {
			t.Fatalf("THECROWLER_E2E_CDP_EXPECTED_GENERATION must be a positive integer")
		}
		expectedGeneration = parsedGeneration
	}

	var (
		mu           sync.Mutex
		called       bool
		auth         string
		key          string
		localFixture = apiURL == ""
		server       *http.Server
	)
	if localFixture {
		browserHost := strings.TrimSpace(os.Getenv("THECROWLER_E2E_CDP_API_HOST"))
		if browserHost == "" {
			browserHost = "127.0.0.1"
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == cdpBootstrapPath {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodPost && r.URL.Path == proxycontrol.RotatePath {
				mu.Lock()
				called = true
				auth = r.Header.Get("Authorization")
				key = r.Header.Get("Idempotency-Key")
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"status":"rotated","provider":"e2e-provider","generation":1}`)
				return
			}
			http.NotFound(w, r)
		})
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		server = &http.Server{Handler: handler}
		go func() { _ = server.Serve(listener) }()
		defer func() { _ = server.Close() }()
		_, portText, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		port, _ := strconv.Atoi(portText)
		apiURL = fmt.Sprintf("http://%s:%d", browserHost, port)
	}

	parsed, err := url.Parse(webdriverURL)
	if err != nil {
		t.Fatalf("parse THECROWLER_E2E_WEBDRIVER_URL: %v", err)
	}
	seleniumPort, err := strconv.Atoi(parsed.Port())
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("THECROWLER_E2E_WEBDRIVER_URL must contain a host and port")
	}
	sslMode := "disable"
	if strings.EqualFold(parsed.Scheme, "https") {
		sslMode = "enable"
	}
	sel := SeleniumInstance{Config: cfg.Selenium{
		Name: "e2e-vdi", Type: "chrome", Host: parsed.Hostname(), Port: seleniumPort, SSLMode: sslMode,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rotator := NewCDPStickyRotator(sel)
	response, err := rotator.Rotate(ctx, apiURL, proxycontrol.RotateRequest{
		APIURL:         apiURL,
		Token:          "e2e-token",
		IdempotencyKey: "e2e-idempotency-key",
		Provider:       "e2e-provider",
	})
	if err != nil {
		t.Fatalf("real CDP rotation failed at %s via Selenium %s: %v", apiURL, webdriverURL, err)
	}
	if response.Generation != expectedGeneration || response.Provider != "e2e-provider" {
		t.Fatalf("response = %+v, want provider e2e-provider generation %d", response, expectedGeneration)
	}
	if localFixture {
		mu.Lock()
		defer mu.Unlock()
		if !called {
			t.Fatalf("browser did not POST to %s (host may not be reachable from the VDI)", apiURL)
		}
		if auth != "Bearer e2e-token" {
			t.Fatalf("authorization = %q", auth)
		}
		if key != "e2e-idempotency-key" {
			t.Fatalf("idempotency key = %q", key)
		}
	}

	// The temporary browser must be gone before this method returned. The
	// Selenium Grid status does not expose a session inventory, so the
	// successful DELETE is verified by the implementation; this assertion
	// confirms the test fixture itself is still healthy after cleanup.
	statusURL := strings.TrimRight(webdriverURL, "/")
	if strings.Contains(statusURL, "/wd/hub") {
		statusURL = strings.TrimSuffix(statusURL, "/wd/hub") + "/status"
	}
	resp, err := http.Get(statusURL) //nolint:gosec // controlled test endpoint
	if err != nil {
		t.Fatalf("Selenium status after cleanup: %v", err)
	}
	defer resp.Body.Close()
	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode Selenium status: %v", err)
	}
}
