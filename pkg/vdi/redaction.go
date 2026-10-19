package vdi

import "strings"

// cdpRedactedMarker replaces any value whose key is a credential/secret key in
// the verbose CDP log, so the most-verbose logging level never prints secret
// material.
const cdpRedactedMarker = "[redacted]"

// sensitiveCredentialKeys mirrors the credential/secret vocabulary already used
// by the information-seed and mail redactors (cookie, set-cookie,
// authorization, proxy-authorization, password, secret, token) extended with the
// common HTTP authentication headers. It is keyed on the header/parameter NAME
// (case-insensitive) so a verbose CDP log never exposes a proxy credential, an
// authentication token, a session cookie or a complete authenticated proxy URL.
//
// It is intentionally a small, dedicated table rather than a new general-purpose
// redaction framework: the information-seed and mail packages keep their own
// helpers and are left untouched.
var sensitiveCredentialKeys = map[string]bool{
	"cookie":              true,
	"cookie2":             true,
	"set-cookie":          true,
	"authorization":       true,
	"proxy-authorization": true,
	"www-authenticate":    true,
	"x-authorization":     true,
	"x-csrf-token":        true,
	"x-xsrf-token":        true,
	"x-auth-token":        true,
	"x-csrf":              true,
	"x-xsrf":              true,
	"token":               true,
	"secret":              true,
	"secrets":             true,
	"password":            true,
	"passwd":              true,
	"api-key":             true,
	"apikey":              true,
	"x-api-key":           true,
	"proxy-user":          true,
	"proxy-password":      true,
	"proxy_user":          true,
	"proxy_password":      true,
}

// redactCDPParams returns a deep, LOG-SAFE copy of a CDP command's parameter
// map with sensitive values scrubbed. The copy is used ONLY for the verbose log
// line in ExecuteCDPCommand; the caller's original map is not mutated and is
// still handed to the driver unchanged.
//
// Redaction is key-driven (a value whose key is a credential/secret key is fully
// masked) and value-driven for proxy/URL userinfo (the "user:pass@" of an
// authenticated proxy URL is masked while the scheme, host and port are kept so
// the log stays diagnostic). Benign values — a referer, a User-Agent, Client
// Hints metadata, and numeric/boolean parameters — are preserved so diagnostics
// remain useful.
func redactCDPParams(params map[string]interface{}) interface{} {
	if len(params) == 0 {
		return nil
	}
	return redactMap(params)
}

// redactMap builds a new redacted copy of m. A value under a sensitive key is
// fully masked; otherwise the value is redacted recursively so nested header
// collections and lists are covered.
func redactMap(m map[string]interface{}) interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if sensitiveCredentialKeys[strings.ToLower(k)] {
			out[k] = cdpRedactedMarker
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

// redactAny redacts a single value by kind, preserving structure (maps, lists,
// scalars) and only the sensitive material.
func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return redactMap(t)
	case map[string]string:
		return redactStringMap(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = redactValue(e)
		}
		return out
	case string:
		return redactURLUserinfo(t)
	}
	return v
}

// redactStringMap redacts a map[string]string by key and per-value userinfo,
// returning a map[string]interface{} so it can be nested uniformly.
func redactStringMap(m map[string]string) interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if sensitiveCredentialKeys[strings.ToLower(k)] {
			out[k] = cdpRedactedMarker
			continue
		}
		out[k] = redactURLUserinfo(v)
	}
	return out
}

// redactURLUserinfo scrubs the "user:pass@" userinfo of an authenticated
// proxy/URL string while preserving the scheme, host, port (and path), reusing
// the existing proxy redaction. Strings that are not credential-shaped URLs — an
// email, or a path that merely contains an '@' — are returned unchanged so the
// log is not mangled.
func redactURLUserinfo(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	if i := strings.Index(s, "://"); i >= 0 {
		// Restrict the authority to the segment before the first path/query/
		// fragment separator so an '@' in the path cannot be mistreated.
		rest := s[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			rest = rest[:j]
		}
		if !strings.Contains(rest, "@") {
			return s
		}
		return redactProxyURL(s)
	}
	// Schemeless: only a "user:pass@host" credential shape is redacted.
	if i := strings.LastIndex(s, "@"); i > 0 && strings.Contains(s[:i], ":") &&
		!strings.ContainsAny(s[:i], " \t/#") {
		return redactProxyURL(s)
	}
	return s
}
