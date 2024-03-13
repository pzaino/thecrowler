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

// Package scrapper implements the scrapper library for the Crowler and
// the Crowler search engine.
package scraper

import (
	"regexp"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/robertkrimen/otto"
	"golang.org/x/net/html"
)

// ApplyRule applies the provided scraping rule to the provided web page.
func ApplyRule(rule *rs.ScrapingRule, webPage *selenium.WebDriver) map[string]interface{} {
	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Prepare content for goquery:
	htmlContent, _ := (*webPage).PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading HTML content: %v", err)
		return extractedData
	}
	// Parse the HTML content
	node, err := htmlquery.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// handle error
		cmn.DebugMsg(cmn.DbgLvlError, "Error parsing HTML content: %v", err)
		return extractedData
	}

	// Iterate over the elements to be extracted
	for _, elementSet := range rule.Elements {
		key := elementSet.Key
		selectors := elementSet.Selectors
		for _, element := range selectors {
			selectorType := element.SelectorType
			selector := element.Selector

			var extracted string
			switch selectorType {
			case "css":
				extracted = extractByCSS(doc, selector)
			case "xpath":
				extracted = extractByXPath(node, selector)
			case "id":
				extracted = extractByCSS(doc, "#"+selector)
			case "class", "class_name":
				extracted = extractByCSS(doc, "."+selector)
			case "name":
				extracted = extractByCSS(doc, "[name="+selector+"]")
			case "tag":
				extracted = extractByCSS(doc, selector)
			case "link_text", "partial_link_text":
				extracted = extractByCSS(doc, "a:contains('"+selector+"')")
			case "regex":
				extracted = extractByRegex(htmlContent, selector)
			default:
				extracted = ""
			}
			if extracted != "" {
				extractedData[key] = extracted
				break
			}
		}
	}

	// Optional: Extract JavaScript files if required
	if rule.JsFiles {
		jsFiles := extractJSFiles(doc)
		extractedData["js_files"] = jsFiles
	}

	return extractedData
}

// extractJSFiles extracts the JavaScript files from the provided document.
func extractJSFiles(doc *goquery.Document) []string {
	var jsFiles []string
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			jsFiles = append(jsFiles, src)
		}
	})
	return jsFiles
}

// extractByCSS extracts the content from the provided document using the provided CSS selector.
func extractByCSS(doc *goquery.Document, selector string) string {
	return doc.Find(selector).Text()
}

func extractByXPath(node *html.Node, selector string) string {
	extractedNode := htmlquery.FindOne(node, selector)
	if extractedNode != nil {
		return htmlquery.InnerText(extractedNode)
	}
	return ""
}

func extractByRegex(htmlContent string, selector string) string {
	regex, err := regexp.Compile(selector)
	if err != nil {
		// handle regex compilation error
		return ""
	}
	matches := regex.FindStringSubmatch(htmlContent)
	if len(matches) > 1 { // matches[0] is the full match, matches[1] is the first group
		return matches[1]
	}
	return ""
}

// ApplyRulesGroup extracts the data from the provided web page using the provided a rule group.
func ApplyRulesGroup(ruleGroup *rs.RuleGroup, url string, webPage *selenium.WebDriver) (map[string]interface{}, error) {
	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Iterate over the rules in the rule group
	for _, rule := range ruleGroup.ScrapingRules {
		// Apply the rule to the web page
		data := ApplyRule(&rule, webPage)
		// Add the extracted data to the map
		for k, v := range data {
			extractedData[k] = v
		}
	}

	return extractedData, nil
}

// ApplyPostProcessingStep applies the provided post-processing step to the provided data.
func ApplyPostProcessingStep(step *rs.PostProcessingStep, data *[]byte) {
	// Implement the post-processing step here
	stepType := strings.ToLower(strings.TrimSpace(step.Type))
	switch stepType {
	case "replace":
		ppStepReplace(data, step)
	case "remove":
		ppStepRemove(data, step)
	case "transform":
		ppStepTransform(data, step)
	}
}

// ppStepReplace applies the "replace" post-processing step to the provided data.
func ppStepReplace(data *[]byte, step *rs.PostProcessingStep) {
	// Replace all instances of step.Details["target"] with step.Details["replacement"] in data
	*data = []byte(strings.ReplaceAll(string(*data), step.Details["target"].(string), step.Details["replacement"].(string)))
}

// ppStepRemove applies the "remove" post-processing step to the provided data.
func ppStepRemove(data *[]byte, step *rs.PostProcessingStep) {
	// Remove all instances of step.Details["target"] from data
	*data = []byte(strings.ReplaceAll(string(*data), step.Details["target"].(string), ""))
}

// ppStepTransform applies the "transform" post-processing step to the provided data.
func ppStepTransform(data *[]byte, step *rs.PostProcessingStep) {
	// Implement the transformation logic here
	transformType := strings.ToLower(strings.TrimSpace(step.Details["transform_type"].(string)))
	switch transformType {
	case "api": // Call an API to transform the data
		// Implement the API call here
	case "custom": // Use a custom transformation function
		result, err := processCustomJS(step, data)
		// Convert the value to a string and set it in the data slice.
		if err == nil {
			*data = []byte(result)
		}
	}
}

// processCustomJS executes the provided custom JavaScript code using the provided VM.
// The JavaScript module must be written as follows:
/*
	// Parse the JSON string back into an object
	var dataObj = JSON.parse(jsonDataString);

	// Let's assume you want to manipulate or use the data somehow
	function processData(data) {
		// Example manipulation: create a greeting message
		return "Hello, " + data.name + " from " + data.city + "!";
	}

	// Call processData with the parsed object
	var result = processData(dataObj);
	result; // This will be the return value of vm.Run(jsCode)
*/
func processCustomJS(step *rs.PostProcessingStep, data *[]byte) (string, error) {
	// Create a new instance of the Otto VM.
	vm := otto.New()

	// Convert the jsonData byte slice to a string and set it in the JS VM.
	jsonData := *data
	if err := vm.Set("jsonDataString", string(jsonData)); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error setting jsonDataString in JS VM: %v", err)
		return "", err
	}

	// Execute the JavaScript code.
	value, err := vm.Run(step.Details["custom_js"].(string))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error executing JS: %v", err)
		return "", err
	}

	// Convert the value to a string
	return value.String(), nil
}
