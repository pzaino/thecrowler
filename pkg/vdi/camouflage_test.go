package vdi

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Pure-Go camouflage assertions (VDI-008/VDI-011/VDI-012/VDI-013/VDI-014).
// These run everywhere (no node required); node-executed contract tests live
// in camouflage_node_test.go.

func TestCanvasSpoofDataURLIsValidPNG(t *testing.T) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(canvasSpoofDataURL, prefix) {
		t.Fatalf("not a PNG data URL: %q", canvasSpoofDataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(canvasSpoofDataURL, prefix))
	if err != nil {
		t.Fatalf("body is not valid base64: %v", err)
	}
	magic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(raw) < 8 || string(raw[:8]) != string(magic) {
		t.Errorf("constant does not decode to a PNG image (%d bytes)", len(raw))
	}
}

func TestSharedMockBuilders(t *testing.T) {
	plugins := pluginArrayLiteral()
	for _, want := range []string{"Chrome PDF Viewer", "Chromium PDF Plugin", ".item = function", ".namedItem = function", ".refresh = function", "length"} {
		if !strings.Contains(plugins, want) {
			t.Errorf("plugin literal lacks %q", want)
		}
	}
	mimes := mimeArrayLiteral()
	for _, want := range []string{"application/pdf", ".item = function", ".namedItem = function"} {
		if !strings.Contains(mimes, want) {
			t.Errorf("mime literal lacks %q", want)
		}
	}
	if !strings.Contains(webglSpoofScript(), "WebGL2RenderingContext") {
		t.Error("webgl snippet must cover WebGL2")
	}
	if !strings.Contains(webglSpoofScript(), "getParameter.call(this, parameter)") {
		t.Error("webgl snippet must bind the native receiver")
	}
	if !strings.Contains(gpuPatchScript(), "if (!ctx) return ctx") {
		t.Error("gpu patch lacks the null-context guard")
	}
	if !strings.Contains(gpuPatchScript(), gpuRendererSpoof) || !strings.Contains(gpuPatchScript(), gpuVendorSpoof) {
		t.Error("gpu patch must use the shared spoofed strings")
	}
}

func TestReinforceScriptRepairs(t *testing.T) {
	script := reinforceScript()
	// Stage 1 must be re-definable (stage 7 redefines webdriver).
	if !strings.Contains(script, "Object.defineProperty(navigator, 'webdriver', {get: () => undefined, configurable: true})") {
		t.Error("stage 1 webdriver definition must be configurable")
	}
	// The proto-cleanup after the throwing delete must be reachable.
	if strings.Contains(script, "delete navigator.webdriver.chrome;\n") && !strings.Contains(script, "navigator.webdriver !== undefined") {
		t.Error("unconditional delete navigator.webdriver.chrome still present")
	}
	// Canvas must emit a decodable image, not the invalid constant.
	if strings.Contains(script, "fakemockdata") {
		t.Error("invalid canvas constant still present")
	}
	if !strings.Contains(script, canvasSpoofDataURL) {
		t.Error("canvas must use the valid PNG constant")
	}
	// attachShadow override removed: it broke every web-component page.
	if strings.Contains(script, "attachShadow") {
		t.Error("attachShadow override must be gone (breaks platform contract)")
	}
	// Permissions delegation must keep its receiver.
	if !strings.Contains(script, "navigator.permissions.query.bind(navigator.permissions)") {
		t.Error("permissions delegation must bind its receiver")
	}
	// Canonical plugin shape, shared strings, canonical stages present.
	for _, want := range []string{"namedItem", gpuRendererSpoof, "devicePixelRatio", "selenium-evaluate", "doNotTrack", "deleteDatabase('webdriver')"} {
		if !strings.Contains(script, want) {
			t.Errorf("reinforce script lacks %q", want)
		}
	}
}

func TestNavigatorPropertyScripts(t *testing.T) {
	ident := MapAdvertisedOSIdentity("darwin", false)
	scripts := navigatorPropertyScripts("en-gb", "ua", ident)
	joined := strings.Join(scripts, "\n")
	if !strings.Contains(joined, "['en-GB', 'en']") {
		t.Error("en-gb language mapping missing")
	}
	if !strings.Contains(joined, `"MacIntel"`) {
		t.Error("advertised platform mapping missing")
	}
	if !strings.Contains(joined, "namedItem") {
		t.Error("plugins mock lacks canonical shape")
	}
	if !strings.Contains(joined, "configurable: true") {
		t.Error("webdriver definition should be re-definable")
	}
	// Geometry + privacy surface preserved.
	for _, want := range []string{"1920", "1080", "deviceMemory", "hardwareConcurrency", "doNotTrack", "getBattery", "RTCPeerConnection"} {
		if !strings.Contains(joined, want) {
			t.Errorf("navigator scripts lost %q", want)
		}
	}
	// Default language + desktop geometry sanity.
	def := strings.Join(navigatorPropertyScripts("", "ua", MapAdvertisedOSIdentity("linux", false)), "\n")
	if !strings.Contains(def, "['en-US', 'en']") {
		t.Error("default language must be en-US")
	}
}

func TestAntiDetectionScriptUsesSharedMocks(t *testing.T) {
	ident := MapAdvertisedOSIdentity("windows", false)
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0"
	script := antiDetectionScript(ua, ident, ClientHintBrandsFor("chrome", "132", ""), NewClientHintsDetails(ua, "chrome", ident))
	for _, want := range []string{"namedItem", gpuRendererSpoof, "WebGL2RenderingContext", "getHighEntropyValues"} {
		if !strings.Contains(script, want) {
			t.Errorf("persistent script lacks shared %q", want)
		}
	}
	if strings.Contains(script, "[1, 2, 3, 4, 5]") || strings.Contains(script, "fakemockdata") || strings.Contains(script, "Intel Iris OpenGL") {
		t.Error("persistent script still carries a superseded mock string")
	}
}

func TestLoadListenerScopeDocumented(t *testing.T) {
	// The listener registers one-shot on the current document; it must at
	// least use the canonical plugin shape. Full lifecycle (target-document
	// reachability, iframes) requires a live browser (VDI-014/VDI-021).
	script := loadListenerScript()
	if !strings.Contains(script, "addEventListener('load'") {
		t.Error("load listener registration missing")
	}
	if strings.Contains(script, "[1, 2, 3, 4, 5]") {
		t.Error("load listener still uses the numeric plugin mock")
	}
}
