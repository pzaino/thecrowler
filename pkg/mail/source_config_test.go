package mail

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResolveSourceConfig(t *testing.T) {
	config := sourceRunnerConfig()
	t.Run("explicit", func(t *testing.T) {
		resolved, err := ResolveSourceConfig(&config, json.RawMessage(`{"email":{"connector":{}}}`))
		if err != nil {
			t.Fatalf("ResolveSourceConfig() error = %v", err)
		}
		if resolved.Connector.Endpoint != config.Connector.Endpoint {
			t.Errorf("endpoint = %q, want %q", resolved.Connector.Endpoint, config.Connector.Endpoint)
		}
	})

	t.Run("custom email envelope", func(t *testing.T) {
		raw, err := json.Marshal(map[string]any{"custom": map[string]any{"email": config}})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := ResolveSourceConfig(nil, raw)
		if err != nil {
			t.Fatalf("ResolveSourceConfig() error = %v", err)
		}
		if resolved.Connector.Provider != "maildir" {
			t.Errorf("provider = %q, want maildir", resolved.Connector.Provider)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := ResolveSourceConfig(nil, json.RawMessage(`{"crawling_config":{"source_type":"email"}}`))
		if !errors.Is(err, ErrSourceConfigNotFound) {
			t.Fatalf("ResolveSourceConfig() error = %v, want ErrSourceConfigNotFound", err)
		}
	})
}
