package crawler

import (
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func boolPtr(v bool) *bool { return &v }

// TestRedirectNotPermittedDecisionMatrix is the acceptance table for the
// per-source redirect override. It fixes the resource-identity contract:
// standards-equivalent spellings are the same resource, every other difference
// is a redirect.
func TestRedirectNotPermittedDecisionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
		final     string
		follow    bool
		want      bool
	}{
		// --- same resource: not a redirect -----------------------------------
		{"identical URL", "https://a.test/x", "https://a.test/x", false, false},
		{"fragment difference", "https://a.test/x#top", "https://a.test/x", false, false},
		{"fragment only navigation", "https://a.test/x", "https://a.test/x#section", false, false},
		{"root without slash", "https://a.test", "https://a.test/", false, false},
		{"root with slash requested", "https://a.test/", "https://a.test", false, false},
		{"host case", "https://A.Test/x", "https://a.test/x", false, false},
		{"scheme case", "HTTPS://a.test/x", "https://a.test/x", false, false},
		{"https default port", "https://a.test:443/x", "https://a.test/x", false, false},
		{"http default port", "http://a.test:80/x", "http://a.test/x", false, false},
		{"surrounding whitespace", "  https://a.test/x  ", "https://a.test/x", false, false},

		// --- path distinctions: a redirect -----------------------------------
		{"trailing slash on a real path", "https://a.test/hotel", "https://a.test/hotel/", false, true},
		{"trailing slash removed", "https://a.test/hotel/", "https://a.test/hotel", false, true},
		{"double slash", "https://a.test/a//b", "https://a.test/a/b", false, true},
		{"repeated trailing slashes", "https://a.test/a//", "https://a.test/a/", false, true},
		{"escaped separator", "https://a.test/a%2Fb", "https://a.test/a/b", false, true},
		{"path case", "https://a.test/Hotel", "https://a.test/hotel", false, true},
		{"path parent traversal", "https://a.test/a/./b", "https://a.test/a/b", false, true},
		{"child path", "https://a.test/x", "https://a.test/x/child", false, true},

		// --- origin and query distinctions: a redirect -----------------------
		{"cross origin", "https://a.test/x", "https://b.test/x", false, true},
		{"cross origin same path", "https://a.test/hotel", "https://b.test/hotel", false, true},
		{"scheme change", "http://a.test/x", "https://a.test/x", false, true},
		{"non default port", "https://a.test:8443/x", "https://a.test/x", false, true},
		{"query added", "https://a.test/x", "https://a.test/x?a=1", false, true},
		{"query value", "https://a.test/x?a=1", "https://a.test/x?a=2", false, true},
		{"query removed", "https://a.test/x?a=1", "https://a.test/x", false, true},
		{"query separator only", "https://a.test/x", "https://a.test/x?", false, true},
		{"query order", "https://a.test/x?a=1&b=2", "https://a.test/x?b=2&a=1", false, true},

		// --- fail closed -----------------------------------------------------
		{"empty final", "https://a.test/x", "", false, true},
		{"empty requested", "", "https://a.test/x", false, true},
		{"non http final", "https://a.test/x", "file:///etc/passwd", false, true},
		{"browser error page", "https://a.test/x", "chrome-error://chromewebdata/", false, true},
		{"about blank", "https://a.test/x", "about:blank", false, true},
		{"relative final", "https://a.test/x", "/x", false, true},
		{"hostless", "https://a.test/x", "https:///x", false, true},

		// --- policy enabled: everything is permitted --------------------------
		{"following allowed across origin", "https://a.test/x", "https://b.test/y", true, false},
		{"following allowed across path", "https://a.test/hotel", "https://a.test/hotel/", true, false},
		{"following allowed on unparsable final", "https://a.test/x", "", true, false},
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

// TestNavigationResourceIdentity pins the parsed identity fields directly so a
// future change to the equivalence rules is visible instead of implicit.
func TestNavigationResourceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want navigationResource
	}{
		{"origin root", "https://a.test", navigationResource{scheme: "https", host: "a.test", path: "/"}},
		{"root slash", "https://a.test/", navigationResource{scheme: "https", host: "a.test", path: "/"}},
		{"path kept verbatim", "https://a.test/Hotel//x/", navigationResource{scheme: "https", host: "a.test", path: "/Hotel//x/"}},
		{"escaped separator kept", "https://a.test/a%2Fb", navigationResource{scheme: "https", host: "a.test", path: "/a%2Fb"}},
		{"default port dropped", "https://a.test:443/x", navigationResource{scheme: "https", host: "a.test", path: "/x"}},
		{"non default port kept", "https://a.test:8443/x", navigationResource{scheme: "https", host: "a.test:8443", path: "/x"}},
		{"query preserved", "https://a.test/x?a=1&b=2", navigationResource{scheme: "https", host: "a.test", path: "/x", query: "a=1&b=2"}},
		{"bare query separator", "https://a.test/x?", navigationResource{scheme: "https", host: "a.test", path: "/x", query: "?"}},
		{"fragment excluded", "https://a.test/x#frag", navigationResource{scheme: "https", host: "a.test", path: "/x"}},
		{"host lower-cased", "https://A.TEST/x", navigationResource{scheme: "https", host: "a.test", path: "/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNavigationResource(tc.raw)
			if err != nil {
				t.Fatalf("parseNavigationResource(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseNavigationResource(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}

	for _, bad := range []string{"", "   ", "/relative", "mailto:a@b.test", "file:///etc/passwd", "javascript:alert(1)", "https:///x"} {
		if _, err := parseNavigationResource(bad); err == nil {
			t.Fatalf("parseNavigationResource(%q) must fail closed", bad)
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
