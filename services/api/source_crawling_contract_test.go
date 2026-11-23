package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// addSourceRequestEnvelope mirrors the JSON body accepted by POST /v1/source/add.
// It decodes through the real addSourceRequest type so the test exercises the
// same decoder the handler uses.
func decodeAddSource(t *testing.T, body string) error {
	t.Helper()
	var params addSourceRequest
	return json.Unmarshal([]byte(body), &params)
}

// TestAddSourceRequestRejectsNullSourceCrawlingBooleans proves that the API
// request boundary enforces the same strict contract as the schema: the two
// source-scoped crawling booleans may be omitted, true or false, and an explicit
// null is rejected before any database work happens.
func TestAddSourceRequestRejectsNullSourceCrawlingBooleans(t *testing.T) {
	base := `{"url":"https://example.test/","restricted":0,"disabled":false,"config":{"version":"1.0","format_version":"1.0.0","source_name":"strict","crawling_config":{"site":"https://example.test/"%s}}}`

	for _, tc := range []struct {
		name    string
		extra   string
		wantErr bool
	}{
		{"omitted", "", false},
		{"both true", `,"incognito":true,"follow_redirects":true`, false},
		{"both false", `,"incognito":false,"follow_redirects":false`, false},
		{"null incognito", `,"incognito":null`, true},
		{"null follow_redirects", `,"follow_redirects":null`, true},
		{"string incognito", `,"incognito":"false"`, true},
		{"numeric follow_redirects", `,"follow_redirects":1`, true},
		{"array incognito", `,"incognito":[]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeAddSource(t, strings.Replace(base, "%s", tc.extra, 1))
			if tc.wantErr && err == nil {
				t.Fatal("expected the request to be rejected")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "crawling_config.") {
				t.Fatalf("rejection must name the offending field, got: %v", err)
			}
		})
	}
}

// TestExtractAddSourceParamsPropagatesDecodeErrors proves that the POST body
// decoder used by the add-source handler no longer logs and ignores a malformed
// document. Before RC4F the error was only logged, so the handler created a
// source with an empty configuration and answered 201.
func TestValidateAndReformatConfigChecksProtocolProxyAndStickyOptions(t *testing.T) {
	base := func() cfg.SourceConfig {
		return cfg.SourceConfig{
			Version:       "1.0",
			FormatVersion: "1.0.0",
			SourceName:    "options",
			CrawlingConfig: cfg.CrawlingConfig{
				Site:       "https://example.test/",
				SourceType: "website",
			},
		}
	}

	valid := base()
	valid.CrawlingConfig.Proxy = "http://microproxy-vdi1:8081"
	sticky := true
	valid.CrawlingConfig.StickySession = &sticky
	if err := validateAndReformatConfig(&valid); err != nil {
		t.Fatalf("valid protocol options rejected: %v", err)
	}

	badProxy := base()
	badProxy.CrawlingConfig.Proxy = "http://bad host:1"
	if err := validateAndReformatConfig(&badProxy); err == nil {
		t.Fatal("malformed proxy accepted by update/add validation")
	}

	emailSticky := base()
	emailSticky.CrawlingConfig.SourceType = "email"
	emailSticky.CrawlingConfig.Site = "imaps://mail.example.test:993"
	emailSticky.CrawlingConfig.StickySession = &sticky
	if err := validateAndReformatConfig(&emailSticky); err == nil {
		t.Fatal("email sticky source accepted by update/add validation")
	}

	networkSticky := base()
	networkSticky.CrawlingConfig.Site = "null://example.test"
	networkSticky.CrawlingConfig.StickySession = &sticky
	if err := validateAndReformatConfig(&networkSticky); err == nil {
		t.Fatal("network-only sticky source accepted by update/add validation")
	}
}

func TestExtractAddSourceParamsPropagatesDecodeErrors(t *testing.T) {
	valid := `{"url":"https://example.test/","restricted":0,"config":{"version":"1.0","format_version":"1.0.0","source_name":"ok","crawling_config":{"site":"https://example.test/","incognito":false}}}`
	var params addSourceRequest
	if err := extractAddSourceParams(valid, &params); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	if params.Config.CrawlingConfig.Incognito == nil || *params.Config.CrawlingConfig.Incognito {
		t.Fatalf("explicit false was not decoded: %+v", params.Config.CrawlingConfig.Incognito)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"null incognito", `{"url":"https://example.test/","config":{"version":"1.0","format_version":"1.0.0","source_name":"x","crawling_config":{"site":"https://example.test/","incognito":null}}}`},
		{"null follow_redirects", `{"url":"https://example.test/","config":{"version":"1.0","format_version":"1.0.0","source_name":"x","crawling_config":{"site":"https://example.test/","follow_redirects":null}}}`},
		{"string redirect", `{"url":"https://example.test/","config":{"version":"1.0","format_version":"1.0.0","source_name":"x","crawling_config":{"site":"https://example.test/","follow_redirects":"true"}}}`},
		{"malformed json", `{"url":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got addSourceRequest
			err := extractAddSourceParams(tc.body, &got)
			if err == nil {
				t.Fatal("expected a decode error")
			}
			if !errors.Is(err, errInvalidSourceConfig) {
				t.Fatalf("error must classify as an invalid source configuration (HTTP 400), got: %v", err)
			}
		})
	}
}
