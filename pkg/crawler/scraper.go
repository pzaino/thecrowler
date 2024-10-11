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
	"strconv"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"

	"golang.org/x/net/html"
)

// ApplyRule applies the provided scraping rule to the provided web page.
func ApplyRule(ctx *ProcessContext, rule *rs.ScrapingRule, webPage *selenium.WebDriver) map[string]interface{} {
	// Debug message
	cmn.DebugMsg(cmn.DbgLvlInfo, "Applying scraping rule: %v", rule.RuleName)

	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Iterate over the elements to be extracted
	for _, elementSet := range rule.Elements {
		key := elementSet.Key
		selectors := elementSet.Selectors

		var allExtracted []string
		for _, element := range selectors {
			selectorType := strings.ToLower(strings.TrimSpace(element.SelectorType))
			getAllOccurrences := element.ExtractAllOccurrences

			extracted := extractContent(ctx, webPage, element, getAllOccurrences)
			if len(extracted) > 0 {
				allExtracted = append(allExtracted, extracted...)
				if !getAllOccurrences || selectorType == "plugin_call" {
					break
				}
			}
		}
		if len(allExtracted) > 0 {
			// Ensure that allExtracted is JSON valid
			if len(allExtracted) == 1 {
				// Check if it's a JSON string
				if json.Valid([]byte(allExtracted[0])) {
					var jsonData map[string]interface{}
					if err := json.Unmarshal([]byte(allExtracted[0]), &jsonData); err == nil {
						extractedData[key] = jsonData
						continue
					}
				}
			} else {
				// Check if it's a JSON array
				if json.Valid([]byte("[" + strings.Join(allExtracted, ",") + "]")) {
					var jsonData []interface{}
					if err := json.Unmarshal([]byte("["+strings.Join(allExtracted, ",")+"]"), &jsonData); err == nil {
						extractedData[key] = jsonData
						continue
					}
				}
			}
		}
	}

	// Optional: Extract JavaScript files if required
	if rule.JsFiles {
		jsFiles := extractJSFiles(webPage)
		extractedData["js_files"] = jsFiles
	}

	return extractedData
}

// extractJSFiles extracts the JavaScript files from the current page.
func extractJSFiles(wd *selenium.WebDriver) []CollectedScript {
	var jsFiles []CollectedScript

	script := `
	var scripts = document.getElementsByTagName('script');
	var result = [];
	for (var i = 0; i < scripts.length; i++) {
		if (scripts[i].src) {
			try {
				var xhr = new XMLHttpRequest();
				xhr.open('GET', scripts[i].src, false);  // synchronous request
				xhr.send(null);
				if (xhr.status === 200) {
					result.push({type: 'external', content: xhr.responseText});
				} else {
					result.push({type: 'external', content: 'Error: ' + xhr.statusText});
				}
			} catch (e) {
				result.push({type: 'external', content: 'Error: ' + e.message});
			}
		} else {
			result.push({type: 'inline', content: scripts[i].innerHTML});
		}
	}
	return result;
	`

	// Execute the JavaScript
	res, err := (*wd).ExecuteScript(script, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error executing the script: %v", err)
	}

	// Parse the result
	scriptContents, ok := res.([]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "Expected script content to be a slice of interfaces")
	}

	for i, script := range scriptContents {
		scriptMap, ok := script.(map[string]interface{})
		if !ok {
			continue
		}

		// Get the script content
		scriptOrig, ok := scriptMap["content"].(string)
		if !ok {
			continue
		}

		// Check if the script is obfuscated
		isObfuscated := detectObfuscation(scriptOrig)

		// Deobfuscate the script content if needed
		var scriptSource string
		if isObfuscated {
			scriptSource = deobfuscateScript(scriptOrig)
		}

		scriptContent := "<!---- TheCRWOler: [Extracted script number: " + fmt.Sprint(i) +
			", of type: " + scriptMap["type"].(string) + "] //---->\n" +
			scriptSource +
			"\n<!---- TheCRWOler: [End of extracted script number: " + fmt.Sprint(i) + "] //---->"

		// Append the script content to the list
		newCollectedScript := CollectedScript{
			ID:           uint64(i),
			ScriptType:   scriptMap["type"].(string),
			Original:     scriptOrig,
			Script:       scriptContent,
			Errors:       lintScript(scriptContent),
			IsObfuscated: isObfuscated,
		}

		jsFiles = append(jsFiles, newCollectedScript)

	}

	return jsFiles
}

func detectObfuscation(scriptContent string) bool {
	// Calculate entropy
	entropy := cmn.CalculateEntropy(scriptContent)

	// Check for high entropy and common obfuscation patterns
	if entropy > 4.0 || containsObfuscationPatterns(scriptContent) {
		return true
	}
	return false
}

func containsObfuscationPatterns(scriptContent string) bool {
	// Check for common obfuscation patterns
	patterns := []string{
		"eval(", "Function(", "document.write(", "String.fromCharCode(", "\\x", "\\u",
	}
	for _, pattern := range patterns {
		if strings.Contains(scriptContent, pattern) {
			return true
		}
	}
	return false
}

func deobfuscateScript(scriptContent string) string {
	// Apply various deobfuscation techniques

	// Unescape Unicode sequences
	re := regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)
	scriptContent = re.ReplaceAllStringFunc(scriptContent, func(match string) string {
		r, _ := strconv.ParseInt(match[2:], 16, 32)
		return string(rune(r))
	})

	// Unescape hexadecimal sequences
	re = regexp.MustCompile(`\\x[0-9a-fA-F]{2}`)
	scriptContent = re.ReplaceAllStringFunc(scriptContent, func(match string) string {
		r, _ := strconv.ParseInt(match[2:], 16, 32)
		return string(rune(r))
	})

	// TODO: I need to improve this and add further deobfuscation techniques here

	return scriptContent
}

func lintScript(scriptContent string) []string {
	var errors []string

	// Use minify to parse and lint JavaScript
	result := api.Transform(scriptContent, api.TransformOptions{
		Loader: api.LoaderJS,
	})
	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			errors = append(errors, err.Text)
		}
	}

	return errors
}

// extractContent extracts the content from the provided document using the provided CSS selector.
func extractContent(ctx *ProcessContext, wd *selenium.WebDriver, selector rs.Selector, all bool) []string {
	var results []string
	var elements []selenium.WebElement
	var err error
	if all {
		elements, err = FindElementsByType(ctx, wd, selector)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error finding elements: %v", err)
			return results
		}
	} else {
		element, err := FindElementByType(ctx, wd, selector)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error finding element: %v", err)
			return results
		}
		elements = append(elements, element)
	}
	for _, element := range elements {
		text, _ := element.Text()
		results = append(results, text)
	}
	return results
}

// ApplyRulesGroup extracts the data from the provided web page using the provided a rule group.
func ApplyRulesGroup(ctx *ProcessContext, ruleGroup *rs.RuleGroup, url string, webPage *selenium.WebDriver) (map[string]interface{}, error) {
	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Set the environment variables
	ruleGroup.SetEnv(ctx.GetContextID())

	// Iterate over the rules in the rule group
	for _, rule := range ruleGroup.ScrapingRules {
		// Apply the rule to the web page
		data := ApplyRule(ctx, &rule, webPage)
		// Add the extracted data to the map
		for k, v := range data {
			extractedData[k] = v
		}
	}

	// Remove non-persistent environment variables
	cmn.KVStore.DeleteByCID(ctx.GetContextID())

	return extractedData, nil
}

// ApplyPostProcessingStep applies the provided post-processing step to the provided data.
func ApplyPostProcessingStep(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) {
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
	case "plugin_call":
		ppStepPluginCall(ctx, step, data)
	default:
		cmn.DebugMsg(cmn.DbgLvlError, "Unknown post-processing step type: %v", stepType)
	}
}

func ppStepPluginCall(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) {
	err := processCustomJS(ctx, step, data)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "There was an error while running a rule post-processing JS module: %v", err)
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
func ppStepTransform(ctx *ProcessContext, data *[]byte, step *rs.PostProcessingStep) {
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
func processCustomJS(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) error {
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
		errMsg := fmt.Sprintf("Plugin %s not found", pluginName)
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
