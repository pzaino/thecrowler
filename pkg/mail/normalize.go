package mail

import (
	"net/mail"
	"net/textproto"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxRetainedHeaderNames     = 200
	maxRetainedValuesPerHeader = 20
	maxRetainedHeaderValueSize = 4096
	maxRetainedHeadersSize     = 64 * 1024
)

var (
	messageIDPattern      = regexp.MustCompile(`<([^<>\s]+)>`)
	authResultPattern     = regexp.MustCompile(`(?i)(?:^|[;\s])(?:smtp\.)?(spf|dkim|dmarc|arc|tls)\s*=\s*([a-z][a-z0-9_-]*)`)
	arcCVPattern          = regexp.MustCompile(`(?i)(?:^|[;\s])cv\s*=\s*([a-z][a-z0-9_-]*)`)
	signatureValuePattern = regexp.MustCompile(`(?i)(^|;)\s*b\s*=\s*[^;]*`)
)

func boundedHeaders(source map[string][]string, redactSignatures bool) (HeaderMap, []ParserWarning) {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	retained := make(HeaderMap)
	var warnings []ParserWarning
	total := 0
	for _, key := range keys {
		if len(retained) >= maxRetainedHeaderNames {
			warnings = append(warnings, headerWarning("headers_truncated", "header count exceeded retention limit", ""))
			break
		}
		name := textproto.CanonicalMIMEHeaderKey(key)
		if name == "" {
			warnings = append(warnings, headerWarning("malformed_header_name", "header name was not retained", ""))
			continue
		}

		values := source[key]
		limit := len(values)
		if limit > maxRetainedValuesPerHeader {
			limit = maxRetainedValuesPerHeader
			warnings = append(warnings, headerWarning("header_values_truncated", "repeated header values exceeded retention limit", name))
		}
		for _, value := range values[:limit] {
			value = safeHeaderValue(value)
			if redactSignatures && isSignatureHeader(name) {
				value = redactSignature(value)
			}
			value, truncated := truncateUTF8(value, maxRetainedHeaderValueSize)
			if truncated {
				warnings = append(warnings, headerWarning("header_value_truncated", "header value exceeded retention limit", name))
			}
			if total+len(name)+len(value) > maxRetainedHeadersSize {
				warnings = append(warnings, headerWarning("headers_truncated", "headers exceeded total retention limit", name))
				return retained, warnings
			}
			retained[name] = append(retained[name], value)
			total += len(name) + len(value)
		}
	}
	return retained, warnings
}

func safeHeaderValue(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		default:
			if unicode.IsControl(r) {
				return '�'
			}
			return r
		}
	}, value)
	return strings.TrimSpace(value)
}

func unfoldHeaderValue(value string) string {
	return strings.Join(strings.Fields(safeHeaderValue(value)), " ")
}

func truncateUTF8(value string, maximum int) (string, bool) {
	if len(value) <= maximum {
		return value, false
	}
	end := maximum - len("…")
	if end < 0 {
		return "", true
	}
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…", true
}

func isSignatureHeader(name string) bool {
	switch name {
	case "Dkim-Signature", "Domainkey-Signature", "Arc-Seal", "Arc-Message-Signature":
		return true
	default:
		return false
	}
}

func redactSignature(value string) string {
	return signatureValuePattern.ReplaceAllString(value, "$1 b=[redacted]")
}

func normalizeSubject(headers HeaderMap) string {
	for _, value := range headers["Subject"] {
		if normalized := unfoldHeaderValue(value); normalized != "" {
			return normalized
		}
	}
	return ""
}

func normalizeMessageID(headers HeaderMap, name string) string {
	for _, value := range headers[textproto.CanonicalMIMEHeaderKey(name)] {
		if ids := messageIDs(value); len(ids) != 0 {
			return ids[0]
		}
	}
	return ""
}

func messageIDs(value string) []string {
	matches := messageIDPattern.FindAllStringSubmatch(safeHeaderValue(value), -1)
	ids := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		id := normalizeMessageIDToken(match[1])
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func normalizeMessageIDToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "<>\r\n\t ") {
		return ""
	}
	if at := strings.LastIndexByte(value, '@'); at >= 0 {
		value = value[:at+1] + strings.ToLower(value[at+1:])
	}
	return "<" + value + ">"
}

func normalizeReferences(headers HeaderMap) []string {
	var references []string
	seen := make(map[string]bool)
	for _, value := range headers["References"] {
		for _, id := range messageIDs(value) {
			if !seen[id] {
				seen[id] = true
				references = append(references, id)
			}
		}
	}
	return references
}

func normalizeListID(headers HeaderMap) string {
	for _, value := range headers["List-Id"] {
		matches := messageIDPattern.FindStringSubmatch(value)
		if len(matches) == 2 {
			id := strings.ToLower(strings.TrimSpace(matches[1]))
			if id != "" && !strings.ContainsAny(id, "<>\r\n\t ") {
				return id
			}
		}
	}
	return ""
}

func normalizeDate(headers HeaderMap) (time.Time, string, []ParserWarning) {
	values := headers["Date"]
	var warnings []ParserWarning
	for _, value := range values {
		original := unfoldHeaderValue(value)
		parsed, err := mail.ParseDate(original)
		if err == nil {
			return parsed.UTC(), original, warnings
		}
		warnings = append(warnings, headerWarning("malformed_date", "Date header could not be parsed", "Date"))
	}
	return time.Time{}, firstHeaderValue(headers, "Date"), warnings
}

func normalizeAddresses(headers HeaderMap, name string) ([]Address, []ParserWarning) {
	canonicalName := textproto.CanonicalMIMEHeaderKey(name)
	var normalized []Address
	var warnings []ParserWarning
	for _, value := range headers[canonicalName] {
		addresses, err := mail.ParseAddressList(value)
		if err != nil {
			warnings = append(warnings, headerWarning("malformed_address", "address header value could not be parsed", canonicalName))
			continue
		}
		for _, address := range addresses {
			mailbox := strings.TrimSpace(address.Address)
			if mailbox == "" {
				continue
			}
			normalized = append(normalized, Address{
				Name:       unfoldHeaderValue(address.Name),
				Address:    mailbox,
				Normalized: strings.ToLower(mailbox),
			})
		}
	}
	return normalized, warnings
}

func normalizeSecurity(headers HeaderMap) SecuritySignals {
	results := append([]string(nil), headers["Authentication-Results"]...)
	results = append(results, headers["Arc-Authentication-Results"]...)
	security := SecuritySignals{AuthenticationResults: results}
	for _, value := range results {
		for _, match := range authResultPattern.FindAllStringSubmatch(value, -1) {
			setSecurityResult(&security, strings.ToLower(match[1]), strings.ToLower(match[2]))
		}
		if security.ARC == "" {
			if match := arcCVPattern.FindStringSubmatch(value); len(match) == 2 {
				security.ARC = strings.ToLower(match[1])
			}
		}
	}
	if security.SPF == "" {
		for _, value := range headers["Received-Spf"] {
			if fields := strings.Fields(value); len(fields) != 0 {
				security.SPF = strings.ToLower(strings.Trim(fields[0], ";"))
				break
			}
		}
	}
	return security
}

func setSecurityResult(security *SecuritySignals, method, result string) {
	switch method {
	case "spf":
		if security.SPF == "" {
			security.SPF = result
		}
	case "dkim":
		if security.DKIM == "" {
			security.DKIM = result
		}
	case "dmarc":
		if security.DMARC == "" {
			security.DMARC = result
		}
	case "arc":
		if security.ARC == "" {
			security.ARC = result
		}
	case "tls":
		security.TLS = security.TLS || result == "pass" || result == "yes"
	}
}

func headerWarning(code, message, header string) ParserWarning {
	category := WarningCategory("")
	if strings.HasPrefix(code, "malformed_") {
		category = WarningMalformedHeader
	}
	return ParserWarning{Category: category, Code: code, Message: message, Header: header}
}
