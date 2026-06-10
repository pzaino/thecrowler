package mail

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClassifyLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		url    string
		wanted LinkClass
	}{
		{name: "normal HTTPS", url: "https://example.test/articles/42?utm_source=newsletter", wanted: LinkNormal},
		{name: "normal campaign mentioning unsubscribe", url: "https://example.test/article?utm_campaign=unsubscribe-tips", wanted: LinkNormal},
		{name: "normal relative", url: "../account/details?tab=profile", wanted: LinkNormal},
		{name: "normal query relative", url: "?page=2", wanted: LinkNormal},
		{name: "normal fragment relative", url: "#section", wanted: LinkNormal},
		{name: "normal protocol relative", url: "//cdn.example.test/document", wanted: LinkNormal},
		{name: "tracking click path", url: "https://links.example.test/track/click/abc123", wanted: LinkTracking},
		{name: "tracking redirect query", url: "https://example.test/out?redirect_url=https%3A%2F%2Fdestination.test", wanted: LinkTracking},
		{name: "tracking host casing", url: "HTTPS://TRACK.Example.Test/o/abc", wanted: LinkTracking},
		{name: "tracking pixel beats image", url: "https://example.test/open-pixel.gif", wanted: LinkTracking},
		{name: "unsubscribe path", url: "https://example.test/email/unsubscribe?token=abc", wanted: LinkUnsubscribe},
		{name: "unsubscribe mixed casing", url: "https://example.test/Email/UnSubscribe?id=42", wanted: LinkUnsubscribe},
		{name: "unsubscribe encoded redirect destination", url: "https://example.test/click?target=https%3A%2F%2Faccount.test%2Fopt-out", wanted: LinkUnsubscribe},
		{name: "auth email verification", url: "https://example.test/account/verify-email?token=abc", wanted: LinkAuthAction},
		{name: "auth short verification with token", url: "https://example.test/verify?token=abc", wanted: LinkAuthAction},
		{name: "normal verification article without token", url: "https://example.test/articles/verify", wanted: LinkNormal},
		{name: "auth password reset", url: "https://example.test/RESET_PASSWORD/abc", wanted: LinkAuthAction},
		{name: "calendar ICS", url: "https://example.test/events/meeting.ICS?download=1", wanted: LinkCalendar},
		{name: "calendar render action", url: "https://example.test/calendar/render?action=TEMPLATE", wanted: LinkCalendar},
		{name: "calendar webcal", url: "WEBCAL://calendar.example.test/team", wanted: LinkCalendar},
		{name: "mailto mixed casing", url: "MailTo:Person@Example.Test?subject=Hello", wanted: LinkMailto},
		{name: "CID mixed casing", url: "CID:logo@example.test", wanted: LinkCID},
		{name: "remote PNG image", url: "https://images.example.test/banner.PNG?width=600", wanted: LinkRemoteImage},
		{name: "remote image format query", url: "https://cdn.example.test/asset?id=4&format=webp", wanted: LinkRemoteImage},
		{name: "remote image path", url: "//cdn.example.test/images/banner?id=4", wanted: LinkRemoteImage},
		{name: "ambiguous unsubscribe image", url: "https://example.test/unsubscribe/button.png", wanted: LinkUnsubscribe},
		{name: "ambiguous auth tracking redirect", url: "https://example.test/redirect?target=https%3A%2F%2Fid.test%2Fmagic-link%2Fabc", wanted: LinkAuthAction},
		{name: "malformed percent escape", url: "https://example.test/%zz", wanted: LinkUnknown},
		{name: "malformed HTTP without host", url: "https:///missing-host", wanted: LinkUnknown},
		{name: "malformed whitespace", url: "https://example.test/not allowed", wanted: LinkUnknown},
		{name: "unsupported JavaScript", url: "JaVaScRiPt:alert(1)", wanted: LinkUnknown},
		{name: "unsupported data URI", url: "data:image/png;base64,AAAA", wanted: LinkUnknown},
		{name: "malformed mailto address", url: "mailto:not-an-address", wanted: LinkUnknown},
		{name: "empty mailto", url: "mailto:", wanted: LinkUnknown},
		{name: "empty CID", url: "cid:", wanted: LinkUnknown},
		{name: "fragment only", url: "#", wanted: LinkUnknown},
		{name: "empty", url: "  ", wanted: LinkUnknown},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyLink(test.url); got != test.wanted {
				t.Errorf("ClassifyLink(%q) = %q, want %q", test.url, got, test.wanted)
			}
		})
	}
}

func TestClassifyLinkDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Redirect(response, request, "https://destination.example.test/unsubscribe", http.StatusFound)
	}))
	defer server.Close()

	if got := ClassifyLink(server.URL + "/redirect?target=https%3A%2F%2Fdestination.example.test%2Farticle"); got != LinkTracking {
		t.Errorf("ClassifyLink(redirect) = %q, want %q", got, LinkTracking)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests made while classifying redirect = %d, want 0", got)
	}
}

func TestLinkClassValuesAreStable(t *testing.T) {
	t.Parallel()

	classes := []LinkClass{
		LinkNormal,
		LinkTracking,
		LinkUnsubscribe,
		LinkAuthAction,
		LinkCalendar,
		LinkMailto,
		LinkCID,
		LinkRemoteImage,
		LinkUnknown,
	}
	wanted := []string{
		"normal",
		"tracking",
		"unsubscribe",
		"auth_action",
		"calendar",
		"mailto",
		"cid",
		"remote_image",
		"unknown",
	}
	for index, class := range classes {
		if string(class) != wanted[index] {
			t.Errorf("class %d = %q, want %q", index, class, wanted[index])
		}
	}
}

func TestDocumentExtractionClassifiesLinks(t *testing.T) {
	t.Parallel()

	document, err := documentFromParsedMessage("source", ParsedMessage{HTMLBody: `
		<a href="https://example.test/article">Article</a>
		<a href="https://example.test/click?target=https%3A%2F%2Fdestination.test">Tracked</a>
		<a href="mailto:person@example.test">Email</a>
		<a href="cid:logo@example.test">Inline image</a>
		<a href="https://cdn.example.test/logo.png">Remote image</a>
		<a href="javascript:alert(1)">Unsupported</a>
	`}, ExtractionConfig{})
	if err != nil {
		t.Fatalf("documentFromParsedMessage() error = %v", err)
	}

	wanted := []LinkClass{LinkNormal, LinkTracking, LinkMailto, LinkCID, LinkRemoteImage, LinkUnknown}
	if len(document.Links) != len(wanted) {
		t.Fatalf("link count = %d, want %d: %#v", len(document.Links), len(wanted), document.Links)
	}
	for index, link := range document.Links {
		if link.Classification != wanted[index] {
			t.Errorf("link %d (%q) classification = %q, want %q", index, link.URL, link.Classification, wanted[index])
		}
	}
}
