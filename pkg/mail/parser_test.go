package mail

import (
	"context"
	"io"
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
	content, err := io.ReadAll(attachment.Content)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if string(content) != "report content" {
		t.Errorf("attachment content = %q", content)
	}
}
