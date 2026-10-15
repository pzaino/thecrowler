package vdi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Node-executed camouflage contract tests. Patch scripts built by the Go
// builders run in a stub DOM where native methods enforce receiver contracts,
// so unbound calls, descriptor conflicts, and invalid API shapes fail exactly
// as a real browser would surface them. Tests skip when node is unavailable;
// pure-Go assertions in camouflage_test.go always run.

func nodeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

type harnessResult struct {
	Phase      string                 `json:"phase"`
	Threw      *string                `json:"threw"`
	Errors     []string               `json:"errors"`
	Values     map[string]interface{} `json:"values"`
	LineErrors []string               `json:"lineErrors"`
}

func runCamouflageNode(t *testing.T, phase, script string) harnessResult {
	t.Helper()
	return runCamouflageNodePayload(t, phase, []byte(script))
}

func runCamouflageNodeLines(t *testing.T, scripts []string) harnessResult {
	t.Helper()
	payload, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	return runCamouflageNodePayload(t, "navigator-lines", payload)
}

func runCamouflageNodePayload(t *testing.T, phase string, payload []byte) harnessResult {
	return runCamouflageNodePayloadEnv(t, phase, payload, nil)
}

func runCamouflageNodePayloadEnv(t *testing.T, phase string, payload []byte, extraEnv []string) harnessResult {
	return runCamouflageNodeScript(t, phase, payload, extraEnv, true)
}

// runCamouflageNodeMaybeThrow runs the harness WITHOUT treating a top-level
// throw as a test failure, returning the raw result so a test can ASSERT that a
// legacy defect (notably a missing-API throw that aborts surrounding
// initialization) is observable. The legacy-defect control relies on this.
func runCamouflageNodeMaybeThrow(t *testing.T, phase, script string) harnessResult {
	t.Helper()
	return runCamouflageNodeScript(t, phase, []byte(script), nil, false)
}

func runCamouflageNodeScript(t *testing.T, phase string, payload []byte, extraEnv []string, fatalOnThrow bool) harnessResult {
	t.Helper()
	if !nodeAvailable() {
		t.Skip("node binary not available for patch-execution tests")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.js")
	if err := os.WriteFile(scriptPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join("testdata", "camouflage_harness.js"), phase, scriptPath)
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node harness (%s): %v\n%s", phase, err, stderr.String())
	}
	var res harnessResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); err != nil {
		t.Fatalf("decode harness output: %v\n%s", err, stdout.String())
	}
	if fatalOnThrow && res.Threw != nil {
		t.Fatalf("patch threw at top level: %s", *res.Threw)
	}
	return res
}

func checkScriptSyntax(t *testing.T, name, script string) {
	t.Helper()
	if !nodeAvailable() {
		t.Skip("node binary not available for syntax checks")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.js")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "--check", scriptPath).CombinedOutput(); err != nil {
		t.Errorf("syntax check %s: %v\n%s", name, err, out)
	}
}

func decodePNGDataURL(t *testing.T, value string) []byte {
	t.Helper()
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("not a PNG data URL: %q", value)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		t.Fatalf("data URL body is not valid base64: %v", err)
	}
	return raw
}

func assertValidPNG(t *testing.T, value string) {
	t.Helper()
	raw := decodePNGDataURL(t, value)
	magic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(raw) < 8 || string(raw[:8]) != string(magic) {
		t.Errorf("data URL does not decode to a PNG image (%d bytes)", len(raw))
	}
}

func TestPatchScriptsSyntax(t *testing.T) {
	ident := MapAdvertisedOSIdentity("windows", false)
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	checkScriptSyntax(t, "reinforce", reinforceScript())
	checkScriptSyntax(t, "gpu", gpuPatchScript())
	checkScriptSyntax(t, "load-listener", loadListenerScript())
	checkScriptSyntax(t, "navigator", strings.Join(navigatorPropertyScripts("en", ua, ident), "\n"))
	checkScriptSyntax(t, "inject", antiDetectionScript(ua, ident, brands, details, 0))
	checkScriptSyntax(t, "plugins", pluginArrayLiteral())
	checkScriptSyntax(t, "mime", mimeArrayLiteral())
	checkScriptSyntax(t, "webgl", webglSpoofScript())
	checkScriptSyntax(t, "audio", audioPatchScript(123456))
}

func TestReinforceExecution(t *testing.T) {
	res := runCamouflageNode(t, "reinforce", reinforceScript())
	if len(res.Errors) != 0 {
		t.Errorf("reinforce logged stage errors: %v", res.Errors)
	}
	v := res.Values
	if v["webdriver"] != nil {
		t.Errorf("navigator.webdriver = %v, want undefined", v["webdriver"])
	}
	if v["pluginsLength"] != float64(2) {
		t.Errorf("plugins.length = %v, want 2", v["pluginsLength"])
	}
	if v["plugin0"] != "Chrome PDF Viewer" {
		t.Errorf("plugins[0].name = %v", v["plugin0"])
	}
	if v["pluginItem"] != "function" || v["pluginNamedItem"] != "function" {
		t.Errorf("plugin methods = %v/%v, want functions", v["pluginItem"], v["pluginNamedItem"])
	}
	if v["pluginItem0"] != "Chrome PDF Viewer" {
		t.Errorf("plugins.item(0).name = %v", v["pluginItem0"])
	}
	if v["pluginMimeType"] != "application/pdf" {
		t.Errorf("plugins[0].item(0).type = %v", v["pluginMimeType"])
	}
	if v["devicePixelRatio"] != float64(1) {
		t.Errorf("devicePixelRatio = %v, want 1", v["devicePixelRatio"])
	}
	assertValidPNG(t, strVal(t, v["toDataURL"]))
	if v["webglVendor"] != "Intel Inc." || v["webglRenderer"] != "Mesa Intel(R) UHD Graphics" {
		t.Errorf("webgl = %v/%v", v["webglVendor"], v["webglRenderer"])
	}
	if v["webglPassthrough"] != "other" {
		t.Errorf("webgl passthrough = %v (native binding broken)", v["webglPassthrough"])
	}
	if v["webgl2Patched"] != true {
		t.Error("WebGL2 prototype not patched")
	}
	if v["attachShadowNull"] != false || v["attachShadowType"] != "object" {
		t.Errorf("attachShadow broken: null=%v type=%v", v["attachShadowNull"], v["attachShadowType"])
	}
	if v["permsNotifications"] != "denied" {
		t.Errorf("notifications permission = %v, want denied", v["permsNotifications"])
	}
	if v["permsCamera"] != "prompt" {
		t.Errorf("camera permission = %v, want delegated prompt (binding broken?)", v["permsCamera"])
	}
	if v["stackTraceLimit"] != float64(0) {
		t.Errorf("stackTraceLimit = %v", v["stackTraceLimit"])
	}
}

func TestReinforceTwiceIdempotent(t *testing.T) {
	// Repeated initialization (setupBrowser runs per URL) must not throw:
	// every redefined property is configurable.
	res := runCamouflageNode(t, "reinforce-twice", reinforceScript())
	if len(res.Errors) != 0 {
		t.Errorf("second reinforce run logged errors: %v", res.Errors)
	}
	if res.Values["webdriver"] != nil {
		t.Errorf("webdriver survived twice-run as %v", res.Values["webdriver"])
	}
}

func TestGPUPatchExecution(t *testing.T) {
	res := runCamouflageNode(t, "gpu", gpuPatchScript())
	if len(res.Errors) != 0 {
		t.Errorf("gpu patch logged errors: %v", res.Errors)
	}
	v := res.Values
	if v["gpuCtxVendor"] != "Intel Inc." || v["gpuCtxRenderer"] != "Mesa Intel(R) UHD Graphics" {
		t.Errorf("gpu context = %v/%v", v["gpuCtxVendor"], v["gpuCtxRenderer"])
	}
	if v["gpuCtxPassthrough"] != "passthrough-value" {
		t.Errorf("gpu passthrough = %v", v["gpuCtxPassthrough"])
	}
	if v["gpuNullPassthrough"] != true {
		t.Error("null WebGL context must pass through without throwing")
	}
}

func TestNavigatorLinesExecution(t *testing.T) {
	ua := `Test "UA" \\ with quotes`
	ident := MapAdvertisedOSIdentity("windows", false)
	scripts := navigatorPropertyScripts("en-gb", ua, ident)
	res := runCamouflageNodeLines(t, scripts)
	if len(res.LineErrors) != 0 {
		t.Errorf("navigator lines failed: %v", res.LineErrors)
	}
	v := res.Values
	if v["userAgent"] != ua {
		t.Errorf("userAgent round-trip = %v", v["userAgent"])
	}
	if v["platform"] != "Win32" {
		t.Errorf("platform = %v, want Win32", v["platform"])
	}
	if v["deviceMemory"] != float64(8) || v["hardwareConcurrency"] != float64(4) {
		t.Errorf("device = %v/%v", v["deviceMemory"], v["hardwareConcurrency"])
	}
	if v["pluginsLength"] != float64(2) || v["pluginItem"] != "function" {
		t.Errorf("plugins shape = %+v", v)
	}
}

func TestLoadListenerExecution(t *testing.T) {
	res := runCamouflageNode(t, "loadlistener", loadListenerScript())
	if len(res.Errors) != 0 {
		t.Errorf("load listener logged errors: %v", res.Errors)
	}
	if res.Values["webdriver"] != nil {
		t.Errorf("webdriver after load = %v", res.Values["webdriver"])
	}
	if res.Values["pluginsLength"] != float64(2) {
		t.Errorf("plugins after load = %v", res.Values["pluginsLength"])
	}
}

func TestInjectExecution(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	res := runCamouflageNode(t, "inject", antiDetectionScript(ua, ident, brands, details, 7))
	if len(res.Errors) != 0 {
		t.Errorf("inject logged errors: %v", res.Errors)
	}
	v := res.Values
	if v["userAgent"] != ua || v["platform"] != "Win32" {
		t.Errorf("identity = %v/%v", v["userAgent"], v["platform"])
	}
	if v["uadPlatform"] != "Windows" {
		t.Errorf("userAgentData.platform = %v", v["uadPlatform"])
	}
	if v["webgl2Patched"] != true {
		t.Error("inject must patch WebGL2 as well")
	}
	if v["pluginItem0"] != "Chrome PDF Viewer" {
		t.Errorf("inject plugins.item(0).name = %v", v["pluginItem0"])
	}
	if v["cdcDeleted"] != true {
		t.Error("chromedriver markers not deleted")
	}
	he, ok := v["highEntropy"].(map[string]interface{})
	if !ok {
		t.Fatalf("highEntropy = %v, want object", v["highEntropy"])
	}
	for k, want := range map[string]interface{}{
		"platformVersion": "10.0.0", "architecture": "x86", "model": "",
		"bitness": "64", "uaFullVersion": "132.0.0.0",
	} {
		if he[k] != want {
			t.Errorf("highEntropy[%q] = %v, want %v (full: %v)", k, he[k], want, he)
		}
	}
}

func TestHighEntropyWithheldStaysWithheld(t *testing.T) {
	// Finding 1 behavioral coverage: fields the native result withholds
	// (simulated Permissions Policy denial via HARNESS_WITHHOLD) must stay
	// withheld instead of being synthesized, while exposed fields still
	// resolve to spoofed values and unrelated fields pass through native.
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	script := antiDetectionScript(ua, ident, brands, details, 7)
	res := runCamouflageNodePayloadEnv(t, "inject", []byte(script), []string{"HARNESS_WITHHOLD=model,bitness"})
	if len(res.Errors) != 0 {
		t.Errorf("inject logged errors: %v", res.Errors)
	}
	he, ok := res.Values["highEntropy"].(map[string]interface{})
	if !ok {
		t.Fatalf("highEntropy = %v, want object", res.Values["highEntropy"])
	}
	for _, k := range []string{"model", "bitness"} {
		if _, present := he[k]; present {
			t.Errorf("highEntropy[%q] present despite native withholding: %v", k, he[k])
		}
	}
	for k, want := range map[string]interface{}{
		"platformVersion": "10.0.0", "architecture": "x86", "uaFullVersion": "132.0.0.0",
	} {
		if he[k] != want {
			t.Errorf("highEntropy[%q] = %v, want %v (full: %v)", k, he[k], want, he)
		}
	}
	if he["unknown-hint-xyz"] != "native:unknown-hint-xyz" {
		t.Errorf("highEntropy passthrough = %v, want native value", he["unknown-hint-xyz"])
	}
}

func strVal(t *testing.T, v interface{}) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T (%v)", v, v)
	}
	return s
}

func floatVal(t *testing.T, v interface{}) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T (%v)", v, v)
	}
	return f
}

func mustBool(t *testing.T, v interface{}) bool {
	t.Helper()
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("expected bool, got %T (%v)", v, v)
	}
	return b
}

// ----- Deterministic AudioBuffer camouflage (PR5 HIGH) -----
//
// The patch must be: type-preserving (a Float32Array), session-stable
// (repeated reads agree), non-mutating (the live channel store survives
// copyFromChannel/copyToChannel and application writes), non-accumulating
// (an idempotent install, not a stacked per-call Math.random mutation),
// an observable perturbation, and a faithful error proxy (out-of-range
// channels still throw through the wrapper).

func TestAudioPatchExecution(t *testing.T) {
	res := runCamouflageNode(t, "audio", audioPatchScript(123456))
	v := res.Values
	if tType := v["audioType"]; tType != "[object float32array]" {
		t.Errorf("audioType = %v, want [object float32array] (native return type preserved)", tType)
	}
	if floatVal(t, v["audioLen"]) != 300 {
		t.Errorf("audioLen = %v, want 300", v["audioLen"])
	}
	if !mustBool(t, v["audioDeterministic"]) {
		t.Error("repeated getChannelData reads disagree: perturbation is not session-stable")
	}
	if !mustBool(t, v["audioRawIntact"]) {
		t.Error("live channel store was mutated by the patch: audio pipeline corruption (cumulative-mutation defect)")
	}
	if !mustBool(t, v["audioPerturbed"]) {
		t.Error("perturbation not observable in float32: camouflage is a no-op")
	}
	if !mustBool(t, v["audioWritePropagates"]) {
		t.Error("a copyFromChannel write to the live store is not visible to a subsequent read through the patch")
	}
	if strVal(t, v["audioRangeError"]) != "threw" {
		t.Errorf("audioRangeError = %v, want 'threw': native out-of-range channel error must propagate", v["audioRangeError"])
	}
}

// TestAudioPatchIdempotent proves the non-enumerable marker makes a
// re-installed new-document script a no-op so the perturbation is neither
// stacked nor accumulated across an identity/UA rotation window.
func TestAudioPatchIdempotent(t *testing.T) {
	res := runCamouflageNode(t, "audio-twice", audioPatchScript(123456))
	v := res.Values
	if !mustBool(t, v["audioMarker"]) {
		t.Error("idempotency marker __crowlerAudioPatched__ not installed on AudioBuffer.prototype")
	}
	if !mustBool(t, v["audioProtoStable"]) {
		t.Error("re-install replaced getChannelData: the patch stacked on itself (not idempotent)")
	}
	if !mustBool(t, v["audioRawIntact"]) {
		t.Error("double install corrupted the live channel store (per-call accumulation)")
	}
	if !mustBool(t, v["audioDeterministic"]) {
		t.Error("double install made reads non-deterministic (stacked perturbation)")
	}
}

// TestAudioPatchSeedStability proves the perturbation is a pure function of
// (seed, index): the SAME seed yields identical samples across independent
// realms (session-stable across navigation/reload/proxy rotation via the same
// seed), while DIFFERENT seeds yield different fingerprints.
func TestAudioPatchSeedStability(t *testing.T) {
	a1 := runCamouflageNode(t, "audio", audioPatchScript(123456))
	a2 := runCamouflageNode(t, "audio", audioPatchScript(123456))
	b := runCamouflageNode(t, "audio", audioPatchScript(999999))
	sa1, sa2, sb := a1.Values["audioSample100"], a2.Values["audioSample100"], b.Values["audioSample100"]
	if fa, fb, fc := floatVal(t, sa1), floatVal(t, sa2), floatVal(t, sb); fa != fb || fb == fc {
		t.Errorf("seed stability: same seed %v vs %v (want equal), different seeds %v vs %v (want distinct)", sa1, sa2, sa1, sb)
	}
}

// ----- WebGL identity patch (PR5 HIGH) -----

// TestWebGLPatchIdempotent proves a re-installed WebGL patch does not stack
// getParameter on itself: the prototype reference stays put and the
// advertised identity does not drift across a second install.
func TestWebGLPatchIdempotent(t *testing.T) {
	res := runCamouflageNode(t, "webgl-double", webglSpoofScript())
	if len(res.Errors) != 0 {
		t.Errorf("webgl-double logged errors: %v", res.Errors)
	}
	v := res.Values
	if !mustBool(t, v["webglStable"]) {
		t.Error("webgl-double: getParameter was re-stacked across the second install (not idempotent)")
	}
	if got := v["webglVendorAfter"]; got != gpuVendorSpoof {
		t.Errorf("webglVendorAfter = %v, want %q (identity drifted across reinstall)", got, gpuVendorSpoof)
	}
}

// TestWebGLPatchMissingAPI proves a missing WebGL/WebGL2 binding does not throw
// and, crucially, does not abort the surrounding identity initialization that
// follows the WebGL block (the high-entropy override must still install).
func TestWebGLPatchMissingAPI(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	res := runCamouflageNode(t, "webgl-absent", antiDetectionScript(ua, ident, brands, details, 7))
	// webgl-absent deletes both GL bindings; the new-document script must run
	// to completion without a top-level throw.
	if res.Threw != nil {
		t.Fatalf("missing WebGL threw (%s): the patch aborted the surrounding init", *res.Threw)
	}
	if len(res.Errors) != 0 {
		t.Errorf("missing-WebGL script logged errors: %v", res.Errors)
	}
	he, ok := res.Values["highEntropy"].(map[string]interface{})
	if !ok {
		t.Fatalf("highEntropy = %v, want object: a WebGL throw would have dropped the subsequent init", res.Values["highEntropy"])
	}
	if he["platformVersion"] != "10.0.0" {
		t.Errorf("highEntropy[platformVersion] = %v, want 10.0.0 (subsequent init not completed)", he["platformVersion"])
	}
}

// ----- Plugin / MIME object graph (PR5) -----

// TestPluginMimeConsistency proves the object graph is stable, indexable, and
// mutually linked: repeated reads return identical objects; per-plugin
// mimeTypes are the SAME instances as navigator.mimeTypes; and enabledPlugin
// round-trips back to the owning plugin.
func TestPluginMimeConsistency(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	res := runCamouflageNode(t, "inject", antiDetectionScript(ua, ident, brands, details, 7))
	v := res.Values
	if floatVal(t, v["pluginsLength"]) != 2 || floatVal(t, v["mimeLength"]) != 2 {
		t.Errorf("plugins/mime length = %v/%v, want 2/2", v["pluginsLength"], v["mimeLength"])
	}
	if !mustBool(t, v["pluginsStable"]) {
		t.Error("navigator.plugins is not stable across reads (elementary fingerprint inconsistency)")
	}
	if !mustBool(t, v["mimesStable"]) {
		t.Error("navigator.mimeTypes is not stable across reads")
	}
	if !mustBool(t, v["pluginIndexConsistent"]) {
		t.Error("plugins[0][0] !== plugins[0].item(0): indexing and item() disagree")
	}
	if !mustBool(t, v["pluginNamedItemConsistent"]) {
		t.Error("plugins.namedItem(name) does not round-trip to the owning plugin")
	}
	if !mustBool(t, v["pluginMimeShared"]) {
		t.Error("plugins[0].mimeTypes[0] !== navigator.mimeTypes[0]: MimeType instances are not shared across the graph")
	}
	if !mustBool(t, v["mimeEnabledPlugin"]) {
		t.Error("mimeType.enabledPlugin does not round-trip to the owning plugin")
	}
}

// ----- Legacy-defect differential control (proves the harness is not green-washing) -----
//
// The harness must DETECT each PR5 defect when fed the exact pre-fix patch
// text, and report the fixed surface clean when fed the new builder output.
// This guards against a faithful-but-blind mock that would pass either way.

// legacyAudioPatch is the exact pre-fix AudioBuffer patch (cumulative
// Math.random mutation of the live getChannelData view), verbatim from the
// pre-PR5 identity.go.
const legacyAudioPatch = `(function() {
    const original = AudioBuffer.prototype.getChannelData;
    AudioBuffer.prototype.getChannelData = function() {
        const data = original.apply(this, arguments);
        for (let i = 0; i < data.length; i += 100) {
            data[i] = data[i] + (Math.random() * 0.0000001);
        }
        return data;
    };
})();`

// legacyWebGLPatch is the exact pre-fix WebGL patch, verbatim from the
// pre-PR5 camouflage.go (with the shared spoof constants inlined). It probes
// WebGLRenderingContext unguarded, so a missing binding throws.
const legacyWebGLPatch = `(function() {
    const contexts = [WebGLRenderingContext];
    if (typeof WebGL2RenderingContext !== "undefined") contexts.push(WebGL2RenderingContext);
    contexts.forEach(function(Ctx) {
        try {
            const getParameter = Ctx.prototype.getParameter;
            Ctx.prototype.getParameter = function(parameter) {
                if (parameter === 37445) return "Intel Inc.";
                if (parameter === 37446) return "Mesa Intel(R) UHD Graphics";
                return getParameter.call(this, parameter);
            };
        } catch (e) { console.error('Error applying WebGL identity patch:', e); }
    });
})();`

// legacyBrowserRegistry is the exact pre-fix navigator.plugins / navigator.mimeTypes
// getters (each rebuilds its object graph on every access — an observable
// instability: navigator.plugins !== navigator.plugins).
const legacyBrowserRegistry = `Object.defineProperty(navigator, "plugins", { get: () => (() => {
    const makePlugin = (name, filename, description) => {
        const plugin = { description: description, filename: filename, name: name, length: 1 };
        plugin.item = function(index) { return (index === 0) ? { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" } : null; };
        plugin.namedItem = function(name) { return (name === "application/pdf") ? { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" } : null; };
        return plugin;
    };
    const plugins = [
        makePlugin("Chrome PDF Viewer", "internal-pdf-viewer", "Portable Document Format"),
        makePlugin("Chromium PDF Plugin", "internal-pdf-viewer", "Portable Document Format")
    ];
    plugins.item = function(index) { return (index >= 0 && index < this.length) ? this[index] : null; };
    plugins.namedItem = function(name) { for (let i = 0; i < this.length; i++) { if (this[i].name === name) return this[i]; } return null; };
    plugins.refresh = function() {};
    return plugins;
})() });
Object.defineProperty(navigator, "mimeTypes", { get: () => (() => {
    const mimes = [
        { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" }
    ];
    mimes.item = function(index) { return (index >= 0 && index < this.length) ? this[index] : null; };
    mimes.namedItem = function(name) { for (let i = 0; i < this.length; i++) { if (this[i].type === name) return this[i]; } return null; };
    return mimes;
})() });`

func TestCanvasExportExecution(t *testing.T) {
	// The deterministic export patch is installed on HTMLCanvasElement.prototype;
	// the pixel-faithful harness then exercises its full contract.
	res := runCamouflageNode(t, "canvas", canvasExportScript(123456))
	if len(res.Errors) != 0 {
		t.Errorf("canvas patch logged errors: %v", res.Errors)
	}
	v := res.Values
	if perr := v["canvasProbeError"]; perr != nil {
		t.Fatalf("canvas probe error: %v", perr)
	}
	// Default export is a decodable PNG and the MIME type is honored.
	assertValidPNG(t, strVal(t, v["canvasPNG"]))
	if got := strVal(t, v["canvasJPEG"]); !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Errorf("JPEG export = %q, want an image/jpeg data URL", got)
	}
	if got := strVal(t, v["canvasWebP"]); !strings.HasPrefix(got, "data:image/webp;base64,") {
		t.Errorf("WebP export = %q, want an image/webp data URL", got)
	}
	// toBlob and toDataURL must encode identical content for the same export.
	if !mustBool(t, v["canvasBlobEqURL"]) {
		t.Error("toBlob and toDataURL produced different bytes for the same content")
	}
	// Repeated exports of the same content agree (deterministic, not random).
	if !mustBool(t, v["canvasDeterministic"]) {
		t.Error("repeated export of the same content diverged (not deterministic)")
	}
	// The source canvas pixels must never be mutated by an export.
	if !mustBool(t, v["canvasOriginalUnchanged"]) {
		t.Error("export mutated the source canvas (must perturb a copy, not the source)")
	}
	// Tainted-canvas SecurityError must propagate through BOTH export APIs
	// rather than being swallowed by the native fallback.
	if got := strVal(t, v["canvasSecurityURL"]); got != "threw-security" {
		t.Errorf("toDataURL SecurityError = %v, want threw-security", got)
	}
	if got := strVal(t, v["canvasSecurityBlob"]); got != "threw-security" {
		t.Errorf("toBlob SecurityError = %v, want threw-security", got)
	}
	// A non-security source error must fall back to the native export (not throw).
	if got := strVal(t, v["canvasNonSecurityFallback"]); got != "native" {
		t.Errorf("non-security error fallback = %v, want native export", got)
	}
	// A 2D-unavailable working copy must fall back to the native source export.
	if got := strVal(t, v["canvasNull2DFallback"]); got != "native" {
		t.Errorf("null-2D fallback = %v, want native export", got)
	}
}

// TestCanvasExportSeedStability proves the export fingerprint is a deterministic
// function of the session seed: two independent realms with the SAME seed
// produce identical exports, while a different seed diverges.
func TestCanvasExportSeedStability(t *testing.T) {
	a := runCamouflageNode(t, "canvas", canvasExportScript(123456))
	b := runCamouflageNode(t, "canvas", canvasExportScript(123456))
	c := runCamouflageNode(t, "canvas", canvasExportScript(999999))
	sa, sb, sc := strVal(t, a.Values["canvasPNG"]), strVal(t, b.Values["canvasPNG"]), strVal(t, c.Values["canvasPNG"])
	if sa != sb {
		t.Errorf("same-seed export diverged across realms: %q vs %q", sa, sb)
	}
	if sc == sa {
		t.Errorf("different-seed export did not diverge: %q (want distinct)", sc)
	}
}

func TestCamouflageControlsDetectLegacyDefects(t *testing.T) {
	// Audio: the legacy cumulative-mutation patch corrupts the live store, so
	// the faithful probe reports audioRawIntact=false; the fixed patch keeps
	// the store intact (true). The harness must distinguish the two.
	legacyAudio := runCamouflageNode(t, "audio", legacyAudioPatch)
	fixedAudio := runCamouflageNode(t, "audio", audioPatchScript(123456))
	if mustBool(t, legacyAudio.Values["audioRawIntact"]) {
		t.Error("control failed: harness did NOT detect the legacy AudioBuffer cumulative-mutation defect (audioRawIntact=true on legacy)")
	}
	if !mustBool(t, fixedAudio.Values["audioRawIntact"]) {
		t.Error("control failed: fixed AudioBuffer patch corrupts the live store")
	}

	// WebGL: the legacy unguarded patch throws on a missing GL binding and would
	// abort the surrounding init; the fixed patch's typeof-guards prevent that.
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	legacyGL := runCamouflageNodeMaybeThrow(t, "webgl-absent", legacyWebGLPatch)
	if legacyGL.Threw == nil {
		t.Error("control failed: harness did NOT detect the legacy WebGL missing-API throw")
	}
	fixedGL := runCamouflageNode(t, "webgl-absent", antiDetectionScript(ua, ident, brands, details, 7))
	if fixedGL.Threw != nil {
		t.Errorf("control failed: fixed script threw on missing WebGL (%s)", *fixedGL.Threw)
	}
	if _, ok := fixedGL.Values["highEntropy"].(map[string]interface{}); !ok {
		t.Error("control failed: fixed script dropped the high-entropy init on missing WebGL")
	}

	// Plugin/mime: the legacy getters rebuild the object graph on every access,
	// so the graph is unstable; the fixed memoized registry is stable.
	legacyReg := runCamouflageNode(t, "registry-legacy", legacyBrowserRegistry)
	if mustBool(t, legacyReg.Values["pluginsStable"]) || mustBool(t, legacyReg.Values["mimesStable"]) {
		t.Error("control failed: harness did not detect the legacy plugin/mime instability (got stable)")
	}
	fixedReg := runCamouflageNode(t, "inject", antiDetectionScript(ua, ident, brands, details, 7))
	if !mustBool(t, fixedReg.Values["pluginsStable"]) || !mustBool(t, fixedReg.Values["mimesStable"]) {
		t.Error("control failed: fixed registry is not stable")
	}
	if !mustBool(t, fixedReg.Values["pluginMimeShared"]) {
		t.Error("control failed: fixed registry does not share MimeType instances across the graph")
	}
}
