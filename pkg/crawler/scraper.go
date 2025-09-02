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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/evanw/esbuild/pkg/api"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"

	"golang.org/x/net/html"
)

// ApplyRule applies the provided scraping rule to the provided web page.
func ApplyRule(ctx *ProcessContext, rule *rs.ScrapingRule, webPage *vdi.WebDriver) (map[string]interface{}, error) {

	_ = vdi.Refresh(ctx)

	cmn.DebugMsg(cmn.DbgLvlDebug, "Applying scraping rule: %v", rule.RuleName)
	extractedData := make(map[string]interface{})

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

					case map[string]interface{}:
						// Directly append map values (as sub-documents)
						// Safely convert the map to JSON and store it directly in extractedData
						jsonStr, err := json.Marshal(v)
						if err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, "Error marshalling map to JSON: %v", err)
							errContainer = append(errContainer, err)
						}
						allExtracted = append(allExtracted, string(jsonStr))

					case []interface{}:
						// Append all elements of the array
						for _, item := range v {
							switch item := item.(type) {
							case string:
								allExtracted = append(allExtracted, item)
							case map[string]interface{}:
								// Safely convert the map to JSON and store it directly in extractedData
								jsonStr, err := json.Marshal(item)
								if err != nil {
									cmn.DebugMsg(cmn.DbgLvlError, "Error marshalling map to JSON: %v", err)
									errContainer = append(errContainer, err)
								}
								allExtracted = append(allExtracted, string(jsonStr))
							default:
								// Log unexpected types and skip them
								cmn.DebugMsg(cmn.DbgLvlWarn, "Unexpected type in extracted content: %T", item)
								errContainer = append(errContainer, errors.New("unexpected type in extracted content"))
							}
						}

					default:
						// Log unexpected types and skip them
						cmn.DebugMsg(cmn.DbgLvlWarn, "Unexpected type in extracted content: %T", v)
						errContainer = append(errContainer, errors.New("unexpected type in extracted content"))
					}

					// Break early if not extracting all occurrences or using a plugin_call selector
					if !getAllOccurrences || strings.ToLower(strings.TrimSpace(selectors[i].SelectorType)) == strPluginCall {
						break
					}
				}
				break // If we found data, break the selectors loop and return the data!
			} else if rule.Elements[e].Critical {
				ErrorState = true
				ErrorMsg = "element not found, with " + errCriticalError + " flag set"
				cmn.DebugMsg(cmn.DbgLvlError, "element not found "+errCriticalError+": `%v`", selectors[i].Selector)
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
	endTime := time.Now()
	// Log the time taken to extract the data for this element
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Time taken to execute rule '%s': %v", rule.RuleName, endTime.Sub(startTime))

	// Optional: Extract JavaScript files if required
	if rule.JsFiles {
		jsFiles := extractJSFiles(webPage)
		extractedData["js_files"] = jsFiles
	}

	// Log the full scraped content for debugging purposes
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Full scraped content (at ApplyRule level): %v", extractedData)

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

// extractJSFiles extracts the JavaScript files from the current page.
func extractJSFiles(wd *vdi.WebDriver) []CollectedScript {
	var jsFiles []CollectedScript

	const script = `
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
			//nolint:gosec // Disabling G115: integer overflow conversion int -> uint64 as it won't happen here
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
func extractContent(ctx *ProcessContext, wd *vdi.WebDriver, selector rs.Selector, all bool) []interface{} {
	var results []interface{}
	var elements []vdi.WebElement
	var err error
	sType := strings.ToLower(strings.TrimSpace(selector.SelectorType))

	// Find the elements using the provided selector directly in the VDI's browser
	if (sType != strPluginCall) && (sType != strRegEx) && (sType != strXPath) {
		if all {
			elements, err = FindElementsByType(ctx, wd, selector)
		} else {
			element, err := FindElementByType(ctx, wd, selector)
			if err == nil {
				elements = append(elements, element)
			}
		}
	}

	// Fallback mechanism if there are no elements found, we had errors or the selector type is not supported by FindElementsByType
	if len(elements) == 0 || err != nil ||
		selector.SelectorType == strPluginCall || selector.SelectorType == strRegEx {

		// Let's use fallback mechanism to try to extract the data
		htmlContent, _ := (*wd).PageSource()
		doc, err2 := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err2 != nil {
			// Fallback failed
			cmn.DebugMsg(cmn.DbgLvlError, "Error finding elements: %v, and fallback mechanism failed too: %v", err, err2)
			return results
		}

		rval := []string{}
		switch sType {
		case "css":
			rval = fallbackExtractByCSS(ctx, doc, selector, all)
		case strXPath: // XPath is not supported by goquery, so we use htmlquery
			rval = fallbackExtractByXPath(ctx, doc, selector, all)
		case strRegEx:
			rval = fallbackExtractByRegex(htmlContent, selector.Selector, all)
		case strPluginCall:
			results = extractByPlugin(ctx, wd, selector.Selector)
		}
		for _, s := range rval {
			results = append(results, s)
		}

	} else {
		// All good, let's extract the data from the found elements
		for i := 0; i < len(elements); i++ {
			if selector.Extract != (rs.ItemToExtract{}) {
				// Extract the data using the provided regex
				for _, s := range extractDataFromElement(ctx, elements[i], selector) {
					results = append(results, s)
				}
			} else {
				// Extract the innerText from the element
				text, _ := elements[i].Text()
				results = append(results, text)
			}
		}
	}

	// Let's check results before we return it
	if len(results) == 0 {
		if cmn.GetDebugLevel() <= cmn.DbgLvlDebug3 {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "No results found for selector: '%s'", selector.Selector)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "Failed to find element: '%s' %v", selector.Selector, err)
		}
	} else {
		tstResults := cmn.ConvertSliceInfToString(results)
		if cmn.GetDebugLevel() <= cmn.DbgLvlDebug3 {
			if len(tstResults) > 128 {
				tstResults = tstResults[:128] + "..."
			}
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Found element: '%s' %s", selector.Selector, tstResults)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "Found element: '%s' %v", selector.Selector, tstResults)
		}
	}
	return results
}

func extractDataFromElement(_ *ProcessContext, item interface{}, selector rs.Selector) []string {
	// Check if item is a WebElement
	tmp1, ok := item.(vdi.WebElement)
	var tmp2 *goquery.Selection
	if !ok {
		tmp1 = nil
		// Check if item is a *goquery.Selection
		tmp2, ok = item.(*goquery.Selection)
		if !ok {
			return []string{}
		}
	}

	var results []string
	var err error

	eEpType := strings.ToLower(strings.TrimSpace(selector.Extract.Type))

	var data string
	pattern := selector.Extract.Pattern
	switch eEpType {
	case strText1, strText2, strText3, strText4:
		if tmp1 != nil {
			data, err = tmp1.Text()
		} else {
			data = tmp2.Text()
		}
	case "attribute":
		if tmp1 != nil {
			data, err = tmp1.GetAttribute(selector.Extract.Pattern)
		} else {
			var exists bool
			data, exists = tmp2.Attr(selector.Extract.Pattern)
			if !exists {
				data = ""
				err = errors.New("Attribute not found")
			}
		}
		pattern = ""
	}
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error extracting data from element: %v", err)
		return results
	}

	if pattern != "" && pattern != ".*" {
		// Extract the data using the provided regex
		re := regexp.MustCompile(pattern)
		if allMatches := re.FindAllString(data, -1); len(allMatches) > 0 {
			results = append(results, allMatches...)
		}
	} else {
		results = append(results, data)
	}

	return results
}

// extractByCSS extracts the content from the provided document using the provided CSS selector.
func fallbackExtractByCSS(ctx *ProcessContext, doc *goquery.Document, selector rs.Selector, all bool) []string {
	var results []string
	var elements []*goquery.Selection

	if all {
		doc.Find(selector.Selector).Each(func(_ int, s *goquery.Selection) {
			elements = append(elements, s)
		})
	} else {
		if selection := doc.Find(selector.Selector).First(); selection.Length() > 0 {
			elements = append(elements, selection)
		}
	}

	// Process the matched elements
	for _, e := range elements {
		matchL2 := false
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue, exists := e.Attr(strings.TrimSpace(selector.Attribute.Name))
			if !exists {
				continue
			}

			matchValue := strings.TrimSpace(selector.Attribute.Value)
			if matchValue != "" && matchValue != "*" && matchValue != ".*" {
				// Use regex for matching
				re, err := regexp.Compile(matchValue)
				if err == nil && re.MatchString(attrValue) {
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
			results = append(results, extractDataFromElement(ctx, e, selector)...)
			if !all {
				break // Stop after first match if not processing all
			}
		}
	}

	return results
}

// fallbackExtractByXPath extracts the content from the provided document using the provided XPath selector.
func fallbackExtractByXPath(ctx *ProcessContext, doc *goquery.Document, selector rs.Selector, all bool) []string {
	var results []string
	items, err := htmlquery.QueryAll(doc.Nodes[0], selector.Selector)
	if err != nil {
		// handle error
		return results
	}

	// Process the matched elements
	for _, item := range items {
		matchL2 := false

		// Check for attribute match if specified
		if strings.TrimSpace(selector.Attribute.Name) != "" {
			attrValue := htmlquery.SelectAttr(item, strings.TrimSpace(selector.Attribute.Name))
			matchValue := strings.TrimSpace(selector.Attribute.Value)
			if matchValue != "" && matchValue != "*" && matchValue != ".*" {
				// Use regex for matching
				re, err := regexp.Compile(matchValue)
				if err == nil && re.MatchString(attrValue) {
					matchL2 = true
				}
			} else {
				matchL2 = true
			}
		} else {
			matchL2 = true
		}

		matchL3 := false
		// Check if the element's text or value matches the selector's value
		if matchL2 && strings.TrimSpace(selector.Value) != "" {
			if matchValue(ctx, item, selector) {
				matchL3 = true
			}
		} else {
			if matchL2 {
				matchL3 = true
			}
		}

		if matchL3 {
			results = append(results, htmlquery.InnerText(item))
			if !all {
				break // Stop after first match if not processing all
			}
		}
	}

	return results
}

func fallbackExtractByRegex(content string, pattern string, all bool) []string {
	re := regexp.MustCompile(pattern)

	if all {
		// Find all matches
		matches := re.FindAllStringSubmatch(content, -1)
		var results []string
		for _, match := range matches {
			if len(match) > 1 {
				// Capture group found
				results = append(results, match[1])
			} else {
				// No capture group, use the whole match
				results = append(results, match[0])
			}
		}
		return results
	}

	// Find the first match
	if match := re.FindStringSubmatch(content); match != nil {
		if len(match) > 1 {
			// Return the capture group if present
			return []string{match[1]}
		}
		// Otherwise, return the whole match
		return []string{match[0]}
	}

	return []string{}
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
	default:
		cmn.DebugMsg(cmn.DbgLvlError, "Unknown post-processing step type: %v", stepType)
	}
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
/*
	if err = vm.Set("jsonDataString", string(jsonData)); err != nil {
		errMsg := fmt.Sprintf("Error setting jsonDataString in JS VM: %v", err)
		return errors.New(errMsg)
	}
*/
func processCustomJS(ctx *ProcessContext, step *rs.PostProcessingStep, data *[]byte) error {
	var err error

	// Convert the jsonData byte slice to a map
	jsonData := *data
	var jsonDataMap map[string]interface{}
	if err = json.Unmarshal(jsonData, &jsonDataMap); err != nil {
		return fmt.Errorf("error unmarshalling jsonData: %v", err)
	}

	// Prepare script parameters
	params := make(map[string]interface{})
	params["json_data"] = jsonDataMap

	// Check if we have a valid webdriver
	params["currentURL"] = ""
	if ctx.wd != nil {
		// Get the current URL
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Getting current URL for custom JS")
		params["currentURL"], err = ctx.wd.CurrentURL()
		if err != nil {
			params["currentURL"] = ""
		}
	}

	// Get JSON object "meta_data" from ctx.source.Config raw JSON object (*json.RawMessage)
	var configMap map[string]interface{}
	var metaData map[string]interface{}
	if err := json.Unmarshal(*ctx.source.Config, &configMap); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling config: %v", err)
		metaData = nil
	} else {
		if configMap["meta_data"] != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Processing custom JS with meta_data: %v", configMap["meta_data"])
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
				cmn.DebugMsg(cmn.DbgLvlDebug3, "Processing custom JS with parameters: %v", step.Details["parameters"])
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
								cmn.DebugMsg(cmn.DbgLvlError, "Error getting value from KVStore: %v", err)
								v = ""
							} else {
								cmn.DebugMsg(cmn.DbgLvlDebug5, "Value from KVStore for '%s': %v", k, v)
							}
						} else if strings.HasPrefix(str, "${") && strings.HasSuffix(str, "}") {
							// We need to interpolate the value (it's an ENV variable)
							key := str[2 : len(str)-1]
							key = strings.TrimSpace(key)
							// Get the value of the ENV variable
							v = os.Getenv(key)
							if v == "" {
								cmn.DebugMsg(cmn.DbgLvlError, "ENV variable '%s' not found", key)
							} else {
								cmn.DebugMsg(cmn.DbgLvlDebug5, "Value from ENV for '%s': %v", k, v)
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
		return fmt.Errorf("plugin %s not found", pluginName)
	}

	// Execute the plugin
	var value interface{}
	value, err = plugin.Execute(&ctx.wd, ctx.db, ctx.config.Plugins.PluginsTimeout, params)
	if err != nil {
		return fmt.Errorf("error executing JS plugin: %v", err)
	}

	// Validate the plugin result
	switch v := value.(type) {
	case map[string]interface{}:
		// Serialize map to JSON and assign to *data
		jsonResult, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("error marshalling plugin output to JSON: %v", err)
		}
		if jsonResult != nil {
			*data = jsonResult
		}

	case string:
		// Validate if the string is JSON
		if !json.Valid([]byte(v)) {
			return fmt.Errorf("plugin returned an invalid JSON string")
		}
		v = strings.TrimSpace(v)
		if v != "" {
			*data = []byte(v)
		}

	default:
		return fmt.Errorf("plugin returned an unsupported type: %T", v)
	}

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
