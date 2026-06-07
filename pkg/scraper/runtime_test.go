package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type recordedFailures struct{ failures []Failure }

func (r *recordedFailures) ReportFailure(_ context.Context, failure Failure) {
	r.failures = append(r.failures, failure)
}

func TestApplyRulePureExtractionWithZeroRuntime(t *testing.T) {
	driverImpl := &pageSourceDriver{source: `<html><body><h1>Reusable</h1></body></html>`}
	var driver vdi.WebDriver = driverImpl
	rule := &rs.ScrapingRule{RuleName: "pure", Elements: []rs.Element{{Key: "title", Selectors: []rs.Selector{{SelectorType: "css", Selector: "h1", Extract: rs.ItemToExtract{Type: "text"}}}}}}

	got, err := ApplyRule(context.Background(), nil, rule, &driver)
	if err != nil {
		t.Fatalf("ApplyRule() error = %v", err)
	}
	want := map[string]interface{}{"title": "Reusable"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyRule() = %#v, want %#v", got, want)
	}
}

func TestPostProcessingFailureIdentifiesStepWithoutSensitiveData(t *testing.T) {
	recorder := &recordedFailures{}
	step := &rs.PostProcessingStep{Type: "plugin_call", Details: map[string]interface{}{"plugin_name": "safe-name", "api_secret": "do-not-report"}}
	input := []byte(`{"page":"private page content"}`)

	_, err := ApplyPostProcessingStep(context.Background(), &Runtime{Failures: recorder}, "named-rule", 3, step, input)
	if err == nil {
		t.Fatal("ApplyPostProcessingStep() error = nil")
	}
	message := err.Error()
	for _, forbidden := range []string{"do-not-report", "private page content"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("error %q exposed %q", message, forbidden)
		}
	}
	if !strings.Contains(message, "named-rule") || !strings.Contains(message, "step 3") {
		t.Fatalf("error %q does not identify rule and step", message)
	}
	if len(recorder.failures) != 1 || recorder.failures[0].Name != "safe-name" {
		t.Fatalf("reported failures = %#v", recorder.failures)
	}
}

func TestHTTPClientTransformerRequiresBoundedClient(t *testing.T) {
	_, err := (HTTPClientTransformer{Client: &http.Client{}}).TransformHTTP(context.Background(), HTTPTransformRequest{URL: "http://example.invalid"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("TransformHTTP() error = %v, want timeout requirement", err)
	}
}

func TestExternalTransformationHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	step := &rs.PostProcessingStep{Type: "external_api", Details: map[string]interface{}{"api_url": server.URL, "timeout": 5}}
	done := make(chan error, 1)
	go func() {
		_, err := ApplyPostProcessingStep(ctx, &Runtime{HTTP: HTTPClientTransformer{Client: &http.Client{Timeout: 10 * time.Second}}}, "http-rule", 1, step, []byte(`{"value":1}`))
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ApplyPostProcessingStep() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("external transformation did not stop after cancellation")
	}
}

func TestExecuteRuleRunsWaitActionBeforeScrape(t *testing.T) {
	var events []string
	driverImpl := &orderedPageSourceDriver{
		pageSourceDriver: pageSourceDriver{source: `<html><body><h1>Ready</h1></body></html>`},
		events:           &events,
	}
	var driver vdi.WebDriver = driverImpl
	runtime := &Runtime{
		WaitCondition: func(context.Context, rs.WaitCondition) error {
			events = append(events, "action")
			return nil
		},
	}
	rule := &rs.ScrapingRule{
		RuleName:       "ordered",
		WaitConditions: []rs.WaitCondition{{ConditionType: "delay", Value: "0"}},
		Elements:       []rs.Element{{Key: "title", Selectors: []rs.Selector{{SelectorType: "css", Selector: "h1", Extract: rs.ItemToExtract{Type: "text"}}}}},
	}

	got, err := ExecuteRule(context.Background(), runtime, rule, &driver)
	if err != nil {
		t.Fatalf("ExecuteRule() error = %v", err)
	}
	if got != `"title":"Ready"` {
		t.Fatalf("ExecuteRule() = %q, want %q", got, `"title":"Ready"`)
	}
	wantEvents := []string{"action", "scrape"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("execution events = %#v, want %#v", events, wantEvents)
	}
}

type orderedPageSourceDriver struct {
	pageSourceDriver
	events *[]string
}

func (d *orderedPageSourceDriver) PageSource() (string, error) {
	*d.events = append(*d.events, "scrape")
	return d.pageSourceDriver.PageSource()
}
