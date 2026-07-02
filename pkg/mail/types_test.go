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

func TestMessageRefSerializesProviderNeutralMetadata(t *testing.T) {
	internalDate := time.Date(2026, time.June, 9, 12, 30, 0, 0, time.UTC)
	ref := MessageRef{
		Provider:          "imap",
		AccountID:         "account-123",
		Mailbox:           Mailbox{ID: "mailbox-456", Name: "INBOX"},
		UID:               789,
		UIDValidity:       321,
		ProviderMessageID: "message-abc",
		ProviderThreadID:  "thread-def",
		Version:           "version-2",
		InternalDate:      internalDate,
		Flags:             []string{"\\Seen", "important"},
		Size:              4096,
		Headers: HeaderMap{
			"Message-Id": {"<message@example.com>"},
			"Subject":    {"Example"},
		},
	}

	jsonData, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal MessageRef as JSON: %v", err)
	}
	for _, field := range []string{
		`"account_id":"account-123"`,
		`"mailbox":{"id":"mailbox-456","name":"INBOX"}`,
		`"uid":789`,
		`"uid_validity":321`,
		`"provider_message_id":"message-abc"`,
		`"provider_thread_id":"thread-def"`,
		`"internal_date":"2026-06-09T12:30:00Z"`,
		`"flags":["\\Seen","important"]`,
		`"size":4096`,
		`"headers"`,
	} {
		if !strings.Contains(string(jsonData), field) {
			t.Errorf("JSON MessageRef does not contain %s: %s", field, jsonData)
		}
	}

	yamlData, err := yaml.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal MessageRef as YAML: %v", err)
	}
	for _, field := range []string{
		"account_id: account-123",
		"mailbox:",
		"uid: 789",
		"uid_validity: 321",
		"provider_message_id: message-abc",
		"provider_thread_id: thread-def",
		"internal_date:",
		"flags:",
		"size: 4096",
		"headers:",
	} {
		if !strings.Contains(string(yamlData), field) {
			t.Errorf("YAML MessageRef does not contain %q: %s", field, yamlData)
		}
	}
}

func TestRawMessageDoesNotSerializeRFC822(t *testing.T) {
	raw := RawMessage{
		Ref:    MessageRef{AccountID: "account-123", Mailbox: Mailbox{ID: "inbox"}},
		RFC822: io.NopCloser(strings.NewReader("secret message content")),
	}

	jsonData, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal RawMessage as JSON: %v", err)
	}
	if strings.Contains(string(jsonData), "secret message content") || strings.Contains(string(jsonData), "RFC822") {
		t.Fatalf("JSON RawMessage exposed RFC822 stream: %s", jsonData)
	}

	yamlData, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal RawMessage as YAML: %v", err)
	}
	if strings.Contains(string(yamlData), "secret message content") || strings.Contains(string(yamlData), "rfc822") {
		t.Fatalf("YAML RawMessage exposed RFC822 stream: %s", yamlData)
	}
}

func TestMailTransportTypesHaveJSONAndYAMLTags(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Mailbox{}),
		reflect.TypeOf(MessageRef{}),
		reflect.TypeOf(RawMessage{}),
		reflect.TypeOf(Cursor{}),
		reflect.TypeOf(FetchOptions{}),
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

func TestCursorAndFetchOptionsSerializeWithoutConnectorTypes(t *testing.T) {
	value := struct {
		Cursor Cursor       `json:"cursor"`
		Fetch  FetchOptions `json:"fetch"`
	}{
		Cursor: Cursor{Token: "opaque-page-token", HistoryID: 18446744073709551615, UID: 42, UIDValidity: 7},
		Fetch: FetchOptions{
			Headers:     []string{"Message-ID", "Subject"},
			IncludeBody: true,
			MaxBytes:    1 << 20,
		},
	}

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal cursor and fetch options: %v", err)
	}
	for _, field := range []string{
		`"token":"opaque-page-token"`,
		`"history_id":18446744073709551615`,
		`"uid":42`,
		`"uid_validity":7`,
		`"headers":["Message-ID","Subject"]`,
		`"include_body":true`,
		`"max_bytes":1048576`,
	} {
		if !strings.Contains(string(data), field) {
			t.Errorf("serialized transport models do not contain %s: %s", field, data)
		}
	}
}
