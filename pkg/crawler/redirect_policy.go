package crawler

import (
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

// normalizedNavigationURL canonicalises a URL for redirect comparison. Only
// cosmetic differences are collapsed (surrounding whitespace, URL fragment and
// a trailing slash), because a browser reports `https://host` as
// `https://host/` without any HTTP redirect having occurred.
func normalizedNavigationURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimRight(trimmed, "/")
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

// redirectNotPermitted reports whether a completed navigation ended on a
// different resource than the one requested while the effective policy forbids
// following redirects.
func redirectNotPermitted(requested, final string, follow bool) bool {
	if follow {
		return false
	}
	requestedURL := normalizedNavigationURL(requested)
	finalURL := normalizedNavigationURL(final)
	if requestedURL == "" || finalURL == "" {
		// An unparsable or empty destination cannot be proven to be the
		// requested resource: fail closed.
		return true
	}
	return requestedURL != finalURL
}
