package database

import (
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func stickySourceConfigForTest(sticky bool) cfg.SourceConfig {
	value := sticky
	return cfg.SourceConfig{
		Version:       "1.0",
		FormatVersion: "1.0.0",
		SourceName:    "sticky-test",
		CrawlingConfig: cfg.CrawlingConfig{
			Site:          "https://example.test/",
			SourceType:    "website",
			StickySession: &value,
		},
	}
}

func TestValidateSourceConfigStickyProtocolBoundary(t *testing.T) {
	if err := validateSourceConfig(stickySourceConfigForTest(true)); err != nil {
		t.Fatalf("browser-backed sticky source rejected: %v", err)
	}

	emailType := stickySourceConfigForTest(true)
	emailType.CrawlingConfig.SourceType = "email"
	if err := validateSourceConfig(emailType); err == nil {
		t.Fatal("email source_type accepted sticky_session")
	}

	emailConfig := stickySourceConfigForTest(true)
	emailConfig.Email = &cfg.EmailSourceConfig{}
	if err := validateSourceConfig(emailConfig); err == nil {
		t.Fatal("email configuration accepted sticky_session")
	}

	imapSite := stickySourceConfigForTest(true)
	imapSite.CrawlingConfig.Site = "imaps://mail.example.test:993"
	if err := validateSourceConfig(imapSite); err == nil {
		t.Fatal("IMAP site accepted sticky_session")
	}

	networkSite := stickySourceConfigForTest(true)
	networkSite.CrawlingConfig.Site = "null://host"
	if err := validateSourceConfig(networkSite); err == nil {
		t.Fatal("network-only site accepted sticky_session")
	}

	disabled := stickySourceConfigForTest(false)
	disabled.CrawlingConfig.Site = "imaps://mail.example.test:993"
	if err := validateSourceConfig(disabled); err != nil {
		t.Fatalf("non-sticky email source rejected: %v", err)
	}
}

func TestValidateSourceConfigCanonicalProxy(t *testing.T) {
	valid := stickySourceConfigForTest(false)
	valid.CrawlingConfig.Proxy = "http://regular-proxy:3128"
	if err := validateSourceConfig(valid); err != nil {
		t.Fatalf("valid canonical proxy rejected: %v", err)
	}

	for _, proxy := range []string{
		"http://bad host:1",
		"http://proxy.example",
		"http://user:pass@proxy.example:3128",
		"http://proxy.example:3128/path",
		"no_proxy",
	} {
		config := stickySourceConfigForTest(false)
		config.CrawlingConfig.Proxy = proxy
		err := validateSourceConfig(config)
		if err == nil || !strings.Contains(err.Error(), "crawling_config.proxy") {
			t.Errorf("proxy %q: error = %v, want a crawling_config.proxy rejection", proxy, err)
		}
	}
}
