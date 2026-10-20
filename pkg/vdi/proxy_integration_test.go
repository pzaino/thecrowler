//go:build integration

package vdi

// Controlled proxy isolation/integration test.
//
// It drives live Chromium sessions through the production proxy path
// (resolveEffectiveProxyURL/effectiveSessionProxy + buildLaunchArgs) against
// two local dummy forward proxies and one local target, proving per-source
// proxy application and cross-source isolation end to end.
//
// Scope: plain-HTTP forwarding only (no CONNECT/TLS: the dummy proxies do
// not terminate TLS, and DNS behavior with real hostnames is out of scope).
// The fixture target uses a loopback address, so the test appends a
// test-only --proxy-bypass-list=<-loopback> AFTER the builder args to allow
// loopback traffic through the proxy (Chromium otherwise bypasses loopback
// implicitly); production bypass configuration is unchanged.
//
// Run: THECROWLER_E2E_WEBDRIVER=1 go test -tags integration ./pkg/vdi/ -run TestProxyIsolationIntegration -v
// Optional: THECROWLER_E2E_WEBDRIVER_URL (default http://127.0.0.1:4444/wd/hub).
// Without the env var the test skips (a skip is not validation). With the
// env var set but Selenium unreachable, the test fails loudly.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-auxiliaries/selenium"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// dummyProxy is a minimal plain-HTTP forward proxy for tests. It records how
// many requests it forwarded and tags them so the target can attribute them.
type dummyProxy struct {
	name string
	mu   sync.Mutex
	hits int
}

func (p *dummyProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.hits++
	p.mu.Unlock()
	if !r.URL.IsAbs() {
		http.Error(w, "forward proxy requires absolute URI", http.StatusBadRequest)
		return
	}
	out, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	out.Header.Set("X-Forwarded-By", p.name)
	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *dummyProxy) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

type proxyTarget struct {
	mu     sync.Mutex
	agents map[string]int // User-Agent -> hits
	via    map[string]int // X-Forwarded-By -> hits ("" = direct)
}

func (t *proxyTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.agents == nil {
		t.agents = map[string]int{}
	}
	if t.via == nil {
		t.via = map[string]int{}
	}
	t.agents[r.Header.Get("User-Agent")]++
	t.via[r.Header.Get("X-Forwarded-By")]++
	t.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "proxied-ok")
}

func listenLoopback(t *testing.T, handler http.Handler) (addr string, closeFn func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("loopback listener unavailable: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() { _ = server.Close() }
}

func TestProxyIsolationIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the proxy isolation integration test")
	}
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}

	proxyA, proxyB := &dummyProxy{name: "proxy-a"}, &dummyProxy{name: "proxy-b"}
	proxyAAddr, closeA := listenLoopback(t, proxyA)
	defer closeA()
	proxyBAddr, closeB := listenLoopback(t, proxyB)
	defer closeB()
	target := &proxyTarget{}
	targetAddr, closeTarget := listenLoopback(t, target)
	defer closeTarget()
	targetURL := "http://" + targetAddr + "/page"

	const fixtureUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	crawlerCfg := cfg.Crawler{RequestImages: true, RequestCSS: true, RequestScripts: true, RequestPlugins: true}

	newProxiedDriver := func(t *testing.T, proxyURL string) selenium.WebDriver {
		t.Helper()
		args := buildLaunchArgs(BrowserChrome, BrowserChrome, fixtureUA, crawlerCfg, proxyURL)
		// Test-only loopback allowance (see header note); production bypass
		// configuration is untouched.
		filtered := args[:0:0]
		for _, a := range args {
			if !strings.HasPrefix(a, "--proxy-bypass-list=") {
				filtered = append(filtered, a)
			}
		}
		filtered = append(filtered,
			"--headless=new",
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--proxy-bypass-list=<-loopback>",
		)
		capabilities := selenium.Capabilities{"browserName": "chrome"}
		capabilities.AddChrome(buildChromeCapabilities(filtered, map[string]interface{}{}))
		wd, err := selenium.NewRemote(capabilities, webdriverURL)
		if err != nil {
			t.Fatalf("Selenium/Chrome unavailable at %s: %v", webdriverURL, err)
		}
		return wd
	}

	navigate := func(t *testing.T, wd selenium.WebDriver, url string) string {
		t.Helper()
		if err := wd.Get(url); err != nil {
			t.Fatalf("Get(%s): %v", url, err)
		}
		title, err := wd.Title()
		if err != nil {
			t.Fatalf("Title(): %v", err)
		}
		return title
	}

	// Source A via proxy A, source B via proxy B: effective values resolved
	// through the production three-layer composition.
	effectiveA := effectiveSessionProxy("http://"+proxyAAddr, true, "", "http://vdi-default:8080")
	effectiveB := effectiveSessionProxy("", false, "http://"+proxyBAddr, "http://vdi-default:8080")
	if !strings.Contains(effectiveA, proxyAAddr) || !strings.Contains(effectiveB, proxyBAddr) {
		t.Fatalf("effective proxies = %q, %q", effectiveA, effectiveB)
	}

	wdA := newProxiedDriver(t, effectiveA)
	defer func() { _ = wdA.Quit() }()
	wdB := newProxiedDriver(t, effectiveB)
	defer func() { _ = wdB.Quit() }()

	navigate(t, wdA, targetURL)
	navigate(t, wdB, targetURL)

	if got := proxyA.count(); got != 1 {
		t.Errorf("proxy A forwarded %d requests, want exactly source A's", got)
	}
	if got := proxyB.count(); got != 1 {
		t.Errorf("proxy B forwarded %d requests, want exactly source B's", got)
	}
	target.mu.Lock()
	viaA, viaB, direct := target.via["proxy-a"], target.via["proxy-b"], target.via[""]
	agents := target.agents[fixtureUA]
	target.mu.Unlock()
	if viaA != 1 || viaB != 1 {
		t.Errorf("target attribution = a:%d b:%d, want 1/1 (cross-source leak or loss)", viaA, viaB)
	}
	if direct != 0 {
		t.Errorf("target saw %d direct hits, want 0", direct)
	}
	if agents != 2 {
		t.Errorf("target saw fixture UA %d times, want 2", agents)
	}

	// no_proxy session goes direct: proxies see nothing new.
	effectiveDirect := effectiveSessionProxy("no_proxy", true, "", "http://vdi-default:8080")
	if effectiveDirect != "" {
		t.Fatalf("no_proxy resolved to %q, want direct", effectiveDirect)
	}
	wdDirect := newProxiedDriver(t, effectiveDirect)
	defer func() { _ = wdDirect.Quit() }()
	navigate(t, wdDirect, targetURL)
	if got := proxyA.count(); got != 1 {
		t.Errorf("proxy A saw %d after direct session, want still 1", got)
	}
	if got := proxyB.count(); got != 1 {
		t.Errorf("proxy B saw %d after direct session, want still 1", got)
	}
	target.mu.Lock()
	direct = target.via[""]
	target.mu.Unlock()
	if direct != 1 {
		t.Errorf("target saw %d direct hits, want 1", direct)
	}
}
