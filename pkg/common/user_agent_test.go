package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUAFamilyForBrowser(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"chrome", "chrome"},
		{" Chromium ", "chrome"}, // chromium shares chrome's UA group
		{"CHROMIUM", "chrome"},
		{"firefox", "firefox"},
		{" Firefox ", "firefox"},
		{"safari", "safari"}, // unknown types pass through for existing fallback
		{"", ""},
	}
	for _, tc := range tests {
		if got := UAFamilyForBrowser(tc.in); got != tc.want {
			t.Errorf("UAFamilyForBrowser(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// stubIntN returns a deterministic intN for tests: always the upper bound - 1.
func stubIntN(n int) int {
	if n <= 0 {
		return 0
	}
	return n - 1
}

func TestExpandUserAgentTemplate(t *testing.T) {
	// No placeholders: custom UAs pass through untouched, never rejected.
	custom := "MyCustomBot/1.0 (compatible; +https://example.com/bot)"
	if got := ExpandUserAgentTemplate(custom, stubIntN); got != custom {
		t.Errorf("passthrough = %q, want %q", got, custom)
	}

	// Placeholder ranges preserved: int1 in [0,8000), int2 in [0,999).
	got := ExpandUserAgentTemplate("Chrome/132.0.{random_int1}.{random_int2}", stubIntN)
	if want := "Chrome/132.0.7999.998"; got != want {
		t.Errorf("expanded = %q, want %q", got, want)
	}

	// Multiple occurrences of the same placeholder all expand.
	got = ExpandUserAgentTemplate("{random_int2}-{random_int2}", func(int) int { return 7 })
	if got != "7-7" {
		t.Errorf("multi expand = %q, want %q", got, "7-7")
	}

	// Special characters in the template survive expansion (encoding of the
	// result for script embedding is covered by TestJSStringLiteral).
	special := `A"b\cünicode {random_int2}`
	got = ExpandUserAgentTemplate(special, func(int) int { return 1 })
	if !strings.HasPrefix(got, `A"b\cünicode 1`) {
		t.Errorf("special-char template = %q", got)
	}

	// Zero-returning source is honored (no production determinism implied).
	if got := ExpandUserAgentTemplate("{random_int1}", func(int) int { return 0 }); got != "0" {
		t.Errorf("zero expand = %q, want %q", got, "0")
	}
}

func TestResolveAdvertisedOS(t *testing.T) {
	for _, tc := range []struct {
		platform, class, want string
	}{
		{"windows", "desktop", "windows"},
		{"darwin", "desktop", "darwin"},
		{"linux", "desktop", "linux"},
		{"", "desktop", "linux"}, // empty defaults to linux (Validate normalization)
		{"android", "mobile", "android"},
		{"ios", "mobile", "ios"},
	} {
		if got := ResolveAdvertisedOS(tc.platform, tc.class, stubIntN); got != tc.want {
			t.Errorf("ResolveAdvertisedOS(%q, %q) = %q, want %q", tc.platform, tc.class, got, tc.want)
		}
	}

	// "random" resolves within the device-class pool, deterministically here.
	if got := ResolveAdvertisedOS("random", "desktop", func(int) int { return 0 }); got != "windows" {
		t.Errorf("random desktop = %q, want windows", got)
	}
	if got := ResolveAdvertisedOS("random", "mobile", func(int) int { return 1 }); got != "ios" {
		t.Errorf("random mobile = %q, want ios", got)
	}
	if got := ResolveAdvertisedOS(" random ", "", stubIntN); got != "darwin" {
		t.Errorf("random default class = %q, want darwin (stub picks last)", got)
	}
}

// syntheticUA returns a minimal in-memory database for selection tests.
func syntheticUA() *UserAgentsDB {
	return &UserAgentsDB{UserAgentsGroups: []UserAgentGroup{
		{OS: "linux", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{{BR: "chrome", UA: "UA-linux-chrome-{random_int2}", PCT: 50}}},
		{OS: "windows", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{{BR: "edge", UA: "UA-win-edge Edg/131", PCT: 25}}},
		{OS: "android", BRG: "chrome", Type: "mobile", UserAgents: []UserAgent{{BR: "chrome", UA: "UA-android-chrome-mobile", PCT: 48}}},
		{OS: "linux", BRG: "firefox", Type: "desktop", UserAgents: []UserAgent{{BR: "firefox", UA: "UA-linux-firefox", PCT: 7}}},
	}}
}

func TestSelectUserAgentChromiumSharesChromeGroup(t *testing.T) {
	db := syntheticUA()
	// "chromium" must hit the chrome group, not the static fallback.
	sel := db.SelectUserAgent("desktop", "linux", "chromium", false, func(int) int { return 3 })
	if sel.FallbackUsed {
		t.Errorf("chromium selection unexpectedly used fallback: %q", sel.UserAgent)
	}
	if sel.ResolvedOS != "linux" {
		t.Errorf("resolvedOS = %q, want linux", sel.ResolvedOS)
	}
	if want := "UA-linux-chrome-3"; sel.UserAgent != want {
		t.Errorf("ua = %q, want %q", sel.UserAgent, want)
	}
	if sel.Family != "chrome" || sel.Product != "chrome" {
		t.Errorf("family/product = %q/%q, want chrome/chrome", sel.Family, sel.Product)
	}
}

func TestSelectUserAgentPreservesProductMetadata(t *testing.T) {
	db := syntheticUA()
	// Review finding 1: the entry BR field must survive selection.
	sel := db.SelectUserAgent("desktop", "windows", "chrome", false, stubIntN)
	if sel.FallbackUsed {
		t.Fatalf("expected DB hit, got fallback: %q", sel.UserAgent)
	}
	if sel.Product != "edge" {
		t.Errorf("product = %q, want edge (entry BR preserved)", sel.Product)
	}
}

func TestSelectUserAgentFallbackIsProfileAware(t *testing.T) {
	db := &UserAgentsDB{} // empty: every lookup misses
	// Chromium falls back to the chrome family, same device class.
	sel := db.SelectUserAgent("desktop", "linux", "chromium", false, stubIntN)
	if !sel.FallbackUsed {
		t.Error("empty DB must report fallbackUsed")
	}
	if want := UsrAgentStrMap["chrome-desktop01"]; sel.UserAgent != want {
		t.Errorf("chromium desktop fallback = %q, want chrome-desktop01 %q", sel.UserAgent, want)
	}
	// Static chrome-desktop01 UA carries a Linux token: advertised OS follows.
	if sel.ResolvedOS != "linux" {
		t.Errorf("fallback resolvedOS = %q, want linux (from UA token)", sel.ResolvedOS)
	}
	sel = db.SelectUserAgent("mobile", "android", "firefox", true, stubIntN)
	if want := UsrAgentStrMap["firefox-mobile01"]; sel.UserAgent != want {
		t.Errorf("firefox mobile fallback = %q, want %q", sel.UserAgent, want)
	}
	if sel.ResolvedOS != "android" {
		t.Errorf("firefox mobile resolvedOS = %q, want android", sel.ResolvedOS)
	}
}

func TestSelectUserAgentFallbackAdjustsMissingOS(t *testing.T) {
	db := syntheticUA()
	// Review finding 3: no mobile/ios/chrome group exists. The fallback must
	// not pair an Android UA with iOS metadata: ResolvedOS follows the UA.
	sel := db.SelectUserAgent("mobile", "ios", "chrome", true, stubIntN)
	if !sel.FallbackUsed {
		t.Error("ios/chrome must miss and fall back")
	}
	if want := "UA-android-chrome-mobile"; sel.UserAgent != want {
		t.Errorf("ua = %q, want %q", sel.UserAgent, want)
	}
	if sel.ResolvedOS != "android" {
		t.Errorf("resolvedOS = %q, want android (matching the UA, never ios)", sel.ResolvedOS)
	}

	// Same for a missing desktop combination: darwin/firefox has no group;
	// linux is preferred over windows.
	sel = db.SelectUserAgent("desktop", "darwin", "firefox", false, stubIntN)
	if !sel.FallbackUsed {
		t.Error("darwin/firefox must miss and fall back")
	}
	if sel.UserAgent != "UA-linux-firefox" || sel.ResolvedOS != "linux" {
		t.Errorf("got (%q, %q), want (UA-linux-firefox, linux)", sel.UserAgent, sel.ResolvedOS)
	}
}

func TestSelectUserAgentDegradesUnknownProduct(t *testing.T) {
	db := syntheticUA()
	// No edge group exists anywhere: degrade to chrome, keep OS consistent.
	sel := db.SelectUserAgent("desktop", "windows", "edge", false, stubIntN)
	if !sel.FallbackUsed || !sel.DegradedProduct {
		t.Errorf("edge selection must degrade (fallback=%t degraded=%t): %#v", sel.FallbackUsed, sel.DegradedProduct, sel)
	}
	if sel.UserAgent != "UA-win-edge Edg/131" {
		t.Errorf("degraded ua = %q, want windows chrome-group entry", sel.UserAgent)
	}
	if sel.ResolvedOS != "windows" {
		t.Errorf("degraded resolvedOS = %q, want windows", sel.ResolvedOS)
	}
	// Fully unknown family on empty DB: empty UA, still consistent + diagnosed.
	// (The static chrome fallback UA carries a Linux token, so linux is
	// advertised rather than the requested windows: never contradict the UA.)
	sel = (&UserAgentsDB{}).SelectUserAgent("desktop", "windows", "edge", false, stubIntN)
	if sel.UserAgent == "" || !sel.DegradedProduct {
		t.Errorf("empty-DB edge selection = %#v, want degraded chrome fallback", sel)
	}
	if sel.ResolvedOS != "linux" {
		t.Errorf("empty-DB resolvedOS = %q, want linux (from fallback UA token)", sel.ResolvedOS)
	}
}

func TestSelectUserAgentRandomOSConsistency(t *testing.T) {
	db := syntheticUA()
	// One selection event resolves a single OS key used for every surface.
	sel := db.SelectUserAgent("desktop", "random", "chrome", false, func(int) int { return 0 })
	if sel.FallbackUsed || sel.ResolvedOS != "windows" || sel.UserAgent != "UA-win-edge Edg/131" {
		t.Errorf("random selection = (%q, %q, %t), want (UA-win-edge Edg/131, windows, false)",
			sel.UserAgent, sel.ResolvedOS, sel.FallbackUsed)
	}
	if sel.Product != "edge" {
		t.Errorf("random selection product = %q, want edge", sel.Product)
	}
}

func TestBrowserProduct(t *testing.T) {
	edgeUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
	legacyEdgeUA := "Mozilla/5.0 (Windows NT 10.0) Chrome/70.0.3538.0 Safari/537.36 Edge/18.1958"
	operaUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0"
	chromeUA := "Mozilla/5.0 (X11; Linux x86_64) Chrome/124.0.6367.78 Safari/537.36"
	firefoxUA := "Mozilla/5.0 (X11; Linux x86_64; rv:85.0) Gecko/20100101 Firefox/85.0"
	for _, tc := range []struct {
		ua, br, want string
	}{
		{edgeUA, "edge", "edge"},
		{edgeUA, "", "edge"},           // tokens win over missing metadata
		{legacyEdgeUA, "edge", "edge"}, // EdgeHTML keeps its product label (no CH brands downstream)
		{operaUA, "opera", "opera"},
		{chromeUA, "herring", "herring"}, // database product preserved on Chrome-token UAs
		{chromeUA, "", "chrome"},
		{firefoxUA, "firefox", "firefox"},
		{"Mozilla/5.0 (Macintosh) Safari/605.1.15", "safari", "safari"},
		{"custom-thing/1.0", "", ""},
		{"custom-thing/1.0", "chrome", "chrome"},
	} {
		if got := BrowserProduct(tc.ua, tc.br); got != tc.want {
			t.Errorf("BrowserProduct(%q, %q) = %q, want %q", tc.ua, tc.br, got, tc.want)
		}
	}
}

func TestProductMajorVersion(t *testing.T) {
	edgeUA := "Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
	operaUA := "Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0"
	if got := ProductMajorVersion(edgeUA, "edge"); got != "131" {
		t.Errorf("edge major = %q, want 131", got)
	}
	if got := ProductMajorVersion(operaUA, "opera"); got != "115" {
		t.Errorf("opera major = %q, want 115", got)
	}
	if got := ProductMajorVersion("Chrome/124.0.0.0", "chrome"); got != "" {
		t.Errorf("chrome product major = %q, want empty", got)
	}
	if got := ProductMajorVersion("Edge/18.1958", "edge"); got != "" {
		t.Errorf("legacy edge product major = %q, want empty", got)
	}
}

func TestUserAgentCompatibilityNote(t *testing.T) {
	if got := UserAgentCompatibilityNote(UASelection{UserAgent: "Mozilla/5.0 Chrome/131.0.0.0", Family: "chrome"}); got != "" {
		t.Errorf("clean selection note = %q, want empty", got)
	}
	if got := UserAgentCompatibilityNote(UASelection{Family: "chrome"}); got == "" {
		t.Error("empty UA must produce a note")
	}
	if got := UserAgentCompatibilityNote(UASelection{UserAgent: "x", Family: "chromium", FallbackUsed: true}); !strings.Contains(got, "fallback") {
		t.Errorf("fallback note = %q, want mention of fallback", got)
	}
	if got := UserAgentCompatibilityNote(UASelection{UserAgent: "Mozilla/5.0 Firefox/85.0", Family: "chrome"}); got == "" {
		t.Error("chrome family with Firefox UA must produce a note")
	}
	if got := UserAgentCompatibilityNote(UASelection{UserAgent: "x", Family: "edge", FallbackUsed: true, DegradedProduct: true}); !strings.Contains(got, "chrome instead") {
		t.Errorf("degraded note = %q, want mention of chrome degradation", got)
	}
}

func TestJSStringLiteral(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain", `"plain"`},
		{`quote"d`, `"quote\"d"`},
		{`back\slash`, `"back\\slash"`},
		{"new\nline", `"new\nline"`},
		{"ünicode ✓", `"ünicode ✓"`},
		{"", `""`},
	}
	for _, tc := range tests {
		if got := JSStringLiteral(tc.in); got != tc.want {
			t.Errorf("JSStringLiteral(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	// A hostile UA cannot break out of the literal: the encoded form must
	// round-trip back to the exact input through JSON decoding.
	hostile := `x"); alert(1); ("`
	lit := JSStringLiteral(hostile)
	var decoded string
	if err := json.Unmarshal([]byte(lit), &decoded); err != nil {
		t.Fatalf("encoded hostile UA is not valid JSON string: %s (%v)", lit, err)
	}
	if decoded != hostile {
		t.Errorf("round-trip = %q, want %q", decoded, hostile)
	}
}

func TestFindEntryRejectsEmptyTemplates(t *testing.T) {
	// Follow-up finding 3 (P2): an empty template must never be selected,
	// even when it shares a group with valid entries.
	db := &UserAgentsDB{UserAgentsGroups: []UserAgentGroup{
		{OS: "linux", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{
			{BR: "chrome", UA: "", PCT: 90},
			{BR: "chrome", UA: "   ", PCT: 5},
			{BR: "chrome", UA: "VALID-UA", PCT: 5},
		}},
	}}
	for i := 0; i < 25; i++ {
		template, _, ok := db.findEntry("desktop", "linux", "chrome")
		if !ok {
			t.Fatal("findEntry reported miss despite a valid candidate")
		}
		if template != "VALID-UA" {
			t.Fatalf("findEntry = %q, want the only valid candidate", template)
		}
	}

	// An all-empty group misses, driving the fallback chain instead of an
	// empty UA.
	emptyDB := &UserAgentsDB{UserAgentsGroups: []UserAgentGroup{
		{OS: "linux", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{{BR: "chrome", UA: ""}}},
	}}
	if _, _, ok := emptyDB.findEntry("desktop", "linux", "chrome"); ok {
		t.Error("all-empty group must miss")
	}
	sel := emptyDB.SelectUserAgent("desktop", "linux", "chrome", false, stubIntN)
	if !sel.FallbackUsed || sel.UserAgent == "" {
		t.Errorf("all-empty group selection = %#v, want non-empty fallback", sel)
	}
	if sel.ResolvedOS != "linux" {
		t.Errorf("fallback resolvedOS = %q, want linux", sel.ResolvedOS)
	}
}
