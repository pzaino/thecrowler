package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	mailconfig "github.com/pzaino/thecrowler/pkg/mail/config"
	"gopkg.in/yaml.v2"
)

func TestEmailSourceConfigDecodesJSONAndYAMLWithDefaults(t *testing.T) {
	t.Parallel()

	const jsonConfig = `{
		"version":"1.0",
		"format_version":"1.0",
		"source_name":"archive",
		"crawling_config":{"site":"imaps://mail.example.test","source_type":"email"},
		"email":{
			"connector":{"provider":"imap","endpoint":"imaps://mail.example.test"},
			"auth":{"credential_ref":"secret/archive"},
			"mailboxes":{"include":["Archive"]}
		}
	}`
	const yamlConfig = `
version: "1.0"
format_version: "1.0"
source_name: archive
crawling_config:
  site: imaps://mail.example.test
  source_type: email
email:
  connector:
    provider: imap
    endpoint: imaps://mail.example.test
  auth:
    credential_ref: secret/archive
  mailboxes:
    include: [Archive]
`

	for _, test := range []struct {
		name   string
		decode func([]byte, any) error
		data   string
	}{
		{name: "JSON", decode: json.Unmarshal, data: jsonConfig},
		{name: "YAML", decode: yaml.Unmarshal, data: yamlConfig},
	} {
		t.Run(test.name, func(t *testing.T) {
			var source SourceConfig
			if err := test.decode([]byte(test.data), &source); err != nil {
				t.Fatalf("decode %s source config: %v", test.name, err)
			}
			if source.Email == nil {
				t.Fatal("decoded source has no email configuration")
			}
			if source.Email.Connector.Provider != "imap" || source.Email.Connector.Endpoint != "imaps://mail.example.test" {
				t.Fatalf("unexpected connector: %#v", source.Email.Connector)
			}
			if !reflect.DeepEqual(source.Email.Mailboxes.Include, []string{"Archive"}) {
				t.Fatalf("mailboxes = %#v, want Archive", source.Email.Mailboxes.Include)
			}
			if source.Email.Connector.Timeout != 30*time.Second {
				t.Fatalf("connector timeout = %s, want default 30s", source.Email.Connector.Timeout)
			}
			if source.Email.Crawl.BatchSize != 100 || source.Email.Reconciliation.PollInterval != 5*time.Minute {
				t.Fatalf("mail defaults were not applied: %#v", source.Email.SourceConfig)
			}
			if err := source.Email.Validate(); err != nil {
				t.Fatalf("validate decoded email config: %v", err)
			}
		})
	}
}

func TestEmailSourceConfigRoundTripsJSONAndYAML(t *testing.T) {
	t.Parallel()

	email := DefaultEmailSourceConfig()
	email.Connector.Provider = "maildir"
	email.Connector.Endpoint = "maildir:///var/mail/archive"
	email.Mailboxes.Include = []string{"Archive"}
	email.Extensions = map[string]any{"owner": "records"}
	source := SourceConfig{
		Version:        "1.0",
		FormatVersion:  "1.0",
		SourceName:     "archive",
		CrawlingConfig: CrawlingConfig{Site: "maildir:///var/mail/archive", SourceType: SourceTypeEmail},
		Email:          &email,
		Custom:         map[string]any{"crawler": map[string]any{"workers": 2}},
	}

	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		{name: "JSON", marshal: json.Marshal, unmarshal: json.Unmarshal},
		{name: "YAML", marshal: yaml.Marshal, unmarshal: yaml.Unmarshal},
	} {
		t.Run(codec.name, func(t *testing.T) {
			encoded, err := codec.marshal(source)
			if err != nil {
				t.Fatalf("marshal %s: %v", codec.name, err)
			}
			var decoded SourceConfig
			if err := codec.unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("unmarshal %s: %v\n%s", codec.name, err, encoded)
			}
			if decoded.Version != source.Version || decoded.FormatVersion != source.FormatVersion ||
				decoded.SourceName != source.SourceName || !reflect.DeepEqual(decoded.CrawlingConfig, source.CrawlingConfig) {
				t.Fatalf("%s project fields changed during round trip: got %#v, want %#v", codec.name, decoded, source)
			}
			if decoded.Email == nil || !reflect.DeepEqual(*decoded.Email, *source.Email) {
				t.Fatalf("%s email config changed during round trip:\n got: %#v\nwant: %#v", codec.name, decoded.Email, source.Email)
			}
			if decoded.Custom["crawler"] == nil {
				t.Fatalf("%s round trip dropped existing custom composition", codec.name)
			}
		})
	}
}

func TestSourceConfigAcceptsLegacyMailEnvelopes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
	}{
		{name: "mail", data: `{"mail":{"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"}}}`},
		{name: "email config", data: `{"email_config":{"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"}}}`},
		{name: "mail config", data: `{"mail_config":{"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"}}}`},
		{name: "custom email", data: `{"custom":{"email":{"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"}}}}`},
		{name: "custom mail", data: `{"custom":{"mail":{"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"}}}}`},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var source SourceConfig
			if err := json.Unmarshal([]byte(test.data), &source); err != nil {
				t.Fatalf("decode legacy envelope: %v", err)
			}
			if source.Email == nil {
				t.Fatal("legacy envelope did not populate SourceConfig.Email")
			}
			if source.Email.Connector.Provider != "maildir" || source.Email.Connector.Endpoint != "maildir:///var/mail/archive" {
				t.Fatalf("unexpected connector: %#v", source.Email.Connector)
			}
			if source.Email.Crawl.BatchSize != mailconfig.DefaultSourceConfig().Crawl.BatchSize {
				t.Fatal("legacy envelope did not receive mail defaults")
			}
		})
	}
}

func TestSourceConfigAcceptsLegacyMailEnvelopeYAML(t *testing.T) {
	t.Parallel()

	var source SourceConfig
	if err := yaml.Unmarshal([]byte(`
custom:
  mail_config:
    connector:
      provider: maildir
      endpoint: maildir:///var/mail/archive
`), &source); err != nil {
		t.Fatalf("decode legacy YAML envelope: %v", err)
	}
	if source.Email == nil || source.Email.Connector.Provider != "maildir" {
		t.Fatalf("legacy YAML did not populate email config: %#v", source.Email)
	}
}

func TestEmailSourceConfigValidationDelegatesToMail(t *testing.T) {
	t.Parallel()

	config := DefaultEmailSourceConfig()
	config.Connector.Provider = "imap"
	config.Connector.Endpoint = "https://mail.example.test"
	config.Auth.CredentialRef = "secret/archive"

	projectErr := config.Validate()
	mailErr := mailconfig.ValidateSourceConfig(config.SourceConfig)
	if projectErr == nil || mailErr == nil {
		t.Fatalf("expected both validators to reject the endpoint: project=%v mail=%v", projectErr, mailErr)
	}
	if projectErr.Error() != mailErr.Error() || !strings.Contains(projectErr.Error(), "scheme") {
		t.Fatalf("project validation diverged from mail validation: project=%q mail=%q", projectErr, mailErr)
	}
}

func TestSourceConfigWithEmailIsNotEmpty(t *testing.T) {
	t.Parallel()

	source := SourceConfig{Email: &EmailSourceConfig{}}
	if source.IsEmpty() {
		t.Fatal("source with an email configuration was reported empty")
	}
}

func TestRepresentativeEmailSourceFixture(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/email-source.yml")
	if err != nil {
		t.Fatalf("read representative email source fixture: %v", err)
	}

	var source SourceConfig
	if err := yaml.Unmarshal(data, &source); err != nil {
		t.Fatalf("parse representative email source fixture: %v", err)
	}
	if source.Email == nil {
		t.Fatal("representative fixture has no email configuration")
	}

	email := source.Email
	if source.CrawlingConfig.SourceType != SourceTypeEmail || email.Connector.Provider != "imap" {
		t.Fatalf("fixture source type or connector is not supported: source=%q provider=%q", source.CrawlingConfig.SourceType, email.Connector.Provider)
	}
	if email.Auth.CredentialRef != "secret/test/email-archive" {
		t.Fatalf("credential reference = %q, want test secret reference", email.Auth.CredentialRef)
	}
	if email.Crawl.Mode != mailconfig.ModeListen || !email.Listener.Enabled {
		t.Fatalf("listener mode is not enabled: mode=%q listener=%#v", email.Crawl.Mode, email.Listener)
	}

	links := email.Extraction.Links
	if !links.Extract || links.FollowRemote {
		t.Fatalf("links must be extracted without remote fetching: %#v", links)
	}
	if email.Extraction.PreferHTML || !email.Extraction.CleanupHTML {
		t.Fatalf("safe body extraction settings were not parsed: %#v", email.Extraction)
	}
	if !email.Extraction.Attachments.Include || email.Extraction.Attachments.IncludeInline || email.Extraction.Attachments.ExtractText {
		t.Fatalf("safe attachment extraction settings were not parsed: %#v", email.Extraction.Attachments)
	}

	limits := email.Crawl.Limits
	if limits.MaxAttachmentBytes != 2<<20 || limits.MaxTotalAttachmentBytes != 5<<20 || limits.MaxAttachments != 10 {
		t.Fatalf("attachment limits were not parsed: %#v", limits)
	}
	if err := email.Validate(); err != nil {
		t.Fatalf("validate representative email source fixture: %v", err)
	}
}
