package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func testStickyProfile(provider string) StickySessionConfig {
	return StickySessionConfig{
		ProxyURL:  "http://127.0.0.1:8081",
		APIURL:    "http://microproxy-vdi1:8094",
		VDIAPIURL: "http://127.0.0.1:8094",
		TokenEnv:  "MICROPROXY_VDI1_SESSION_API_KEY",
		Provider:  provider,
	}
}

func testVDI(name, proxy string, profile StickySessionConfig, approved ...StickySessionConfig) Selenium {
	return Selenium{
		Name:                   name,
		Type:                   "chrome",
		Host:                   "selenium-" + name,
		Port:                   4444,
		ProxyURL:               proxy,
		StickySession:          profile,
		ApprovedStickyProfiles: approved,
	}
}

func testSourceWithProfile(profile StickySessionConfig) SourceConfig {
	custom := map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{
			"proxy_url": profile.ProxyURL,
			"sticky_session": map[string]interface{}{
				"proxy_url":   profile.ProxyURL,
				"api_url":     profile.APIURL,
				"vdi_api_url": profile.VDIAPIURL,
				"token_env":   profile.TokenEnv,
				"provider":    profile.Provider,
			},
		}},
	}
	return SourceConfig{Custom: custom}
}

func resolveTestSelection(t *testing.T, leased Selenium, merged []Selenium, source SourceConfig) (EffectiveVDISelection, error) {
	t.Helper()
	return ResolveEffectiveVDI(EffectiveVDIInput{
		Leased:      leased,
		LeasedIndex: 0,
		Merged:      merged,
		Source:      source,
	})
}

func TestEffectiveVDISelectionTable(t *testing.T) {
	global := testStickyProfile("global-provider")
	replacement := testStickyProfile("replacement-provider")
	replacement.ProxyURL = "http://127.0.0.1:8091"
	replacement.APIURL = "http://microproxy-vdi1:8094"
	replacement.TokenEnv = "REPLACEMENT_TOKEN"

	conventional := func(name string) Selenium {
		return testVDI(name, "http://corporate-proxy:3128", StickySessionConfig{})
	}
	direct := func(name string) Selenium {
		return testVDI(name, "", StickySessionConfig{})
	}

	t.Run("A direct", func(t *testing.T) {
		leased := direct("vdi-1")
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, SourceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "" || selection.Proxy.Source != ProxySourceDirect {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("B conventional proxy", func(t *testing.T) {
		leased := conventional("vdi-1")
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, SourceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://corporate-proxy:3128" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("C rotating listener", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, SourceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://127.0.0.1:8080" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	for _, tc := range []struct {
		name   string
		leased Selenium
	}{
		{"D direct plus custom profile", direct("vdi-1")},
		{"E conventional plus custom profile", conventional("vdi-1")},
		{"F rotating plus custom profile", testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leased := tc.leased
			leased.ApprovedStickyProfiles = []StickySessionConfig{replacement}
			selection, err := resolveTestSelection(t, leased, []Selenium{leased}, testSourceWithProfile(replacement))
			if err != nil {
				t.Fatal(err)
			}
			if !selection.Sticky || selection.StickyProfile.Provider != "replacement-provider" ||
				selection.Proxy.URL != replacement.ProxyURL {
				t.Fatalf("selection = %+v", selection)
			}
		})
	}
	t.Run("G global sticky listener is the default", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global)
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, SourceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.StickyOrigin != StickySelectionGlobal ||
			selection.StickyProfile.Provider != global.Provider {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("H standby profile does not activate for ordinary source", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", global)
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, SourceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://127.0.0.1:8080" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("I explicit true opts into standby profile", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", global)
		enabled := true
		source := SourceConfig{CrawlingConfig: CrawlingConfig{StickySession: &enabled}}
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.StickyOrigin != StickySelectionExplicit ||
			selection.Proxy.URL != global.ProxyURL {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("J source conventional proxy replaces sticky default", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global)
		source := SourceConfig{CrawlingConfig: CrawlingConfig{Proxy: "http://source-proxy:3128"}}
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://source-proxy:3128" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("J custom.vdi conventional proxy replaces sticky default", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global)
		source := SourceConfig{Custom: map[string]interface{}{
			"vdi": []interface{}{map[string]interface{}{
				"proxy_url": "http://custom-proxy:3129",
			}},
		}}
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://custom-proxy:3129" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("K explicit false cannot browse known sticky listener", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global)
		disabled := false
		source := SourceConfig{CrawlingConfig: CrawlingConfig{StickySession: &disabled}}
		if _, err := resolveTestSelection(t, leased, []Selenium{leased}, source); err == nil {
			t.Fatal("explicit false accepted the inherited sticky listener")
		}
	})
	t.Run("K explicit false with normal proxy", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global)
		disabled := false
		source := SourceConfig{CrawlingConfig: CrawlingConfig{
			StickySession: &disabled,
			Proxy:         "http://source-proxy:3128",
		}}
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Sticky || selection.Proxy.URL != "http://source-proxy:3128" {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("L source replacement profile wins", func(t *testing.T) {
		leased := testVDI("vdi-1", global.ProxyURL, global, replacement)
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, testSourceWithProfile(replacement))
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.StickyOrigin != StickySelectionProfile ||
			selection.StickyProfile.Provider != replacement.Provider {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("M explicit matching standby listener rotates", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", global)
		source := SourceConfig{CrawlingConfig: CrawlingConfig{Proxy: global.ProxyURL}}
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.StickyOrigin != StickySelectionListener {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("N conflicting explicit proxy fails", func(t *testing.T) {
		leased := testVDI("vdi-1", "", StickySessionConfig{})
		leased.ApprovedStickyProfiles = []StickySessionConfig{replacement}
		source := testSourceWithProfile(replacement)
		source.CrawlingConfig.Proxy = "http://other-proxy:3128"
		if _, err := resolveTestSelection(t, leased, []Selenium{leased}, source); err == nil {
			t.Fatal("conflicting source proxy was accepted")
		}
	})
	t.Run("O malformed custom profile fails", func(t *testing.T) {
		leased := testVDI("vdi-1", "", StickySessionConfig{})
		source := SourceConfig{Custom: map[string]interface{}{
			"vdi": []interface{}{map[string]interface{}{
				"sticky_session": map[string]interface{}{"proxy_url": "http://x:1"},
			}},
		}}
		if _, err := resolveTestSelection(t, leased, []Selenium{leased}, source); err == nil {
			t.Fatal("incomplete custom profile was accepted")
		}
	})
	t.Run("P unapproved custom profile fails", func(t *testing.T) {
		leased := testVDI("vdi-1", "", StickySessionConfig{})
		leased.ApprovedStickyProfiles = []StickySessionConfig{replacement}
		other := replacement
		other.Provider = "attacker-provider"
		if _, err := resolveTestSelection(t, leased, []Selenium{leased}, testSourceWithProfile(other)); err == nil {
			t.Fatal("unapproved custom profile was accepted")
		}
	})
	t.Run("Q multi-VDI override stays positional", func(t *testing.T) {
		first := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})
		second := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{}, replacement)
		source := SourceConfig{Custom: map[string]interface{}{
			"vdi": []interface{}{
				map[string]interface{}{},
				map[string]interface{}{
					"sticky_session": map[string]interface{}{
						"proxy_url":   replacement.ProxyURL,
						"api_url":     replacement.APIURL,
						"vdi_api_url": replacement.VDIAPIURL,
						"token_env":   replacement.TokenEnv,
						"provider":    replacement.Provider,
					},
				},
			},
		}}
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: second, LeasedIndex: 1, Merged: []Selenium{first, second}, Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.StickyProfile.Provider != replacement.Provider {
			t.Fatalf("second VDI selection = %+v", selection)
		}
		firstSelection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: first, LeasedIndex: 0, Merged: []Selenium{first, second}, Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		if firstSelection.Sticky {
			t.Fatalf("first VDI unexpectedly rotated: %+v", firstSelection)
		}
	})
	t.Run("Q management keeps the leased Selenium identity", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{}, replacement)
		merged := leased
		merged.Host = "source-controlled-selenium"
		merged.Port = 5555
		merged.Type = "firefox"
		source := testSourceWithProfile(replacement)
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 0, Merged: []Selenium{merged}, Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Host != "source-controlled-selenium" ||
			selection.Browser.Port != 5555 || selection.Browser.Type != "firefox" {
			t.Fatalf("effective browser did not honor source VDI overrides: %+v", selection.Browser)
		}
		if selection.Management.Host != leased.Host || selection.Management.Port != leased.Port ||
			selection.Management.Type != leased.Type {
			t.Fatalf("management browser was retargeted: %+v", selection.Management)
		}
	})
	t.Run("R same-VDI configurations stay isolated", func(t *testing.T) {
		first := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{}, replacement)
		second := testVDI("vdi-2", "http://127.0.0.1:8080", StickySessionConfig{}, replacement)
		globalCopy := append([]Selenium(nil), first, second)
		sourceA := testSourceWithProfile(replacement)
		sourceB := SourceConfig{CrawlingConfig: CrawlingConfig{Proxy: "http://other-proxy:3128"}}
		if _, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: first, LeasedIndex: 0, Merged: []Selenium{first, second}, Source: sourceA,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: second, LeasedIndex: 1, Merged: []Selenium{first, second}, Source: sourceB,
		}); err != nil {
			t.Fatal(err)
		}
		if globalCopy[0].ApprovedStickyProfiles[0].Provider != replacement.Provider ||
			globalCopy[1].ApprovedStickyProfiles[0].Provider != replacement.Provider {
			t.Fatal("selection mutated the global VDI configuration")
		}
	})
	t.Run("V Firefox HTTPS listener rejected before rotation", func(t *testing.T) {
		leased := direct("vdi-1")
		leased.Type = "firefox"
		httpsProfile := replacement
		httpsProfile.ProxyURL = "https://127.0.0.1:8091"
		leased.ApprovedStickyProfiles = []StickySessionConfig{httpsProfile}
		if _, err := resolveTestSelection(t, leased, []Selenium{leased}, testSourceWithProfile(httpsProfile)); err == nil {
			t.Fatal("Firefox HTTPS sticky listener was accepted")
		}
	})
}

func TestSourceStickyAuthorization(t *testing.T) {
	profile := testStickyProfile("approved-provider")
	vdi := testVDI("vdi-1", "", StickySessionConfig{}, profile)
	global := Config{Selenium: []Selenium{vdi}}

	if err := global.ValidateSourceStickyAuthorization(testSourceWithProfile(profile)); err != nil {
		t.Fatalf("approved source profile rejected: %v", err)
	}
	unapproved := profile
	unapproved.TokenEnv = "ATTACKER_TOKEN"
	if err := global.ValidateSourceStickyAuthorization(testSourceWithProfile(unapproved)); err == nil {
		t.Fatal("unapproved token_env was accepted")
	}
	tampered := profile
	tampered.APIURL = "http://attacker:8094"
	if err := global.ValidateSourceStickyAuthorization(testSourceWithProfile(tampered)); err == nil {
		t.Fatal("unapproved management origin was accepted")
	}
	if err := global.ValidateSourceStickyAuthorization(SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{
			"sticky_session": map[string]interface{}{
				"proxy_url": "http://attacker:8081",
				"api_url":   "http://attacker:8094",
				"token_env": "ATTACKER_TOKEN",
				"provider":  "attacker",
			},
		}},
	}}); err == nil {
		t.Fatal("arbitrary management origin was approved")
	}
	if err := global.ValidateSourceStickyAuthorization(SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{}, map[string]interface{}{
			"sticky_session": map[string]interface{}{
				"proxy_url": "http://127.0.0.1:8091",
				"api_url":   "http://microproxy-vdi1:8095",
				"token_env": "MICROPROXY_VDI1_ALT_SESSION_API_KEY",
				"provider":  "approved-provider",
			},
		}},
	}}); err == nil {
		t.Fatal("profile for an unmapped VDI position was approved")
	}

	disabled := false
	custom := testSourceWithProfile(profile)
	custom.CrawlingConfig.StickySession = &disabled
	if err := ValidateSourceProtocolOptions(custom); err == nil {
		t.Fatal("sticky_session:false plus custom profile was accepted")
	}
}

func TestSourceCustomVDIStrictDecoding(t *testing.T) {
	for _, raw := range []string{
		`{"custom":{"vdi":"not-a-list"}}`,
		`{"custom":{"vdi":["not-an-object"]}}`,
		`{"custom":{"vdi":[{"sticky_session":"not-an-object"}]}}`,
		`{"custom":{"vdi":[{"sticky_session":{"proxy_url":"http://x:1"}}]}}`,
		`{"custom":{"vdi":[{"sticky_session":{"proxy_url":"http://x:1","api_url":"http://x:2","token_env":"K","provider":"p","token":"secret"}}]}}`,
		`{"custom":{"vdi":[{"proxy_url":8080}]}}`,
	} {
		if _, err := DecodeSourceSessionConfig(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed custom VDI was accepted: %s", raw)
		}
	}
	if _, err := DecodeSourceSessionConfig(json.RawMessage(`{"custom":{"other":"value"}}`)); err != nil {
		t.Fatalf("unrelated custom extension rejected: %v", err)
	}
}

func TestApprovedStickyProfilesRejectAmbiguity(t *testing.T) {
	profile := testStickyProfile("provider")
	duplicate := profile
	duplicate.Provider = "other"
	vdi := testVDI("vdi-1", profile.ProxyURL, profile, duplicate)
	if _, err := ApprovedStickyProfiles(vdi); err == nil {
		t.Fatal("duplicate sticky listener was accepted")
	}
	vdi.ApprovedStickyProfiles = []StickySessionConfig{profile}
	if _, err := ApprovedStickyProfiles(vdi); err == nil {
		t.Fatal("duplicate profile was accepted")
	}
}

func TestStickyProfileNormalization(t *testing.T) {
	profile := testStickyProfile("Provider")
	profile.TokenEnv = " KEY "
	profile.ProxyURL = "HTTP://MicroProxy:8081/"
	normalized, err := NormalizeStickySessionProfile(profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.TokenEnv != "KEY" || normalized.ProxyURL != "http://microproxy:8081" {
		t.Fatalf("normalized = %+v", normalized)
	}
	if !normalized.Equal(testStickyProfile("Provider")) && !strings.Contains(normalized.ProxyURL, "microproxy") {
		t.Fatalf("unexpected normalization: %+v", normalized)
	}
}
