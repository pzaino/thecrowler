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

func TestParseAdvancedQueryPaginationModifiers(t *testing.T) {
	engine := NewSearcher(nil, cfg.Config{})
	tests := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
	}{
		{name: "colon modifiers with spaces", query: "login &limit:25 &offset:2", wantLimit: 25, wantOffset: 2},
		{name: "equals modifiers with spaces", query: "login &limit=30 &offset=3", wantLimit: 30, wantOffset: 3},
		{name: "colon modifiers attached", query: "login&limit:35&offset:4", wantLimit: 35, wantOffset: 4},
		{name: "equals modifiers attached", query: "login&limit=40&offset=5", wantLimit: 40, wantOffset: 5},
		{name: "offset only", query: "login &offset:2", wantLimit: 10, wantOffset: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := engine.ParseAdvancedQuery("SELECT * FROM table WHERE ", test.query, "")
			if err != nil {
				t.Fatalf("ParseAdvancedQuery() error = %v", err)
			}
			if parsed.Limit() != test.wantLimit || parsed.Offset() != test.wantOffset {
				t.Fatalf(
					"pagination = (%d, %d), want (%d, %d)",
					parsed.Limit(),
					parsed.Offset(),
					test.wantLimit,
					test.wantOffset,
				)
			}

			params := parsed.Params()
			if got := params[len(params)-2]; got != test.wantLimit {
				t.Errorf("limit parameter = %v, want %d", got, test.wantLimit)
			}
			if got := params[len(params)-1]; got != test.wantOffset {
				t.Errorf("offset parameter = %v, want %d", got, test.wantOffset)
			}
			if strings.Contains(parsed.SQL(), "offset") || strings.Contains(parsed.SQL(), "limit") {
				t.Errorf("SQL() contains pagination modifier as a search term: %q", parsed.SQL())
			}
		})
	}
}

func TestParseAdvancedQueryJoinsAdjacentURLConditions(t *testing.T) {
	engine := NewSearcher(nil, cfg.Config{})
	input := "https://www.cyaraportal.us/cyarawebidentity/login?ReturnUrl=/cyarawebidentity/connect/authorize/callback?client_id=cyara.web.portal&response_type=id_token%20token&scope=accounts%20openid%20profile&state=authentication-properties"

	parsed, err := engine.ParseAdvancedQuery("SELECT * FROM table WHERE ", input, "")
	if err != nil {
		t.Fatalf("ParseAdvancedQuery() error = %v", err)
	}

	sqlQuery := parsed.SQL()
	if strings.Contains(sqlQuery, ") (") {
		t.Fatalf("SQL() contains adjacent conditions without an operator: %q", sqlQuery)
	}
	if got := strings.Count(sqlQuery, ") AND ("); got != 3 {
		t.Fatalf("SQL() has %d implicit AND operators, want 3: %q", got, sqlQuery)
	}
	if !strings.Contains(sqlQuery, ") OR ((k.keyword = ") {
		t.Fatalf("SQL() does not preserve the keyword-group OR: %q", sqlQuery)
	}
}
