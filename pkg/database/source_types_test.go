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
