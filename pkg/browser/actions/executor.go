package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	errNoElementFound  = "rule %q reported no element found: %w"
	errElementLocation = "rule %q reported failed to get element location: %w"
)

type actionHandler func(context.Context, *Runtime, *rules.ActionRule) error

// ActionSpec is the executor contract for a canonical schema action.
type ActionSpec struct {
	Handler         actionHandler
	Selectors       int
	TargetSelectors int
	ValueRequired   bool
	StoresResult    bool
	SelectorKind    string
	ValidateValue   func(string) error
}

func strictInteger(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("value must be a strict integer")
	}
	_, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("value must be a strict integer: %w", err)
	}
	return nil
}

var canonicalActions = map[string]ActionSpec{
	"click":           {Handler: func(c context.Context, r *Runtime, a *rules.ActionRule) error { return click(c, r, a, 0) }, Selectors: 1},
	"input_text":      {Handler: input, Selectors: 1, ValueRequired: true},
	"clear":           {Handler: elementAction(func(e vdi.WebElement) error { return e.Clear() }), Selectors: 1},
	"drag_and_drop":   {Handler: dragAndDrop, Selectors: 1, TargetSelectors: 1},
	"mouse_hover":     {Handler: moveToElement, Selectors: 1},
	"right_click":     {Handler: func(c context.Context, r *Runtime, a *rules.ActionRule) error { return click(c, r, a, 2) }, Selectors: 1},
	"double_click":    {Handler: doubleClick, Selectors: 1},
	"click_and_hold":  {Handler: clickAndHold, Selectors: 1},
	"release":         {Handler: release},
	"key_down":        {Handler: func(_ context.Context, r *Runtime, a *rules.ActionRule) error { return r.WebDriver.KeyDown(a.Value) }, ValueRequired: true},
	"key_up":          {Handler: func(_ context.Context, r *Runtime, a *rules.ActionRule) error { return r.WebDriver.KeyUp(a.Value) }, ValueRequired: true},
	"navigate_to_url": {Handler: func(_ context.Context, r *Runtime, a *rules.ActionRule) error { return r.WebDriver.Get(a.GetValue()) }, ValueRequired: true},
	"forward":         {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.Forward() }},
	"back":            {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.Back() }},
	"refresh":         {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.Refresh() }},
	"switch_to_window": {Handler: func(_ context.Context, r *Runtime, a *rules.ActionRule) error {
		return r.WebDriver.SwitchWindow(a.Value)
	}, ValueRequired: true},
	"switch_to_frame": {Handler: switchFrame, Selectors: 1},
	"close_window":    {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.Close() }},
	"accept_alert":    {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.AcceptAlert() }},
	"dismiss_alert":   {Handler: func(_ context.Context, r *Runtime, _ *rules.ActionRule) error { return r.WebDriver.DismissAlert() }},
	"get_alert_text":  {Handler: getAlertText, StoresResult: true},
	"send_keys_to_alert": {Handler: func(_ context.Context, r *Runtime, a *rules.ActionRule) error {
		return r.WebDriver.SetAlertText(a.Value)
	}, ValueRequired: true},
	"scroll_to_element": {Handler: scrollToElement, Selectors: 1},
	"scroll_by_amount":  {Handler: scrollByAmount, ValueRequired: true, ValidateValue: strictInteger},
	"take_screenshot":   {Handler: screenshot, ValueRequired: true},
	"custom":            {Handler: custom, Selectors: 1, SelectorKind: "plugin_call"},
}

var actionAliases = map[string]string{cmn.LClickStr: "click", cmn.RClickStr: "right_click", "scroll": "scroll_by_amount"}

// CanonicalActionKeys returns the keys of the single canonical action table.
func CanonicalActionKeys() []string {
	keys := make([]string, 0, len(canonicalActions))
	for k := range canonicalActions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ExecuteRules executes rules in order. It stops immediately when the context
// is canceled or the runtime status check fails.
func ExecuteRules(ctx context.Context, runtime *Runtime, actionRules []rules.ActionRule) error {
	ctx = runtime.context(ctx)
	for i := range actionRules {
		if err := runtime.check(ctx); err != nil {
			return err
		}
		if err := ExecuteRule(ctx, runtime, &actionRules[i]); err != nil {
			if isStopped(ctx, err) {
				return err
			}
			cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
		}
	}
	return nil
}

// ExecuteRule executes one rule, including context-aware retries and
// post-processing.
func ExecuteRule(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	ctx = runtime.context(ctx)
	if rule == nil {
		return errors.New("browser actions: action rule is nil")
	}
	err := executeRuleOnce(ctx, runtime, rule)
	if err != nil {
		for retry := 0; retry < rule.ErrorHandling.RetryCount; retry++ {
			if isStopped(ctx, err) {
				return err
			}
			if err = wait(ctx, runtime, time.Duration(rule.ErrorHandling.RetryDelay)*time.Second); err != nil {
				return err
			}
			err = executeRuleOnce(ctx, runtime, rule)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		if rule.ErrorHandling.Ignore && !isStopped(ctx, err) {
			return nil
		}
		return err
	}
	for _, step := range rule.PostProcessing {
		if err := postProcess(ctx, runtime, step); err != nil {
			return err
		}
	}
	return nil
}

func elementAction(fn func(vdi.WebElement) error) actionHandler {
	return func(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
		e, _, err := findElement(ctx, runtime, rule.Selectors)
		if err != nil {
			return err
		}
		return fn(e)
	}
}

func switchFrame(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	e, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		return err
	}
	return runtime.WebDriver.SwitchFrame(e)
}

func getAlertText(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	value, err := runtime.WebDriver.AlertText()
	if err != nil {
		return err
	}
	if runtime.Results == nil {
		return errors.New("browser actions: result sink is nil")
	}
	return runtime.Results.StoreResult(ctx, rule.StoreAs, value)
}

func scrollByAmount(_ context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	amount, _ := strconv.Atoi(rule.Value)
	_, err := runtime.WebDriver.ExecuteScript("window.scrollBy(0, arguments[0]);", []interface{}{amount})
	return err
}

func doubleClick(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	e, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		return err
	}
	if err = e.MoveTo(0, 0); err != nil {
		return err
	}
	return runtime.WebDriver.DoubleClick()
}

func dragAndDrop(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	source, _, err := findElement(ctx, runtime, rule.Selectors[:1])
	if err != nil {
		return err
	}
	target, _, err := findElement(ctx, runtime, rule.TargetSelectors)
	if err != nil {
		return err
	}
	if err = source.MoveTo(0, 0); err != nil {
		return err
	}
	if err = runtime.WebDriver.ButtonDown(); err != nil {
		return err
	}
	held := true
	defer func() {
		if held {
			_ = runtime.WebDriver.ButtonUp()
		}
	}() // cleanup on target failure
	if err = target.MoveTo(0, 0); err != nil {
		return err
	}
	held = false
	return runtime.WebDriver.ButtonUp()
}

func executeRuleOnce(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	if err := runtime.check(ctx); err != nil {
		return err
	}
	for _, condition := range rule.WaitConditions {
		if err := WaitForCondition(ctx, runtime, condition); err != nil {
			return err
		}
	}
	ok, err := ConditionsMatch(ctx, runtime, rule.Conditions)
	if err != nil || !ok {
		return err
	}

	key := strings.ToLower(strings.TrimSpace(rule.ActionType))
	if canonical, ok := actionAliases[key]; ok {
		key = canonical
	}
	spec, ok := canonicalActions[key]
	if !ok {
		return fmt.Errorf("action type not supported: %s", rule.ActionType)
	}
	if len(rule.Selectors) < spec.Selectors {
		return fmt.Errorf("action %s requires %d source selector(s)", key, spec.Selectors)
	}
	if len(rule.TargetSelectors) < spec.TargetSelectors {
		return fmt.Errorf("action %s requires %d target selector(s)", key, spec.TargetSelectors)
	}
	if spec.ValueRequired && rule.GetValue() == "" {
		return fmt.Errorf("action %s requires a value", key)
	}
	if spec.SelectorKind != "" {
		for _, selector := range rule.Selectors {
			if !strings.EqualFold(strings.TrimSpace(selector.SelectorType), spec.SelectorKind) {
				return fmt.Errorf("action %s requires selector kind %s", key, spec.SelectorKind)
			}
		}
	}
	if spec.ValidateValue != nil {
		if err := spec.ValidateValue(rule.Value); err != nil {
			return fmt.Errorf("action %s: %w", key, err)
		}
	}
	return spec.Handler(ctx, runtime, rule)
}

// WaitForCondition waits without losing context cancellation or runtime status.
func WaitForCondition(ctx context.Context, runtime *Runtime, condition rules.WaitCondition) error {
	if err := runtime.check(ctx); err != nil {
		return err
	}
	conditionType := rules.WaitConditionType(strings.ToLower(strings.TrimSpace(string(condition.ConditionType))))
	switch conditionType {
	case rules.WaitConditionElementPresence, rules.WaitConditionElementVisible:
		if runtime.Rules == nil {
			return errors.New("browser actions: rule lookup is nil")
		}
		if strings.TrimSpace(condition.Selector.SelectorType) == "" || strings.TrimSpace(condition.Selector.Selector) == "" {
			return errors.New("wait condition requires a typed selector")
		}
		element, err := runtime.Rules.FindElement(ctx, condition.Selector)
		if err != nil {
			return err
		}
		if conditionType == rules.WaitConditionElementVisible {
			visible, err := element.IsDisplayed()
			if err != nil {
				return err
			}
			if !visible {
				return errors.New("wait condition element is not visible")
			}
		}
		return err
	case rules.WaitConditionDelay:
		if strings.TrimSpace(condition.Value) == "" {
			return errors.New("delay wait condition requires value")
		}
		return wait(ctx, runtime, time.Duration(exi.GetFloat(condition.Value)*float64(time.Second)))
	case rules.WaitConditionPluginCall:
		if runtime.Rules == nil {
			return errors.New("browser actions: rule lookup is nil")
		}
		plugin := strings.TrimSpace(condition.Plugin)
		if plugin == "" {
			return errors.New("plugin_call wait condition requires plugin")
		}
		script, exists, err := runtime.Rules.PluginScript(ctx, plugin)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("plugin not found: %s", plugin)
		}
		_, err = runtime.WebDriver.ExecuteScript(script, nil)
		return err
	default:
		return fmt.Errorf("wait condition not supported: %s", condition.ConditionType)
	}
}

func wait(ctx context.Context, runtime *Runtime, duration time.Duration) error {
	if err := runtime.check(ctx); err != nil {
		return err
	}
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	pollInterval := 100 * time.Millisecond
	if duration < pollInterval {
		pollInterval = duration
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return runtime.check(ctx)
		case <-ticker.C:
			if err := runtime.check(ctx); err != nil {
				return err
			}
		}
	}
}

// ConditionsMatch evaluates browser-side action conditions.
func ConditionsMatch(ctx context.Context, runtime *Runtime, condition *rules.ActionCondition) (bool, error) {
	if condition == nil {
		return true, nil
	}
	switch strings.ToLower(strings.TrimSpace(condition.Type)) {
	case "element":
		if strings.TrimSpace(condition.Selector) == "" {
			return false, errors.New("element condition requires selector")
		}
		if _, err := runtime.WebDriver.FindElement(vdi.ByCSSSelector, condition.Selector); err != nil {
			return false, nil
		}
		return true, nil
	case "language":
		if strings.TrimSpace(condition.Language) == "" {
			return false, errors.New("language condition requires language")
		}
		actual, err := runtime.WebDriver.ExecuteScript("return document.documentElement.lang", nil)
		if err != nil {
			return false, err
		}
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(actual)), condition.Language) {
			return false, nil
		}
		return true, nil
	case "plugin_call":
		if strings.TrimSpace(condition.PluginCall) == "" {
			return false, errors.New("plugin_call condition requires plugin_call")
		}
		if runtime.Rules == nil {
			return false, errors.New("browser actions: rule lookup is nil")
		}
		script, exists, err := runtime.Rules.PluginScript(ctx, condition.PluginCall)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
		result, err := runtime.WebDriver.ExecuteScript(script, nil)
		if err != nil {
			return false, nil
		}
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(result)), "true"), nil
	default:
		return false, fmt.Errorf("unknown action condition type %q", condition.Type)
	}
}

func findElement(ctx context.Context, runtime *Runtime, selectors []rules.Selector) (vdi.WebElement, rules.Selector, error) {
	if runtime.Rules == nil {
		return nil, rules.Selector{}, errors.New("browser actions: rule lookup is nil")
	}
	var lastErr error
	for _, selector := range selectors {
		if err := runtime.check(ctx); err != nil {
			return nil, selector, err
		}
		element, err := runtime.Rules.FindElement(ctx, selector)
		if err == nil && element != nil {
			return element, selector, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no selectors configured")
	}
	return nil, rules.Selector{}, lastErr
}

func click(ctx context.Context, runtime *Runtime, rule *rules.ActionRule, button int) error {
	element, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, rule.RuleName, err)
		return fmt.Errorf(errNoElementFound, rule.RuleName, err)
	}
	if runtime.Options.HBS.Enabled {
		location, locationErr := element.Location()
		if locationErr != nil {
			return fmt.Errorf(errElementLocation, rule.RuleName, locationErr)
		}
		action := "click"
		if button == 2 {
			action = "right_click"
		}
		err = executeHBS(ctx, runtime, action, map[string]interface{}{"X": location.X, "Y": location.Y})
		if err == nil {
			return nil
		}
		if stopErr := runtime.check(ctx); stopErr != nil {
			return stopErr
		}
		if !runtime.Options.HBS.SeleniumFallback {
			return err
		}
	}
	if button == 0 {
		return element.Click()
	}
	return dispatchMouseEvent(runtime.WebDriver, element, "contextmenu")
}

func input(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	element, selector, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, rule.RuleName, err)
		return fmt.Errorf(errNoElementFound, rule.RuleName, err)
	}
	value := rule.Value
	if value == "" {
		value = selector.Value
	}
	if runtime.Options.HBS.Enabled {
		location, locationErr := element.Location()
		if locationErr != nil {
			return fmt.Errorf(errElementLocation, rule.RuleName, locationErr)
		}
		err = executeHBS(ctx, runtime, "moveMouse", map[string]interface{}{"X": location.X, "Y": location.Y})
		if err == nil {
			err = executeHBS(ctx, runtime, "click", nil)
		}
		if err == nil {
			err = executeHBS(ctx, runtime, "type", map[string]interface{}{"Value": value})
		}
		if err == nil {
			return nil
		}
		if stopErr := runtime.check(ctx); stopErr != nil {
			return stopErr
		}
		if !runtime.Options.HBS.SeleniumFallback {
			return err
		}
	}
	if err := element.Click(); err != nil {
		return fmt.Errorf("failed to click on element: %w", err)
	}
	return element.SendKeys(value)
}

func scroll(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	attribute := strings.TrimSpace(rule.Value)
	if attribute == "" {
		attribute = "document.body.scrollHeight"
	}
	if runtime.Options.HBS.Enabled {
		err := executeHBS(ctx, runtime, "scroll", map[string]interface{}{"Value": attribute})
		if err == nil {
			return nil
		}
		if stopErr := runtime.check(ctx); stopErr != nil {
			return stopErr
		}
		if !runtime.Options.HBS.SeleniumFallback {
			return err
		}
	}
	_, err := runtime.WebDriver.ExecuteScript(fmt.Sprintf("window.scrollTo(0, %s)", attribute), nil)
	return err
}

func scrollToElement(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	element, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, rule.RuleName, err)
		return fmt.Errorf(errNoElementFound, rule.RuleName, err)
	}
	if runtime.Options.HBS.Enabled {
		location, locationErr := element.Location()
		if locationErr != nil {
			return fmt.Errorf(errElementLocation, rule.RuleName, locationErr)
		}
		err = executeHBS(ctx, runtime, "moveMouse", map[string]interface{}{"X": location.X, "Y": location.Y})
		if err == nil {
			err = executeHBS(ctx, runtime, "scroll", nil)
		}
		if err == nil {
			return nil
		}
		if stopErr := runtime.check(ctx); stopErr != nil {
			return stopErr
		}
		if !runtime.Options.HBS.SeleniumFallback {
			return err
		}
	}
	return element.MoveTo(0, 0)
}

func moveToElement(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	element, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		return err
	}
	if runtime.Options.HBS.Enabled {
		location, locationErr := element.Location()
		if locationErr == nil {
			err = executeHBS(ctx, runtime, "moveMouse", map[string]interface{}{"X": location.X, "Y": location.Y})
			if err == nil {
				return nil
			}
		}
		if stopErr := runtime.check(ctx); stopErr != nil {
			return stopErr
		}
		if !runtime.Options.HBS.SeleniumFallback {
			if locationErr != nil {
				return locationErr
			}
			return err
		}
	}
	return dispatchMouseEvent(runtime.WebDriver, element, "mousemove")
}

func clickAndHold(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	element, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		return err
	}
	if err := dispatchMouseEvent(runtime.WebDriver, element, "mousemove"); err != nil {
		return err
	}
	return dispatchMouseEvent(runtime.WebDriver, element, "mousedown")
}

func release(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	if len(rule.Selectors) == 0 {
		_, err := runtime.WebDriver.ExecuteScript("var elem=document.elementFromPoint(window.event.clientX,window.event.clientY); elem.dispatchEvent(new MouseEvent('mouseup',{bubbles:true,cancelable:true,view:window}));", nil)
		return err
	}
	element, _, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		return err
	}
	return dispatchMouseEvent(runtime.WebDriver, element, "mouseup")
}

func dispatchMouseEvent(driver vdi.WebDriver, element vdi.WebElement, eventName string) error {
	id, err := element.GetAttribute("id")
	byName := false
	if err != nil || id == "" {
		id, err = element.GetAttribute("name")
		byName = true
	}
	if err != nil {
		return err
	}
	lookup := fmt.Sprintf("document.getElementById(%s)", strconv.Quote(id))
	if byName {
		lookup = fmt.Sprintf("document.getElementsByName(%s)[0]", strconv.Quote(id))
	}
	script := fmt.Sprintf("var elem=%s; if (!elem) return false; var rect=elem.getBoundingClientRect(); elem.dispatchEvent(new MouseEvent(%s,{bubbles:true,cancelable:true,clientX:rect.left,clientY:rect.top,view:window})); return true;", lookup, strconv.Quote(eventName))
	_, err = driver.ExecuteScript(script, nil)
	return err
}

func custom(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	for _, selector := range rule.Selectors {
		if !strings.EqualFold(strings.TrimSpace(selector.SelectorType), "plugin_call") {
			continue
		}

		if runtime.Rules == nil {
			return errors.New("browser actions: rule lookup is nil")
		}

		params := make(map[string]interface{})

		if selector.Details != nil {
			if raw, exists := selector.Details["parameters"]; exists {
				params = cmn.ConvertInfToMap(raw)
				if params == nil {
					return errors.New("browser actions: plugin parameters must be an object")
				}
			}
		}

		if err := runtime.Rules.CallPlugin(ctx, selector.Selector, rule.Value, params); err != nil {
			return err
		}
	}

	return nil
}

func screenshot(ctx context.Context, runtime *Runtime, rule *rules.ActionRule) error {
	if runtime.Screenshot == nil {
		return errors.New("browser actions: screenshot hook is not configured")
	}
	parts := strings.SplitN(rule.GetValue(), ",", 2)
	maxHeight := 0
	filename := parts[0]
	if len(parts) == 2 {
		maxHeight = cmn.StringToInt(parts[0])
		filename = parts[1]
	}
	return runtime.Screenshot(ctx, filename, maxHeight)
}

func postProcess(ctx context.Context, runtime *Runtime, step rules.PostProcessingStep) error {
	if !strings.EqualFold(strings.TrimSpace(step.Type), "collect_cookies") {
		cmn.DebugMsg(cmn.DbgLvlError, "post processing step not supported: %s", step.Type)
		return nil
	}
	cookies, err := runtime.WebDriver.GetCookies()
	if err != nil {
		return fmt.Errorf("retrieving cookies: %w", err)
	}
	if runtime.Cookies == nil {
		return nil
	}
	values := make(map[string]interface{}, len(cookies))
	for _, cookie := range cookies {
		values[cookie.Name] = cookie.Value
	}
	return runtime.Cookies.CollectCookies(ctx, values)
}

func executeHBS(ctx context.Context, runtime *Runtime, action string, values map[string]interface{}) error {
	if err := runtime.check(ctx); err != nil {
		return err
	}
	endpoint := runtime.hbsEndpoint()
	if endpoint == "" {
		return errors.New("browser actions: HBS is enabled but the Rbee action URL is empty")
	}
	fields := []string{fmt.Sprintf("%s:%s", strconv.Quote("Action"), strconv.Quote(action))}
	for key, value := range values {
		var encoded string
		switch typed := value.(type) {
		case string:
			encoded = strconv.Quote(typed)
		default:
			encoded = fmt.Sprint(typed)
		}
		fields = append(fields, fmt.Sprintf("%s:%s", strconv.Quote(key), encoded))
	}
	script := fmt.Sprintf("var xhr=new XMLHttpRequest(); xhr.open('POST',%s,false); xhr.setRequestHeader('Content-Type','application/json;charset=UTF-8'); try { xhr.send(JSON.stringify({%s})); return xhr.status>=200 && xhr.status<300; } catch (e) { return false; }", strconv.Quote(endpoint), strings.Join(fields, ","))
	result, err := runtime.WebDriver.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("execute HBS %s: %w", action, err)
	}
	if result != true {
		return fmt.Errorf("HBS %s failed", action)
	}
	return runtime.check(ctx)
}

func isStopped(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRuntimeStopped) || ctx.Err() != nil
}
