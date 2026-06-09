package config

import (
	"encoding/json"
	"reflect"
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

func TestCrawlingConfigSiteValidationSupportsLocalEmailURIs(t *testing.T) {
	field, ok := reflect.TypeOf(CrawlingConfig{}).FieldByName("Site")
	if !ok {
		t.Fatal("CrawlingConfig.Site field not found")
	}

	const expected = "required,url|startswith=maildir:///|startswith=mbox:///"
	if got := field.Tag.Get("validate"); got != expected {
		t.Fatalf("unexpected Site validation: got %q, want %q", got, expected)
	}
}
