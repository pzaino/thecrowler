package vdi

import (
	"strings"
	"testing"
)

func TestMapAdvertisedOSIdentity(t *testing.T) {
	tests := []struct {
		key      string
		mobile   bool
		nav, uad string
	}{
		{"windows", false, "Win32", "Windows"},
		{"darwin", false, "MacIntel", "macOS"},
		{"linux", false, "Linux x86_64", "Linux"},
		{"android", true, "Linux armv8l", "Android"},
		{"ios", true, "iPhone", "iOS"},
		{"WINDOWS", false, "Win32", "Windows"},
		{"unknown-os", false, "Linux x86_64", "Linux"}, // defensive default
	}
	for _, tc := range tests {
		ident := MapAdvertisedOSIdentity(tc.key, tc.mobile)
		if ident.NavigatorPlatform != tc.nav || ident.UserAgentDataPlatform != tc.uad {
			t.Errorf("Map(%q) = (%q, %q), want (%q, %q)",
				tc.key, ident.NavigatorPlatform, ident.UserAgentDataPlatform, tc.nav, tc.uad)
		}
		if ident.Mobile != tc.mobile {
			t.Errorf("Map(%q).Mobile = %t, want %t", tc.key, ident.Mobile, tc.mobile)
		}
		// The device class must never appear as a platform representation.
		for _, v := range []string{ident.NavigatorPlatform, ident.UserAgentDataPlatform} {
			if v == "desktop" || v == "mobile" {
				t.Errorf("Map(%q) leaks device class as platform: %q", tc.key, v)
			}
		}
	}
}

func TestChromeMajorVersion(t *testing.T) {
	major, ok := ChromeMajorVersion("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	if !ok || major != "131" {
		t.Errorf("got (%q, %t), want (131, true)", major, ok)
	}
	if _, ok := ChromeMajorVersion("Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"); ok {
		t.Error("Firefox UA must not yield a Chrome major version")
	}
	if _, ok := ChromeMajorVersion(""); ok {
		t.Error("empty UA must not yield a Chrome major version")
	}
}

func TestAntiDetectionScriptIdentity(t *testing.T) {
	ua := `Test UA "with quotes" and \ backslash ✓`
	ident := MapAdvertisedOSIdentity("windows", false)
	script := antiDetectionScript(ua, ident, "131")

	// Safe encoding: the hostile UA appears only as a JSON literal.
	if !strings.Contains(script, `"Test UA \"with quotes\" and \\ backslash ✓"`) {
		t.Errorf("script lacks safely encoded UA:\n%s", script)
	}
	if strings.Contains(script, "%s") {
		t.Error("script contains unexpanded format verbs")
	}
	// Mapped (not literal-enum, not device-class) platforms on every surface.
	if !strings.Contains(script, `"Win32"`) {
		t.Error("script lacks mapped navigator.platform Win32")
	}
	if !strings.Contains(script, `"Windows"`) {
		t.Error("script lacks mapped userAgentData.platform Windows")
	}
	if strings.Contains(script, `"desktop"`) || strings.Contains(script, `"mobile"`) {
		t.Error("script leaks device class as platform")
	}
	// Derived (not hardcoded) Client Hints brand version + device-class mobile.
	if !strings.Contains(script, `"131"`) {
		t.Error("script lacks derived brand version 131")
	}
	if strings.Contains(script, `"132"`) {
		t.Error("script still carries hardcoded brand version 132")
	}
	if !strings.Contains(script, `{ value: false }`) {
		t.Error("script lacks desktop mobile flag")
	}
	// Preserved script body.
	for _, want := range []string{
		"delete window.cdc_adoQpoasnfa76pfcZLmcfl_Array",
		"getParameter.call(this, param)",
		"getChannelData",
		"Chrome PDF Viewer",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lost preserved body %q", want)
		}
	}
	// Removed debug trace.
	if strings.Contains(script, `console.log("loaded")`) {
		t.Error("script still contains the debug console.log trace")
	}
}

func TestAntiDetectionScriptNonChromeIdentity(t *testing.T) {
	ident := MapAdvertisedOSIdentity("linux", false)
	script := antiDetectionScript("Mozilla/5.0 Firefox/133.0", ident, "")
	if strings.Contains(script, `"brands"`) || strings.Contains(script, "Not-A.Brand") {
		t.Error("non-Chrome identity must not advertise fabricated Client Hints brands")
	}
	if !strings.Contains(script, `"Linux x86_64"`) {
		t.Error("script lacks mapped navigator.platform for linux")
	}
}

func TestUserAgentOverrideParams(t *testing.T) {
	ident := MapAdvertisedOSIdentity("darwin", false)
	params := userAgentOverrideParams("test-ua", ident.NavigatorPlatform, ident, "132")

	if params["userAgent"] != "test-ua" {
		t.Errorf("userAgent = %v", params["userAgent"])
	}
	if params["platform"] != "MacIntel" {
		t.Errorf("platform = %v, want MacIntel (navigator representation, not device class)", params["platform"])
	}
	meta, ok := params["userAgentMetadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing userAgentMetadata in %v", params)
	}
	if meta["platform"] != "macOS" {
		t.Errorf("metadata platform = %v, want macOS", meta["platform"])
	}
	if meta["mobile"] != false {
		t.Errorf("metadata mobile = %v, want false", meta["mobile"])
	}
	brands, ok := meta["brands"].([]map[string]interface{})
	if !ok || len(brands) != 2 {
		t.Fatalf("metadata brands = %v, want two Chromium-shape entries", meta["brands"])
	}
	if brands[0]["brand"] != "Chromium" || brands[0]["version"] != "132" {
		t.Errorf("first brand = %v", brands[0])
	}

	// Non-Chrome identity: no fabricated metadata.
	params = userAgentOverrideParams("firefox-ua", "Linux x86_64", MapAdvertisedOSIdentity("linux", false), "")
	if _, ok := params["userAgentMetadata"]; ok {
		t.Errorf("non-Chrome params must omit userAgentMetadata: %v", params)
	}
	if params["platform"] != "Linux x86_64" {
		t.Errorf("platform = %v", params["platform"])
	}
}
