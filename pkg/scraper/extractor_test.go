package scraper

import (
	"reflect"
	"testing"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type pageSourceDriver struct {
	vdi.WebDriver
	source string
}

func (d *pageSourceDriver) PageSource() (string, error)                           { return d.source, nil }
func (d *pageSourceDriver) FindElements(string, string) ([]vdi.WebElement, error) { return nil, nil }

func TestExtractorFallbackPreservesCSSXPathAndRegexShape(t *testing.T) {
	driverImpl := &pageSourceDriver{source: `<html><body><h1>Title</h1><a class="item">One</a><a class="item">Two</a><div>code-42</div></body></html>`}
	var driver vdi.WebDriver = driverImpl
	extractor := Extractor{Driver: &driver}

	tests := []struct {
		name string
		req  ExtractRequest
		want []interface{}
	}{
		{name: "css all", req: ExtractRequest{Selector: rs.Selector{SelectorType: "css", Selector: ".item"}, All: true}, want: []interface{}{"One", "Two"}},
		{name: "xpath first", req: ExtractRequest{Selector: rs.Selector{SelectorType: "xpath", Selector: "//h1"}}, want: []interface{}{"Title"}},
		{name: "regex capture", req: ExtractRequest{Selector: rs.Selector{SelectorType: "regex", Selector: `code-(\d+)`}}, want: []interface{}{"42"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractor.Extract(tt.req)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if !reflect.DeepEqual(got.Values, tt.want) {
				t.Fatalf("Extract() = %#v, want %#v", got.Values, tt.want)
			}
		})
	}
}

func TestExtractRegexReturnsCompileError(t *testing.T) {
	if _, err := ExtractRegex(RegexRequest{Pattern: "["}); err == nil {
		t.Fatal("ExtractRegex() error = nil, want compile error")
	}
}
