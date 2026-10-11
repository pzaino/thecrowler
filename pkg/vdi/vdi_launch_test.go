package vdi

import (
	"encoding/json"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// countFlag returns how many launch arguments start with the given prefix.
func countFlag(args []string, prefix string) int {
	n := 0
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			n++
		}
	}
	return n
}

// findFlag returns the first launch argument with the given prefix.
func findFlag(args []string, prefix string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return a, true
		}
	}
	return "", false
}

func TestBaseBrowserArgsWindowSize(t *testing.T) {
	for _, browserType := range []string{BrowserChrome, BrowserChromium} {
		args := baseBrowserArgs(browserType)
		found := false
		for _, a := range args {
			if a == browserWindowSizeDefault {
				found = true
			}
		}
		if !found {
			t.Errorf("baseBrowserArgs(%q) = %v, want it to contain %q (lowercase windowSize map key)",
				browserType, args, browserWindowSizeDefault)
		}
	}

	// Firefox has no windowSize entry in browserSettingsMap: absence is the
	// preserved existing behavior, not a regression.
	for _, a := range baseBrowserArgs(BrowserFirefox) {
		if a == browserWindowSizeDefault {
			t.Errorf("baseBrowserArgs(firefox) = %v, want no %q", baseBrowserArgs(BrowserFirefox), browserWindowSizeDefault)
		}
	}

	if got := baseBrowserArgs("unknown-browser"); len(got) != 0 {
		t.Errorf("baseBrowserArgs(unknown) = %v, want empty", got)
	}
}

func TestChromeDisabledFeaturesValue(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		noThird bool
		want    string
	}{
		{"default empty policy", "", false, "UserAgentClientHint,Battery"},
		{"none policy", "none", true, "UserAgentClientHint,Battery"},
		{"isolation without third-party flag", "always", false, "UserAgentClientHint,Battery,IsolateOrigins,site-per-process"},
		{"isolation with third-party flag", "always", true, "UserAgentClientHint,Battery,IsolateOrigins,site-per-process,SameSiteByDefaultCookies"},
		{"on_start policy", "on_start", false, "UserAgentClientHint,Battery,IsolateOrigins,site-per-process"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := chromeDisabledFeaturesValue(tc.policy, tc.noThird); got != tc.want {
				t.Errorf("chromeDisabledFeaturesValue(%q, %t) = %q, want %q",
					tc.policy, tc.noThird, got, tc.want)
			}
		})
	}
}

func TestChromeBlinkSettingsValue(t *testing.T) {
	if got, want := chromeBlinkSettingsValue(true, true, true, true),
		"imagesEnabled=true,CSSImagesEnabled=true,JavaScriptEnabled=true,PluginsEnabled=true"; got != want {
		t.Errorf("all-enabled = %q, want %q", got, want)
	}
	if got, want := chromeBlinkSettingsValue(false, false, false, false),
		"imagesEnabled=false,CSSImagesEnabled=false,JavaScriptEnabled=false,PluginsEnabled=false"; got != want {
		t.Errorf("all-disabled = %q, want %q", got, want)
	}
	// Mixed policies must keep all four key=value pairs in one value.
	got := chromeBlinkSettingsValue(true, false, true, false)
	for _, pair := range []string{"imagesEnabled=true", "CSSImagesEnabled=false", "JavaScriptEnabled=true", "PluginsEnabled=false"} {
		if !strings.Contains(got, pair) {
			t.Errorf("mixed value %q missing %q", got, pair)
		}
	}
}

func TestResolveEffectiveProxyURL(t *testing.T) {
	tests := []struct {
		name   string
		source string
		ok     bool
		vdi    string
		want   string
	}{
		{"absent source inherits VDI", "", false, "http://vdi-proxy:8080", "http://vdi-proxy:8080"},
		{"empty source inherits VDI", "", true, "http://vdi-proxy:8080", "http://vdi-proxy:8080"},
		{"whitespace source inherits VDI", "   ", true, "http://vdi-proxy:8080", "http://vdi-proxy:8080"},
		{"no_proxy forces direct", "no_proxy", true, "http://vdi-proxy:8080", ""},
		{"source overrides VDI", "http://source-proxy:3128", true, "http://vdi-proxy:8080", "http://source-proxy:3128"},
		{"source value is trimmed", "  http://source-proxy:3128  ", true, "http://vdi-proxy:8080", "http://source-proxy:3128"},
		{"no VDI proxy stays direct", "", false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveEffectiveProxyURL(tc.source, tc.ok, tc.vdi); got != tc.want {
				t.Errorf("resolveEffectiveProxyURL(%q, %t, %q) = %q, want %q",
					tc.source, tc.ok, tc.vdi, got, tc.want)
			}
		})
	}
}

func defaultTestCrawler() cfg.Crawler {
	return cfg.Crawler{
		RequestImages:  true,
		RequestCSS:     true,
		RequestScripts: true,
		RequestPlugins: true,
	}
}

func TestBuildLaunchArgsChromeDefaults(t *testing.T) {
	args := buildLaunchArgs(BrowserChrome, BrowserChrome, "test-ua", defaultTestCrawler(), "")

	// Window size must now resolve (P0-13 case fix).
	if countFlag(args, "--window-size=") != 1 {
		t.Errorf("want exactly one --window-size flag, got %v", args)
	}
	if _, ok := findFlag(args, "--user-agent=test-ua"); !ok {
		t.Errorf("missing --user-agent flag in %v", args)
	}

	// Exactly one --disable-features flag carrying the union.
	features := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "--disable-features=") {
			features = append(features, a)
		}
	}
	if len(features) != 1 {
		t.Fatalf("want exactly one --disable-features flag, got %v", features)
	}
	if want := "--disable-features=" + chromeDisabledFeaturesValue("", false); features[0] != want {
		t.Errorf("features flag = %q, want %q", features[0], want)
	}

	// Exactly one --blink-settings flag carrying all four policies.
	if n := countFlag(args, "--blink-settings="); n != 1 {
		t.Errorf("want exactly one --blink-settings flag, got %d in %v", n, args)
	}
	blink, _ := findFlag(args, "--blink-settings=")
	for _, pair := range []string{"imagesEnabled=true", "CSSImagesEnabled=true", "JavaScriptEnabled=true", "PluginsEnabled=true"} {
		if !strings.Contains(blink, pair) {
			t.Errorf("blink flag %q missing %q", blink, pair)
		}
	}

	// DNS privacy retained without destination-rewriting resolver rules.
	if countFlag(args, "--host-resolver-rules=") != 0 {
		t.Errorf("want no --host-resolver-rules flags, got %v", args)
	}
	if _, ok := findFlag(args, "--dns-prefetch-disable"); !ok {
		t.Errorf("want --dns-prefetch-disable retained in %v", args)
	}

	// No exact-duplicate flags anywhere in the assembled command line.
	seen := map[string]int{}
	for _, a := range args {
		seen[a]++
	}
	for flag, n := range seen {
		if n > 1 {
			t.Errorf("duplicate launch flag %q appears %d times in %v", flag, n, args)
		}
	}
}

func TestBuildLaunchArgsCookiePolicy(t *testing.T) {
	crawler := defaultTestCrawler()
	crawler.ResetCookiesPolicy = "always"
	crawler.NoThirdPartyCookies = true
	args := buildLaunchArgs(BrowserChrome, BrowserChrome, "test-ua", crawler, "")

	features, ok := findFlag(args, "--disable-features=")
	if !ok {
		t.Fatalf("missing --disable-features flag in %v", args)
	}
	for _, want := range []string{"UserAgentClientHint", "Battery", "IsolateOrigins", "site-per-process", "SameSiteByDefaultCookies"} {
		if !strings.Contains(features, want) {
			t.Errorf("features flag %q missing %q", features, want)
		}
	}
	if _, ok := findFlag(args, "--disable-site-isolation-trials"); !ok {
		t.Errorf("missing --disable-site-isolation-trials in %v", args)
	}
}

func TestBuildLaunchArgsEffectiveProxy(t *testing.T) {
	vdiProxy := "http://vdi-proxy:8080"
	sourceProxy := "http://source-proxy:3128"

	// The resolved effective proxy (source wins) must be the emitted value,
	// not the VDI-global string (wrong-variable regression test).
	args := buildLaunchArgs(BrowserChrome, BrowserChrome, "test-ua", defaultTestCrawler(), sourceProxy)
	got, ok := findFlag(args, "--proxy-server=")
	if !ok {
		t.Fatalf("missing --proxy-server flag in %v", args)
	}
	if got != "--proxy-server="+sourceProxy {
		t.Errorf("proxy flag = %q, want %q", got, "--proxy-server="+sourceProxy)
	}
	if strings.Contains(strings.Join(args, " "), vdiProxy) {
		t.Errorf("VDI proxy %q must not leak into args when source wins: %v", vdiProxy, args)
	}

	// VDI default applies when no source proxy is resolved.
	args = buildLaunchArgs(BrowserChrome, BrowserChrome, "test-ua", defaultTestCrawler(), vdiProxy)
	if got, _ := findFlag(args, "--proxy-server="); got != "--proxy-server="+vdiProxy {
		t.Errorf("VDI proxy flag = %q, want %q", got, "--proxy-server="+vdiProxy)
	}

	// Direct connection emits no proxy flags.
	args = buildLaunchArgs(BrowserChrome, BrowserChrome, "test-ua", defaultTestCrawler(), "")
	if n := countFlag(args, "--proxy-server="); n != 0 {
		t.Errorf("want no --proxy-server flag for direct connection, got %v", args)
	}
	if n := countFlag(args, "--proxy-bypass-list="); n != 0 {
		t.Errorf("want no --proxy-bypass-list flag for direct connection, got %v", args)
	}
}

func TestBuildLaunchArgsFirefoxUnaffected(t *testing.T) {
	args := buildLaunchArgs(BrowserFirefox, BrowserFirefox, "test-ua", defaultTestCrawler(), "")
	if _, ok := findFlag(args, "--user-agent=test-ua"); !ok {
		t.Errorf("firefox args missing --user-agent in %v", args)
	}
	for _, prefix := range []string{"--disable-features=", "--blink-settings=", "--disable-blink-features="} {
		if n := countFlag(args, prefix); n != 0 {
			t.Errorf("firefox args must not contain %q, got %v", prefix, args)
		}
	}
	if _, ok := findFlag(args, "--enable-logging"); !ok {
		t.Errorf("firefox args missing --enable-logging in %v", args)
	}
}

func TestBuildChromeCapabilitiesSerialization(t *testing.T) {
	prefs := map[string]interface{}{
		"download.default_directory": "/tmp",
	}
	caps := buildChromeCapabilities([]string{"--incognito"}, prefs)

	// ExcludeSwitches must live at the goog:chromeOptions level.
	if len(caps.ExcludeSwitches) != 1 || caps.ExcludeSwitches[0] != "enable-automation" {
		t.Errorf("ExcludeSwitches = %v, want [enable-automation]", caps.ExcludeSwitches)
	}
	// ... and must not leak into prefs.
	if _, ok := prefs["excludeSwitches"]; ok {
		t.Errorf("prefs must not contain excludeSwitches: %v", prefs)
	}
	if _, ok := prefs["useAutomationExtension"]; ok {
		t.Errorf("prefs must not contain useAutomationExtension: %v", prefs)
	}

	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal chrome capabilities: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal chrome capabilities: %v", err)
	}
	excl, ok := decoded["excludeSwitches"].([]interface{})
	if !ok || len(excl) != 1 || excl[0] != "enable-automation" {
		t.Errorf("serialized excludeSwitches = %v, want [enable-automation] in %s", decoded["excludeSwitches"], raw)
	}
	if prefsDecoded, ok := decoded["prefs"].(map[string]interface{}); ok {
		if _, bad := prefsDecoded["excludeSwitches"]; bad {
			t.Errorf("serialized prefs must not contain excludeSwitches: %s", raw)
		}
		if _, bad := prefsDecoded["useAutomationExtension"]; bad {
			t.Errorf("serialized prefs must not contain useAutomationExtension: %s", raw)
		}
	} else {
		t.Errorf("serialized capabilities missing prefs object: %s", raw)
	}
	if strings.Contains(string(raw), "useAutomationExtension") {
		t.Errorf("serialized capabilities must not mention useAutomationExtension: %s", raw)
	}
}
