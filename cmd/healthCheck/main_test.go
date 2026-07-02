package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestGenHealthURL(t *testing.T) {
	config = cfg.Config{}
	config.Crawler.Control.Host = "crawler.internal"
	config.Crawler.Control.Port = 8080
	config.API.Host = "api.internal"
	config.API.Port = 8081
	config.Events.Host = "events.internal"
	config.Events.Port = 8082
	config.API.SSLMode = cmn.EnableStr
	config.Events.SSLMode = cmn.EnableStr

	tests := []struct {
		name      string
		service   serviceType
		checkType string
		want      string
	}{
		{"crawler health over HTTP", crowler, healthEndpoint, "http://crawler.internal:8080/v1/health"},
		{"crawler ready normalized", crowler, " READY ", "http://crawler.internal:8080/v1/ready"},
		{"API health over HTTPS", api, healthEndpoint, "https://api.internal:8081/v1/health"},
		{"events ready over HTTPS", events, readyEndpoint, "https://events.internal:8082/v1/ready"},
		{"unknown check defaults to health", events, "live", "https://events.internal:8082/v1/health"},
		{"VDI is not currently checked", vdi, healthEndpoint, ""},
		{"unknown service type", serviceType(99), healthEndpoint, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := genHealthURL(tt.service, tt.checkType); got != tt.want {
				t.Fatalf("genHealthURL(%d, %q) = %q, want %q", tt.service, tt.checkType, got, tt.want)
			}
		})
	}
}

func TestGenHealthURLCrawlerHTTPSAndAPIHTTP(t *testing.T) {
	config = cfg.Config{}
	config.Crawler.Control.Host = "crawler"
	config.Crawler.Control.Port = 443
	config.Crawler.Control.SSLMode = cmn.EnableStr
	config.API.Host = "api"
	config.API.Port = 80

	if got := genHealthURL(crowler, healthEndpoint); got != "https://crawler:443/v1/health" {
		t.Fatalf("crawler URL = %q", got)
	}
	if got := genHealthURL(api, healthEndpoint); got != "http://api:80/v1/health" {
		t.Fatalf("API URL = %q", got)
	}
}

func TestParseService(t *testing.T) {
	for input, want := range map[string]serviceType{
		"crowler": crowler,
		" VDI ":   vdi,
		"API":     api,
		"events":  events,
	} {
		got, err := parseService(input)
		if err != nil || got != want {
			t.Errorf("parseService(%q) = (%d, %v), want (%d, nil)", input, got, err, want)
		}
	}

	if _, err := parseService("other"); err == nil || err.Error() != "unknown service: other" {
		t.Fatalf("parseService(other) error = %v", err)
	}
}

func TestRun(t *testing.T) {
	originalLoadConfig := loadConfig
	t.Cleanup(func() { loadConfig = originalLoadConfig })

	t.Run("invalid flags", func(t *testing.T) {
		if err := run([]string{"-unknown"}, http.DefaultClient); err == nil {
			t.Fatal("run() error = nil, want flag parsing error")
		}
	})

	t.Run("configuration error", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) {
			return cfg.Config{}, errors.New("broken config")
		}
		err := run(nil, http.DefaultClient)
		if err == nil || !strings.Contains(err.Error(), "load configuration: broken config") {
			t.Fatalf("run() error = %v", err)
		}
	})

	t.Run("unknown service", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
		err := run([]string{"-service", "missing"}, http.DefaultClient)
		if err == nil || err.Error() != "unknown service: missing" {
			t.Fatalf("run() error = %v", err)
		}
	})

	t.Run("VDI succeeds without request", func(t *testing.T) {
		loadConfig = func(string) (cfg.Config, error) { return cfg.Config{}, nil }
		if err := run([]string{"-service", "vdi"}, http.DefaultClient); err != nil {
			t.Fatalf("run() error = %v", err)
		}
	})
}

func TestRunHTTP(t *testing.T) {
	originalLoadConfig := loadConfig
	t.Cleanup(func() { loadConfig = originalLoadConfig })

	tests := []struct {
		name       string
		statusCode int
		wantErr    string
	}{
		{"healthy", http.StatusOK, ""},
		{"unhealthy", http.StatusServiceUnavailable, "returned 503 Service Unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/ready" {
					t.Errorf("request path = %q, want /v1/ready", r.URL.Path)
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")
			loadConfig = func(path string) (cfg.Config, error) {
				if path != "custom.yaml" {
					t.Errorf("config path = %q, want custom.yaml", path)
				}
				parts := strings.Split(host, ":")
				var loaded cfg.Config
				loaded.API.Host = parts[0]
				_, err := fmt.Sscanf(parts[1], "%d", &loaded.API.Port)
				return loaded, err
			}

			err := run([]string{"-config", "custom.yaml", "-service", "api", "-type", "ready"}, server.Client())
			if tt.wantErr == "" && err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("run() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunRequestError(t *testing.T) {
	originalLoadConfig := loadConfig
	t.Cleanup(func() { loadConfig = originalLoadConfig })
	loadConfig = func(string) (cfg.Config, error) {
		var loaded cfg.Config
		loaded.API.Host = "127.0.0.1"
		loaded.API.Port = 1
		return loaded, nil
	}

	err := run([]string{"-service", "api"}, &http.Client{Transport: errorTransport{}})
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("run() error = %v", err)
	}
}

type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("request failed")
}
