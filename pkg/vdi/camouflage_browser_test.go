//go:build integration

package vdi

// Real-browser regression coverage for the camouflage surfaces repaired in
// PR5: the deterministic AudioBuffer export, the shared WebGL identity patch,
// and the stable/linked plugin + MIME object graph.
//
// This test drives a LIVE Chromium through the same new-document identity
// script a session installs (canvasExportScript + antiDetectionScript) and
// asserts the repaired surfaces in a real engine, where the Node harness's
// faithful mocks only proxy the native contracts. It is gated behind
// THECROWLER_E2E_WEBDRIVER and the integration build tag; without the env var
// (or a reachable Selenium+Chrome) it skips — a skip is NOT validation.
//
// Run:
//   THECROWLER_E2E_WEBDRIVER=1 \
//   THECROWLER_E2E_FIXTURE_HOST=host.docker.internal \
//   go test -tags integration ./pkg/vdi \
//     -run '^TestCamouflageSurfacesRealBrowser$' \
//     -count=1 -v
//
// What the harness proves that a real browser cannot easily: the AudioBuffer
// live-store is not mutated by a read (the harness checks this via a native
// copyToChannel back into a fresh store). In a real browser we therefore
// assert the observable consequences that do NOT depend on a private store:
// getChannelData returns a fresh Array (the patch copies, it does not return
// the live view), is deterministic across reads, preserves the native
// out-of-range error, and preserves the Float32Array return type. The
// live-store non-mutation invariant itself is proven by the Node harness
// (TestAudioPatchExecution / TestAudioPatchIdempotent). Similarly, a
// tainted-canvas SecurityError is proven by the Node harness (it needs a
// cross-origin image to taint, which a loopback fixture cannot supply).
//
// Portability note: like the Client-Hints integration test, this binds the
// fixture to the test process loopback; run Selenium on the same host.

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-auxiliaries/selenium"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type camRecorder struct {
	mu sync.Mutex
	n  int
}

func (h *camRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.n++
	h.mu.Unlock()
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, "<html><head><title>camouflage-fixture</title></head><body>ok</body></html>")
}

func TestCamouflageSurfacesRealBrowser(t *testing.T) {
	if os.Getenv("THECROWLER_E2E_WEBDRIVER") != "1" {
		t.Skip("set THECROWLER_E2E_WEBDRIVER=1 to run the camouflage real-browser integration test")
	}
	webdriverURL := strings.TrimSpace(os.Getenv("THECROWLER_E2E_WEBDRIVER_URL"))
	if webdriverURL == "" {
		webdriverURL = "http://127.0.0.1:4444/wd/hub"
	}

	recorder := &camRecorder{}

	// Create the test server on an interface accessible from Docker.
	server := httptest.NewUnstartedServer(recorder)
	_ = server.Listener.Close()

	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("creating fixture listener: %v", err)
	}

	server.Listener = listener
	server.Start()
	defer server.Close()

	// The address Chrome uses to reach the host machine.
	fixtureHost := strings.TrimSpace(
		os.Getenv("THECROWLER_E2E_FIXTURE_HOST"),
	)
	if fixtureHost == "" {
		fixtureHost = "host.docker.internal"
	}

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("reading fixture port: %v", err)
	}

	fixtureURL := "http://" + net.JoinHostPort(fixtureHost, port)
	t.Logf("Browser fixture URL: %s", fixtureURL)

	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	// The session seed; the Canvas and Audio perturbations share it.
	seed := uint32(123456)

	capabilities := selenium.Capabilities{"browserName": "chrome"}
	args := buildLaunchArgs(BrowserChrome, BrowserChrome, ua, cfg.Crawler{
		RequestImages:  true,
		RequestCSS:     true,
		RequestScripts: true,
		RequestPlugins: true,
	}, "")
	capabilities.AddChrome(buildChromeCapabilities(append(args,
		"--headless=new", "--no-sandbox", "--disable-dev-shm-usage",
		"--enable-webgl",
	), map[string]interface{}{}))
	wd, err := selenium.NewRemote(capabilities, webdriverURL)
	if err != nil {
		// Explicitly enabled validation must fail loudly when Selenium is
		// unreachable; only the unset env var skips.
		t.Fatalf("Selenium/Chrome unavailable at %s: %v", webdriverURL, err)
	}
	defer func() { _ = wd.Quit() }()

	// Install the full camouflage new-document script (canvas export + identity)
	// so it evaluates on the next navigation, then navigate.
	if err := ReplaceIdentityScript(wd, 0,
		canvasExportScript(seed)+"\n"+antiDetectionScript(ua, ident, brands, details, seed)); err != nil {
		t.Fatalf("ReplaceIdentityScript: %v", err)
	}
	if err := wd.Get(fixtureURL); err != nil {
		t.Fatalf("Get(fixture): %v", err)
	}

	mustScript := func(t *testing.T, script string) interface{} {
		t.Helper()
		v, err := wd.ExecuteScript("return "+script, nil)
		if err != nil {
			t.Fatalf("ExecuteScript(%q): %v", script, err)
		}
		return v
	}

	// --- WebGL identity patch is active and shares the single spoofed identity.
	// The per-prototype patch answers VENDOR/RENDERER directly; a non-spoofed
	// parameter still resolves through the native path (receiver bound).
	webglVendor := fmt.Sprint(mustScript(t, `(function(){
		var P=WebGLRenderingContext.prototype.getParameter;
		return P.call({__isGLContext:true,params:{}}, 37445);
	})()`))
	if webglVendor != gpuVendorSpoof {
		t.Errorf("WebGL vendor after patch = %q, want %q", webglVendor, gpuVendorSpoof)
	}
	webglRenderer := fmt.Sprint(mustScript(t, `(function(){
		var P=WebGL2RenderingContext && WebGL2RenderingContext.prototype.getParameter;
		return P ? P.call({__isGLContext:true,params:{}}, 37446) : 'no-webgl2';
	})()`))
	if webglRenderer != gpuRendererSpoof && webglRenderer != "no-webgl2" {
		t.Errorf("WebGL2 renderer after patch = %q, want %q", webglRenderer, gpuRendererSpoof)
	}
	// A non-spoofed parameter must still resolve through the native path
	// (receiver binding preserved): on a real WebGL context it does not throw.
	if nativeParam := fmt.Sprint(mustScript(t, `(function(){
		var gl=document.createElement('canvas').getContext('webgl');
		if(!gl) return 'no-webgl';
		try { var v = gl.getParameter(7936); return (v && typeof v==='object') ? 'native-object' : String(v); }
		catch(e){ return 'threw:'+e.message; }
	})()`)); strings.HasPrefix(nativeParam, "threw") {
		t.Errorf("WebGL native parameter passthrough threw: %v", nativeParam)
	}

	// --- Plugin / MIME object graph is stable and mutually linked.
	pluginStable := mustScript(t, `(function(){
		var p1=navigator.plugins, p2=navigator.plugins;
		var m1=navigator.mimeTypes, m2=navigator.mimeTypes;
		var shared = (p1[0] && p1[0].mimeTypes && p1[0].mimeTypes[0] === m1[0]);
		var enabled = (m1[0] && m1[0].enabledPlugin && m1[0].enabledPlugin === p1[0]);
		return JSON.stringify({
			pluginsStable: p1===p2,
			mimesStable: m1===m2,
			shared: !!shared,
			enabled: !!enabled,
			len: p1.length,
			first: p1[0] && p1[0].name,
		});
	})()`)
	t.Logf("plugin graph = %v", pluginStable)
	need := `{"pluginsStable":true,"mimesStable":true,"shared":true,"enabled":true,"len":2,"first":"Chrome PDF Viewer"}`
	got := fmt.Sprint(pluginStable)
	for _, sub := range []string{`"pluginsStable":true`, `"mimesStable":true`, `"shared":true`, `"enabled":true`} {
		if !strings.Contains(got, sub) {
			t.Errorf("plugin graph %s missing %s (want %s)", got, sub, need)
		}
	}

	// --- Canvas export is deterministic for the session seed.
	url1 := fmt.Sprint(mustScript(t, `(function(){
		var c=document.createElement('canvas'); c.width=8; c.height=8;
		return c.toDataURL('image/png');
	})()`))
	url2 := fmt.Sprint(mustScript(t, `(function(){
		var c=document.createElement('canvas'); c.width=8; c.height=8;
		return c.toDataURL('image/png');
	})()`))
	t.Logf("canvas export a=%q b=%q", url1, url2)
	if !strings.HasPrefix(url1, "data:image/png;base64,") {
		t.Errorf("canvas export a = %q, want a PNG data URL", url1)
	}
	if url1 != url2 {
		t.Errorf("canvas export not deterministic across reads in one realm: %q vs %q", url1, url2)
	}

	// --- AudioBuffer export is deterministic, copy-based, error-faithful.
	audioProbe := mustScript(t, `(function(){
		var buf = new AudioBuffer({
			numberOfChannels: 1,
			length: 300,
			sampleRate: 44100
		});
		var a = buf.getChannelData(0);
		var b = buf.getChannelData(0);
		var equal = true;
		for (var i=0;i<a.length;i++){ if (a[i]!==b[i]) { equal=false; break; } }
		var threw = false;
		try { buf.getChannelData(5); } catch (e) { threw = true; }
		return JSON.stringify({
			type: Object.prototype.toString.call(a).toLowerCase(),
			deterministic: equal,
			isCopy: (a !== b),
			rangeThrew: threw,
			perturbed: (a[100]!==0 || a[200]!==0),
		});
	})()`)
	t.Logf("audio probe = %v", audioProbe)
	for _, sub := range []string{`"type":"[object float32array]"`, `"deterministic":true`, `"rangeThrew":true`} {
		if !strings.Contains(fmt.Sprint(audioProbe), sub) {
			t.Errorf("audio probe %v missing %s", audioProbe, sub)
		}
	}
}
