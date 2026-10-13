//go:build integration

package vdi

// Controlled Client Hints consistency test (review finding 4 / VDI-021,
// Instagram/VDI-006 completion).
//
// It drives a live Chromium through the PR1 launch-argument builder and the
// identity stack, then compares three surfaces for one selection event:
// the main-document HTTP request headers observed by httptest, page-side
// navigator values, and navigator.userAgentData (low-entropy + high-entropy).
// The fixture identity reproduces the reported Instagram case (advertised
// macOS/Chrome 132 on a Linux host); rotation then crosses to Windows.
//
// Run: THECROWLER_E2E_WEBDRIVER=1 go test -tags integration ./pkg/vdi/ -run TestIdentityClientHintsIntegration -v
// Optional: THECROWLER_E2E_WEBDRIVER_URL (default http://127.0.0.1:4444/wd/hub).
// Without the env var the test skips (a skip is not validation). With the
// env var set but Selenium unreachable, the test fails loudly.
//
// Portability note (minor follow-up 2): the fixture binds to the test
// process's loopback interface, so a Selenium server in another container
// cannot reach it; run Selenium on the same host or extend this test with an
// externally reachable fixture URL before interpreting results.
//
// Deliberately non-committal on the open question: Sec-CH-UA* request
// headers are recorded and reported, and contradictions with the selected
// identity fail the test, but mere absence (e.g. while
// --disable-features=UserAgentClientHint is configured) is informational.
// The launch flag and metadata implementation must change only if this test
// demonstrates a problem.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-auxiliaries/selenium"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type recordedRequest struct {
	path             string
	userAgent        string
	secCHUA          string
	secCHMobile      string
	secCHPlatform    string
	secCHFullList    string
	secCHPlatVersion string
	secCHArch        string
	secCHModel       string
}

type headerRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (h *headerRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, recordedRequest{
		path:             r.URL.Path,
		userAgent:        r.Header.Get("User-Agent"),
		secCHUA:          r.Header.Get("Sec-CH-UA"),
		secCHMobile:      r.Header.Get("Sec-CH-UA-Mobile"),
		secCHPlatform:    r.Header.Get("Sec-CH-UA-Platform"),
		secCHFullList:    r.Header.Get("Sec-CH-UA-Full-Version-List"),
		secCHPlatVersion: r.Header.Get("Sec-CH-UA-Platform-Version"),
		secCHArch:        r.Header.Get("Sec-CH-UA-Arch"),
		secCHModel:       r.Header.Get("Sec-CH-UA-Model"),
	})
	h.mu.Unlock()
	// Opt the origin into high-entropy hints so later same-origin requests
	// carry them (localhost is a secure context). Without opt-in the
	// high-entropy headers are legitimately absent.
	w.Header().Set("Accept-CH", "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Platform-Version, Sec-CH-UA-Arch, Sec-CH-UA-Model, Sec-CH-UA-Bitness")
	w.Header().Set("Accept-CH-Lifetime", "86400")
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, "<html><head><title>identity-fixture</title></head><body>ok</body></html>")
}

func (h *headerRecorder) last() (recordedRequest, bool) {
	return h.lastDocument()
}

// lastDocument returns the most recent non-favicon request: Chromium fetches
// /favicon.ico automatically and must not shadow the navigated document.
func (h *headerRecorder) lastDocument() (recordedRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.requests) - 1; i >= 0; i-- {
		if h.requests[i].path != "/favicon.ico" {
			return h.requests[i], true
		}
	}
	return recordedRequest{}, false
}

func TestIdentityClientHintsIntegration(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the identity/Client-Hints integration test")
	}
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}

	recorder := &headerRecorder{}
	server := httptest.NewServer(recorder)
	defer server.Close()

	// Fixed selection: the Instagram case — advertised macOS/Chrome 132 while
	// the host is Linux. The derived brands agree with every surface at
	// once; rotation then crosses to a Windows identity.
	const selectedUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	const rotatedUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	newDriver := func(t *testing.T, ua string) selenium.WebDriver {
		t.Helper()
		args := buildLaunchArgs(BrowserChrome, BrowserChrome, ua, cfg.Crawler{
			RequestImages:  true,
			RequestCSS:     true,
			RequestScripts: true,
			RequestPlugins: true,
		}, "")
		capabilities := selenium.Capabilities{"browserName": "chrome"}
		capabilities.AddChrome(buildChromeCapabilities(append(args,
			"--headless=new",
			"--no-sandbox",
			"--disable-dev-shm-usage",
		), map[string]interface{}{}))
		wd, err := selenium.NewRemote(capabilities, webdriverURL)
		if err != nil {
			// Explicitly enabled validation must fail loudly when Selenium
			// is unreachable: a skip here would masquerade as success
			// (minor follow-up 2). Only the unset env var skips.
			t.Fatalf("Selenium/Chrome unavailable at %s: %v", webdriverURL, err)
		}
		return wd
	}

	wd := newDriver(t, selectedUA)
	defer func() { _ = wd.Quit() }()

	mustScript := func(t *testing.T, script string) interface{} {
		t.Helper()
		value, err := wd.ExecuteScript(script, nil)
		if err != nil {
			t.Fatalf("ExecuteScript(%q): %v", script, err)
		}
		return value
	}

	ident := MapAdvertisedOSIdentity("darwin", false)
	major, ok := ChromeMajorVersion(selectedUA)
	if !ok || major != "132" {
		t.Fatalf("fixture UA major = %q, %t", major, ok)
	}
	brands := ClientHintBrandsFor("chrome", major, "")
	details := NewClientHintsDetails(selectedUA, "chrome", ident)

	// Phase 0 — initial ConnectVDI flow (launch-args UA only; no script, no
	// CDP override yet). Documents the baseline the crawler's first
	// navigation actually uses. The HTTP UA must already equal the launch
	// argument; page-side identity is still native here by construction.
	if err := wd.Get(server.URL + "/phase0"); err != nil {
		t.Fatalf("Get(phase0 fixture): %v", err)
	}
	observed, ok := recorder.last()
	if !ok {
		t.Fatal("fixture server observed no request")
	}
	if observed.userAgent != selectedUA {
		t.Errorf("phase0 HTTP User-Agent = %q, want launch-args %q", observed.userAgent, selectedUA)
	}
	t.Logf("phase0 (launch args only): HTTP-UA=%q native-JS-UA=%q Sec-CH-UA=%q Mobile=%q Platform=%q",
		observed.userAgent,
		fmt.Sprint(mustScript(t, "return navigator.userAgent")),
		observed.secCHUA, observed.secCHMobile, observed.secCHPlatform)

	// Phase 1 — new-document script only (still no CDP override): this is the
	// initial-flow observation the validation note asks for. JS identity must
	// equal the selection; HTTP Client Hints (if the browser sends any while
	// --disable-features=UserAgentClientHint is configured) are recorded and
	// reported, not asserted.
	if err := ReplaceIdentityScript(wd, 0, antiDetectionScript(selectedUA, ident, brands, details)); err != nil {
		t.Fatalf("ReplaceIdentityScript: %v", err)
	}
	if err := wd.Get(server.URL + "/phase1"); err != nil {
		t.Fatalf("Get(phase1 fixture): %v", err)
	}
	observed, ok = recorder.last()
	if !ok {
		t.Fatal("fixture server observed no phase1 request")
	}
	if observed.userAgent != selectedUA {
		t.Errorf("phase1 HTTP User-Agent = %q, want %q", observed.userAgent, selectedUA)
	}
	if got := fmt.Sprint(mustScript(t, "return navigator.userAgent")); got != selectedUA {
		t.Errorf("phase1 navigator.userAgent = %q, want %q", got, selectedUA)
	}
	t.Logf("phase1 (script, no CDP override): HTTP-UA=%q Sec-CH-UA=%q Mobile=%q Platform=%q JS-brands=%s",
		observed.userAgent, observed.secCHUA, observed.secCHMobile, observed.secCHPlatform,
		fmt.Sprint(mustScript(t, "return JSON.stringify((navigator.userAgentData && navigator.userAgentData.brands) || [])")))

	// Phase 2 — full initial identity via the production path
	// (establishInitialIdentity mirrors ConnectVDI): every surface must
	// agree with the selection.
	if err := establishInitialIdentity(wd, 0, selectedUA, ident, brands, details); err != nil {
		t.Fatalf("establishInitialIdentity: %v", err)
	}

	if err := wd.Get(server.URL); err != nil {
		t.Fatalf("Get(fixture): %v", err)
	}

	// 1. HTTP surface: the main-document request must carry the selected UA.
	observed, ok = recorder.last()
	if !ok {
		t.Fatal("fixture server observed no request")
	}
	if observed.userAgent != selectedUA {
		t.Errorf("HTTP User-Agent = %q, want %q", observed.userAgent, selectedUA)
	}

	// 2. JavaScript surface: navigator values must equal the selection.
	if got := fmt.Sprint(mustScript(t, "return navigator.userAgent")); got != selectedUA {
		t.Errorf("navigator.userAgent = %q, want %q", got, selectedUA)
	}
	if got := fmt.Sprint(mustScript(t, "return navigator.platform")); got != "MacIntel" {
		t.Errorf("navigator.platform = %q, want MacIntel", got)
	}
	if got := fmt.Sprint(mustScript(t, "return (navigator.userAgentData && navigator.userAgentData.platform) || ''")); got != "macOS" {
		t.Errorf("userAgentData.platform = %q, want macOS", got)
	}
	if got := fmt.Sprint(mustScript(t, "return (navigator.userAgentData && String(navigator.userAgentData.mobile)) || ''")); got != "false" {
		t.Errorf("userAgentData.mobile = %q, want false", got)
	}
	brandsJSON := fmt.Sprint(mustScript(t, "return JSON.stringify((navigator.userAgentData && navigator.userAgentData.brands) || [])"))
	if !strings.Contains(brandsJSON, `"Chromium"`) || !strings.Contains(brandsJSON, `"132"`) {
		t.Errorf("userAgentData.brands = %s, want Chromium at 132", brandsJSON)
	}
	// High-entropy reads must match the advertised identity, not the host.
	highEntropy, err := wd.ExecuteScriptAsync(`const done = arguments[0];
		if (!navigator.userAgentData || !navigator.userAgentData.getHighEntropyValues) { done("unavailable"); return; }
		navigator.userAgentData.getHighEntropyValues(["platformVersion", "architecture", "model", "bitness", "fullVersionList"]).then(
			v => done(JSON.stringify(v)), e => done("error:" + e));`, nil)
	if err != nil {
		t.Fatalf("high-entropy read: %v", err)
	}
	for _, want := range []string{`"platformVersion":"14.0.0"`, `"architecture":"arm"`, `"model":""`, `"bitness":"64"`} {
		if !strings.Contains(fmt.Sprint(highEntropy), want) {
			t.Errorf("high-entropy values = %v, want %s", highEntropy, want)
		}
	}
	// The full-version list must mirror the advertised brands, never the
	// runtime build.
	if fullList := fmt.Sprint(highEntropy); !strings.Contains(fullList, `"132.0.0.0"`) || !strings.Contains(fullList, `"Chromium"`) {
		t.Errorf("high-entropy fullVersionList = %s, want Chromium at 132.0.0.0", fullList)
	} else if strings.Contains(fullList, "6834") {
		t.Errorf("high-entropy fullVersionList = %s leaks the runtime Chromium build", fullList)
	}

	// 3. Client Hints request headers: recorded and cross-checked when the
	// browser sends them; absence is informational (open question: interaction
	// with --disable-features=UserAgentClientHint), contradiction is fatal.
	t.Logf("observed request headers: UA=%q Sec-CH-UA=%q Mobile=%q Platform=%q",
		observed.userAgent, observed.secCHUA, observed.secCHMobile, observed.secCHPlatform)
	if observed.secCHUA != "" && !strings.Contains(observed.secCHUA, `"Chromium"`) {
		t.Errorf("Sec-CH-UA = %q contradicts the selected Chromium identity", observed.secCHUA)
	}
	// Instagram case: the advertised platform must be macOS, never the
	// host's Linux value.
	if observed.secCHPlatform != "" && !strings.Contains(observed.secCHPlatform, "macOS") {
		t.Errorf("Sec-CH-UA-Platform = %q contradicts the selected macOS identity", observed.secCHPlatform)
	}
	// High-entropy headers (opted in via Accept-CH on the previous
	// response): contradiction fatal, absence informational.
	t.Logf("high-entropy request headers: FullList=%q PlatVersion=%q Arch=%q Model=%q",
		observed.secCHFullList, observed.secCHPlatVersion, observed.secCHArch, observed.secCHModel)
	if observed.secCHFullList != "" {
		if !strings.Contains(observed.secCHFullList, `"Chromium"`) || !strings.Contains(observed.secCHFullList, "132.0.0.0") {
			t.Errorf("Sec-CH-UA-Full-Version-List = %q contradicts the selected identity", observed.secCHFullList)
		}
		if strings.Contains(observed.secCHFullList, "6834") {
			t.Errorf("Sec-CH-UA-Full-Version-List = %q leaks the runtime Chromium build", observed.secCHFullList)
		}
	}
	if observed.secCHPlatVersion != "" && observed.secCHPlatVersion != "14.0.0" {
		t.Errorf("Sec-CH-UA-Platform-Version = %q, want 14.0.0", observed.secCHPlatVersion)
	}
	if observed.secCHArch != "" && observed.secCHArch != `"arm"` && observed.secCHArch != "arm" {
		t.Errorf("Sec-CH-UA-Arch = %q, want arm", observed.secCHArch)
	}

	// 4. Rotation (finding 2, end-to-end, cross-OS): replacing the
	// registration must move later navigations to the new identity in the
	// same session.
	rotMajor, _ := ChromeMajorVersion(rotatedUA)
	rotBrands := ClientHintBrandsFor("chrome", rotMajor, "")
	rotIdent := MapAdvertisedOSIdentity("windows", false)
	rotDetails := NewClientHintsDetails(rotatedUA, "chrome", rotIdent)
	if err := SetUserAgentOverrideWithMetadata(wd, 0, rotatedUA, rotIdent, rotBrands, rotDetails); err != nil {
		t.Fatalf("rotation CDP override: %v", err)
	}
	if err := RefreshIdentityScript(wd, 0, rotatedUA, rotIdent, rotBrands, rotDetails); err != nil {
		t.Fatalf("RefreshIdentityScript: %v", err)
	}
	if err := wd.Get(server.URL + "/rotated"); err != nil {
		t.Fatalf("Get(rotated fixture): %v", err)
	}
	if got := fmt.Sprint(mustScript(t, "return navigator.userAgent")); got != rotatedUA {
		t.Errorf("post-rotation navigator.userAgent = %q, want %q (stale script)", got, rotatedUA)
	}
	if got := fmt.Sprint(mustScript(t, "return navigator.platform")); got != "Win32" {
		t.Errorf("post-rotation navigator.platform = %q, want Win32", got)
	}
	observed, ok = recorder.last()
	if !ok || observed.userAgent != rotatedUA {
		t.Errorf("post-rotation HTTP User-Agent = %+v, want %q", observed, rotatedUA)
	}
	if observed.secCHPlatform != "" && !strings.Contains(observed.secCHPlatform, "Windows") {
		t.Errorf("post-rotation Sec-CH-UA-Platform = %q contradicts the rotated Windows identity", observed.secCHPlatform)
	}
}
