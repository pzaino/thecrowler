// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"fmt"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	errNoElementFound        = "Rule `%s` reported no element found: %v"
	errFailedToGetLoc        = "Rule `%s` reported failed to get element location: %v"
	defaultActionRulesConfig = "{\"config\":\"default\"}"
)

func processActionRules(wd *vdi.WebDriver, ctx *ProcessContext, url string) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ProcActionRules] Starting to search and process CROWler Action rules...")
	// Run Action Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Executing CROWler configured Action rules...")
		// Execute the rules
		if strings.TrimSpace(string((*ctx.source.Config))) == defaultActionRulesConfig {
			runDefaultActionRules(wd, ctx)
		} else {
			configStr := string((*ctx.source.Config))
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Configuration: %v", configStr)
		}
	}
	// Check for rules based on the URL
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Executing CROWler URL based Action rules (if any)...")
	// If the URL matches a rule, execute it
	processURLRules(wd, ctx, url)

}

func processURLRules(wd *vdi.WebDriver, ctx *ProcessContext, url string) {
	// Find all the rulesets that match the URL
	rsl, err := ctx.re.GetAllRulesetByURL(url)
	if err == nil && len(rsl) != 0 {
		for _, rs := range rsl {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcURLRules] Executing ruleset: %s", rs.Name)
			// Execute all the rules in the ruleset
			executeActionRules(ctx, rs.GetAllEnabledActionRules(ctx.GetContextID(), true), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}

	// Find all the rulesgroup that match the URL
	rgl, err := ctx.re.GetAllRulesGroupByURL(url)
	if err == nil && len(rgl) != 0 {
		for _, rg := range rgl {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcURLRules] Executing rule group: %s", rg.GroupName)
			// Set the environment variables for the rule group
			rg.SetEnv(ctx.GetContextID())
			// Execute all the rules in the rule group
			executeActionRules(ctx, rg.GetActionRules(), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}

}

func executeActionRules(ctx *ProcessContext,
	rules []rules.ActionRule, wd *vdi.WebDriver) {
	// Extract each rule and execute it
	for _, r := range rules {
		executeRule(ctx, &r, wd)
		ctx.Status.TotalActions.Add(1)
	}
}

func executeRule(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) {
	// Execute the rule
	err := executeActionRule(ctx, r, wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
		if !r.ErrorHandling.Ignore {
			if r.ErrorHandling.RetryCount > 0 {
				for i := 0; i < r.ErrorHandling.RetryCount; i++ {
					if r.ErrorHandling.RetryDelay > 0 {
						time.Sleep(time.Duration(r.ErrorHandling.RetryDelay) * time.Second)
					}
					err = executeActionRule(ctx, r, wd)
					if err == nil {
						break
					}
				}
			}
		}
	}
	if r.PostProcessing != nil && err == nil {
		for _, pp := range r.PostProcessing {
			executeActionPostProcessingStep(ctx, pp, wd)
		}
	}
}

func executeActionPostProcessingStep(ctx *ProcessContext, pp rules.PostProcessingStep, wd *vdi.WebDriver) {
	// Execute the post processing step
	if pp.Type == "collect_cookies" {
		cookies, err := retrieveCookies(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "retrieving cookies: %v", err)
		}
		if len(cookies) != 0 {
			for name, value := range cookies {
				ctx.CollectedCookies[name] = value
			}
		}
		return
	}
	cmn.DebugMsg(cmn.DbgLvlError, "post processing step not supported: %s", pp.Type)
}

// executeActionRule executes a single ActionRule
func executeActionRule(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	// Execute Wait condition first
	if len(r.WaitConditions) != 0 {
		for _, wc := range r.WaitConditions {
			// Execute the wait condition
			err := WaitForCondition(ctx, wd, wc)
			if err != nil {
				return err
			}
		}
	}
	// Execute the action based on the ActionType
	if (len(r.Conditions) == 0) || checkActionConditions(ctx, r.Conditions, wd) {
		switch strings.ToLower(strings.TrimSpace(r.ActionType)) {
		case cmn.ClickStr, cmn.LClickStr:
			return executeActionClick(ctx, r, wd, 0)
		case cmn.RClickStr:
			return executeActionClick(ctx, r, wd, 2)
		case "scroll":
			return executeActionScroll(r, wd)
		case "input_text":
			return executeActionInput(ctx, r, wd)
		case "clear":
			return executeActionClear(ctx, r, wd)
		case "custom":
			return executeActionJS(ctx, r, wd)
		case "take_screenshot":
			return executeActionScreenshot(r, wd)
		case "key_down":
			return executeActionKeyDown(r, wd)
		case "key_up":
			return executeActionKeyUp(r, wd)
		case "mouse_hover":
			return executeActionMouseHover(ctx, r, wd)
		case "forward":
			return executeActionForward(wd)
		case "back":
			return executeActionBack(wd)
		case "refresh":
			return executeActionRefresh(wd)
		case "switch_to_frame":
			return executeActionSwitchFrame(ctx, r, wd)
		case "switch_to_window":
			return executeActionSwitchWindow(r, wd)
		case "scroll_to_element":
			return executeActionScrollToElement(ctx, r, wd)
		case "scroll_by_amount":
			return executeActionScrollByAmount(r, wd)
		case "click_and_hold":
			return executeActionClickAndHold(ctx, r, wd)
		case "release":
			return executeActionRelease(ctx, r, wd)
		case "navigate_to_url":
			return executeActionNavigateToURL(r, wd)
		}
		return fmt.Errorf("action type not supported: %s", r.ActionType)
	}
	return nil
}

func executeActionNavigateToURL(r *rules.ActionRule, wd *vdi.WebDriver) error {
	return (*wd).Get(r.GetValue())
}

func executeActionClickAndHold(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	wdf, _, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		return err
	}
	// JavaScript to simulate a click and hold
	id, err := wdf.GetAttribute("id")
	if err != nil {
		id, err = wdf.GetAttribute("name")
		if err != nil {
			return err
		}
	}
	script := `
		var elem = document.getElementById('` + id + `');
		var evt1 = new MouseEvent('mousemove', {
			bubbles: true,
			cancelable: true,
			clientX: elem.getBoundingClientRect().left,
			clientY: elem.getBoundingClientRect().top,
			view: window
		});
		var evt2 = new MouseEvent('mousedown', {
			bubbles: true,
			cancelable: true,
			clientX: elem.getBoundingClientRect().left,
			clientY: elem.getBoundingClientRect().top,
			view: window
		});
		elem.dispatchEvent(evt1);
		elem.dispatchEvent(evt2);
	`
	_, err = (*wd).ExecuteScript(script, nil)
	return err
}

func executeActionRelease(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	var element vdi.WebElement
	if r.Selectors != nil {
		element, _, _ = findElementBySelectorType(ctx, wd, r.Selectors)
	}
	var script string
	if element != nil {
		id, err := element.GetAttribute("id")
		if err != nil {
			id, err = element.GetAttribute("name")
			if err != nil {
				return err
			}
		}
		script = `
			var elem = document.getElementById('` + id + `');
			var evt3 = new MouseEvent('mouseup', {
				bubbles: true,
				cancelable: true,
				clientX: elem.getBoundingClientRect().left,
				clientY: elem.getBoundingClientRect().top,
				view: window
			});
			elem.dispatchEvent(evt3);
		`
	} else {
		// Get the element at the current mouse coordinates:
		script = `
			const x = event.clientX;
			const y = event.clientY;
			elem = document.elementFromPoint(x, y);
			var evt3 = new MouseEvent('mouseup', {
				bubbles: true,
				cancelable: true,
				clientX: elem.getBoundingClientRect().left,
				clientY: elem.getBoundingClientRect().top,
				view: window
			});
			elem.dispatchEvent(evt3);
		`
	}
	_, err := (*wd).ExecuteScript(script, nil)
	return err
}

func executeActionClear(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	wdf, _, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		return err
	}
	return wdf.Clear()
}

// executeActionScreenshot is responsible for executing a "take_screenshot" action
// It takes a screenshot of the current page and saves it to the configured location
// r.Value contains the filename of the screenshot and the max height of the screenshot
// (optional, if not provided the screenshot will be taken of the entire page)
// rValue syntax is: "maxHeight,fileName"
func executeActionScreenshot(r *rules.ActionRule, wd *vdi.WebDriver) error {
	// Check if the rule contains also a max height
	val := r.GetValue()
	hVal := ""
	fVal := ""
	if strings.Contains(val, ",") {
		hVal = strings.Split(val, ",")[0]
		fVal = strings.Split(val, ",")[1]
	} else {
		hVal = "0"
		fVal = val
	}
	hInt := cmn.StringToInt(hVal)

	_, err := TakeScreenshot(wd, fVal, hInt)
	return err
}

func executeActionKeyDown(r *rules.ActionRule, wd *vdi.WebDriver) error {
	return (*wd).KeyDown(r.Value)
}

func executeActionKeyUp(r *rules.ActionRule, wd *vdi.WebDriver) error {
	return (*wd).KeyUp(r.Value)
}

func executeActionMouseHover(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	return executeMoveToElement(ctx, r, wd)
}

func executeActionForward(wd *vdi.WebDriver) error {
	return (*wd).Forward()
}

func executeActionBack(wd *vdi.WebDriver) error {
	return (*wd).Back()
}

func executeActionRefresh(wd *vdi.WebDriver) error {
	return (*wd).Refresh()
}

func executeActionSwitchFrame(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	wdf, _, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		return err
	}
	return (*wd).SwitchFrame(wdf)
}

func executeActionSwitchWindow(r *rules.ActionRule, wd *vdi.WebDriver) error {
	return (*wd).SwitchWindow(r.Value)
}

// executeActionScrollToElement is responsible for executing a "scroll to element" action
func executeActionScrollToElement(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	// Find the element
	wdf, selector, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, r.RuleName, err)
		err = nil
	}

	// If the element is found, attempt to scroll to it using Rbee
	if wdf != nil {
		loc, err := wdf.Location()
		if err != nil {
			return fmt.Errorf(errFailedToGetLoc, r.RuleName, err)
		}

		// JavaScript to send a POST request to Rbee for scrolling to the element
		jsScript := fmt.Sprintf(`
            (function() {
                var xhr = new XMLHttpRequest();
                xhr.open("POST", "http://localhost:3000/v1/rb", true);
                xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                var data = JSON.stringify({
                    "Action": "moveMouse",
                    "X": %d,
                    "Y": %d
                });
                xhr.onreadystatechange = function () {
                    if (xhr.readyState === 4 && xhr.status === 200) {
                        var scrollXhr = new XMLHttpRequest();
                        scrollXhr.open("POST", "http://localhost:3000/v1/rb", true);
                        scrollXhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                        var scrollData = JSON.stringify({
                            "Action": "scroll"
                        });
                        scrollXhr.onreadystatechange = function () {
                            if (scrollXhr.readyState === 4 && scrollXhr.status === 200) {
                                console.log("done.");
                                return true;
                            } else if (scrollXhr.readyState === 4) {
                                console.error("Failed: " + scrollXhr.responseText);
                                return false;
                            }
                        };
                        scrollXhr.send(scrollData);
                    } else if (xhr.readyState === 4) {
                        console.error("Failed: " + xhr.responseText);
                        return false;
                    }
                };
                xhr.send(data);
            })();
        `, loc.X, loc.Y)

		// Execute the JavaScript in the browser context
		success, err := (*wd).ExecuteScript(jsScript, nil)
		if err == nil && success == true {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Scroll to element action executed successfully using Rbee")
			return nil
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Failed to execute scroll to element using Rbee, falling back to Selenium")

		// Fall back to using Selenium's ExecuteScript method
		scrollScript := fmt.Sprintf(`
            (function() {
                var element = document.querySelector("%s");
                if (element) {
                    element.scrollIntoView({ behavior: 'smooth', block: 'center' });
                    return true;
                } else {
                    return false;
                }
            })();
        `, selector.Value)

		success, err = (*wd).ExecuteScript(scrollScript, nil)
		if err != nil {
			return fmt.Errorf("failed to scroll to element using Selenium: %v", err)
		}

		if success != true {
			return fmt.Errorf("element not found for scrolling")
		}
	}

	return err
}

func executeActionScrollByAmount(r *rules.ActionRule, wd *vdi.WebDriver) error {
	y := cmn.StringToInt(r.Value)
	scrollScript := fmt.Sprintf("window.scrollTo(0, %d);", y)
	_, err := (*wd).ExecuteScript(scrollScript, nil)
	return err
}

// executeActionClick is responsible for executing a "click" action
func executeActionClick(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver, button int) error {
	var err error

	// Find the element
	wdf, _, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, r.RuleName, err)
		err = nil
	}

	// Set correct button for click
	var buttonName string
	if button == 2 {
		buttonName = cmn.RClickStr
	} else {
		buttonName = cmn.ClickStr
	}

	// If the element is found, attempt to move the mouse and click using Rbee
	if wdf != nil {
		loc, err := wdf.Location()
		if err != nil {
			return fmt.Errorf(errFailedToGetLoc, r.RuleName, err)
		}

		// JavaScript to send a POST request to Rbee for mouse move and click
		jsScript := fmt.Sprintf(`
            (function() {
                var xhr = new XMLHttpRequest();
                xhr.open("POST", "http://localhost:3000/v1/rb", true);
                xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                var data = JSON.stringify({
                    "Action": "moveMouse",
                    "X": %d,
                    "Y": %d
                });
                xhr.onreadystatechange = function () {
                    if (xhr.readyState === 4 && xhr.status === 200) {
                        var clickXhr = new XMLHttpRequest();
                        clickXhr.open("POST", "http://localhost:3000/v1/rb", true);
                        clickXhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                        var clickData = JSON.stringify({
                            "Action": "%s" // Click or right_click
                        });
                        clickXhr.onreadystatechange = function () {
                            if (clickXhr.readyState === 4 && clickXhr.status === 200) {
                                console.log("done.");
                                return true; // Clicked successfully
                            } else if (clickXhr.readyState === 4) {
                                console.error("Failed: " + clickXhr.responseText);
                                return false; // Failed to click
                            }
                        };
                        clickXhr.send(clickData);
                    } else if (xhr.readyState === 4) {
                        console.error("Failed: " + xhr.responseText);
                        return false; // Failed to move mouse
                    }
                };
                xhr.send(data);
            })();
        `, loc.X, loc.Y, buttonName)

		// Execute the JavaScript in the browser context
		var success interface{}
		success, err = (*wd).ExecuteScript(jsScript, nil)
		if err == nil && success == true {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionClick] Mouse move and click action executed successfully using Rbee")
			return nil
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionClick] Failed to execute mouse move and click using Rbee, falling back to Selenium")

		// Fall back to using Selenium's Click method
		if button == 0 {
			err = wdf.Click()
		} else if button == 2 {
			// Selenium does not support right_click directly so we use the following workaround
			id, err := wdf.GetAttribute("id")
			if err != nil {
				id, err = wdf.GetAttribute("name")
				if err != nil {
					return err
				}
			}
			script := `
				var elem = document.getElementById('` + id + `');
				var evt = new MouseEvent('contextmenu', {
					bubbles: true,
					cancelable: true,
					clientX: elem.getBoundingClientRect().left,
					clientY: elem.getBoundingClientRect().top,
					view: window
				});
				elem.dispatchEvent(evt);
			`
			_, err = (*wd).ExecuteScript(script, nil)
			if err != nil {
				return fmt.Errorf("failed to right click on element: %v", err)
			}
		}
		return err
	}

	return err
}

func executeMoveToElement(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	wdf, _, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		return err
	}
	id, err := wdf.GetAttribute("id")
	if err != nil {
		id, err = wdf.GetAttribute("name")
		if err != nil {
			return err
		}
	}

	// Get the location of the element
	loc, err := wdf.Location()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting element location: %v", err)
	}

	// Get the size of the element (optional, but useful for debugging)
	size, err := wdf.Size()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting element size: %v", err)
	}

	// Output the element's location and size for debugging
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Element location: (%d, %d)\n", loc.X, loc.Y)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Element size: (width: %d, height: %d)\n", size.Width, size.Height)

	script := fmt.Sprintf(`
        (function() {
            var xhr = new XMLHttpRequest();
            xhr.open("POST", "http://localhost:3000/v1/rb", true);
            xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
            var data = JSON.stringify({
                "Action": "moveMouse",
                "X": %d,
                "Y": %d
            });
            xhr.onreadystatechange = function () {
                if (xhr.readyState === 4 && xhr.status === 200) {
                    console.log("done.");
                } else if (xhr.readyState === 4) {
                    console.error("Failed: " + xhr.responseText);
                }
            };
            xhr.send(data);
        })();
    `, loc.X, loc.Y)

	// Move the mouse to the element using Rbee
	if err == nil {
		// If err is nill then we have all the information we need
		// to use human-simulation to move the mouse to the element
		_, err = (*wd).ExecuteScript(script, nil)
	}
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing human-simulation script: %v", err)
		// Moving human way failed, use Selenium way
		script = `
		var elem = document.getElementById('` + id + `');
		var evt = new MouseEvent('mousemove', {
			bubbles: true,
			cancelable: true,
			clientX: elem.getBoundingClientRect().left,
			clientY: elem.getBoundingClientRect().top,
			view: window
		});
		elem.dispatchEvent(evt);
		`
		// Move the mouse to the element using Rbee
		_, err = (*wd).ExecuteScript(script, nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "executing teleport script: %v", err)
		}
	}
	return err
}

// executeActionScroll is responsible for executing a "scroll" action
func executeActionScroll(r *rules.ActionRule, wd *vdi.WebDriver) error {
	// Get Selectors list
	value := r.Value

	// Get the attribute to scroll to
	var attribute string
	if value == "" {
		attribute = "document.body.scrollHeight"
	} else {
		attribute = value
	}

	// JavaScript to send a POST request to Rbee
	jsScript := fmt.Sprintf(`
        (function() {
            var xhr = new XMLHttpRequest();
            xhr.open("POST", "http://localhost:3000/v1/rb", true);
            xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
            var data = JSON.stringify({
                "Action": "scroll",
                "Value": "%s"
            });
            xhr.onreadystatechange = function () {
                if (xhr.readyState === 4 && xhr.status === 200) {
                    console.log("done.");
                    return true;
                } else if (xhr.readyState === 4) {
                    console.error("Failed: " + xhr.responseText);
                    return false;
                }
            };
            xhr.send(data);
        })();
    `, attribute)

	// Execute the JavaScript in the browser context
	success, err := (*wd).ExecuteScript(jsScript, nil)
	if err == nil && success == true {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Scroll action executed successfully using Rbee")
		return nil
	}
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Failed to execute scroll using Rbee, falling back to Selenium")

	// Fall back to using Selenium's ExecuteScript method
	script := fmt.Sprintf("window.scrollTo(0, %s)", attribute)
	_, err = (*wd).ExecuteScript(script, nil)
	return err
}

// executeActionJS is responsible for executing a "execute_javascript" action
func executeActionJS(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	for _, selector := range r.Selectors {
		if selector.SelectorType == "plugin_call" {
			// retrieve the JavaScript from the plugins registry using the value as the key
			plugin, exists := ctx.re.JSPlugins.GetPlugin(selector.Selector)
			if !exists {
				return fmt.Errorf("plugin not found: %s", selector.Selector)
			}

			// Check r.Value for macros:
			temp := strings.TrimSpace(r.Value)
			if temp != "" {
				switch strings.ToLower(temp) {
				case "%current_url%":
					rval, _ := (*wd).CurrentURL()
					temp = rval
				case "%source_url%":
					temp = ctx.source.URL
				}
			}

			// collect value as an argument to the plugin
			args := []interface{}{}
			args = append(args, temp)

			// Execute the JavaScript
			_, err := (*wd).ExecuteScript(plugin.String(), args)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// executeActionInput is responsible for executing an "input" action
// Note from Paolo:
// This may looks complex, because it is a complex problem to solve!
// This function tries to move the mouse (human-like) to the element,
// clicks (generating a system level event) on it, and then inputs
// the text using Rbee. 'cause that's what us human do and tools like
// Selenium don't.
func executeActionInput(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	var err error

	// Find the element
	wdf, selector, err := findElementBySelectorType(ctx, wd, r.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errNoElementFound, r.RuleName, err)
		return nil
	}

	// If the element is found, attempt to input the text using Rbee
	if wdf != nil {
		loc, err := wdf.Location()
		if err != nil {
			return fmt.Errorf(errFailedToGetLoc, r.RuleName, err)
		}

		// JavaScript to send a POST request to Rbee for mouse move and click
		jsScriptMoveAndClick := fmt.Sprintf(`
            (function() {
                var xhr = new XMLHttpRequest();
                xhr.open("POST", "http://localhost:3000/v1/rb", true);
                xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                var data = JSON.stringify({
                    "Action": "moveMouse",
                    "X": %d,
                    "Y": %d
                });
                xhr.onreadystatechange = function () {
                    if (xhr.readyState === 4 && xhr.status === 200) {
                        var clickXhr = new XMLHttpRequest();
                        clickXhr.open("POST", "http://localhost:3000/v1/rb", true);
                        clickXhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
                        var clickData = JSON.stringify({
                            "Action": "click"
                        });
                        clickXhr.onreadystatechange = function () {
                            if (clickXhr.readyState === 4 && clickXhr.status === 200) {
								console.log("done.");
                                return true; // Clicked successfully
                            } else if (clickXhr.readyState === 4) {
								console.error("Failed: " + clickXhr.responseText);
                                return false; // Failed to click
                            }
                        };
                        clickXhr.send(clickData);
                    } else if (xhr.readyState === 4) {
						console.error("Failed: " + xhr.responseText);
                        return false; // Failed to move mouse
                    }
                };
                xhr.send(data);
            })();
        `, loc.X, loc.Y)

		// Execute the JavaScript to move the mouse and click
		success, err := (*wd).ExecuteScript(jsScriptMoveAndClick, nil)
		if err == nil && success == true {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionInput] Mouse move and click action executed successfully using Rbee")

			attribute := selector.Value

			// JavaScript to send a POST request to Rbee for text input
			jsScriptType := fmt.Sprintf(`
				(function() {
					var xhr = new XMLHttpRequest();
					xhr.open("POST", "http://localhost:3000/v1/rb", true);
					xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
					var data = JSON.stringify({
						"Action": "type",
						"Value": "%s"
					});
					xhr.onreadystatechange = function () {
						if (xhr.readyState === 4 && xhr.status === 200) {
							console.log("done.");
							return true; // Typed successfully
						} else if (xhr.readyState === 4) {
							console.error("Failed: " + xhr.responseText);
							return false; // Failed to type
						}
					};
					xhr.send(data);
				})();
			`, attribute)

			// Execute the JavaScript to type the text
			success, err := (*wd).ExecuteScript(jsScriptType, nil)
			if err == nil && success == true {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionInput] Text input action executed successfully using Rbee")
				return nil
			}
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionInput] Failed to execute text input using Rbee, falling back to Selenium")
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ActionInput] Failed to execute mouse move and click using Rbee, falling back to Selenium")
		}

		// Fall back to using Selenium's Click and SendKeys methods
		err = wdf.Click()
		if err != nil {
			return fmt.Errorf("failed to click on element: %v", err)
		}

		attribute := selector.Value
		err = wdf.SendKeys(attribute)
		return err
	}

	return err
}

// findElementBySelectorType is responsible for finding an element in the WebDriver
// using the appropriate selector type. It returns the first element found and an error.
func findElementBySelectorType(ctx *ProcessContext, wd *vdi.WebDriver, selectors []rules.Selector) (vdi.WebElement, rules.Selector, error) {
	var wdf vdi.WebElement
	var err error
	var selector rules.Selector
	for _, selector = range selectors {
		wdf, err = FindElementByType(ctx, wd, selector)
		if err == nil && wdf != nil {
			break
		}
	}

	return wdf, selector, err
}

// DefaultActionConfig returns a default configuration for the action rules
func DefaultActionConfig(url string) cfg.SourceConfig {
	return cfg.SourceConfig{
		FormatVersion: "1.0",
		Author:        "The CROWler team",
		CreatedAt:     time.Now(),
		Description:   "Default configuration",
		SourceName:    "Example Source",
		CrawlingConfig: cfg.CrawlingConfig{
			Site: url,
		},
		ExecutionPlan: []cfg.ExecutionPlanItem{
			{
				Label: "Default Execution Plan",
				Conditions: cfg.Condition{
					URLPatterns: []string{url},
				},
				RuleGroups: []string{"CookieAcceptanceRulesExtended"},
			},
		},
	}
}

func runDefaultActionRules(wd *vdi.WebDriver, ctx *ProcessContext) {
	// Execute the default scraping rules
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing default action rules...")

	// Get the default scraping rules
	url, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting the current URL: %v", err)
		url = ""
	}
	rs := DefaultActionConfig(url)
	// Check if the conditions are met
	if len(rs.ExecutionPlan) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No execution plan found for the current URL")
		return
	}
	// Execute all the rules in the ruleset
	for _, r := range rs.ExecutionPlan {
		// Check the conditions
		if !checkActionPreConditions(r.Conditions, url) {
			continue
		}
		if !checkActionConditions(ctx, r.AdditionalConditions, wd) {
			continue
		}
		if len(r.Rulesets) > 0 {
			executePlannedRulesets(wd, ctx, r)
		}
		if len(r.RuleGroups) > 0 {
			executePlannedRuleGroups(wd, ctx, r)
		}
		if len(r.Rules) > 0 {
			executePlannedRules(wd, ctx, r)
		}
	}
}

// checkActionPreConditions checks if the pre conditions are met
// for example if the page URL is listed in the list of URLs
// for which this rule is valid.
func checkActionPreConditions(conditions cfg.Condition, url string) bool {
	canProceed := true
	// Check the URL patterns
	if len(conditions.URLPatterns) > 0 {
		for _, pattern := range conditions.URLPatterns {
			if strings.Contains(url, pattern) {
				canProceed = true
			} else {
				canProceed = false
			}
		}
	}
	return canProceed
}

// checkActionConditions checks all types of conditions: Action and Config Conditions
// These are page related conditions, for instance check if an element is present
// or if the page is in the desired language etc.
func checkActionConditions(ctx *ProcessContext, conditions map[string]interface{}, wd *vdi.WebDriver) bool {
	canProceed := true
	// Check the additional conditions
	if len(conditions) > 0 {
		// Check if the page contains a specific element
		if _, ok := conditions["element"]; ok {
			// Check if the element is present
			_, err := (*wd).FindElement(vdi.ByCSSSelector, conditions["element"].(string))
			if err != nil {
				canProceed = false
			}
		}
		// If a language condition is present, check if the page is in the correct language
		if _, ok := conditions["language"]; ok {
			// Get the page language
			lang, err := (*wd).ExecuteScript("return document.documentElement.lang", nil)
			if err != nil {
				canProceed = false
			}
			// Check if the language is correct
			if lang != conditions["language"] {
				canProceed = false
			}
		}
		// If the requested script returns true, proceed
		if _, ok := conditions["plugin_call"]; ok {
			// retrieve the JavaScript from the plugins registry using the value as the key
			plugin, exists := ctx.re.JSPlugins.GetPlugin(conditions["selector"].(string))
			if !exists {
				canProceed = false
			} else {
				pluginCode := plugin.String()
				rval, err := (*wd).ExecuteScript(pluginCode, nil)
				if err != nil {
					canProceed = false
				} else {
					// Process rval
					rvalStr := fmt.Sprintf("%v", rval)
					rvalStr = strings.ToLower(strings.TrimSpace(rvalStr))
					if rvalStr == "true" {
						canProceed = true
					} else {
						canProceed = false
					}
				}
			}
		}
	}
	return canProceed
}

// executePlannedRules executes the rules in the execution plan
func executePlannedRules(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rules in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rules...")
	// Get the rule
	for _, ruleName := range planned.Rules {
		if ruleName == "" {
			continue
		}
		executeActionRuleByName(ruleName, wd, ctx)
	}
}

func executeActionRuleByName(ruleName string, wd *vdi.WebDriver, ctx *ProcessContext) {
	rule, err := ctx.re.GetActionRuleByName(ruleName)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting action rule: %v", err)
		return
	}

	// Execute the rule
	if err = executeActionRule(ctx, rule, wd); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
		if !rule.ErrorHandling.Ignore {
			if rule.ErrorHandling.RetryCount > 0 {
				for i := 0; i < rule.ErrorHandling.RetryCount; i++ {
					if rule.ErrorHandling.RetryDelay > 0 {
						time.Sleep(time.Duration(rule.ErrorHandling.RetryDelay) * time.Second)
					}
					if err = executeActionRule(ctx, rule, wd); err == nil {
						break
					}
				}
			}
		}
	}
}

// executePlannedRuleGroups executes the rule groups in the execution plan
func executePlannedRuleGroups(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rule groups in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rule groups...")
	// Get the rule group
	for _, ruleGroupName := range planned.RuleGroups {
		if strings.TrimSpace(ruleGroupName) == "" {
			continue
		}
		rg, err := ctx.re.GetRuleGroupByName(ruleGroupName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting rule group '%s': %v", ruleGroupName, err)
		} else {
			// Execute the rule group
			executeActionRules(ctx, rg.GetActionRules(), wd)
			ctx.Status.TotalActions.Add(int32(len(rg.GetActionRules())))
		}
	}
}

// executePlannedRulesets executes the rulesets in the execution plan
func executePlannedRulesets(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rulesets in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rulesets...")
	// Get the ruleset
	for _, rulesetName := range planned.Rulesets {
		if rulesetName == "" {
			continue
		}
		rs, err := ctx.re.GetRulesetByName(rulesetName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting ruleset: %v", err)
		} else {
			// Execute the ruleset
			executeActionRules(ctx, rs.GetAllEnabledActionRules(ctx.GetContextID(), true), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}
}

// Retrieve cookies after an action rule has been executed
func retrieveCookies(wd *vdi.WebDriver) (map[string]interface{}, error) {
	cookies, err := (*wd).GetCookies()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "retrieving cookies: %v", err)
	}

	// Transform the cookies into a map[string]interface{} for easier processing
	cookieMap := make(map[string]interface{})
	for _, cookie := range cookies {
		cookieMap[cookie.Name] = cookie.Value
	}

	return cookieMap, err
}
