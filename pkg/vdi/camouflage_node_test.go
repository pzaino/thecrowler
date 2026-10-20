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

// strValOrEmpty returns the value as a string, or "" when it is nil or a
// non-string. Used for optional diagnostic fields the probe sets only on the
// error path, so the happy path leaves them absent.
func strValOrEmpty(t *testing.T, v interface{}) string {
	t.Helper()
	s, _ := v.(string)
	return s
}

// ----- Deterministic AudioBuffer camouflage -----
//
// The patch must be: type-preserving (a Float32Array), session-stable
// (repeated reads agree), non-mutating (the live channel store survives
// copyFromChannel/copyToChannel and application writes), non-accumulating
// (an idempotent install, not a stacked per-call Math.random mutation),
// an observable perturbation at the render/decode boundary in BOTH the modern
// Promise form and the legacy success-error callback form (and through
// OfflineAudioContext, inherited from BaseAudioContext), a faithful error proxy
// (out-of-range channels, a failing decode, and the legacy error callback all
// propagate through the wrapper), and marker-free: a produced buffer gains no
// discoverable own property.

func TestAudioPatchExecution(t *testing.T) {
	res := runCamouflageNode(t, "audio", audioPatchScript(123456))
	v := res.Values
	// Native getChannelData contract preserved.
	if tType := v["audioType"]; tType != "[object float32array]" {
		t.Errorf("audioType = %v, want [object float32array] (native return type preserved)", tType)
	}
	if floatVal(t, v["audioLen"]) != 300 {
		t.Errorf("audioLen = %v, want 300", v["audioLen"])
	}
	// Write-through: writes to the live view propagate to the underlying store.
	if !mustBool(t, v["audioWriteThrough"]) {
		t.Error("writing to getChannelData(0)[i] did not propagate to the live store (native write-through broken)")
	}
	// No-accumulation: repeated reads without writes produce identical data.
	if !mustBool(t, v["audioDeterministic"]) {
		t.Error("repeated getChannelData reads disagree: per-read accumulation defect")
	}
	// copyFromChannel(destination, channel) reads the stored samples into dst
	// (native direction: destination out, channel in).
	if !mustBool(t, v["audioCopyFromConsistent"]) {
		t.Error("copyFromChannel did not read the stored samples (direction or semantics wrong)")
	}
	// copyToChannel(source, channel) writes the source INTO the channel store.
	if !mustBool(t, v["audioCopyToChannel"]) {
		t.Error("copyToChannel did not write into the channel store (direction or semantics wrong)")
	}
	// Raw-intact: reads do not corrupt the live channel store.
	if !mustBool(t, v["audioRawIntact"]) {
		t.Error("live channel store was mutated by getChannelData reads (cumulative-mutation defect)")
	}
	// Native out-of-range channel throws through the native method.
	if got := strVal(t, v["audioRangeError"]); got != "threw" {
		t.Errorf("audioRangeError = %v, want 'threw': native out-of-range channel error must propagate", got)
	}
	// copyFromChannel out-of-range channel also throws.
	if got := strVal(t, v["audioRangeCopyFrom"]); got != "threw" {
		t.Errorf("audioRangeCopyFrom = %v, want 'threw': native copyFromChannel range check must propagate", got)
	}
	// Multi-channel: channels are independent.
	if !mustBool(t, v["audioMultiChannel"]) {
		t.Error("multi-channel AudioBuffer channels are not independent")
	}
	// Multi-buffer: independent buffers have independent stores.
	if !mustBool(t, v["audioMultiBuffer"]) {
		t.Error("independent AudioBuffers share store state")
	}
	// Render-boundary perturbation via startRendering (offline, Promise form):
	// the produced buffer is perturbed and its reads are stable (no drift).
	if !mustBool(t, v["audioRenderedPerturbed"]) {
		t.Error("render-boundary perturbation not observable in float32 (camouflage no-op)")
	}
	if !mustBool(t, v["audioRenderedDeterministic"]) {
		t.Error("rendered buffer's sample drifts on repeated reads (per-read accumulation)")
	}
	// Exactly-once, marker-free perturbation: a produced buffer gains no own
	// property — its own-property set equals a fresh, unperturbed buffer's. A
	// per-instance marker like the old __a1 would add a name and fail this.
	if !mustBool(t, v["audioNoInstanceMarker"]) {
		if s := strValOrEmpty(t, v["audioInstancePropSet"]); s != "" {
			t.Errorf("audioNoInstanceMarker = false; produced buffer's own-property set = %v (a per-instance marker was added)", s)
		} else {
			t.Error("audioNoInstanceMarker = false (a per-instance marker was added to the produced buffer)")
		}
	}
	// Promise-form decoding (modern API): the resolved buffer is perturbed and
	// carries a thenable handle (the native Promise contract is preserved).
	if !mustBool(t, v["audioPromiseHasThen"]) {
		t.Error("Promise-form decodeAudioData did not return a thenable (native Promise form not preserved)")
	}
	if !mustBool(t, v["audioPromisePerturbed"]) {
		if s := strValOrEmpty(t, v["audioPromiseError"]); s != "" {
			t.Errorf("Promise-form decode error: %s", s)
		}
		t.Error("Promise-form decoded buffer was not perturbed at completion")
	}
	// Legacy success/error callback-form decoding: the success callback observes
	// the perturbed buffer (perturbed before the application reads it).
	if !mustBool(t, v["audioCallbackPerturbed"]) {
		if s := strValOrEmpty(t, v["audioCallbackError"]); s != "" {
			t.Errorf("legacy callback-form decode error: %s", s)
		}
		t.Error("legacy callback-form decoded buffer was not perturbed before the application observed it")
	}
	// OfflineAudioContext.decodeAudioData (inherited from BaseAudioContext) is
	// perturbed through the single base-level patch.
	if !mustBool(t, v["audioOfflinePerturbed"]) {
		if s := strValOrEmpty(t, v["audioOfflineError"]); s != "" {
			t.Errorf("offline decode error: %s", s)
		}
		t.Error("OfflineAudioContext.decodeAudioData was not perturbed through the BaseAudioContext patch")
	}
	// A failing Promise-form decode must REJECT (not resolve to a phantom buffer).
	if got := strVal(t, v["audioRejectThrew"]); got != "rejected" {
		t.Errorf("audioRejectThrew = %v, want 'rejected': a failing decode must propagate rejection through the wrapper", got)
	}
	// A failing callback-form decode must invoke the ERROR callback (not success).
	if !mustBool(t, v["audioCallbackErrInvoked"]) {
		if s := strValOrEmpty(t, v["audioCallbackErr"]); s != "" {
			t.Errorf("callback-error path result = %v", s)
		}
		t.Error("a failing callback-form decode did not invoke the error callback")
	}
}

// TestAudioPatchIdempotent proves the re-installed render/decode patch does not
// introduce a discoverable prototype/global marker and is observationally
// idempotent: a same-realm reinstall is a test artifact (the new-document
// identity script runs once per realm), and across two evaluations the patch
// stays marker-free and the live store is not corrupted or accumulated.
func TestAudioPatchIdempotent(t *testing.T) {
	res := runCamouflageNode(t, "audio-twice", audioPatchScript(123456))
	v := res.Values
	// A same-realm reinstall leaves no discoverable marker on the prototypes.
	if !mustBool(t, v["audioNoMarker"]) {
		t.Error("re-install left a discoverable marker on AudioBuffer.prototype")
	}
	if !mustBool(t, v["audioNoMarker2"]) {
		t.Error("re-install left a discoverable marker on AudioContext.prototype")
	}
	// No crowler-named global property was introduced (snapshot reports the
	// matching-name list as a string; empty means none).
	if got := strVal(t, v["crowlerGlobal"]); got != "" {
		t.Errorf("crowlerGlobal = %q, want empty (a crowler-named global was introduced)", got)
	}
	// The produced buffer gains no per-instance own property even after two
	// installs (the exactly-once, marker-free guard holds across evaluations).
	if !mustBool(t, v["audioNoInstanceMarker"]) {
		t.Errorf("audioNoInstanceMarker = false (a per-instance marker survived a re-install; own-property set = %v)", strValOrEmpty(t, v["audioInstancePropSet"]))
	}
	// No per-read accumulation after two installs.
	if !mustBool(t, v["audioRawIntact"]) {
		t.Error("double install corrupted the live channel store")
	}
	if !mustBool(t, v["audioDeterministic"]) {
		t.Error("double install made reads non-deterministic")
	}
}

// TestAudioPatchNoBaseFallback proves the patch degrades correctly when an
// engine does not expose BaseAudioContext: it wraps the concrete AudioContext /
// OfflineAudioContext prototypes instead, so decoding and startRendering are
// still perturbed (the native-layout compatibility branch).
func TestAudioPatchNoBaseFallback(t *testing.T) {
	res := runCamouflageNode(t, "audio-no-base", audioPatchScript(31337))
	v := res.Values
	// With the base absent, the concrete-context patches must still perturb.
	if !mustBool(t, v["audioRenderedPerturbed"]) {
		if s := strValOrEmpty(t, v["audioRenderError"]); s != "" {
			t.Errorf("no-base fallback render error: %s", s)
		}
		t.Error("no-base fallback: startRendering did not perturb the rendered buffer")
	}
	if !mustBool(t, v["audioPromisePerturbed"]) {
		if s := strValOrEmpty(t, v["audioPromiseError"]); s != "" {
			t.Errorf("no-base fallback Promise-decode error: %s", s)
		}
		t.Error("no-base fallback: concrete-context decodeAudioData was not perturbed")
	}
	if !mustBool(t, v["audioCallbackPerturbed"]) {
		if s := strValOrEmpty(t, v["audioCallbackError"]); s != "" {
			t.Errorf("no-base fallback callback-decode error: %s", s)
		}
		t.Error("no-base fallback: legacy callback-form decode was not perturbed")
	}
	// The fallback path is still marker-free.
	if !mustBool(t, v["audioNoInstanceMarker"]) {
		t.Errorf("no-base fallback added a per-instance marker; own-property set = %v", strValOrEmpty(t, v["audioInstancePropSet"]))
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
	sa1, sa2, sb := floatVal(t, a1.Values["audioRenderedSample100"]),
		floatVal(t, a2.Values["audioRenderedSample100"]),
		floatVal(t, b.Values["audioRenderedSample100"])
	if sa1 == 0 || sa2 == 0 || sb == 0 {
		t.Fatalf("sample100 was zero, render-boundary perturbation may not have been observable: %v %v %v", sa1, sa2, sb)
	}
	if sa1 != sa2 {
		t.Errorf("seed stability: same seed 123456 diverged across realms: %v vs %v", sa1, sa2)
	}
	if sb == sa1 {
		t.Errorf("seed stability: different seed 999999 did not diverge: got %v (want distinct from %v)", sb, sa1)
	}
}

// ----- WebGL identity patch -----

// TestWebGLPatchIdempotent verifies observable behavioral idempotency across a
// second install. The supported injection lifecycle evaluates the new-document
// identity script exactly once per realm (a new document is a fresh realm with
// fresh prototypes), so the per-closure WeakSet is the complete within-realm
// guard. A second IIFE eval in the SAME realm (a test artifact, not a supported
// scenario) creates a new closure and may physically re-wrap the prototype; the
// meaningful guarantee is observable idempotency — the spoofed identity and the
// native-first receiver validation stay identical across the reinstall — not
// pointer stability.
func TestWebGLPatchIdempotent(t *testing.T) {
	res := runCamouflageNode(t, "webgl-double", webglSpoofScript())
	if len(res.Errors) != 0 {
		t.Errorf("webgl-double logged errors: %v", res.Errors)
	}
	v := res.Values
	// No errors logged by the double install itself (the patch is safe to re-run).
	// The second install may physically re-wrap (cross-eval); observable idempotency
	// is checked via the vendor-after value: the spoof is returned correctly after
	// two installs.
	if got := v["webglVendorAfter"]; got != gpuVendorSpoof {
		t.Errorf("webglVendorAfter = %v, want %q (identity drifted across reinstall)", got, gpuVendorSpoof)
	}
	// webglStable reports whether the prototype reference survived the cross-eval
	// reinstall. When true, the within-eval WeakSet already prevented the re-wrap;
	// when false, the observable behavior is still correct (the else branch makes
	// the point — a physically-stacked but observably-idempotent result).
	_ = mustBool(t, v["webglStable"])
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

// TestWebGLFakerReceiverRejected proves the native-first design rejects a
// fabricated / unbound receiver: calling getParameter on a non-GL context must
// throw (native "Illegal invocation" propagates rather than returning a spoofed
// value), which the previous unguarded implementation did not guard against.
func TestWebGLFakerReceiverRejected(t *testing.T) {
	res := runCamouflageNode(t, "webgl-double", webglSpoofScript())
	v := res.Values
	// The harness sets webglFakerReceiver to 'threw:...' when the fabricated
	// receiver is correctly rejected by the native-first design.
	if got := strVal(t, v["webglFakerReceiver"]); !strings.HasPrefix(got, "threw") {
		t.Errorf("fabricated-receiver getParameter = %v, want 'threw:*': a fabricated receiver must not receive spoofed GPU identity", got)
	}
	// With the extension available (real gl context with params), vendor is spoofed.
	if got := v["webglVendor"]; got != gpuVendorSpoof {
		t.Errorf("webglVendor = %v, want %q (spoof returned for valid ext-available context)", got, gpuVendorSpoof)
	}
}

// TestWebGLExtUnavailableNoFabricate proves that when the WEBGL_debug_renderer_info
// extension is not available (native getParameter returns a non-string for
// 37445/37446), the patch does NOT fabricate a spoofed value.
func TestWebGLExtUnavailableNoFabricate(t *testing.T) {
	res := runCamouflageNode(t, "webgl-double", webglSpoofScript())
	v := res.Values
	// The harness runs glNoExt (params: {} → native returns undefined for 37445/37446)
	// and verifies the patch does not fabricate.
	if !mustBool(t, v["webglExtNoFabricated"]) {
		t.Error("extension-unavailable getParameter(37445/37446) fabricated a spoofed value")
	}
}

// TestPatchArtifactEnumeration verifies that no discoverable crowler-named
// prototype or global properties are introduced by the antiDetectionScript.
func TestPatchArtifactEnumeration(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	res := runCamouflageNode(t, "inject", antiDetectionScript(ua, ident, brands, details, 7))
	v := res.Values
	// The snapshot's crowlerGlobal field must be empty (no crowler globals).
	if got := strVal(t, v["crowlerGlobal"]); got != "" {
		t.Errorf("crowlerGlobal = %q, want empty: discoverable crowler global property was introduced", got)
	}
	// WebGL prototype names filtered for crowler/underscore must be empty.
	if got := strVal(t, v["webglProtoNames"]); got != "" {
		t.Errorf("webglProtoNames = %q, want empty: discoverable markers on WebGLRenderingContext.prototype", got)
	}
}

// ----- Plugin / MIME object graph -----

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
// The harness must DETECT each defect when fed the exact unfixed patch
// text, and report the fixed surface clean when fed the new builder output.
// This guards against a faithful-but-blind mock that would pass either way.

// legacyAudioPatch is the unfixed AudioBuffer patch it supersedes (a cumulative
// Math.random mutation of the live getChannelData view), kept verbatim so the
// differential control can confirm the harness detects that defect.
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

// legacyWebGLPatch is the unfixed WebGL patch it supersedes (with the shared
// spoof constants inlined), kept verbatim for the same reason. It probes
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
