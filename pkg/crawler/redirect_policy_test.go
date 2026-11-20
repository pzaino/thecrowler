package crawler

import (
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func boolPtr(v bool) *bool { return &v }

// TestRedirectNotPermittedDecisionMatrix is the core acceptance table for the
// per-source redirect override, including the cosmetic-equivalence cases that
// must not be reported as redirects.
func TestRedirectNotPermittedDecisionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
		final     string
		follow    bool
		want      bool
	}{
		{"same url", "https://a.test/x", "https://a.test/x", false, false},
		{"same url with fragment", "https://a.test/x#top", "https://a.test/x", false, false},
		{"browser adds root slash", "https://a.test", "https://a.test/", false, false},
		{"trailing slash only", "https://a.test/x/", "https://a.test/x", false, false},
		{"cross origin redirect refused", "https://a.test/x", "https://b.test/y", false, true},
		{"same origin redirect refused", "https://a.test/x", "https://a.test/y", false, true},
		{"scheme upgrade refused", "http://a.test/x", "https://a.test/x", false, true},
		{"path child refused", "https://a.test/x", "https://a.test/x/child", false, true},
		{"following allowed when enabled", "https://a.test/x", "https://b.test/y", true, false},
		{"empty final fails closed", "https://a.test/x", "", false, true},
		{"empty requested fails closed", "", "https://a.test/x", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := redirectNotPermitted(tc.requested, tc.final, tc.follow)
			if got != tc.want {
				t.Fatalf("redirectNotPermitted(%q, %q, %v) = %v, want %v",
					tc.requested, tc.final, tc.follow, got, tc.want)
			}
		})
	}
}

// TestNormalizedNavigationURL covers the comparison helper directly so a future
// change to the cosmetic rules is visible.
func TestNormalizedNavigationURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  https://a.test/x  ", "https://a.test/x"},
		{"https://a.test/x#frag", "https://a.test/x"},
		{"https://a.test/", "https://a.test"},
		{"https://a.test//", "https://a.test"},
		{"", ""},
	} {
		if got := normalizedNavigationURL(tc.in); got != tc.want {
			t.Errorf("normalizedNavigationURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRedirectsFollowingPermittedReadsMergedConfig proves the browser path reads
// the same effective value that the HTTP path uses, for all four precedence
// combinations.
func TestRedirectsFollowingPermittedReadsMergedConfig(t *testing.T) {
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
		base := *cfg.NewConfig()
		base.HTTPHeaders.FollowRedirects = tc.global
		merged := cfg.CombineConfigs(base, cfg.SourceConfig{
			CrawlingConfig: cfg.CrawlingConfig{FollowRedirects: tc.source},
		})
		ctx := &ProcessContext{config: merged}
		if got := ctx.redirectsFollowingPermitted(); got != tc.effective {
			t.Errorf("global=%v source=%v: permitted=%v, want %v", tc.global, tc.source, got, tc.effective)
		}
	}
}

// TestRedirectsFollowingPermittedNilContext documents the fail-open guard for a
// nil context (defensive; production always has a context).
func TestRedirectsFollowingPermittedNilContext(t *testing.T) {
	var ctx *ProcessContext
	if !ctx.redirectsFollowingPermitted() {
		t.Fatal("nil context must not be treated as a redirect-forbidding source")
	}
}
