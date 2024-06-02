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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// ApplyRule applies the provided scraping rule to the provided web page.
func ApplyRule(rule *rs.ScrapingRule, webPage *selenium.WebDriver) map[string]interface{} {
	// Debug message
	cmn.DebugMsg(cmn.DbgLvlInfo, "Applying rule: %v", rule.RuleName)

	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Prepare content for goquery:
	htmlContent, _ := (*webPage).PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading HTML content: %v", err)
		return extractedData
	}

	// Parse the HTML content
	node, err := htmlquery.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// handle error
		cmn.DebugMsg(cmn.DbgLvlError, "parsing HTML content: %v", err)
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
	var scriptID int
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			scriptID += 1
			scriptTag := "<!---- TheCRWOler: [Extracted script number: " + fmt.Sprint(scriptID) + "] //---->\n"
			jsFiles = append(jsFiles, scriptTag)
			jsFiles = append(jsFiles, src)
			scriptTag = "<!---- TheCROWler: [End of extracted script number: " + fmt.Sprint(scriptID) + "] //---->\n"
			jsFiles = append(jsFiles, scriptTag)
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
func ApplyPostProcessingStep(ctx *processContext, step *rs.PostProcessingStep, data *[]byte) {
	// Implement the post-processing step here
	stepType := strings.ToLower(strings.TrimSpace(step.Type))
	switch stepType {
	case "replace":
		ppStepReplace(data, step)
	case "remove":
		ppStepRemove(data, step)
	case "transform":
		ppStepTransform(ctx, data, step)
	case "validate":
		ppStepValidate(data, step)
	case "clean":
		ppStepClean(data, step)
	default:
		cmn.DebugMsg(cmn.DbgLvlError, "Unknown post-processing step type: %v", stepType)
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

// ppStepValidate applies the "validate" post-processing step to the provided data.
// The step should contain a list of keys that must be present in the data.
// If any of the keys is missing, an error message is logged.
func ppStepValidate(data *[]byte, step *rs.PostProcessingStep) {
	// check if the data is a valid JSON document
	if !json.Valid(*data) {
		cmn.DebugMsg(cmn.DbgLvlError, "Data is not valid JSON")
	}
	// Get keys from the step details and check if they are present in the data
	for _, key := range step.Details["keys"].([]string) {
		if !strings.Contains(string(*data), key) {
			cmn.DebugMsg(cmn.DbgLvlError, "Key %v is missing from the data", key)
		}
	}
}

// ppStepClean applies the "clean" post-processing step to the provided data.
// The step should contain a "target" key that specifies the string to be removed from the data.
func ppStepClean(data *[]byte, step *rs.PostProcessingStep) {
	// Clean the data based on the provided details
	if step == nil {
		return
	}
	if len(step.Details) == 0 {
		return
	}
	// Process all the details in the step
	for key, value := range step.Details {
		// Cast interface to bool
		var useValue bool
		if value != nil {
			useValue = value.(bool)
		} else {
			useValue = false
		}
		switch key {
		case "remove_html":
			if useValue {
				*data = []byte(stripHTML(string(*data)))
			}
		case "remove_whitespace":
			if useValue {
				*data = []byte(strings.ReplaceAll(string(*data), " ", ""))
			}
		case "remove_extra_whitespace":
			if useValue {
				*data = []byte(strings.Join(strings.Fields(string(*data)), " "))
			}
		case "remove_newlines":
			if useValue {
				*data = []byte(strings.ReplaceAll(string(*data), "\n", ""))
			}
		case "remove_special_chars":
			if useValue {
				*data = []byte(stripSpecialChars(string(*data)))
			}
		case "remove_numbers":
			if useValue {
				*data = []byte(stripNumbers(string(*data)))
			}
		case "decode_html_entities":
			if useValue {
				*data = []byte(html.UnescapeString(string(*data)))
			}
		}
	}
}

func stripHTML(data string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(data, "")
}

func stripSpecialChars(data string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9\s]`)
	return re.ReplaceAllString(data, "")
}

func stripNumbers(data string) string {
	re := regexp.MustCompile(`[0-9]`)
	return re.ReplaceAllString(data, "")
}

// ppStepTransform applies the "transform" post-processing step to the provided data.
func ppStepTransform(ctx *processContext, data *[]byte, step *rs.PostProcessingStep) {
	// Implement the transformation logic here
	transformType := strings.ToLower(strings.TrimSpace(step.Details["transform_type"].(string)))
	var err error
	switch transformType {
	case "api": // Call an API to transform the data
		// Implement the API call here
		err = processAPITransformation(step, data)
	case "plugin_call": // Use a custom transformation function
		err = processCustomJS(ctx, step, data)

	}
	// Convert the value to a string and set it in the data slice.
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "There was an error while running a rule post-processing JS module: %v", err)
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
func processCustomJS(ctx *processContext, step *rs.PostProcessingStep, data *[]byte) error {
	// Convert the jsonData byte slice to a string and set it in the JS VM.
	jsonData := *data
	var jsonDataMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &jsonDataMap); err != nil {
		errMsg := fmt.Sprintf("Error unmarshalling jsonData: %v", err)
		return errors.New(errMsg)
	}
	/*
		if err = vm.Set("jsonDataString", string(jsonData)); err != nil {
			errMsg := fmt.Sprintf("Error setting jsonDataString in JS VM: %v", err)
			return errors.New(errMsg)
		}
	*/
	// Prepare script parameters
	params := make(map[string]interface{})
	params["jsonData"] = string(jsonData)

	// Retrieve the JS plugin
	pluginName := step.Details["plugin_name"].(string)
	plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
	if !exists {
		errMsg := fmt.Sprintf("Plugin %v not found", pluginName)
		return errors.New(errMsg)
	}

	// Execute the plugin
	value, err := plugin.Execute(ctx.config.Plugins.PluginTimeout, params)
	if err != nil {
		errMsg := fmt.Sprintf("Error executing JS plugin: %v", err)
		return errors.New(errMsg)
	}

	// Check if the result is a string
	for _, val := range value {
		// Convert the value to a string and check if it's a valid JSON
		valStr := fmt.Sprintf("%v", val)

		// Convert the value to a string and check if it's a valid JSON
		if !json.Valid([]byte(valStr)) {
			continue
		}

		// Set the data to the new value
		*data = []byte(valStr)
		return nil
	}

	// It seems we were unable to retrieve the result from the JS output
	return errors.New("modified JSON is not valid")
}

// processAPITransformation allows to use a 3rd party API to process the JSON
func processAPITransformation(step *rs.PostProcessingStep, data *[]byte) error {
	// Implement an API client that uses step.Details[] items to connect to a
	// 3rd party API, pass our JSON document in data and retrieve the results

	if err := validateAPIURL(step); err != nil {
		return err
	}

	protocol, sslMode := determineProtocolAndSSLMode(step)
	url := protocol + ":://" + step.Details["api_url"].(string)

	timeout := determineTimeout(step)

	httpClient := &http.Client{
		Transport: cmn.SafeTransport(timeout, sslMode),
	}

	request := buildRequest(step, data)

	req, err := http.NewRequest("POST", url, strings.NewReader(request))
	if err != nil {
		return fmt.Errorf("failed to create POST request to %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response from %s: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	*data = body
	resp.Body.Close()

	return nil
}

func validateAPIURL(step *rs.PostProcessingStep) error {
	if step.Details["api_url"] == nil {
		return errors.New("API URL is missing")
	}
	return nil
}

func determineProtocolAndSSLMode(step *rs.PostProcessingStep) (string, string) {
	var protocol string
	var sslMode string
	if step.Details["ssl_mode"] == nil {
		protocol = "http"
		sslMode = "disable"
	} else {
		sslMode = strings.ToLower(strings.TrimSpace(step.Details["ssl_mode"].(string)))
		if sslMode == "disable" || sslMode == "disabled" {
			protocol = "http"
		} else {
			protocol = "https"
		}
	}
	return protocol, sslMode
}

func determineTimeout(step *rs.PostProcessingStep) int {
	var timeout int
	if step.Details["timeout"] == nil {
		timeout = 15
	} else {
		timeout = step.Details["timeout"].(int)
	}
	return timeout
}

func buildRequest(step *rs.PostProcessingStep, data *[]byte) string {
	request := "{"
	if step.Details["api_key"] != nil {
		request += "\"api_key\": \"" + step.Details["api_key"].(string) + "\","
	}
	if step.Details["api_secret"] != nil {
		request += "\"api_secret\": \"" + step.Details["api_secret"].(string) + "\","
	}
	if step.Details["custom_json"] != nil {
		request += step.Details["custom_json"].(string) + ","
	}
	if step.Details["data_label"] != nil {
		request += step.Details["data_label"].(string) + " { "
	} else {
		request += "\"data\": {"
	}
	request += string(*data)
	request += "}"
	request += "}"
	return request
}
