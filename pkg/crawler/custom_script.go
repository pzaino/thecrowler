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

package crawler

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

// processCustomJS executes a configured JavaScript plugin for scraper post-processing.
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
