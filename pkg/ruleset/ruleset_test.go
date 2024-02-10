package ruleset

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var rulesets = []Ruleset{
	{
		Name: "example.com",
		RuleGroups: []RuleGroup{
			{
				GroupName: "Group1",
				ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
				ValidTo:   CustomTime{Time: time.Date(2029, time.December, 31, 0, 0, 0, 0, time.UTC)},
				IsEnabled: true,
				ScrapingRules: []ScrapingRule{
					{
						Path: "/articles",
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
						Path: "/news",
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
		Name: "another-example.com",
		RuleGroups: []RuleGroup{
			{
				GroupName: "GroupA",
				ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
				ValidTo:   CustomTime{Time: time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC)},
				IsEnabled: true,
				ScrapingRules: []ScrapingRule{
					{
						Path: "/products",
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
	tempFile := "./test_rules.yaml"

	// Call the ParseRules function with the temporary file
	sites, err := ParseRules(tempFile)
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

func (m *MockRuleParser) ParseRules(file string) ([]Ruleset, error) {
	// Return your mock data here
	return []Ruleset{}, nil
}

func TestInitializeLibrary(t *testing.T) {
	mockParser := &MockRuleParser{}
	engine, err := NewRuleEngineWithParser(mockParser, "./test_rules.yaml")
	if err != nil {
		t.Fatalf("InitializeLibrary returned an error: %v", err)
	}
	if engine == nil {
		t.Errorf("Expected non-nil engine, got nil")
	}
	// Additional assertions...
}
func TestNewRuleEngine(t *testing.T) {
	sites := []Ruleset{
		{
			Name: "example.com",
			RuleGroups: []RuleGroup{
				{
					GroupName: "Group1",
					ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
					ValidTo:   CustomTime{Time: time.Date(2029, time.December, 31, 0, 0, 0, 0, time.UTC)},
					IsEnabled: true,
					ScrapingRules: []ScrapingRule{
						{
							Path: "/articles",
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
							Path: "/news",
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
			Name: "another-example.com",
			RuleGroups: []RuleGroup{
				{
					GroupName: "GroupA",
					ValidFrom: CustomTime{Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
					ValidTo:   CustomTime{Time: time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC)},
					IsEnabled: true,
					ScrapingRules: []ScrapingRule{
						{
							Path: "/products",
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

	engine := NewRuleEngine(sites)

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
