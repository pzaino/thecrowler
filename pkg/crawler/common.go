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

	"github.com/PuerkitoBio/goquery"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
	"golang.org/x/net/html"
)

// FindElementByType finds an element by the provided selector type
// and returns it if found, otherwise it returns an error.
func FindElementByType(ctx *ProcessContext, wd *vdi.WebDriver, selector rules.Selector) (vdi.WebElement, error) {
	var elements []vdi.WebElement
	var err error
	selectorType := strings.TrimSpace(selector.SelectorType)
	switch strings.ToLower(selectorType) {
	case strCSS:
		elements, err = (*wd).FindElements(vdi.ByCSSSelector, selector.Selector)
	case "id":
		elements, err = (*wd).FindElements(vdi.ByID, selector.Selector)
	case strName:
		elements, err = (*wd).FindElements(vdi.ByName, selector.Selector)
	case strLinkText1, strLinkText2:
		elements, err = (*wd).FindElements(vdi.ByLinkText, selector.Selector)
	case strPartialLinkText1, strPartialLinkText2:
		elements, err = (*wd).FindElements(vdi.ByPartialLinkText, selector.Selector)
	case strTagName1, strTagName2, strTagName3, strTagName4:
		elements, err = (*wd).FindElements(vdi.ByTagName, selector.Selector)
	case strClassName1, strClassName2, strClassName3:
		elements, err = (*wd).FindElements(vdi.ByClassName, selector.Selector)
	case strJSPath:
		js := fmt.Sprintf("return document.querySelector(\"%s\");", selector.Selector)
		res, err := (*wd).ExecuteScript(js, nil)
		if err != nil {
			return nil, fmt.Errorf("error executing JavaScript: %v", err)
		}
		if element, ok := res.(vdi.WebElement); ok {
			elements = append(elements, element)
		} else {
			return nil, fmt.Errorf("no element found for JS Path: %s", selector.Selector)
		}
	case strXPath:
		elements, err = (*wd).FindElements(vdi.ByXPATH, selector.Selector)
	default:
		return nil, fmt.Errorf("unsupported selector type: %s", selectorType)
	}
	if err != nil {
		return nil, fmt.Errorf("error finding element: %v", err)
	}

	// Check for the Value if provided
	var element vdi.WebElement
	for _, e := range elements {
		matchL2 := false
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue, err := e.GetAttribute(strings.TrimSpace(selector.Attribute.Name))
			if err != nil {
				continue
			}
			matchValue := selector.Attribute.Value
			if matchValue != "" {
				re := regexp.MustCompile(matchValue)
				if re.MatchString(attrValue) {
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
func FindElementsByType(ctx *ProcessContext, wd *vdi.WebDriver, selector rules.Selector) ([]vdi.WebElement, error) {
	var elements []vdi.WebElement
	var err error

	switch strings.ToLower(strings.TrimSpace(selector.SelectorType)) {
	case strCSS:
		elements, err = (*wd).FindElements(vdi.ByCSSSelector, selector.Selector)
	case "id":
		elements, err = (*wd).FindElements(vdi.ByID, selector.Selector)
	case strName:
		elements, err = (*wd).FindElements(vdi.ByName, selector.Selector)
	case strLinkText1, strLinkText2:
		elements, err = (*wd).FindElements(vdi.ByLinkText, selector.Selector)
	case strPartialLinkText1, strPartialLinkText2:
		elements, err = (*wd).FindElements(vdi.ByPartialLinkText, selector.Selector)
	case strTagName1, strTagName2, strTagName3, strTagName4:
		elements, err = (*wd).FindElements(vdi.ByTagName, selector.Selector)
	case strClassName1, strClassName2, strClassName3:
		elements, err = (*wd).FindElements(vdi.ByClassName, selector.Selector)
	case strJSPath:
		js := fmt.Sprintf("return document.querySelector(\"%s\");", selector.Selector)
		res, err := (*wd).ExecuteScript(js, nil)
		if err != nil {
			return nil, fmt.Errorf("error executing JavaScript: %v", err)
		}
		if element, ok := res.(vdi.WebElement); ok {
			elements = append(elements, element)
		} else {
			return nil, fmt.Errorf("no element found for JS Path: %s", selector.Selector)
		}
	case strPluginCall:
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
		if elements, ok = result.([]vdi.WebElement); !ok {
			return nil, fmt.Errorf("plugin did not return a list of elements")
		}
	case strXPath:
		elements, err = (*wd).FindElements(vdi.ByXPATH, selector.Selector)
	default:
		return nil, fmt.Errorf("unsupported selector type: %s", selector.SelectorType)
	}
	if err != nil {
		return nil, fmt.Errorf("error finding element: %v", err)
	}

	// Check for the Value if provided
	for i, e := range elements {
		matchL2 := false
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue, err := e.GetAttribute(strings.TrimSpace(selector.Attribute.Name))
			if err != nil {
				continue
			}
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

func matchValue(ctx *ProcessContext, item interface{}, selector rules.Selector) bool {
	var wdfText string
	var err error
	if tmp1, ok := item.(vdi.WebElement); ok { // Check if the wdf is a WebElement
		// Get the element Text
		wdfText, err = tmp1.Text()
		if err != nil {
			return false
		}
	} else if tmp2, ok := item.(goquery.Selection); ok { // Check if item is a goquery.Selection
		// Get the goquery.Selection Text
		wdfText = tmp2.Text()
	} else if tmp3, ok := item.(*html.Node); ok { // Check if item is a *html.Node
		// Get the *html.Node Text
		wdfText = tmp3.Data
	} else {
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

// WaitForCondition waits for a condition to be met before continuing.
func WaitForCondition(ctx *ProcessContext, wd *vdi.WebDriver, r rs.WaitCondition) error {
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
	case strPluginCall:
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
