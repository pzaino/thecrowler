package config

import (
	"encoding/json"
	"errors"
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
	t.Run("Q source cannot retarget the leased Selenium identity", func(t *testing.T) {
		leased := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{}, replacement)
		merged := leased
		merged.Host = "source-controlled-selenium"
		merged.Port = 5555
		merged.Type = "firefox"
		source := testSourceWithProfile(replacement)
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 0, Merged: []Selenium{merged}, Source: source,
		})
		if err == nil {
			t.Fatal("source-controlled Selenium host/port/type was accepted")
		}
		if !strings.Contains(err.Error(), "host") {
			t.Fatalf("error does not identify the conflicting identity field: %v", err)
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

func TestEffectiveVDIIdentityAndIndexBinding(t *testing.T) {
	leased := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})
	source := SourceConfig{}

	assertConflict := func(t *testing.T, merged Selenium, wantField string) {
		t.Helper()
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 0, Merged: []Selenium{merged}, Source: source,
		})
		if err == nil {
			t.Fatalf("identity mismatch in %s was accepted", wantField)
		}
		if !strings.Contains(err.Error(), wantField) {
			t.Fatalf("error %v does not identify field %q", err, wantField)
		}
		if !errors.Is(err, ErrVDIIdentityMismatch) {
			t.Fatalf("error %v does not classify as an identity mismatch", err)
		}
	}

	t.Run("A1 no override", func(t *testing.T) {
		selection, err := resolveTestSelection(t, leased, []Selenium{leased}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Host != leased.Host || selection.Browser.Port != leased.Port ||
			selection.Browser.Type != leased.Type || selection.Browser.Name != leased.Name {
			t.Fatalf("effective browser changed identity: %+v", selection.Browser)
		}
	})
	t.Run("A2 proxy-only override", func(t *testing.T) {
		merged := leased
		merged.ProxyURL = "http://source-proxy:3128"
		sourceWithProxy := SourceConfig{CrawlingConfig: CrawlingConfig{Proxy: "http://source-proxy:3128"}}
		selection, err := resolveTestSelection(t, leased, []Selenium{merged}, sourceWithProxy)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Proxy.URL != merged.ProxyURL || selection.Browser.Host != leased.Host ||
			selection.Browser.Port != leased.Port || selection.Browser.Type != leased.Type {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("A3 approved sticky profile", func(t *testing.T) {
		profile := testStickyProfile("approved")
		leasedWithProfile := leased
		leasedWithProfile.ApprovedStickyProfiles = []StickySessionConfig{profile}
		merged := leasedWithProfile
		merged.StickySession = profile
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leasedWithProfile, LeasedIndex: 0,
			Merged: []Selenium{merged}, Source: testSourceWithProfile(profile),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !selection.Sticky || selection.Browser.Host != leased.Host ||
			selection.Browser.Port != leased.Port || selection.Browser.Type != leased.Type {
			t.Fatalf("selection = %+v", selection)
		}
	})
	t.Run("A4 host change", func(t *testing.T) {
		merged := leased
		merged.Host = "attacker-selenium"
		assertConflict(t, merged, "host")
	})
	t.Run("A5 port change", func(t *testing.T) {
		merged := leased
		merged.Port = 5555
		assertConflict(t, merged, "port")
	})
	t.Run("A6 host and port change", func(t *testing.T) {
		merged := leased
		merged.Host = "attacker-selenium"
		merged.Port = 5555
		assertConflict(t, merged, "host")
	})
	t.Run("A7 scheme/sslmode change", func(t *testing.T) {
		merged := leased
		merged.SSLMode = "enable"
		assertConflict(t, merged, "sslmode")
	})
	t.Run("A7b sslmode case change is a launcher-scheme change", func(t *testing.T) {
		upper := leased
		upper.SSLMode = "ENABLE"
		lower := leased
		lower.SSLMode = "enable"
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: upper, LeasedIndex: 0, Merged: []Selenium{lower}, Source: source,
		})
		if err == nil || !errors.Is(err, ErrVDIIdentityMismatch) {
			t.Fatalf("case-variant sslmode error = %v, want ErrVDIIdentityMismatch", err)
		}
	})
	t.Run("A7c equivalent launcher scheme is accepted", func(t *testing.T) {
		merged := leased
		merged.SSLMode = "disable"
		selection, err := resolveTestSelection(t, leased, []Selenium{merged}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.SSLMode != leased.SSLMode {
			t.Fatalf("effective sslmode = %q, want lease %q", selection.Browser.SSLMode, leased.SSLMode)
		}
	})
	t.Run("A8 browser type change", func(t *testing.T) {
		merged := leased
		merged.Type = "firefox"
		assertConflict(t, merged, "type")
	})
	t.Run("A9 equivalent identity spellings", func(t *testing.T) {
		merged := leased
		merged.Name = " VDI-1 "
		merged.Host = " SELENIUM-VDI-1 "
		merged.Type = "CHROME"
		merged.SSLMode = "disable"
		selection, err := resolveTestSelection(t, leased, []Selenium{merged}, source)
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Name != leased.Name || selection.Browser.Host != leased.Host ||
			selection.Browser.Port != leased.Port || selection.Browser.Type != leased.Type ||
			selection.Browser.SSLMode != leased.SSLMode {
			t.Fatalf("effective browser was not canonicalized to the lease: %+v", selection.Browser)
		}
	})
	t.Run("A10 correct positional binding", func(t *testing.T) {
		other := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{})
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 0, Merged: []Selenium{leased, other}, Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Name != leased.Name {
			t.Fatalf("selection used %q", selection.Browser.Name)
		}
		if selection.LeasedIndex != 0 {
			t.Fatalf("selection index = %d, want 0", selection.LeasedIndex)
		}
	})
	t.Run("A11 wrong authoritative index does not fall back by name", func(t *testing.T) {
		other := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{})
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 0, Merged: []Selenium{other, leased}, Source: source,
		})
		if err == nil {
			t.Fatal("wrong pool index was silently remapped to another VDI entry")
		}
		if !strings.Contains(err.Error(), "name") {
			t.Fatalf("error = %v, want identity/name conflict", err)
		}
	})
	t.Run("A12 out-of-range index", func(t *testing.T) {
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: 2, Merged: []Selenium{leased}, Source: source,
		})
		if err == nil {
			t.Fatal("out-of-range index was accepted")
		}
	})
	t.Run("A12b other negative index", func(t *testing.T) {
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: -2, Merged: []Selenium{leased}, Source: source,
		})
		if err == nil {
			t.Fatal("leased index -2 was accepted")
		}
		if errors.Is(err, ErrVDIIdentityMismatch) {
			t.Fatalf("index validation error was misclassified as an identity mismatch: %v", err)
		}
	})
	t.Run("A13 unique name fallback without an index", func(t *testing.T) {
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: -1, Merged: []Selenium{leased}, Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Name != leased.Name {
			t.Fatalf("selection used %q", selection.Browser.Name)
		}
	})
	t.Run("A13b unique-name fallback still validates identity", func(t *testing.T) {
		merged := leased
		merged.Host = "attacker-selenium"
		_, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: -1, Merged: []Selenium{merged}, Source: source,
		})
		if err == nil || !errors.Is(err, ErrVDIIdentityMismatch) {
			t.Fatalf("fallback identity mismatch error = %v, want ErrVDIIdentityMismatch", err)
		}
		if !strings.Contains(err.Error(), "custom.vdi[0]") {
			t.Fatalf("error %v does not identify the resolved source index", err)
		}
	})
	t.Run("A14 missing or duplicate name without an index", func(t *testing.T) {
		other := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{})
		if _, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: -1, Merged: []Selenium{other}, Source: source,
		}); err == nil {
			t.Fatal("missing VDI name was accepted")
		}
		duplicate := leased
		if _, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: leased, LeasedIndex: -1, Merged: []Selenium{leased, duplicate}, Source: source,
		}); err == nil {
			t.Fatal("duplicate VDI name was accepted as a unique fallback")
		}
	})
	t.Run("A14b legacy non-identity fields stay mergeable", func(t *testing.T) {
		merged := leased
		merged.ServiceType = "hub"
		merged.Path = "/legacy/driver"
		merged.DriverPath = "/legacy/driver/bin"
		merged.UseService = true
		selection, err := resolveTestSelection(t, leased, []Selenium{merged}, source)
		if err != nil {
			t.Fatalf("legacy non-identity overrides rejected: %v", err)
		}
		if selection.Browser.Host != leased.Host || selection.Browser.Port != leased.Port ||
			selection.Browser.Type != leased.Type || selection.Browser.Name != leased.Name {
			t.Fatalf("legacy fields altered identity: %+v", selection.Browser)
		}
	})
	t.Run("A15 VDI B override does not affect leased VDI A", func(t *testing.T) {
		first := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})
		second := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{})
		mergedSecond := second
		mergedSecond.Host = "other-selenium"
		selection, err := ResolveEffectiveVDI(EffectiveVDIInput{
			Leased: first, LeasedIndex: 0,
			Merged: []Selenium{first, mergedSecond},
			Source: SourceConfig{Custom: map[string]interface{}{
				"vdi": []interface{}{map[string]interface{}{}, map[string]interface{}{"host": "other-selenium"}},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Browser.Name != first.Name || selection.Browser.Host != first.Host {
			t.Fatalf("VDI A was affected by VDI B override: %+v", selection.Browser)
		}
	})
}

func TestValidateSourceVDIIdentityOverrides(t *testing.T) {
	first := testVDI("vdi-1", "http://127.0.0.1:8080", StickySessionConfig{})
	second := testVDI("vdi-2", "http://127.0.0.1:8082", StickySessionConfig{})
	conf := Config{Selenium: []Selenium{first, second}}

	entry := func(values map[string]interface{}) SourceConfig {
		return SourceConfig{Custom: map[string]interface{}{
			"vdi": []interface{}{map[string]interface{}{}, values},
		}}
	}

	if err := conf.ValidateSourceVDIIdentityOverrides(entry(map[string]interface{}{
		"host": " SELENIUM-VDI-2 ", "port": float64(4444), "type": "CHROME",
	})); err != nil {
		t.Fatalf("equivalent second-position identity rejected: %v", err)
	}
	for _, tc := range []struct {
		name  string
		value map[string]interface{}
	}{
		{"host", map[string]interface{}{"host": "attacker-selenium"}},
		{"port", map[string]interface{}{"port": float64(5555)}},
		{"type", map[string]interface{}{"type": "firefox"}},
		{"sslmode", map[string]interface{}{"sslmode": "enable"}},
	} {
		if err := conf.ValidateSourceVDIIdentityOverrides(entry(tc.value)); err == nil {
			t.Fatalf("second-position %s mismatch was accepted", tc.name)
		}
	}
	outOfRange := SourceConfig{Custom: map[string]interface{}{
		"vdi": []interface{}{map[string]interface{}{}, map[string]interface{}{}, map[string]interface{}{"host": "ignored"}},
	}}
	if err := conf.ValidateSourceVDIIdentityOverrides(outOfRange); err != nil {
		t.Fatalf("entry beyond the configured VDI list should be ignored: %v", err)
	}
}
