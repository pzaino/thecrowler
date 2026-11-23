package config

import (
	"testing"
)

func mergeTestBase() Config {
	cfg := *NewConfig()
	cfg.Selenium = []Selenium{
		{Name: "vdi-1", Type: "chrome", Host: "h1", Port: 4444},
		{Name: "vdi-2", Type: "firefox", Host: "h2", Port: 4445},
	}
	return cfg
}

func TestCombineVDICfgMergesProxyPerIndex(t *testing.T) {
	dst := mergeTestBase()
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{
			map[string]interface{}{"proxy_url": "http://custom-1:1111"},
			map[string]interface{}{},
		},
	}}
	got := CombineConfigs(dst, src)
	if got.Selenium[0].ProxyURL != "http://custom-1:1111" {
		t.Errorf("[0].ProxyURL = %q", got.Selenium[0].ProxyURL)
	}
	if got.Selenium[1].ProxyURL != "" {
		t.Errorf("[1].ProxyURL = %q, want untouched", got.Selenium[1].ProxyURL)
	}
	// Unrelated fields survive the merge.
	if got.Selenium[0].Host != "h1" || got.Selenium[1].Type != "firefox" {
		t.Errorf("unrelated fields altered: %+v", got.Selenium)
	}
}

func TestCombineVDICfgDoesNotMutateSharedBaseSlice(t *testing.T) {
	base := mergeTestBase()
	base.Selenium[0].ProxyURL = "http://global-vdi:8080"
	source := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": "http://source:3128"}},
	}}
	first := CombineConfigs(base, source)
	second := CombineConfigs(base, SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": "http://other:3129"}},
	}})
	if base.Selenium[0].ProxyURL != "http://global-vdi:8080" {
		t.Fatalf("base VDI proxy mutated to %q", base.Selenium[0].ProxyURL)
	}
	if first.Selenium[0].ProxyURL != "http://source:3128" {
		t.Fatalf("first merged proxy = %q", first.Selenium[0].ProxyURL)
	}
	if second.Selenium[0].ProxyURL != "http://other:3129" {
		t.Fatalf("second merged proxy = %q", second.Selenium[0].ProxyURL)
	}
	if first.Selenium[0].ProxyURL == second.Selenium[0].ProxyURL {
		t.Fatal("merged source configs share VDI proxy state")
	}
}

func TestCombineVDICfgCannotMergeTrustedStickyProfile(t *testing.T) {
	dst := mergeTestBase()
	dst.Selenium[0].StickySession = StickySessionConfig{
		ProxyURL: "http://trusted:8081",
		APIURL:   "http://trusted:8094",
		TokenEnv: "TRUSTED_TOKEN",
		Provider: "trusted-provider",
	}
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{
			map[string]interface{}{
				"proxy_url": "http://untrusted:8080",
				"sticky_session": map[string]interface{}{
					"proxy_url": "http://untrusted:8081",
					"api_url":   "http://untrusted:8094",
					"token_env": "UNTRUSTED_TOKEN",
					"provider":  "untrusted-provider",
				},
			},
		},
	}}
	got := CombineConfigs(dst, src)
	if got.Selenium[0].StickySession != dst.Selenium[0].StickySession {
		t.Fatalf("source merged trusted sticky profile: %+v", got.Selenium[0].StickySession)
	}
	if got.Selenium[0].ProxyURL != "http://untrusted:8080" {
		t.Fatalf("source custom proxy = %q", got.Selenium[0].ProxyURL)
	}
}

func TestCombineVDICfgIgnoresExcessEntries(t *testing.T) {
	dst := mergeTestBase()
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{
			map[string]interface{}{"proxy_url": "http://custom-1:1111"},
			map[string]interface{}{"proxy_url": "http://custom-2:2222"},
			map[string]interface{}{"proxy_url": "http://custom-3:3333"},
		},
	}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("out-of-range source entries panicked: %v", r)
		}
	}()
	got := CombineConfigs(dst, src)
	if len(got.Selenium) != 2 {
		t.Fatalf("VDI list length = %d, want 2", len(got.Selenium))
	}
	if got.Selenium[0].ProxyURL != "http://custom-1:1111" || got.Selenium[1].ProxyURL != "http://custom-2:2222" {
		t.Errorf("merged proxies = %q, %q", got.Selenium[0].ProxyURL, got.Selenium[1].ProxyURL)
	}
}

func TestCombineVDICfgIgnoresMalformedEntries(t *testing.T) {
	dst := mergeTestBase()
	cases := []struct {
		name   string
		custom interface{}
	}{
		{"non-list", map[string]interface{}{"proxy_url": "http://x:1"}},
		{"string", "http://x:1"},
		{"nil", nil},
		{"non-object element", []interface{}{"http://x:1", map[string]interface{}{"proxy_url": "http://custom-2:2222"}}},
		{"non-string proxy", []interface{}{map[string]interface{}{"proxy_url": 8080}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked on %s: %v", tc.name, r)
				}
			}()
			got := CombineConfigs(dst, SourceConfig{Custom: map[string]interface{}{"vdi": tc.custom}})
			if len(got.Selenium) != 2 {
				t.Fatalf("VDI list length = %d", len(got.Selenium))
			}
		})
	}
	// The non-object element case must still merge the valid sibling.
	got := CombineConfigs(dst, SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{"http://x:1", map[string]interface{}{"proxy_url": "http://custom-2:2222"}},
	}})
	if got.Selenium[1].ProxyURL != "http://custom-2:2222" {
		t.Errorf("[1].ProxyURL = %q", got.Selenium[1].ProxyURL)
	}
}

func TestCombineVDICfgLegacySeleniumKeyIgnored(t *testing.T) {
	// The source override key is custom.vdi (consistent with the global vdi
	// section since release 2.0.0). The legacy custom.selenium key is not
	// honored: no supported configuration depends on it (it never drove
	// launch behavior), so it was removed rather than aliased.
	dst := mergeTestBase()
	got := CombineConfigs(dst, SourceConfig{Custom: map[string]interface{}{
		"selenium": []interface{}{map[string]interface{}{"proxy_url": "http://legacy:9999"}},
		"vdi":      []interface{}{map[string]interface{}{"proxy_url": "http://canonical:1111"}},
	}})
	if got.Selenium[0].ProxyURL != "http://canonical:1111" {
		t.Errorf("[0].ProxyURL = %q, want the canonical custom.vdi value", got.Selenium[0].ProxyURL)
	}
}
