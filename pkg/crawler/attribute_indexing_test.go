package crawler

import (
	"reflect"
	"testing"
)

func TestExtractSimple(t *testing.T) {
	data := map[string]interface{}{
		"details": map[string]interface{}{
			"title": "Hello",
		},
	}

	result := ExtractValues(data, "details.title")

	expected := []interface{}{"Hello"}

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestExtractArrayObjects(t *testing.T) {
	data := map[string]interface{}{
		"details": map[string]interface{}{
			"contacts": []interface{}{
				map[string]interface{}{"email": "a@test.com"},
				map[string]interface{}{"email": "b@test.com"},
			},
		},
	}

	result := ExtractValues(data, "details.contacts[*].email")

	expected := []interface{}{"a@test.com", "b@test.com"}

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestExtractArrayPrimitives(t *testing.T) {
	data := map[string]interface{}{
		"details": map[string]interface{}{
			"tags": []interface{}{"a", "b", "c"},
		},
	}

	result := ExtractValues(data, "details.tags[*]")

	expected := []interface{}{"a", "b", "c"}

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestExtractRootArray(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"email": "a@test.com"},
		map[string]interface{}{"email": "b@test.com"},
	}

	result := ExtractValues(data, "[*].email")

	expected := []interface{}{"a@test.com", "b@test.com"}

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestExtractMissingField(t *testing.T) {
	data := map[string]interface{}{
		"details": map[string]interface{}{},
	}

	result := ExtractValues(data, "details.title")

	if len(result) != 0 {
		t.Fatalf("expected empty result, got %v", result)
	}
}

func TestToString(t *testing.T) {
	cases := []struct {
		input    interface{}
		expected string
	}{
		{"abc", "abc"},
		{123, "123"},
		{int64(456), "456"},
		{true, "true"},
		{nil, ""},
	}

	for _, c := range cases {
		got := ToString(c.input)
		if got != c.expected {
			t.Fatalf("expected %s, got %s", c.expected, got)
		}
	}
}

func TestApplyNormalizers(t *testing.T) {
	input := "  Hello   WORLD  "

	result := ApplyNormalizers(input, []string{
		"trim",
		"collapse_spaces",
		"lowercase",
	})

	expected := "hello world"

	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestFixUTF8(t *testing.T) {
	// invalid UTF-8 sequence
	input := string([]byte{0xff, 0xfe, 'a', 0x00, 'b'})

	result := FixUTF8(input)

	if result == "" {
		t.Fatalf("expected non-empty sanitized string")
	}

	if containsNullByte(result) {
		t.Fatalf("string still contains null byte")
	}
}

func containsNullByte(s string) bool {
	for _, r := range s {
		if r == '\x00' {
			return true
		}
	}
	return false
}

func TestNormalizeURL(t *testing.T) {
	input := "HTTP://Example.COM/test#fragment"

	result := ApplyNormalizers(input, []string{"normalize_url"})

	expected := "http://example.com/test"

	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestExtractValuesWithContextNestedRecords(t *testing.T) {
	data := map[string]interface{}{
		"scraped_data": map[string]interface{}{
			"xhr": []interface{}{
				map[string]interface{}{"response_body": map[string]interface{}{"payload": map[string]interface{}{
					"items": []interface{}{
						map[string]interface{}{"media": map[string]interface{}{"pk": "111", "like_count": float64(10), "user": map[string]interface{}{"username": "alice"}}},
						map[string]interface{}{"media": map[string]interface{}{"pk": "222", "like_count": float64(20), "user": map[string]interface{}{"username": "bob"}}},
					},
				}}},
			},
		},
	}
	prefix := "scraped_data.xhr[*].response_body.payload.items[*].media."
	pk := ExtractValuesWithContext(data, prefix+"pk")
	users := ExtractValuesWithContext(data, prefix+"user.username")
	likes := ExtractValuesWithContext(data, prefix+"like_count")
	if len(pk) != 2 || pk[0].SourcePath != "scraped_data.xhr[0].response_body.payload.items[0].media.pk" {
		t.Fatalf("unexpected provenance: %#v", pk)
	}
	if pk[0].ContextPath != "scraped_data.xhr[0].response_body.payload.items[0]" {
		t.Fatalf("context boundary = %q", pk[0].ContextPath)
	}
	if pk[0].ContextRef == pk[1].ContextRef || pk[0].ContextRef == "" {
		t.Fatal("nested records must have distinct non-empty contexts")
	}
	for i := range pk {
		if pk[i].ContextRef != users[i].ContextRef || pk[i].ContextRef != likes[i].ContextRef {
			t.Fatalf("record %d fields do not share context", i)
		}
	}
	flat := ExtractValues(data, prefix+"user.username")
	if len(flat) != 2 || flat[0] != "alice" || flat[1] != "bob" {
		t.Fatalf("flattened API changed: %#v", flat)
	}
}

func TestExtractValuesWithContextObjectWildcardsAndExplicitIndex(t *testing.T) {
	data := map[string]interface{}{"items": []interface{}{0.0, 1.0, 2.0}, "fields": map[string]interface{}{"pre_b": "b", "pre_a": "a"}}
	indexed := ExtractValuesWithContext(data, "items[2]")
	if len(indexed) != 1 || indexed[0].SourcePath != "items[2]" || indexed[0].Value != 2.0 {
		t.Fatalf("explicit index: %#v", indexed)
	}
	prefixed := ExtractValuesWithContext(data, "fields.pre*")
	if len(prefixed) != 2 || prefixed[0].SourcePath != "fields.pre_a" || prefixed[0].ContextRef == prefixed[1].ContextRef {
		t.Fatalf("prefix wildcard: %#v", prefixed)
	}
	if got := ExtractValuesWithContext(data, "missing[*].value"); len(got) != 0 {
		t.Fatalf("missing path returned %#v", got)
	}
}
