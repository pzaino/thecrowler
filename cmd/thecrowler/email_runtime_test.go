package main

import (
	"context"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

func TestConfiguredEmailConnectorFactoryResolvesMainConfigCredential(t *testing.T) {
	factory := configuredEmailConnectorFactory{credentials: map[string]cfg.EmailCredentialConfig{
		"secret/archive": {Username: "reader", Password: "password"},
	}}

	credential, err := factory.credential(" secret/archive ")
	if err != nil {
		t.Fatalf("credential() error = %v", err)
	}
	if credential.Username != "reader" || credential.Password != "password" {
		t.Fatalf("credential() = %#v", credential)
	}
	if _, err := factory.credential("secret/missing"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing credential error = %v", err)
	}
}

func TestConfiguredOAuthCredentialSourcesValidateMainConfigMaterial(t *testing.T) {
	gmailSource := staticGmailCredentialSource{credential: cfg.EmailCredentialConfig{OAuthJSON: `{"client_id":"test"}`}}
	gmailCredential, err := gmailSource.LoadCredentials(context.Background(), "secret/gmail")
	if err != nil || string(gmailCredential.JSON) != `{"client_id":"test"}` {
		t.Fatalf("Gmail LoadCredentials() = %q, %v", gmailCredential.JSON, err)
	}

	graphSource := staticGraphCredentialSource{credential: cfg.EmailCredentialConfig{ClientID: "client", ClientSecret: "secret"}}
	graphCredential, err := graphSource.LoadCredentials(context.Background(), "secret/graph")
	if err != nil || graphCredential.ClientID != "client" || graphCredential.ClientSecret != "secret" {
		t.Fatalf("Graph LoadCredentials() = %#v, %v", graphCredential, err)
	}
}

func TestProviderConfigTranslationUsesSourcePolicy(t *testing.T) {
	source := mail.DefaultSourceConfig()
	source.Auth.Identity = "account"
	source.Auth.CredentialRef = "secret/provider"
	source.Mailboxes.Include = []string{"Archive"}
	source.Connector.Endpoint = "gmail://reader@example.test"
	source.Connector.Extensions = map[string]any{"query": "after:2026/01/01", "tenant_id": "tenant", "user_id": "user@example.test"}

	gmail := gmailConnectorConfig(source)
	if gmail.AccountID != "account" || gmail.UserID != "user@example.test" || gmail.Query != "after:2026/01/01" {
		t.Fatalf("gmail config = %#v", gmail)
	}
	graph := graphConnectorConfig(source)
	if graph.TenantID != "tenant" || graph.UserID != "user@example.test" || graph.CredentialRef != "secret/provider" {
		t.Fatalf("graph config = %#v", graph)
	}
}

func TestConfiguredEmailConnectorFactoryRedactsMissingCredentialReference(t *testing.T) {
	t.Parallel()

	const reference = "secret/mail/archive"
	_, err := (configuredEmailConnectorFactory{}).credential(reference)
	if err == nil {
		t.Fatal("credential() error = nil, want missing credential error")
	}
	if strings.Contains(err.Error(), reference) || !strings.Contains(err.Error(), mail.RedactedValue) {
		t.Fatalf("credential() error was not redacted: %v", err)
	}
}

func TestProviderConnectorConfigsCarrySourceProxy(t *testing.T) {
	source := mail.DefaultSourceConfig()
	source.Connector.ProxyURL = "http://proxy.example.test:8080"
	source.Auth.Identity = "reader@example.test"
	source.Auth.CredentialRef = "secret/mail"
	source.Connector.Endpoint = "gmail://reader@example.test"
	source.Connector.Extensions = map[string]any{
		"tenant_id": "tenant",
		"user_id":   "reader@example.test",
	}

	if got := gmailConnectorConfig(source).ProxyURL; got != source.Connector.ProxyURL {
		t.Fatalf("Gmail proxy URL = %q, want %q", got, source.Connector.ProxyURL)
	}
	if got := graphConnectorConfig(source).ProxyURL; got != source.Connector.ProxyURL {
		t.Fatalf("Graph proxy URL = %q, want %q", got, source.Connector.ProxyURL)
	}
}
