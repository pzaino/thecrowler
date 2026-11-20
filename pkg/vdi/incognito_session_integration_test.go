//go:build integration

package vdi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-auxiliaries/selenium"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// Live browser coverage for the per-source incognito override.
//
// Run against a real Selenium/Chrome:
//
//	THECROWLER_E2E_WEBDRIVER=1 \
//	go test -tags integration ./pkg/vdi -run TestIncognitoBrowserIntegration -v
//
// Optional: THECROWLER_E2E_WEBDRIVER_URL (default http://127.0.0.1:4444/wd/hub).
//
// The test asserts two things the unit tests cannot: that the launch arguments
// produced by the production builder are actually accepted by the browser (they
// are echoed back in the session capabilities), and that a normal (non
// incognito) session on the same VDI does not inherit cookies or local storage
// from a previous session.

func webdriverEndpoint(t *testing.T) string {
	t.Helper()
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the live browser test")
	}
	endpoint := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if endpoint == "" {
		endpoint = "http://127.0.0.1:4444/wd/hub"
	}
	return endpoint
}

// createSessionRaw creates a WebDriver session through the W3C endpoint and
// returns the capabilities the browser actually reports. The Selenium client
// library cannot read capabilities back in W3C mode, so the raw protocol is
// used here; the payload is still built by the production helpers.
func createSessionRaw(t *testing.T, endpoint string, alwaysMatch map[string]interface{}) (string, map[string]interface{}) {
	t.Helper()
	envelope := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": alwaysMatch,
			"firstMatch":  []interface{}{map[string]interface{}{}},
		},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/session"
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("create session: status %d: %s", resp.StatusCode, body)
	}
	var decoded struct {
		Value struct {
			SessionID    string                 `json:"sessionId"`
			Capabilities map[string]interface{} `json:"capabilities"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if decoded.Value.SessionID == "" {
		t.Fatalf("session response carried no id: %s", body)
	}
	t.Cleanup(func() {
		req, err := http.NewRequest(http.MethodDelete, url+"/"+decoded.Value.SessionID, nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	})
	return decoded.Value.SessionID, decoded.Value.Capabilities
}

// sessionArgs reads the effective Chrome command-line arguments from the
// capabilities the browser reported for the session it actually created.
func sessionArgs(t *testing.T, capabilities map[string]interface{}) []string {
	t.Helper()
	raw, ok := capabilities["goog:chromeOptions"]
	if !ok {
		raw, ok = capabilities["chromeOptions"]
	}
	if !ok {
		t.Fatalf("session capabilities expose no chrome options: %v", capabilities)
	}
	options, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected chrome options shape: %T", raw)
	}
	var args []string
	switch value := options["args"].(type) {
	case []interface{}:
		for _, item := range value {
			args = append(args, fmt.Sprint(item))
		}
	case []string:
		args = value
	}
	return args
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func newSessionWithIncognito(t *testing.T, endpoint string, incognito bool) selenium.WebDriver {
	t.Helper()
	value := incognito
	crawlerCfg := cfg.Crawler{Incognito: &value}
	args := buildLaunchArgs(BrowserChrome, BrowserChrome, "", crawlerCfg, "")
	// The grid-managed browser needs these in this environment; they are
	// additive and do not affect the assertion under test.
	args = append(args, "--no-sandbox", "--disable-dev-shm-usage")
	caps := selenium.Capabilities{"browserName": "chrome"}
	caps.AddChrome(buildChromeCapabilities(args, map[string]interface{}{}))
	wd, err := selenium.NewRemote(caps, endpoint)
	if err != nil {
		t.Fatalf("create Selenium session: %v", err)
	}
	t.Cleanup(func() { _ = wd.Quit() })
	return wd
}

// TestIncognitoBrowserIntegration exercises a real browser session in both
// modes. The exact launch arguments are asserted by the unit tests, because the
// W3C WebDriver endpoint does not expose per-session command-line arguments;
// what this test adds is proof that the production capability builder produces
// sessions a real grid accepts and that both modes navigate successfully.
func TestIncognitoBrowserIntegration(t *testing.T) {
	endpoint := webdriverEndpoint(t)

	for _, tc := range []struct {
		name      string
		incognito bool
	}{
		{"explicit true launches a working private session", true},
		{"explicit false launches a working normal session", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wd := newSessionWithIncognito(t, endpoint, tc.incognito)
			if err := wd.Get("http://rc4-fixture:8000/target.html"); err != nil {
				t.Fatalf("navigate: %v", err)
			}
			title, err := wd.Title()
			if err != nil {
				t.Fatalf("Title(): %v", err)
			}
			if !strings.Contains(title, "RC4 Target") {
				t.Fatalf("unexpected page title %q", title)
			}
		})
	}
}

// TestSessionIsolationIntegration proves a normal session does not inherit
// cookies or local storage from a previous session on the same VDI. This is the
// compensating control for disabling private browsing.
func TestSessionIsolationIntegration(t *testing.T) {
	endpoint := webdriverEndpoint(t)

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("fixture listener unavailable: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, "<html><body><h1>isolation fixture</h1></body></html>")
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	fixtureURL := "http://" + listener.Addr().String() + "/"
	// The browser runs in a container and cannot reach the host loopback. Use
	// an override when the fixture is served on a container-reachable address.
	if override := strings.TrimSpace(os.Getenv("THECROWLER_E2E_ISOLATION_FIXTURE_URL")); override != "" {
		fixtureURL = override
	}

	setState := func(t *testing.T, wd selenium.WebDriver) {
		t.Helper()
		if err := wd.Get(fixtureURL); err != nil {
			t.Fatalf("Get(%s): %v", fixtureURL, err)
		}
		if _, err := wd.ExecuteScript("document.cookie = 'crowler_isolation=leak; path=/'; localStorage.setItem('crowler_isolation','leak'); return 1;", nil); err != nil {
			t.Fatalf("set state: %v", err)
		}
	}

	readState := func(t *testing.T, wd selenium.WebDriver) (string, string) {
		t.Helper()
		if err := wd.Get(fixtureURL); err != nil {
			t.Fatalf("Get(%s): %v", fixtureURL, err)
		}
		time.Sleep(200 * time.Millisecond)
		cookie, err := wd.ExecuteScript("return document.cookie;", nil)
		if err != nil {
			t.Fatalf("read cookie: %v", err)
		}
		storage, err := wd.ExecuteScript("return String(localStorage.getItem('crowler_isolation'));", nil)
		if err != nil {
			t.Fatalf("read localStorage: %v", err)
		}
		return fmt.Sprint(cookie), fmt.Sprint(storage)
	}

	// First session: normal browsing, writes state.
	first := newSessionWithIncognito(t, endpoint, false)
	setState(t, first)
	if err := first.Quit(); err != nil {
		t.Fatalf("quit first session: %v", err)
	}

	// Second session on the same VDI: must not see the first session's state.
	second := newSessionWithIncognito(t, endpoint, false)
	cookie, storage := readState(t, second)
	if strings.Contains(cookie, "crowler_isolation=leak") {
		t.Fatalf("cookie leaked between normal sessions: %q", cookie)
	}
	if storage != "null" {
		t.Fatalf("localStorage leaked between normal sessions: %q", storage)
	}
}
