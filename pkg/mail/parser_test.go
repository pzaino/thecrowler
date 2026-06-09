package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParserParsesRFC822Message(t *testing.T) {
	t.Parallel()

	const raw = "From: =?UTF-8?Q?Jos=C3=A9_Example?= <Sender@Example.Test>\r\n" +
		"To: Recipient <recipient@example.test>\r\n" +
		"Message-ID: <message@example.test>\r\n" +
		"Date: Tue, 9 Jun 2026 18:45:00 +0000\r\n" +
		"Subject: =?UTF-8?Q?Quarterly_report?=\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=outer\r\n" +
		"X-Repeated: first\r\n" +
		"X-Repeated: second\r\n" +
		"\r\n" +
		"--outer\r\n" +
		"Content-Type: multipart/alternative; boundary=alternative\r\n" +
		"\r\n" +
		"--alternative\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Hello, world!=0A\r\n" +
		"--alternative\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>Hello, <strong>world!</strong></p>\r\n" +
		"--alternative--\r\n" +
		"--outer\r\n" +
		"Content-Type: text/plain; name=report.txt\r\n" +
		"Content-Disposition: attachment; filename=report.txt\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"cmVwb3J0IGNvbnRlbnQ=\r\n" +
		"--outer--\r\n"

	message := RawMessage{
		Ref:    MessageRef{Provider: "imap", AccountID: "account-1", UID: 42},
		RFC822: io.NopCloser(strings.NewReader(raw)),
	}
	defer message.RFC822.Close()

	parsed, err := NewParser().Parse(context.Background(), message)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !reflect.DeepEqual(parsed.Ref, message.Ref) {
		t.Fatalf("Ref = %#v, want %#v", parsed.Ref, message.Ref)
	}
	if parsed.MessageID != "<message@example.test>" {
		t.Errorf("MessageID = %q", parsed.MessageID)
	}
	if parsed.Subject != "Quarterly report" {
		t.Errorf("Subject = %q", parsed.Subject)
	}
	wantDate := time.Date(2026, time.June, 9, 18, 45, 0, 0, time.UTC)
	if !parsed.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", parsed.Date, wantDate)
	}
	if len(parsed.From) != 1 || parsed.From[0].Name != "José Example" || parsed.From[0].Normalized != "sender@example.test" {
		t.Errorf("From = %#v", parsed.From)
	}
	if len(parsed.To) != 1 || parsed.To[0].Address != "recipient@example.test" {
		t.Errorf("To = %#v", parsed.To)
	}
	if parsed.TextBody != "Hello, world!\n" {
		t.Errorf("TextBody = %q", parsed.TextBody)
	}
	if parsed.HTMLBody != "<p>Hello, <strong>world!</strong></p>" {
		t.Errorf("HTMLBody = %q", parsed.HTMLBody)
	}
	if got := parsed.Headers["X-Repeated"]; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("X-Repeated headers = %#v", got)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("Attachments = %#v", parsed.Attachments)
	}

	attachment := parsed.Attachments[0]
	defer attachment.Content.Close()
	if attachment.Filename != "report.txt" || attachment.MediaType != "text/plain" || attachment.Size != 14 {
		t.Errorf("attachment metadata = %#v", attachment)
	}
	if attachment.Disposition != "attachment" || attachment.TransferEncoding != "base64" ||
		attachment.SHA256 != sha256Hex([]byte("report content")) {
		t.Errorf("attachment indexing metadata = %#v", attachment)
	}
	content, err := io.ReadAll(attachment.Content)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if string(content) != "report content" {
		t.Errorf("attachment content = %q", content)
	}
}

func TestParserDiscoversMultipartMixedAttachmentsWithoutIndexingTheirContents(t *testing.T) {
	t.Parallel()

	parsed := parseMessageFixture(t, "multipart_mixed_attachments.eml")
	if parsed.TextBody != "Index only this message body." {
		t.Errorf("TextBody = %q", parsed.TextBody)
	}
	if strings.Contains(parsed.TextBody, "attached text") || strings.Contains(parsed.TextBody, "nested secret") {
		t.Fatalf("TextBody recursively indexed attachment content: %q", parsed.TextBody)
	}
	if len(parsed.Attachments) != 3 {
		t.Fatalf("Attachments = %#v, want 3 opaque top-level attachments", parsed.Attachments)
	}

	want := []struct {
		filename         string
		mediaType        string
		disposition      string
		contentID        string
		transferEncoding string
		content          []byte
	}{
		{"report.txt", "text/plain", "attachment", "report@example.test", "base64", []byte("report content")},
		{"data.bin", "application/octet-stream", "", "", "base64", []byte{0, 1, 2, 3, 4}},
		{"bundle.mime", "multipart/mixed", "attachment", "bundle@example.test", "", nil},
	}
	for index, attachment := range parsed.Attachments {
		attachment := attachment
		t.Cleanup(func() { _ = attachment.Content.Close() })
		expected := want[index]
		if attachment.Filename != expected.filename || attachment.MediaType != expected.mediaType ||
			attachment.Disposition != expected.disposition || attachment.ContentID != expected.contentID ||
			attachment.TransferEncoding != expected.transferEncoding || attachment.Size != int64(len(expected.content)) ||
			attachment.SHA256 != sha256Hex(expected.content) {
			t.Errorf("Attachments[%d] = %#v", index, attachment)
		}
		content, err := io.ReadAll(attachment.Content)
		if err != nil {
			t.Fatalf("read Attachments[%d]: %v", index, err)
		}
		if !bytes.Equal(content, expected.content) {
			t.Errorf("Attachments[%d] content = %q, want %q", index, content, expected.content)
		}
	}
}

func TestParserRecoversMalformedMultipartMixedAttachments(t *testing.T) {
	t.Parallel()

	parsed := parseMessageFixture(t, "multipart_mixed_malformed_attachments.eml")
	if parsed.TextBody != "Readable body." {
		t.Errorf("TextBody = %q", parsed.TextBody)
	}
	if len(parsed.Attachments) != 2 {
		t.Fatalf("Attachments = %#v, want 2", parsed.Attachments)
	}

	for _, attachment := range parsed.Attachments {
		attachment := attachment
		t.Cleanup(func() { _ = attachment.Content.Close() })
	}
	first := parsed.Attachments[0]
	if first.Filename != "broken.bin" || first.ContentID != "broken@example.test" ||
		first.TransferEncoding != "base64" || first.Size != 5 || first.SHA256 != sha256Hex([]byte("Hello")) {
		t.Errorf("first malformed attachment metadata = %#v", first)
	}
	second := parsed.Attachments[1]
	if second.Filename != "notes.txt" || second.MediaType != "text/plain" || second.Disposition != "attachment" ||
		second.TransferEncoding != "quoted-printable" || second.Size == 0 || second.SHA256 == "" {
		t.Errorf("second malformed attachment metadata = %#v", second)
	}

	codes := make(map[string]bool)
	for _, warning := range parsed.Warnings {
		codes[warning.Code] = true
	}
	if !codes["malformed_base64"] {
		t.Errorf("Warnings = %#v, want malformed_base64", parsed.Warnings)
	}
}

func parseMessageFixture(t *testing.T, name string) ParsedMessage {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { _ = file.Close() })

	parsed, err := NewParser().Parse(context.Background(), RawMessage{RFC822: file})
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return parsed
}

func sha256Hex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func TestParserTraversesMultipartAlternativeBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		parts    string
		wantText string
		wantHTML string
	}{
		{
			name:     "plain only",
			parts:    alternativePart("text/plain; charset=utf-8", "Plain body."),
			wantText: "Plain body.",
		},
		{
			name:     "HTML only",
			parts:    alternativePart("text/html; charset=utf-8", "<p>HTML body.</p>"),
			wantHTML: "<p>HTML body.</p>",
		},
		{
			name: "HTML before plain",
			parts: alternativePart("text/html; charset=utf-8", "<p>HTML first.</p>") +
				alternativePart("text/plain; charset=utf-8", "Plain second."),
			wantText: "Plain second.",
			wantHTML: "<p>HTML first.</p>",
		},
		{
			name: "plain before HTML",
			parts: alternativePart("text/plain; charset=utf-8", "Plain first.") +
				alternativePart("text/html; charset=utf-8", "<p>HTML second.</p>"),
			wantText: "Plain first.",
			wantHTML: "<p>HTML second.</p>",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseAlternativeMessage(test.parts)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if parsed.TextBody != test.wantText {
				t.Errorf("TextBody = %q, want %q", parsed.TextBody, test.wantText)
			}
			if parsed.HTMLBody != test.wantHTML {
				t.Errorf("HTMLBody = %q, want %q", parsed.HTMLBody, test.wantHTML)
			}
		})
	}
}

func TestParserRecoversMultipartAlternativePartWarnings(t *testing.T) {
	t.Parallel()

	parts := alternativePart("text/plain; charset=utf-8", "Readable plain body.") +
		"--alternative\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"PHA+T0s8L3A+%%%\r\n"

	parsed, err := parseAlternativeMessage(parts)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.TextBody != "Readable plain body." {
		t.Errorf("TextBody = %q, want recovered plain body", parsed.TextBody)
	}
	if parsed.HTMLBody != "<p>OK</p>" {
		t.Errorf("HTMLBody = %q, want recovered malformed body", parsed.HTMLBody)
	}
	if len(parsed.Warnings) == 0 {
		t.Fatal("Warnings is empty, want malformed-part warning")
	}
	if warning := parsed.Warnings[0]; warning.Code != "malformed_base64" || warning.PartID == "" {
		t.Errorf("Warnings[0] = %#v, want malformed_base64 with part ID", warning)
	}
}

func alternativePart(contentType, body string) string {
	return "--alternative\r\n" +
		"Content-Type: " + contentType + "\r\n" +
		"\r\n" +
		body + "\r\n"
}

func parseAlternativeMessage(parts string) (ParsedMessage, error) {
	raw := "From: sender@example.test\r\n" +
		"To: recipient@example.test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=alternative\r\n" +
		"\r\n" +
		parts +
		"--alternative--\r\n"

	return NewParser().Parse(context.Background(), RawMessage{
		RFC822: io.NopCloser(strings.NewReader(raw)),
	})
}

func TestParserTraversesMultipartRelatedAndAssociatesContentIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		html       string
		resources  string
		wantIDs    []string
		wantInline []bool
	}{
		{
			name:       "matched normalized content ID",
			html:       `<html><body><img src="CID:Image%40Example.Test"><img src="https://remote.example.test/tracker.png"></body></html>`,
			resources:  relatedResource("image/png", "<IMAGE@example.test>", "first image"),
			wantIDs:    []string{"image@example.test"},
			wantInline: []bool{true},
		},
		{
			name:       "unmatched content ID",
			html:       `<html><body><img src="cid:other@example.test"></body></html>`,
			resources:  relatedResource("image/png", "<image@example.test>", "unmatched image"),
			wantIDs:    []string{"image@example.test"},
			wantInline: []bool{false},
		},
		{
			name:       "malformed content IDs",
			html:       `<html><body><img src="cid:%zz"><img src="cid:bad id"></body></html>`,
			resources:  relatedResource("image/png", "<bad id>", "malformed image"),
			wantIDs:    []string{""},
			wantInline: []bool{false},
		},
		{
			name: "duplicate content IDs use first resource",
			html: `<html><body><img src="cid:duplicate@example.test"></body></html>`,
			resources: relatedResource("image/png", "<DUPLICATE@example.test>", "first duplicate") +
				relatedResource("image/jpeg", "duplicate@example.test", "second duplicate"),
			wantIDs:    []string{"duplicate@example.test", "duplicate@example.test"},
			wantInline: []bool{true, false},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseRelatedMessage(test.html, test.resources)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if parsed.HTMLBody != test.html {
				t.Errorf("HTMLBody = %q, want %q", parsed.HTMLBody, test.html)
			}
			if len(parsed.Attachments) != len(test.wantIDs) {
				t.Fatalf("Attachments = %#v, want %d", parsed.Attachments, len(test.wantIDs))
			}
			for index, attachment := range parsed.Attachments {
				attachment := attachment
				t.Cleanup(func() { _ = attachment.Content.Close() })
				if attachment.ContentID != test.wantIDs[index] || attachment.ID != test.wantIDs[index] {
					t.Errorf("Attachments[%d] IDs = (%q, %q), want %q", index, attachment.ID, attachment.ContentID, test.wantIDs[index])
				}
				if attachment.Inline != test.wantInline[index] {
					t.Errorf("Attachments[%d].Inline = %t, want %t", index, attachment.Inline, test.wantInline[index])
				}
			}
		})
	}
}

func relatedResource(contentType, contentID, body string) string {
	return "--related\r\n" +
		"Content-Type: " + contentType + "\r\n" +
		"Content-ID: " + contentID + "\r\n" +
		"\r\n" +
		body + "\r\n"
}

func parseRelatedMessage(htmlBody, resources string) (ParsedMessage, error) {
	raw := "From: sender@example.test\r\n" +
		"To: recipient@example.test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=related; start=\"<root@example.test>\"\r\n" +
		"\r\n" +
		resources +
		"--related\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-ID: <ROOT@example.test>\r\n" +
		"\r\n" +
		htmlBody + "\r\n" +
		"--related--\r\n"

	return NewParser().Parse(context.Background(), RawMessage{
		RFC822: io.NopCloser(strings.NewReader(raw)),
	})
}

func TestParserRecoveryWarningCategories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		raw          string
		parser       Parser
		category     WarningCategory
		wantText     string
		wantHTML     string
		wantPartCode string
	}{
		{
			name:         "unknown charset",
			raw:          "Content-Type: text/plain; charset=x-unknown\r\n\r\nReadable bytes.",
			parser:       NewParser(),
			category:     WarningUnknownCharset,
			wantText:     "Readable bytes.",
			wantPartCode: "character_set_conversion",
		},
		{
			name: "malformed child header",
			raw: "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=m\r\n\r\n" +
				"--m\r\nBroken Header\r\n\r\nLost part.\r\n" +
				"--m\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nRecovered body.\r\n--m--\r\n",
			parser:       NewParser(),
			category:     WarningMalformedHeader,
			wantText:     "Recovered body.",
			wantPartCode: "malformed_child_part",
		},
		{
			name: "unsupported part",
			raw: "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=m\r\n\r\n" +
				"--m\r\nContent-Type: text/plain\r\n\r\nReadable body.\r\n" +
				"--m\r\nContent-Type: application/x-custom\r\n\r\nopaque\r\n--m--\r\n",
			parser:       NewParser(),
			category:     WarningUnsupportedPart,
			wantText:     "Readable body.",
			wantPartCode: "unsupported_media_type",
		},
		{
			name: "oversized part",
			raw: "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=m\r\n\r\n" +
				"--m\r\nContent-Type: text/plain\r\n\r\nThis body is too large.\r\n" +
				"--m\r\nContent-Type: text/html\r\n\r\n<p>OK</p>\r\n--m--\r\n",
			parser:       NewParser(WithMaxPartBytes(12)),
			category:     WarningOversizedPart,
			wantHTML:     "<p>OK</p>",
			wantPartCode: "part_too_large",
		},
		{
			name: "signed content",
			raw: "MIME-Version: 1.0\r\nContent-Type: multipart/signed; boundary=s; protocol=application/pgp-signature\r\n\r\n" +
				"--s\r\nContent-Type: text/plain\r\n\r\nSigned readable body.\r\n" +
				"--s\r\nContent-Type: application/pgp-signature\r\n\r\nsignature\r\n--s--\r\n",
			parser:       NewParser(),
			category:     WarningProtectedContent,
			wantText:     "Signed readable body.",
			wantPartCode: "signed_content",
		},
		{
			name: "encrypted content",
			raw: "From: sender@example.test\r\nSubject: encrypted\r\nMIME-Version: 1.0\r\n" +
				"Content-Type: application/pkcs7-mime; smime-type=enveloped-data\r\n\r\nciphertext",
			parser:       NewParser(),
			category:     WarningProtectedContent,
			wantPartCode: "encrypted_content",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := test.parser.Parse(context.Background(), RawMessage{RFC822: io.NopCloser(strings.NewReader(test.raw))})
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if parsed.TextBody != test.wantText || parsed.HTMLBody != test.wantHTML {
				t.Errorf("bodies = (%q, %q), want (%q, %q)", parsed.TextBody, parsed.HTMLBody, test.wantText, test.wantHTML)
			}
			if !hasParserWarning(parsed.Warnings, test.category, test.wantPartCode) {
				t.Errorf("Warnings = %#v, want category %q code %q", parsed.Warnings, test.category, test.wantPartCode)
			}
		})
	}
}

func TestParserOmitsOversizedAttachmentContent(t *testing.T) {
	t.Parallel()

	raw := "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=m\r\n\r\n" +
		"--m\r\nContent-Type: text/plain\r\n\r\nOK\r\n" +
		"--m\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=large.bin\r\n\r\n0123456789\r\n--m--\r\n"
	parsed, err := NewParser(WithMaxPartBytes(5)).Parse(context.Background(), RawMessage{RFC822: io.NopCloser(strings.NewReader(raw))})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("Attachments = %#v, want one", parsed.Attachments)
	}
	attachment := parsed.Attachments[0]
	if !attachment.Truncated || attachment.Content != nil || attachment.Size != 10 || attachment.SHA256 != "" {
		t.Errorf("oversized attachment = %#v", attachment)
	}
	if !hasParserWarning(parsed.Warnings, WarningOversizedPart, "part_too_large") {
		t.Errorf("Warnings = %#v, want oversized warning", parsed.Warnings)
	}
}

func TestParserRejectsTrulyFatalRootHeader(t *testing.T) {
	t.Parallel()

	_, err := NewParser().Parse(context.Background(), RawMessage{
		RFC822: io.NopCloser(strings.NewReader("Broken Header\r\n\r\nbody")),
	})
	if err == nil {
		t.Fatal("Parse() error = nil, want fatal malformed input")
	}
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorMalformed {
		t.Fatalf("Parse() error = %#v, want ErrorMalformed", err)
	}
}

func hasParserWarning(warnings []ParserWarning, category WarningCategory, code string) bool {
	for _, warning := range warnings {
		if warning.Category == category && warning.Code == code {
			return true
		}
	}
	return false
}
