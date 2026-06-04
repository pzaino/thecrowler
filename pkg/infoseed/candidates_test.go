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
