package mail

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDocumentPreservesNormalizedMessageData(t *testing.T) {
	messageDate := time.Date(2026, time.June, 9, 18, 45, 0, 0, time.UTC)
	document := Document{
		ID:       "mail:source-1:account-1:inbox:provider-message-1",
		SourceID: "source-1",
		Ref: MessageRef{
			Provider:          "imap",
			AccountID:         "account-1",
			Mailbox:           Mailbox{ID: "inbox", Name: "INBOX"},
			UID:               42,
			UIDValidity:       7,
			ProviderMessageID: "provider-message-1",
			ProviderThreadID:  "provider-thread-1",
			InternalDate:      messageDate.Add(time.Minute),
			Flags:             []string{"\\Seen"},
		},
		MessageID: "<message@example.test>",
		ThreadID:  "thread-1",
		Date:      messageDate,
		From: []Address{{
			Name:       "Example Sender",
			Address:    "Sender@Example.Test",
			Normalized: "sender@example.test",
		}},
		To:      []Address{{Address: "recipient@example.test", Normalized: "recipient@example.test"}},
		Subject: "Normalized message",
		Headers: HeaderSet{
			MessageID:    "<message@example.test>",
			InReplyTo:    "<parent@example.test>",
			References:   []string{"<root@example.test>", "<parent@example.test>"},
			ListID:       "Example List <list.example.test>",
			OriginalDate: "Tue, 9 Jun 2026 18:45:00 +0000",
			Values: HeaderMap{
				"Subject":          {"Normalized message"},
				"X-Repeated-Field": {"first", "second"},
			},
			Raw: HeaderMap{
				"Subject": {"=?UTF-8?Q?Normalized_message?="},
			},
		},
		TextBody:      "Plain body with https://example.test.",
		HTMLBody:      `<p>HTML body with <a href="https://example.test">example</a>.</p>`,
		ExtractedText: "Plain body with example.",
		Links: []Link{{
			URL:            "https://example.test",
			Text:           "example",
			Title:          "Example",
			Source:         "html",
			Classification: LinkNormal,
		}},
		Attachments: []Attachment{{
			ID:                "attachment-1",
			PartID:            "2.1",
			Filename:          "report.pdf",
			MediaType:         "application/pdf",
			DetectedMediaType: "application/pdf",
			Disposition:       "attachment",
			ContentID:         "report@example.test",
			TransferEncoding:  "base64",
			Size:              2048,
			SHA256:            strings.Repeat("a", 64),
			ExtractedText:     "Quarterly report",
			Truncated:         true,
			Content:           io.NopCloser(strings.NewReader("secret attachment bytes")),
		}},
		Security: SecuritySignals{
			SPF:                   "pass",
			DKIM:                  "pass",
			DMARC:                 "pass",
			ARC:                   "none",
			TLS:                   true,
			AuthenticationResults: []string{"mx.example.test; spf=pass; dkim=pass; dmarc=pass"},
		},
		Warnings: []ParserWarning{{
			Code:    "part_truncated",
			Message: "attachment exceeded extraction limit",
			PartID:  "2.1",
			Offset:  2048,
		}},
	}

	jsonData, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal Document as JSON: %v", err)
	}
	serialized := string(jsonData)
	for _, value := range []string{
		`"source_id":"source-1"`,
		`"provider_message_id":"provider-message-1"`,
		`"normalized":"sender@example.test"`,
		`"text_body":"Plain body`,
		`"html_body":"\u003cp\u003eHTML body`,
		`"extracted_text":"Plain body with example.`,
		`"url":"https://example.test"`,
		`"classification":"normal"`,
		`"detected_media_type":"application/pdf"`,
		`"transfer_encoding":"base64"`,
		`"sha256":"` + strings.Repeat("a", 64) + `"`,
		`"values":{"Subject":["Normalized message"],"X-Repeated-Field":["first","second"]}`,
		`"raw":{"Subject":["=?UTF-8?Q?Normalized_message?="]}`,
		`"spf":"pass"`,
		`"code":"part_truncated"`,
		`"part_id":"2.1"`,
	} {
		if !strings.Contains(serialized, value) {
			t.Errorf("JSON Document does not preserve %s: %s", value, serialized)
		}
	}
	if strings.Contains(serialized, "secret attachment bytes") || strings.Contains(serialized, `"Content"`) {
		t.Fatalf("JSON Document exposed attachment content: %s", serialized)
	}

	yamlData, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal Document as YAML: %v", err)
	}
	yamlText := string(yamlData)
	for _, value := range []string{
		"source_id: source-1",
		"provider_message_id: provider-message-1",
		"normalized: sender@example.test",
		"text_body:",
		"html_body:",
		"extracted_text: Plain body with example.",
		"url: https://example.test",
		"classification: normal",
		"detected_media_type: application/pdf",
		"transfer_encoding: base64",
		"spf: pass",
		"code: part_truncated",
	} {
		if !strings.Contains(yamlText, value) {
			t.Errorf("YAML Document does not preserve %q: %s", value, yamlText)
		}
	}
	if strings.Contains(yamlText, "secret attachment bytes") || strings.Contains(yamlText, "content:") {
		t.Fatalf("YAML Document exposed attachment content: %s", yamlText)
	}
}

func TestNormalizedDocumentTypesHaveJSONAndYAMLTags(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Document{}),
		reflect.TypeOf(DocumentIdentity{}),
		reflect.TypeOf(ChildDocumentDescriptor{}),
		reflect.TypeOf(Address{}),
		reflect.TypeOf(Attachment{}),
		reflect.TypeOf(Link{}),
		reflect.TypeOf(HeaderSet{}),
		reflect.TypeOf(SecuritySignals{}),
		reflect.TypeOf(ParserWarning{}),
	}

	for _, modelType := range types {
		for i := 0; i < modelType.NumField(); i++ {
			field := modelType.Field(i)
			if field.Tag.Get("json") == "" {
				t.Errorf("%s.%s is missing a JSON tag", modelType.Name(), field.Name)
			}
			if field.Tag.Get("yaml") == "" {
				t.Errorf("%s.%s is missing a YAML tag", modelType.Name(), field.Name)
			}
		}
	}
}
