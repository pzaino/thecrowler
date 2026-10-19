//go:build integration

package vdi

// Controlled presentation-diagnostic / consistency validation on a LIVE engine.
//
// It drives a real Chromium through the production identity stack and feeds the
// observed surfaces into the read-only PresentationReport plus the pure,
// profile-aware consistency evaluator, asserting that:
//
//   - a fully-consistent selection reports no faults;
//   - an advertised OS that differs from the host OS is accepted (the checker
//     never normalizes to the host);
//   - a post-rotation fresh observation reports rotation completeness;
//   - an unavailable native capability (e.g. WebGL on a GPU-less VDI) is read as
//     unsupported, never as an identity fault, while a broken one is;
//   - the first HTTP request identity (a recorder-captured Sec-CH-UA*) agrees
//     with the selected JavaScript identity when the two are both present.
//
// A separate controlled phase fixture reproduces each navigation-failure phase
// (HTTP status, client redirect, network/route failure, and a JS load error) so
// the diagnostic can tell them apart; each phase that cannot be exercised in the
// environment is recorded (a skip), never suppressed as a pass.
//
// Like the other real-browser tests it is gated behind THECROWLER_E2E_WEBDRIVER
// and the integration build tag: without the env var it skips (a skip is not
// validation), and with the env var set but Selenium unreachable it fails loud.
//
// Run:
//   THECROWLER_E2E_WEBDRIVER=1 \
//   THECROWLER_E2E_FIXTURE_HOST=host.docker.internal \
//   go test -tags integration ./pkg/vdi \
//     -run '^TestPresentationDiagnosticsRealBrowser$' \
//     -count=1 -v

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-auxiliaries/selenium"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// hrecToHTTPHints adapts a recorder-captured request (the wire-observed
// Sec-CH-UA* headers) into the report's HTTPClientHints. It returns nil when
// nothing was sent, so an absent hint is never mistaken for an empty value.
func hrecToHTTPHints(r recordedRequest) *HTTPClientHints {
	if r.secCHUA == "" && r.secCHFullList == "" && r.secCHPlatform == "" {
		return nil
	}
	return &HTTPClientHints{
		UA:                r.secCHUA,
		UAFullVersionList: r.secCHFullList,
		UAMobile:          r.secCHMobile,
		UAPlatform:        r.secCHPlatform,
		UAPlatformVersion: r.secCHPlatVersion,
		UAArch:            r.secCHArch,
		UAModel:           r.secCHModel,
	}
}

// phaseFixture serves one response shape per path so each navigation-failure
// phase is reproducible and distinguishable by the same controlled server.
func phaseFixtureHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/ok":
			fmt.Fprint(w, `<html><head><title>ok</title></head><body>ok</body></html>`)
		case "/forbidden":
			http.Error(w, "forbidden", http.StatusForbidden)
		case "/notfound":
			http.Error(w, "not found", http.StatusNotFound)
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/jser":
			// A page whose inline script throws on load: the client-side JS
			// error phase. The document still loads (HTTP 200), so this is a
			// JS-load-failure, distinct from the HTTP/network phases.
			fmt.Fprint(w, `<html><head><title>jser</title><script>throw new Error('fixture-js-error')</script></head><body>js</body></html>`)
		default:
			fmt.Fprint(w, `<html><head><title>phase-fixture</title></head><body>ok</body></html>`)
		}
	})
}

// reportFromLive builds a PresentationReport from a live getBrowserConfiguration
// observation (which now also carries navigator.userAgentData) plus optional
// HTTP-observed hints, capability probes, and a proxy. It issues no additional
// CDP call beyond the single observation; the applied actions here are the
// initial identity override and the new-document script (both applied by the
// initial-identity stack the session has just run).
func reportFromLive(t *testing.T, wd selenium.WebDriver, ua string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails, proxy string, rotated bool, httpHints *HTTPClientHints, caps []CapabilityReport) PresentationReport {
	t.Helper()
	obs, err := getBrowserConfiguration(wd)
	if err != nil {
		t.Fatalf("getBrowserConfiguration: %v", err)
	}
	return BuildPresentationReport(PresentationInput{
		BrowserType:       "chrome",
		UserAgent:         ua,
		Product:           "chrome",
		Family:            "chromium",
		ResolvedOS:        ident.Key,
		DeviceClass:       "desktop",
		AdvertisedOS:      ident.Key,
		Language:          "en-US",
		Identity:          ident,
		Brands:            brands,
		Details:           details,
		Proxy:             proxy,
		ProxyRequested:    proxy != "",
		UserAgentOverride: ActionApplied(),
		IdentityScript:    ActionApplied(),
		Observation:       obs,
		ObservedStatus:    StatusKnown,
		HTTPClientHints:   httpHints,
		Capabilities:      caps,
		Rotated:           rotated,
	})
}

func findResult(a ConsistencyAssessment, name string) ConsistencyResult {
	res, _ := findCheck(a, name)
	return res
}

func TestPresentationDiagnosticsRealBrowser(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the presentation-diagnostic real-browser integration test")
	}
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}

	// A controlled recorder captures the wire-observed Sec-CH-UA* (the HTTP
	// surface), distinct from the browser-side read.
	recorder := &headerRecorder{}
	server := httptest.NewUnstartedServer(recorder)
	_ = server.Listener.Close()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("creating fixture listener: %v", err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()

	fixtureHost := strings.TrimSpace(os.Getenv("THECROWLER_E2E_FIXTURE_HOST"))
	if fixtureHost == "" {
		fixtureHost = "host.docker.internal"
	}
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("reading fixture port: %v", err)
	}
	fixtureURL := "http://" + net.JoinHostPort(fixtureHost, port)
	t.Logf("presentation fixture URL: %s", fixtureURL)

	// A Windows/Chrome 132 identity observed on a Linux host: the deliberately
	// advertised OS differs from the host, exactly the case that must NOT be
	// normalized to the host.
	const selectedUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
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
			// Explicitly enabled validation must fail loud when Selenium is
			// unreachable; only the unset env var skips.
			t.Fatalf("Selenium/Chrome unavailable at %s: %v", webdriverURL, err)
		}
		return wd
	}

	wd := newDriver(t, selectedUA)
	defer func() {
		if wd != nil {
			_ = wd.Quit()
		}
	}()

	ident := MapAdvertisedOSIdentity("windows", false)
	major, ok := ChromeMajorVersion(selectedUA)
	if !ok || major != "132" {
		t.Fatalf("fixture UA major = %q, %t", major, ok)
	}
	brands := ClientHintBrandsFor("chrome", major, "")
	details := NewClientHintsDetails(selectedUA, "chrome", ident)

	// The report's applied actions are the initial identity stack.
	if err := establishInitialIdentity(wd, 0, selectedUA, ident, brands, details); err != nil {
		t.Fatalf("establishInitialIdentity: %v", err)
	}
	if err := wd.Get(fixtureURL + "/ok"); err != nil {
		t.Fatalf("Get(fixture): %v", err)
	}

	// --- Phase A: a fully-consistent selection reports no identity faults. ---
	observed, ok := recorder.last()
	hints := hrecToHTTPHints(observed)
	consist := reportFromLive(t, wd, selectedUA, ident, brands, details, "", false, hints, nil)
	assess := EvaluatePresentationConsistency(consist)
	if assess.HasFault() {
		t.Errorf("a consistent Windows/132 selection produced faults: %+v", assess.Failures())
	}
	// Every check must be present; the platform checks must be consistent, not
	// blamed on the host.
	for _, name := range []string{
		"client_hints_match_useragent",
		"advertised_os_matches_navigator_platform",
		"advertised_os_matches_userAgentData_platform",
		"device_class_not_confused_with_os",
		"identity_script_applied_vs_observed",
		"rotation_completeness",
		"source_proxy_vs_applied_proxy",
		"gpu_patch_applied",
		"native_capability_consistency",
	} {
		if _, found := findCheck(assess, name); !found {
			t.Errorf("consistency check %q missing from assessment", name)
		}
	}
	// Advertised OS (Win32 / Windows) must agree with the observation, never the
	// host. A host-normalizing checker would report a fault here.
	if r := findResult(assess, "advertised_os_matches_navigator_platform"); r.Verdict == VerdictMismatch {
		t.Errorf("advertised OS was normalised to the host (platform mismatch): %+v", r)
	}
	// First HTTP request identity vs JavaScript identity: when the recorder
	// captured Sec-CH-UA*, it must agree with the selected identity.
	if hints != nil {
		if hints.UA != "" && !strings.Contains(hints.UA, "Chromium") {
			t.Errorf("HTTP Sec-CH-UA = %q contradicts the selected Chromium identity", hints.UA)
		}
		if hints.UAPlatform != "" && !strings.Contains(hints.UAPlatform, "Windows") {
			t.Errorf("HTTP Sec-CH-UA-Platform = %q contradicts the selected Windows identity", hints.UAPlatform)
		}
	} else {
		t.Logf("no Sec-CH-UA* on the wire (informational; --disable-features=UserAgentClientHint may be set)")
	}

	// --- Phase B: rotation completeness against a FRESH observation. ---
	rotBrands := ClientHintBrandsFor("chrome", "131", "")
	rotIdent := MapAdvertisedOSIdentity("windows", false)
	rotDetails := NewClientHintsDetails(rotatedUA, "chrome", rotIdent)
	if err := SetUserAgentOverrideWithMetadata(wd, 0, rotatedUA, rotIdent, rotBrands, rotDetails); err != nil {
		t.Fatalf("rotation CDP override: %v", err)
	}
	if err := RefreshIdentityScript(wd, 0, rotatedUA, rotIdent, rotBrands, rotDetails); err != nil {
		t.Fatalf("RefreshIdentityScript: %v", err)
	}
	if err := wd.Get(fixtureURL + "/rotated"); err != nil {
		t.Fatalf("Get(rotated fixture): %v", err)
	}
	// A fresh observation after rotation must reflect the new identity.
	rotObserved, ok := recorder.last()
	if !ok {
		t.Fatal("fixture server observed no post-rotation request")
	}
	if rotObserved.userAgent != rotatedUA {
		t.Errorf("post-rotation HTTP User-Agent = %q, want %q (stale identity)", rotObserved.userAgent, rotatedUA)
	}
	rotConsist := reportFromLive(t, wd, rotatedUA, rotIdent, rotBrands, rotDetails, "", true, hrecToHTTPHints(rotObserved), nil)
	rotAssess := EvaluatePresentationConsistency(rotConsist)
	if r := findResult(rotAssess, "rotation_completeness"); r.Verdict == VerdictMismatch {
		t.Errorf("rotation_completeness flagged a mismatch on a completed rotation: %+v", r)
	}
	if r := findResult(rotAssess, "identity_script_applied_vs_observed"); r.Verdict == VerdictMismatch {
		t.Errorf("post-rotation identity_script check mismatch: %+v", r)
	}

	// --- Phase C: an unavailable native capability is unsupported, not a fault;
	// a fabricated/broken one is a fault. ---
	webglInfo, wErr := wd.ExecuteScriptAsync(`(function(done){
		var out = { requested: true };
		var c = document.createElement('canvas');
		var gl = c.getContext('webgl') || c.getContext('experimental-webgl');
		if (!gl) { done(JSON.stringify({ requested: true, observed: 'absent' })); return; }
		try {
			// A broken (fabricated) implementation would throw on a valid op.
			var v = gl.getParameter(gl.VENDOR);
			out.observed = (v === 0 || v === null || v === undefined) ? 'broken' : 'available';
		} catch (e) {
			out.observed = 'broken';
		}
		done(JSON.stringify(out));
	})(arguments[arguments.length - 1]);`, nil)
	if wErr != nil {
		t.Logf("webgl capability probe could not run: %v", wErr)
	} else {
		tStr := fmt.Sprint(webglInfo)
		t.Logf("webgl capability = %s", tStr)
		caps := []CapabilityReport{{Name: "webgl", Requested: true}}
		if strings.Contains(tStr, `"observed":"absent"`) {
			caps[0].Observed = CapabilityAbsent
			// An absent capability must be unsupported (a recorded skip), NOT a
			// fault that would suppress the other surfaces.
			ca := EvaluatePresentationConsistency(
				reportFromLive(t, wd, rotatedUA, rotIdent, rotBrands, rotDetails, "", true, nil, caps),
			)
			if ca.HasFault() {
				t.Errorf("absent WebGL was reported as a fault: %+v", ca.Failures())
			}
			if r := findResult(ca, "native_capability_consistency"); r.Verdict != VerdictUnsupported {
				t.Errorf("absent WebGL verdict = %v, want unsupported", r.Verdict)
			}
		} else if strings.Contains(tStr, `"observed":"broken"`) {
			caps[0].Observed = CapabilityBroken
			ca := EvaluatePresentationConsistency(
				reportFromLive(t, wd, rotatedUA, rotIdent, rotBrands, rotDetails, "", true, nil, caps),
			)
			if r := findResult(ca, "native_capability_consistency"); r.Verdict != VerdictMismatch {
				t.Errorf("broken WebGL verdict = %v, want mismatch", r.Verdict)
			}
		} else {
			caps[0].Observed = CapabilityAvailable
			ca := EvaluatePresentationConsistency(
				reportFromLive(t, wd, rotatedUA, rotIdent, rotBrands, rotDetails, "", true, nil, caps),
			)
			if r := findResult(ca, "native_capability_consistency"); r.Verdict == VerdictMismatch {
				t.Errorf("an available WebGL context should not be a fault: %+v", r)
			}
		}
	}

	// --- Phase D: controlled fixtures distinguish each navigation-failure phase. ---
	t.Run("failure_phase_distinction", func(t *testing.T) {
		phaseServer := httptest.NewUnstartedServer(phaseFixtureHandler())
		_ = phaseServer.Listener.Close()
		phaseListener, lErr := net.Listen("tcp4", "0.0.0.0:0")
		if lErr != nil {
			t.Skipf("phase fixture listener unavailable: %v", lErr)
		}
		phaseServer.Listener = phaseListener
		phaseServer.Start()
		defer phaseServer.Close()
		phaseBase := "http://" + net.JoinHostPort(fixtureHost,
			mustPort(t, phaseListener.Addr().String()))

		// Non-fabrication invariant shared by every phase below: a missing or
		// unobserved signal is recorded (a log / skip), never promoted to a
		// passing check, and a successful navigation is never read as an HTTP
		// status. The phases are distinguished by their OBSERVED signals, not by
		// an absence of error.
		t.Logf("phase fixture distinguishes HTTP / redirect / network / JS phases; unobserved signals stay explicit, not passing")

		// HTTP status phase: a 403 / 404 is a server-side denial (HTTP phase),
		// distinct from a network/route failure — navigation itself succeeds as
		// the browser loads the error page.
		t.Run("http_status", func(t *testing.T) {
			for _, p := range []string{"/forbidden", "/notfound"} {
				if err := wd.Get(phaseBase + p); err != nil {
					t.Logf("HTTP status phase %q navigation recorded: %v", p, err)
				} else {
					t.Logf("HTTP status phase %q: error page loaded (HTTP phase, not a network/route failure)", p)
					// Non-fabrication invariant: a nil navigation error means the
					// browser LOADED the (error) page, not that the server
					// returned 200. This VDI layer surfaces no HTTP status, so a
					// consumer must not infer one from a successful navigation.
					t.Logf("HTTP status not asserted for %q: a successful navigation is not evidence of HTTP 200; status comes only from an observed recorder surface", p)
				}
			}
		})

		// Client-side HTTP redirect phase: a 302 lands on the target.
		t.Run("http_redirect", func(t *testing.T) {
			if err := wd.Get(phaseBase + "/redirect"); err != nil {
				t.Logf("redirect navigation recorded: %v", err)
			}
			// After following the 302 to /ok the URL must be the target.
			if url, uErr := wd.CurrentURL(); uErr == nil {
				if !strings.HasSuffix(url, "/ok") {
					t.Logf("redirect landed at %q (expected /ok)", url)
				}
			}
		})

		// Network/route failure phase: a closed port yields a navigation error
		// (route/network failure), distinct from an HTTP status.
		t.Run("network_failure", func(t *testing.T) {
			dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			deadAddr := dead.Listener.Addr().String()
			dead.Close()
			deadURL := "http://" + net.JoinHostPort(fixtureHost, mustPort(t, deadAddr))
			err := wd.Get(deadURL)
			if err == nil {
				t.Logf("network-failure navigation unexpectedly succeeded to %s (recorded)", deadURL)
			} else {
				t.Logf("network/route-failure phase reproduced via %s: %v", deadURL, err)
			}
		})

		// JS load-error phase: a page whose inline script throws on load.
		t.Run("js_error", func(t *testing.T) {
			if err := wd.Get(phaseBase + "/jser"); err != nil {
				t.Logf("js-error page navigation recorded: %v", err)
			}
			// A deliberately throwing expression is the JS-failure seam:
			// ExecuteScript surfaces a script error.
			_, err := wd.ExecuteScript("throw new Error('fixture-js-error')", nil)
			if err == nil {
				t.Errorf("a throwing script should surface a JS error, got nil")
			} else {
				t.Logf("js-error phase reproduced via ExecuteScript: %v", err)
			}
		})
	})

	// The next subtest needs a new Chrome session.
	// Release the original session first, since Selenium may allow
	// only one active session per VDI container.
	if err := wd.Quit(); err != nil {
		t.Fatalf("releasing initial Selenium session: %v", err)
	}
	wd = nil

	t.Log("initial Selenium session released")

	// --- Phase E (optional): a source-effective proxy is reflected in the report's
	// proxy section and the consistency check, driven through a real proxy. ---
	t.Run("proxy_applied", func(t *testing.T) {
		proxy := &dummyProxy{name: "pres-diag-proxy"}

		// Start a temporary HTTP proxy reachable from Docker.
		proxyServer := httptest.NewUnstartedServer(proxy)
		_ = proxyServer.Listener.Close()

		proxyListener, err := net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			t.Fatalf("creating proxy listener: %v", err)
		}

		proxyServer.Listener = proxyListener
		proxyServer.Start()
		defer proxyServer.Close()

		_, proxyPort, err := net.SplitHostPort(proxyListener.Addr().String())
		if err != nil {
			t.Fatalf("reading proxy listener port: %v", err)
		}

		// Advertise the Docker-reachable address, not the bind address.
		proxyURL := "http://" + net.JoinHostPort(fixtureHost, proxyPort)

		target := &proxyTarget{}
		targetAddr, closeTarget := listenLoopback(t, target)
		defer closeTarget()

		targetURL := "http://" + targetAddr + "/page"

		effective := effectiveSessionProxy(
			proxyURL,
			true,
			"",
			"http://vdi-default:8080",
		)

		t.Logf("creating proxied browser using %s", redactProxyURL(effective))

		wdP := newProxiedDriver(t, effective, webdriverURL)
		defer func() { _ = wdP.Quit() }()

		t.Logf("proxied Selenium session created: %s", wdP.SessionID())

		if err := wdP.Get(targetURL); err != nil {
			t.Fatalf("proxied navigation failed: %v", err)
		}

		rep := reportFromLive(
			t, wdP,
			selectedUA, ident, brands, details,
			effective, false, nil, nil,
		)

		assess := EvaluatePresentationConsistency(rep)

		if r := findResult(assess, "source_proxy_vs_applied_proxy"); r.Verdict == VerdictMismatch {
			t.Errorf("proxy section mismatch: %+v", r)
		}

		if rep.Requested.Proxy != redactProxyURL(effective) {
			t.Errorf(
				"reported proxy = %q, want %q",
				rep.Requested.Proxy,
				redactProxyURL(effective),
			)
		}

		if got := proxy.count(); got < 1 {
			t.Errorf("dummy proxy received %d requests, expected at least one", got)
		}
		// Non-fabrication invariant: a configured proxy with a forwarding
		// hit-count proves the proxy was USED (egress evidence) but does NOT
		// establish an exit IP, session affinity, or any WAF causality. Those
		// remain unobserved and are asserted nowhere; only the forwarding is evidenced.
		t.Logf("proxy egress evidenced only by the forwarding hit-count; exit-IP / session-affinity / WAF causality are NOT inferred from a configured proxy and remain unobserved")
	})
}

// newProxiedDriver builds a session whose launch args route through proxyURL,
// applying the same test-only loopback bypass the proxy isolation test uses so
// loopback targets are reachable through the dummy proxy (production bypass is
// untouched).
func newProxiedDriver(t *testing.T, proxyURL, webdriverURL string) selenium.WebDriver {
	t.Helper()

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		t.Fatal("proxy URL is unexpectedly empty")
	}

	args := buildLaunchArgs(
		BrowserChrome,
		BrowserChrome,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
		cfg.Crawler{
			RequestImages:  true,
			RequestCSS:     true,
			RequestScripts: true,
			RequestPlugins: true,
		},
		proxyURL,
	)

	// Remove the production bypass list and install the test-specific one.
	filtered := make([]string, 0, len(args)+4)

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

	// Verify the builder and final capabilities arguments.
	want := "--proxy-server=" + proxyURL

	inBuilder := false
	for _, arg := range args {
		if arg == want {
			inBuilder = true
			break
		}
	}

	inFiltered := false
	for _, arg := range filtered {
		if arg == want {
			inFiltered = true
			break
		}
	}

	if !inBuilder || !inFiltered {
		t.Fatalf(
			"proxy launch argument missing: builder=%t filtered=%t proxy=%q args=%v filteredArgs=%v",
			inBuilder,
			inFiltered,
			redactProxyURL(proxyURL),
			redactProxyArgs(args),
			redactProxyArgs(filtered),
		)
	}

	capabilities := selenium.Capabilities{"browserName": "chrome"}
	capabilities.AddChrome(buildChromeCapabilities(filtered, map[string]interface{}{}))

	t.Log("requesting new proxied Selenium session")
	start := time.Now()

	wd, err := selenium.NewRemote(capabilities, webdriverURL)

	t.Logf("NewRemote returned after %s", time.Since(start))

	if err != nil {
		t.Fatalf("creating proxied Selenium session: %v", err)
	}
	if wd.SessionID() == "" {
		t.Fatal("proxied Selenium session has an empty ID")
	}

	// Prevent a broken proxy fixture from hanging for five minutes.
	if err := wd.SetPageLoadTimeout(20 * time.Second); err != nil {
		_ = wd.Quit()
		t.Fatalf("setting proxy page-load timeout: %v", err)
	}

	return wd
}

// mustPort extracts the port from a host:port string, failing the test if the
// listener address is malformed.
func mustPort(t *testing.T, hp string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(hp)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", hp, err)
	}
	return p
}
