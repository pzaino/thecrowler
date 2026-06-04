package infoseed

import "testing"

func TestNormalizeURLCanonicalizesAndRemovesTracking(t *testing.T) {
	got, host, ok := NormalizeURL("HTTPS://Example.COM:443/path?utm_source=x&b=2&a=1#frag", []string{"utm_source"})
	if !ok {
		t.Fatal("expected URL to normalize")
	}
	if host != "example.com" {
		t.Fatalf("expected host example.com, got %q", host)
	}
	if got != "https://example.com/path?a=1&b=2" {
		t.Fatalf("unexpected normalized URL: %s", got)
	}
}

func TestNormalizeCandidatesDropsUnsupportedAndDeduplicatesHost(t *testing.T) {
	candidates := []Candidate{
		{URL: "https://example.com:443/a?utm_campaign=x"},
		{URL: "https://example.com/b"},
		{URL: "mailto:test@example.com"},
		{URL: "http://other.example:80/"},
	}
	got := NormalizeCandidates(candidates, CandidateOptions{TrackingParams: []string{"utm_campaign"}, DeduplicateHost: true})
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %#v", len(got), got)
	}
	if got[0].URL != "https://example.com/a" || got[1].URL != "http://other.example/" {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestValidateCandidatePluginOutputRequiresContract(t *testing.T) {
	output, err := validateCandidatePluginOutput([]byte(`{"accepted":true,"score":0.87,"reason":"trusted source","tags":["news"]}`))
	if err != nil {
		t.Fatalf("expected valid plugin output, got %v", err)
	}
	if output.Accepted == nil || !*output.Accepted || output.Score == nil || *output.Score != 0.87 {
		t.Fatalf("unexpected output: %#v", output)
	}

	if _, err := validateCandidatePluginOutput([]byte(`{"accept":true,"score":1,"reason":"legacy field"}`)); err == nil {
		t.Fatal("expected legacy/unknown fields to fail schema validation")
	}
	if _, err := validateCandidatePluginOutput([]byte(`{"accepted":true,"score":1}`)); err == nil {
		t.Fatal("expected missing reason to fail schema validation")
	}
}

func TestValidateSourceOverridesSafeAllowList(t *testing.T) {
	overrides, err := validateSourceOverrides(map[string]interface{}{
		"name":          "Example Source",
		"priority":      "high",
		"restricted":    float64(1),
		"flags":         float64(2),
		"source_config": map[string]interface{}{"version": "1.0.0"},
	})
	if err != nil {
		t.Fatalf("expected safe source overrides, got %v", err)
	}
	if overrides.Name == nil || *overrides.Name != "Example Source" {
		t.Fatalf("unexpected name override: %#v", overrides.Name)
	}
	if len(overrides.SourceConfig) == 0 {
		t.Fatal("expected source_config override to be preserved")
	}

	if _, err := validateSourceOverrides(map[string]interface{}{"url": "https://evil.example"}); err == nil {
		t.Fatal("expected unsafe url override to fail")
	}
	if _, err := validateSourceOverrides(map[string]interface{}{"restricted": 1.5}); err == nil {
		t.Fatal("expected non-integer restricted override to fail")
	}
}
