package ruleset

import (
	"reflect"
	"testing"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"

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
				PostProcessing: []PostProcessingStep{{Type: "remove"}, {Type: "replace"}, {Type: "plugin_call"}},
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
				PostProcessing: nil,
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
				PostProcessing: nil,
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

type MockRuleParser struct{}

func (m *MockRuleParser) ParseRules(_ *jsonschema.Schema, file string) ([]Ruleset, error) {
	// Return your mock data here
	return []Ruleset{}, nil
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

	actualActionRules := ruleset.GetAllEnabledActionRules("")

	if len(actualActionRules) != len(expectedActionRules) {
		t.Errorf("Expected %d action rules, but got %d", len(expectedActionRules), len(actualActionRules))
	}

	for i, expected := range expectedActionRules {
		if !reflect.DeepEqual(actualActionRules[i], expected) {
			t.Errorf("Expected action rule %v, but got %v", expected, actualActionRules[i])
		}
	}
}

func TestRulesetSetEnv(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			Env: []EnvSetting{
				{
					Key:    "key1",
					Values: "value1",
				},
				{
					Key:    "key2",
					Values: "value2",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
		},
	}
	ruleset.RuleGroups = ruleGroups

	CtxID := "test-context-id"
	cmn.KVStore = cmn.NewKeyValueStore()
	ruleset.SetEnv(CtxID)

	for _, kve := range cmn.KVStore.Keys(CtxID) {
		if kve != "key1" && kve != "key2" {
			t.Errorf("Expected CtxID to be %s, but got %s", CtxID, kve)
		}
	}
}

func TestRulesetGetAllEnabledCrawlingRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "CrawlingRule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "CrawlingRule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			CrawlingRules: []CrawlingRule{
				{
					RuleName: "CrawlingRule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedCrawlingRules := []CrawlingRule{
		{
			RuleName: "CrawlingRule1",
		},
		{
			RuleName: "CrawlingRule2",
		},
	}

	actualCrawlingRules := ruleset.GetAllEnabledCrawlingRules()

	if len(actualCrawlingRules) != len(expectedCrawlingRules) {
		t.Errorf("Expected %d enabled crawling rules, but got %d", len(expectedCrawlingRules), len(actualCrawlingRules))
	}

	for i, expected := range expectedCrawlingRules {
		if !reflect.DeepEqual(actualCrawlingRules[i], expected) {
			t.Errorf("Expected crawling rule %v, but got %v", expected, actualCrawlingRules[i])
		}
	}
}

func TestRulesetGetAllEnabledDetectionRules(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			DetectionRules: []DetectionRule{
				{
					RuleName: "DetectionRule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			DetectionRules: []DetectionRule{
				{
					RuleName: "DetectionRule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			DetectionRules: []DetectionRule{
				{
					RuleName: "DetectionRule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	expectedDetectionRules := []DetectionRule{
		{
			RuleName: "DetectionRule1",
		},
		{
			RuleName: "DetectionRule2",
		},
	}

	actualDetectionRules := ruleset.GetAllEnabledDetectionRules()

	if len(actualDetectionRules) != len(expectedDetectionRules) {
		t.Errorf("Expected %d enabled detection rules, but got %d", len(expectedDetectionRules), len(actualDetectionRules))
	}

	for i, expected := range expectedDetectionRules {
		if !reflect.DeepEqual(actualDetectionRules[i], expected) {
			t.Errorf("Expected detection rule %v, but got %v", expected, actualDetectionRules[i])
		}
	}
}

func TestGetRuleGroupByName2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name          string
		groupName     string
		expectedGroup RuleGroup
		expectError   bool
	}{
		{
			name:          "Valid group name",
			groupName:     "Group1",
			expectedGroup: ruleGroups[0],
			expectError:   false,
		},
		{
			name:          "Valid group name with different case",
			groupName:     "group1",
			expectedGroup: ruleGroups[0],
			expectError:   false,
		},
		{
			name:        "Non-existent group name",
			groupName:   "NonExistentGroup",
			expectError: true,
		},
		{
			name:        "Disabled group name",
			groupName:   "Group3",
			expectError: true,
		},
		{
			name:        "Empty group name",
			groupName:   "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, err := ruleset.GetRuleGroupByName(tt.groupName)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(group, tt.expectedGroup) {
					t.Errorf("Expected group %v, got %v", tt.expectedGroup, group)
				}
			}
		})
	}
}

func TestGetRulesGroupByURL2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			URL:       "https://example.com/group1",
			IsEnabled: true,
		},
		{
			GroupName: "Group2",
			URL:       "https://example.com/group2",
			IsEnabled: true,
		},
		{
			GroupName: "Group3",
			URL:       "https://example.com/group3",
			IsEnabled: false,
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		urlStr      string
		expected    RuleGroup
		expectError bool
	}{
		{
			name:        "Valid URL for enabled group",
			urlStr:      "https://example.com/group1",
			expected:    ruleGroups[0],
			expectError: false,
		},
		{
			name:        "Valid URL for another enabled group",
			urlStr:      "https://example.com/group2",
			expected:    ruleGroups[1],
			expectError: false,
		},
		{
			name:        "URL for disabled group",
			urlStr:      "https://example.com/group3",
			expectError: true,
		},
		{
			name:        "Non-existent URL",
			urlStr:      "https://example.com/nonexistent",
			expectError: true,
		},
		{
			name:        "Empty URL",
			urlStr:      "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, err := ruleset.GetRulesGroupByURL(tt.urlStr)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(group, tt.expected) {
					t.Errorf("Expected group %v, got %v", tt.expected, group)
				}
			}
		})
	}
}

func TestGetActionRuleByName2(t *testing.T) {
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

	tests := []struct {
		name        string
		ruleName    string
		expected    ActionRule
		expectError bool
	}{
		{
			name:        "Valid rule name in enabled group",
			ruleName:    "ActionRule1",
			expected:    ruleGroups[0].ActionRules[0],
			expectError: false,
		},
		{
			name:        "Valid rule name in another enabled group",
			ruleName:    "ActionRule2",
			expected:    ruleGroups[1].ActionRules[0],
			expectError: false,
		},
		{
			name:        "Rule name in disabled group",
			ruleName:    "ActionRule3",
			expectError: true,
		},
		{
			name:        "Non-existent rule name",
			ruleName:    "NonExistentRule",
			expectError: true,
		},
		{
			name:        "Empty rule name",
			ruleName:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ruleset.GetActionRuleByName(tt.ruleName)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(rule, tt.expected) {
					t.Errorf("Expected rule %v, got %v", tt.expected, rule)
				}
			}
		})
	}
}

func TestGetActionRuleByURL2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule1",
					URL:      "https://example.com/action1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule2",
					URL:      "https://example.com/action2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ActionRules: []ActionRule{
				{
					RuleName: "ActionRule3",
					URL:      "https://example.com/action3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		urlStr      string
		expected    ActionRule
		expectError bool
	}{
		{
			name:        "Valid URL for enabled group",
			urlStr:      "https://example.com/action1",
			expected:    ruleGroups[0].ActionRules[0],
			expectError: false,
		},
		{
			name:        "Valid URL for another enabled group",
			urlStr:      "https://example.com/action2",
			expected:    ruleGroups[1].ActionRules[0],
			expectError: false,
		},
		{
			name:        "URL for disabled group",
			urlStr:      "https://example.com/action3",
			expectError: true,
		},
		{
			name:        "Non-existent URL",
			urlStr:      "https://example.com/nonexistent",
			expectError: true,
		},
		{
			name:        "Empty URL",
			urlStr:      "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ruleset.GetActionRuleByURL(tt.urlStr)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(rule, tt.expected) {
					t.Errorf("Expected rule %v, got %v", tt.expected, rule)
				}
			}
		})
	}
}

func TestGetScrapingRuleByName2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		ruleName    string
		expected    ScrapingRule
		expectError bool
	}{
		{
			name:        "Valid rule name in enabled group",
			ruleName:    "ScrapingRule1",
			expected:    ruleGroups[0].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "Valid rule name in another enabled group",
			ruleName:    "ScrapingRule2",
			expected:    ruleGroups[1].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "Rule name in disabled group",
			ruleName:    "ScrapingRule3",
			expectError: true,
		},
		{
			name:        "Non-existent rule name",
			ruleName:    "NonExistentRule",
			expectError: true,
		},
		{
			name:        "Empty rule name",
			ruleName:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ruleset.GetScrapingRuleByName(tt.ruleName)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(rule, tt.expected) {
					t.Errorf("Expected rule %v, got %v", tt.expected, rule)
				}
			}
		})
	}
}

func TestGetScrapingRuleByPath2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path3"},
					},
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		path        string
		expected    ScrapingRule
		expectError bool
	}{
		{
			name:        "Valid path in enabled group",
			path:        "/path1",
			expected:    ruleGroups[0].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "Valid path in another enabled group",
			path:        "/path2",
			expected:    ruleGroups[1].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "Path in disabled group",
			path:        "/path3",
			expectError: true,
		},
		{
			name:        "Non-existent path",
			path:        "/nonexistent",
			expectError: true,
		},
		{
			name:        "Empty path",
			path:        "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ruleset.GetScrapingRuleByPath(tt.path)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(rule, tt.expected) {
					t.Errorf("Expected rule %v, got %v", tt.expected, rule)
				}
			}
		})
	}
}

func TestGetScrapingRuleByURL2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{URL: "https://example.com/path1"},
					},
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{URL: "https://example.com/path2"},
					},
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{URL: "https://example.com/path3"},
					},
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		urlStr      string
		expected    ScrapingRule
		expectError bool
	}{
		{
			name:        "Valid URL in enabled group",
			urlStr:      "https://example.com/path1",
			expected:    ruleGroups[0].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "Valid URL in another enabled group",
			urlStr:      "https://example.com/path2",
			expected:    ruleGroups[1].ScrapingRules[0],
			expectError: false,
		},
		{
			name:        "URL in disabled group",
			urlStr:      "https://example.com/path3",
			expectError: true,
		},
		{
			name:        "Non-existent URL",
			urlStr:      "https://example.com/nonexistent",
			expectError: true,
		},
		{
			name:        "Empty URL",
			urlStr:      "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ruleset.GetScrapingRuleByURL(tt.urlStr)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(rule, tt.expected) {
					t.Errorf("Expected rule %v, got %v", tt.expected, rule)
				}
			}
		})
	}
}

func TestGetEnabledRulesByGroupName2(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
				},
				{
					RuleName: "ScrapingRule2",
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule4",
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		groupName   string
		expected    []ScrapingRule
		expectEmpty bool
	}{
		{
			name:      "Enabled group with multiple rules",
			groupName: "Group1",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
				},
				{
					RuleName: "ScrapingRule2",
				},
			},
			expectEmpty: false,
		},
		{
			name:      "Enabled group with single rule",
			groupName: "Group2",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
				},
			},
			expectEmpty: false,
		},
		{
			name:        "Disabled group",
			groupName:   "Group3",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Non-existent group",
			groupName:   "NonExistentGroup",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ruleset.GetEnabledRulesByGroupName(tt.groupName)
			if tt.expectEmpty {
				if len(actual) != 0 {
					t.Errorf("Expected no rules, but got %d", len(actual))
				}
			} else {
				if !reflect.DeepEqual(actual, tt.expected) {
					t.Errorf("Expected rules %v, but got %v", tt.expected, actual)
				}
			}
		})
	}
}

func TestGetEnabledRulesByPath(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path3"},
					},
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		path        string
		expected    []ScrapingRule
		expectEmpty bool
	}{
		{
			name: "Valid path in enabled group",
			path: "/path1",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name: "Valid path in another enabled group",
			path: "/path2",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:        "Path in disabled group",
			path:        "/path3",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Non-existent path",
			path:        "/nonexistent",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Empty path",
			path:        "",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ruleset.GetEnabledRulesByPath(tt.path)
			if tt.expectEmpty {
				if len(actual) != 0 {
					t.Errorf("Expected no rules, but got %d", len(actual))
				}
			} else {
				if !reflect.DeepEqual(actual, tt.expected) {
					t.Errorf("Expected rules %v, but got %v", tt.expected, actual)
				}
			}
		})
	}
}

func TestGetEnabledScrapingRulesByPathHelper(t *testing.T) {
	ruleGroup := RuleGroup{
		GroupName: "Group1",
		IsEnabled: true,
		ScrapingRules: []ScrapingRule{
			{
				RuleName: "ScrapingRule1",
				PreConditions: []PreCondition{
					{Path: "/path1"},
				},
			},
			{
				RuleName: "ScrapingRule2",
				PreConditions: []PreCondition{
					{Path: "/path2"},
				},
			},
			{
				RuleName: "ScrapingRule3",
				PreConditions: []PreCondition{
					{Path: "/path1"},
				},
			},
		},
	}

	ruleset := NewRuleset("Test Ruleset")

	tests := []struct {
		name        string
		parsedPath  string
		expected    []ScrapingRule
		expectEmpty bool
	}{
		{
			name:       "Valid path with multiple rules",
			parsedPath: "/path1",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:       "Valid path with single rule",
			parsedPath: "/path2",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:        "Non-existent path",
			parsedPath:  "/nonexistent",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Empty path",
			parsedPath:  "",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ruleset.getEnabledScrapingRulesByPathHelper(ruleGroup, tt.parsedPath)
			if tt.expectEmpty {
				if len(actual) != 0 {
					t.Errorf("Expected no rules, but got %d", len(actual))
				}
			} else {
				if !reflect.DeepEqual(actual, tt.expected) {
					t.Errorf("Expected rules %v, but got %v", tt.expected, actual)
				}
			}
		})
	}
}

func TestGetEnabledScrapingRulesByPathAndGroup(t *testing.T) {
	ruleset := NewRuleset("Test Ruleset")
	ruleGroups := []RuleGroup{
		{
			GroupName: "Group1",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
		},
		{
			GroupName: "Group2",
			IsEnabled: true,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
		},
		{
			GroupName: "Group3",
			IsEnabled: false,
			ScrapingRules: []ScrapingRule{
				{
					RuleName: "ScrapingRule4",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
		},
	}
	ruleset.RuleGroups = ruleGroups

	tests := []struct {
		name        string
		path        string
		groupName   string
		expected    []ScrapingRule
		expectEmpty bool
	}{
		{
			name:      "Valid path and group with multiple rules",
			path:      "/path1",
			groupName: "Group1",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:      "Valid path and group with single rule",
			path:      "/path1",
			groupName: "Group2",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:        "Disabled group",
			path:        "/path1",
			groupName:   "Group3",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Non-existent group",
			path:        "/path1",
			groupName:   "NonExistentGroup",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Non-existent path",
			path:        "/nonexistent",
			groupName:   "Group1",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Empty path",
			path:        "",
			groupName:   "Group1",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ruleset.GetEnabledScrapingRulesByPathAndGroup(tt.path, tt.groupName)
			if tt.expectEmpty {
				if len(actual) != 0 {
					t.Errorf("Expected no rules, but got %d", len(actual))
				}
			} else {
				if !reflect.DeepEqual(actual, tt.expected) {
					t.Errorf("Expected rules %v, but got %v", tt.expected, actual)
				}
			}
		})
	}
}

func TestGetEnabledScrapingRulesByPathAndGroupHelper(t *testing.T) {
	ruleGroup := RuleGroup{
		GroupName: "Group1",
		IsEnabled: true,
		ScrapingRules: []ScrapingRule{
			{
				RuleName: "ScrapingRule1",
				PreConditions: []PreCondition{
					{Path: "/path1"},
				},
			},
			{
				RuleName: "ScrapingRule2",
				PreConditions: []PreCondition{
					{Path: "/path2"},
				},
			},
			{
				RuleName: "ScrapingRule3",
				PreConditions: []PreCondition{
					{Path: "/path1"},
				},
			},
		},
	}

	ruleset := NewRuleset("Test Ruleset")

	tests := []struct {
		name        string
		path        string
		expected    []ScrapingRule
		expectEmpty bool
	}{
		{
			name: "Valid path with multiple rules",
			path: "/path1",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule1",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
				{
					RuleName: "ScrapingRule3",
					PreConditions: []PreCondition{
						{Path: "/path1"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name: "Valid path with single rule",
			path: "/path2",
			expected: []ScrapingRule{
				{
					RuleName: "ScrapingRule2",
					PreConditions: []PreCondition{
						{Path: "/path2"},
					},
				},
			},
			expectEmpty: false,
		},
		{
			name:        "Non-existent path",
			path:        "/nonexistent",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
		{
			name:        "Empty path",
			path:        "",
			expected:    []ScrapingRule{},
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ruleset.getEnabledScrapingRulesByPathAndGroupHelper(ruleGroup, tt.path)
			if tt.expectEmpty {
				if len(actual) != 0 {
					t.Errorf("Expected no rules, but got %d", len(actual))
				}
			} else {
				if !reflect.DeepEqual(actual, tt.expected) {
					t.Errorf("Expected rules %v, but got %v", tt.expected, actual)
				}
			}
		})
	}
}
