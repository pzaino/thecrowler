package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestConfigConsoleInformationSeedProviderEndpoints(t *testing.T) {
	oldConfig := config
	config = cfg.Config{}
	config.InformationSeed.Providers = map[string]cfg.InformationSeedProviderConfig{
		"brave": {
			Provider:   "brave",
			Transport:  "http_json",
			Host:       "https://api.search.brave.com",
			APIKey:     "secret-key",
			Parameters: map[string]string{"api_token": "secret-token", "safe": "active"},
			Headers:    map[string]string{"Authorization": "Bearer secret", "Accept": "application/json"},
		},
		"public_json": {Provider: "public_json"},
	}
	t.Cleanup(func() { config = oldConfig })

	rec := httptest.NewRecorder()
	informationSeedConfigProvidersHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/information_seed/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var list InformationSeedConfigProvidersResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode providers list: %v", err)
	}
	if len(list.Providers) != 2 || list.Providers[0] != "brave" || list.Providers[1] != "public_json" {
		t.Fatalf("providers = %#v", list.Providers)
	}

	rec = httptest.NewRecorder()
	informationSeedConfigProviderHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/information_seed/providers/brave?name=brave", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail InformationSeedConfigProviderResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode provider detail: %v", err)
	}
	if detail.Provider.APIKey != "[REDACTED]" || detail.Provider.Parameters["api_token"] != "[REDACTED]" {
		t.Fatalf("credentials were not redacted: %#v", detail.Provider)
	}
	if detail.Provider.Parameters["safe"] != "active" || detail.Provider.Headers["Accept"] != "application/json" {
		t.Fatalf("non-secret values were unexpectedly changed: %#v", detail.Provider)
	}

	rec = httptest.NewRecorder()
	informationSeedConfigProviderHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/information_seed/providers/missing?name=missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestConfigConsoleVDIEndpoints(t *testing.T) {
	oldConfig := config
	config = cfg.Config{}
	config.Selenium = []cfg.Selenium{
		{Name: "chrome-us", Type: "chrome", Host: "selenium-us", Port: 4444, Headless: true, UseService: true},
		{Name: "firefox-eu", Type: "firefox", Host: "selenium-eu", Port: 4445},
	}
	t.Cleanup(func() { config = oldConfig })

	rec := httptest.NewRecorder()
	vdiConfigListHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/vdis", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var list VDIConfigListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode VDI list: %v", err)
	}
	if len(list.VDIs) != 2 || list.VDIs[0] != "chrome-us" || list.VDIs[1] != "firefox-eu" {
		t.Fatalf("VDIs = %#v", list.VDIs)
	}

	rec = httptest.NewRecorder()
	vdiConfigHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/vdis/chrome-us?name=chrome-us", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail VDIConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode VDI detail: %v", err)
	}
	if detail.VDI.Name != "chrome-us" || detail.VDI.Type != "chrome" || detail.VDI.Port != 4444 || !detail.VDI.Headless {
		t.Fatalf("VDI detail = %#v", detail.VDI)
	}

	rec = httptest.NewRecorder()
	vdiConfigHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/config/vdis/missing?name=missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
