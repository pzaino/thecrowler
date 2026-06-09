package config

import (
	"encoding/json"
	"testing"
)

func TestSourceTypeEmailSerialization(t *testing.T) {
	config := CrawlingConfig{
		Site:       "imaps://mail.example.test",
		SourceType: SourceTypeEmail,
	}

	serialized, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal email crawling config: %v", err)
	}

	const expected = `{"site":"imaps://mail.example.test","source_type":"email"}`
	if string(serialized) != expected {
		t.Fatalf("unexpected serialized email crawling config: got %s, want %s", serialized, expected)
	}
}
