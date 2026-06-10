package mail

import (
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// LinkClass is a provider-neutral semantic classification of a URI found in a
// message. Classification is based only on the URI text and never dereferences
// the URI or follows redirects.
type LinkClass string

const (
	LinkNormal      LinkClass = "normal"
	LinkTracking    LinkClass = "tracking"
	LinkUnsubscribe LinkClass = "unsubscribe"
	LinkAuthAction  LinkClass = "auth_action"
	LinkCalendar    LinkClass = "calendar"
	LinkMailto      LinkClass = "mailto"
	LinkCID         LinkClass = "cid"
	LinkRemoteImage LinkClass = "remote_image"
	LinkUnknown     LinkClass = "unknown"
)

var imageExtensionPattern = regexp.MustCompile(`(?i)\.(?:avif|bmp|gif|ico|jpe?g|png|svg|tiff?|webp)$`)

// ClassifyLink classifies rawURI without making a network request. It accepts
// absolute HTTP(S) URLs, protocol-relative URLs, and relative references.
// Unsupported schemes, incomplete absolute URLs, and malformed escaping are
// classified as unknown.
func ClassifyLink(rawURI string) LinkClass {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" || containsUnsafeURLCharacter(rawURI) {
		return LinkUnknown
	}

	parsed, err := url.Parse(rawURI)
	if err != nil {
		return LinkUnknown
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "mailto":
		recipient := normalizedURLText(strings.SplitN(parsed.Opaque, "?", 2)[0])
		if recipient == "" || !strings.Contains(recipient, "@") {
			return LinkUnknown
		}
		return LinkMailto
	case "cid":
		if parsed.Opaque == "" {
			return LinkUnknown
		}
		return LinkCID
	case "webcal", "webcals":
		if parsed.Host == "" {
			return LinkUnknown
		}
		return LinkCalendar
	case "http", "https":
		if parsed.Host == "" {
			return LinkUnknown
		}
	case "":
		if parsed.Host == "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" {
			return LinkUnknown
		}
	case "data":
		return LinkUnknown
	default:
		return LinkUnknown
	}

	pathText := normalizedURLText(parsed.EscapedPath())
	queryText := normalizedURLText(parsed.RawQuery)

	// Action semantics take precedence over wrappers. For example, a tracking
	// redirect whose statically visible destination is an unsubscribe endpoint
	// remains an unsubscribe action; the destination is inspected but not
	// requested.
	if isUnsubscribeReference(parsed, pathText) {
		return LinkUnsubscribe
	}
	if isAuthActionReference(parsed, pathText) {
		return LinkAuthAction
	}
	if isCalendarReference(pathText, queryText) {
		return LinkCalendar
	}
	if isTrackingReference(parsed, pathText, queryText) {
		return LinkTracking
	}
	if isRemoteImageReference(pathText, queryText) {
		return LinkRemoteImage
	}

	return LinkNormal
}

func containsUnsafeURLCharacter(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '\\' {
			return true
		}
	}
	return false
}

func normalizedURLText(value string) string {
	decoded, err := url.QueryUnescape(value)
	if err == nil {
		value = decoded
	}
	return strings.ToLower(value)
}

func containsAnyToken(value string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func isUnsubscribeReference(parsed *url.URL, pathText string) bool {
	if containsAnyToken(pathText,
		"unsubscribe", "optout", "opt-out", "remove-me", "remove_me",
		"email-preferences", "email_preferences", "subscription-preferences",
	) {
		return true
	}
	return queryContainsAction(parsed, []string{"unsubscribe", "unsub", "optout", "opt-out"})
}

func isAuthActionReference(parsed *url.URL, pathText string) bool {
	if containsAnyToken(pathText,
		"verify-email", "verify_email", "email-verification", "email_verification",
		"confirm-email", "confirm_email", "activate-account", "activate_account",
		"reset-password", "reset_password", "password-reset", "password_reset",
		"magic-link", "magic_link", "account-recovery", "account_recovery",
		"/password/reset", "/account/activate", "/email/verify", "/email/confirm",
	) {
		return true
	}
	if pathHasSegment(pathText, "verify", "confirm", "activate", "reset", "login") && queryHasKey(parsed, "token", "code", "key", "signature", "secret") {
		return true
	}
	return queryContainsAction(parsed, []string{
		"verify-email", "verify_email", "confirm-email", "confirm_email",
		"activate-account", "activate_account", "reset-password", "reset_password",
		"magic-link", "magic_link", "account-recovery", "account_recovery",
	})
}

func pathHasSegment(pathText string, wanted ...string) bool {
	segments := strings.FieldsFunc(pathText, func(character rune) bool {
		return character == '/' || character == '-' || character == '_'
	})
	for _, segment := range segments {
		for _, candidate := range wanted {
			if segment == candidate {
				return true
			}
		}
	}
	return false
}

func queryHasKey(parsed *url.URL, keys ...string) bool {
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		for _, candidate := range keys {
			if strings.EqualFold(key, candidate) {
				return true
			}
		}
	}
	return false
}

func queryContainsAction(parsed *url.URL, actions []string) bool {
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		normalizedKey := strings.ToLower(key)
		for _, action := range actions {
			if normalizedKey == action {
				return true
			}
		}
		switch normalizedKey {
		case "action", "operation", "redirect", "redirect_url", "redirect_uri", "destination", "destination_url", "target", "target_url", "url", "next", "continue":
			for _, value := range values {
				normalizedValue := strings.ToLower(value)
				for _, action := range actions {
					if strings.Contains(normalizedValue, action) {
						return true
					}
				}
			}
		}
	}
	return false
}

func isCalendarReference(pathText, queryText string) bool {
	if strings.HasSuffix(strings.TrimSuffix(pathText, "/"), ".ics") {
		return true
	}
	return containsAnyToken(pathText+" "+queryText,
		"/calendar/", "/calendar", "add-to-calendar", "add_to_calendar",
		"calendar-event", "calendar_event", "event.ics", "format=ics", "output=ics",
	)
}

func isTrackingReference(parsed *url.URL, pathText, queryText string) bool {
	host := strings.ToLower(parsed.Hostname())
	if containsAnyToken(host, "click.", "clicks.", "track.", "tracker.", "tracking.") {
		return true
	}

	cleanPath := strings.Trim(path.Clean("/"+pathText), "/")
	segments := strings.FieldsFunc(cleanPath, func(character rune) bool {
		return character == '/' || character == '-' || character == '_'
	})
	for _, segment := range segments {
		switch segment {
		case "click", "clicks", "track", "tracking", "redirect", "redir":
			return true
		}
	}

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "redirect", "redirect_url", "redirect_uri", "destination", "destination_url", "target", "target_url":
			return true
		}
	}
	return containsAnyToken(pathText+" "+queryText, "tracking-pixel", "tracking_pixel", "open-pixel", "open_pixel")
}

func isRemoteImageReference(pathText, queryText string) bool {
	cleanPath := strings.TrimSuffix(pathText, "/")
	if imageExtensionPattern.MatchString(cleanPath) {
		return true
	}
	return containsAnyToken(pathText+" "+queryText,
		"/image/", "/images/", "/img/", "format=image", "format=png", "format=jpg",
		"format=jpeg", "format=gif", "format=webp", "type=image", "content-type=image", "content_type=image",
	)
}
