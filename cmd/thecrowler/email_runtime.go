package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

// configuredEmailConnectorFactory constructs provider connectors from the
// source policy and credentials loaded with the process-wide CROWler config.
type configuredEmailConnectorFactory struct {
	credentials map[string]cfg.EmailCredentialConfig
}

func newEmailPipelineDependencies(config cfg.EmailConfig, handler cdb.Handler) (*mail.PipelineDependencies, error) {
	if !config.Enabled {
		return nil, nil
	}
	stateStore, err := mail.NewDatabaseStateStore(handler)
	if err != nil {
		return nil, err
	}
	credentials := make(map[string]cfg.EmailCredentialConfig, len(config.Credentials))
	for reference, credential := range config.Credentials {
		credentials[reference] = credential
	}
	return &mail.PipelineDependencies{
		ConnectorFactory: configuredEmailConnectorFactory{credentials: credentials},
		StateStore:       stateStore,
	}, nil
}

func (factory configuredEmailConnectorFactory) NewConnector(ctx context.Context, source mail.SourceConfig) (mail.Connector, error) {
	provider := strings.ToLower(strings.TrimSpace(source.Connector.Provider))
	credential, err := factory.credential(source.Auth.CredentialRef)
	if err != nil && provider != "maildir" && provider != "mbox" {
		return nil, err
	}

	switch provider {
	case "imap":
		connectorConfig, err := mail.IMAPConnectorConfigFromSource(source, mail.IMAPAuth{Username: credential.Username, Password: credential.Password})
		if err != nil {
			return nil, err
		}
		return mail.NewIMAPConnector(ctx, connectorConfig)
	case "pop3":
		connectorConfig, err := mail.POP3ConnectorConfigFromSource(source, mail.POP3Auth{Username: credential.Username, Password: credential.Password})
		if err != nil {
			return nil, err
		}
		return mail.NewPOP3Connector(ctx, connectorConfig)
	case "gmail":
		return mail.NewGmailConnector(ctx, gmailConnectorConfig(source), mail.GmailDependencies{Credentials: staticGmailCredentialSource{credential: credential}})
	case "graph-mail":
		return mail.NewGraphConnector(ctx, graphConnectorConfig(source), mail.GraphDependencies{Credentials: staticGraphCredentialSource{credential: credential}})
	default:
		return nil, fmt.Errorf("email connector provider %q is not supported by the CROWler runtime", provider)
	}
}

func (factory configuredEmailConnectorFactory) credential(reference string) (cfg.EmailCredentialConfig, error) {
	reference = strings.TrimSpace(reference)
	credential, ok := factory.credentials[reference]
	if !ok {
		return cfg.EmailCredentialConfig{}, fmt.Errorf("email credential reference %q is not configured", reference)
	}
	return credential, nil
}

type staticGmailCredentialSource struct{ credential cfg.EmailCredentialConfig }

func (source staticGmailCredentialSource) LoadCredentials(context.Context, string) (mail.GmailOAuthCredentials, error) {
	if strings.TrimSpace(source.credential.OAuthJSON) == "" {
		return mail.GmailOAuthCredentials{}, fmt.Errorf("email Gmail credential oauth_json is empty")
	}
	if !json.Valid([]byte(source.credential.OAuthJSON)) {
		return mail.GmailOAuthCredentials{}, fmt.Errorf("email Gmail credential oauth_json is not valid JSON")
	}
	return mail.GmailOAuthCredentials{JSON: []byte(source.credential.OAuthJSON)}, nil
}

type staticGraphCredentialSource struct{ credential cfg.EmailCredentialConfig }

func (source staticGraphCredentialSource) LoadCredentials(context.Context, string) (mail.GraphOAuthCredentials, error) {
	if strings.TrimSpace(source.credential.ClientID) == "" || source.credential.ClientSecret == "" {
		return mail.GraphOAuthCredentials{}, fmt.Errorf("email Microsoft Graph credential requires client_id and client_secret")
	}
	return mail.GraphOAuthCredentials{ClientID: source.credential.ClientID, ClientSecret: source.credential.ClientSecret}, nil
}

func gmailConnectorConfig(source mail.SourceConfig) mail.GmailConnectorConfig {
	userID := extensionString(source.Connector.Extensions, "user_id")
	if endpoint, err := url.Parse(source.Connector.Endpoint); err == nil && endpoint.User != nil && userID == "" {
		userID = endpoint.User.Username()
	}
	return mail.GmailConnectorConfig{
		AccountID:     source.Auth.Identity,
		UserID:        userID,
		CredentialRef: source.Auth.CredentialRef,
		Mailboxes:     mail.MailboxSelector{Include: source.Mailboxes.Include, Exclude: source.Mailboxes.Exclude},
		Query:         extensionString(source.Connector.Extensions, "query"),
	}
}

func graphConnectorConfig(source mail.SourceConfig) mail.GraphConnectorConfig {
	return mail.GraphConnectorConfig{
		AccountID:     source.Auth.Identity,
		TenantID:      extensionString(source.Connector.Extensions, "tenant_id"),
		UserID:        extensionString(source.Connector.Extensions, "user_id"),
		CredentialRef: source.Auth.CredentialRef,
		Mailboxes:     mail.MailboxSelector{Include: source.Mailboxes.Include, Exclude: source.Mailboxes.Exclude},
		BaseURL:       extensionString(source.Connector.Extensions, "base_url"),
	}
}

func extensionString(extensions map[string]any, key string) string {
	value, _ := extensions[key].(string)
	return strings.TrimSpace(value)
}
