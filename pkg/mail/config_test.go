package mail

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSourceConfigSerializesToJSONAndYAML(t *testing.T) {
	config := SourceConfig{
		Connector: ConnectorConfig{
			Provider: "imap",
			Endpoint: "imaps://mail.example.com:993",
			Extensions: map[string]any{
				"capability": "idle",
			},
		},
		Auth: AuthConfig{
			Method:        "secret_ref",
			CredentialRef: "secret/mail-archive",
		},
		Mailboxes: MailboxConfig{Include: []string{"INBOX"}},
		Crawl: CrawlConfig{
			Mode:      ModeListen,
			BatchSize: 100,
			Limits:    Limits{MaxMessageBytes: 25 << 20},
		},
		Extraction: ExtractionConfig{
			Links:       LinkPolicy{Extract: true, AllowedSchemes: []string{"https"}},
			Attachments: AttachmentPolicy{Include: true, IncludeInline: true},
		},
		Listener:       ListenerConfig{Enabled: true, CoalesceWindow: time.Second},
		Reconciliation: ReconciliationConfig{PollInterval: 5 * time.Minute, PageSize: 100},
	}

	jsonData, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal JSON source config: %v", err)
	}
	for _, field := range []string{`"connector"`, `"credential_ref"`, `"mailboxes"`, `"reconciliation"`, `"extensions"`} {
		if !strings.Contains(string(jsonData), field) {
			t.Errorf("JSON source config does not contain %s: %s", field, jsonData)
		}
	}

	yamlData, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("marshal YAML source config: %v", err)
	}
	for _, field := range []string{"connector:", "credential_ref:", "mailboxes:", "reconciliation:", "extensions:"} {
		if !strings.Contains(string(yamlData), field) {
			t.Errorf("YAML source config does not contain %q: %s", field, yamlData)
		}
	}
}

func TestConfigTypesHaveJSONAndYAMLTags(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(SourceConfig{}),
		reflect.TypeOf(ConnectorConfig{}),
		reflect.TypeOf(AuthConfig{}),
		reflect.TypeOf(MailboxConfig{}),
		reflect.TypeOf(CrawlConfig{}),
		reflect.TypeOf(ExtractionConfig{}),
		reflect.TypeOf(LinkPolicy{}),
		reflect.TypeOf(AttachmentPolicy{}),
		reflect.TypeOf(ListenerConfig{}),
		reflect.TypeOf(ReconciliationConfig{}),
	}

	for _, configType := range types {
		for i := 0; i < configType.NumField(); i++ {
			field := configType.Field(i)
			if field.Tag.Get("json") == "" {
				t.Errorf("%s.%s is missing a JSON tag", configType.Name(), field.Name)
			}
			if field.Tag.Get("yaml") == "" {
				t.Errorf("%s.%s is missing a YAML tag", configType.Name(), field.Name)
			}
		}
	}
}
