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
	element vdi.WebElement
}

func (l testLookup) FindElement(context.Context, rules.Selector) (vdi.WebElement, error) {
	if l.element == nil {
		return nil, errors.New("not found")
	}
	return l.element, nil
}

func (testLookup) PluginScript(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (testLookup) CallPlugin(context.Context, string, string) error { return nil }

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
