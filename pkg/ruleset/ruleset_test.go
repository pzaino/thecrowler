package ruleset

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/qri-io/jsonschema"
)

const (
	goodTestFile = "../../schemas/ruleset-schema.json"
)

var rulesets = []Ruleset{
	{
		Name:          "Example Items Extraction Ruleset",
		FormatVersion: "1.0",
		RuleGroups: []RuleGroup{
			{
				GroupName: "Group1",
				ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
				ValidTo:   CustomTime{Time: time.Date(2029, time.December, 31, 0, 0, 0, 0, time.UTC)},
				IsEnabled: true,
				ScrapingRules: []ScrapingRule{
					{
						RuleName: "Articles",
						PreConditions: []PreCondition{
							{Path: "/articles"},
						},
						Elements: []Element{
							{
								Key: "title",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "h1.article-title"},
									{SelectorType: "xpath", Selector: "//h1[@class='article-title']"},
								},
							},
							{
								Key: "content",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "div.article-content"},
								},
							},
							{
								Key: "date",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "span.date"},
								},
							},
						},
						JsFiles:            true,
						TechnologyPatterns: []string{"jquery", "bootstrap"},
					},
				},
			},
			{
				GroupName: "Group2",
				ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
				ValidTo:   CustomTime{Time: time.Date(2021, time.December, 31, 0, 0, 0, 0, time.UTC)},
				IsEnabled: false,
				ScrapingRules: []ScrapingRule{
					{
						RuleName: "News",
						PreConditions: []PreCondition{
							{Path: "/news"},
						},
						Elements: []Element{
							{
								Key: "headline",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "h1.headline"},
								},
							},
							{
								Key: "summary",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "p.summary"},
								},
							},
						},
						JsFiles: false,
					},
				},
			},
		},
	},
	{
		Name:          "another-example.com",
		FormatVersion: "1.0",
		RuleGroups: []RuleGroup{
			{
				GroupName: "GroupA",
				ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
				ValidTo:   CustomTime{Time: time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC)},
				IsEnabled: true,
				ScrapingRules: []ScrapingRule{
					{
						RuleName: "Products",
						PreConditions: []PreCondition{
							{URL: "https://www.another-example.com", Path: "/products"},
						},
						Elements: []Element{
							{
								Key: "name",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "div.product-name"},
								},
							},
							{
								Key: "price",
								Selectors: []Selector{
									{SelectorType: "css", Selector: "span.price"},
								},
							},
						},
					},
				},
			},
		},
	},
}

func TestCustomTimeIsEmpty(t *testing.T) {
	// Create a non-empty CustomTime
	nonEmptyTime := time.Now()
	ct := CustomTime{Time: nonEmptyTime}

	// Verify that IsEmpty returns false for a non-empty CustomTime
	if ct.IsEmpty() {
		t.Errorf("Expected IsEmpty() to return false, got true")
	}

	// Create an empty CustomTime
	emptyTime := time.Time{}
	ct = CustomTime{Time: emptyTime}

	// Verify that IsEmpty returns true for an empty CustomTime
	if !ct.IsEmpty() {
		t.Errorf("Expected IsEmpty() to return true, got false")
	}
}
func TestParseRules(t *testing.T) {
	// Create a temporary YAML file for testing
	tempFile := "./test-ruleset.yaml"

	// Call the ParseRules function with the temporary file
	sites, err := BulkLoadRules(nil, tempFile)
	if err != nil {
		t.Fatalf("ParseRules returned an error: %v", err)
	}

	// Verify the parsed rules
	expectedSites := rulesets
	if diff := cmp.Diff(expectedSites, sites); diff != "" {
		t.Errorf("Parsed rules mismatch (-expected +actual):\n%s", diff)
	}
	/*
		if !reflect.DeepEqual(sites, expectedSites) {
			t.Errorf("Parsed rules do not match expected rules")
		}
	*/
}

type MockRuleParser struct{}

func (m *MockRuleParser) ParseRules(_ *jsonschema.Schema, file string) ([]Ruleset, error) {
	// Return your mock data here
	return []Ruleset{}, nil
}

func TestInitializeLibrary(t *testing.T) {
	mockParser := &MockRuleParser{}
	engine, err := NewRuleEngineWithParser(mockParser, "./test-ruleset.yaml")
	if err != nil {
		t.Fatalf("InitializeLibrary returned an error: %v", err)
	}
	if engine == nil {
		t.Errorf("Expected non-nil engine, got nil")
	}
}
func TestNewRuleEngine(t *testing.T) {
	sites := rulesets

	engine := NewRuleEngine(goodTestFile, sites)

	// Verify that the RuleEngine is initialized correctly
	if engine == nil {
		t.Fatalf("Expected non-nil RuleEngine, got nil")
	}
	if engine.Rulesets == nil {
		t.Fatalf("Expected non-nil Rulesets, got nil")
	}
	if !reflect.DeepEqual(engine.Rulesets, sites) {
		t.Errorf("Expected Rulesets to be %v, got %v", sites, engine.Rulesets)
	}
}
func TestFindRulesetByName(t *testing.T) {
	engine := NewRuleEngine(goodTestFile, rulesets)

	// Test case 1: Valid ruleset name
	name := "Example Items Extraction Ruleset"
	//expectedRuleset := &rulesets[0]
	ruleset, err := engine.FindRulesetByName(name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	/*
		if ruleset != expectedRuleset {
			t.Errorf("Expected ruleset %v, got %v", expectedRuleset, ruleset)
		}*/
	if ruleset == nil {
		t.Errorf("Expected non-nil ruleset, got nil")
	}

	// Test case 2: Empty ruleset name
	name = ""
	expectedError := "empty name provided"
	ruleset, err = engine.FindRulesetByName(name)
	if err == nil {
		t.Errorf("Expected error: %s, got nil", expectedError)
	}
	if err.Error() != expectedError {
		t.Errorf("Expected error: %s, got %v", expectedError, err)
	}
	if ruleset != nil {
		t.Errorf("Expected nil ruleset, got %v", ruleset)
	}

	// Test case 3: Non-existent ruleset name
	name = "Non-existent Ruleset"
	ruleset, err = engine.FindRulesetByName(name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if ruleset != nil {
		t.Errorf("Expected nil ruleset, got %v", ruleset)
	}
}

func TestDefaultRuleset(t *testing.T) {
	engine := NewEmptyRuleEngine(goodTestFile)

	// Load ruleset from a file
	err := engine.LoadRulesFromFile([]string{"../../rules/AcceptCookies-ruleset.json"})
	if err != nil {
		t.Fatalf("LoadRulesFromFile returned an error: %v", err)
	}

	/*
		// Create a JSON document from the engine:
		jsonBytes, err := engine.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON returned an error: %v", err)
		}

		// Pretty Print out the JSON document
		fmt.Println(string(jsonBytes))
	*/

	// Verify that the ruleset was loaded correctly
	ruleset, err := engine.FindRulesetByName("CookiePolicyAcceptanceMultilingual")
	if err != nil {
		t.Fatalf("FindRulesetByName returned an error: %v", err)
	}
	if ruleset == nil {
		t.Fatalf("Expected non-nil ruleset, got nil")
	}

}
