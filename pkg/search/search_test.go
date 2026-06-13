package search

import (
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestParseAdvancedQueryMatchesLegacySemantics(t *testing.T) {
	engine := NewSearcher(nil, cfg.Config{})
	parsed, err := engine.ParseAdvancedQuery("SELECT * FROM table WHERE ", "title:value1 summary:value2 &limit:25 &offset:5", "")
	if err != nil {
		t.Fatalf("ParseAdvancedQuery() error = %v", err)
	}
	if !strings.Contains(parsed.SQL(), "k.keyword = $3 OR k.keyword = $4") {
		t.Fatalf("SQL() = %q, want keyword equality parameters", parsed.SQL())
	}
	want := []any{"%value1%", "%value2%", "value1", "value2", 25, 5}
	got := parsed.Params()
	if len(got) != len(want) {
		t.Fatalf("Params() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Params()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestSearchSelectsContentTemplateFromConfig(t *testing.T) {
	withoutContent := NewSearcher(nil, cfg.Config{})
	parsed, err := withoutContent.ParseAdvancedQuery(sqlSearchIndexBodyNoContent, "crowler", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parsed.SQL(), "'' AS content") {
		t.Fatalf("SQL() should suppress content: %q", parsed.SQL())
	}

	withConfig := cfg.Config{}
	withConfig.API.ReturnContent = true
	withContent := NewSearcher(nil, withConfig)
	parsed, err = withContent.ParseAdvancedQuery(sqlSearchIndexBody, "crowler", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parsed.SQL(), "wo.object_content AS content") {
		t.Fatalf("SQL() should return content: %q", parsed.SQL())
	}
}

func TestParsedQueryAccessorsReturnDefensiveParamsCopy(t *testing.T) {
	engine := NewSearcher(nil, cfg.Config{})
	parsed, err := engine.ParseAdvancedQuery("SELECT 1 WHERE ", "crowler", "")
	if err != nil {
		t.Fatal(err)
	}
	params := parsed.Params()
	params[0] = "changed"
	if parsed.Params()[0] == "changed" {
		t.Fatal("Params returned the ParsedQuery backing slice")
	}
	if parsed.Limit() != 10 || parsed.Offset() != 0 {
		t.Fatalf("pagination = (%d, %d), want (10, 0)", parsed.Limit(), parsed.Offset())
	}
}
