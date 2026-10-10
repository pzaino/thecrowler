// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"errors"
	"fmt"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// PluginAction executes plugins
type PluginAction struct{}

// Name returns the name of the action
func (p *PluginAction) Name() string {
	return "PluginExecution"
}

// Execute runs a plugin using the CROWler plugin system
func (p *PluginAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	// Extract the Plugin library pointer from the config
	plugins, ok := config["plugins_register"].(*plg.JSPluginRegister)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'pluginRegister' in config section"
		return rval, errors.New("missing 'pluginRegister' in config")
	}

	// Recover previous step response
	if _, err := getInput(params); err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Canonical input context: $response is the previous step's payload.
	ictx := NewInputContext(params)

	// Extract plugin's names from params
	plgName, ok := params["plugin_name"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'plugin_name' in parameters section"
		return rval, errors.New("missing 'plugin' parameter")
	}
	// Check if plgName needs to be resolved
	resolvedName, err := ResolveString(ictx, plgName)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	plgName = strings.TrimSpace(resolvedName)
	if plgName == "" {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty plugin name"
		return rval, fmt.Errorf("empty plugin name")
	}

	// Retrieve the plugin
	plg, exists := plugins.GetPlugin(plgName)
	if !exists {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("plugin '%s' not found", cmn.SafeEscapeJSONString(plgName))
		return rval, fmt.Errorf("plugin '%s' not found", cmn.SafeEscapeJSONString(plgName))
	}

	// Prepare the plugin parameters
	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		dbHandler = nil
	}
	wd, ok := config["vdi_hook"].(*vdi.WebDriver)
	if !ok {
		wd = nil
	}
	plgParams := make(map[string]interface{})
	// Check if we have an event field
	// Add event from config if available
	if event, exists := config[StrEvent]; exists {
		plgParams[StrEvent] = event
	} else {
		if event, exists := params[StrEvent]; exists {
			plgParams[StrEvent] = event
		} else {
			plgParams[StrEvent] = nil
		}
	}
	// Add meta_data from config if available
	if meta, exists := config["meta_data"]; exists {
		plgParams["meta_data"] = meta
	} else {
		if meta, exists := params["meta_data"]; exists {
			plgParams["meta_data"] = meta
		} else {
			plgParams["meta_data"] = nil
		}
	}
	// Collect custom params
	for k, v := range params {
		if k != "plugin_name" &&
			k != StrEvent &&
			k != "meta_data" &&
			k != "config" &&
			k != "vdi_hook" &&
			k != "db_handler" {
			// Resolve references against the canonical input context.
			// Non-string scalars pass through untouched.
			switch tv := v.(type) {
			case string:
				resolved, err := ResolveString(ictx, tv)
				if err != nil {
					rval[StrStatus] = StatusError
					rval[StrMessage] = err.Error()
					return rval, err
				}
				plgParams[k] = resolved
			case map[string]interface{}:
				resolved, err := ResolveValue(ictx, tv)
				if err != nil {
					rval[StrStatus] = StatusError
					rval[StrMessage] = err.Error()
					return rval, err
				}
				plgParams[k] = resolved
			default:
				plgParams[k] = v
			}
		}
	}
	// Check if params have a response field
	if params[StrRequest] != nil {
		plgParams["json_data"] = params[StrRequest]
	}

	// log plgParams
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Plugin plgParams: %v", plgParams)

	// Execute the plugin
	pRval, err := plg.Execute(wd, &dbHandler, 150, plgParams)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, fmt.Errorf("executing plugin '%s': %v", plgName, err)
	}

	// Parse the plugin response
	pRvalStr := string(cmn.ConvertMapToJSON(pRval))
	if pRvalStr == "" {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty plugin response"
		return rval, fmt.Errorf("empty plugin response")
	}

	// Handle plugin response (e.g., storing or logging results)
	var results []map[string]interface{}
	results = append(results, map[string]interface{}{
		"plugin_name": plgName,
		"response":    pRval,
	})

	// Return the aggregated plugin execution results
	rval[StrResponse] = results
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "plugin executed successfully"

	return rval, nil
}
