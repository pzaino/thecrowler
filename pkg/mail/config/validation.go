package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxMessageBytes         = 25 << 20
	defaultMaxAttachmentBytes      = 10 << 20
	defaultMaxTotalAttachmentBytes = 25 << 20
	defaultMaxAttachments          = 50
	defaultMaxHeaderBytes          = 1 << 20
	defaultMaxEmbeddedMessageDepth = 3
	defaultMaxMIMEDepth            = 30
	defaultMaxMIMEParts            = 1_000
	defaultMaxLinksPerMessage      = 100
)

var providerSchemes = map[string]string{
	"pop3":       "pop3",
	"imap":       "imap",
	"gmail":      "gmail",
	"graph-mail": "graph-mail",
	"maildir":    "maildir",
	"mbox":       "mbox",
}

// DefaultSourceConfig returns a conservative starting configuration for a mail
// source. The caller must still set the provider, endpoint, and credential
// reference before validating and using the configuration.
func DefaultSourceConfig() SourceConfig {
	return SourceConfig{
		Connector: ConnectorConfig{
			Timeout: 30 * time.Second,
		},
		Mailboxes: MailboxConfig{
			Include: []string{"INBOX"},
		},
		Crawl: CrawlConfig{
			Mode:        ModePoll,
			BatchSize:   100,
			MaxMessages: 1_000,
			Timeout:     10 * time.Minute,
			Limits: Limits{
				MaxMessageBytes:         defaultMaxMessageBytes,
				MaxAttachmentBytes:      defaultMaxAttachmentBytes,
				MaxTotalAttachmentBytes: defaultMaxTotalAttachmentBytes,
				MaxAttachments:          defaultMaxAttachments,
				MaxHeaderBytes:          defaultMaxHeaderBytes,
				MaxEmbeddedMessageDepth: defaultMaxEmbeddedMessageDepth,
				MaxMIMEDepth:            defaultMaxMIMEDepth,
				MaxMIMEParts:            defaultMaxMIMEParts,
			},
		},
		Extraction: ExtractionConfig{
			Links: LinkPolicy{
				Extract:            true,
				AllowedSchemes:     []string{"http", "https"},
				MaxLinksPerMessage: defaultMaxLinksPerMessage,
			},
		},
		Listener: ListenerConfig{
			BufferSize:          128,
			CoalesceWindow:      time.Second,
			ReconnectBackoff:    5 * time.Second,
			MaxReconnectBackoff: time.Minute,
			IdleReissueInterval: 25 * time.Minute,
		},
		Reconciliation: ReconciliationConfig{
			PollInterval:     5 * time.Minute,
			FullSyncInterval: 24 * time.Hour,
			PageSize:         100,
			MaxPages:         100,
			LeaseTTL:         2 * time.Minute,
		},
	}
}

// ValidateSourceConfig verifies that config is internally consistent and
// bounded before it is passed to a provider adapter.
func ValidateSourceConfig(config SourceConfig) error {
	provider := strings.ToLower(strings.TrimSpace(config.Connector.Provider))
	expectedScheme, ok := providerSchemes[provider]
	if !ok {
		return fmt.Errorf("connector.provider %q is unsupported", config.Connector.Provider)
	}

	if err := validateEndpoint(config.Connector.Endpoint, provider, expectedScheme, config.Connector.TLS); err != nil {
		return err
	}
	if err := validateProxyURL(config.Connector.ProxyURL, provider); err != nil {
		return err
	}

	if config.Connector.Timeout <= 0 {
		return fmt.Errorf("connector.timeout must be greater than zero")
	}
	if strings.TrimSpace(config.Auth.CredentialRef) == "" && provider != "maildir" && provider != "mbox" {
		return fmt.Errorf("auth.credential_ref is required for provider %q", provider)
	}
	if err := validateMailboxSelection(config.Mailboxes); err != nil {
		return err
	}
	if err := validateCrawl(config.Crawl); err != nil {
		return err
	}
	if err := validateExtraction(config.Extraction); err != nil {
		return err
	}
	if err := validateSafety(config.Safety, config.Extraction.Links); err != nil {
		return err
	}
	if err := validateListener(config.Crawl.Mode, provider, config.Listener); err != nil {
		return err
	}
	if err := validateReconciliation(config.Reconciliation); err != nil {
		return err
	}
	return nil
}

func validateProxyURL(rawProxyURL, provider string) error {
	rawProxyURL = strings.TrimSpace(rawProxyURL)
	if rawProxyURL == "" {
		return nil
	}
	if provider == "maildir" || provider == "mbox" {
		return fmt.Errorf("connector.proxy_url is not valid for provider %q", provider)
	}
	if strings.ContainsAny(rawProxyURL, "\r\n\t ") {
		return fmt.Errorf("connector.proxy_url must not contain whitespace")
	}
	proxyURL, err := url.Parse(rawProxyURL)
	if err != nil {
		return fmt.Errorf("connector.proxy_url is invalid: %w", err)
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5":
	default:
		return fmt.Errorf("connector.proxy_url scheme must be http, https, or socks5")
	}
	if proxyURL.Host == "" || proxyURL.Hostname() == "" {
		return fmt.Errorf("connector.proxy_url must contain a host")
	}
	if proxyURL.Path != "" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return fmt.Errorf("connector.proxy_url must not contain a path, query, or fragment")
	}
	if err := validateProxyPort(proxyURL); err != nil {
		return err
	}
	return nil
}

func validateProxyPort(proxyURL *url.URL) error {
	port := proxyURL.Port()
	if port == "" {
		return nil
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("connector.proxy_url port must be between 1 and 65535")
	}
	return nil
}

func validateEndpoint(rawEndpoint, provider, expectedScheme string, tlsConfig TLSConfig) error {
	rawEndpoint = strings.TrimSpace(rawEndpoint)
	if rawEndpoint == "" {
		return fmt.Errorf("connector.endpoint is required")
	}
	if strings.ContainsAny(rawEndpoint, "\r\n\t ") {
		return fmt.Errorf("connector.endpoint must not contain whitespace")
	}

	endpoint, err := url.Parse(rawEndpoint)
	if err != nil {
		return fmt.Errorf("connector.endpoint is invalid: %w", err)
	}
	scheme := strings.ToLower(endpoint.Scheme)
	if provider == "imap" {
		if scheme != "imap" && scheme != "imaps" {
			return fmt.Errorf("connector.endpoint scheme must be imap or imaps for provider %q", provider)
		}
	} else if provider == "pop3" {
		if scheme != "pop3" && scheme != "pop3s" {
			return fmt.Errorf("connector.endpoint scheme must be pop3 or pop3s for provider %q", provider)
		}
	} else if scheme != expectedScheme {
		return fmt.Errorf("connector.endpoint scheme must be %q for provider %q", expectedScheme, provider)
	}

	switch provider {
	case "maildir", "mbox":
		if endpoint.Host != "" || endpoint.User != nil || !strings.HasPrefix(endpoint.Path, "/") || endpoint.Path == "/" {
			return fmt.Errorf("connector.endpoint for provider %q must contain an absolute path and no host", provider)
		}
		if endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return fmt.Errorf("connector.endpoint for provider %q must not contain a query or fragment", provider)
		}
		if tlsConfig.InsecureSkipVerify || strings.TrimSpace(tlsConfig.ServerName) != "" {
			return fmt.Errorf("connector.tls is not valid for provider %q", provider)
		}
		return nil
	default:
		if endpoint.Host == "" || endpoint.Hostname() == "" {
			return fmt.Errorf("connector.endpoint for provider %q must contain a host", provider)
		}
	}

	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("connector.endpoint must not contain a query or fragment")
	}

	if endpoint.User != nil {
		if provider != "gmail" || endpoint.User.Username() == "" {
			return fmt.Errorf("connector.endpoint must not contain credentials")
		}
		if _, hasPassword := endpoint.User.Password(); hasPassword {
			return fmt.Errorf("connector.endpoint must not contain credentials")
		}
	}
	if err := validatePort(endpoint); err != nil {
		return err
	}

	if provider == "imap" || provider == "pop3" {
		secureScheme := map[string]string{"imap": "imaps", "pop3": "pop3s"}[provider]
		if scheme != secureScheme && (tlsConfig.InsecureSkipVerify || strings.TrimSpace(tlsConfig.ServerName) != "") {
			return fmt.Errorf("connector.tls options require an %s endpoint", secureScheme)
		}
	} else if tlsConfig.InsecureSkipVerify || strings.TrimSpace(tlsConfig.ServerName) != "" {
		return fmt.Errorf("connector.tls options are only supported by network mail providers")
	}

	return nil
}

func validatePort(endpoint *url.URL) error {
	port := endpoint.Port()
	if port == "" {
		return nil
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("connector.endpoint port must be between 1 and 65535")
	}
	if net.ParseIP(endpoint.Hostname()) == nil && strings.Contains(endpoint.Hostname(), ":") {
		return fmt.Errorf("connector.endpoint contains an invalid host")
	}
	return nil
}

func validateMailboxSelection(config MailboxConfig) error {
	included := make(map[string]struct{}, len(config.Include))
	for _, mailbox := range config.Include {
		name := strings.TrimSpace(mailbox)
		if name == "" {
			return fmt.Errorf("mailboxes.include must not contain an empty mailbox")
		}
		included[strings.ToLower(name)] = struct{}{}
	}
	for _, mailbox := range config.Exclude {
		name := strings.TrimSpace(mailbox)
		if name == "" {
			return fmt.Errorf("mailboxes.exclude must not contain an empty mailbox")
		}
		if _, conflict := included[strings.ToLower(name)]; conflict {
			return fmt.Errorf("mailbox %q cannot be both included and excluded", name)
		}
	}
	return nil
}

func validateCrawl(config CrawlConfig) error {
	if config.Mode != ModePoll && config.Mode != ModeListen {
		return fmt.Errorf("crawl.mode must be %q or %q", ModePoll, ModeListen)
	}
	if config.BatchSize <= 0 {
		return fmt.Errorf("crawl.batch_size must be greater than zero")
	}
	if config.MaxMessages <= 0 {
		return fmt.Errorf("crawl.max_messages must be greater than zero")
	}
	if config.BatchSize > config.MaxMessages {
		return fmt.Errorf("crawl.batch_size must not exceed crawl.max_messages")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("crawl.timeout must be greater than zero")
	}

	limits := config.Limits
	if limits.MaxMessageBytes <= 0 {
		return fmt.Errorf("crawl.limits.max_message_bytes must be greater than zero")
	}
	if limits.MaxAttachmentBytes <= 0 {
		return fmt.Errorf("crawl.limits.max_attachment_bytes must be greater than zero")
	}
	if limits.MaxAttachmentBytes > limits.MaxMessageBytes {
		return fmt.Errorf("crawl.limits.max_attachment_bytes must not exceed max_message_bytes")
	}
	if limits.MaxTotalAttachmentBytes <= 0 || limits.MaxTotalAttachmentBytes > limits.MaxMessageBytes {
		return fmt.Errorf("crawl.limits.max_total_attachment_bytes must be greater than zero and not exceed max_message_bytes")
	}
	if limits.MaxAttachmentBytes > limits.MaxTotalAttachmentBytes {
		return fmt.Errorf("crawl.limits.max_attachment_bytes must not exceed max_total_attachment_bytes")
	}
	if limits.MaxAttachments <= 0 {
		return fmt.Errorf("crawl.limits.max_attachments must be greater than zero")
	}
	if limits.MaxHeaderBytes <= 0 || limits.MaxHeaderBytes > limits.MaxMessageBytes {
		return fmt.Errorf("crawl.limits.max_header_bytes must be greater than zero and not exceed max_message_bytes")
	}
	if limits.MaxEmbeddedMessageDepth <= 0 {
		return fmt.Errorf("crawl.limits.max_embedded_message_depth must be greater than zero")
	}
	if limits.MaxMIMEDepth <= 0 {
		return fmt.Errorf("crawl.limits.max_mime_depth must be greater than zero")
	}
	if limits.MaxMIMEParts <= 0 {
		return fmt.Errorf("crawl.limits.max_mime_parts must be greater than zero")
	}
	return nil
}

func validateExtraction(config ExtractionConfig) error {
	if config.Links.FollowRemote && !config.Links.Extract {
		return fmt.Errorf("extraction.links.follow_remote requires extraction.links.extract")
	}
	if config.Links.MaxLinksPerMessage <= 0 {
		return fmt.Errorf("extraction.links.max_links_per_message must be greater than zero")
	}
	if (config.Attachments.IncludeInline || config.Attachments.Download || config.Attachments.ExtractText) && !config.Attachments.Include {
		return fmt.Errorf("attachment inline, download, or text extraction requires extraction.attachments.include")
	}

	for _, mediaType := range config.Attachments.AllowedMediaTypes {
		if strings.TrimSpace(mediaType) == "" {
			return fmt.Errorf("extraction.attachments.allowed_media_types must not contain an empty value")
		}
	}
	for _, mediaType := range config.Attachments.BlockedMediaTypes {
		if strings.TrimSpace(mediaType) == "" {
			return fmt.Errorf("extraction.attachments.blocked_media_types must not contain an empty value")
		}
	}
	return nil
}

func validateSafety(config SafetyConfig, links LinkPolicy) error {
	if config.AllowRemoteResources {
		return fmt.Errorf("safety.allow_remote_resources is not supported; message content must remain static")
	}
	if config.AllowJavaScript {
		return fmt.Errorf("safety.allow_javascript is not supported; message content must not execute JavaScript")
	}
	if config.AllowMailboxMutation {
		return fmt.Errorf("safety.allow_mailbox_mutation is not supported; mail sources must remain read-only")
	}

	if config.AllowUnrestrictedLinks && !links.FollowRemote {
		return fmt.Errorf("safety.allow_unrestricted_links requires extraction.links.follow_remote")
	}
	if links.FollowRemote && !hasNonEmptyValue(links.Allowlist) && !config.AllowUnrestrictedLinks {
		return fmt.Errorf("extraction.links.follow_remote requires a non-empty allowlist or safety.allow_unrestricted_links=true")
	}
	return nil
}

func hasNonEmptyValue(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func validateListener(mode Mode, provider string, config ListenerConfig) error {
	if config.BufferSize <= 0 {
		return fmt.Errorf("listener.buffer_size must be greater than zero")
	}
	if config.CoalesceWindow <= 0 {
		return fmt.Errorf("listener.coalesce_window must be greater than zero")
	}
	if config.ReconnectBackoff <= 0 {
		return fmt.Errorf("listener.reconnect_backoff must be greater than zero")
	}
	if config.MaxReconnectBackoff < config.ReconnectBackoff {
		return fmt.Errorf("listener.max_reconnect_backoff must be at least listener.reconnect_backoff")
	}
	if config.IdleReissueInterval <= 0 {
		return fmt.Errorf("listener.idle_reissue_interval must be greater than zero")
	}
	if mode == ModeListen && !config.Enabled {
		return fmt.Errorf("listener.enabled must be true when crawl.mode is %q", ModeListen)
	}
	if config.Enabled && mode != ModeListen {
		return fmt.Errorf("crawl.mode must be %q when listener.enabled is true", ModeListen)
	}
	if config.Enabled && (provider == "pop3" || provider == "maildir" || provider == "mbox") {
		return fmt.Errorf("listener mode is not supported by provider %q", provider)
	}
	return nil
}

func validateReconciliation(config ReconciliationConfig) error {
	if config.PollInterval <= 0 {
		return fmt.Errorf("reconciliation.poll_interval must be greater than zero")
	}
	if config.FullSyncInterval <= 0 {
		return fmt.Errorf("reconciliation.full_sync_interval must be greater than zero")
	}
	if config.FullSyncInterval < config.PollInterval {
		return fmt.Errorf("reconciliation.full_sync_interval must not be shorter than poll_interval")
	}
	if config.PageSize <= 0 {
		return fmt.Errorf("reconciliation.page_size must be greater than zero")
	}
	if config.MaxPages <= 0 {
		return fmt.Errorf("reconciliation.max_pages must be greater than zero")
	}
	if config.LeaseTTL <= 0 {
		return fmt.Errorf("reconciliation.lease_ttl must be greater than zero")
	}
	if config.LeaseTTL >= config.PollInterval {
		return fmt.Errorf("reconciliation.lease_ttl must be shorter than poll_interval")
	}
	return nil
}
