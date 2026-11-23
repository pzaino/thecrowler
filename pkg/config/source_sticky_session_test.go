package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/qri-io/jsonschema"
)

func TestCrawlingConfigStickySessionStrictBoolean(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    *bool
		wantErr bool
	}{
		{"omitted", `{"site":"https://example.test/"}`, nil, false},
		{"true", `{"site":"https://example.test/","sticky_session":true}`, boolPtr(true), false},
		{"false", `{"site":"https://example.test/","sticky_session":false}`, boolPtr(false), false},
		{"null", `{"site":"https://example.test/","sticky_session":null}`, nil, true},
		{"string", `{"site":"https://example.test/","sticky_session":"true"}`, nil, true},
		{"number", `{"site":"https://example.test/","sticky_session":1}`, nil, true},
		{"object", `{"site":"https://example.test/","sticky_session":{}}`, nil, true},
		{"array", `{"site":"https://example.test/","sticky_session":[]}`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var crawling CrawlingConfig
			err := json.Unmarshal([]byte(tc.raw), &crawling)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected decode error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected decode error: %v", err)
			}
			if tc.want == nil {
				if crawling.StickySession != nil {
					t.Fatalf("sticky_session = %v, want nil", *crawling.StickySession)
				}
				return
			}
			if crawling.StickySession == nil || *crawling.StickySession != *tc.want {
				t.Fatalf("sticky_session = %v, want %v", crawling.StickySession, *tc.want)
			}
		})
	}
}

func TestDecodeSourceConfigStoredDocumentRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{
		"version":"1.0",
		"format_version":"1.0.0",
		"source_name":"sticky",
		"crawling_config":{
			"site":"https://example.test/",
			"source_type":"website",
			"proxy":"http://microproxy-vdi1:8081",
			"sticky_session":true
		}
	}`)
	got, err := DecodeSourceConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.CrawlingConfig.StickySession == nil || !*got.CrawlingConfig.StickySession {
		t.Fatal("sticky_session was not preserved")
	}
	if got.CrawlingConfig.Proxy != "http://microproxy-vdi1:8081" {
		t.Fatalf("proxy = %q", got.CrawlingConfig.Proxy)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := DecodeSourceConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CrawlingConfig.StickySession == nil || !*reloaded.CrawlingConfig.StickySession {
		t.Fatal("sticky_session was lost on round trip")
	}

	if _, err := DecodeSourceConfig(json.RawMessage(`{
		"version":"1.0","format_version":"1.0.0","source_name":"bad",
		"crawling_config":{"site":"https://example.test/","sticky_session":null}
	}`)); err == nil {
		t.Fatal("stored null sticky_session must be rejected")
	}
}

func TestDecodeSourceSessionConfigIsIndependentOfUnrelatedFields(t *testing.T) {
	raw := json.RawMessage(`{
		"version":"1.0",
		"format_version":"1.0.0",
		"source_name":"sticky",
		"created_at":"not-a-timestamp",
		"crawling_config":{
			"site":"https://example.test/",
			"proxy":"http://microproxy-vdi1:8081",
			"sticky_session":true
		},
		"custom":{"vdi":[{"proxy_url":"http://microproxy-vdi1:8081"}]}
	}`)
	got, err := DecodeSourceSessionConfig(raw)
	if err != nil {
		t.Fatalf("session subtree should decode independently: %v", err)
	}
	if got.CrawlingConfig.StickySession == nil || !*got.CrawlingConfig.StickySession {
		t.Fatal("sticky_session lost")
	}
	if got.CrawlingConfig.Proxy != "http://microproxy-vdi1:8081" {
		t.Fatalf("proxy = %q", got.CrawlingConfig.Proxy)
	}
	if _, ok := got.Custom["vdi"]; !ok {
		t.Fatal("custom subtree lost")
	}

	// A malformed crawling_config is always fatal: it may hide the opt-in.
	if _, err := DecodeSourceSessionConfig(json.RawMessage(`{
		"crawling_config":{"site":"https://example.test/","sticky_session":"true"}
	}`)); err == nil {
		t.Fatal("malformed crawling_config must fail independently of other fields")
	}
}

func TestSourceSchemaSupportsStickySessionAndProxy(t *testing.T) {
	schemaData, err := os.ReadFile("../../schemas/crowler-source-config-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	crawling := schemaDocument.Properties["crawling_config"].Properties
	if len(crawling["proxy"]) == 0 {
		t.Fatal("crawling_config.proxy schema missing")
	}
	stickyRaw := crawling["sticky_session"]
	if len(stickyRaw) == 0 {
		t.Fatal("crawling_config.sticky_session schema missing")
	}
	var sticky struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(stickyRaw, &sticky); err != nil {
		t.Fatal(err)
	}
	if sticky.Type != "boolean" {
		t.Fatalf("sticky_session schema type = %q, want boolean", sticky.Type)
	}

	wrapper := json.RawMessage(fmt.Sprintf(
		`{"type":"object","properties":{"sticky_session":%s},"required":["sticky_session"]}`,
		string(stickyRaw)))
	schema := &jsonschema.Schema{}
	if err := schema.UnmarshalJSON(wrapper); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		document   string
		wantIssues bool
	}{
		{"true", `{"sticky_session":true}`, false},
		{"false", `{"sticky_session":false}`, false},
		{"null", `{"sticky_session":null}`, true},
		// The declared type is boolean. qri-io/jsonschema intentionally
		// coerces the strings "true"/"false" for boolean schemas, so the
		// schema-level string check is covered by the runtime decoder test
		// above instead of being asserted through this validator.
		{"number", `{"sticky_session":1}`, true},
		{"object", `{"sticky_session":{}}`, true},
		{"array", `{"sticky_session":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := schema.ValidateBytes(context.Background(), []byte(tc.document))
			if err != nil {
				t.Fatalf("schema validation error: %v", err)
			}
			if got := len(issues) != 0; got != tc.wantIssues {
				t.Fatalf("issues = %v, want issues = %v: %v", got, tc.wantIssues, issues)
			}
		})
	}

	proxySchema := &jsonschema.Schema{}
	if err := proxySchema.UnmarshalJSON(crawling["proxy"]); err != nil {
		t.Fatal(err)
	}
	if issues, err := proxySchema.ValidateBytes(context.Background(), []byte(fmt.Sprintf("%q", "http://microproxy-vdi1:8081"))); err != nil || len(issues) != 0 {
		t.Fatalf("valid proxy rejected: err=%v issues=%v", err, issues)
	}
}
