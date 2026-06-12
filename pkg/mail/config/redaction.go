package config

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// RedactedValue is the stable replacement used when mail authentication
// material crosses an observability or response boundary.
const RedactedValue = "[REDACTED]"

var (
	sensitiveQuotedJSONPattern = regexp.MustCompile(`(?i)("(?:password|passphrase|secret|api_key|api_secret|api_token|token|access_token|refresh_token|bearer_token|oauth_json|client_secret|private_key|authorization|authorization_header|credential_ref|credential_reference|secret_ref|secret_reference)"\s*:\s*)"(?:\\.|[^"\\])*"`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)(\b(?:password|passphrase|secret|api[_-]?(?:key|secret|token)|token|access[_-]?token|refresh[_-]?token|bearer[_-]?token|oauth[_-]?json|client[_-]?secret|private[_-]?key|authorization(?:[_-]?header)?|credential[_-]?(?:ref|reference)|secret[_-]?(?:ref|reference))\b\s*[:=]\s*)(?:bearer\s+|basic\s+)?(?:"[^"]*"|'[^']*'|[^\s,;}&\]]+)`)
	authorizationHeaderPattern = regexp.MustCompile(`(?im)(^\s*authorization\s*:\s*)(?:bearer\s+|basic\s+)?[^\r\n]+`)
)

// IsSensitiveKey reports whether a structured field names mail credentials or
// a reference to credentials. Matching is case-insensitive and treats dashes,
// spaces, and dots like underscores.
func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(normalized)
	compact := strings.ReplaceAll(normalized, "_", "")
	switch compact {
	case "password", "passphrase", "secret", "apikey", "apisecret", "apitoken", "token",
		"accesstoken", "refreshtoken", "bearertoken", "oauthjson", "clientsecret", "privatekey",
		"authorization", "authorizationheader", "credentialref", "credentialreference",
		"secretref", "secretreference":
		return true
	}
	switch normalized {
	case "password", "passphrase", "secret", "api_key", "api_secret", "api_token", "token",
		"access_token", "refresh_token", "bearer_token", "oauth_json", "client_secret", "private_key",
		"authorization", "authorization_header", "credential_ref", "credential_reference",
		"secret_ref", "secret_reference":
		return true
	default:
		return strings.HasSuffix(normalized, "_password") ||
			strings.HasSuffix(normalized, "_secret") ||
			strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_private_key") ||
			strings.HasSuffix(normalized, "_access_token") ||
			strings.HasSuffix(normalized, "_refresh_token") ||
			strings.HasSuffix(normalized, "_client_secret") ||
			strings.HasSuffix(normalized, "_authorization") ||
			strings.HasSuffix(normalized, "_credential_ref") ||
			strings.HasSuffix(normalized, "_secret_ref")
	}
}

// RedactValue returns a recursively redacted copy of JSON-shaped data. It does
// not mutate maps or slices supplied by the caller. String values are also
// scanned so formatted headers and key/value diagnostics are protected even
// when their containing field has a non-sensitive name.
func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, nested := range typed {
			if IsSensitiveKey(key) && hasSensitiveValue(nested) {
				redacted[key] = RedactedValue
				continue
			}
			redacted[key] = RedactValue(nested)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, nested := range typed {
			redacted[index] = RedactValue(nested)
		}
		return redacted
	case string:
		return RedactString(typed)
	default:
		return value
	}
}

func hasSensitiveValue(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return text != ""
	}
	return true
}

// RedactString removes supported mail secrets from formatted diagnostics,
// URLs, JSON snippets, and HTTP Authorization header lines.
func RedactString(value string) string {
	value = authorizationHeaderPattern.ReplaceAllString(value, `${1}`+RedactedValue)
	value = sensitiveQuotedJSONPattern.ReplaceAllString(value, `${1}"`+RedactedValue+`"`)
	return sensitiveAssignmentPattern.ReplaceAllString(value, `${1}`+RedactedValue)
}

// RedactJSON redacts a JSON document while preserving its structured shape.
func RedactJSON(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(RedactValue(value))
}

// RedactedSourceConfig returns a deep, redacted copy suitable for APIs and
// diagnostics. The original source configuration is left unchanged.
func RedactedSourceConfig(source SourceConfig) (SourceConfig, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return SourceConfig{}, err
	}
	redacted, err := RedactJSON(encoded)
	if err != nil {
		return SourceConfig{}, err
	}
	var result SourceConfig
	if err := json.Unmarshal(redacted, &result); err != nil {
		return SourceConfig{}, err
	}
	return result, nil
}

// String formats source configuration for diagnostics without exposing
// authentication material or secret-store references.
func (source SourceConfig) String() string {
	return source.redactedString()
}

// GoString provides the same protection for %#v formatting.
func (source SourceConfig) GoString() string {
	return source.redactedString()
}

// Format protects all fmt representations, including %+v and %#v.
func (source SourceConfig) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, source.redactedString())
}

func (source SourceConfig) redactedString() string {
	encoded, err := json.Marshal(source)
	if err != nil {
		return "<mail config unavailable>"
	}
	redacted, err := RedactJSON(encoded)
	if err != nil {
		return "<mail config unavailable>"
	}
	return string(redacted)
}
