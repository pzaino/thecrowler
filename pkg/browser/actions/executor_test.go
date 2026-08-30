package actions

import (
	"context"
	"errors"
	"strings"
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
	clicks     int
	location   selenium.Point
	displayed  bool
	displayErr error
}

func (e *testElement) IsDisplayed() (bool, error) { return e.displayed, e.displayErr }

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

type retryLookup struct {
	attempts int
	element  vdi.WebElement
}

func (l *retryLookup) FindElement(context.Context, rules.Selector) (vdi.WebElement, error) {
	l.attempts++
	if l.attempts == 1 {
		return nil, errors.New("not found")
	}
	return l.element, nil
}

func (*retryLookup) PluginScript(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (*retryLookup) CallPlugin(context.Context, string, string, map[string]interface{}) error {
	return nil
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

func TestWaitForConditionBehavior(t *testing.T) {
	typedSelector := rules.Selector{SelectorType: "css", Selector: "#ready"}
	tests := []struct {
		name      string
		condition rules.WaitCondition
		lookup    testLookup
		wantErr   string
	}{
		{name: "presence", condition: rules.WaitCondition{ConditionType: rules.WaitConditionElementPresence, Selector: typedSelector}, lookup: testLookup{element: &testElement{}}},
		{name: "visible", condition: rules.WaitCondition{ConditionType: rules.WaitConditionElementVisible, Selector: typedSelector}, lookup: testLookup{element: &testElement{displayed: true}}},
		{name: "not visible", condition: rules.WaitCondition{ConditionType: rules.WaitConditionElementVisible, Selector: typedSelector}, lookup: testLookup{element: &testElement{}}, wantErr: "not visible"},
		{name: "plugin", condition: rules.WaitCondition{ConditionType: rules.WaitConditionPluginCall, Plugin: "ready"}, lookup: testLookup{pluginScript: "return true", pluginExists: true}},
		{name: "plugin lookup failure", condition: rules.WaitCondition{ConditionType: rules.WaitConditionPluginCall, Plugin: "missing"}, lookup: testLookup{}, wantErr: "plugin not found: missing"},
		{name: "missing selector", condition: rules.WaitCondition{ConditionType: rules.WaitConditionElementPresence}, lookup: testLookup{}, wantErr: "requires a typed selector"},
		{name: "missing delay", condition: rules.WaitCondition{ConditionType: rules.WaitConditionDelay}, lookup: testLookup{}, wantErr: "requires value"},
		{name: "missing plugin", condition: rules.WaitCondition{ConditionType: rules.WaitConditionPluginCall}, lookup: testLookup{}, wantErr: "requires plugin"},
		{name: "unknown", condition: rules.WaitCondition{ConditionType: "agent_call"}, lookup: testLookup{}, wantErr: "not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WaitForCondition(context.Background(), &Runtime{WebDriver: &testDriver{}, Rules: tt.lookup}, tt.condition)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("WaitForCondition() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("WaitForCondition() error = %v, want substring %q", err, tt.wantErr)
			}
		})
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

func TestExecuteRuleRetriesElementLookupWhenErrorsAreIgnored(t *testing.T) {
	element := &testElement{}
	lookup := &retryLookup{element: element}
	runtime := &Runtime{WebDriver: &testDriver{}, Rules: lookup}
	rule := &rules.ActionRule{
		ActionType: "click",
		Selectors:  []rules.Selector{{SelectorType: "css", Selector: "button"}},
		ErrorHandling: rules.ErrorHandling{
			Ignore:     true,
			RetryCount: 1,
		},
	}

	if err := ExecuteRule(context.Background(), runtime, rule); err != nil {
		t.Fatalf("ExecuteRule() error = %v", err)
	}
	if lookup.attempts != 2 {
		t.Fatalf("element lookup attempts = %d, want 2", lookup.attempts)
	}
	if element.clicks != 1 {
		t.Fatalf("element clicks = %d, want 1", element.clicks)
	}
}

func TestExecuteRuleFinalErrorHandling(t *testing.T) {
	ordinaryErrRule := func(ignore bool) *rules.ActionRule {
		return &rules.ActionRule{
			ActionType:    "unsupported",
			ErrorHandling: rules.ErrorHandling{Ignore: ignore},
		}
	}
	runtime := &Runtime{WebDriver: &testDriver{}}

	if err := ExecuteRule(context.Background(), runtime, ordinaryErrRule(false)); err == nil {
		t.Fatal("ExecuteRule() error = nil with ignore false")
	}
	if err := ExecuteRule(context.Background(), runtime, ordinaryErrRule(true)); err != nil {
		t.Fatalf("ExecuteRule() error = %v with ignore true", err)
	}
}

func TestExecuteRuleDoesNotIgnoreStopErrors(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()

	tests := []struct {
		name      string
		ctx       context.Context
		statusErr error
		want      error
	}{
		{name: "cancellation", ctx: canceled, want: context.Canceled},
		{name: "deadline", ctx: deadline, want: context.DeadlineExceeded},
		{name: "runtime stop", ctx: context.Background(), statusErr: ErrRuntimeStopped, want: ErrRuntimeStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &Runtime{WebDriver: &testDriver{}}
			if tt.statusErr != nil {
				runtime.CheckStatus = func(context.Context) error { return tt.statusErr }
			}
			rule := &rules.ActionRule{
				ActionType: "refresh",
				ErrorHandling: rules.ErrorHandling{
					Ignore:     true,
					RetryCount: 1,
				},
			}

			err := ExecuteRule(tt.ctx, runtime, rule)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ExecuteRule() error = %v, want %v", err, tt.want)
			}
		})
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
