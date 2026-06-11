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
			CleanupHTML: true,
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
	for _, field := range []string{`"connector"`, `"credential_ref"`, `"mailboxes"`, `"cleanup_html"`, `"reconciliation"`, `"extensions"`} {
		if !strings.Contains(string(jsonData), field) {
			t.Errorf("JSON source config does not contain %s: %s", field, jsonData)
		}
	}

	yamlData, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("marshal YAML source config: %v", err)
	}
	for _, field := range []string{"connector:", "credential_ref:", "mailboxes:", "cleanup_html:", "reconciliation:", "extensions:"} {
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

func TestDefaultSourceConfig(t *testing.T) {
	got := DefaultSourceConfig()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "connector timeout", got: got.Connector.Timeout, want: 30 * time.Second},
		{name: "mailboxes", got: got.Mailboxes.Include, want: []string{"INBOX"}},
		{name: "crawl mode", got: got.Crawl.Mode, want: ModePoll},
		{name: "batch size", got: got.Crawl.BatchSize, want: 100},
		{name: "max messages", got: got.Crawl.MaxMessages, want: 1_000},
		{name: "crawl timeout", got: got.Crawl.Timeout, want: 10 * time.Minute},
		{name: "message bytes", got: got.Crawl.Limits.MaxMessageBytes, want: int64(25 << 20)},
		{name: "attachment bytes", got: got.Crawl.Limits.MaxAttachmentBytes, want: int64(10 << 20)},
		{name: "total attachment bytes", got: got.Crawl.Limits.MaxTotalAttachmentBytes, want: int64(25 << 20)},
		{name: "attachment count", got: got.Crawl.Limits.MaxAttachments, want: 50},
		{name: "header bytes", got: got.Crawl.Limits.MaxHeaderBytes, want: int64(1 << 20)},
		{name: "embedded message depth", got: got.Crawl.Limits.MaxEmbeddedMessageDepth, want: 3},
		{name: "MIME depth", got: got.Crawl.Limits.MaxMIMEDepth, want: 30},
		{name: "MIME parts", got: got.Crawl.Limits.MaxMIMEParts, want: 1_000},
		{name: "cleanup HTML", got: got.Extraction.CleanupHTML, want: false},
		{name: "extract links", got: got.Extraction.Links.Extract, want: true},
		{name: "follow remote links", got: got.Extraction.Links.FollowRemote, want: false},
		{name: "allowed link schemes", got: got.Extraction.Links.AllowedSchemes, want: []string{"http", "https"}},
		{name: "maximum links per message", got: got.Extraction.Links.MaxLinksPerMessage, want: 100},
		{name: "include attachments", got: got.Extraction.Attachments.Include, want: false},
		{name: "listener enabled", got: got.Listener.Enabled, want: false},
		{name: "listener buffer", got: got.Listener.BufferSize, want: 128},
		{name: "coalesce window", got: got.Listener.CoalesceWindow, want: time.Second},
		{name: "reconnect backoff", got: got.Listener.ReconnectBackoff, want: 5 * time.Second},
		{name: "poll interval", got: got.Reconciliation.PollInterval, want: 5 * time.Minute},
		{name: "full sync interval", got: got.Reconciliation.FullSyncInterval, want: 24 * time.Hour},
		{name: "page size", got: got.Reconciliation.PageSize, want: 100},
		{name: "max pages", got: got.Reconciliation.MaxPages, want: 100},
		{name: "lease TTL", got: got.Reconciliation.LeaseTTL, want: 2 * time.Minute},
		{name: "TLS verification enabled", got: got.Connector.TLS.InsecureSkipVerify, want: false},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if !reflect.DeepEqual(check.got, check.want) {
				t.Fatalf("got %#v, want %#v", check.got, check.want)
			}
		})
	}

	got.Mailboxes.Include[0] = "Archive"
	got.Extraction.Links.AllowedSchemes[0] = "ftp"
	fresh := DefaultSourceConfig()
	if fresh.Mailboxes.Include[0] != "INBOX" || fresh.Extraction.Links.AllowedSchemes[0] != "http" {
		t.Fatal("DefaultSourceConfig returned shared mutable slices")
	}
}

func TestValidateSourceConfigProviders(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		endpoint    string
		withoutAuth bool
	}{
		{name: "POP3", provider: "pop3", endpoint: "pop3s://mail.example.com:995"},
		{name: "plain POP3", provider: "POP3", endpoint: "pop3://mail.example.com:110"},
		{name: "IMAP", provider: "imap", endpoint: "imaps://mail.example.com:993"},
		{name: "plain IMAP", provider: "IMAP", endpoint: "imap://mail.example.com:143"},
		{name: "Gmail", provider: "gmail", endpoint: "gmail://user@example.com"},
		{name: "Graph mail", provider: "graph-mail", endpoint: "graph-mail://tenant/mailbox"},
		{name: "Maildir", provider: "maildir", endpoint: "maildir:///var/mail/user", withoutAuth: true},
		{name: "mbox", provider: "mbox", endpoint: "mbox:///var/mail/archive.mbox", withoutAuth: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validSourceConfig()
			config.Connector.Provider = test.provider
			config.Connector.Endpoint = test.endpoint
			if test.withoutAuth {
				config.Auth.CredentialRef = ""
			}
			if err := ValidateSourceConfig(config); err != nil {
				t.Fatalf("ValidateSourceConfig() error = %v", err)
			}
		})
	}
}

func TestValidateSourceConfigAllowsAttachmentDenylistToOverrideAllowlist(t *testing.T) {
	config := DefaultSourceConfig()
	config.Connector.Provider = "imap"
	config.Connector.Endpoint = "imaps://mail.example.com:993"
	config.Auth.CredentialRef = "secret/mail"
	config.Extraction.Attachments.Include = true
	config.Extraction.Attachments.AllowedMediaTypes = []string{"application/pdf"}
	config.Extraction.Attachments.BlockedMediaTypes = []string{"APPLICATION/PDF"}

	if err := ValidateSourceConfig(config); err != nil {
		t.Fatalf("ValidateSourceConfig() error = %v, denylist overlap should be valid", err)
	}
}

func TestValidateSourceConfigRejectsInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SourceConfig)
		wantErr string
	}{
		{name: "unsupported provider", mutate: func(c *SourceConfig) { c.Connector.Provider = "jmap" }, wantErr: "unsupported"},
		{name: "unbounded links", mutate: func(c *SourceConfig) { c.Extraction.Links.MaxLinksPerMessage = 0 }, wantErr: "max_links_per_message"},
		{name: "missing endpoint", mutate: func(c *SourceConfig) { c.Connector.Endpoint = "" }, wantErr: "endpoint is required"},
		{name: "provider scheme mismatch", mutate: func(c *SourceConfig) { c.Connector.Endpoint = "gmail://user@example.com" }, wantErr: "scheme"},
		{name: "missing host", mutate: func(c *SourceConfig) { c.Connector.Endpoint = "imaps:///INBOX" }, wantErr: "host"},
		{name: "invalid port", mutate: func(c *SourceConfig) { c.Connector.Endpoint = "imaps://mail.example.com:70000" }, wantErr: "port"},
		{name: "endpoint password", mutate: func(c *SourceConfig) { c.Connector.Endpoint = "imaps://user:password@mail.example.com:993" }, wantErr: "credentials"},
		{name: "TLS options on plain IMAP", mutate: func(c *SourceConfig) {
			c.Connector.Endpoint = "imap://mail.example.com:143"
			c.Connector.TLS.ServerName = "mail.example.com"
		}, wantErr: "require an imaps endpoint"},
		{name: "TLS options on API provider", mutate: func(c *SourceConfig) {
			c.Connector.Provider = "gmail"
			c.Connector.Endpoint = "gmail://user@example.com"
			c.Connector.TLS.InsecureSkipVerify = true
		}, wantErr: "only supported by network mail providers"},
		{name: "TLS options on local provider", mutate: func(c *SourceConfig) {
			c.Connector.Provider = "maildir"
			c.Connector.Endpoint = "maildir:///var/mail/user"
			c.Connector.TLS.ServerName = "mail.example.com"
		}, wantErr: "not valid"},
		{name: "missing credential reference", mutate: func(c *SourceConfig) { c.Auth.CredentialRef = "" }, wantErr: "credential_ref"},
		{name: "mailbox conflict", mutate: func(c *SourceConfig) { c.Mailboxes.Exclude = []string{" inbox "} }, wantErr: "both included and excluded"},
		{name: "empty included mailbox", mutate: func(c *SourceConfig) { c.Mailboxes.Include = []string{""} }, wantErr: "empty mailbox"},
		{name: "invalid mode", mutate: func(c *SourceConfig) { c.Crawl.Mode = "push" }, wantErr: "crawl.mode"},
		{name: "batch exceeds message limit", mutate: func(c *SourceConfig) { c.Crawl.MaxMessages = 99 }, wantErr: "batch_size"},
		{name: "zero message limit", mutate: func(c *SourceConfig) { c.Crawl.Limits.MaxMessageBytes = 0 }, wantErr: "max_message_bytes"},
		{name: "attachment larger than message", mutate: func(c *SourceConfig) {
			c.Crawl.Limits.MaxAttachmentBytes = c.Crawl.Limits.MaxMessageBytes + 1
		}, wantErr: "must not exceed max_message_bytes"},
		{name: "zero attachment count", mutate: func(c *SourceConfig) { c.Crawl.Limits.MaxAttachments = 0 }, wantErr: "max_attachments"},
		{name: "zero embedded message depth", mutate: func(c *SourceConfig) { c.Crawl.Limits.MaxEmbeddedMessageDepth = 0 }, wantErr: "max_embedded_message_depth"},
		{name: "header limit larger than message", mutate: func(c *SourceConfig) {
			c.Crawl.Limits.MaxHeaderBytes = c.Crawl.Limits.MaxMessageBytes + 1
		}, wantErr: "max_header_bytes"},
		{name: "inline attachment without attachments", mutate: func(c *SourceConfig) {
			c.Extraction.Attachments.IncludeInline = true
		}, wantErr: "requires extraction.attachments.include"},
		{name: "zero total attachment bytes", mutate: func(c *SourceConfig) {
			c.Crawl.Limits.MaxTotalAttachmentBytes = 0
		}, wantErr: "max_total_attachment_bytes"},
		{name: "attachment larger than total attachment budget", mutate: func(c *SourceConfig) {
			c.Crawl.Limits.MaxTotalAttachmentBytes = c.Crawl.Limits.MaxAttachmentBytes - 1
		}, wantErr: "must not exceed max_total_attachment_bytes"},
		{name: "listen mode without listener", mutate: func(c *SourceConfig) { c.Crawl.Mode = ModeListen }, wantErr: "listener.enabled"},
		{name: "listener in poll mode", mutate: func(c *SourceConfig) { c.Listener.Enabled = true }, wantErr: "crawl.mode"},
		{name: "listener on local provider", mutate: func(c *SourceConfig) {
			c.Connector.Provider = "maildir"
			c.Connector.Endpoint = "maildir:///var/mail/user"
			c.Auth.CredentialRef = ""
			c.Crawl.Mode = ModeListen
			c.Listener.Enabled = true
		}, wantErr: "not supported"},
		{name: "zero poll interval", mutate: func(c *SourceConfig) { c.Reconciliation.PollInterval = 0 }, wantErr: "poll_interval"},
		{name: "full sync shorter than poll", mutate: func(c *SourceConfig) {
			c.Reconciliation.FullSyncInterval = time.Minute
		}, wantErr: "full_sync_interval"},
		{name: "zero page size", mutate: func(c *SourceConfig) { c.Reconciliation.PageSize = 0 }, wantErr: "page_size"},
		{name: "lease not shorter than poll", mutate: func(c *SourceConfig) {
			c.Reconciliation.LeaseTTL = c.Reconciliation.PollInterval
		}, wantErr: "lease_ttl"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validSourceConfig()
			test.mutate(&config)
			err := ValidateSourceConfig(config)
			if err == nil {
				t.Fatalf("ValidateSourceConfig() error = nil, want error containing %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateSourceConfig() error = %q, want substring %q", err, test.wantErr)
			}
		})
	}
}

func validSourceConfig() SourceConfig {
	config := DefaultSourceConfig()
	config.Connector.Provider = "imap"
	config.Connector.Endpoint = "imaps://mail.example.com:993"
	config.Auth.CredentialRef = "secret/mail-archive"
	return config
}
