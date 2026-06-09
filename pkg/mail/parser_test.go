package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
