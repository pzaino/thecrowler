package crawler

import (
	"fmt"
	"net/url"
	"strings"
)

// Source-scoped redirect policy.
//
// The browser follows HTTP redirects itself: Selenium navigation cannot be told
// to stop at a 3xx, and a cross-origin redirect has already happened by the time
// the crawler can inspect the page. The override implemented here therefore
// controls *acceptance*, not interception: when a source forbids following
// redirects, a navigation that lands on a different resource is rejected and
// retried through the existing `retries_on_redirect` budget instead of being
// treated as the requested page.
//
// This is deliberately conservative:
//
//   - the HTTP-level fetch paths (HTTP header collection) honour the same
//     effective value through http_headers.follow_redirects;
//   - browser-managed redirects are detected after navigation and refused,
//     which is documented as a limitation rather than claimed as interception;
//   - the redirect target is never used to bypass the source's own URL
//     restrictions, unwanted-URL patterns or ownership checks, because the
//     existing checks still run on the final URL.

// redirectsFollowingPermitted reports the effective redirect policy for this
// source. The source-level `crawling_config.follow_redirects` override has
// already been folded into the merged configuration by
// config.CombineConfigs, so a single value governs HTTP and browser paths.
func (ctx *ProcessContext) redirectsFollowingPermitted() bool {
	if ctx == nil {
		return true
	}
	return ctx.config.EffectiveFollowRedirects()
}

// navigationResource is the part of a URL that identifies an HTTP resource for
// redirect-acceptance purposes.
//
// The comparison is intentionally narrow. Two navigations are treated as the
// same resource only when they are standards-equivalent for the server:
//
//   - scheme, lower-cased (schemes are case-insensitive);
//   - host, lower-cased (DNS names are case-insensitive) with the scheme's
//     default port removed (`http://h` == `http://h:80`);
//   - path, byte-for-byte as serialized, EXCEPT that an empty path becomes `/`
//     because that is how a browser serializes the origin root
//     (`https://host` == `https://host/`);
//   - query, byte-for-byte, including whether a query separator was present;
//   - the fragment is excluded because a browser never sends it to the server.
//
// Everything else is a distinct resource and therefore a redirect when the
// policy forbids following redirects. In particular `/hotel` and `/hotel/` are
// different paths for HTTP servers and stay different here, as do `/a//b` and
// `/a/b`, `%2F` versus `/`, path case, and any query difference.
type navigationResource struct {
	scheme string
	host   string
	path   string
	query  string
}

// canonicalHost lower-cases the host and removes the default port for the
// scheme. It does not otherwise rewrite the authority.
func canonicalHost(parsed *url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port != "" {
		switch parsed.Scheme {
		case "http":
			if port == "80" {
				port = ""
			}
		case "https":
			if port == "443" {
				port = ""
			}
		}
	}
	if port == "" {
		return host
	}
	return host + ":" + port
}

// parseNavigationResource parses a navigation URL into the comparable resource
// identity. It fails closed on anything that is not an absolute http(s) URL with
// a host, because such a destination can never be proven to be the requested
// resource.
func parseNavigationResource(raw string) (navigationResource, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return navigationResource{}, fmt.Errorf("empty URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return navigationResource{}, fmt.Errorf("unparsable URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return navigationResource{}, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return navigationResource{}, fmt.Errorf("missing host")
	}
	// EscapedPath preserves the distinction between an escaped separator
	// (%2F) and a real path separator, which Path would collapse.
	path := parsed.EscapedPath()
	if path == "" {
		// Browsers serialize the origin root with a single slash.
		path = "/"
	}
	query := parsed.RawQuery
	if parsed.ForceQuery {
		// Preserve "?" with no parameters as distinct from no query at all.
		query += "?"
	}
	return navigationResource{
		scheme: scheme,
		host:   canonicalHost(parsed),
		path:   path,
		query:  query,
	}, nil
}

// redirectNotPermitted reports whether a completed navigation ended on a
// different resource than the one requested while the effective policy forbids
// following redirects. A destination that cannot be parsed as an absolute
// http(s) URL fails closed.
func redirectNotPermitted(requested, final string, follow bool) bool {
	if follow {
		return false
	}
	requestedResource, err := parseNavigationResource(requested)
	if err != nil {
		return true
	}
	finalResource, err := parseNavigationResource(final)
	if err != nil {
		return true
	}
	return requestedResource != finalResource
}
