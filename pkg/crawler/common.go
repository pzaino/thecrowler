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
	"regexp"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// FindElementByType finds an element by the provided selector type
// and returns it if found, otherwise it returns an error.
func FindElementByType(ctx *ProcessContext, wd *selenium.WebDriver, selector rules.Selector) (selenium.WebElement, error) {
	var elements []selenium.WebElement
	var err error
	selectorType := strings.TrimSpace(selector.SelectorType)
	switch strings.ToLower(selectorType) {
	case "css":
		elements, err = (*wd).FindElements(selenium.ByCSSSelector, selector.Selector)
	case "id":
		elements, err = (*wd).FindElements(selenium.ByID, selector.Selector)
	case "name":
		elements, err = (*wd).FindElements(selenium.ByName, selector.Selector)
	case "linktext", "link_text":
		elements, err = (*wd).FindElements(selenium.ByLinkText, selector.Selector)
	case "partiallinktext", "partial_link_text":
		elements, err = (*wd).FindElements(selenium.ByPartialLinkText, selector.Selector)
	case "tagname", "tag_name", "tag", "element":
		elements, err = (*wd).FindElements(selenium.ByTagName, selector.Selector)
	case "class", "classname", "class_name":
		elements, err = (*wd).FindElements(selenium.ByClassName, selector.Selector)
	case "js_path":
		js := fmt.Sprintf("return document.querySelector(\"%s\");", selector.Selector)
		res, err := (*wd).ExecuteScript(js, nil)
		if err != nil {
			return nil, fmt.Errorf("error executing JavaScript: %v", err)
		}
		if element, ok := res.(selenium.WebElement); ok {
			elements = append(elements, element)
		} else {
			return nil, fmt.Errorf("no element found for JS Path: %s", selector.Selector)
		}
	case "xpath":
		elements, err = (*wd).FindElements(selenium.ByXPATH, selector.Selector)
	default:
		return nil, fmt.Errorf("unsupported selector type: %s", selectorType)
	}
	if err != nil {
		return nil, fmt.Errorf("error finding element: %v", err)
	}

	// Check for the Value if provided
	var element selenium.WebElement
	for _, e := range elements {
		matchL2 := false
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue, _ := e.GetAttribute(strings.TrimSpace(selector.Attribute.Name))
			matchValue := strings.TrimSpace(selector.Attribute.Value)
			if matchValue != "" && matchValue != "*" && matchValue != ".*" {
				if strings.EqualFold(strings.TrimSpace(attrValue), strings.TrimSpace(selector.Attribute.Value)) {
					matchL2 = true
				}
			} else {
				matchL2 = true
			}
		} else {
			matchL2 = true
		}
		matchL3 := false
		if matchL2 && strings.TrimSpace(selector.Value) != "" {
			if matchValue(ctx, e, selector) {
				matchL3 = true
			}
		} else {
			if matchL2 {
				matchL3 = true
			}
		}
		if matchL3 {
			element = e
			break
		}
	}
	if element == nil {
		return element, fmt.Errorf("element '%s' Not found", selector.Selector)
	}

	return element, nil
}

// FindElementsByType finds all elements by the provided selector type
// and returns them, otherwise it returns an error.
func FindElementsByType(ctx *ProcessContext, wd *selenium.WebDriver, selector rules.Selector) ([]selenium.WebElement, error) {
	var elements []selenium.WebElement
	var err error
	selectorType := strings.TrimSpace(selector.SelectorType)
	switch strings.ToLower(selectorType) {
	case "css":
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Finding elements by CSS Selector: '%s'", selector.Selector)
		elements, err = (*wd).FindElements(selenium.ByCSSSelector, selector.Selector)
	case "id":
		elements, err = (*wd).FindElements(selenium.ByID, selector.Selector)
	case "name":
		elements, err = (*wd).FindElements(selenium.ByName, selector.Selector)
	case "linktext", "link_text":
		elements, err = (*wd).FindElements(selenium.ByLinkText, selector.Selector)
	case "partiallinktext", "partial_link_text":
		elements, err = (*wd).FindElements(selenium.ByPartialLinkText, selector.Selector)
	case "tagname", "tag_name", "tag", "element":
		elements, err = (*wd).FindElements(selenium.ByTagName, selector.Selector)
	case "class", "classname", "class_name":
		elements, err = (*wd).FindElements(selenium.ByClassName, selector.Selector)
	case "js_path":
		js := fmt.Sprintf("return document.querySelector(\"%s\");", selector.Selector)
		res, err := (*wd).ExecuteScript(js, nil)
		if err != nil {
			return nil, fmt.Errorf("error executing JavaScript: %v", err)
		}
		if element, ok := res.(selenium.WebElement); ok {
			elements = append(elements, element)
		} else {
			return nil, fmt.Errorf("no element found for JS Path: %s", selector.Selector)
		}
	case "plugin_call":
		// Call the plugin
		pluginName := strings.TrimSpace(selector.Selector)
		plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
		if !exists {
			return nil, fmt.Errorf("plugin not found: %s", pluginName)
		}
		pluginCode := plugin.String()
		result, err := (*wd).ExecuteScript(pluginCode, nil)
		if err != nil {
			return nil, fmt.Errorf("error executing plugin: %v", err)
		}
		// result should contain the elements, but it's an interface{} so we need to convert it
		var ok bool
		if elements, ok = result.([]selenium.WebElement); !ok {
			return nil, fmt.Errorf("plugin did not return a list of elements")
		}
	case "xpath":
		elements, err = (*wd).FindElements(selenium.ByXPATH, selector.Selector)
	default:
		return nil, fmt.Errorf("unsupported selector type: %s", selectorType)
	}
	if err != nil {
		return nil, fmt.Errorf("error finding element: %v", err)
	}

	// Check for the Value if provided
	for i, e := range elements {
		matchL2 := false
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue, _ := e.GetAttribute(strings.TrimSpace(selector.Attribute.Name))
			matchValue := strings.TrimSpace(selector.Attribute.Value)
			if matchValue != "" && matchValue != "*" && matchValue != ".*" {
				if strings.EqualFold(strings.TrimSpace(attrValue), strings.TrimSpace(selector.Attribute.Value)) {
					matchL2 = true
				}
			} else {
				matchL2 = true
			}
		} else {
			matchL2 = true
		}
		matchL3 := false
		if matchL2 && strings.TrimSpace(selector.Value) != "" {
			if matchValue(ctx, e, selector) {
				matchL3 = true
			}
		} else {
			if matchL2 {
				matchL3 = true
			}
		}
		if !matchL3 {
			// Remove the element from the list
			elements = append(elements[:i], elements[i+1:]...)
		}
	}
	if len(elements) == 0 {
		return elements, fmt.Errorf("element '%s' Not found", selector.Selector)
	}

	return elements, nil
}

func matchValue(ctx *ProcessContext, wdf selenium.WebElement, selector rules.Selector) bool {
	// Precompute the common value for comparison
	wdfText, err := wdf.Text()
	if err != nil {
		return false
	}

	// Check if the selector value is one of the special cases
	selValue := strings.TrimSpace(selector.Value)
	var rValue cmn.EnvValue
	if strings.HasPrefix(selValue, "{{") && strings.HasSuffix(selValue, "}}") {
		rValue, err = cmn.ProcessEnvTemplate(selValue, ctx.GetContextID())
		if err != nil {
			selValue = ""
		} else {
			// We have a match, let's update the selector value
			switch rValue.Type {
			case "string":
				selValue = rValue.Value.(string)
			case "int":
				selValue = strconv.Itoa(rValue.Value.(int))
			case "float":
				selValue = fmt.Sprintf("%f", rValue.Value.(float64))
			case "bool":
				selValue = fmt.Sprintf("%t", rValue.Value.(bool))
			case "[]string":
				selValue = strings.Join(rValue.Value.([]string), "|")
			case "[]int":
				selValue = cmn.IntSliceToString(rValue.Value.([]int), "|")
			case "[]float64":
				selValue = cmn.Float64SliceToString(rValue.Value.([]float64), "|")
			case "[]float32":
				selValue = cmn.Float32SliceToString(rValue.Value.([]float32), "|")
			case "[]bool":
				selValue = cmn.BoolSliceToString(rValue.Value.([]bool), "|")
			}

		}
	}

	//cmn.DebugMsg(cmn.DbgLvlDebug3, "Selector Value Resolved: '%s'", selValue)

	// Use Regex to match the selValue against the wdfText
	regEx := regexp.MustCompile(selValue)
	return regEx.MatchString(wdfText)
}

func WaitForCondition(ctx *ProcessContext, wd *selenium.WebDriver, r rs.WaitCondition) error {
	// Execute the wait condition
	switch strings.ToLower(strings.TrimSpace(r.ConditionType)) {
	case "element":
		return nil
	case "delay":
		delay := exi.GetFloat(r.Value)
		if delay > 0 {
			time.Sleep(time.Duration(delay) * time.Second)
		}
		return nil
	case "plugin_call":
		plugin, exists := ctx.re.JSPlugins.GetPlugin(r.Value)
		if !exists {
			return fmt.Errorf("plugin not found: %s", r.Value)
		}
		pluginCode := plugin.String()
		_, err := (*wd).ExecuteScript(pluginCode, nil)
		return err
	default:
		return fmt.Errorf("wait condition not supported: %s", r.ConditionType)
	}
}
