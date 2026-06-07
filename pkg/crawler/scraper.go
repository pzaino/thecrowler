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
	"os"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	scraper "github.com/pzaino/thecrowler/pkg/scraper"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// ApplyRule applies the provided scraping rule to the provided web page.
func ApplyRule(ctx *ProcessContext, rule *rs.ScrapingRule, webPage *vdi.WebDriver) (map[string]interface{}, error) {

	_ = vdi.Refresh(ctx)

	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ApplyRule] Applying scraping rule: %v", rule.RuleName)

	// Extracted data will be stored here
	extractedData := make(map[string]interface{})

	// Errors tracking
	errContainer := []error{}
	ErrorState := false
	ErrorMsg := ""

	startTime := time.Now()
	// Iterate over the rule's elements to be extracted
	for e := 0; e < len(rule.Elements); e++ {
		key := rule.Elements[e].Key
		selectors := rule.Elements[e].Selectors
		var allExtracted []interface{} // Changed to []interface{} to handle mixed data types

		// Iterate over the rule element's selectors to extract the data
		for i := 0; i < len(selectors); i++ {
			getAllOccurrences := selectors[i].ExtractAllOccurrences

			// Try to find and extract the data from the web page
			extracted := extractContent(ctx, webPage, selectors[i], getAllOccurrences)

			// Check if there was data extracted and append it to the allExtracted slice
			if len(extracted) > 0 {
				for _, ex := range extracted {
					switch v := ex.(type) {
					case string:
						// Directly append string values
						allExtracted = append(allExtracted, v)
					case float64:
						// Directly append numeric values
						allExtracted = append(allExtracted, v)
					case map[string]interface{}:
						// Directly append map values (as sub-documents)
						// Safely convert the map to JSON and store it directly in extractedData
						jsonStr, err := json.Marshal(v)
						if err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, "marshalling map to JSON: %v", err)
							errContainer = append(errContainer, err)
						}
						allExtracted = append(allExtracted, string(jsonStr))

					case []interface{}:
						// Append all elements of the array
						for _, item := range v {
							switch item := item.(type) {
							case string:
								allExtracted = append(allExtracted, item)
							case float64:
								allExtracted = append(allExtracted, item)
							case map[string]interface{}:
								// Safely convert the map to JSON and store it directly in extractedData
								jsonStr, err := json.Marshal(item)
								if err != nil {
									cmn.DebugMsg(cmn.DbgLvlError, "marshalling map to JSON: %v", err)
									errContainer = append(errContainer, err)
								}
								allExtracted = append(allExtracted, string(jsonStr))
							default:
								// Log unexpected types and skip them
								cmn.DebugMsg(cmn.DbgLvlWarn, "[DEBUG-ApplyRule] Unexpected type in extracted content: %T", item)
								errContainer = append(errContainer, errors.New("unexpected type in extracted content"))
							}
						}

					default:
						// Log unexpected types and skip them
						cmn.DebugMsg(cmn.DbgLvlWarn, "Unexpected type in extracted content: %T", v)
						errContainer = append(errContainer, errors.New("unexpected type in extracted content"))
					}

					// Break early if not extracting all occurrences or using a plugin_call selector
					if !getAllOccurrences || (strings.ToLower(strings.TrimSpace(selectors[i].SelectorType)) == strPluginCall) {
						break
					}
				}
				break // If we found data, break the selectors loop and return the data!
			} else if rule.Elements[e].Critical {
				ErrorState = true
				ErrorMsg = "element not found, with " + errCriticalError + " flag set"
				cmn.DebugMsg(cmn.DbgLvlError, "element not found by rule `%s` "+errCriticalError+": `%v`", rule.RuleName, selectors[i].Selector)
			}
		}

		if rule.Elements[e].TransformHTMLToJSON {
			// Transform the extracted HTML to JSON
			var transformed []interface{}
			for _, item := range allExtracted {
				if strItem, ok := item.(string); ok {
					// Transform the string to an *html type:
					htmlDoc, err := TransformTextToHTML(strItem)
					if err != nil {
						transformed = append(transformed, htmlDoc)
					} else {
						jsonObj := ExtractHTMLData(htmlDoc)
						transformed = append(transformed, jsonObj)
					}
				} else {
					transformed = append(transformed, item)
				}
			}
			allExtracted = transformed
		}

		// Add the extracted data to the WebObject's map
		if len(allExtracted) == 1 {
			// If only one result, store it directly (as an object or string)
			extractedData[key] = allExtracted[0]
		} else {
			// Otherwise, store as an array
			extractedData[key] = allExtracted
		}
	}
	// Check if the Rule has a PostProcessing section
	if len(rule.PostProcessing) != 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ApplyRule] Applying Rule `%s` post-processing steps to the extracted data", rule.RuleName)
		data := cmn.ConvertMapToJSON(extractedData)
		for _, step := range rule.PostProcessing {
			ApplyPostProcessingStep(ctx, &step, &data)
		}
		// Check if data has double "{{" and "}}" at the beginning and end
		if strings.HasPrefix(string(data), "{{") && strings.HasSuffix(string(data), "}}") {
			data = data[1:]
			data = data[:len(data)-1]
		}
		// update the extractedData with the post-processed data
		extractedData = cmn.ConvertJSONToMap(data)
	}
	endTime := time.Now()
	// Log the time taken to extract the data for this element
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ApplyRule] Time taken to execute rule `%s`: %v", rule.RuleName, endTime.Sub(startTime))

	// Optional: Extract JavaScript files if required
	if rule.JsFiles {
		jsFiles := extractJSFiles(webPage)
		extractedData["js_files"] = jsFiles
	}

	// Log the full scraped content for debugging purposes
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-ApplyRule] Full scraped content by rule `%s` (at ApplyRule level): %v", rule.RuleName, extractedData)

	if ErrorState {
		// If there was a (critical) error, return the error message
		extraErrors := ""
		if len(errContainer) > 0 {
			// Append other errors to the error message
			extraErrors = "\n Other issues (if any): "
			for _, err := range errContainer {
				extraErrors += err.Error() + ", "
			}
		}
		ErrorMsg += extraErrors
		return extractedData, errors.New(ErrorMsg)
	}

	// Return the extracted data as a portion of the WebObject's scraped_data: {} JSON object
	return extractedData, nil
}

func extractByPlugin(ctx *ProcessContext, wd *vdi.WebDriver, selector string) []interface{} {
	// Retrieve the JS plugin
	plugin, exists := ctx.re.JSPlugins.GetPlugin(selector)
	if !exists {
		cmn.DebugMsg(cmn.DbgLvlError, "Plugin '%s' does not exist", selector)
		return []interface{}{}
	}

	// Execute the plugin
	value, err := (*wd).ExecuteScript(plugin.String(), nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error executing JS plugin: %v", err)
		return []interface{}{}
	}

	// Handle nil return value from the plugin
	if value == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Plugin '%s' returned `nil`", selector)
		return []interface{}{}
	}

	switch output := value.(type) {
	case string:
		// If the output is a string, check if it's valid JSON
		if json.Valid([]byte(output)) {
			var parsedOutput interface{}
			err := json.Unmarshal([]byte(output), &parsedOutput)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to parse plugin output as JSON: %v", err)
				return []interface{}{output} // Return raw string as fallback
			}
			return []interface{}{parsedOutput} // Store parsed JSON
		}
		// Return raw string as fallback if not valid JSON
		return []interface{}{output}

	case map[string]interface{}:
		// If the output is already a map, return it directly
		return []interface{}{output}

	default:
		// For unsupported types, log a warning and return the raw output
		cmn.DebugMsg(cmn.DbgLvlWarn, "Plugin '%s' returned unsupported type: %T", selector, value)
		return []interface{}{value}
	}
}

// ApplyRulesGroup extracts the data from the provided web page using the provided a rule group.
func ApplyRulesGroup(ctx *ProcessContext, ruleGroup *rs.RuleGroup, _ string, webPage *vdi.WebDriver) (map[string]interface{}, error) {
	_ = vdi.Refresh(ctx)

	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Set the environment variables
	ruleGroup.SetEnv(ctx.GetContextID())

	// Iterate over the rules in the rule group
	for _, rule := range ruleGroup.ScrapingRules {
		// Apply the rule to the web page
		data, err := ApplyRule(ctx, &rule, webPage)
		// Add the extracted data to the map
		for k, v := range data {
			// Check if the key already exists in the map
			if _, exists := extractedData[k]; exists {
				// If the key already exists, append the new value to the existing one
				extractedData[k] = append(extractedData[k].([]interface{}), v)
			} else {
				extractedData[k] = v
			}
		}
		if err != nil {
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
			return extractedData, err
		}
	}

	// Apply the post-processing steps to the extracted data
	if len(ruleGroup.PostProcessing) != 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Applying Rules group's post-processing steps to the extracted data")
		data := cmn.ConvertMapToJSON(extractedData)
		for _, step := range ruleGroup.PostProcessing {
			ApplyPostProcessingStep(ctx, &step, &data)
		}

		// Check if data has double "{{" and "}}" at the beginning and end
		if strings.HasPrefix(string(data), "{{") && strings.HasSuffix(string(data), "}}") {
			data = data[1:]
			data = data[:len(data)-1]
		}

		// update the extractedData with the post-processed data
		for k, v := range cmn.ConvertJSONToMap(data) {
			// Check if the key already exists in the map
			if _, exists := extractedData[k]; exists {
				// If the key already exists, append the new value to the existing one
				extractedData[k] = append(extractedData[k].([]interface{}), v)
			} else {
				extractedData[k] = v
			}
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
	case "set_env":
		ppStepSetEnv(ctx, step, data)
	case strPluginCall:
		ppStepPluginCall(ctx, step, data)
	case string(RuleCallKindAgent):
		ppStepAgentCall(ctx, step, data)
	case "external_api":
		ppStepExternalAPI(ctx, step, data)
	default:
		cmn.DebugMsg(cmn.DbgLvlError, "Unknown post-processing step type: %v", stepType)
	}
}

func ppStepAgentCall(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) {
	if step.AgentCall == nil {
		return
	}
	req := normalizeFromAgentCall(step.AgentCall, "scraping.post_processing")
	res := executeRuleCall(ctx, ctx.GetWebDriver(), req)
	if !res.Success {
		return
	}
	base := cmn.ConvertJSONToMap(*data)
	strategy := strings.ToLower(strings.TrimSpace(step.AgentCall.MergeStrategy))
	if strategy == "" {
		strategy = "replace"
	}
	switch strategy {
	case "merge":
		if m, ok := res.Value.(map[string]interface{}); ok {
			for k, v := range m {
				base[k] = v
			}
		}
	case "append":
		existing, _ := base["_agent_results"].([]interface{})
		base["_agent_results"] = append(existing, res.Value)
	case "ignore":
		// no-op
	default: // replace
		base["_agent_result"] = res.Value
	}
	*data = cmn.ConvertMapToJSON(base)
}

// ppStepSetEnv applies the "set_env" post-processing step to the provided data.
func ppStepSetEnv(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) {
	// Set the environment variable in the context
	envKey := step.Details["env_key"].(string)

	// Transform data into a JSON Document
	var jsonData map[string]interface{}
	if err := json.Unmarshal(*data, &jsonData); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling data: %v", err)
		return
	}

	// Search for the key in the jsonData
	envValue, exists := jsonData[envKey]
	if !exists {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Key %v not found in the data", step.Details["env_value"])
		return
	}

	// Get the env variable properties from the step
	envPropRaw, ok := step.Details["env_properties"]
	envProperties := cmn.Properties{
		Persistent: false,
		Static:     false,
		CtxID:      ctx.GetContextID(),
	}
	if ok {
		// Convert the envPropRaw to a map[string]interface{}
		envPropMap := cmn.ConvertInfToMap(envPropRaw)
		// Check if envPropMap is not nil and has the required keys
		if envPropMap != nil {
			// Extract the persistent and static properties
			persistentSrc := envPropMap["persistent"]
			persistent := false
			if persistentSrc != nil {
				persistent = persistentSrc.(bool)
			}
			staticSrc := envPropMap["static"]
			static := false
			if staticSrc != nil {
				static = staticSrc.(bool)
			}

			// Update the envProperties
			envProperties.Persistent = persistent
			envProperties.Static = static
		}
	}

	err := cmn.KVStore.Set(envKey, envValue, envProperties)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error setting environment variable: %v", err)
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
	result, err := scraper.Replace(scraper.TransformRequest{Data: *data, Details: step.Details})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "replace post-processing: %v", err)
		return
	}
	*data = result.Data
}

func ppStepRemove(data *[]byte, step *rs.PostProcessingStep) {
	result, err := scraper.Remove(scraper.TransformRequest{Data: *data, Details: step.Details})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "remove post-processing: %v", err)
		return
	}
	*data = result.Data
}

func ppStepValidate(data *[]byte, step *rs.PostProcessingStep) {
	_, err := scraper.Validate(scraper.TransformRequest{Data: *data, Details: step.Details})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "%v", err)
	}
}

func ppStepExternalAPI(_ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) {
	// Implement the API call here
	err := processAPITransformation(step, data)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "There was an error while running a rule post-processing external API call: %v", err)
	}
}

// ppStepClean applies the "clean" post-processing step to the provided data.
// The step should contain a "target" key that specifies the string to be removed from the data.
func ppStepClean(data *[]byte, step *rs.PostProcessingStep) {
	if step == nil || len(step.Details) == 0 {
		return
	}
	result, err := scraper.Clean(scraper.TransformRequest{Data: *data, Details: step.Details})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "clean post-processing: %v", err)
		return
	}
	*data = result.Data
}

func ppStepTransform(ctx *ProcessContext, data *[]byte, step *rs.PostProcessingStep) {
	// Implement the transformation logic here
	transformType := strings.ToLower(strings.TrimSpace(step.Details["transform_type"].(string)))
	var err error
	switch transformType {
	case "api": // Call an API to transform the data
		// Implement the API call here
		err = processAPITransformation(step, data)
	case strPluginCall: // Use a custom transformation function
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
	var err error

	// Safely retrieve the JS plugin
	pluginNameRaw, exists := step.Details["plugin_name"]
	if !exists {
		return fmt.Errorf("plugin_name not specified in step details")
	}

	pluginName, isString := pluginNameRaw.(string)
	if !isString {
		return fmt.Errorf("plugin_name is not a valid string")
	}

	plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
	if !exists {
		return fmt.Errorf("plugin '%s' not found", pluginName)
	}

	// Convert the jsonData byte slice to a map
	jsonData := *data
	var jsonDataMap map[string]interface{}
	if err = json.Unmarshal(jsonData, &jsonDataMap); err != nil {
		return fmt.Errorf("error unmarshalling jsonData for plugin `%s`: %v", pluginName, err)
	}

	// Prepare script parameters
	params := make(map[string]interface{})
	params["json_data"] = jsonDataMap

	// Check if we have a valid webdriver
	params["currentURL"] = ""
	if ctx.wd != nil {
		// Get the current URL
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Getting current URL for plugin `%s`", pluginName)
		params["currentURL"], err = ctx.wd.CurrentURL()
		if err != nil {
			params["currentURL"] = ""
		}
	}

	// Get JSON object "meta_data" from ctx.source.Config raw JSON object (*json.RawMessage)
	var configMap map[string]interface{}
	var metaData map[string]interface{}
	if err := json.Unmarshal(*ctx.source.Config, &configMap); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling config for plugin `%s`: %v", pluginName, err)
		metaData = nil
	} else {
		if configMap["meta_data"] != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Processing meta_data for plugin `%s`: %v", pluginName, configMap["meta_data"])
			metaData = configMap["meta_data"].(map[string]interface{})
		}
	}
	params["meta_data"] = metaData

	// Safely extract and add "parameters" from Details map
	if step.Details != nil {
		if step.Details["parameters"] != nil {
			parametersRaw := step.Details["parameters"] // Extract parameters from Details
			// transform parametersRaw to a map[string]interface{}
			parametersMap := cmn.ConvertInfToMap(parametersRaw)
			if parametersMap != nil {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "Processing parameters for plugin `%s`: %v", pluginName, step.Details["parameters"])
				for k, v := range parametersMap {
					// Check if v is a string first:
					if str, ok := v.(string); ok {
						str = strings.TrimSpace(str)
						// Check if the value is a request to the KVStore (aka check if the value is between {{ and }})
						if strings.HasPrefix(str, "{{") && strings.HasSuffix(str, "}}") {
							// Extract the key from the value
							key := str[2 : len(str)-2]
							key = strings.TrimSpace(key)
							// Get the value from the KVStore
							v, _, err = cmn.KVStore.Get(key, ctx.GetContextID())
							if err != nil {
								if cmn.KVSErrorIsKeyNotFound(err) {
									cmn.DebugMsg(cmn.DbgLvlDebug2, "Plugin `%s` required key not found in KVStore, did you create it? %v", pluginName, err)
									v = ""
								} else {
									cmn.DebugMsg(cmn.DbgLvlError, "getting value from KVStore for plugin `%s`: %v", pluginName, err)
								}
							} else {
								cmn.DebugMsg(cmn.DbgLvlDebug5, "Value from KVStore for plugin `%s` and key '%s': %v", pluginName, k, v)
							}
						} else if strings.HasPrefix(str, "${") && strings.HasSuffix(str, "}") {
							// We need to interpolate the value (it's an ENV variable)
							key := str[2 : len(str)-1]
							key = strings.TrimSpace(key)
							// Get the value of the ENV variable
							v = os.Getenv(key)
							if v == "" {
								cmn.DebugMsg(cmn.DbgLvlError, "ENV variable for plugin `%s` '%s' not found", pluginName, key)
							} else {
								cmn.DebugMsg(cmn.DbgLvlDebug5, "Value from ENV for plugin `%s` and key '%s': %v", pluginName, k, v)
							}
						}
					}
					if k != "" {
						params[k] = v
					}
				}
			}
		}
	}

	// Execute the plugin
	var value interface{}
	value, err = plugin.Execute(&ctx.wd, ctx.db, ctx.config.Plugins.PluginsTimeout, params)
	if err != nil {
		return fmt.Errorf("error executing plugin `%s`: %v", pluginName, err)
	}

	// Validate the plugin result
	switch v := value.(type) {
	case map[string]interface{}:
		// Serialize map to JSON and assign to *data
		jsonResult, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshalling plugin `%s` output to JSON: %v", pluginName, err)
		}
		if jsonResult != nil {
			*data = jsonResult
		}

	case string:
		// Validate if the string is JSON
		if !json.Valid([]byte(v)) {
			return fmt.Errorf("plugin `%s` returned an invalid JSON string", pluginName)
		}
		v = strings.TrimSpace(v)
		if v != "" {
			*data = []byte(v)
		}

	default:
		return fmt.Errorf("plugin `%s` returned an unsupported type: %T", pluginName, v)
	}

	//cmn.DebugMsg(cmn.DbgLvlDebug3, "Received data from custom JS plugin: %s", string(*data))
	return nil
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
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response from %s: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	*data = body

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
	const dis1 = cmn.DisableStr
	if step.Details["ssl_mode"] == nil {
		protocol = cmn.HTTPStr
		sslMode = dis1
	} else {
		sslMode = strings.ToLower(strings.TrimSpace(step.Details["ssl_mode"].(string)))
		if sslMode == dis1 || sslMode == "disabled" {
			protocol = cmn.HTTPStr
		} else {
			protocol = cmn.HTTPSStr
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
