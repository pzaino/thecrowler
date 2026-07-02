package mail

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSourceConfigNotFound indicates that a crawler source did not contain a
// recognizable mail configuration.
var ErrSourceConfigNotFound = errors.New("mail: source configuration not found")

// ResolveSourceConfig returns an explicitly injected configuration when one is
// present, otherwise it extracts mail configuration from a source's JSON. The
// supported envelopes are mail/email/mail_config/email_config at the root or
// below the conventional custom object. This keeps knowledge of the mail
// configuration shape in package mail.
func ResolveSourceConfig(explicit *SourceConfig, raw json.RawMessage) (SourceConfig, error) {
	if explicit != nil {
		return *explicit, nil
	}
	if len(raw) == 0 {
		return SourceConfig{}, ErrSourceConfigNotFound
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return SourceConfig{}, fmt.Errorf("mail: decode source configuration: %w", err)
	}
	if config, ok, err := decodeSourceConfigObject(raw); ok || err != nil {
		return config, err
	}
	for _, key := range []string{"mail", "email", "mail_config", "email_config"} {
		if value := root[key]; len(value) != 0 {
			config, ok, err := decodeSourceConfigObject(value)
			if err != nil {
				return SourceConfig{}, fmt.Errorf("mail: decode %s configuration: %w", key, err)
			}
			if ok {
				return config, nil
			}
		}
	}
	if custom := root["custom"]; len(custom) != 0 {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(custom, &nested); err != nil {
			return SourceConfig{}, fmt.Errorf("mail: decode custom source configuration: %w", err)
		}
		for _, key := range []string{"mail", "email", "mail_config", "email_config"} {
			if value := nested[key]; len(value) != 0 {
				config, ok, err := decodeSourceConfigObject(value)
				if err != nil {
					return SourceConfig{}, fmt.Errorf("mail: decode custom.%s configuration: %w", key, err)
				}
				if ok {
					return config, nil
				}
			}
		}
	}
	return SourceConfig{}, ErrSourceConfigNotFound
}

func decodeSourceConfigObject(raw json.RawMessage) (SourceConfig, bool, error) {
	var probe struct {
		Connector json.RawMessage `json:"connector"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return SourceConfig{}, false, err
	}
	if len(probe.Connector) == 0 {
		return SourceConfig{}, false, nil
	}
	var config SourceConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return SourceConfig{}, true, err
	}
	return config, true, nil
}
