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
	if res.Threw != nil {
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
	checkScriptSyntax(t, "inject", antiDetectionScript(ua, ident, brands, details))
	checkScriptSyntax(t, "plugins", pluginArrayLiteral())
	checkScriptSyntax(t, "mime", mimeArrayLiteral())
	checkScriptSyntax(t, "webgl", webglSpoofScript())
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
	res := runCamouflageNode(t, "inject", antiDetectionScript(ua, ident, brands, details))
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
	script := antiDetectionScript(ua, ident, brands, details)
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
