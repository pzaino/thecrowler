package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const maxHTTPTransformResponse = 16 << 20

// RuleError identifies a failed scraping rule without retaining extracted page content.
type RuleError struct {
	RuleName string
	Element  string
	Err      error
}

func (e *RuleError) Error() string {
	if e.Element == "" {
		return fmt.Sprintf("scraping rule %q failed: %v", e.RuleName, e.Err)
	}
	return fmt.Sprintf("scraping rule %q element %q failed: %v", e.RuleName, e.Element, e.Err)
}
func (e *RuleError) Unwrap() error { return e.Err }

// StepError identifies a failed post-processing step without retaining its details or data.
type StepError struct {
	RuleName string
	Index    int
	Type     string
	Err      error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("post-processing step %d (%s) for rule %q failed: %v", e.Index, e.Type, e.RuleName, e.Err)
}
func (e *StepError) Unwrap() error { return e.Err }

// ApplyRule applies a scraping rule with explicit optional runtime capabilities.
func ApplyRule(ctx context.Context, runtime *Runtime, rule *rs.ScrapingRule, webPage *vdi.WebDriver) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt := runtimeOrZero(runtime)
	if rule == nil {
		return nil, errors.New("scraping rule is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	extractor := Extractor{Driver: webPage, MatchValue: rt.MatchValue}
	extractor.ExtractExternal = func(selector rs.Selector) ([]interface{}, error) {
		return extractExternal(ctx, rt, rule.RuleName, selector)
	}

	extractedData := make(map[string]interface{}, len(rule.Elements))
	var collectedErrors []error
	for _, element := range rule.Elements {
		if err := ctx.Err(); err != nil {
			return extractedData, err
		}
		var allExtracted []interface{}
		for _, selector := range element.Selectors {
			result, err := extractor.Extract(ExtractRequest{Selector: selector, All: selector.ExtractAllOccurrences})
			if err != nil {
				collectedErrors = append(collectedErrors, err)
				continue
			}
			allExtracted = appendExtracted(allExtracted, result.Values, &collectedErrors)
			if len(allExtracted) > 0 {
				break
			}
		}
		if len(allExtracted) == 0 && element.Critical {
			err := &RuleError{RuleName: rule.RuleName, Element: element.Key, Err: errors.New("critical element not found")}
			reportFailure(ctx, rt, Failure{RuleName: rule.RuleName, Step: -1, Kind: "element", Name: element.Key, Err: err})
			if len(collectedErrors) > 0 {
				err.Err = errors.Join(err.Err, errors.Join(collectedErrors...))
			}
			return extractedData, err
		}
		if element.TransformHTMLToJSON {
			allExtracted = transformExtractedHTML(allExtracted)
		}
		if len(allExtracted) == 1 {
			extractedData[element.Key] = allExtracted[0]
		} else {
			extractedData[element.Key] = allExtracted
		}
	}

	if len(rule.PostProcessing) > 0 {
		data, err := json.Marshal(extractedData)
		if err != nil {
			return extractedData, &RuleError{RuleName: rule.RuleName, Err: errors.New("marshal extracted result")}
		}
		data, err = applyPostProcessing(ctx, rt, rule.RuleName, rule.PostProcessing, data)
		if err != nil {
			return extractedData, err
		}
		data = unwrapDoubleObject(data)
		if err := json.Unmarshal(data, &extractedData); err != nil {
			return extractedData, &RuleError{RuleName: rule.RuleName, Err: errors.New("post-processing returned a non-object result")}
		}
	}
	return extractedData, nil
}

func extractExternal(ctx context.Context, runtime Runtime, ruleName string, selector rs.Selector) ([]interface{}, error) {
	typ := strings.ToLower(strings.TrimSpace(selector.SelectorType))
	switch typ {
	case "plugin_call":
		if runtime.Plugins != nil {
			value, err := runtime.Plugins.RunPlugin(ctx, PluginRequest{Name: selector.Selector, Caller: "scraping.selector", RuleName: ruleName, Step: -1})
			return normalizeExternalValue(value), err
		}
		if runtime.RuleCalls != nil {
			result, err := runtime.RuleCalls.CallRule(ctx, RuleCallRequest{Kind: typ, Name: selector.Selector, Caller: "scraping.selector", RuleName: ruleName, Step: -1})
			return normalizeExternalValue(result.Value), err
		}
		return nil, UnavailableCapabilityError{Capability: "plugin"}
	case "agent_call":
		if selector.AgentCall == nil {
			return nil, errors.New("agent selector is missing agent_call details")
		}
		name := selector.AgentCall.AgentName
		if runtime.Agents != nil {
			value, err := runtime.Agents.RunAgent(ctx, AgentRequest{Name: name, Parameters: selector.AgentCall.Params, Caller: "scraping.selector", RuleName: ruleName, Step: -1})
			return normalizeExternalValue(value), err
		}
		if runtime.RuleCalls != nil {
			result, err := runtime.RuleCalls.CallRule(ctx, RuleCallRequest{Kind: typ, Name: name, Parameters: selector.AgentCall.Params, Timeout: seconds(selector.AgentCall.Timeout), Caller: "scraping.selector", RuleName: ruleName, Step: -1})
			return normalizeExternalValue(result.Value), err
		}
		return nil, UnavailableCapabilityError{Capability: "agent"}
	default:
		return nil, fmt.Errorf("unsupported external selector type %q", selector.SelectorType)
	}
}

func normalizeExternalValue(value interface{}) []interface{} {
	if value == nil {
		return nil
	}
	if values, ok := value.([]interface{}); ok {
		return values
	}
	return []interface{}{value}
}

func appendExtracted(dst, values []interface{}, errs *[]error) []interface{} {
	for _, value := range values {
		switch v := value.(type) {
		case string, float64:
			dst = append(dst, v)
		case map[string]interface{}:
			encoded, err := json.Marshal(v)
			if err != nil {
				*errs = append(*errs, err)
				continue
			}
			dst = append(dst, string(encoded))
		case []interface{}:
			dst = appendExtracted(dst, v, errs)
		default:
			*errs = append(*errs, fmt.Errorf("unsupported extracted value type %T", value))
		}
	}
	return dst
}

func transformExtractedHTML(values []interface{}) []interface{} {
	transformed := make([]interface{}, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			transformed = append(transformed, value)
			continue
		}
		result, err := ConvertHTML(HTMLConversionRequest{HTML: text})
		if err != nil {
			transformed = append(transformed, text)
			continue
		}
		transformed = append(transformed, result.Data)
	}
	return transformed
}

// ApplyRulesGroup applies all scraping rules and group post-processing.
func ApplyRulesGroup(ctx context.Context, runtime *Runtime, group *rs.RuleGroup, webPage *vdi.WebDriver) (result map[string]interface{}, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt := runtimeOrZero(runtime)
	if group == nil {
		return nil, errors.New("rule group is required")
	}
	result = make(map[string]interface{})
	if rt.EnvCleaner != nil {
		defer func() {
			if cleanErr := rt.EnvCleaner.ClearEnvironment(ctx); err == nil && cleanErr != nil {
				err = cleanErr
			}
		}()
	}
	if rt.EnvSetter != nil {
		for _, setting := range group.Env {
			if setErr := rt.EnvSetter.SetEnvironment(ctx, setting.Key, setting.Values, environmentProperties(setting.Properties)); setErr != nil {
				return result, setErr
			}
		}
	}
	for i := range group.ScrapingRules {
		data, applyErr := ApplyRule(ctx, &rt, &group.ScrapingRules[i], webPage)
		mergeResult(result, data)
		if applyErr != nil {
			return result, applyErr
		}
	}
	if len(group.PostProcessing) > 0 {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return result, marshalErr
		}
		data, applyErr := applyPostProcessing(ctx, rt, group.GroupName, group.PostProcessing, data)
		if applyErr != nil {
			return result, applyErr
		}
		var processed map[string]interface{}
		if unmarshalErr := json.Unmarshal(unwrapDoubleObject(data), &processed); unmarshalErr != nil {
			return result, &RuleError{RuleName: group.GroupName, Err: errors.New("group post-processing returned a non-object result")}
		}
		mergeResult(result, processed)
	}
	return result, nil
}

func mergeResult(dst, src map[string]interface{}) {
	for key, value := range src {
		if previous, exists := dst[key]; exists {
			slice, ok := previous.([]interface{})
			if !ok {
				slice = []interface{}{previous}
			}
			dst[key] = append(slice, value)
		} else {
			dst[key] = value
		}
	}
}

// ApplyPostProcessing applies a sequence of post-processing steps.
func ApplyPostProcessing(ctx context.Context, runtime *Runtime, ruleName string, steps []rs.PostProcessingStep, data []byte) ([]byte, error) {
	return applyPostProcessing(ctx, runtimeOrZero(runtime), ruleName, steps, data)
}

func applyPostProcessing(ctx context.Context, runtime Runtime, ruleName string, steps []rs.PostProcessingStep, data []byte) ([]byte, error) {
	for index := range steps {
		var err error
		data, err = applyPostProcessingStep(ctx, runtime, ruleName, index, &steps[index], data)
		if err != nil {
			return data, err
		}
	}
	return data, nil
}

// ApplyPostProcessingStep applies one post-processing step.
func ApplyPostProcessingStep(ctx context.Context, runtime *Runtime, ruleName string, index int, step *rs.PostProcessingStep, data []byte) ([]byte, error) {
	return applyPostProcessingStep(ctx, runtimeOrZero(runtime), ruleName, index, step, data)
}

func applyPostProcessingStep(ctx context.Context, runtime Runtime, ruleName string, index int, step *rs.PostProcessingStep, data []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	if step == nil {
		return data, stepFailure(ctx, runtime, ruleName, index, "unknown", "", errors.New("step is required"))
	}
	stepType := strings.ToLower(strings.TrimSpace(step.Type))
	var result TransformResult
	var err error
	switch stepType {
	case "replace":
		result, err = Replace(TransformRequest{Data: data, Details: step.Details})
	case "remove":
		result, err = Remove(TransformRequest{Data: data, Details: step.Details})
	case "validate":
		result, err = Validate(TransformRequest{Data: data, Details: step.Details})
	case "clean":
		result, err = Clean(TransformRequest{Data: data, Details: step.Details})
	case "set_env":
		result.Data = data
		err = setEnvironment(ctx, runtime, step, data)
	case "plugin_call":
		result.Data, err = runPluginStep(ctx, runtime, ruleName, index, step, data)
	case "agent_call":
		result.Data, err = runAgentStep(ctx, runtime, ruleName, index, step, data)
	case "external_api":
		result.Data, err = runHTTPTransform(ctx, runtime, step, data)
	case "transform":
		transformType, _ := stringDetail(step.Details, "transform_type")
		switch strings.ToLower(strings.TrimSpace(transformType)) {
		case "api":
			result.Data, err = runHTTPTransform(ctx, runtime, step, data)
		case "plugin_call":
			result.Data, err = runPluginStep(ctx, runtime, ruleName, index, step, data)
		default:
			err = fmt.Errorf("unsupported transform type %q", transformType)
		}
	default:
		err = fmt.Errorf("unsupported post-processing step type %q", stepType)
	}
	if err != nil {
		name, _ := stringDetail(step.Details, "plugin_name")
		if stepType == "agent_call" && step.AgentCall != nil {
			name = step.AgentCall.AgentName
		}
		return data, stepFailure(ctx, runtime, ruleName, index, stepType, name, err)
	}
	return result.Data, nil
}

func stepFailure(ctx context.Context, runtime Runtime, ruleName string, index int, kind, name string, err error) error {
	wrapped := &StepError{RuleName: ruleName, Index: index, Type: kind, Err: err}
	reportFailure(ctx, runtime, Failure{RuleName: ruleName, Step: index, Kind: kind, Name: name, Err: wrapped})
	return wrapped
}

func setEnvironment(ctx context.Context, runtime Runtime, step *rs.PostProcessingStep, data []byte) error {
	if runtime.EnvSetter == nil {
		return UnavailableCapabilityError{Capability: "environment setter"}
	}
	key, err := stringDetail(step.Details, "env_key")
	if err != nil {
		return err
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		return errors.New("environment input is not a JSON object")
	}
	value, exists := document[key]
	if !exists {
		return fmt.Errorf("environment source key %q was not found", key)
	}
	properties := EnvironmentProperties{}
	if raw, ok := step.Details["env_properties"].(map[string]interface{}); ok {
		properties.Persistent, _ = raw["persistent"].(bool)
		properties.Static, _ = raw["static"].(bool)
	}
	return runtime.EnvSetter.SetEnvironment(ctx, key, value, properties)
}

func runPluginStep(ctx context.Context, runtime Runtime, ruleName string, index int, step *rs.PostProcessingStep, data []byte) ([]byte, error) {
	if runtime.Plugins == nil {
		return nil, UnavailableCapabilityError{Capability: "plugin"}
	}
	name, err := stringDetail(step.Details, "plugin_name")
	if err != nil {
		return nil, err
	}
	parameters, _ := step.Details["parameters"].(map[string]interface{})
	value, err := runtime.Plugins.RunPlugin(ctx, PluginRequest{Name: name, Data: data, Parameters: parameters, Caller: "scraping.post_processing", RuleName: ruleName, Step: index})
	if err != nil {
		return nil, err
	}
	return marshalRuntimeResult(value)
}

func runAgentStep(ctx context.Context, runtime Runtime, ruleName string, index int, step *rs.PostProcessingStep, data []byte) ([]byte, error) {
	if step.AgentCall == nil {
		return nil, errors.New("agent_call details are required")
	}
	var value interface{}
	var err error
	if runtime.Agents != nil {
		value, err = runtime.Agents.RunAgent(ctx, AgentRequest{Name: step.AgentCall.AgentName, Data: data, Parameters: step.AgentCall.Params, Caller: "scraping.post_processing", RuleName: ruleName, Step: index})
	} else if runtime.RuleCalls != nil {
		var result RuleCallResult
		result, err = runtime.RuleCalls.CallRule(ctx, RuleCallRequest{Kind: "agent_call", Name: step.AgentCall.AgentName, Parameters: step.AgentCall.Params, Timeout: seconds(step.AgentCall.Timeout), Caller: "scraping.post_processing", RuleName: ruleName, Step: index})
		value = result.Value
	} else {
		return nil, UnavailableCapabilityError{Capability: "agent"}
	}
	if err != nil {
		return nil, err
	}
	var base map[string]interface{}
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, errors.New("agent input is not a JSON object")
	}
	switch strings.ToLower(strings.TrimSpace(step.AgentCall.MergeStrategy)) {
	case "merge":
		if values, ok := value.(map[string]interface{}); ok {
			for key, item := range values {
				base[key] = item
			}
		}
	case "append":
		existing, _ := base["_agent_results"].([]interface{})
		base["_agent_results"] = append(existing, value)
	case "ignore":
	default:
		base["_agent_result"] = value
	}
	return json.Marshal(base)
}

func marshalRuntimeResult(value interface{}) ([]byte, error) {
	switch result := value.(type) {
	case []byte:
		if !json.Valid(result) {
			return nil, errors.New("runtime returned invalid JSON")
		}
		return result, nil
	case string:
		if !json.Valid([]byte(result)) {
			return nil, errors.New("runtime returned invalid JSON")
		}
		return []byte(strings.TrimSpace(result)), nil
	default:
		return json.Marshal(result)
	}
}

func runHTTPTransform(ctx context.Context, runtime Runtime, step *rs.PostProcessingStep, data []byte) ([]byte, error) {
	if runtime.HTTP == nil {
		return nil, UnavailableCapabilityError{Capability: "HTTP transformer"}
	}
	rawURL, err := stringDetail(step.Details, "api_url")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(rawURL, "://") {
		scheme := "http"
		if mode, _ := stringDetail(step.Details, "ssl_mode"); mode != "" && mode != "disable" && mode != "disabled" {
			scheme = "https"
		}
		rawURL = scheme + "://" + rawURL
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return nil, errors.New("invalid external transformation URL")
	}
	timeout := detailDuration(step.Details, "timeout", 15*time.Second)
	body, err := buildHTTPTransformBody(step.Details, data)
	if err != nil {
		return nil, err
	}
	return runtime.HTTP.TransformHTTP(ctx, HTTPTransformRequest{URL: rawURL, Body: body, Headers: http.Header{"Content-Type": []string{"application/json"}}, Timeout: timeout})
}

func buildHTTPTransformBody(details map[string]interface{}, data []byte) ([]byte, error) {
	var input interface{}
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, errors.New("external transformation input is invalid JSON")
	}
	label := "data"
	if configured, ok := details["data_label"].(string); ok && strings.TrimSpace(configured) != "" {
		label = strings.Trim(strings.TrimSpace(configured), `" :{}`)
	}
	body := map[string]interface{}{label: input}
	if custom, ok := details["custom_json"].(string); ok && strings.TrimSpace(custom) != "" {
		custom = strings.TrimSpace(custom)
		if !strings.HasPrefix(custom, "{") {
			custom = "{" + strings.Trim(custom, " ,") + "}"
		}
		var fields map[string]interface{}
		if err := json.Unmarshal([]byte(custom), &fields); err != nil {
			return nil, errors.New("custom_json is invalid")
		}
		for key, value := range fields {
			body[key] = value
		}
	}
	if key, ok := details["api_key"].(string); ok {
		body["api_key"] = key
	}
	if secret, ok := details["api_secret"].(string); ok {
		body["api_secret"] = secret
	}
	return json.Marshal(body)
}

func transformHTTP(ctx context.Context, client *http.Client, request HTTPTransformRequest) ([]byte, error) {
	requestCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return nil, errors.New("create external transformation request")
	}
	req.Header = request.Headers.Clone()
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("external transformation request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("external transformation returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPTransformResponse+1))
	if err != nil {
		return nil, errors.New("read external transformation response")
	}
	if len(body) > maxHTTPTransformResponse {
		return nil, errors.New("external transformation response is too large")
	}
	return body, nil
}

func stringDetail(details map[string]interface{}, key string) (string, error) {
	value, ok := details[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return strings.TrimSpace(text), nil
}

func detailDuration(details map[string]interface{}, key string, fallback time.Duration) time.Duration {
	value, exists := details[key]
	if !exists {
		return fallback
	}
	var secondsValue float64
	switch typed := value.(type) {
	case int:
		secondsValue = float64(typed)
	case int64:
		secondsValue = float64(typed)
	case float64:
		secondsValue = typed
	case string:
		secondsValue, _ = strconv.ParseFloat(typed, 64)
	}
	if secondsValue <= 0 {
		return fallback
	}
	return time.Duration(secondsValue * float64(time.Second))
}

func seconds(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func unwrapDoubleObject(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) >= 4 && bytes.HasPrefix(trimmed, []byte("{{")) && bytes.HasSuffix(trimmed, []byte("}}")) {
		return trimmed[1 : len(trimmed)-1]
	}
	return data
}
