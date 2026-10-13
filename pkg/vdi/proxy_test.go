package vdi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-auxiliaries/selenium"
	"github.com/go-auxiliaries/selenium/chrome"
	"github.com/go-auxiliaries/selenium/firefox"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestEffectiveSessionProxyMatrix(t *testing.T) {
	const vdiProxy = "http://vdi-proxy:8080"
	const customProxy = "http://custom-proxy:3128"
	const topProxy = "http://source-proxy:8888"
	tests := []struct {
		name   string
		top    string
		topOK  bool
		custom string
		vdi    string
		want   string
	}{
		{"all absent inherits VDI", "", false, "", vdiProxy, vdiProxy},
		{"custom fills absent top", "", false, customProxy, vdiProxy, customProxy},
		{"custom whitespace ignored", "", false, "  \t ", vdiProxy, vdiProxy},
		{"custom no_proxy disables", "", false, "no_proxy", vdiProxy, ""},
		{"custom no_proxy padded", "", false, "  no_proxy  ", vdiProxy, ""},
		{"top wins over custom", topProxy, true, customProxy, vdiProxy, topProxy},
		{"top no_proxy wins over custom", "no_proxy", true, customProxy, vdiProxy, ""},
		{"top empty falls to custom", "", true, customProxy, vdiProxy, customProxy},
		{"top blank falls to custom", "   ", true, customProxy, vdiProxy, customProxy},
		{"top wins over VDI", topProxy, true, "", vdiProxy, topProxy},
		{"malformed top passes through unvalidated", "http://bad host:1/x y", true, "", vdiProxy, "http://bad host:1/x y"},
		{"no proxy anywhere stays direct", "", false, "", "", ""},
		// Case sensitivity is preserved exactly as configured: only the
		// exact lowercase sentinel disables.
		{"uppercase sentinel is a value not a sentinel", "NO_PROXY", true, "", vdiProxy, "NO_PROXY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveSessionProxy(tc.top, tc.topOK, tc.custom, tc.vdi); got != tc.want {
				t.Errorf("effectiveSessionProxy(%q, %t, %q, %q) = %q, want %q",
					tc.top, tc.topOK, tc.custom, tc.vdi, got, tc.want)
			}
		})
	}
}

func TestCustomSeleniumProxyURL(t *testing.T) {
	processCfg := cfg.Config{Selenium: []cfg.Selenium{
		{Name: "vdi-1", ProxyURL: "http://custom-1:1111"},
		{Name: "vdi-2", ProxyURL: ""},
	}}
	if got := customSeleniumProxyURL(processCfg, "vdi-1"); got != "http://custom-1:1111" {
		t.Errorf("match = %q", got)
	}
	if got := customSeleniumProxyURL(processCfg, "vdi-2"); got != "" {
		t.Errorf("empty entry = %q, want empty", got)
	}
	if got := customSeleniumProxyURL(processCfg, "missing"); got != "" {
		t.Errorf("missing name = %q, want empty (no override, no panic)", got)
	}
	if got := customSeleniumProxyURL(processCfg, ""); got != "" {
		t.Errorf("empty name = %q, want empty", got)
	}
	if got := customSeleniumProxyURL(cfg.Config{}, "vdi-1"); got != "" {
		t.Errorf("empty process list = %q, want empty", got)
	}
	if got := customSeleniumProxyURL(cfg.Config{Selenium: []cfg.Selenium{{Name: " vdi-1 ", ProxyURL: "  http://x:1  "}}}, "vdi-1"); got != "http://x:1" {
		t.Errorf("whitespace = %q, want trimmed", got)
	}
}

func TestProxyBypassList(t *testing.T) {
	bypass := proxyBypassList()
	if len(bypass) == 0 || bypass[0] != "localhost" {
		t.Errorf("bypass = %v, want localhost first", bypass)
	}
}

func TestFirefoxProxyCapability(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHTTP  string
		wantSSL   string
		wantSOCKS string
		wantVer   int
		wantErr   bool
	}{
		{
			name:  "direct",
			input: "",
		},
		{
			name:  "whitespace",
			input: "   ",
		},
		{
			name:     "HTTP MicroProxy",
			input:    "http://127.0.0.1:8082",
			wantHTTP: "127.0.0.1:8082",
			wantSSL:  "127.0.0.1:8082",
		},
		{
			name:      "SOCKS5 MicroProxy",
			input:     "socks5://127.0.0.1:1080",
			wantSOCKS: "127.0.0.1:1080",
			wantVer:   5,
		},
		{
			name:     "IPv6 listener",
			input:    "http://[::1]:8082",
			wantHTTP: "[::1]:8082",
			wantSSL:  "[::1]:8082",
		},
		{
			name:    "invalid port",
			input:   "http://127.0.0.1:99999",
			wantErr: true,
		},
		{
			name:    "missing port",
			input:   "http://127.0.0.1",
			wantErr: true,
		},
		{
			name:    "browser authentication prohibited",
			input:   "http://user:password@127.0.0.1:8082",
			wantErr: true,
		},
		{
			name:    "HTTPS downstream unsupported",
			input:   "https://127.0.0.1:8443",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := firefoxProxyCapability(tc.input)

			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			if tc.wantHTTP == "" && tc.wantSOCKS == "" {
				if got != nil {
					t.Fatalf("expected nil proxy, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected proxy capability")
			}

			if got.Type != selenium.Manual {
				t.Errorf("type = %q, want manual", got.Type)
			}
			if got.HTTP != tc.wantHTTP || got.SSL != tc.wantSSL {
				t.Errorf("HTTP/SSL = %q/%q, want %q/%q",
					got.HTTP, got.SSL, tc.wantHTTP, tc.wantSSL)
			}
			if got.SOCKS != tc.wantSOCKS ||
				got.SOCKSVersion != tc.wantVer {
				t.Errorf("SOCKS/version = %q/%d, want %q/%d",
					got.SOCKS, got.SOCKSVersion,
					tc.wantSOCKS, tc.wantVer)
			}
		})
	}
}

func proxyTestCrawler() cfg.Crawler {
	return cfg.Crawler{
		RequestImages:  true,
		RequestCSS:     true,
		RequestScripts: true,
		RequestPlugins: true,
	}
}

func hasFlag(args []string, prefix string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return a, true
		}
	}
	return "", false
}

func TestBuildLaunchArgsProxySurfaces(t *testing.T) {
	const proxy = "http://proxy:8080"
	chromeArgs := buildLaunchArgs(BrowserChrome, BrowserChrome, "ua", proxyTestCrawler(), proxy)
	if got, ok := hasFlag(chromeArgs, "--proxy-server="); !ok || got != "--proxy-server="+proxy {
		t.Errorf("chrome proxy flag = %q, %t", got, ok)
	}

	// Firefox must not receive binary-unknown proxy switches; its proxy
	// travels via the W3C capability instead.
	firefoxArgs := buildLaunchArgs(BrowserFirefox, BrowserFirefox, "ua", proxyTestCrawler(), proxy)
	for _, prefix := range []string{"--proxy-server=", "--force-proxy-for-all", "--proxy-bypass-list="} {
		if got, ok := hasFlag(firefoxArgs, prefix); ok {
			t.Errorf("firefox args must not contain %q (got %q)", prefix, got)
		}
	}
	if _, ok := hasFlag(firefoxArgs, "--user-agent=ua"); !ok {
		t.Error("firefox args lost the user-agent flag")
	}
}

func TestRedactProxyURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http://user:s3cr3t@proxy:8080", "http://***@proxy:8080"},
		{"http://user@proxy:8080", "http://***@proxy:8080"},
		{"http://proxy:8080", "http://proxy:8080"},
		{"http://proxy:8080/some/path?q=1", "http://proxy:8080/some/path?q=1"},
		{"socks5://u:p@h:1080", "socks5://***@h:1080"},
		{"", ""},
		{"   ", ""},
		{"http://[::1", "http://[::1"}, // malformed but secret-free: stays visible for diagnosis
		{"justastring", "justastring"}, // no userinfo shape: nothing to scrub
		{"user:pass@host:9", "***@host:9"},
		{"user@", "redacted-invalid-proxy"},
		{"u@ho st", "redacted-invalid-proxy"},
	}
	for _, tc := range tests {
		if got := redactProxyURL(tc.in); got != tc.want {
			t.Errorf("redactProxyURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRedactedCapabilities(t *testing.T) {
	const secret = "http://user:s3cr3t@proxy:8080"
	args := []string{"--incognito", "--proxy-server=" + secret, "--v=1"}
	caps := selenium.Capabilities{"browserName": "chrome"}
	caps.AddChrome(chrome.Capabilities{Args: args, W3C: true, Prefs: map[string]interface{}{"k": "v"}})

	redacted := redactedCapabilities(caps)
	cc, ok := redacted[chrome.CapabilitiesKey].(chrome.Capabilities)
	if !ok {
		t.Fatalf("redacted caps missing chrome options: %v", redacted)
	}
	joined := strings.Join(cc.Args, " ")
	if strings.Contains(joined, "s3cr3t") || strings.Contains(joined, "user@") {
		t.Errorf("redacted args leak credentials: %v", cc.Args)
	}
	if !strings.Contains(joined, "--proxy-server=http://***@proxy:8080") {
		t.Errorf("redacted args lost host/port context: %v", cc.Args)
	}
	// Original must be unmutated.
	orig := caps[chrome.CapabilitiesKey].(chrome.Capabilities)
	if strings.Join(orig.Args, " ") != strings.Join(args, " ") {
		t.Errorf("original caps mutated: %v", orig.Args)
	}
	// Firefox branch redacts too; nil and proxy-free caps pass through.
	fcaps := selenium.Capabilities{"browserName": "firefox"}
	fcaps.AddFirefox(firefox.Capabilities{Args: args})
	if got := redactedCapabilities(fcaps)[firefox.CapabilitiesKey].(firefox.Capabilities); strings.Contains(strings.Join(got.Args, " "), "s3cr3t") {
		t.Errorf("firefox redaction leaks: %v", got.Args)
	}
	if redactedCapabilities(nil) != nil {
		t.Error("nil caps must stay nil")
	}
	plain := selenium.Capabilities{"browserName": "chrome"}
	if _, ok := redactedCapabilities(plain)["browserName"]; !ok {
		t.Error("proxy-free caps altered")
	}
}

func TestCustomVDIMergeFeedsSessionResolution(t *testing.T) {
	// End-to-end across the merge/resolve seam using the documented source
	// shape {"custom": {"vdi": [{"proxy_url": ...}]}}: the merged process
	// config feeds the name-matched lookup, which feeds session resolution.
	base := cfg.Config{Selenium: []cfg.Selenium{
		{Name: "vdi-1", Type: "chrome", Host: "h1", Port: 4444, ProxyURL: "http://global:1"},
	}}
	merged := cfg.CombineConfigs(base, cfg.SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": "http://127.0.0.1:8082"}},
	}})
	if merged.Selenium[0].ProxyURL != "http://127.0.0.1:8082" {
		t.Fatalf("merged proxy = %q", merged.Selenium[0].ProxyURL)
	}
	custom := customSeleniumProxyURL(merged, "vdi-1")
	if custom != "http://127.0.0.1:8082" {
		t.Fatalf("lookup = %q", custom)
	}
	// Leased instance still carries the global default; the session
	// composition prefers the merged per-source override.
	if got := effectiveSessionProxy("", false, custom, "http://global:1"); got != "http://127.0.0.1:8082" {
		t.Errorf("effective = %q", got)
	}
	// And the top-level source key still wins over custom.vdi.
	if got := effectiveSessionProxy("http://top:9", true, custom, "http://global:1"); got != "http://top:9" {
		t.Errorf("effective with top-level = %q", got)
	}
}

func TestPoolProxyIsolation(t *testing.T) {
	pool := NewPool(2)
	for _, tc := range []struct {
		name, proxy string
		port        int
	}{
		{"vdi-a", "http://proxy-a:1111", 4444},
		{"vdi-b", "http://proxy-b:2222", 4445},
	} {
		if err := pool.Add(SeleniumInstance{Config: cfg.Selenium{
			Name: tc.name, Host: "localhost", Port: tc.port, Type: "chrome", ProxyURL: tc.proxy,
		}}); err != nil {
			t.Fatalf("add %s: %v", tc.name, err)
		}
	}

	// Two sources with different top-level proxies resolve independently.
	sourceA := `{"proxy_url": "http://source-a:9001"}`
	sourceB := `{"proxy_url": "http://source-b:9002"}`
	seen := map[string]int{}
	for slot, src := range []string{sourceA, sourceB} {
		lease, instance, err := pool.AcquireContext(context.Background(), "")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		var sourceConfig map[string]interface{}
		if err := json.Unmarshal([]byte(src), &sourceConfig); err != nil {
			t.Fatal(err)
		}
		val, _ := sourceConfig["proxy_url"].(string)
		effective := effectiveSessionProxy(val, true, "", instance.Config.ProxyURL)
		args := buildLaunchArgs("chrome", "chrome", "ua", proxyTestCrawler(), effective)
		flag, ok := hasFlag(args, "--proxy-server=")
		if !ok {
			t.Fatalf("slot %d: no proxy flag in %v", slot, args)
		}
		seen[flag]++
		if err := lease.Release(); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	if len(seen) != 2 || seen["--proxy-server=http://source-a:9001"] != 1 || seen["--proxy-server=http://source-b:9002"] != 1 {
		t.Errorf("proxy isolation broken, flags seen: %v", seen)
	}
	if got := pool.Available(); got != 2 {
		t.Errorf("available = %d, want 2", got)
	}

	// Re-acquiring the same slot for a third source must resolve afresh
	// (never serve the previous source's proxy from slot state).
	lease, instance, err := pool.AcquireContext(context.Background(), "")
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Errorf("release: %v", err)
		}
	}()
	effective := effectiveSessionProxy("http://source-c:9003", true, "", instance.Config.ProxyURL)
	args := buildLaunchArgs("chrome", "chrome", "ua", proxyTestCrawler(), effective)
	if flag, ok := hasFlag(args, "--proxy-server="); !ok || flag != "--proxy-server=http://source-c:9003" {
		t.Errorf("re-acquired slot flag = %q, %t (stale proxy leakage)", flag, ok)
	}
}

func TestPoolReloadPicksNewProxy(t *testing.T) {
	// SIGHUP-analog at pool level: a rebuilt pool carries the reloaded VDI
	// defaults, so future sessions resolve them; nothing is cached.
	oldPool := NewPool(1)
	if err := oldPool.Add(SeleniumInstance{Config: cfg.Selenium{
		Name: "vdi-1", Host: "localhost", Port: 4444, Type: "chrome", ProxyURL: "http://old:1",
	}}); err != nil {
		t.Fatal(err)
	}
	newPool := NewPool(1)
	if err := newPool.Add(SeleniumInstance{Config: cfg.Selenium{
		Name: "vdi-1", Host: "localhost", Port: 4444, Type: "chrome", ProxyURL: "http://new:2",
	}}); err != nil {
		t.Fatal(err)
	}
	_, oldInstance, err := oldPool.AcquireContext(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, newInstance, err := newPool.AcquireContext(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := effectiveSessionProxy("", false, "", oldInstance.Config.ProxyURL); got != "http://old:1" {
		t.Errorf("old pool resolves %q", got)
	}
	if got := effectiveSessionProxy("", false, "", newInstance.Config.ProxyURL); got != "http://new:2" {
		t.Errorf("reloaded pool resolves %q", got)
	}
}

func TestProxyResolutionIsStateless(t *testing.T) {
	// SIGHUP-analog: the same source JSON resolved against a reloaded VDI
	// default picks up the new value; the old snapshot is untouched because
	// resolution is a pure function of its inputs (no globals).
	const sourceProxy = "http://source:9000"
	if got := effectiveSessionProxy(sourceProxy, true, "", "http://old-vdi:1"); got != sourceProxy {
		t.Errorf("before reload = %q", got)
	}
	if got := effectiveSessionProxy("", false, "", "http://new-vdi:2"); got != "http://new-vdi:2" {
		t.Errorf("after reload (inherited) = %q", got)
	}
	// Direct sentinel survives reload on both layers.
	if got := effectiveSessionProxy("no_proxy", true, "http://custom:3", "http://new-vdi:2"); got != "" {
		t.Errorf("no_proxy after reload = %q, want direct", got)
	}
}
