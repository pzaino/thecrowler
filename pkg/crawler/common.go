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
