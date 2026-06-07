package scraper

import (
	"reflect"
	"testing"
)

func TestPureTransforms(t *testing.T) {
	replaced, err := Replace(TransformRequest{Data: []byte("a-b-a"), Details: map[string]interface{}{"target": "a", "replacement": "x"}})
	if err != nil || string(replaced.Data) != "x-b-x" {
		t.Fatalf("Replace() = %q, %v", replaced.Data, err)
	}

	cleaned, err := Clean(TransformRequest{Data: []byte("<b>A 1</b>\n"), Details: map[string]interface{}{"remove_html": true, "remove_numbers": true, "remove_extra_whitespace": true}})
	if err != nil || string(cleaned.Data) != "A" {
		t.Fatalf("Clean() = %q, %v", cleaned.Data, err)
	}

	input := []byte(`{"name":"value"}`)
	validated, err := Validate(TransformRequest{Data: input, Details: map[string]interface{}{"keys": []string{"name"}}})
	if err != nil || !reflect.DeepEqual(validated.Data, input) {
		t.Fatalf("Validate() = %q, %v", validated.Data, err)
	}
}

func TestConvertHTMLPreservesOutputShape(t *testing.T) {
	result, err := ConvertHTML(HTMLConversionRequest{HTML: `<main id="content"><p>Hello</p><script>ignored()</script></main>`})
	if err != nil {
		t.Fatalf("ConvertHTML() error = %v", err)
	}
	if len(result.Data.Children) == 0 {
		t.Fatalf("ConvertHTML() returned no document children: %#v", result.Data)
	}
}
