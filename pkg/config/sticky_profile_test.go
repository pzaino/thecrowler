package config

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qri-io/jsonschema"
)

func TestStickySessionProfileParsesAndNormalizes(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    proxy_url: http://microproxy-vdi1:8080
    sticky_session:
      proxy_url: HTTP://MicroProxy-VDI1:8081/
      api_url: http://microproxy-vdi1:8094
      token_env: MICROPROXY_VDI1_SESSION_API_KEY
      provider: residential-sticky
`))
	if err != nil {
		t.Fatalf("ParseConfig rejected a valid sticky profile: %v", err)
	}
	profile := cfg.Selenium[0].StickySession
	if !profile.Configured() {
		t.Fatalf("profile not configured: %+v", profile)
	}
	if profile.ProxyURL != "http://microproxy-vdi1:8081" {
		t.Fatalf("proxy_url normalized to %q", profile.ProxyURL)
	}
	if profile.APIURL != "http://microproxy-vdi1:8094" {
		t.Fatalf("api_url = %q", profile.APIURL)
	}
}

func TestStickySessionProfileRejectsIncompleteOrUnsafeValues(t *testing.T) {
	tests := []struct {
		name    string
		proxy   string
		api     string
		token   string
		prov    string
		wantErr string
	}{
		{"valid", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", ""},
		{"missing provider", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", `""`, "requires proxy_url"},
		{"missing token", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, `""`, "residential-sticky", "requires proxy_url"},
		{"bad proxy scheme", `"ftp://microproxy-vdi1:21"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.proxy_url"},
		{"proxy credentials", `"http://user:pass@microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.proxy_url"},
		{"proxy path", `"http://microproxy-vdi1:8081/api"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.proxy_url"},
		{"proxy query", `"http://microproxy-vdi1:8081?x=1"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.proxy_url"},
		{"proxy no port", `"http://microproxy-vdi1"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.proxy_url"},
		{"api no port", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.api_url"},
		{"api userinfo", `"http://microproxy-vdi1:8081"`, `"http://user@microproxy-vdi1:8094"`, "MICROPROXY_KEY", "residential-sticky", "sticky_session.api_url"},
		{"bad token env", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, `"NOT-A-NAME"`, "residential-sticky", "token_env"},
		{"provider whitespace", `"http://microproxy-vdi1:8081"`, `"http://microproxy-vdi1:8094"`, "MICROPROXY_KEY", `"residential sticky"`, "provider"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
    sticky_session:
      proxy_url: ` + tc.proxy + `
      api_url: ` + tc.api + `
      token_env: ` + tc.token + `
      provider: ` + tc.prov + `
`
			_, err := ParseConfig([]byte(raw))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestStickySessionProfileAbsentIsValid(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
vdi:
  - name: vdi-1
    type: chrome
    host: selenium-vdi1
    port: 4444
`))
	if err != nil {
		t.Fatalf("ParseConfig rejected an absent profile: %v", err)
	}
	if !cfg.Selenium[0].StickySession.IsEmpty() {
		t.Fatalf("absent profile = %+v", cfg.Selenium[0].StickySession)
	}
}

func TestStickySessionSchemaContract(t *testing.T) {
	schemaData, err := os.ReadFile("../../schemas/crowler-config-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	stickyRaw := schemaDocument.Properties["vdi"].Items.Properties["sticky_session"]
	if len(stickyRaw) == 0 {
		t.Fatal("vdi[].sticky_session schema missing")
	}
	schema := &jsonschema.Schema{}
	if err := schema.UnmarshalJSON(stickyRaw); err != nil {
		t.Fatal(err)
	}
	valid := `{
		"proxy_url":"http://microproxy-vdi1:8081",
		"api_url":"http://microproxy-vdi1:8094",
		"token_env":"MICROPROXY_VDI1_SESSION_API_KEY",
		"provider":"residential-sticky"
	}`
	issues, err := schema.ValidateBytes(context.Background(), []byte(valid))
	if err != nil {
		t.Fatalf("schema validation error: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("valid profile rejected: %v", issues)
	}
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"missing api", `{"proxy_url":"http://microproxy-vdi1:8081","token_env":"KEY","provider":"p"}`},
		{"missing token", `{"proxy_url":"http://microproxy-vdi1:8081","api_url":"http://microproxy-vdi1:8094","provider":"p"}`},
		{"missing provider", `{"proxy_url":"http://microproxy-vdi1:8081","api_url":"http://microproxy-vdi1:8094","token_env":"KEY"}`},
		{"unknown field", `{"proxy_url":"http://microproxy-vdi1:8081","api_url":"http://microproxy-vdi1:8094","token_env":"KEY","provider":"p","token":"secret"}`},
		{"wrong type", `{"proxy_url":8081,"api_url":"http://microproxy-vdi1:8094","token_env":"KEY","provider":"p"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := schema.ValidateBytes(context.Background(), []byte(tc.doc))
			if err != nil {
				t.Fatalf("schema validation error: %v", err)
			}
			if len(issues) == 0 {
				t.Fatal("invalid profile passed schema validation")
			}
		})
	}
}
