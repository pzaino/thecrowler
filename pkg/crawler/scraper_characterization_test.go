// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crawler

import (
	"errors"
	"reflect"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const scraperFixtureHTML = `<!doctype html>
<html><body>
  <h1 class="headline">Fixture Heading</h1>
  <p id="summary">XPath summary</p>
  <ul><li class="tag">first</li><li class="tag">second</li></ul>
</body></html>`

// pageSourceDriver deliberately makes Selenium lookups fail so ApplyRule's
// existing, browser-independent HTML fallback is exercised deterministically.
type pageSourceDriver struct {
	vdi.WebDriver
	html string
}

func (d *pageSourceDriver) SessionID() string { return "characterization-session" }
func (d *pageSourceDriver) Title() (string, error) {
	return "characterization fixture", nil
}
func (d *pageSourceDriver) PageSource() (string, error) { return d.html, nil }
func (d *pageSourceDriver) FindElements(string, string) ([]vdi.WebElement, error) {
	return nil, errors.New("fixture uses page-source fallback")
}

func scraperFixtureContext(t *testing.T) (*ProcessContext, *vdi.WebDriver) {
	t.Helper()
	driver := vdi.WebDriver(&pageSourceDriver{html: scraperFixtureHTML})
	ctx := &ProcessContext{
		SelID:  7,
		wd:     driver,
		source: &cdb.Source{ID: 42},
	}
	return ctx, &driver
}

func textSelector(selectorType, selector string, all bool) rs.Selector {
	return rs.Selector{
		SelectorType:          selectorType,
		Selector:              selector,
		Extract:               rs.ItemToExtract{Type: "text"},
		ExtractAllOccurrences: all,
	}
}

func TestApplyRuleCharacterizesCSSXPathTextAndMultipleValues(t *testing.T) {
	ctx, driver := scraperFixtureContext(t)
	rule := &rs.ScrapingRule{
		RuleName: "fixture extraction",
		Elements: []rs.Element{
			{Key: "heading", Selectors: []rs.Selector{
				textSelector("css", ".does-not-exist", false),
				textSelector(" CSS ", ".headline", false),
			}},
			{Key: "summary", Selectors: []rs.Selector{
				textSelector(" xpath ", "//p[@id='summary']", false),
			}},
			{Key: "tags", Selectors: []rs.Selector{
				textSelector("css", ".tag", true),
			}},
		},
		PostProcessing: []rs.PostProcessingStep{
			{Type: " replace ", Details: map[string]interface{}{"target": "Fixture Heading", "replacement": "Post-processed Heading"}},
		},
	}

	got, err := ApplyRule(ctx, rule, driver)
	if err != nil {
		t.Fatalf("ApplyRule returned error: %v", err)
	}
	want := map[string]interface{}{
		"heading": "Post-processed Heading",
		"summary": "XPath summary",
		"tags":    []interface{}{"first", "second"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyRule result = %#v, want %#v", got, want)
	}
}

func TestApplyRulesGroupCharacterizesExportedGroupEntryPoint(t *testing.T) {
	ctx, driver := scraperFixtureContext(t)
	group := &rs.RuleGroup{
		GroupName: "fixture group",
		ScrapingRules: []rs.ScrapingRule{{
			RuleName: "fixture rule",
			Elements: []rs.Element{{
				Key:       "heading",
				Selectors: []rs.Selector{textSelector("css", ".headline", false)},
			}},
		}},
	}

	got, err := ApplyRulesGroup(ctx, group, "ignored", driver)
	if err != nil {
		t.Fatalf("ApplyRulesGroup returned error: %v", err)
	}
	want := map[string]interface{}{"heading": "Fixture Heading"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyRulesGroup result = %#v, want %#v", got, want)
	}
}

func TestApplyPostProcessingStepCharacterizesPluginFreeSteps(t *testing.T) {
	tests := []struct {
		name string
		step rs.PostProcessingStep
		in   string
		want string
	}{
		{
			name: "replace",
			step: rs.PostProcessingStep{Type: "replace", Details: map[string]interface{}{"target": "alpha", "replacement": "beta"}},
			in:   `{"value":"alpha alpha"}`,
			want: `{"value":"beta beta"}`,
		},
		{
			name: "remove",
			step: rs.PostProcessingStep{Type: "remove", Details: map[string]interface{}{"target": "-remove"}},
			in:   `{"value":"keep-remove"}`,
			want: `{"value":"keep"}`,
		},
		{
			name: "clean",
			step: rs.PostProcessingStep{Type: "clean", Details: map[string]interface{}{"remove_extra_whitespace": true}},
			in:   "  spaced   text  ",
			want: "spaced text",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.in)
			ApplyPostProcessingStep(nil, &tc.step, &data)
			if got := string(data); got != tc.want {
				t.Fatalf("ApplyPostProcessingStep result = %q, want %q", got, tc.want)
			}
		})
	}
}
