package vdi

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-auxiliaries/selenium"
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

func TestClientHintBrandsFor(t *testing.T) {
	chrome := ClientHintBrandsFor("chrome", "131", "")
	if len(chrome) != 2 || chrome[0] != (ClientHintBrand{Brand: "Chromium", Version: "131"}) {
		t.Errorf("chrome brands = %+v, want two-brand Chromium shape at 131", chrome)
	}
	edge := ClientHintBrandsFor("edge", "131", "131")
	if len(edge) != 3 || edge[2] != (ClientHintBrand{Brand: "Microsoft Edge", Version: "131"}) {
		t.Errorf("edge brands = %+v, want Chromium + Not-A.Brand + Microsoft Edge", edge)
	}
	opera := ClientHintBrandsFor("opera", "130", "115")
	if len(opera) != 3 || opera[2] != (ClientHintBrand{Brand: "Opera", Version: "115"}) {
		t.Errorf("opera brands = %+v, want Chromium + Not-A.Brand + Opera 115", opera)
	}
	// Legacy EdgeHTML (no Edg/ major) and non-Chromium products yield none.
	if got := ClientHintBrandsFor("edge", "70", ""); got != nil {
		t.Errorf("legacy edge brands = %+v, want nil", got)
	}
	if got := ClientHintBrandsFor("firefox", "", ""); got != nil {
		t.Errorf("firefox brands = %+v, want nil", got)
	}
	if got := ClientHintBrandsFor("safari", "", ""); got != nil {
		t.Errorf("safari brands = %+v, want nil", got)
	}
	if got := ClientHintBrandsFor("chrome", "", ""); got != nil {
		t.Errorf("versionless chrome brands = %+v, want nil", got)
	}
}

func TestAntiDetectionScriptIdentity(t *testing.T) {
	ua := `Test UA "with quotes" and \ backslash ✓`
	ident := MapAdvertisedOSIdentity("windows", false)
	brands := ClientHintBrandsFor("chrome", "131", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	script := antiDetectionScript(ua, ident, brands, details)

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
		"getParameter.call(this, parameter)",
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
	// High-entropy override carries the selection-consistent values.
	for _, want := range []string{"getHighEntropyValues", `"10.0.0"`, `"x86"`, `"64"`} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks high-entropy override %q", want)
		}
	}
}

func TestAntiDetectionScriptEdgeIdentity(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	script := antiDetectionScript(ua, ident, ClientHintBrandsFor("edge", "131", "131"),
		NewClientHintsDetails(ua, "edge", ident))
	if !strings.Contains(script, `"Microsoft Edge"`) || !strings.Contains(script, `"131"`) {
		t.Errorf("edge script lacks Microsoft Edge brand:\n%s", script)
	}
}

func TestAntiDetectionScriptNonChromeIdentity(t *testing.T) {
	ident := MapAdvertisedOSIdentity("linux", false)
	firefoxUA := "Mozilla/5.0 Firefox/133.0"
	script := antiDetectionScript(firefoxUA, ident, nil, NewClientHintsDetails(firefoxUA, "firefox", ident))
	if strings.Contains(script, `"brands"`) || strings.Contains(script, "Not-A.Brand") {
		t.Error("non-Chrome identity must not advertise fabricated Client Hints brands")
	}
	if !strings.Contains(script, `"Linux x86_64"`) {
		t.Error("script lacks mapped navigator.platform for linux")
	}
}

func TestUserAgentOverrideParams(t *testing.T) {
	ident := MapAdvertisedOSIdentity("darwin", false)
	darwinUA := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	details := NewClientHintsDetails(darwinUA, "chrome", ident)
	params := userAgentOverrideParams("test-ua", ident.NavigatorPlatform, ident, ClientHintBrandsFor("chrome", "132", ""), details)

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
	// Required high-entropy metadata fields: the payload
	// must be schema-complete or Chromium may reject the whole override.
	if meta["platformVersion"] != "14.0.0" {
		t.Errorf("metadata platformVersion = %v, want 14.0.0", meta["platformVersion"])
	}
	if meta["architecture"] != "arm" {
		t.Errorf("metadata architecture = %v, want arm", meta["architecture"])
	}
	if meta["model"] != "" {
		t.Errorf("metadata model = %v, want empty on desktop", meta["model"])
	}
	if meta["fullVersion"] != "132.0.0.0" {
		t.Errorf("metadata fullVersion = %v, want 132.0.0.0", meta["fullVersion"])
	}

	// Non-Chrome identity: no fabricated metadata.
	ffIdent := MapAdvertisedOSIdentity("linux", false)
	params = userAgentOverrideParams("firefox-ua", "Linux x86_64", ffIdent, nil,
		NewClientHintsDetails("firefox-ua", "firefox", ffIdent))
	if _, ok := params["userAgentMetadata"]; ok {
		t.Errorf("non-Chrome params must omit userAgentMetadata: %v", params)
	}
	if params["platform"] != "Linux x86_64" {
		t.Errorf("platform = %v", params["platform"])
	}

	// Edge identity: product brand present with its own major.
	edgeIdent := MapAdvertisedOSIdentity("windows", false)
	edgeUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
	edgeParams := userAgentOverrideParams("edge-ua", "Win32", edgeIdent,
		ClientHintBrandsFor("edge", "131", "131"), NewClientHintsDetails(edgeUA, "edge", edgeIdent))
	edgeMeta, ok := edgeParams["userAgentMetadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("edge params missing metadata: %v", edgeParams)
	}
	edgeBrands, ok := edgeMeta["brands"].([]map[string]interface{})
	if !ok || len(edgeBrands) != 3 || edgeBrands[2]["brand"] != "Microsoft Edge" {
		t.Errorf("edge metadata brands = %v", edgeMeta["brands"])
	}
}

func TestParseScriptIdentifier(t *testing.T) {
	if got := parseScriptIdentifier(map[string]interface{}{"identifier": "abc-123"}); got != "abc-123" {
		t.Errorf("identifier = %q, want abc-123", got)
	}
	for _, bad := range []interface{}{nil, "abc", map[string]interface{}{}, map[string]interface{}{"identifier": 42}} {
		if got := parseScriptIdentifier(bad); got != "" {
			t.Errorf("parseScriptIdentifier(%v) = %q, want empty", bad, got)
		}
	}
}

func TestForgetIdentityScript(t *testing.T) {
	identityScripts.Lock()
	identityScripts.bySession["sess-test-forget"] = "id-1"
	identityScripts.Unlock()
	forgetIdentityScript("sess-test-forget")
	identityScripts.Lock()
	_, stillThere := identityScripts.bySession["sess-test-forget"]
	identityScripts.Unlock()
	if stillThere {
		t.Error("forgetIdentityScript did not drop the registration")
	}
	// Empty session IDs are no-ops and must not panic.
	forgetIdentityScript("")
}

func TestWebdriverBrowserName(t *testing.T) {
	// Follow-up finding 2 (P1): Chromium registers as "chrome" so the
	// binding's CDP gate (== "chrome") and ChromeDriver accept it, while
	// identity selection keeps the configured type.
	if got := webdriverBrowserName(BrowserChromium); got != BrowserChrome {
		t.Errorf("chromium capability = %q, want chrome", got)
	}
	for _, tc := range []struct{ in, want string }{
		{BrowserChrome, BrowserChrome},
		{BrowserFirefox, BrowserFirefox},
		{"", ""},
		{"safari", "safari"},
	} {
		if got := webdriverBrowserName(tc.in); got != tc.want {
			t.Errorf("webdriverBrowserName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeCDPDriver fakes only the WebDriver surface ReplaceIdentityScript
// exercises; every other method comes from the embedded (nil) interface and
// panics if called, which fails the test by design.
type fakeCDPDriver struct {
	selenium.WebDriver
	sessionID string
	cdp       func(cmd string, params map[string]interface{}) (interface{}, error)
}

func (f *fakeCDPDriver) SessionID() string { return f.sessionID }

func (f *fakeCDPDriver) ExecuteChromeDPCommand(cmd string, params map[string]interface{}) (interface{}, error) {
	return f.cdp(cmd, params)
}

func trackedID(t *testing.T, session string) (string, bool) {
	t.Helper()
	identityScripts.Lock()
	defer identityScripts.Unlock()
	id, ok := identityScripts.bySession[session]
	return id, ok
}

func TestReplaceIdentityScriptRegisters(t *testing.T) {
	const session = "sess-test-replace-register"
	defer forgetIdentityScript(session)
	var cmds []string
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			cmds = append(cmds, cmd)
			if cmd != "Page.addScriptToEvaluateOnNewDocument" {
				t.Errorf("unexpected CDP command %q", cmd)
			}
			return map[string]interface{}{"identifier": "id-A"}, nil
		},
	}
	if err := ReplaceIdentityScript(wd, 0, "source-A"); err != nil {
		t.Fatalf("ReplaceIdentityScript: %v", err)
	}
	if id, ok := trackedID(t, session); !ok || id != "id-A" {
		t.Errorf("tracked = (%q, %t), want (id-A, true)", id, ok)
	}
}

func TestReplaceIdentityScriptReplacesPrevious(t *testing.T) {
	const session = "sess-test-replace-previous"
	defer forgetIdentityScript(session)
	identityScripts.Lock()
	identityScripts.bySession[session] = "id-A"
	identityScripts.Unlock()
	var removed, added []string
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			switch cmd {
			case "Page.removeScriptToEvaluateOnNewDocument":
				removed = append(removed, params["identifier"].(string))
				return map[string]interface{}{}, nil
			case "Page.addScriptToEvaluateOnNewDocument":
				added = append(added, params["source"].(string))
				return map[string]interface{}{"identifier": "id-B"}, nil
			default:
				t.Errorf("unexpected CDP command %q", cmd)
				return nil, nil
			}
		},
	}
	if err := ReplaceIdentityScript(wd, 0, "source-B"); err != nil {
		t.Fatalf("ReplaceIdentityScript: %v", err)
	}
	if len(removed) != 1 || removed[0] != "id-A" {
		t.Errorf("removed = %v, want [id-A] first", removed)
	}
	if len(added) != 1 || added[0] != "source-B" {
		t.Errorf("added = %v, want [source-B]", added)
	}
	if id, _ := trackedID(t, session); id != "id-B" {
		t.Errorf("tracked = %q, want id-B", id)
	}
}

func TestReplaceIdentityScriptRemovalFailureRetains(t *testing.T) {
	const session = "sess-test-replace-failure"
	defer forgetIdentityScript(session)
	identityScripts.Lock()
	identityScripts.bySession[session] = "id-A"
	identityScripts.Unlock()
	addCalls := 0
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			if cmd == "Page.removeScriptToEvaluateOnNewDocument" {
				return nil, errors.New("invalid session id")
			}
			addCalls++
			return map[string]interface{}{"identifier": "id-B"}, nil
		},
	}
	// Follow-up finding 1 (P1): the failure must propagate, the old
	// identifier must be retained for a later retry, and no second script
	// may be stacked.
	err := ReplaceIdentityScript(wd, 0, "source-B")
	if err == nil || !strings.Contains(err.Error(), "removing superseded") {
		t.Fatalf("err = %v, want removal failure", err)
	}
	if addCalls != 0 {
		t.Errorf("new script registered despite removal failure (%d adds)", addCalls)
	}
	if id, _ := trackedID(t, session); id != "id-A" {
		t.Errorf("tracked = %q, want retained id-A", id)
	}
}

func TestReplaceIdentityScriptUntracked(t *testing.T) {
	const session = "sess-test-replace-untracked"
	defer forgetIdentityScript(session)
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			return map[string]interface{}{}, nil // success, no identifier
		},
	}
	err := ReplaceIdentityScript(wd, 0, "source")
	if !errors.Is(err, ErrIdentityScriptUntracked) {
		t.Fatalf("err = %v, want ErrIdentityScriptUntracked", err)
	}
	if _, ok := trackedID(t, session); ok {
		t.Error("untracked registration must not be stored")
	}
}

func TestReplaceIdentityScriptNilDriver(t *testing.T) {
	if err := ReplaceIdentityScript(nil, 0, "source"); err == nil {
		t.Error("nil driver must fail")
	}
}

func TestNewClientHintsDetails(t *testing.T) {
	windowsUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.6834.159 Safari/537.36"
	darwinUA := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	androidUA := "Mozilla/5.0 (Linux; Android 10; SM-G960F) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Mobile Safari/537.36"
	linuxUA := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.6367.78 Safari/537.36"
	tests := []struct {
		name    string
		ua      string
		product string
		ident   OSIdentity
		want    ClientHintsDetails
	}{
		{"windows chrome", windowsUA, "chrome", MapAdvertisedOSIdentity("windows", false),
			ClientHintsDetails{FullVersion: "132.0.6834.159", PlatformVersion: "10.0.0", Architecture: "x86", Model: "", Bitness: "64",
				FullVersionList: []ClientHintBrand{{Brand: "Chromium", Version: "132.0.6834.159"}, {Brand: "Not-A.Brand", Version: "8.0.0.0"}}}},
		{"darwin chrome (Instagram case)", darwinUA, "chrome", MapAdvertisedOSIdentity("darwin", false),
			ClientHintsDetails{FullVersion: "132.0.0.0", PlatformVersion: "14.0.0", Architecture: "arm", Model: "", Bitness: "64",
				FullVersionList: []ClientHintBrand{{Brand: "Chromium", Version: "132.0.0.0"}, {Brand: "Not-A.Brand", Version: "8.0.0.0"}}}},
		{"linux chrome", linuxUA, "chrome", MapAdvertisedOSIdentity("linux", false),
			ClientHintsDetails{FullVersion: "124.0.6367.78", PlatformVersion: "", Architecture: "x86", Model: "", Bitness: "64",
				FullVersionList: []ClientHintBrand{{Brand: "Chromium", Version: "124.0.6367.78"}, {Brand: "Not-A.Brand", Version: "8.0.0.0"}}}},
		{"android chrome", androidUA, "chrome", MapAdvertisedOSIdentity("android", true),
			ClientHintsDetails{FullVersion: "122.0.6261.112", PlatformVersion: "10.0.0", Architecture: "arm", Model: "SM-G960F", Bitness: "32",
				FullVersionList: []ClientHintBrand{{Brand: "Chromium", Version: "122.0.6261.112"}, {Brand: "Not-A.Brand", Version: "8.0.0.0"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewClientHintsDetails(tc.ua, tc.product, tc.ident); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("details = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFullVersionFor(t *testing.T) {
	for _, tc := range []struct {
		ua, product, want string
	}{
		{"Chrome/132.0.6834.159 Safari/537.36", "chrome", "132.0.6834.159"},
		{"Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0", "edge", "131.0.0.0"},
		{"Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0", "opera", "115.0.0.0"},
		{"Firefox/133.0", "firefox", ""},
		{"no-version-here", "chrome", ""},
	} {
		if got := FullVersionFor(tc.ua, tc.product); got != tc.want {
			t.Errorf("FullVersionFor(%q, %q) = %q, want %q", tc.ua, tc.product, got, tc.want)
		}
	}
}

func TestFullVersionListFor(t *testing.T) {
	chrome := []ClientHintBrand{{Brand: "Chromium", Version: "132"}, {Brand: "Not-A.Brand", Version: "8"}}
	got := FullVersionListFor(chrome, "132.0.0.0", "chrome", "")
	want := []ClientHintBrand{{Brand: "Chromium", Version: "132.0.0.0"}, {Brand: "Not-A.Brand", Version: "8.0.0.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chrome list = %+v, want %+v", got, want)
	}
	// Edge list mirrors the short-brand set with full versions.
	edge := append(append([]ClientHintBrand{}, chrome...), ClientHintBrand{Brand: "Microsoft Edge", Version: "131"})
	got = FullVersionListFor(edge, "131.0.0.0", "edge", "131.0.0.0")
	if len(got) != 3 || got[2] != (ClientHintBrand{Brand: "Microsoft Edge", Version: "131.0.0.0"}) {
		t.Errorf("edge list = %+v, want 3 entries ending in Edge 131.0.0.0", got)
	}
	// Incomplete inputs yield no list rather than a host-mixed one.
	if got := FullVersionListFor(chrome, "", "chrome", ""); got != nil {
		t.Errorf("versionless list = %+v, want nil", got)
	}
	if got := FullVersionListFor(nil, "132.0.0.0", "chrome", ""); got != nil {
		t.Errorf("brandless list = %+v, want nil", got)
	}
	if got := FullVersionListFor(edge, "132.0.0.0", "edge", ""); got == nil || got[2] != (ClientHintBrand{Brand: "Microsoft Edge", Version: "132.0.0.0"}) {
		t.Errorf("edge list without product full = %+v, want engine-build fallback (never host-mixed)", got)
	}
}

func TestFullVersionForTruncatedTokens(t *testing.T) {
	// The shipped database truncates product tokens ("Edg/131.0.0."); the
	// major reconstruction keeps the value shape-valid and consistent.
	if got := FullVersionFor("Chrome/131.0.0.1 Safari/537.36 Edg/131.0.0.", "edge"); got != "131.0.0.0" {
		t.Errorf("truncated Edg full = %q, want 131.0.0.0", got)
	}
	if got := FullVersionFor("Chrome/130.0.0.1 Safari/537.36 OPR/115.0.0.", "opera"); got != "115.0.0.0" {
		t.Errorf("truncated OPR full = %q, want 115.0.0.0", got)
	}
}

func TestHighEntropySpoofBlockContract(t *testing.T) {
	details := ClientHintsDetails{
		FullVersion: "132.0.0.0", PlatformVersion: "14.0.0", Architecture: "arm",
		Model: "", Bitness: "64",
		FullVersionList: []ClientHintBrand{{Brand: "Chromium", Version: "132.0.0.0"}},
	}
	script := highEntropySpoofBlock(details)
	// Native delegation first: permission behavior and result shape come
	// from the native call, never from an unconditional resolve.
	if !strings.Contains(script, "return nativeHints(hints).then(native => {") {
		t.Error("wrapper must delegate to native first")
	}
	if strings.Contains(script, "Promise.resolve(out)") {
		t.Error("wrapper must not bypass native with an unconditional resolve")
	}
	// Allowlist-gated overlay only; low-entropy names never synthesized.
	for _, token := range []string{`"platformVersion"`, `"architecture"`, `"model"`, `"bitness"`, `"uaFullVersion"`, `"fullVersionList"`, `"wow64"`, `"formFactors"`} {
		if !strings.Contains(script, token) {
			t.Errorf("wrapper lacks allowlisted hint %s", token)
		}
	}
	if strings.Contains(script, `"platform":`) {
		t.Error("wrapper must not synthesize the low-entropy platform field")
	}
	// Omitted argument defaults to the full vocabulary (native parity).
	if !strings.Contains(script, "Array.isArray(hints) ? hints : highEntropy") {
		t.Error("wrapper must default omitted hints to the full vocabulary")
	}
	// Native-exposure gating (Permissions Policy): spoofed values replace
	// only fields the native result actually exposed.
	if !strings.Contains(script, "Object.prototype.hasOwnProperty.call(result, h)") {
		t.Error("wrapper must only replace fields exposed by the native response")
	}
	// Full-version list travels when resolvable.
	if !strings.Contains(script, `"fullVersionList"`) || !strings.Contains(script, `"132.0.0.0"`) {
		t.Error("wrapper lacks the resolvable fullVersionList")
	}
}

func TestAbortFailedSession(t *testing.T) {
	const session = "sess-test-abort"
	identityScripts.Lock()
	identityScripts.bySession[session] = "stale-id"
	identityScripts.Unlock()
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp:       func(cmd string, params map[string]interface{}) (interface{}, error) { return nil, nil },
	}
	// Close/Quit need tracking: wrap via counters below.
	closer := &closeQuitCounter{WebDriver: wd}
	abortFailedSession(closer)
	if closer.closes != 1 || closer.quits != 1 {
		t.Errorf("closes=%d quits=%d, want exactly one teardown each", closer.closes, closer.quits)
	}
	if _, ok := trackedID(t, session); ok {
		t.Error("aborted session registration must be forgotten")
	}
	// Nil driver is a safe no-op.
	abortFailedSession(nil)
}

type closeQuitCounter struct {
	selenium.WebDriver
	closes, quits int
}

func (f *closeQuitCounter) Close() error { f.closes++; return nil }
func (f *closeQuitCounter) Quit() error  { f.quits++; return nil }
func (f *closeQuitCounter) SessionID() string {
	if d, ok := f.WebDriver.(interface{ SessionID() string }); ok {
		return d.SessionID()
	}
	return ""
}
func TestDeviceModelForUA(t *testing.T) {
	if got := deviceModelForUA("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.0.0", false); got != "" {
		t.Errorf("desktop model = %q, want empty", got)
	}
	if got := deviceModelForUA("Mozilla/5.0 (Linux; Android 10; SM-G960F) Chrome/122.0.6261.112 Mobile", true); got != "SM-G960F" {
		t.Errorf("android model = %q, want SM-G960F", got)
	}
	if got := deviceModelForUA("Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X)", true); got != "iPhone" {
		t.Errorf("ios model = %q, want iPhone", got)
	}
	if got := deviceModelForUA("no-parens-here", true); got != "" {
		t.Errorf("parens-less model = %q, want empty", got)
	}
	if got := deviceModelForUA("Mozilla/5.0 (Linux; Android 10; K) Chrome/130.0.1.2 Mobile", true); got != "" {
		t.Errorf("truncated single-char model = %q, want empty (data artifact, see report)", got)
	}
}

func TestEstablishInitialIdentityOrder(t *testing.T) {
	// The pre-navigation contract: Network.enable first, then the UA +
	// metadata override carrying the complete required payload.
	const session = "sess-test-initial-identity"
	defer forgetIdentityScript(session)
	var cmds []string
	var overrideParams map[string]interface{}
	wd := &fakeCDPDriver{
		sessionID: session,
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			cmds = append(cmds, cmd)
			if cmd == "Network.setUserAgentOverride" {
				overrideParams = params
			}
			return map[string]interface{}{}, nil
		},
	}
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	ident := MapAdvertisedOSIdentity("darwin", false)
	brands := ClientHintBrandsFor("chrome", "132", "")
	details := NewClientHintsDetails(ua, "chrome", ident)
	if err := establishInitialIdentity(wd, 0, ua, ident, brands, details); err != nil {
		t.Fatalf("establishInitialIdentity: %v", err)
	}
	if len(cmds) != 2 || cmds[0] != "Network.enable" || cmds[1] != "Network.setUserAgentOverride" {
		t.Fatalf("CDP order = %v, want [Network.enable Network.setUserAgentOverride]", cmds)
	}
	if overrideParams["userAgent"] != ua {
		t.Errorf("override userAgent = %v", overrideParams["userAgent"])
	}
	if overrideParams["platform"] != "MacIntel" {
		t.Errorf("override platform = %v, want MacIntel", overrideParams["platform"])
	}
	meta, ok := overrideParams["userAgentMetadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing userAgentMetadata in %v", overrideParams)
	}
	// Schema-complete payload: every required metadata field present.
	for field, want := range map[string]interface{}{
		"platform": "macOS", "platformVersion": "14.0.0", "architecture": "arm",
		"model": "", "mobile": false, "fullVersion": "132.0.0.0",
	} {
		if meta[field] != want {
			t.Errorf("metadata[%q] = %v, want %v (full payload: %v)", field, meta[field], want, meta)
		}
	}
}

func TestEstablishInitialIdentityFailure(t *testing.T) {
	wd := &fakeCDPDriver{
		sessionID: "sess-test-initial-failure",
		cdp: func(cmd string, params map[string]interface{}) (interface{}, error) {
			return nil, errors.New("boom")
		},
	}
	ident := MapAdvertisedOSIdentity("windows", false)
	err := establishInitialIdentity(wd, 0, "ua", ident, ClientHintBrandsFor("chrome", "132", ""),
		NewClientHintsDetails("Chrome/132.0.0.0", "chrome", ident))
	if err == nil || !strings.Contains(err.Error(), "enabling CDP network domain") {
		t.Errorf("err = %v, want network-domain failure", err)
	}
}

func TestLinuxPlatformVersionIsEmpty(t *testing.T) {
	// Verified against Chromium source
	// (components/embedder_support/user_agent_utils.cc GetPlatformVersion):
	// native Linux reports an EMPTY platform version, so a hardcoded kernel
	// number would mismatch real Chromium-on-Linux behavior.
	linuxUA := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.6367.78 Safari/537.36"
	details := NewClientHintsDetails(linuxUA, "chrome", MapAdvertisedOSIdentity("linux", false))
	if details.PlatformVersion != "" {
		t.Errorf("linux platformVersion = %q, want empty (native Chromium-on-Linux behavior)", details.PlatformVersion)
	}
	// The empty value still travels in CDP metadata (schema-required key).
	params := userAgentOverrideParams(linuxUA, "Linux x86_64", MapAdvertisedOSIdentity("linux", false),
		ClientHintBrandsFor("chrome", "124", ""), details)
	meta, ok := params["userAgentMetadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing userAgentMetadata in %v", params)
	}
	if pv, ok := meta["platformVersion"]; !ok || pv != "" {
		t.Errorf("metadata platformVersion = %v (present=%t), want present-but-empty", pv, ok)
	}
}

// lossySessionDriver simulates a driver that clears its session ID during
// teardown: after Quit, SessionID reports "".
type lossySessionDriver struct {
	selenium.WebDriver
	id    string
	quits int
}

func (f *lossySessionDriver) SessionID() string {
	if f.quits > 0 {
		return ""
	}
	return f.id
}

func (f *lossySessionDriver) Close() error { return nil }
func (f *lossySessionDriver) Quit() error  { f.quits++; return nil }

func TestAbortFailedSessionCapturesIDBeforeTeardown(t *testing.T) {
	const session = "sess-test-abort-ordering"
	identityScripts.Lock()
	identityScripts.bySession[session] = "stale-id"
	identityScripts.Unlock()
	// abortFailedSession must capture the ID before Close/Quit so the
	// registry entry is cleared even when teardown wipes it.
	abortFailedSession(&lossySessionDriver{id: session})
	if _, ok := trackedID(t, session); ok {
		t.Error("registry entry survived teardown that cleared the session ID")
	}
}
