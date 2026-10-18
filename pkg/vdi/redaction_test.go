package vdi

import (
	"reflect"
	"strings"
	"testing"
)

// TestRedactCDPParamsRedactsSensitiveValues proves the verbose-log redactor
// scrubs credential/secret and proxy-credential material from nested CDP
// parameter structures at the most-verbose level.
func TestRedactCDPParamsRedactsSensitiveValues(t *testing.T) {
	t.Run("nested headers", func(t *testing.T) {
		params := map[string]interface{}{
			"headers": map[string]interface{}{
				"Referer":             "https://example.com/path",
				"Cookie":              "sessionid=secretvalue; csrftoken=abc",
				"Authorization":       "Bearer supersecrettoken",
				"Proxy-Authorization": "Basic dXNlcjpwYXNz",
				"User-Agent":          "Mozilla/5.0",
			},
		}
		got := redactCDPParams(params).(map[string]interface{})
		headers, _ := got["headers"].(map[string]interface{})
		if headers == nil {
			t.Fatalf("nested headers not preserved: %v", got)
		}
		if v := headers["Cookie"].(string); v != cdpRedactedMarker {
			t.Errorf("Cookie not redacted: %q", v)
		}
		if v := headers["Authorization"].(string); v != cdpRedactedMarker {
			t.Errorf("Authorization not redacted: %q", v)
		}
		// The redactor preserves the original key case; look it up as written.
		if v := headers["Proxy-Authorization"].(string); v != cdpRedactedMarker {
			t.Errorf("Proxy-Authorization (case-insensitive key) not redacted: %q", v)
		}
		// Benign surfaces are preserved so the log stays diagnostic.
		if v := headers["Referer"].(string); v != "https://example.com/path" {
			t.Errorf("benign Referer lost: %q", v)
		}
		if v := headers["User-Agent"].(string); v != "Mozilla/5.0" {
			t.Errorf("benign User-Agent lost: %q", v)
		}
	})

	t.Run("authored proxy URL userinfo scrubbed, host preserved", func(t *testing.T) {
		params := map[string]interface{}{
			"headers": map[string]interface{}{
				"X-Custom": "http://user:s3cr3t@proxy.local:8080/",
			},
		}
		got := redactCDPParams(params).(map[string]interface{})
		headers := got["headers"].(map[string]interface{})
		gotVal, _ := headers["X-Custom"].(string)
		if strings.Contains(gotVal, "s3cr3t") || strings.Contains(gotVal, "user:") {
			t.Errorf("proxy credential leaked: %q", gotVal)
		}
		// Host/port kept for diagnosis.
		if !strings.Contains(gotVal, "proxy.local:8080") {
			t.Errorf("proxy host/port lost: %q", gotVal)
		}
	})

	t.Run("path with @ is not mangled", func(t *testing.T) {
		params := map[string]interface{}{
			"headers": map[string]interface{}{
				"referer": "https://example.com/with@query",
				"email":   "someone@corp.com",
			},
		}
		got := redactCDPParams(params).(map[string]interface{})
		headers := got["headers"].(map[string]interface{})
		if v := headers["referer"].(string); v != "https://example.com/with@query" {
			t.Errorf("path-@ URL mangled: %q", v)
		}
		if v := headers["email"].(string); v != "someone@corp.com" {
			t.Errorf("email mangled: %q", v)
		}
	})
}

// TestRedactCDPParamsDoesNotMutateInput proves the operational parameters sent
// to the driver are never altered: redaction is performed on a copy.
func TestRedactCDPParamsDoesNotMutateInput(t *testing.T) {
	params := map[string]interface{}{
		"headers": map[string]interface{}{
			"Cookie":        "sessionid=keepme",
			"Authorization": "Bearer keepme",
			"Referer":       "https://example.com",
		},
	}
	before := cloneParams(params)
	_ = redactCDPParams(params)
	if !reflect.DeepEqual(params, before) {
		t.Errorf("redaction mutated the input: before=%v after=%v", before, params)
	}
	if c := params["headers"].(map[string]interface{})["Cookie"].(string); c != "sessionid=keepme" {
		t.Errorf("original Cookie mutated to %q", c)
	}
}

// TestRedactCDPParamsPassthrough proves benign, empty, and non-sensitive CDP
// payloads pass through without spurious redaction.
func TestRedactCDPParamsPassthrough(t *testing.T) {
	if got := redactCDPParams(nil); got != nil {
		t.Errorf("nil params should stay nil, got %v", got)
	}
	if got := redactCDPParams(map[string]interface{}{}); got != nil {
		t.Errorf("empty params should stay nil, got %v", got)
	}
	params := map[string]interface{}{
		"maxPostDataSize": 65536,
		"cacheDisabled":   true,
		"userAgent":       "Mozilla/5.0",
		"platform":        "Win32",
		"autoAttach":      true,
	}
	got := redactCDPParams(params).(map[string]interface{})
	for k, want := range map[string]interface{}{
		"maxPostDataSize": 65536, "cacheDisabled": true,
		"userAgent": "Mozilla/5.0", "platform": "Win32", "autoAttach": true,
	} {
		if !reflect.DeepEqual(got[k], want) {
			t.Errorf("benign key %q = %v, want %v", k, got[k], want)
		}
	}
}

// TestRedactCDPParamsStringMap proves nested map[string]string header values are
// also redacted by key.
func TestRedactCDPParamsStringMap(t *testing.T) {
	params := map[string]interface{}{
		"headers": map[string]string{
			"Cookie":      "keepaway",
			"Some-Benign": "ok",
		},
	}
	got := redactCDPParams(params).(map[string]interface{})
	red, ok := got["headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected redacted map[string]interface{}, got %T", got["headers"])
	}
	if red["Cookie"] != cdpRedactedMarker {
		t.Errorf("map[string]string Cookie not redacted: %v", red["Cookie"])
	}
	if red["Some-Benign"] != "ok" {
		t.Errorf("benign map[string]string value lost: %v", red["Some-Benign"])
	}
}

// cloneParams deep-copies a CDP parameter map for the non-mutation assertion.
func cloneParams(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case map[string]interface{}:
			out[k] = cloneParams(t)
		case map[string]string:
			sm := make(map[string]string, len(t))
			for kk, vv := range t {
				sm[kk] = vv
			}
			out[k] = sm
		default:
			out[k] = v
		}
	}
	return out
}
