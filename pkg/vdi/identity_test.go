package vdi

import (
	"errors"
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
	script := antiDetectionScript(ua, ident, brands)

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

func TestAntiDetectionScriptEdgeIdentity(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
	ident := MapAdvertisedOSIdentity("windows", false)
	script := antiDetectionScript(ua, ident, ClientHintBrandsFor("edge", "131", "131"))
	if !strings.Contains(script, `"Microsoft Edge"`) || !strings.Contains(script, `"131"`) {
		t.Errorf("edge script lacks Microsoft Edge brand:\n%s", script)
	}
}

func TestAntiDetectionScriptNonChromeIdentity(t *testing.T) {
	ident := MapAdvertisedOSIdentity("linux", false)
	script := antiDetectionScript("Mozilla/5.0 Firefox/133.0", ident, nil)
	if strings.Contains(script, `"brands"`) || strings.Contains(script, "Not-A.Brand") {
		t.Error("non-Chrome identity must not advertise fabricated Client Hints brands")
	}
	if !strings.Contains(script, `"Linux x86_64"`) {
		t.Error("script lacks mapped navigator.platform for linux")
	}
}

func TestUserAgentOverrideParams(t *testing.T) {
	ident := MapAdvertisedOSIdentity("darwin", false)
	params := userAgentOverrideParams("test-ua", ident.NavigatorPlatform, ident, ClientHintBrandsFor("chrome", "132", ""))

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
	params = userAgentOverrideParams("firefox-ua", "Linux x86_64", MapAdvertisedOSIdentity("linux", false), nil)
	if _, ok := params["userAgentMetadata"]; ok {
		t.Errorf("non-Chrome params must omit userAgentMetadata: %v", params)
	}
	if params["platform"] != "Linux x86_64" {
		t.Errorf("platform = %v", params["platform"])
	}

	// Edge identity: product brand present with its own major.
	edgeParams := userAgentOverrideParams("edge-ua", "Win32", MapAdvertisedOSIdentity("windows", false),
		ClientHintBrandsFor("edge", "131", "131"))
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
