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
						JsFiles: true,
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

func TestNewRuleset(t *testing.T) {
	name := "Test Ruleset"
	ruleset := NewRuleset(name)

	// Verify that the name is set correctly
	if ruleset.Name != name {
		t.Errorf("Expected ruleset name to be %q, got %q", name, ruleset.Name)
	}

	// Verify that the RuleGroups slice is initialized
	if ruleset.RuleGroups == nil {
		t.Error("Expected non-nil RuleGroups slice, got nil")
	}

	// Verify that the RuleGroups slice is empty
	if len(ruleset.RuleGroups) != 0 {
		t.Errorf("Expected RuleGroups slice to be empty, got %d elements", len(ruleset.RuleGroups))
	}
}

func TestIsValid(t *testing.T) {
	// Create a valid Ruleset
	validRuleset := NewRuleset("Valid Ruleset")
	validRuleset.RuleGroups = []RuleGroup{
		{
			GroupName: "Group1",
			ValidFrom: CustomTime{Time: time.Now()},
			ValidTo:   CustomTime{Time: time.Now().AddDate(1, 0, 0)},
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
	}
	// Verify that IsValid returns true for a valid Ruleset
	if !validRuleset.IsValid() {
		t.Errorf("Expected IsValid() to return true, got false")
	}

	// Create an invalid Ruleset with an empty name
	invalidRuleset := NewRuleset("")
	invalidRuleset.RuleGroups = []RuleGroup{
		{
			GroupName: "Group1",
			ValidFrom: CustomTime{Time: time.Now()},
			ValidTo:   CustomTime{Time: time.Now().AddDate(1, 0, 0)},
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
	}
	// Verify that IsValid returns false for an invalid Ruleset with an empty name
	if invalidRuleset.IsValid() {
		t.Errorf("Expected IsValid() to return false, got true")
	}

	// Create an invalid Ruleset with no RuleGroups
	invalidRulesetNoGroups := NewRuleset("Invalid Ruleset")
	// Verify that IsValid returns false for an invalid Ruleset with no RuleGroups
	if invalidRulesetNoGroups.IsValid() {
		t.Errorf("Expected IsValid() to return false, got true")
	}
}

func TestRulesetIsEmpty(t *testing.T) {
	// Create a non-empty Ruleset
	nonEmptyRuleset := NewRuleset("Non-empty Ruleset")
	nonEmptyRuleset.RuleGroups = []RuleGroup{
		{
			GroupName: "Group1",
			ValidFrom: CustomTime{Time: time.Now()},
			ValidTo:   CustomTime{Time: time.Now().AddDate(1, 0, 0)},
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
	}
	// Verify that IsEmpty returns false for a non-empty Ruleset
	if nonEmptyRuleset.IsEmpty() {
		t.Errorf("Expected IsEmpty() to return false, got true")
	}

	// Create an empty Ruleset
	emptyRuleset := NewRuleset("")
	// Verify that IsEmpty returns true for an empty Ruleset
	if !emptyRuleset.IsEmpty() {
		t.Errorf("Expected IsEmpty() to return true, got false")
	}
}

func TestRulesetGetAllRuleGroups(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			ValidFrom: CustomTime{Time: time.Now()},
			ValidTo:   CustomTime{Time: time.Now().AddDate(1, 0, 0)},
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
		{
			GroupName: "Group2",
			ValidFrom: CustomTime{Time: time.Now()},
			ValidTo:   CustomTime{Time: time.Now().AddDate(1, 0, 0)},
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule2",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedRuleGroups := ruleGroups
	actualRuleGroups := ruleset.GetAllRuleGroups()

	if len(actualRuleGroups) != len(expectedRuleGroups) {
		t.Errorf("Expected %d rule groups, got %d", len(expectedRuleGroups), len(actualRuleGroups))
	}

	for i := range expectedRuleGroups {
		if !reflect.DeepEqual(actualRuleGroups[i], expectedRuleGroups[i]) {
			t.Errorf("Expected rule group %v, got %v", expectedRuleGroups[i], actualRuleGroups[i])
		}
	}
}

func TestRulesetGetAllEnabledRuleGroups(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedEnabledRuleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule2",
				},
			},
		},
	}

	actualEnabledRuleGroups := ruleset.GetAllEnabledRuleGroups()

	if len(actualEnabledRuleGroups) != len(expectedEnabledRuleGroups) {
		t.Errorf("Expected %d enabled rule groups, got %d", len(expectedEnabledRuleGroups), len(actualEnabledRuleGroups))
	}

	for i, expected := range expectedEnabledRuleGroups {
		actual := actualEnabledRuleGroups[i]
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected enabled rule group %v, got %v", expected, actual)
		}
	}
}

func TestRulesetGetAllActionRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule1",
				},
			},
		},
		{
			GroupName: "Group2",
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule2",
				},
				{
					RuleName: "ActionRule3",
				},
			},
		},
		{
			GroupName: "Group3",
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule4",
				},
				{
					RuleName: "ActionRule5",
				},
				{
					RuleName: "ActionRule6",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedActionRules := []ActionRule{
		{
			RuleName: "ActionRule1",
		},
		{
			RuleName: "ActionRule2",
		},
		{
			RuleName: "ActionRule3",
		},
		{
			RuleName: "ActionRule4",
		},
		{
			RuleName: "ActionRule5",
		},
		{
			RuleName: "ActionRule6",
		},
	}

	actualActionRules := ruleset.GetAllActionRules()

	if len(actualActionRules) != len(expectedActionRules) {
		t.Errorf("Expected %d action rules, but got %d", len(expectedActionRules), len(actualActionRules))
	}

	for i, expected := range expectedActionRules {
		actual := actualActionRules[i]
		if expected.RuleName != actual.RuleName {
			t.Errorf("Expected action rule name %s, but got %s", expected.RuleName, actual.RuleName)
		}
	}
}

func TestRulesetGetAllScrapingRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	scrapingRules := []ScrapingRule{
		{
			RuleName: "Rule1",
		},
		{
			RuleName: "Rule2",
		},
	}
	ruleset.RuleGroups = []RuleGroup{
		{
			ScrapingRules: scrapingRules,
		},
	}
	expectedScrapingRules := scrapingRules
	actualScrapingRules := ruleset.GetAllScrapingRules()
	if len(actualScrapingRules) != len(expectedScrapingRules) {
		t.Errorf("Expected %d scraping rules, got %d", len(expectedScrapingRules), len(actualScrapingRules))
	}
	for i := range expectedScrapingRules {
		if expectedScrapingRules[i].RuleName != actualScrapingRules[i].RuleName {
			t.Errorf("Expected scraping rule name %s, got %s", expectedScrapingRules[i].RuleName, actualScrapingRules[i].RuleName)
		}
	}
}
func TestRulesetGetAllCrawlingRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "Rule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "Rule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedCrawlingRules := []CrawlingRule{
		{
			RuleName: "Rule1",
		},
		{
			RuleName: "Rule2",
		},
		{
			RuleName: "Rule3",
		},
	}

	// This will retrieve ALL the crawling rules, regardless of whether the rule group is enabled or not
	actualCrawlingRules := ruleset.GetAllCrawlingRules()

	if len(actualCrawlingRules) != len(expectedCrawlingRules) {
		t.Errorf("Expected %d crawling rules, but got %d", len(expectedCrawlingRules), len(actualCrawlingRules))
	}

	for i, expected := range expectedCrawlingRules {
		if !reflect.DeepEqual(actualCrawlingRules[i], expected) {
			t.Errorf("Expected crawling rule %v, but got %v", expected, actualCrawlingRules[i])
		}
	}
}

func TestRulesetGetAllDetectionRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	detectionRules := []DetectionRule{
		{
			RuleName: "Rule1",
		},
		{
			RuleName: "Rule2",
		},
	}
	ruleGroups := []RuleGroup{
		{
			GroupName:      "Group1",
			DetectionRules: detectionRules,
		},
		{
			GroupName:      "Group2",
			DetectionRules: detectionRules,
		},
	}
	ruleset.RuleGroups = ruleGroups

	actualDetectionRules := ruleset.GetAllDetectionRules()

	if len(actualDetectionRules) != 4 {
		t.Errorf("Expected %d detection rules, got %d", 4, len(actualDetectionRules))
	}
}

func TestRulesetGetAllEnabledScrapingRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "Rule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedRules := []ScrapingRule{
		{
			RuleName: "Rule1",
		},
		{
			RuleName: "Rule2",
		},
	}

	actualRules := ruleset.GetAllEnabledScrapingRules()

	if len(actualRules) != len(expectedRules) {
		t.Errorf("Expected %d enabled scraping rules, but got %d", len(expectedRules), len(actualRules))
	}

	for i, expected := range expectedRules {
		if !reflect.DeepEqual(actualRules[i], expected) {
			t.Errorf("Expected scraping rule %v, but got %v", expected, actualRules[i])
		}
	}
}

func TestRulesetGetAllEnabledActionRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedActionRules := []ActionRule{
		{
			RuleName: "ActionRule1",
		},
		{
			RuleName: "ActionRule2",
		},
	}

	actualActionRules := ruleset.GetAllEnabledActionRules()

	if len(actualActionRules) != len(expectedActionRules) {
		t.Errorf("Expected %d action rules, but got %d", len(expectedActionRules), len(actualActionRules))
	}

	for i, expected := range expectedActionRules {
		if !reflect.DeepEqual(actualActionRules[i], expected) {
			t.Errorf("Expected action rule %v, but got %v", expected, actualActionRules[i])
		}
	}
}
