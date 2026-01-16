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
	"os"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
)

// ProcessDataSource executes non-web crawling (e.g., APIs, files, DBs).
// It runs the Action, Detection, and Scraping rules in sequence,
// mirroring the same logic order used for web crawling.
func ProcessDataSource(args *Pars) {
	var err error

	processCtx := NewProcessContext(args)
	if processCtx == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to create ProcessContext for data source")
		UpdateSourceState(args.DB, args.Src.URL, errors.New("failed to create a new ProcessContext"))
		return
	}

	processCtx.Status.StartTime = time.Now()
	processCtx.Status.PipelineRunning.Store(1)

	// Combine configuration
	if processCtx.source.Config != nil {
		processCtx.config, err = cfg.CombineConfig(processCtx.config, *processCtx.source.Config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "combining data source configuration: %v", err)
		}
	}

	// Load source-specific configuration
	if err := processCtx.LoadSourceConfiguration(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading source configuration: %v", err)
	}

	// Retrieve basic network info if available
	processCtx.GetNetInfo(args.Src.URL)
	_, _ = processCtx.IndexNetInfo(1)

	// Generate a unique batch UID
	batchUID := cmn.GenerateUID()

	// The full document to be populated by the rules
	DataDoc := []byte("{}")

	// ======================
	// 1️⃣ ACTION RULES
	// ======================
	cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessDataSource] Executing Action rules for: %s", args.Src.URL)
	err = processCCActionRules(processCtx, args.Src.URL, &DataDoc)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "processing data source action rules: %v", err)
	}

	// ======================
	// 2️⃣ DETECTION RULES
	// ======================
	//cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessDataSource] Executing Detection rules for: %s", args.Src.URL)
	//processDataSourceDetectionRules(processCtx, args.Src.URL)

	// ======================
	// 3️⃣ SCRAPING RULES
	// ======================
	//cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessDataSource] Executing Scraping rules for: %s", args.Src.URL)
	//processDataSourceScrapingRules(processCtx, args.Src.URL)

	// Finalize
	processCtx.Status.EndTime = time.Now()
	processCtx.Status.PipelineRunning.Store(2)
	UpdateSourceState(args.DB, args.Src.URL, nil)

	// Emit event if configured
	if processCtx.config.Crawler.CreateEventWhenDone {

		evt := cdb.Event{
			Action:    "new",
			Type:      "started_batch_crawling",
			SourceID:  0,
			Severity:  config.Crawler.SourcePriority,
			ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
			Details: map[string]interface{}{
				"uid":                batchUID,
				"node":               cmn.GetMicroServiceName(),
				"time":               time.Now(),
				"initial_batch_size": 1,
			},
		}

		if _, err := cdb.CreateEventWithRetries(processCtx.db, evt); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to create start-of-batch event: %v", err)
		}
	}

	cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessDataSource] Completed processing for: %s", args.Src.URL)
}

// processCCActionRules processes action rules that use plugin_call selectors only.
// It follows the same environment and KVStore lifecycle as scraping_rules.go.
func processCCActionRules(ctx *ProcessContext, url string, dataDoc *[]byte) error {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[ProcessCustomActionRules] Searching and processing Action rules for: %s", url)

	var errList []error

	// --- Step 1: Rule Groups by URL ---
	rgl, err := ctx.re.GetAllRulesGroupByURL(url)
	if err == nil && len(rgl) > 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessCustomActionRules] Found %d rule groups for URL: %s", len(rgl), url)

		for _, rg := range rgl {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[ProcessCustomActionRules] Executing RuleGroup: %s", rg.GroupName)

			// Setup the environment
			rg.SetEnv(ctx.GetContextID())

			// Execute each ActionRule in the group
			for _, rule := range rg.GetActionRules() {
				var data []byte
				err = executeCCActionRule(ctx, &rule, &data)
				if err != nil {
					errList = append(errList, fmt.Errorf("executing action rule '%s' from group '%s': %v", rule.RuleName, rg.GroupName, err))
				}

				// If data is not empty, merge it to data_doc as JSON and using the top-level keys
				if len(data) > 0 {
					var dataMap map[string]interface{}
					if err := json.Unmarshal(data, &dataMap); err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error unmarshalling data from rule '%s': %v", rule.RuleName, err)
					} else {
						var dataDocMap map[string]interface{}
						if err := json.Unmarshal(*dataDoc, &dataDocMap); err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error unmarshalling data_doc: %v", err)
						} else {
							// Merge dataMap into dataDocMap
							for k, v := range dataMap {
								dataDocMap[k] = v
							}
							// Marshal back to JSON
							mergedData, err := json.Marshal(dataDocMap)
							if err != nil {
								cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error marshalling merged data: %v", err)
							} else {
								*dataDoc = mergedData
							}
						}
					}
				}
				// erase data slice
				data = nil
			}

			// Cleanup non-persistent KV variables after each group
			cmn.KVStore.DeleteNonPersistentByCID(ctx.GetContextID())
		}
	} else if err != nil {
		errList = append(errList, fmt.Errorf("getting rule groups by URL: %v", err))
	}

	// --- Step 2: Rulesets by URL ---
	rsl, err := ctx.re.GetAllRulesetByURL(url)
	if err == nil && len(rsl) > 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[ProcessCustomActionRules] Found %d rulesets for URL: %s", len(rsl), url)

		for _, rs := range rsl {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[ProcessCustomActionRules] Executing Ruleset: %s", rs.Name)

			// Setup environment
			rs.SetEnv(ctx.GetContextID())

			// Execute all enabled rules in this ruleset
			for _, rule := range rs.GetAllEnabledActionRules(ctx.GetContextID(), true) {
				var data []byte
				err = executeCCActionRule(ctx, &rule, &data)
				if err != nil {
					errList = append(errList, fmt.Errorf("executing action rule '%s' from ruleset '%s': %v", rule.RuleName, rs.Name, err))
				}

				// If data is not empty, merge it to data_doc as JSON and using the top-level keys
				if len(data) > 0 {
					var dataMap map[string]interface{}
					if err := json.Unmarshal(data, &dataMap); err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error unmarshalling data from rule '%s': %v", rule.RuleName, err)
					} else {
						var dataDocMap map[string]interface{}
						if err := json.Unmarshal(*dataDoc, &dataDocMap); err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error unmarshalling data_doc: %v", err)
						} else {
							// Merge dataMap into dataDocMap
							for k, v := range dataMap {
								dataDocMap[k] = v
							}
							// Marshal back to JSON
							mergedData, err := json.Marshal(dataDocMap)
							if err != nil {
								cmn.DebugMsg(cmn.DbgLvlError, "[ProcessCustomActionRules] Error marshalling merged data: %v", err)
							} else {
								*dataDoc = mergedData
							}
						}
					}
				}
				// erase data slice
				data = nil
			}

			// Cleanup environment after the ruleset
			cmn.KVStore.DeleteNonPersistentByCID(ctx.GetContextID())
		}
	} else if err != nil {
		errList = append(errList, fmt.Errorf("getting rulesets by URL: %v", err))
	}

	// --- Step 3: Aggregate errors ---
	if len(errList) > 0 {
		var joined string
		for _, e := range errList {
			joined += e.Error() + "\n"
		}
		return fmt.Errorf("[ProcessCustomActionRules] encountered errors:\n%s", joined)
	}

	return nil
}

// executeCCActionRule executes a single CustomCrawler Action Rule.
func executeCCActionRule(ctx *ProcessContext, r *rules.ActionRule, data *[]byte) error {
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[executeCCActionRule] Executing CustomCrawler Action Rule: %s", r.RuleName)

	// Execute Wait condition first
	if len(r.WaitConditions) != 0 {
		for _, wc := range r.WaitConditions {
			// Execute the wait condition
			err := waitForCCCondition(ctx, wc)
			if err != nil {
				return err
			}
		}
	}

	var errList []error
	switch strings.ToLower(strings.TrimSpace(r.ActionType)) {
	case "custom":
		{
			if err := executeCCPluginActionRule(ctx, r, data); err != nil {
				errList = append(errList, fmt.Errorf("executing action rule '%s': %v", r.RuleName, err))
			}
		}
	default:
		{
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[ProcessCustomActionRules] Skipping rule '%s' (unsupported selector_type: %s)", r.RuleName, r.ActionType)
		}
	}

	if len(errList) > 0 {
		var joined string
		for _, e := range errList {
			joined += e.Error() + "\n"
		}
		return fmt.Errorf("[executeCCActionRule] encountered errors:\n%s", joined)
	}

	cmn.DebugMsg(cmn.DbgLvlDebug2, "[executeCCActionRule] Completed CustomCrawler Action Rule: %s", r.RuleName)
	return nil
}

// waitForCCCondition waits for a condition to be met before continuing.
func waitForCCCondition(_ *ProcessContext, r rules.WaitCondition) error {
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
		return nil
	default:
		return fmt.Errorf("wait condition not supported: %s", r.ConditionType)
	}
}

// executeCCPluginActionRule executes a single plugin_call Action Rule.
// It runs the plugin specified in the rule, passing rule parameters to it.
func executeCCPluginActionRule(ctx *ProcessContext, r *rules.ActionRule, data *[]byte) error {
	var err error

	for _, selector := range r.Selectors {
		if selector.SelectorType == "plugin_call" {
			// Prepare parameters
			params := make(map[string]interface{})

			// Safely extract and add "parameters" from Details map
			if r.Details != nil {
				if r.Details["parameters"] != nil {
					parametersRaw := r.Details["parameters"] // Extract parameters from Details
					// transform parametersRaw to a map[string]interface{}
					parametersMap := cmn.ConvertInfToMap(parametersRaw)
					if parametersMap != nil {
						cmn.DebugMsg(cmn.DbgLvlDebug3, "Processing custom JS with parameters: %v", r.Details["parameters"])
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
											cmn.DebugMsg(cmn.DbgLvlDebug2, "JS Plugin required key not found in KVStore, did you create it? %v", err)
											v = ""
										} else {
											cmn.DebugMsg(cmn.DbgLvlError, "getting value from KVStore for JS Plugin: %v", err)
										}
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

			// Call processCCCustomJS
			err = processCCCustomJS(ctx, selector.Selector, params, data)
		}
	}

	return err
}

func processCCCustomJSStep(ctx *ProcessContext, step *rules.PostProcessingStep, data *[]byte) error {
	var err error
	params := make(map[string]interface{})

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
								if cmn.KVSErrorIsKeyNotFound(err) {
									cmn.DebugMsg(cmn.DbgLvlDebug2, "JS Plugin required key not found in KVStore, did you create it? %v", err)
									v = ""
								} else {
									cmn.DebugMsg(cmn.DbgLvlError, "getting value from KVStore for JS Plugin: %v", err)
								}
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

	return processCCCustomJS(ctx, pluginName, params, data)
}

func processCCCustomJS(ctx *ProcessContext, pluginName string, params map[string]interface{}, data *[]byte) error {
	var err error

	// Convert the jsonData byte slice to a map
	jsonData := *data
	var jsonDataMap map[string]interface{}
	if err = json.Unmarshal(jsonData, &jsonDataMap); err != nil {
		return fmt.Errorf("error unmarshalling jsonData: %v", err)
	}

	// Prepare script parameters
	params["json_data"] = jsonDataMap

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

	plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
	if !exists {
		return fmt.Errorf("plugin '%s' not found", pluginName)
	}

	// Execute the plugin
	var value interface{}
	value, err = plugin.Execute(&ctx.wd, ctx.db, ctx.config.Plugins.PluginsTimeout, params)
	if err != nil {
		return fmt.Errorf("error executing JS plugin '%s': %v", pluginName, err)
	}

	// Validate the plugin result
	switch v := value.(type) {
	case map[string]interface{}:
		// Serialize map to JSON and assign to *data
		jsonResult, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("error marshalling plugin '%s' output to JSON: %v", pluginName, err)
		}
		if jsonResult != nil {
			*data = jsonResult
		}

	case string:
		// Validate if the string is JSON
		if !json.Valid([]byte(v)) {
			return fmt.Errorf("plugin '%s' returned an invalid JSON string", pluginName)
		}
		v = strings.TrimSpace(v)
		if v != "" {
			*data = []byte(v)
		}

	default:
		return fmt.Errorf("plugin '%s' returned an unsupported type: %T", pluginName, v)
	}

	//cmn.DebugMsg(cmn.DbgLvlDebug3, "Received data from custom JS plugin: %s", string(*data))
	return nil
}
