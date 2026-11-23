package database

import (
	"encoding/json"
	"testing"
)

func TestSourceConfigTypeEmailSerialization(t *testing.T) {
	config := SourceCrawlingConfig{
		Site:       "imaps://mail.example.test",
		SourceType: SourceConfigTypeEmail,
	}

	serialized, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal email source config: %v", err)
	}

	const expected = `{"site":"imaps://mail.example.test","source_type":"email"}`
	if string(serialized) != expected {
		t.Fatalf("unexpected serialized email source config: got %s, want %s", serialized, expected)
	}
}

func TestSourceCrawlingConfigStickySessionStrictBoolean(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    *bool
		wantErr bool
	}{
		{"omitted", `{"site":"https://example.test/"}`, nil, false},
		{"true", `{"site":"https://example.test/","sticky_session":true,"proxy":"http://p:1"}`, boolValue(true), false},
		{"false", `{"site":"https://example.test/","sticky_session":false}`, boolValue(false), false},
		{"null", `{"site":"https://example.test/","sticky_session":null}`, nil, true},
		{"string", `{"site":"https://example.test/","sticky_session":"true"}`, nil, true},
		{"number", `{"site":"https://example.test/","sticky_session":1}`, nil, true},
		{"object", `{"site":"https://example.test/","sticky_session":{}}`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config SourceCrawlingConfig
			err := json.Unmarshal([]byte(tc.raw), &config)
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
				if config.StickySession != nil {
					t.Fatalf("sticky_session = %v, want nil", *config.StickySession)
				}
				return
			}
			if config.StickySession == nil || *config.StickySession != *tc.want {
				t.Fatalf("sticky_session = %v, want %v", config.StickySession, *tc.want)
			}
		})
	}
}

func boolValue(v bool) *bool { return &v }
