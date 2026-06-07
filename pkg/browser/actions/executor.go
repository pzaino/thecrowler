package actions

import (
	"context"
	"errors"
	"fmt"
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
	if err != nil && !rule.ErrorHandling.Ignore {
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
		return err
	}
	for _, step := range rule.PostProcessing {
		if err := postProcess(ctx, runtime, step); err != nil {
			return err
		}
	}
	return nil
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

	switch strings.ToLower(strings.TrimSpace(rule.ActionType)) {
	case cmn.ClickStr, cmn.LClickStr:
		return click(ctx, runtime, rule, 0)
	case cmn.RClickStr:
		return click(ctx, runtime, rule, 2)
	case "scroll":
		return scroll(ctx, runtime, rule)
	case "input_text":
		return input(ctx, runtime, rule)
	case "clear":
		element, _, err := findElement(ctx, runtime, rule.Selectors)
		if err != nil {
			return err
		}
		return element.Clear()
	case "custom":
		return custom(ctx, runtime, rule)
	case "take_screenshot":
		return screenshot(ctx, runtime, rule)
	case "key_down":
		return runtime.WebDriver.KeyDown(rule.Value)
	case "key_up":
		return runtime.WebDriver.KeyUp(rule.Value)
	case "mouse_hover":
		return moveToElement(ctx, runtime, rule)
	case "forward":
		return runtime.WebDriver.Forward()
	case "back":
		return runtime.WebDriver.Back()
	case "refresh":
		return runtime.WebDriver.Refresh()
	case "switch_to_frame":
		element, _, err := findElement(ctx, runtime, rule.Selectors)
		if err != nil {
			return err
		}
		return runtime.WebDriver.SwitchFrame(element)
	case "switch_to_window":
		return runtime.WebDriver.SwitchWindow(rule.Value)
	case "scroll_to_element":
		return scrollToElement(ctx, runtime, rule)
	case "scroll_by_amount":
		_, err := runtime.WebDriver.ExecuteScript(fmt.Sprintf("window.scrollTo(0, %d);", cmn.StringToInt(rule.Value)), nil)
		return err
	case "click_and_hold":
		return clickAndHold(ctx, runtime, rule)
	case "release":
		return release(ctx, runtime, rule)
	case "navigate_to_url":
		return runtime.WebDriver.Get(rule.GetValue())
	default:
		return fmt.Errorf("action type not supported: %s", rule.ActionType)
	}
}

// WaitForCondition waits without losing context cancellation or runtime status.
func WaitForCondition(ctx context.Context, runtime *Runtime, condition rules.WaitCondition) error {
	if err := runtime.check(ctx); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(condition.ConditionType)) {
	case "element":
		_, err := runtime.Rules.FindElement(ctx, condition.Selector)
		return err
	case "delay":
		return wait(ctx, runtime, time.Duration(exi.GetFloat(condition.Value)*float64(time.Second)))
	case "plugin_call":
		if runtime.Rules == nil {
			return errors.New("browser actions: rule lookup is nil")
		}
		script, exists, err := runtime.Rules.PluginScript(ctx, condition.Value)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("plugin not found: %s", condition.Value)
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
func ConditionsMatch(ctx context.Context, runtime *Runtime, conditions map[string]interface{}) (bool, error) {
	if len(conditions) == 0 {
		return true, nil
	}
	if element, ok := conditions["element"].(string); ok {
		if _, err := runtime.WebDriver.FindElement(vdi.ByCSSSelector, element); err != nil {
			return false, nil
		}
	}
	if language, ok := conditions["language"]; ok {
		actual, err := runtime.WebDriver.ExecuteScript("return document.documentElement.lang", nil)
		if err != nil || actual != language {
			return false, nil
		}
	}
	if _, ok := conditions["plugin_call"]; ok {
		name, _ := conditions["selector"].(string)
		script, exists, err := runtime.Rules.PluginScript(ctx, name)
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
	}
	return true, nil
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
		return nil
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
		return nil
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
			err = executeHBS(ctx, runtime, "type", map[string]interface{}{"Value": selector.Value})
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
	return element.SendKeys(selector.Value)
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
	element, selector, err := findElement(ctx, runtime, rule.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, rule.RuleName, err)
		return nil
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
	script := fmt.Sprintf("var element=document.querySelector(%s); if (!element) return false; element.scrollIntoView({behavior:'smooth',block:'center'}); return true;", strconv.Quote(selector.Selector))
	ok, err := runtime.WebDriver.ExecuteScript(script, nil)
	if err != nil {
		return fmt.Errorf("failed to scroll to element using Selenium: %w", err)
	}
	if ok != true {
		return errors.New("element not found for scrolling")
	}
	return nil
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
		if strings.EqualFold(strings.TrimSpace(selector.SelectorType), "plugin_call") {
			if runtime.Rules == nil {
				return errors.New("browser actions: rule lookup is nil")
			}
			if err := runtime.Rules.CallPlugin(ctx, selector.Selector, rule.Value); err != nil {
				return err
			}
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
