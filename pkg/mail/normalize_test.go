package mail

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProcessorNormalizesEncodedDuplicatedAndFoldedHeaders(t *testing.T) {
	t.Parallel()

	message := strings.Join([]string{
		"Message-ID: not-a-message-id",
		"Message-ID: <Local.Part@EXAMPLE.TEST>",
		"Subject: =?UTF-8?Q?Ol=C3=A1?=  mundo",
		"Subject: ignored duplicate",
		"From: =?UTF-8?Q?Jos=C3=A9_Example?= <Sender@Example.Test>",
		"To: First <FIRST@Example.Test>",
		"To: Second <second@example.test>",
		"Cc: =?UTF-8?Q?Equipa?= <TEAM@Example.Test>",
		"Reply-To: Replies <REPLY@Example.Test>",
		"Date: definitely not a date",
		"Date: Tue, 9 Jun 2026 20:45:00 +0200",
		"In-Reply-To: commentary <Parent@EXAMPLE.TEST>",
		"References: <Root@EXAMPLE.TEST>",
		"\t<Parent@EXAMPLE.TEST> <Root@EXAMPLE.TEST>",
		"List-ID: Example Announcements <NEWS.EXAMPLE.TEST>",
		"Authentication-Results: mx.example.test;",
		"\tspf=pass smtp.mailfrom=example.test; dkim=fail; dmarc=pass; smtp.tls=pass",
		"ARC-Authentication-Results: i=1; mx.example.test; arc=pass",
		"DKIM-Signature: v=1; a=rsa-sha256; b=super-secret-signature; bh=body-hash",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Body.",
	}, "\r\n")

	document, err := NewProcessor("source-1").Process(context.Background(), RawMessage{
		RFC822: io.NopCloser(strings.NewReader(message)),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if document.MessageID != "<Local.Part@example.test>" {
		t.Errorf("MessageID = %q", document.MessageID)
	}
	if document.Subject != "Olá mundo" {
		t.Errorf("Subject = %q", document.Subject)
	}
	if !document.Date.Equal(time.Date(2026, time.June, 9, 18, 45, 0, 0, time.UTC)) || document.Date.Location() != time.UTC {
		t.Errorf("Date = %v, want normalized UTC instant", document.Date)
	}
	if len(document.From) != 1 || document.From[0].Name != "José Example" || document.From[0].Normalized != "sender@example.test" {
		t.Errorf("From = %#v", document.From)
	}
	if len(document.To) != 2 || document.To[0].Normalized != "first@example.test" || document.To[1].Normalized != "second@example.test" {
		t.Errorf("To = %#v", document.To)
	}
	if len(document.CC) != 1 || document.CC[0].Normalized != "team@example.test" {
		t.Errorf("CC = %#v", document.CC)
	}
	if len(document.ReplyTo) != 1 || document.ReplyTo[0].Normalized != "reply@example.test" {
		t.Errorf("ReplyTo = %#v", document.ReplyTo)
	}
	if document.Headers.InReplyTo != "<Parent@example.test>" {
		t.Errorf("InReplyTo = %q", document.Headers.InReplyTo)
	}
	wantReferences := []string{"<Root@example.test>", "<Parent@example.test>"}
	if strings.Join(document.Headers.References, ",") != strings.Join(wantReferences, ",") {
		t.Errorf("References = %#v, want %#v", document.Headers.References, wantReferences)
	}
	if document.Headers.ListID != "news.example.test" {
		t.Errorf("ListID = %q", document.Headers.ListID)
	}
	if document.Headers.OriginalDate != "Tue, 9 Jun 2026 20:45:00 +0200" {
		t.Errorf("OriginalDate = %q", document.Headers.OriginalDate)
	}
	if got := document.Headers.Raw["Subject"][0]; got != "=?UTF-8?Q?Ol=C3=A1?=  mundo" {
		t.Errorf("raw Subject = %q", got)
	}
	if got := document.Headers.Values["Subject"][0]; got != "Olá  mundo" {
		t.Errorf("decoded Subject = %q", got)
	}
	if got := document.Headers.Raw["Dkim-Signature"][0]; strings.Contains(got, "super-secret-signature") || !strings.Contains(got, "b=[redacted]") {
		t.Errorf("raw DKIM signature was not redacted: %q", got)
	}
	if document.Security.SPF != "pass" || document.Security.DKIM != "fail" || document.Security.DMARC != "pass" || document.Security.ARC != "pass" || !document.Security.TLS {
		t.Errorf("Security = %#v", document.Security)
	}
	if len(document.Security.AuthenticationResults) != 2 {
		t.Errorf("AuthenticationResults = %#v", document.Security.AuthenticationResults)
	}
	if !hasWarning(document.Warnings, "malformed_date", "Date") {
		t.Errorf("Warnings = %#v, want malformed Date warning", document.Warnings)
	}
}

func TestProcessorHandlesMissingAndMalformedHeaders(t *testing.T) {
	t.Parallel()

	message := strings.Join([]string{
		"From: broken address",
		"To: Valid <valid@example.test>",
		"To: also broken",
		"Date: not-a-date",
		"Message-ID: broken",
		"In-Reply-To: broken",
		"References: broken references",
		"List-ID: broken list id",
		"Received-SPF: SoftFail (policy reason)",
		"Content-Type: text/plain",
		"",
		"Body.",
	}, "\r\n")

	document, err := NewProcessor("source-1").Process(context.Background(), RawMessage{
		RFC822: io.NopCloser(strings.NewReader(message)),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if document.MessageID != "" || document.Subject != "" || !document.Date.IsZero() {
		t.Errorf("missing/malformed scalar fields were not empty: %#v", document)
	}
	if len(document.From) != 0 || len(document.To) != 1 || document.To[0].Normalized != "valid@example.test" {
		t.Errorf("addresses = From %#v, To %#v", document.From, document.To)
	}
	if document.Headers.InReplyTo != "" || len(document.Headers.References) != 0 || document.Headers.ListID != "" {
		t.Errorf("malformed promoted headers = %#v", document.Headers)
	}
	if document.Security.SPF != "softfail" {
		t.Errorf("SPF = %q", document.Security.SPF)
	}
	if !hasWarning(document.Warnings, "malformed_address", "From") ||
		!hasWarning(document.Warnings, "malformed_address", "To") ||
		!hasWarning(document.Warnings, "malformed_date", "Date") {
		t.Errorf("Warnings = %#v", document.Warnings)
	}
}

func TestBoundedHeadersSanitizeAndLimitRawRepresentation(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", maxRetainedHeaderValueSize+100)
	headers, warnings := boundedHeaders(map[string][]string{
		"X-Control": {"safe\x00value\r\n next"},
		"X-Large":   {oversized},
	}, true)

	if got := headers["X-Control"][0]; got != "safe�value   next" {
		t.Errorf("sanitized control header = %q", got)
	}
	if got := headers["X-Large"][0]; len(got) > maxRetainedHeaderValueSize || !strings.HasSuffix(got, "…") {
		t.Errorf("bounded large header has length %d and suffix %q", len(got), got[len(got)-3:])
	}
	if !hasWarning(warnings, "header_value_truncated", "X-Large") {
		t.Errorf("Warnings = %#v", warnings)
	}
}

func hasWarning(warnings []ParserWarning, code, header string) bool {
	for _, warning := range warnings {
		if warning.Code == code && warning.Header == header {
			return true
		}
	}
	return false
}
