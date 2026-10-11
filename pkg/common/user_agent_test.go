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
		{" Chromium ", "chrome"}, // VDI-003: chromium shares chrome's UA group
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
		{OS: "linux", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{{UA: "UA-linux-chrome-{random_int2}", PCT: 50}}},
		{OS: "windows", BRG: "chrome", Type: "desktop", UserAgents: []UserAgent{{UA: "UA-win-chrome", PCT: 25}}},
		{OS: "android", BRG: "chrome", Type: "mobile", UserAgents: []UserAgent{{UA: "UA-android-chrome-mobile", PCT: 48}}},
		{OS: "linux", BRG: "firefox", Type: "desktop", UserAgents: []UserAgent{{UA: "UA-linux-firefox", PCT: 7}}},
	}}
}

func TestSelectUserAgentChromiumSharesChromeGroup(t *testing.T) {
	db := syntheticUA()
	// VDI-003: "chromium" must hit the chrome group, not the static fallback.
	ua, resolvedOS, fallback := db.SelectUserAgent("desktop", "linux", "chromium", false, func(int) int { return 3 })
	if fallback {
		t.Errorf("chromium selection unexpectedly used fallback: %q", ua)
	}
	if resolvedOS != "linux" {
		t.Errorf("resolvedOS = %q, want linux", resolvedOS)
	}
	if want := "UA-linux-chrome-3"; ua != want {
		t.Errorf("ua = %q, want %q", ua, want)
	}
}

func TestSelectUserAgentFallbackIsProfileAware(t *testing.T) {
	db := &UserAgentsDB{} // empty: every lookup misses
	// Chromium falls back to the chrome family, same device class.
	ua, _, fallback := db.SelectUserAgent("desktop", "linux", "chromium", false, stubIntN)
	if !fallback {
		t.Error("empty DB must report fallbackUsed")
	}
	if want := UsrAgentStrMap["chrome-desktop01"]; ua != want {
		t.Errorf("chromium desktop fallback = %q, want chrome-desktop01 %q", ua, want)
	}
	ua, _, _ = db.SelectUserAgent("mobile", "android", "firefox", true, stubIntN)
	if want := UsrAgentStrMap["firefox-mobile01"]; ua != want {
		t.Errorf("firefox mobile fallback = %q, want %q", ua, want)
	}
}

func TestSelectUserAgentRandomOSConsistency(t *testing.T) {
	db := syntheticUA()
	// One selection event resolves a single OS key used for every surface.
	ua, resolvedOS, fallback := db.SelectUserAgent("desktop", "random", "chrome", false, func(int) int { return 0 })
	if fallback || resolvedOS != "windows" || ua != "UA-win-chrome" {
		t.Errorf("random selection = (%q, %q, %t), want (UA-win-chrome, windows, false)", ua, resolvedOS, fallback)
	}
}

func TestUserAgentCompatibilityNote(t *testing.T) {
	if got := UserAgentCompatibilityNote("Mozilla/5.0 Chrome/131.0.0.0", "chrome", false); got != "" {
		t.Errorf("clean selection note = %q, want empty", got)
	}
	if got := UserAgentCompatibilityNote("", "chrome", false); got == "" {
		t.Error("empty UA must produce a note")
	}
	if got := UserAgentCompatibilityNote("x", "chromium", true); !strings.Contains(got, "fallback") {
		t.Errorf("fallback note = %q, want mention of fallback", got)
	}
	if got := UserAgentCompatibilityNote("Mozilla/5.0 Firefox/85.0", "chrome", false); got == "" {
		t.Error("chrome family with Firefox UA must produce a note")
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
