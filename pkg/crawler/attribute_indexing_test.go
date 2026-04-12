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
