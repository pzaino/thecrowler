package config

import (
	"encoding/json"
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

func TestCombineVDICfgMergesCompleteStickyProfilePerIndex(t *testing.T) {
	dst := mergeTestBase()
	dst.Selenium[0].StickySession = StickySessionConfig{
		ProxyURL:  "http://trusted:8081",
		APIURL:    "http://trusted:8094",
		VDIAPIURL: "http://127.0.0.1:8094",
		TokenEnv:  "TRUSTED_TOKEN",
		Provider:  "trusted-provider",
	}
	dst.Selenium[0].ApprovedStickyProfiles = []StickySessionConfig{{
		ProxyURL:  "http://approved:8081",
		APIURL:    "http://approved:8094",
		VDIAPIURL: "http://127.0.0.1:8095",
		TokenEnv:  "APPROVED_TOKEN",
		Provider:  "approved-provider",
	}}
	baseProfile := dst.Selenium[0].StickySession
	baseApproved := append([]StickySessionConfig(nil), dst.Selenium[0].ApprovedStickyProfiles...)
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{
			map[string]interface{}{
				"proxy_url": "http://approved:8081",
				"sticky_session": map[string]interface{}{
					"proxy_url":   "http://approved:8081",
					"api_url":     "http://approved:8094",
					"vdi_api_url": "http://127.0.0.1:8095",
					"token_env":   "APPROVED_TOKEN",
					"provider":    "approved-provider",
				},
			},
		},
	}}
	got := CombineConfigs(dst, src)
	if got.Selenium[0].StickySession.Provider != "approved-provider" {
		t.Fatalf("source sticky profile was not merged: %+v", got.Selenium[0].StickySession)
	}
	if got.Selenium[0].ProxyURL != "http://approved:8081" {
		t.Fatalf("source custom proxy = %q", got.Selenium[0].ProxyURL)
	}
	if dst.Selenium[0].StickySession != baseProfile {
		t.Fatalf("base sticky profile mutated: %+v", dst.Selenium[0].StickySession)
	}
	if len(dst.Selenium[0].ApprovedStickyProfiles) != len(baseApproved) ||
		dst.Selenium[0].ApprovedStickyProfiles[0] != baseApproved[0] {
		t.Fatalf("base approved profiles mutated: %+v", dst.Selenium[0].ApprovedStickyProfiles)
	}
	if len(got.Selenium[0].ApprovedStickyProfiles) != len(baseApproved) ||
		got.Selenium[0].ApprovedStickyProfiles[0] != baseApproved[0] {
		t.Fatalf("source altered the operator allowlist: %+v", got.Selenium[0].ApprovedStickyProfiles)
	}
}

func TestCombineVDICfgMergesCompleteStickyProfileWithoutGlobalDefault(t *testing.T) {
	dst := mergeTestBase()
	profile := StickySessionConfig{
		ProxyURL:  "http://approved:8091",
		APIURL:    "http://approved:8095",
		VDIAPIURL: "http://127.0.0.1:8095",
		TokenEnv:  "APPROVED_TOKEN",
		Provider:  "approved-provider",
	}
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{
			"sticky_session": map[string]interface{}{
				"proxy_url":   profile.ProxyURL,
				"api_url":     profile.APIURL,
				"vdi_api_url": profile.VDIAPIURL,
				"token_env":   profile.TokenEnv,
				"provider":    profile.Provider,
			},
		}},
	}}
	got := CombineConfigs(dst, src)
	if got.Selenium[0].StickySession != profile {
		t.Fatalf("source profile not merged into a VDI without a default: %+v", got.Selenium[0].StickySession)
	}
	if dst.Selenium[0].StickySession != (StickySessionConfig{}) {
		t.Fatalf("base VDI mutated: %+v", dst.Selenium[0].StickySession)
	}
}

func TestCombineConfigRejectsMalformedSourceStickyProfile(t *testing.T) {
	dst := mergeTestBase()
	for _, profile := range []interface{}{
		map[string]interface{}{
			"proxy_url": "http://approved:8081",
			"api_url":   "http://approved:8094",
			"token_env": "APPROVED_TOKEN",
		},
		map[string]interface{}{
			"proxy_url":   "http://approved:8081",
			"api_url":     "http://approved:8094",
			"token_env":   "APPROVED_TOKEN",
			"provider":    "approved-provider",
			"unknown_key": "value",
		},
		"not-an-object",
	} {
		raw, err := json.Marshal(SourceConfig{Custom: map[string]interface{}{
			"vdi": []interface{}{map[string]interface{}{"sticky_session": profile}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CombineConfig(dst, raw); err == nil {
			t.Fatalf("malformed source sticky profile was accepted: %#v", profile)
		}
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

func TestCombineVDICfgMergesLegacyIdentityFieldsForRuntimeValidation(t *testing.T) {
	base := mergeTestBase()
	leased := base.Selenium[0]
	src := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{
			"host":    "attacker-selenium",
			"port":    float64(5555),
			"type":    "firefox",
			"sslmode": "enable",
		}},
	}}
	merged := CombineConfigs(base, src)
	if merged.Selenium[0].Host != "attacker-selenium" ||
		merged.Selenium[0].Port != 5555 ||
		merged.Selenium[0].Type != "firefox" ||
		merged.Selenium[0].SSLMode != "enable" {
		t.Fatalf("legacy identity fields were not merged for validation: %+v", merged.Selenium[0])
	}
	if base.Selenium[0].Host != leased.Host || base.Selenium[0].Port != leased.Port ||
		base.Selenium[0].Type != leased.Type || base.Selenium[0].SSLMode != leased.SSLMode {
		t.Fatalf("source merge mutated the caller's VDI configuration: %+v", base.Selenium[0])
	}
	if _, err := ResolveEffectiveVDI(EffectiveVDIInput{
		Leased: leased, LeasedIndex: 0, Merged: merged.Selenium, Source: SourceConfig{},
	}); err == nil {
		t.Fatal("runtime accepted a merged source-controlled VDI identity")
	}
}
