package config

import (
	"encoding/json"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

// TestSourceCrawlingOverrideDefaultsPreserved proves that an omitted source
// override leaves both the global redirect policy and the historical private
// browsing default untouched.
func TestSourceCrawlingOverrideDefaultsPreserved(t *testing.T) {
	base := mergeTestBase()
	base.HTTPHeaders.FollowRedirects = true

	got := CombineConfigs(base, SourceConfig{})

	if got.Crawler.Incognito != nil {
		t.Fatalf("incognito = %v, want nil (default preserved)", *got.Crawler.Incognito)
	}
	if !got.EffectiveIncognito() {
		t.Fatal("effective incognito must stay private when the source omits it")
	}
	if !got.EffectiveFollowRedirects() {
		t.Fatal("effective redirect policy must inherit the global true value")
	}
}

// TestSourceCrawlingOverrideIncognito covers explicit true/false and proves an
// explicit false is not mistaken for an absent value.
func TestSourceCrawlingOverrideIncognito(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  *bool
		want bool
	}{
		{"explicit false disables private browsing", boolPtr(false), false},
		{"explicit true enables private browsing", boolPtr(true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CombineConfigs(mergeTestBase(), SourceConfig{
				CrawlingConfig: CrawlingConfig{Incognito: tc.set},
			})
			if got.Crawler.Incognito == nil {
				t.Fatal("expected the source override to be recorded")
			}
			if got.EffectiveIncognito() != tc.want {
				t.Fatalf("effective incognito = %v, want %v", got.EffectiveIncognito(), tc.want)
			}
		})
	}
}

// TestSourceCrawlingOverrideFollowRedirectsMatrix is the precedence table from
// the work package: global is the default, the source override wins when set.
func TestSourceCrawlingOverrideFollowRedirectsMatrix(t *testing.T) {
	for _, tc := range []struct {
		global    bool
		source    *bool
		effective bool
	}{
		{false, nil, false},
		{false, boolPtr(true), true},
		{true, boolPtr(false), false},
		{true, nil, true},
	} {
		base := mergeTestBase()
		base.HTTPHeaders.FollowRedirects = tc.global
		got := CombineConfigs(base, SourceConfig{
			CrawlingConfig: CrawlingConfig{FollowRedirects: tc.source},
		})
		if got.EffectiveFollowRedirects() != tc.effective {
			t.Errorf("global=%v source=%v effective=%v, want %v",
				tc.global, tc.source, got.EffectiveFollowRedirects(), tc.effective)
		}
		// The global configuration value itself must never be rewritten: the
		// merge works on the per-source copy.
		if base.HTTPHeaders.FollowRedirects != tc.global {
			t.Errorf("global config mutated: %v -> %v", tc.global, base.HTTPHeaders.FollowRedirects)
		}
	}
}

// TestSourceCrawlingOverrideIsolation proves two sources merged from the same
// base keep independent values and never share a mutable flag.
func TestSourceCrawlingOverrideIsolation(t *testing.T) {
	base := mergeTestBase()
	base.HTTPHeaders.FollowRedirects = true

	private := CombineConfigs(base, SourceConfig{CrawlingConfig: CrawlingConfig{
		Incognito: boolPtr(true), FollowRedirects: boolPtr(false),
	}})
	normal := CombineConfigs(base, SourceConfig{CrawlingConfig: CrawlingConfig{
		Incognito: boolPtr(false), FollowRedirects: boolPtr(true),
	}})

	if !private.EffectiveIncognito() || normal.EffectiveIncognito() {
		t.Fatalf("incognito leaked: private=%v normal=%v", private.EffectiveIncognito(), normal.EffectiveIncognito())
	}
	if private.EffectiveFollowRedirects() || !normal.EffectiveFollowRedirects() {
		t.Fatalf("redirect policy leaked: private=%v normal=%v",
			private.EffectiveFollowRedirects(), normal.EffectiveFollowRedirects())
	}
	if private.Crawler.Incognito == normal.Crawler.Incognito {
		t.Fatal("two sources must not share the same incognito pointer")
	}
	// Mutating one source's merged copy must not affect the other.
	*normal.Crawler.Incognito = true
	if private.EffectiveIncognito() != true || normal.EffectiveIncognito() != true {
		t.Fatal("unexpected shared state between merged source configs")
	}
	if base.Crawler.Incognito != nil {
		t.Fatal("the base configuration must never receive a source override")
	}
}

// TestCrawlingConfigStrictBooleans proves malformed documents are rejected
// rather than silently disabling private browsing or enabling redirects.
func TestCrawlingConfigStrictBooleans(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"omitted", `{"site":"https://example.test/"}`, false},
		{"true", `{"site":"https://example.test/","incognito":true,"follow_redirects":true}`, false},
		{"false", `{"site":"https://example.test/","incognito":false,"follow_redirects":false}`, false},
		{"null is treated as absent", `{"site":"https://example.test/","incognito":null,"follow_redirects":null}`, false},
		{"string incognito", `{"site":"https://example.test/","incognito":"false"}`, true},
		{"numeric incognito", `{"site":"https://example.test/","incognito":0}`, true},
		{"numeric follow_redirects", `{"site":"https://example.test/","follow_redirects":1}`, true},
		{"object incognito", `{"site":"https://example.test/","incognito":{}}`, true},
		{"array follow_redirects", `{"site":"https://example.test/","follow_redirects":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var crawling CrawlingConfig
			err := json.Unmarshal([]byte(tc.raw), &crawling)
			if tc.wantErr && err == nil {
				t.Fatal("expected a decode error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected decode error: %v", err)
			}
			if !tc.wantErr && tc.name == "null is treated as absent" {
				if crawling.Incognito != nil || crawling.FollowRedirects != nil {
					t.Fatal("JSON null must be treated as absent, not as an explicit false")
				}
			}
		})
	}
}

// TestCrawlingConfigNullKeepsPrivateBrowsing is the behavioural consequence of
// accepting JSON null as "absent".
func TestCrawlingConfigNullKeepsPrivateBrowsing(t *testing.T) {
	var src SourceConfig
	if err := json.Unmarshal([]byte(`{"crawling_config":{"site":"https://example.test/","incognito":null}}`), &src); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := CombineConfigs(mergeTestBase(), src)
	if !got.EffectiveIncognito() {
		t.Fatal("JSON null must preserve the private-browsing default")
	}
}

// TestCrawlingConfigRoundTripPreservesOverrides proves the API round-trip
// (marshal -> unmarshal -> merge) keeps an explicit false.
func TestCrawlingConfigRoundTripPreservesOverrides(t *testing.T) {
	src := SourceConfig{
		Version:        "1.0",
		FormatVersion:  "1.0.0",
		SourceName:     "round-trip",
		CrawlingConfig: CrawlingConfig{Site: "https://example.test/", Incognito: boolPtr(false), FollowRedirects: boolPtr(false)},
	}
	encoded, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SourceConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.CrawlingConfig.Incognito == nil || *decoded.CrawlingConfig.Incognito {
		t.Fatalf("incognito lost in round trip: %v", decoded.CrawlingConfig.Incognito)
	}
	if decoded.CrawlingConfig.FollowRedirects == nil || *decoded.CrawlingConfig.FollowRedirects {
		t.Fatalf("follow_redirects lost in round trip: %v", decoded.CrawlingConfig.FollowRedirects)
	}
	got := CombineConfigs(mergeTestBase(), decoded)
	if got.EffectiveIncognito() {
		t.Fatal("explicit false became private after a round trip")
	}
	if got.EffectiveFollowRedirects() {
		t.Fatal("explicit false became true after a round trip")
	}
}
