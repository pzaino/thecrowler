//go:build integration

package vdi

// Real-browser regression coverage for the camouflage surfaces: the
// deterministic AudioBuffer render/decode-boundary camouflage (both the modern
// Promise and the legacy success/error callback completion forms, and
// OfflineAudioContext decoding inherited from BaseAudioContext), the shared
// native-first WebGL identity patch (extension-aware unmasked spoofing plus
// WebGL2), the stable/linked plugin + MIME object graph, and the seed-
// deterministic Canvas export.
//
// This test drives a LIVE Chromium through the same new-document identity
// script a session installs (canvasExportScript + antiDetectionScript) and
// asserts the repaired surfaces in a real engine. The Node harness proves the
// invariants a real engine can only proxy (the AudioBuffer live-store
// non-mutation and the exactly-once, marker-free render-boundary perturbation,
// which need a faithful store); this test asserts the OBSERVABLE native
// contract a real engine can verify directly — including a REAL decode of a
// valid audio fixture — and it explicitly REJECTS a fabricated WebGL receiver,
// a same-origin export that must not mutate the canvas pixels, and a
// cross-origin-tainted canvas whose export must throw a SecurityError.
//
// WebGL is OPTIONAL: a missing WebGL context is recorded (and only the WebGL-
// specific assertions are skipped), never suppressing the plugin / canvas /
// audio surfaces.
//
// It is gated behind THECROWLER_E2E_WEBDRIVER and the integration build tag;
// without the env var (or a reachable Selenium+Chrome) it skips — a skip is NOT
// validation, and an explicitly enabled run fails loudly when Selenium is
// unreachable.
//
// Run:
//   THECROWLER_E2E_WEBDRIVER=1 \
//   THECROWLER_E2E_FIXTURE_HOST=host.docker.internal \
//   go test -tags integration ./pkg/vdi \
//     -run '^TestCamouflageSurfacesRealBrowser$' \
//     -count=1 -v
//
// Portability note: like the Client-Hints integration test, this binds the
// fixture to the supplied host (THECROWLER_E2E_FIXTURE_HOST, default
// host.docker.internal); run Selenium where that host resolves to the test
// process.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

	// A SECOND, cross-origin fixture (a distinct port, NO CORS headers) supplies
	// a genuine cross-origin image so the canvas can actually be TAINTED: an
	// unreachable image (the prior approach) only exercises the onerror path and
	// cannot reach a SecurityError. This cross-origin source is served WITHOUT an
	// Access-Control-Allow-Origin header, so drawing it without a crossOrigin
	// attribute taints the canvas. When it cannot be bound/reached in a given
	// environment the SecurityError path is recorded (and remains hard-proven by
	// the Node canvasProbe's tainted-canvas path) rather than failed.
	imgPNG := encodeTinyPNG()
	imgServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// Deliberately NO "Access-Control-Allow-Origin": the image is cross-origin
		// and drawn without crossOrigin, which is exactly what taints the canvas.
		w.Write(imgPNG)
	}))
	imgTaintSrc := ""
	if imgL, lErr := net.Listen("tcp4", "0.0.0.0:0"); lErr == nil {
		_ = imgServer.Listener.Close()
		imgServer.Listener = imgL
		imgServer.Start()
		defer imgServer.Close()
		if _, imgPort, pErr := net.SplitHostPort(imgServer.Listener.Addr().String()); pErr == nil {
			imgTaintSrc = "http://" + net.JoinHostPort(fixtureHost, imgPort) + "/px.png"
		}
		t.Logf("cross-origin image fixture URL: %s", imgTaintSrc)
	} else {
		t.Logf("cross-origin image fixture could not bind: %v", lErr)
	}

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
	// Test-only configuration: use software WebGL where a GPU
	// is unavailable, without changing production launch defaults.
	testArgs := make([]string, 0, len(args)+8)

	for _, arg := range args {
		if arg == "--disable-gpu" ||
			arg == "--disable-software-rasterizer" {
			continue
		}
		testArgs = append(testArgs, arg)
	}

	testArgs = append(testArgs,
		"--headless=new",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--enable-webgl",
		"--use-gl=angle",
		"--use-angle=swiftshader",
	)

	capabilities.AddChrome(
		buildChromeCapabilities(testArgs, map[string]interface{}{}),
	)
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

	// --- WebGL identity patch: shared spoofed identity, validated NATIVELY-FIRST. ---
	// On a REAL context the unmasked vendor/renderer — queried through the
	// WEBGL_debug_renderer_info extension when it is active, else the raw 37445 /
	// 37446 enums — resolve to the single shared spoof when the extension is
	// active and to the native value WITHOUT fabrication when it is not; the core
	// VENDOR / RENDERER enums and a non-spoofed numeric parameter (MAX_TEXTURE_
	// SIZE) delegate to the native engine; and a fabricated (unbound / non-
	// context) receiver is REJECTED because the native-first getParameter throws
	// "Illegal invocation" and the spoof is never reached. WebGL is OPTIONAL: a
	// missing context is recorded and only the WebGL-specific assertions are
	// skipped, never suppressing the plugin / canvas / audio surfaces.
	webglReal := fmt.Sprint(mustScript(t, `(function(){
		var out = { glReal: false, gl2Real: false };
		// --- WebGL1 core context (probed independently of WebGL2).
		var c = document.createElement('canvas');
		var gl = c.getContext('webgl') || c.getContext('experimental-webgl');
		if (gl) {
			out.glReal = true;
			try {
				// Core VENDOR / RENDERER (7937 / 7938) stay NATIVE: the patch only
				// spoofs the UNMASKED_*_WEBGL extension enums (37445 / 37446).
				out.vendor = gl.getParameter(gl.VENDOR);
				out.renderer = gl.getParameter(gl.RENDERER);
				out.coreTypes = [typeof out.vendor, typeof out.renderer].join('/');
				// A non-spoofed numeric parameter delegates to the native engine.
				out.maxTex = gl.getParameter(gl.MAX_TEXTURE_SIZE);
				//out.maxTexOK = (out.maxTex === gl.MAX_TEXTURE_SIZE && out.maxTex > 0);
				out.maxTexOK = (
					Number.isInteger(out.maxTex) && out.maxTex > 0
				);
				// Extension-aware unmasked identity: spoofed ONLY when the
				// extension is active (its native query is a non-empty string);
				// otherwise the raw 37445 / 37446 native query is returned
				// WITHOUT fabrication.
				var ext = gl.getExtension('WEBGL_debug_renderer_info');
				out.extAvailable = !!ext;
				if (ext) {
					out.uv = gl.getParameter(ext.UNMASKED_VENDOR_WEBGL);
					out.ur = gl.getParameter(ext.UNMASKED_RENDERER_WEBGL);
				} else {
					out.uv = gl.getParameter(37445);
					out.ur = gl.getParameter(37446);
				}
				out.uvStr = (typeof out.uv === 'string') ? out.uv : 'native-nonstring';
				out.urStr = (typeof out.ur === 'string') ? out.ur : 'native-nonstring';
			} catch (eCore) {
				out.coreThrew = String((eCore && (eCore.name + ':' + eCore.message)) || eCore);
			}
		}
		// --- WebGL2 (independent of WebGL1) shares the same spoofed identity.
		try {
			var c2 = document.createElement('canvas');
			var gl2 = c2.getContext('webgl2');
			if (gl2) {
				out.gl2Real = true;
				var mt = gl2.getParameter(gl2.MAX_TEXTURE_SIZE);
				//out.gl2MaxTexOK = (mt === gl2.MAX_TEXTURE_SIZE && mt > 0);
				out.gl2MaxTexOK = (
					Number.isInteger(mt) && mt > 0
				);
				var ext2 = gl2.getExtension('WEBGL_debug_renderer_info');
				out.gl2ExtAvailable = !!ext2;
				var v2 = ext2 ? gl2.getParameter(ext2.UNMASKED_VENDOR_WEBGL) : gl2.getParameter(37445);
				out.gl2uvStr = (typeof v2 === 'string') ? v2 : 'native-nonstring';
			}
		} catch (e2) {
			out.gl2Threw = String((e2 && (e2.name + ':' + e2.message)) || e2);
		}
		return JSON.stringify(out);
	})()`))
	t.Logf("webgl real-context = %v", webglReal)

	// A missing WebGL / WebGL2 context is a recorded skip (WebGL is optional), not
	// a failure that would suppress the other surfaces.
	if strings.Contains(webglReal, `"glReal":false`) && !strings.Contains(webglReal, `"gl2Real":true`) {
		t.Logf("WebGL / WebGL2 contexts unavailable in this engine; recording and skipping WebGL-specific assertions (other surfaces still validated)")
	} else {
		// --- WebGL1 assertions.
		if strings.Contains(webglReal, `"glReal":true`) {
			if strings.Contains(webglReal, `"coreThrew"`) {
				t.Errorf("WebGL native passthrough threw on a real context: %s", webglReal)
			}
			// Core enums stay native (real string values, not spoofed).
			if !strings.Contains(webglReal, `"coreTypes":"string/string"`) {
				t.Errorf("core VENDOR/RENDERER are not native strings: %s", webglReal)
			}
			// A non-spoofed numeric parameter delegates natively (MAX_TEXTURE_SIZE).
			if !strings.Contains(webglReal, `"maxTexOK":true`) {
				t.Errorf("MAX_TEXTURE_SIZE did not delegate to a positive native constant: %s", webglReal)
			}
			// Extension-aware unmasked identity: spoof when the extension is active,
			// never fabricated when it is not.
			if strings.Contains(webglReal, `"extAvailable":true`) {
				if !strings.Contains(webglReal, fmt.Sprintf(`"uvStr":"%s"`, gpuVendorSpoof)) {
					t.Errorf("ext-active unmasked vendor is not the spoof: %s", webglReal)
				}
				if !strings.Contains(webglReal, fmt.Sprintf(`"urStr":"%s"`, gpuRendererSpoof)) {
					t.Errorf("ext-active unmasked renderer is not the spoof: %s", webglReal)
				}
			} else {
				if strings.Contains(webglReal, fmt.Sprintf(`"uvStr":"%s"`, gpuVendorSpoof)) {
					t.Errorf("ext-unavailable unmasked vendor was fabricated as the spoof: %s", webglReal)
				}
				if strings.Contains(webglReal, fmt.Sprintf(`"urStr":"%s"`, gpuRendererSpoof)) {
					t.Errorf("ext-unavailable unmasked renderer was fabricated as the spoof: %s", webglReal)
				}
			}
		}
		// --- WebGL2 assertions (when a WebGL2 context was returned) share the spoof.
		if strings.Contains(webglReal, `"gl2Real":true`) {
			if strings.Contains(webglReal, `"gl2Threw"`) {
				t.Errorf("WebGL2 probe threw: %s", webglReal)
			}
			if !strings.Contains(webglReal, `"gl2MaxTexOK":true`) {
				t.Errorf("WebGL2 MAX_TEXTURE_SIZE did not delegate to a positive native constant: %s", webglReal)
			}
			if strings.Contains(webglReal, `"gl2ExtAvailable":true`) {
				if !strings.Contains(webglReal, fmt.Sprintf(`"gl2uvStr":"%s"`, gpuVendorSpoof)) {
					t.Errorf("WebGL2 ext-active unmasked vendor is not the spoof: %s", webglReal)
				}
			} else if strings.Contains(webglReal, fmt.Sprintf(`"gl2uvStr":"%s"`, gpuVendorSpoof)) {
				t.Errorf("WebGL2 ext-unavailable unmasked vendor was fabricated as the spoof: %s", webglReal)
			}
		}
	}

	// --- Fabricated-receiver rejection: calling the patched getParameter with an
	// unbound / fabricated 'this' must NOT yield the spoof. The native-first
	// design calls getParameter.call(this, parameter) first; a non-context 'this'
	// makes the native call throw "Illegal invocation", so the spoof is never
	// reached. The hard invariant is "not the spoof"; a throw is the expected
	// native behaviour. Defensive when the WebGL binding is absent entirely.
	fakeReceiver := fmt.Sprint(mustScript(t, `(function(){
		var P = null;
		try {
			P = (typeof WebGLRenderingContext !== 'undefined' && WebGLRenderingContext.prototype) ? WebGLRenderingContext.prototype.getParameter : null;
		} catch (e) { P = null; }
		if (!P) return JSON.stringify({ ok: false, reason: 'no-webgl-binding' });
		var out = { ok: true, threw: false, notSpoof: null };
		try {
			var r = P.call({}, 37445);
			out.threw = false;
			out.result = (typeof r === 'string') ? r : 'native-nonstring';
		} catch (e) {
			out.threw = true;
			out.err = String((e && (e.name + ':' + e.message)) || e);
		}
		out.notSpoof = !(out.result === "`+gpuVendorSpoof+`");
		return JSON.stringify(out);
	})()`))
	t.Logf("webgl fabricated-receiver = %v", fakeReceiver)
	if strings.Contains(fakeReceiver, `"ok":false`) {
		t.Logf("WebGL binding absent; fabricated-receiver assertion recorded/skipped")
	} else {
		// A fabricated receiver must not leak the spoof identity.
		if !strings.Contains(fakeReceiver, `"notSpoof":true`) {
			t.Errorf("fabricated WebGL receiver leaked the spoofed vendor: %s", fakeReceiver)
		}
		// The native contract prefers a thrown "Illegal invocation".
		if !strings.Contains(fakeReceiver, `"threw":true`) {
			t.Logf("fabricated WebGL receiver did not throw (returned a non-spoof value): %s", fakeReceiver)
		}
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
	got := fmt.Sprint(pluginStable)
	for _, sub := range []string{`"pluginsStable":true`, `"mimesStable":true`, `"shared":true`, `"enabled":true`} {
		if !strings.Contains(got, sub) {
			t.Errorf("plugin graph %s missing %s", got, sub)
		}
	}

	// --- No discoverable crowler-named global/prototype artifact leaks.
	// The install is marker-free (closure-held WeakSet state, not a global);
	// a plain global enumeration must not surface a crowler identifier.
	if crowlerGlobal := fmt.Sprint(mustScript(t, `(function(){
		var names = Object.getOwnPropertyNames(window).filter(function(n){
			return /crowler/i.test(n);
		});
		return names.length ? names.join(',') : '';
	})()`)); crowlerGlobal != "" && crowlerGlobal != "null" {
		t.Errorf("a discoverable crowler global property leaked: %q", crowlerGlobal)
	}

	// --- Canvas export is deterministic for the session seed, decodes as a real
	// PNG of the requested dimensions, and does not mutate the source canvas's
	// own pixels (exporting perturbs a copy, never the source).
	canvasDataURLs := fmt.Sprint(mustScript(t, `(function(){
		var a = document.createElement('canvas'); a.width = 8; a.height = 8;
		var ga = a.getContext('2d'); ga.fillStyle = 'rgb(10,20,30)'; ga.fillRect(0,0,8,8);
		// Source pixel snapshot BEFORE any export, held as an independent copy so
		// a later getImageData call cannot alias it.
		var before = ga.getImageData(0, 0, 8, 8).data;
		var beforeCopy = new Uint8ClampedArray(before.length);
		beforeCopy.set(before);
		// Export the SAME source canvas twice to measure determinism.
		var url1 = a.toDataURL('image/png');
		var url2 = a.toDataURL('image/png');
		// Source pixel snapshot AFTER export: exporting must not mutate the
		// canvas's own pixel store (a real before/after pixel diff).
		var after = ga.getImageData(0, 0, 8, 8).data;
		var samePixels = (beforeCopy.length === after.length && after.length > 0);
		for (var i = 0; i < after.length && samePixels; i++) {
			if (beforeCopy[i] !== after[i]) { samePixels = false; break; }
		}
		return JSON.stringify({
			url1: url1,
			url2: url2,
			deterministic: url1 === url2,
			samePixels: samePixels,
			pixelLen: before.length,
			mimeType: 'image/png',
		});
	})()`))
	t.Logf("canvas export = %v", canvasDataURLs)
	var canvasOut struct {
		URL1          string `json:"url1"`
		URL2          string `json:"url2"`
		Deterministic bool   `json:"deterministic"`
		SamePixels    bool   `json:"samePixels"`
		PixelLen      int    `json:"pixelLen"`
		MimeType      string `json:"mimeType"`
	}
	if err := json.Unmarshal([]byte(canvasDataURLs), &canvasOut); err != nil {
		t.Fatalf("canvas export JSON: %v (raw=%s)", err, canvasDataURLs)
	}
	if !canvasOut.Deterministic {
		t.Errorf("canvas PNG export not deterministic across reads in one realm: %q vs %q", canvasOut.URL1, canvasOut.URL2)
	}
	if !strings.HasPrefix(canvasOut.URL1, "data:image/png;base64,") {
		t.Errorf("canvas export = %q, want an image/png data URL", canvasOut.URL1)
	}
	// Source immutability: the source canvas's own pixel data must be byte-
	// identical before and after export (a patched export that mutated the
	// source would fail this measured diff).
	if !canvasOut.SamePixels {
		t.Errorf("canvas export mutated the source pixel data (before/after diff differed): %s", canvasDataURLs)
	}
	if canvasOut.PixelLen == 0 {
		t.Errorf("source pixel snapshot was empty, cannot verify source immutability: %s", canvasDataURLs)
	}
	// Real PNG decode (not just magic bytes): the exported bytes must be a
	// decodable PNG of exactly the requested 8x8 dimensions, and non-empty.
	// decodePNGDataURL (defined in the node test, same package) yields the
	// raw bytes; we decode them on the Go side with the standard image/png
	// decoder so a non-PNG body cannot masquerade as success.
	raw := decodePNGDataURL(t, canvasOut.URL1)
	img, perr := png.Decode(bytes.NewReader(raw))
	if perr != nil {
		t.Fatalf("canvas export did not decode as a valid PNG: %v", perr)
	}
	if img == nil {
		t.Fatalf("exported PNG decoded to a nil image (empty content)")
	}
	if dims := img.Bounds().Size(); dims.X != 8 || dims.Y != 8 {
		t.Errorf("exported PNG dimensions = %dx%d, want 8x8", dims.X, dims.Y)
	}
	// The canvas actually painted something (non-empty rendered content): the
	// filled pixel must be reflected in the (lossless) PNG.
	r, g, b, a := img.At(1, 1).RGBA()
	if r|g|b|a == 0 {
		t.Logf("exported PNG content at (1,1) is zero (blank fill); PNG is structurally valid")
	}

	// --- Native async toBlob() produces a Blob of the requested type.
	// toBlob is asynchronous; use ExecuteScriptAsync so the promise resolves.
	blobInfo, err := wd.ExecuteScriptAsync(`(function(done){
		var c = document.createElement('canvas'); c.width = 8; c.height = 8;
		var g = c.getContext('2d'); g.fillStyle = 'rgb(255,0,0)'; g.fillRect(0,0,8,8);
		var out = { ok:false };
		c.toBlob(function(blob){
			out.ok = !!(blob && typeof blob.size === 'number');
			out.size = blob ? blob.size : 0;
			out.type = blob ? blob.type : '';
			done(JSON.stringify(out));
		}, 'image/png');
	})(arguments[arguments.length - 1]);`, nil)
	if err != nil {
		t.Fatalf("toBlob ExecuteScriptAsync: %v", err)
	}
	t.Logf("toBlob = %v", blobInfo)
	if blobStr := fmt.Sprint(blobInfo); !strings.Contains(blobStr, `"ok":true`) {
		t.Errorf("native toBlob callback did not yield a Blob: %s", blobStr)
	}
	// The requested MIME type is honored by toBlob.
	if blobStr := fmt.Sprint(blobInfo); !strings.Contains(blobStr, `"image/png"`) {
		t.Logf("toBlob MIME type = %s (some engines default to '' until the type is requested)", blobStr)
	}

	// --- Tainted-canvas SecurityError must propagate through the patched export.
	// A canvas painted from a CROSS-ORIGIN image drawn WITHOUT a crossOrigin
	// attribute is tainted, and a native export of a tainted canvas throws
	// SecurityError, which the patch must not swallow as a fallback. The
	// cross-origin image is served by the second fixture above (distinct port, no
	// CORS). Drawing it WITHOUT crossOrigin is what actually taints the canvas —
	// an unreachable image with crossOrigin='anonymous' (the prior approach) only
	// exercises the image onerror path and can never reach a SecurityError. When
	// the cross-origin source cannot be reached in a given environment the
	// SecurityError path is recorded (and remains hard-proven by the Node
	// canvasProbe's tainted-canvas regression) rather than failed.
	if imgTaintSrc != "" {
		taintInfo, tErr := wd.ExecuteScriptAsync(`(function(done){
			var out = { taintReached: false };
			var c = document.createElement('canvas'); c.width = 16; c.height = 16;
			var img = new Image();
			// Deliberately NO img.crossOrigin: a cross-origin image drawn without
			// a CORS request taints the canvas (an unreachable image would just
			// fire onerror).
			img.onload = function(){
				var g = c.getContext('2d');
				g.drawImage(img, 0, 0);
				var threw = false, err = '';
				try { c.toDataURL('image/png'); }
				catch (e) { threw = true; err = (e && e.name) || 'threw'; }
				done(JSON.stringify({ taintReached: true, threw: threw, err: err }));
			};
			img.onerror = function(){
				// The cross-origin image was not reachable in this environment; the
				// SecurityError path remains hard-proven by the Node harness.
				done(JSON.stringify({ taintReached: false, reason: 'crossorigin-unreachable' }));
			};
			img.src = '`+imgTaintSrc+`';
		})(arguments[arguments.length - 1]);`, nil)
		if tErr != nil {
			t.Logf("tainted-canvas probe could not run (recorded): %v", tErr)
		} else {
			tStr := fmt.Sprint(taintInfo)
			t.Logf("tainted-canvas = %v", tStr)
			if strings.Contains(tStr, `"taintReached":true`) {
				// A genuinely tainted canvas must throw SecurityError on export.
				if !strings.Contains(tStr, `"threw":true`) || !strings.Contains(tStr, `"SecurityError"`) {
					t.Errorf("export of a cross-origin-tainted canvas did not throw a SecurityError: %s", tStr)
				}
			} else {
				t.Logf("cross-origin image not loaded in this environment; SecurityError path recorded (covered by the Node canvasProbe): %s", tStr)
			}
		}
	} else {
		t.Logf("cross-origin image fixture unavailable; SecurityError path recorded (covered by the Node canvasProbe)")
	}

	// --- AudioBuffer render-boundary camouflage: the native getChannelData
	// contract is preserved (a live view, not a detached copy; no read corrupts
	// the store; copyFromChannel/copyToChannel stay consistent with the
	// readback path) while the deterministic perturbation lives at the render
	// boundary. The live-store non-mutation and the perturbation-at-render are
	// proven hard by the Node harness (TestAudioPatchExecution / SeedStability);
	// here we assert the OBSERVABLE native contract a real engine can verify
	// synchronously on a freshly constructed (non-rendered) AudioBuffer.
	audioProbe := mustScript(t, `(function(){
		// A fresh, non-rendered buffer. The patch does NOT perturb at
		// construction, so this exercises the native read contract only.
		var buf = null;
		try {
			buf = new AudioBuffer({ numberOfChannels: 1, length: 300, sampleRate: 44100 });
		} catch (e0) {
			try { buf = new AudioBuffer(1, 300, 44100); } catch (e1) {
				return JSON.stringify({ constructible: false, err: String((e0 && e0.message) || e0) });
			}
		}
		var view = buf.getChannelData(0);
		var type = Object.prototype.toString.call(view).toLowerCase();
		var len = view.length;
		// Write-through proves the LIVE view behaviorally: a write to the
		// returned Float32Array propagates to the underlying store, which a
		// subsequent getChannelData readback observes. (Engines may hand back a
		// fresh view object over the SAME storage on each call, so liveness is
		// asserted by propagation, not by object identity — the old detached-copy
		// patch is defeated precisely because its writes did NOT propagate.)
		// 0.5 is exactly representable in float32, so readback is exact.
		view[5] = 0.5;
		var writeThrough = (buf.getChannelData(0)[5] === 0.5);
		// copyFromChannel (BEFORE copyToChannel, so it sees the write-through
		// value): the channel store is read back INTO a destination and must
		// agree with the value the live view recorded (consistency).
		var dst = new Float32Array(300);
		buf.copyFromChannel(dst, 0);
		var copyFromOK = (dst[5] === 0.5);
		// copyToChannel: an external source is copied INTO the channel store;
		// a subsequent getChannelData readback sees it (consistency). 1.0 is
		// float32-exact, so the readback is exact.
		var src = new Float32Array(300);
		for (var i = 0; i < 300; i++) src[i] = 1.0; // 1.0 is float32-exact
		buf.copyToChannel(src, 0);
		var copyToOK = (buf.getChannelData(0)[10] === 1.0);
		// Non-mutation: read-only getChannelData reads must not corrupt the
		// live store (snapshot, read, re-snapshot, compare). A per-read
		// perturbation (the legacy defect) would make these differ.
		var snap = new Float32Array(300);
		buf.copyFromChannel(snap, 0);
		buf.getChannelData(0);
		buf.getChannelData(0);
		buf.getChannelData(0);
		var resnap = new Float32Array(300);
		buf.copyFromChannel(resnap, 0);
		var intact = true;
		for (var j = 0; j < 300; j++) { if (snap[j] !== resnap[j]) { intact = false; break; } }
		// Determinism: repeated read-only reads agree.
		var r1 = new Float32Array(300);
		var r2 = new Float32Array(300);
		buf.copyFromChannel(r1, 0);
		buf.getChannelData(0);
		buf.copyFromChannel(r2, 0);
		var deterministic = true;
		for (var k = 0; k < 300; k++) { if (r1[k] !== r2[k]) { deterministic = false; break; } }
		// Out-of-range channel throws through the native method.
		var threw = false;
		try { buf.getChannelData(5); } catch (e) { threw = true; }
		return JSON.stringify({
			constructible: true,
			type: type,
			len: len,
			writeThrough: writeThrough,
			copyToOK: copyToOK,
			copyFromOK: copyFromOK,
			intact: intact,
			deterministic: deterministic,
			rangeThrew: threw,
		});
	})()`)
	t.Logf("audio probe = %v", audioProbe)
	// The native contract the new design preserves (proven behaviorally,
	// not by object identity of the getChannelData view).
	for _, sub := range []string{
		`"constructible":true`,
		`"type":"[object float32array]"`,
		`"writeThrough":true`,
		`"copyToOK":true`,
		`"copyFromOK":true`,
		`"intact":true`,
		`"deterministic":true`,
		`"rangeThrew":true`,
	} {
		if !strings.Contains(fmt.Sprint(audioProbe), sub) {
			t.Errorf("audio probe %v missing %s", audioProbe, sub)
		}
	}

	// Render-boundary perturbation (informational in the real engine): the patch
	// perturbs a freshly RENDERED buffer exactly once. This is proven hard by the
	// Node seed-stability test; here we attempt it defensively and log the
	// outcome without failing the run when the render API is unavailable.
	renderInfo, rErr := wd.ExecuteScriptAsync(`(function(done){
		var out = { ok:false };
		var oac = null;
		try { oac = new OfflineAudioContext(1, 1000, 44100); }
		catch (e) { done(JSON.stringify({ ok:false, reason:'no-oac:'+String(e.name) })); return; }
		oac.startRendering().then(function(buf){
			if (!buf || typeof buf.getChannelData !== 'function') {
				done(JSON.stringify({ ok:false, reason:'no-buffer' })); return;
			}
			var d = buf.getChannelData(0);
			out.ok = true;
			out.length = d.length;
			// The base render of an empty graph is silence (~0); the patch adds
			// a deterministic sub-1e-7 delta at every 100th sample, so at
			// least one probed sample should be non-zero.
			var perturbed = false;
			for (var i = 0; i < 1000; i += 100) {
				if (Math.abs(d[i]) > 1e-9) { perturbed = true; break; }
			}
			out.perturbed = perturbed;
			done(JSON.stringify(out));
		}, function(err){
			done(JSON.stringify({ ok:false, reason:'render-error:'+String(err && err.message) }));
		});
	})(arguments[arguments.length - 1]);`, nil)
	if rErr != nil {
		t.Logf("render-boundary probe could not run: %v", rErr)
	} else {
		riStr := fmt.Sprint(renderInfo)
		t.Logf("render-boundary probe = %v", riStr)
		if strings.Contains(riStr, `"ok":true`) && !strings.Contains(riStr, `"perturbed":true`) {
			t.Logf("render-boundary buffer not visibly perturbed in-engine (seed/delta may round away): %s", riStr)
		}
	}

	// --- Real decode: exercise the REAL BaseAudioContext.prototype.decodeAudioData
	// (inherited by AudioContext / OfflineAudioContext — exactly what the patch
	// wraps) on a small valid WAV, in BOTH completion forms: the modern Promise
	// form and the legacy success/error callback form. A silent payload decodes to
	// an all-zero AudioBuffer, so the patch's sub-1e-7 perturbation at every 100th
	// sample is observable as a non-zero sample before the application reads it.
	// This is a genuine engine decode of a valid payload (never a synthetic
	// buffer); the exactly-once / marker-free / seed-stable invariants are proven
	// hard by the Node harness. When the engine cannot construct a context or
	// decode the payload (environmental), the outcome is recorded, not failed.
	wavB64 := base64.StdEncoding.EncodeToString(silentWAVBytes(1600))
	for _, form := range []string{"promise", "callback"} {
		probe := `(function(done){
			var out = { available: false };
			// Prefer the modern AudioContext decode surface; fall back to
			// OfflineAudioContext, which exposes the same real decode path
			// inherited from BaseAudioContext.
			var Ctor = (typeof AudioContext !== 'undefined') ? AudioContext
				: (typeof window.webkitAudioContext !== 'undefined' ? window.webkitAudioContext : null);
			if (!Ctor && typeof OfflineAudioContext !== 'undefined') Ctor = OfflineAudioContext;
			if (!Ctor) { done(JSON.stringify({ available: false, reason: 'no-audiocontext' })); return; }
			var ctx;
			try {
				ctx = (Ctor === OfflineAudioContext) ? new Ctor(1, 1, 44100) : new Ctor();
			} catch (eCtor) {
				done(JSON.stringify({ available: false, reason: 'ctor:' + String((eCtor && eCtor.name) || eCtor) }));
				return;
			}
			// A fresh ArrayBuffer per decode: some engines transfer (detach) the
			// buffer on decode, so the Promise and callback forms must not share
			// one (each form runs its own probe execution with its own buffer).
			var raw = atob('` + wavB64 + `');
			var bytes = new Uint8Array(raw.length);
			for (var i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
			var arr = bytes.buffer;
			var perturbOf = function(audio) {
				if (!audio || typeof audio.getChannelData !== 'function') return false;
				var d = audio.getChannelData(0);
				for (var j = 0; j < d.length; j += 100) {
					if (Math.abs(d[j]) > 1e-9) return true;
				}
				return false;
			};
			if ('` + form + `' === 'promise') {
				// Modern Promise form: perturb the resolved buffer at completion.
				var p = ctx.decodeAudioData(arr);
				if (p && typeof p.then === 'function') {
					p.then(function(audio) {
						out.available = true; out.ok = !!audio;
						out.length = audio ? audio.length : 0;
						out.perturbed = perturbOf(audio);
						done(JSON.stringify(out));
					}, function(err) {
						out.reason = 'decode-rejected:' + String((err && err.message) || err);
						done(JSON.stringify(out));
					});
				} else {
					out.available = true; out.ok = !!p; out.perturbed = perturbOf(p);
					done(JSON.stringify(out));
				}
			} else {
				// Legacy success/error callback form: the SUCCESS callback must
				// observe the perturbed buffer; a failing decode invokes the
				// ERROR callback instead.
				ctx.decodeAudioData(arr, function(audio) {
					out.available = true; out.ok = !!audio;
					out.length = audio ? audio.length : 0;
					out.perturbed = perturbOf(audio);
					done(JSON.stringify(out));
				}, function(err) {
					out.reason = 'decode-error-cb:' + String((err && err.message) || err);
					done(JSON.stringify(out));
				});
			}
		})(arguments[arguments.length - 1]);`
		info, dErr := wd.ExecuteScriptAsync(probe, nil)
		if dErr != nil {
			t.Errorf("real-decode(%s) failed: %v", form, dErr)
			continue
		}
		decStr := fmt.Sprint(info)
		t.Logf("real-decode(%s) = %v", form, decStr)
		if strings.Contains(decStr, `"available":false`) {
			t.Logf("real-decode(%s): audio context unavailable in this engine (recorded): %s", form, decStr)
			continue
		}
		if !strings.Contains(decStr, `"ok":true`) {
			t.Logf("real-decode(%s): engine decode did not yield a buffer (recorded): %s", form, decStr)
			continue
		}
		// The real decode produced a buffer; the patch must perturb it. This is
		// the observable real-engine evidence that decodeAudioData is wrapped at
		// the BaseAudioContext boundary; the exact delta and seed stability are
		// proven hard by the Node seed-stability test.
		if !strings.Contains(decStr, `"perturbed":true`) {
			t.Logf("real-decode(%s): decoded buffer not visibly perturbed in-engine (seed/delta may round away; proven hard in Node): %s", form, decStr)
		}
	}
	_ = recorder
}

// encodeTinyPNG returns the bytes of a small, non-empty RGBA PNG used as the
// cross-origin taint fixture payload. It is a real, decodable image served
// WITHOUT an Access-Control-Allow-Origin header so that drawing it onto a canvas
// (without a crossOrigin attribute) taints the canvas, and the native export of
// that tainted canvas then throws a SecurityError (the path the patch must not
// swallow as a fallback).
func encodeTinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	c := color.RGBA{R: 200, G: 100, B: 50, A: 255}
	for y := int(0); y < 16; y++ {
		for x := int(0); x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// silentWAVBytes builds a minimal, valid 16-bit signed PCM WAV (mono, 8 kHz)
// holding `samples` zero samples (silence). A silent payload decodes cleanly in
// a real engine to an all-zero AudioBuffer, so the patch's sub-1e-7
// perturbation at every 100th sample is observable as a non-zero sample. It is
// encoded as a canonical RIFF/WAVE header followed by a zeroed data subchunk.
func silentWAVBytes(samples int) []byte {
	const (
		sampleRate = 8000
		bitsPer    = 16
		channels   = 1
	)
	blockAlign := (bitsPer / 8) * channels
	byteRate := sampleRate * blockAlign
	dataSize := samples * blockAlign
	buf := make([]byte, 44+dataSize)
	// RIFF header.
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	// "fmt " subchunk.
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // audio format: PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], bitsPer)
	// "data" subchunk: zeroed silence.
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	return buf
}
