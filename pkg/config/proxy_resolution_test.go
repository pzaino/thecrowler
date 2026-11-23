package config

import (
	"encoding/json"
	"testing"
)

func TestResolveEffectiveProxyNonStickyPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		in      EffectiveProxyInput
		wantURL string
		wantSrc ProxySource
		wantErr bool
	}{
		{
			name:    "no override inherits VDI",
			in:      EffectiveProxyInput{VDIProxy: "http://vdi:8080"},
			wantURL: "http://vdi:8080",
			wantSrc: ProxySourceVDI,
		},
		{
			name: "canonical wins",
			in: EffectiveProxyInput{
				SourceProxy:        "http://canonical:3128",
				LegacyProxy:        "http://legacy:8888",
				LegacyProxyPresent: true,
				CustomProxy:        "http://custom:9999",
				CustomProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "http://canonical:3128",
			wantSrc: ProxySourceCanonical,
		},
		{
			name: "legacy wins over custom",
			in: EffectiveProxyInput{
				LegacyProxy:        "http://legacy:8888",
				LegacyProxyPresent: true,
				CustomProxy:        "http://custom:9999",
				CustomProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "http://legacy:8888",
			wantSrc: ProxySourceLegacy,
		},
		{
			name: "custom wins over VDI",
			in: EffectiveProxyInput{
				CustomProxy:        "http://custom:9999",
				CustomProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "http://custom:9999",
			wantSrc: ProxySourceCustomVDI,
		},
		{
			name: "blank canonical inherits legacy",
			in: EffectiveProxyInput{
				SourceProxy:        "   ",
				LegacyProxy:        "http://legacy:8888",
				LegacyProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "http://legacy:8888",
			wantSrc: ProxySourceLegacy,
		},
		{
			name: "legacy no_proxy selects direct",
			in: EffectiveProxyInput{
				LegacyProxy:        "no_proxy",
				LegacyProxyPresent: true,
				CustomProxy:        "http://custom:9999",
				CustomProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "",
			wantSrc: ProxySourceDirect,
		},
		{
			name: "custom no_proxy selects direct",
			in: EffectiveProxyInput{
				CustomProxy:        "no_proxy",
				CustomProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "",
			wantSrc: ProxySourceDirect,
		},
		{
			name: "uppercase sentinel is a value",
			in: EffectiveProxyInput{
				LegacyProxy:        "NO_PROXY",
				LegacyProxyPresent: true,
				VDIProxy:           "http://vdi:8080",
			},
			wantURL: "NO_PROXY",
			wantSrc: ProxySourceLegacy,
		},
		{
			name: "canonical normalizes case and default spelling",
			in: EffectiveProxyInput{
				SourceProxy: "HTTP://Proxy.Example:3128/",
			},
			wantURL: "http://proxy.example:3128",
			wantSrc: ProxySourceCanonical,
		},
		{
			name:    "canonical socks5 accepted",
			in:      EffectiveProxyInput{SourceProxy: "socks5://proxy.example:1080"},
			wantURL: "socks5://proxy.example:1080",
			wantSrc: ProxySourceCanonical,
		},
		{
			name:    "canonical malformed fails",
			in:      EffectiveProxyInput{SourceProxy: "http://bad host:1/x y"},
			wantErr: true,
		},
		{
			name:    "canonical requires port",
			in:      EffectiveProxyInput{SourceProxy: "http://proxy.example"},
			wantErr: true,
		},
		{
			name:    "canonical rejects path",
			in:      EffectiveProxyInput{SourceProxy: "http://proxy.example:3128/path"},
			wantErr: true,
		},
		{
			name:    "canonical rejects credentials",
			in:      EffectiveProxyInput{SourceProxy: "http://user:pass@proxy.example:3128"},
			wantErr: true,
		},
		{
			name:    "canonical rejects no_proxy sentinel",
			in:      EffectiveProxyInput{SourceProxy: "no_proxy"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEffectiveProxy(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.URL != tc.wantURL || got.Source != tc.wantSrc {
				t.Fatalf("resolution = %+v, want URL %q source %q", got, tc.wantURL, tc.wantSrc)
			}
		})
	}
}

func TestResolveEffectiveProxySticky(t *testing.T) {
	const sticky = "http://microproxy-vdi1:8081"
	base := EffectiveProxyInput{
		VDIProxy:                "http://microproxy-vdi1:8080",
		Sticky:                  true,
		StickyProxy:             sticky,
		StickyProfileConfigured: true,
	}

	t.Run("uses sticky listener instead of rotating default", func(t *testing.T) {
		got, err := ResolveEffectiveProxy(base)
		if err != nil {
			t.Fatal(err)
		}
		if got.URL != sticky || got.Source != ProxySourceSticky {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("matching canonical override accepted", func(t *testing.T) {
		in := base
		in.SourceProxy = "HTTP://MICROPROXY-VDI1:8081/"
		got, err := ResolveEffectiveProxy(in)
		if err != nil || got.URL != sticky {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("matching legacy override accepted", func(t *testing.T) {
		in := base
		in.LegacyProxy = sticky
		in.LegacyProxyPresent = true
		got, err := ResolveEffectiveProxy(in)
		if err != nil || got.URL != sticky {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("matching custom override accepted", func(t *testing.T) {
		in := base
		in.CustomProxy = sticky
		in.CustomProxyPresent = true
		got, err := ResolveEffectiveProxy(in)
		if err != nil || got.URL != sticky {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	for _, tc := range []struct {
		name string
		mut  func(*EffectiveProxyInput)
	}{
		{"rotating canonical rejected", func(in *EffectiveProxyInput) { in.SourceProxy = "http://microproxy-vdi1:8080" }},
		{"unrelated canonical rejected", func(in *EffectiveProxyInput) { in.SourceProxy = "http://regular-proxy:3128" }},
		{"different port rejected", func(in *EffectiveProxyInput) { in.SourceProxy = "http://microproxy-vdi1:8082" }},
		{"no_proxy rejected", func(in *EffectiveProxyInput) { in.LegacyProxy, in.LegacyProxyPresent = "no_proxy", true }},
		{"legacy rotating rejected", func(in *EffectiveProxyInput) {
			in.LegacyProxy, in.LegacyProxyPresent = "http://microproxy-vdi1:8080", true
		}},
		{"custom no_proxy rejected", func(in *EffectiveProxyInput) { in.CustomProxy, in.CustomProxyPresent = "no_proxy", true }},
		{"malformed canonical rejected", func(in *EffectiveProxyInput) { in.SourceProxy = "http://bad host:1" }},
		{"missing profile rejected", func(in *EffectiveProxyInput) { in.StickyProfileConfigured = false }},
		{"blank sticky listener rejected", func(in *EffectiveProxyInput) { in.StickyProxy = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			if _, err := ResolveEffectiveProxy(in); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestLegacyTopLevelProxy(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		present bool
	}{
		{"string", `{"proxy_url":"http://legacy:8888"}`, "http://legacy:8888", true},
		{"blank", `{"proxy_url":"  "}`, "  ", true},
		{"absent", `{"crawling_config":{}}`, "", false},
		{"null", `{"proxy_url":null}`, "", false},
		{"number", `{"proxy_url":1}`, "", false},
		{"broken document", `{"proxy_url":`, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, present := LegacyTopLevelProxy(json.RawMessage(tc.raw))
			if got != tc.want || present != tc.present {
				t.Fatalf("got (%q,%t), want (%q,%t)", got, present, tc.want, tc.present)
			}
		})
	}
}

func TestValidateBrowserProxyCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		browser string
		proxy   string
		wantErr bool
	}{
		{"firefox HTTP", "firefox", "http://proxy:8080", false},
		{"firefox SOCKS5", "firefox", "socks5://proxy:1080", false},
		{"firefox direct", "firefox", "", false},
		{"firefox no_proxy", "firefox", "no_proxy", false},
		{"firefox HTTPS", "firefox", "https://proxy:8443", true},
		{"firefox malformed left to fallback", "firefox", "http://bad host:1", false},
		{"chrome HTTPS", "chrome", "https://proxy:8443", false},
		{"chromium HTTPS", "chromium", "https://proxy:8443", false},
		{"empty browser", "", "https://proxy:8443", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBrowserProxyCompatibility(tc.browser, tc.proxy)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tc.wantErr)
			}
		})
	}
}

func TestStrictStickyOverrideParsing(t *testing.T) {
	if _, present, err := LegacyTopLevelProxyStrict(json.RawMessage(`{"proxy_url":1}`)); err == nil || !present {
		t.Fatalf("non-string legacy proxy: present=%t err=%v, want present=true and an error", present, err)
	}
	if value, present, err := LegacyTopLevelProxyStrict(json.RawMessage(`{"proxy_url":"http://p:1"}`)); err != nil || !present || value != "http://p:1" {
		t.Fatalf("string legacy proxy = (%q,%t,%v)", value, present, err)
	}
	if _, present, err := LegacyTopLevelProxyStrict(json.RawMessage(`{"proxy_url":null}`)); err != nil || present {
		t.Fatalf("null legacy proxy: present=%t err=%v", present, err)
	}

	order := []Selenium{{Name: "vdi-1"}}
	if _, present, err := SourceCustomVDIProxyStrict(SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": 1}},
	}}, order, "vdi-1"); err == nil || !present {
		t.Fatalf("non-string custom proxy: present=%t err=%v", present, err)
	}
	if _, _, err := SourceCustomVDIProxyStrict(SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{"not-an-object"},
	}}, order, "vdi-1"); err == nil {
		t.Fatal("non-object custom.vdi entry must be an error for a sticky source")
	}
}

func TestSourceCustomVDIProxyUsesPositionalProvenance(t *testing.T) {
	order := []Selenium{
		{Name: "vdi-1"},
		{Name: "vdi-2"},
	}
	source := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{
			map[string]interface{}{},
			map[string]interface{}{"proxy_url": "http://custom-2:2222"},
			map[string]interface{}{"proxy_url": "http://excess:3333"},
		},
	}}
	if got, ok := SourceCustomVDIProxy(source, order, "vdi-2"); !ok || got != "http://custom-2:2222" {
		t.Fatalf("vdi-2 = (%q,%t)", got, ok)
	}
	if got, ok := SourceCustomVDIProxy(source, order, "vdi-1"); ok || got != "" {
		t.Fatalf("vdi-1 = (%q,%t), want no override", got, ok)
	}
	if _, ok := SourceCustomVDIProxy(source, order, "missing"); ok {
		t.Fatal("missing VDI must not receive another VDI's override")
	}
	if _, ok := SourceCustomVDIProxy(SourceConfig{Custom: map[string]interface{}{"vdi": "bad"}}, order, "vdi-2"); ok {
		t.Fatal("malformed custom.vdi must not produce an override")
	}
	if _, ok := SourceCustomVDIProxy(SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{"proxy_url": 8080}},
	}}, order, "vdi-1"); ok {
		t.Fatal("non-string proxy must not produce an override")
	}
}

func TestVDIByNameUnique(t *testing.T) {
	order := []Selenium{{Name: "vdi-1"}, {Name: "vdi-2"}}
	if !VDIByNameUnique(order, " VDI-2 ") {
		t.Fatal("case/space-insensitive unique name not recognized")
	}
	if VDIByNameUnique(order, "missing") {
		t.Fatal("missing name reported unique")
	}
	if VDIByNameUnique([]Selenium{{Name: "vdi-1"}, {Name: "vdi-1"}}, "vdi-1") {
		t.Fatal("duplicate name reported unique")
	}
	if VDIByNameUnique(order, "") {
		t.Fatal("empty name reported unique")
	}
}

func TestNormalizeProxyListenerRejectsUnsafeForms(t *testing.T) {
	for _, raw := range []string{
		"http://",
		"http://user@proxy:1",
		"http://proxy:1?x=1",
		"http://proxy:1#frag",
		"http://proxy:1/a",
		"ftp://proxy:21",
		"http://proxy:0",
		"http://proxy:99999",
		"http://[::1",
	} {
		if _, err := NormalizeSourceProxyURL(raw); err == nil {
			t.Errorf("NormalizeSourceProxyURL(%q) accepted an unsafe listener", raw)
		}
	}
	got, err := NormalizeSourceProxyURL("HTTPS://[::1]:8443/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://[::1]:8443" {
		t.Fatalf("IPv6 normalization = %q", got)
	}
}
