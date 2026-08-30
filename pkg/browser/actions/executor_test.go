package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-auxiliaries/selenium"

	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type testDriver struct {
	vdi.WebDriver
	executeScript func(string, []interface{}) (interface{}, error)
	findElement   func(string, string) (vdi.WebElement, error)
}

func (d *testDriver) FindElement(by, value string) (vdi.WebElement, error) {
	if d.findElement == nil {
		return nil, errors.New("not found")
	}
	return d.findElement(by, value)
}

func (d *testDriver) ExecuteScript(script string, args []interface{}) (interface{}, error) {
	if d.executeScript == nil {
		return nil, nil
	}
	return d.executeScript(script, args)
}

type testElement struct {
	vdi.WebElement
	clicks   int
	location selenium.Point
}

func (e *testElement) Click() error {
	e.clicks++
	return nil
}

func (e *testElement) Location() (*selenium.Point, error) {
	return &e.location, nil
}

type testLookup struct {
	element      vdi.WebElement
	pluginScript string
	pluginExists bool
}

func (l testLookup) FindElement(context.Context, rules.Selector) (vdi.WebElement, error) {
	if l.element == nil {
		return nil, errors.New("not found")
	}
	return l.element, nil
}

func (l testLookup) PluginScript(context.Context, string) (string, bool, error) {
	return l.pluginScript, l.pluginExists, nil
}

func (testLookup) CallPlugin(context.Context, string, string, map[string]interface{}) error {
	return nil
}

func TestConditionsMatch(t *testing.T) {
	found := &testElement{}
	tests := []struct {
		name      string
		condition *rules.ActionCondition
		driver    *testDriver
		lookup    testLookup
		want      bool
		wantErr   bool
	}{
		{name: "element present", condition: &rules.ActionCondition{Type: "element", Selector: "#ready"}, driver: &testDriver{findElement: func(string, string) (vdi.WebElement, error) { return found, nil }}, want: true},
		{name: "element absent", condition: &rules.ActionCondition{Type: "element", Selector: "#missing"}, driver: &testDriver{}, want: false},
		{name: "language matches", condition: &rules.ActionCondition{Type: "language", Language: "en-US"}, driver: &testDriver{executeScript: func(string, []interface{}) (interface{}, error) { return "en-us", nil }}, want: true},
		{name: "plugin true", condition: &rules.ActionCondition{Type: "plugin_call", PluginCall: "ready"}, driver: &testDriver{executeScript: func(string, []interface{}) (interface{}, error) { return true, nil }}, lookup: testLookup{pluginScript: "return true", pluginExists: true}, want: true},
		{name: "plugin false", condition: &rules.ActionCondition{Type: "plugin_call", PluginCall: "ready"}, driver: &testDriver{executeScript: func(string, []interface{}) (interface{}, error) { return false, nil }}, lookup: testLookup{pluginScript: "return false", pluginExists: true}, want: false},
		{name: "unknown", condition: &rules.ActionCondition{Type: "mystery", Selector: "x"}, driver: &testDriver{}, wantErr: true},
		{name: "incomplete", condition: &rules.ActionCondition{Type: "element"}, driver: &testDriver{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConditionsMatch(context.Background(), &Runtime{WebDriver: tt.driver, Rules: tt.lookup}, tt.condition)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("ConditionsMatch() = (%v, %v), want (%v, error=%v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestWaitForConditionObservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{WebDriver: &testDriver{}}
	done := make(chan error, 1)
	go func() {
		done <- WaitForCondition(ctx, runtime, rules.WaitCondition{ConditionType: "delay", Value: "30"})
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForCondition() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForCondition did not stop after cancellation")
	}
}

func TestCancellationPreventsSeleniumFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	element := &testElement{location: selenium.Point{X: 10, Y: 20}}
	driver := &testDriver{executeScript: func(string, []interface{}) (interface{}, error) {
		cancel()
		return false, nil
	}}
	runtime := &Runtime{
		WebDriver: driver,
		Rules:     testLookup{element: element},
		Options: Options{HBS: HBSOptions{
			Enabled:          true,
			SeleniumFallback: true,
			Rbee:             RbeeEndpoints{Action: "http://rbee.test/v1/rb"},
		}},
	}
	rule := &rules.ActionRule{ActionType: "click", Selectors: []rules.Selector{{SelectorType: "css", Selector: "button"}}}

	err := ExecuteRule(ctx, runtime, rule)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteRule() error = %v, want context.Canceled", err)
	}
	if element.clicks != 0 {
		t.Fatalf("Selenium fallback clicked %d times after cancellation", element.clicks)
	}
}

func TestDisabledHBSUsesSeleniumWithoutRbeeConfiguration(t *testing.T) {
	element := &testElement{}
	runtime := &Runtime{
		WebDriver: &testDriver{},
		Rules:     testLookup{element: element},
		Options:   Options{HBS: HBSOptions{Enabled: false, SeleniumFallback: false}},
	}
	rule := &rules.ActionRule{ActionType: "click", Selectors: []rules.Selector{{SelectorType: "css", Selector: "button"}}}

	if err := ExecuteRule(context.Background(), runtime, rule); err != nil {
		t.Fatalf("ExecuteRule() error = %v", err)
	}
	if element.clicks != 1 {
		t.Fatalf("Selenium click count = %d, want 1", element.clicks)
	}
}

type inputElement struct {
	vdi.WebElement
	value string
}

func (e *inputElement) Click() error { return nil }
func (e *inputElement) SendKeys(value string) error {
	e.value += value
	return nil
}

func TestInputTextUsesRuleValueWithoutFilteringTheElement(t *testing.T) {
	element := &inputElement{}
	runtime := &Runtime{
		WebDriver: &testDriver{},
		Rules:     testLookup{element: element},
	}
	rule := &rules.ActionRule{
		ActionType: "input_text",
		Value:      "hermetic query",
		Selectors:  []rules.Selector{{SelectorType: "css", Selector: "#query"}},
	}

	if err := ExecuteRule(context.Background(), runtime, rule); err != nil {
		t.Fatalf("ExecuteRule() error = %v", err)
	}
	if element.value != "hermetic query" {
		t.Fatalf("input value = %q, want %q", element.value, "hermetic query")
	}
}
