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
						map[string]interface{}{"media": map[string]interface{}{"pk": "111", "caption": "hello"}, "user": map[string]interface{}{"username": "alice"}},
						map[string]interface{}{"media": map[string]interface{}{"pk": "222", "caption": "world"}, "user": map[string]interface{}{"username": "bob"}},
					},
				}}},
			},
		},
	}
	prefix := "scraped_data.xhr[*].response_body.payload.items[*].media."
	pk := ExtractValuesWithContext(data, prefix+"pk")
	users := ExtractValuesWithContext(data, "scraped_data.xhr[*].response_body.payload.items[*].user.username")
	captions := ExtractValuesWithContext(data, prefix+"caption")
	if len(pk) != 2 || pk[0].SourcePath != "scraped_data.xhr[0].response_body.payload.items[0].media.pk" {
		t.Fatalf("unexpected provenance: %#v", pk)
	}
	if pk[0].ContextPath != "scraped_data.xhr[0].response_body.payload.items[0].media" {
		t.Fatalf("context boundary = %q", pk[0].ContextPath)
	}
	if pk[0].ContextRef == pk[1].ContextRef || pk[0].ContextRef == "" {
		t.Fatal("nested records must have distinct non-empty contexts")
	}
	for i := range pk {
		if pk[i].ContextRef != captions[i].ContextRef {
			t.Fatalf("media fields in record %d do not share context", i)
		}
		if pk[i].ContextRef == users[i].ContextRef {
			t.Fatalf("media and user fields in record %d unexpectedly share context", i)
		}
	}
	flat := ExtractValues(data, "scraped_data.xhr[*].response_body.payload.items[*].user.username")
	if len(flat) != 2 || flat[0] != "alice" || flat[1] != "bob" {
		t.Fatalf("flattened API changed: %#v", flat)
	}
}

func TestExtractValuesWithContextObjectWildcardsAndExplicitIndex(t *testing.T) {
	data := map[string]interface{}{"items": []interface{}{0.0, 1.0, 2.0}, "fields": map[string]interface{}{"pre_b": "b", "pre_a": "a"}}
	indexed := ExtractValuesWithContext(data, "items[2]")
	if len(indexed) != 1 || indexed[0].SourcePath != "items[2]" || indexed[0].ContextPath != "items" || indexed[0].Value != 2.0 {
		t.Fatalf("explicit index: %#v", indexed)
	}
	prefixed := ExtractValuesWithContext(data, "fields.pre*")
	if len(prefixed) != 2 || prefixed[0].SourcePath != "fields.pre_a" || prefixed[0].ContextPath != "fields" || prefixed[0].ContextRef != prefixed[1].ContextRef {
		t.Fatalf("prefix wildcard: %#v", prefixed)
	}
	if got := ExtractValuesWithContext(data, "missing[*].value"); len(got) != 0 {
		t.Fatalf("missing path returned %#v", got)
	}
}

func TestExtractValuesWithContextImmediateContainers(t *testing.T) {
	data := map[string]interface{}{
		"creator_name": "root",
		"scraped_data": map[string]interface{}{"creator_name": "Alice"},
		"crowler_meta": map[string]interface{}{"meta_data": map[string]interface{}{"username": "alice"}},
		"tags":         []interface{}{"one", "two"},
	}
	tests := []struct {
		path, source, context string
	}{
		{"scraped_data.creator_name", "scraped_data.creator_name", "scraped_data"},
		{"crowler_meta.meta_data.username", "crowler_meta.meta_data.username", "crowler_meta.meta_data"},
		{"creator_name", "creator_name", "$"},
		{"tags[*]", "tags[0]", "tags"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ExtractValuesWithContext(data, tt.path)
			if len(got) == 0 || got[0].SourcePath != tt.source || got[0].ContextPath != tt.context {
				t.Fatalf("got %#v, want source %q context %q", got, tt.source, tt.context)
			}
			expectedRef := makeExtracted(nil, "", tt.context).ContextRef
			if got[0].ContextRef != expectedRef || got[0].ContextRef == "" {
				t.Fatalf("context ref = %q, want SHA-256 of %q", got[0].ContextRef, tt.context)
			}
		})
	}
}

func TestExtractValuesWithContextObjectWildcard(t *testing.T) {
	data := map[string]interface{}{"objects": map[string]interface{}{
		"b": map[string]interface{}{"value": 2.0},
		"a": map[string]interface{}{"value": 1.0},
	}}
	got := ExtractValuesWithContext(data, "objects.*.value")
	if len(got) != 2 || got[0].SourcePath != "objects.a.value" || got[0].ContextPath != "objects.a" ||
		got[1].SourcePath != "objects.b.value" || got[1].ContextPath != "objects.b" {
		t.Fatalf("object wildcard provenance: %#v", got)
	}
}
