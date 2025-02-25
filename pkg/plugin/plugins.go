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

// Package plugin provides the plugin functionality for the CROWler.
package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robertkrimen/otto"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"

	"github.com/clbanning/mxj/v2"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	httpMethodGet = "GET"
	vdiPlugin     = "vdi_plugin"
	enginePlugin  = "engine_plugin"
	eventPlugin   = "event_plugin"
	none          = "none"
	all           = "all"

	postgresDBMS = "postgres"
	mysqlDBMS    = "mysql"
	sqliteDBMS   = "sqlite"
)

// NewJSPluginRegister returns a new JSPluginRegister
func NewJSPluginRegister() *JSPluginRegister {
	return &JSPluginRegister{}
}

// Register registers a new JS plugin
func (reg *JSPluginRegister) Register(name string, plugin JSPlugin) {
	// Check if the register is initialized
	if reg.Registry == nil {
		reg.Registry = make(map[string]JSPlugin)
	}

	// Check if the name is empty
	if strings.TrimSpace(name) == "" {
		name = plugin.Name
	}
	name = strings.TrimSpace(name)

	// Register the plugin
	reg.Registry[name] = plugin

	// Add the plugin name to the order list
	reg.Order = append(reg.Order, name)
}

// Remove removes a registered plugin from the registry
func (reg *JSPluginRegister) Remove(name string) {
	// Check if the register is initialized
	if reg.Registry == nil {
		return
	}

	// Check if the name is empty
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}

	// Remove the plugin
	delete(reg.Registry, name)

	// Remove the plugin name from the order list
	for i, n := range reg.Order {
		if n == name {
			reg.Order = append(reg.Order[:i], reg.Order[i+1:]...)
			break
		}
	}
}

// GetPlugin returns a JS plugin
func (reg *JSPluginRegister) GetPlugin(name string) (JSPlugin, bool) {
	plugin, exists := reg.Registry[name]
	return plugin, exists
}

// GetPluginsByEventType returns a list of JS plugins to handle an event type
func (reg *JSPluginRegister) GetPluginsByEventType(eventType string) ([]JSPlugin, bool) {
	plugins := make([]JSPlugin, 0)
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		return plugins, false
	}
	// Browse the registry in order of registration
	for i := 0; i < len(reg.Order); i++ {
		plugin := reg.Registry[reg.Order[i]]
		if plugin.EventType == "" ||
			plugin.EventType == none {
			continue
		}
		if plugin.EventType == eventType || plugin.EventType == all {
			plugins = append(plugins, plugin)
		}
	}
	return plugins, len(plugins) > 0
}

// GetPluginsByAgentName returns a list of JS plugins related to the specified agent name
func (reg *JSPluginRegister) GetPluginsByAgentName(agentName string) ([]JSPlugin, bool) {
	plugins := make([]JSPlugin, 0)
	agentName = strings.ToLower(strings.TrimSpace(agentName))
	if agentName == "" {
		return plugins, false
	}
	for i := 0; i < len(reg.Order); i++ {
		plugin := reg.Registry[reg.Order[i]]
		if plugin.EventType == "" ||
			plugin.EventType == none {
			continue
		}
		if plugin.EventType == agentName ||
			plugin.EventType == all {
			plugins = append(plugins, plugin)
		}
	}
	return plugins, len(plugins) > 0
}

// LoadPluginsFromConfig loads the plugins from the specified configuration.
func (reg *JSPluginRegister) LoadPluginsFromConfig(config *cfg.Config, pType string) *JSPluginRegister {
	for _, plugin := range config.Plugins.Plugins {
		err := LoadPluginsFromConfig(reg, plugin, pType)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to load plugins from configuration: %v", err)
		}
	}
	return reg
}

// LoadPluginsFromConfig loads the plugins from the specified configuration.
func LoadPluginsFromConfig(pluginRegistry *JSPluginRegister,
	config cfg.PluginConfig, pType string) error {
	if len(config.Path) == 0 {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Skipping Plugins loading: empty plugins path")
		return nil
	}

	// Ensure pType is set
	pType = strings.ToLower(strings.TrimSpace(pType))

	// Load the plugins from the specified file
	plugins, err := BulkLoadPlugins(config, pType)
	if err != nil {
		return err
	}

	// Register the plugins
	for _, plugin := range plugins {
		pluginRegistry.Register(plugin.Name, *plugin)
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Registered plugin '%s' of type '%s' for events: '%s'", plugin.Name, plugin.PType, plugin.EventType)
	}

	return nil
}

// BulkLoadPlugins loads the plugins from the specified file and returns a pointer to the created JSPlugin.
func BulkLoadPlugins(config cfg.PluginConfig, pType string) ([]*JSPlugin, error) {
	var pluginsSet []*JSPlugin

	// Ensure pType is set
	pType = strings.ToLower(strings.TrimSpace(pType))

	// Construct the URL to download the plugins from
	if config.Host == "" {
		for _, path := range config.Path {
			plugins, err := LoadPluginFromLocal(path)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to load plugin from %s: %v", path, err)
				continue
			}
			if pType != "" {
				for _, plugin := range plugins {
					if pType == eventPlugin {
						if plugin.EventType != "" || plugin.PType != eventPlugin {
							pluginsSet = append(pluginsSet, plugin)
						}
					} else if pType == vdiPlugin {
						if plugin.PType == vdiPlugin {
							pluginsSet = append(pluginsSet, plugin)
						}
					} else if pType == enginePlugin {
						if plugin.PType != vdiPlugin {
							pluginsSet = append(pluginsSet, plugin)
						}
					}
				}
			} else {
				pluginsSet = append(pluginsSet, plugins...)
			}
		}
		return pluginsSet, nil
	}
	// Plugins are stored remotely
	plugins, err := LoadPluginsFromRemote(config)
	if err != nil {
		return nil, err
	}
	if pType != "" {
		for _, plugin := range plugins {
			if pType == eventPlugin {
				if plugin.EventType != "" || plugin.PType != eventPlugin {
					pluginsSet = append(pluginsSet, plugin)
				}
			} else if pType == vdiPlugin {
				if plugin.PType == vdiPlugin {
					pluginsSet = append(pluginsSet, plugin)
				}
			} else if pType == enginePlugin {
				if plugin.PType != vdiPlugin {
					pluginsSet = append(pluginsSet, plugin)
				}
			}
		}
	} else {
		pluginsSet = append(pluginsSet, plugins...)
	}

	return pluginsSet, nil
}

// LoadPluginsFromRemote loads plugins from a distribution server either on the local net or the
// internet.
// TODO: This function needs improvements, it's not very efficient (a server call for each plugin)
func LoadPluginsFromRemote(config cfg.PluginConfig) ([]*JSPlugin, error) {
	var plugins []*JSPlugin

	// Construct the URL to download the plugins from
	for _, path := range config.Path {
		fileType := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
		if fileType != "js" {
			// Ignore unsupported file types
			continue
		}

		url := fmt.Sprintf("http://%s/%s", config.Host, path)
		pluginBody, err := cmn.FetchRemoteFile(url, config.Timeout, config.SSLMode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch plugin from %s: %v", url, err)
		}

		// Extract plugin name
		pluginName := getPluginName(string(pluginBody), path)

		plugin := NewJSPlugin(string(pluginBody))
		if (*plugin).Name == "" {
			(*plugin).Name = pluginName
		}
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// LoadPluginFromLocal loads the plugin from the specified file and returns a pointer to the created JSPlugin.
func LoadPluginFromLocal(path string) ([]*JSPlugin, error) {
	// Check if path is wild carded
	var files []string
	var err error
	if strings.Contains(path, "*") {
		// Generate the list of files to load
		files, err = filepath.Glob(path)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("no files found")
		}
	} else {
		files = append(files, path)
	}

	// Load the plugins from the specified files list
	var plugins []*JSPlugin
	for _, file := range files {
		// Check if the file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			return nil, fmt.Errorf("file %s does not exist", file)
		}

		// Load the specified file
		pluginBody, err := os.ReadFile(file) //nolint:gosec // We are not using end-user input here
		if err != nil {
			return nil, err
		}

		// Extract plugin name from the first line of the plugin file
		pluginName := getPluginName(string(pluginBody), file)

		// I am assuming that the response body is actually a plugin
		// this may need reviewing later on.
		plugin := NewJSPlugin(string(pluginBody))
		if (*plugin).Name == "" {
			(*plugin).Name = pluginName
		}
		plugins = append(plugins, plugin)
		cmn.DebugMsg(cmn.DbgLvlDebug, "Loaded plugin %s from file %s", pluginName, file)
	}

	return plugins, nil
}

// getPluginName extracts the plugin name from the first line of the plugin file.
func getPluginName(pluginBody, file string) string {
	// Extract the first line of the plugin file
	var line0 string
	for _, line := range strings.Split(pluginBody, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			line0 = line
			break
		}
	}

	// Extract the plugin name from the first line
	pluginName := ""
	if strings.HasPrefix(line0, "//") {
		line0 = strings.TrimSpace(line0[2:])
		if strings.HasPrefix(strings.ToLower(line0), "name:") {
			pluginName = strings.TrimSpace(line0[5:])
		}
	}
	if strings.TrimSpace(pluginName) == "" {
		// Extract the file name without the extension from the path
		pluginName = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		pluginName = strings.TrimSpace(pluginName)
	}
	return pluginName
}

// NewJSPlugin returns a new JS plugin
func NewJSPlugin(script string) *JSPlugin {
	pNameRegEx := "^//\\s*[@]?name\\s*\\:\\s*([^\n]+)"
	pDescRegEx := "^//\\s*[@]?description\\s*\\:?\\s*([^\n]+)"
	pVerRegEx := "^//\\s*[@]?version\\s*\\:?\\s*([^\n]+)"
	pTypeRegEx := "^//\\s*[@]?type\\s*\\:\\s*([^\n]+)"
	pEventTypeRegEx := "^//\\s*[@]?event_type\\s*\\:\\s*([^\n]+)"
	re1 := regexp.MustCompile(pNameRegEx)
	re2 := regexp.MustCompile(pDescRegEx)
	re3 := regexp.MustCompile(pTypeRegEx)
	re4 := regexp.MustCompile(pEventTypeRegEx)
	re5 := regexp.MustCompile(pVerRegEx)
	// Extract the "// @name" comment from the script (usually on the first line)
	pName := ""
	pDesc := ""
	pType := vdiPlugin
	pEventType := ""
	pVersion := ""
	lines := strings.Split(script, "\n")
	for _, line := range lines {
		if re1.MatchString(line) {
			pName = strings.TrimSpace(re1.FindStringSubmatch(line)[1])
		}
		if re2.MatchString(line) {
			pDesc = strings.TrimSpace(re2.FindStringSubmatch(line)[1])
		}
		if re3.MatchString(line) {
			pType = strings.ToLower(strings.TrimSpace(re3.FindStringSubmatch(line)[1]))
		}
		if re4.MatchString(line) {
			pEventType = strings.ToLower(strings.TrimSpace(re4.FindStringSubmatch(line)[1]))
		}
		if re5.MatchString(line) {
			pVersion = strings.TrimSpace(re5.FindStringSubmatch(line)[1])
		}
	}

	return &JSPlugin{
		Name:        pName,
		Description: pDesc,
		Version:     pVersion,
		PType:       pType,
		Script:      script,
		EventType:   pEventType,
	}
}

// Execute executes the JS plugin
func (p *JSPlugin) Execute(wd *vdi.WebDriver, db *cdb.Handler, timeout int, params map[string]interface{}) (map[string]interface{}, error) {
	if p.PType == vdiPlugin {
		return execVDIPlugin(p, timeout, params, wd)
	}
	return execEnginePlugin(p, timeout, params, db)
}

func execVDIPlugin(p *JSPlugin, timeout int, params map[string]interface{}, wd *vdi.WebDriver) (map[string]interface{}, error) {
	// Consts
	const (
		errMsg01 = "Error getting result from JS plugin: %v"
	)

	// Transform params to []interface{}
	paramsArr := make([]interface{}, 0)
	for _, v := range params {
		paramsArr = append(paramsArr, v)
	}

	// Setup a timeout for the script
	err := (*wd).SetAsyncScriptTimeout(time.Duration(timeout) * time.Second)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
	}

	// Run the script wd.ExecuteScript(script, args)
	result, err := (*wd).ExecuteScript(p.Script, paramsArr)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
		return nil, err
	}

	// Get the result
	resultMap := cmn.ConvertInfToMap(result)

	return resultMap, nil
}

func execEnginePlugin(p *JSPlugin, timeout int, params map[string]interface{}, db *cdb.Handler) (map[string]interface{}, error) {
	// Consts
	const (
		errMsg01 = "Error getting result from JS plugin: %v"
	)

	// Create a new VM
	vm := otto.New()
	err := removeJSFunctions(vm)
	if err != nil {
		return nil, err
	}

	// Add CROWler JSAPI to the VM
	err = setCrowlerJSAPI(vm, db)
	if err != nil {
		return nil, err
	}

	// Set the params
	err = vm.Set("params", params)
	if err != nil {
		return nil, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Set params to the VM successfully: %v", params)

	vm.Interrupt = make(chan func(), 1) // Set an interrupt channel

	go func(timeout time.Duration) {
		time.Sleep(timeout * time.Second) // Wait for the timeout
		vm.Interrupt <- func() {
			cmn.DebugMsg(cmn.DbgLvlError, "JavaScript execution timeout")
		}
	}(time.Duration(timeout))

	// Run the script
	rval, err := vm.Run(p.Script)
	if err != nil {
		return nil, err
	}

	// Get the result
	result, err := vm.Get("result")
	if err != nil || !result.IsDefined() {
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
		}
		result = rval
	}
	resultMap, err := result.Export()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
		return nil, err
	}
	resultValue, ok := resultMap.(map[string]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
		return nil, err
	}

	return resultValue, nil
}

// removeJSFunctions removes the JS functions from the VM
func removeJSFunctions(vm *otto.Otto) error {
	// We need to keep the following functions:
	// - "fetch", "WebSocket", "Worker", "SharedWorker"
	// - "setTimeout", "setInterval", "clearTimeout", "clearInterval"
	// Because they are used in the JS plugins that needs to make HTTP requests and access APIs

	// Functions to remove
	functionsToRemove := []string{
		"eval",
		"Function",
		"XMLHttpRequest",
		"requestAnimationFrame",
		"cancelAnimationFrame",
		"requestIdleCallback",
		"cancelIdleCallback",
		"importScripts",
		"Notification",
		"navigator",
		"location",
		"document",
		"window",
		"process",
		"globalThis",
		"global",
		"crypto",
	}

	for _, functionName := range functionsToRemove {
		err := vm.Set(functionName, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// setCrowlerJSAPI sets the CROWler JS API functions
func setCrowlerJSAPI(vm *otto.Otto, db *cdb.Handler) error {
	// Extends Otto JS VM with CROWler JS API functions

	// Common functions

	if err := addJSAPIDebugLevel(vm); err != nil {
		return err
	}
	if err := addJSAPIConsoleLog(vm); err != nil {
		return err
	}

	// Crypto API functions

	if err := addJSAPICrypto(vm); err != nil {
		return err
	}

	// API and Web functions

	if err := addJSHTTPRequest(vm); err != nil {
		return err
	}
	if err := addJSAPIClient(vm); err != nil {
		return err
	}
	if err := addJSAPIFetch(vm); err != nil {
		return err
	}

	// CROWler Events API functions

	if err := addJSAPICreateEvent(vm, db); err != nil {
		return err
	}
	if err := addJSAPIScheduleEvent(vm, db); err != nil {
		return err
	}

	// CROWler DB API functions

	if err := addJSAPIRunQuery(vm, db); err != nil {
		return err
	}
	if err := addJSAPICreateSource(vm, db); err != nil {
		return err
	}
	if err := addJSAPIRemoveSource(vm, db); err != nil {
		return err
	}
	if err := addJSAPIVacuumSource(vm, db); err != nil {
		return err
	}

	// External DBs interaction functions

	if err := addJSAPIExternalDBQuery(vm); err != nil {
		return err
	}

	// Data conversion functions

	if err := addJSAPIJSONToCSV(vm); err != nil {
		return err
	}
	if err := addJSAPICSVToJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIXMLToJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIJSONToXML(vm); err != nil {
		return err
	}

	// Data manipulation (transformation) functions

	if err := addJSAPIFilterJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIMapJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIReduceJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIJoinJSON(vm); err != nil {
		return err
	}
	if err := addJSAPISortJSON(vm); err != nil {
		return err
	}
	if err := addJSAPIPipeJSON(vm); err != nil {
		return err
	}

	return nil
}

// addJSHTTPRequest adds the httpRequest function to the VM
func addJSHTTPRequest(vm *otto.Otto) error {
	// Register the fetch function
	err := vm.Set("httpRequest", func(call otto.FunctionCall) otto.Value {
		url, _ := call.Argument(0).ToString()

		// Make the HTTP request
		resp, err := http.Get(url) //nolint:gosec // We are not using user input here
		if err != nil {
			return otto.Value{}
		}
		defer resp.Body.Close() //nolint:errcheck // We can't check error here it's a defer

		body, _ := io.ReadAll(resp.Body)

		// Return the response as a string
		result, _ := vm.ToValue(string(body))

		return result
	})
	return err
}

// addJSAPIClient adds the apiClient function to the VM
/* Usage in javascript:
	// Make a GET request to the specified URL
   let response = apiClient("GET",
   	"https://api.example.com/v1/resource",
	{ "Authorization header": "Bearer token"},
	{"key": "value"},
	30000);

   console.log(response.status);
   console.log(response.headers);
   console.log(response.body);

	// Make a POST request to the specified URL
	   let response = apiClient("POST",
		"https://api.example.com/v1/resource",

		// Headers
		{
			"Authorization header": " Bearer token",
			"Content-Type": "application/json"
		},

		// Body
		{
			"key": "value"
		},

		// Timeout
		30000
	);
	console.log(response.status);
	console.log(response.headers);
	console.log(response.body);

*/
func addJSAPIClient(vm *otto.Otto) error {
	apiClientObject, _ := vm.Object(`({})`)

	// Define the "post" method
	err := apiClientObject.Set("post", func(call otto.FunctionCall) otto.Value {
		// Get the URL (first argument)
		url, _ := call.Argument(0).ToString()

		// Get the request object (second argument)
		requestArg := call.Argument(1)
		if !requestArg.IsDefined() {
			fmt.Println("Request argument is undefined.")
			return otto.UndefinedValue()
		}

		// Export the request object
		request, err := requestArg.Export()
		if err != nil {
			fmt.Printf("Error exporting request object: %v\n", err)
			return otto.UndefinedValue()
		}

		// Type assert the exported request as a map
		reqMap, ok := request.(map[string]interface{})
		if !ok {
			fmt.Println("Request object is not a valid map.")
			return otto.UndefinedValue()
		}

		// Extract headers, body, and timeout from the request object
		var headers map[string]interface{}
		if h, exists := reqMap["headers"]; exists {
			headers, _ = h.(map[string]interface{}) // Type assert headers
		}

		var body io.Reader
		if b, exists := reqMap["body"]; exists {
			if bodyString, ok := b.(string); ok {
				body = strings.NewReader(bodyString) // Handle as pre-serialized JSON
			} else {
				bodyBytes, err := json.Marshal(b) // Serialize object to JSON
				if err != nil {
					fmt.Printf("Error marshaling body: %v\n", err)
					return otto.UndefinedValue()
				}
				body = bytes.NewReader(bodyBytes)
			}
		}

		var timeoutMs int64 = 30000 // Default timeout
		if t, exists := reqMap["timeout"]; exists {
			if timeoutFloat, ok := t.(float64); ok { // Otto exports numbers as float64
				timeoutMs = int64(timeoutFloat)
			}
		}
		timeout := time.Duration(timeoutMs) * time.Millisecond

		// Set up HTTP client with timeout
		client := &http.Client{Timeout: timeout}

		// Create the HTTP request
		req, err := http.NewRequest("POST", url, body)
		if err != nil {
			fmt.Printf("Error creating request: %v\n", err)
			return otto.UndefinedValue()
		}

		// Add headers to the request
		if len(headers) > 0 {
			for key, value := range headers {
				if headerValue, ok := value.(string); ok {
					req.Header.Set(key, headerValue)
				}
			}
		}

		// output the object for debugging purposes:
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Request object:", req)

		resp, err := client.Do(req)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "making request:", err)
			return otto.UndefinedValue()
		}
		defer resp.Body.Close() //nolint:errcheck // We can't check error here it's a defer

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "reading response body:", err)
			return otto.UndefinedValue()
		}

		respObject, _ := vm.Object(`({})`)
		err = respObject.Set("status", resp.StatusCode)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting status:", err)
		}
		err = respObject.Set("headers", resp.Header)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting headers:", err)
		}
		err = respObject.Set("body", string(respBody))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting body:", err)
		}

		return respObject.Value()
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "setting post method:", err)
	}

	// Define the "get" method
	err = apiClientObject.Set("get", func(call otto.FunctionCall) otto.Value {
		// Get the URL (first argument)
		url, _ := call.Argument(0).ToString()

		// Get the request options object (second argument)
		requestArg := call.Argument(1)
		var headers map[string]string
		var timeoutMs int64 = 30000 // Default timeout

		if requestArg.IsDefined() {
			reqMap, err := requestArg.Export()
			if err == nil {
				// Extract headers
				if h, exists := reqMap.(map[string]interface{})["headers"]; exists {
					headersInterface, _ := h.(map[string]interface{})
					headers = make(map[string]string)
					for k, v := range headersInterface {
						if vStr, ok := v.(string); ok {
							headers[k] = vStr
						}
					}
				}
				// Extract timeout
				if t, exists := reqMap.(map[string]interface{})["timeout"]; exists {
					if timeoutFloat, ok := t.(float64); ok { // Otto exports numbers as float64
						timeoutMs = int64(timeoutFloat)
					}
				}
			}
		}

		// Set up HTTP client with timeout
		timeout := time.Duration(timeoutMs) * time.Millisecond
		client := &http.Client{Timeout: timeout}

		// Create the HTTP request
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error creating GET request:", err)
			return otto.UndefinedValue()
		}

		// Add headers to the request
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		// Execute the request
		resp, err := client.Do(req)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error executing GET request:", err)
			return otto.UndefinedValue()
		}
		defer resp.Body.Close() //nolint:errcheck // We can't check error here it's a defer

		// Read response body
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error reading GET response body:", err)
			return otto.UndefinedValue()
		}

		// Create response object for JavaScript
		respObject, _ := vm.Object(`({})`)
		err = respObject.Set("status", resp.StatusCode)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error setting status in response object:", err)
		}
		err = respObject.Set("headers", resp.Header)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error setting headers in response object:", err)
		}
		err = respObject.Set("body", string(respBody))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error setting body in response object:", err)
		}

		return respObject.Value()
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "setting get method:", err)
	}

	return vm.Set("apiClient", apiClientObject)
}

// addJSAPIFetch adds the fetch function to the VM
/* Usage in javascript:
// Make a GET request to the specified URL
let response = fetch("https://api.example.com/v1/resource");
console.log(response.ok);
console.log(response.status);
console.log(response.statusText);
console.log(response.url);
console.log(response.headers);
response.text().then(text => console.log(text));
response.json().then(json => console.log(json));
*/
func addJSAPIFetch(vm *otto.Otto) error {
	// Implement the fetch function
	err := vm.Set("fetch", func(call otto.FunctionCall) otto.Value {
		// Extract arguments
		url, err := call.Argument(0).ToString()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: converting URL argument to string:", err)
			return otto.UndefinedValue()
		}

		options := call.Argument(1)
		method := httpMethodGet
		headers := make(map[string]string)
		var body io.Reader

		if options.IsObject() {
			optionsObj := options.Object()

			// Extract method
			methodVal, err := optionsObj.Get("method")
			if err == nil && methodVal.IsString() {
				method, _ = methodVal.ToString()
			}

			// Extract headers
			headersVal, err := optionsObj.Get("headers")
			if err == nil && headersVal.IsObject() {
				headersObj := headersVal.Object()
				for _, key := range headersObj.Keys() {
					value, err := headersObj.Get(key)
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: getting header value for key", key, ":", err)
						continue
					}
					valueStr, err := value.ToString()
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Header value for key", key, "is not a string:", err)
						continue
					}
					headers[key] = valueStr
				}
			}

			// Extract body
			bodyVal, err := optionsObj.Get("body")
			if err == nil && bodyVal.IsDefined() {
				bodyStr, err := bodyVal.ToString()
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: converting body to string:", err)
				} else {
					body = strings.NewReader(bodyStr)
				}
			}
		}

		// Create the request
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: creating request:", err)
			return otto.UndefinedValue()
		}

		// Set headers
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		// Make the HTTP request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: making HTTP request:", err)
			return otto.UndefinedValue()
		}
		defer resp.Body.Close() //nolint:errcheck // We can't check error here it's a defer

		// Read the response body
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: reading response body:", err)
			return otto.UndefinedValue()
		}

		// Build the response object
		respObject, err := vm.Object(`({})`)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: creating response object:", err)
			return otto.UndefinedValue()
		}

		err = respObject.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting ok:", err)
		}
		err = respObject.Set("status", resp.StatusCode)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting status:", err)
		}
		err = respObject.Set("statusText", resp.Status)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting statusText:", err)
		}
		err = respObject.Set("url", resp.Request.URL.String())
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting url:", err)
		}

		// Set headers
		headersObj, err := vm.Object(`({})`)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: creating headers object:", err)
		} else {
			for key, values := range resp.Header {
				err = headersObj.Set(key, strings.Join(values, ","))
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting header value for key", key, ":", err)
				}
			}
			err = respObject.Set("headers", headersObj)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting headers:", err)
			}
		}

		responseBody := string(respBody)

		// Implement text() method
		err = respObject.Set("text", func(otto.FunctionCall) otto.Value {
			result, err := vm.ToValue(responseBody)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: converting response body to value:", err)
				return otto.UndefinedValue()
			}
			return result
		})
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting text method:", err)
		}

		// Implement json() method
		err = respObject.Set("json", func(otto.FunctionCall) otto.Value {
			var jsonData interface{}
			err := json.Unmarshal(respBody, &jsonData)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: parsing JSON response:", err)
				return otto.UndefinedValue()
			}
			result, err := vm.ToValue(jsonData)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: converting JSON data to value:", err)
				return otto.UndefinedValue()
			}
			return result
		})
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: setting json method:", err)
		}

		return respObject.Value()
	})

	return err
}

func addJSAPIConsoleLog(vm *otto.Otto) error {
	// Implement the console object with log method
	console, err := vm.Object(`console = {}`)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error creating console object:", err)
		return err
	}

	// Implement console.log
	err = console.Set("log", func(call otto.FunctionCall) otto.Value {
		message := formatConsoleLog(extractArguments(call))
		cmn.DebugMsg(cmn.DbgLvlInfo, message)
		return otto.UndefinedValue()
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error setting console.log function:", err)
		return err
	}

	// Implement console.error
	err = console.Set("error", func(call otto.FunctionCall) otto.Value {
		message := formatConsoleLog(extractArguments(call))
		cmn.DebugMsg(cmn.DbgLvlError, message)
		return otto.UndefinedValue()
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error setting console.error function:", err)
		return err
	}

	// Optionally implement console.warn
	err = console.Set("warn", func(call otto.FunctionCall) otto.Value {
		message := formatConsoleLog(extractArguments(call))
		cmn.DebugMsg(cmn.DbgLvlWarn, message)
		return otto.UndefinedValue()
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error setting console.warn function:", err)
		return err
	}

	return nil
}

// NormalizeValues normalizes the values to be exported to JavaScript
func NormalizeValues(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return []interface{}{} // Ensure nil is treated as an empty slice

	case []interface{}:
		// Recursively normalize slice elements
		for i, item := range v {
			v[i] = NormalizeValues(item)
		}
		return v

	case []int, []int64, []float64:
		// Convert any number slice into a []interface{} slice
		var ifaceSlice []interface{}
		s := reflect.ValueOf(value)
		for i := 0; i < s.Len(); i++ {
			ifaceSlice = append(ifaceSlice, NormalizeValues(s.Index(i).Interface()))
		}
		return ifaceSlice

	case map[string]interface{}:
		// Recursively normalize maps
		for key, item := range v {
			v[key] = NormalizeValues(item)
		}
		return v

	default:
		return v
	}
}

// Helper function to extract arguments
func extractArguments(call otto.FunctionCall) []interface{} {
	var args []interface{}
	for _, arg := range call.ArgumentList {
		value, err := arg.Export()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error exporting argument:", err)
			continue
		}
		args = append(args, value)
	}
	return args
}

// Helper function to format console messages
func formatConsoleLog(args []interface{}) string {
	var formattedArgs []string
	for _, arg := range args {
		var formattedArg string
		switch v := arg.(type) {
		case map[string]interface{}, []interface{}:
			// Serialize objects and arrays to JSON
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				formattedArg = fmt.Sprintf("%v", v)
			} else {
				formattedArg = string(jsonBytes)
			}
		default:
			formattedArg = fmt.Sprintf("%v", v)
		}
		formattedArgs = append(formattedArgs, formattedArg)
	}
	return strings.Join(formattedArgs, " ")
}

// adds a way for a plugin to run a DB query and get the results as a JSON document, for example:
// let result = runQuery("SELECT * FROM users WHERE id = ?", [42]);
// console.log(JSON.parse(result)); // Parses the JSON string into a JavaScript object
func addJSAPIRunQuery(vm *otto.Otto, db *cdb.Handler) error {
	// Implement the runQuery function
	err := vm.Set("runQuery", func(call otto.FunctionCall) otto.Value {
		// Extract the query and arguments from the JavaScript call
		query, err := call.Argument(0).ToString()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "extracting query from JavaScript call:", err)
			return otto.UndefinedValue()
		}

		argsArray := call.Argument(1)
		var args []interface{}
		if argsArray.IsObject() {
			argsObj, err := argsArray.Export()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error exporting query arguments: %v", err)
				return otto.UndefinedValue()
			}

			var argsSlice []interface{}
			var ok bool
			argsSlice, ok = argsObj.([]interface{})
			if !ok {
				// If the arguments are not an array, convert them to a slice
				argsSlice = append(argsSlice, argsObj)
			}

			// Process arguments from the JavaScript array
			for _, arg := range argsSlice {
				switch v := arg.(type) {
				case float64:
					args = append(args, int64(v)) // Convert to int64
				case []interface{}: // Handle nested slices
					args = append(args, v...)
				case []uint64: // Flatten uint64 slices
					for _, nested := range v {
						args = append(args, nested)
					}
				default:
					args = append(args, v)
				}
			}
		}

		// Run the query using the provided db handler and arguments
		rows, err := (*db).ExecuteQuery(query, args...)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "executing query:", err)
			return otto.UndefinedValue()
		}
		defer rows.Close() //nolint:errcheck // We can't check error here it's a defer

		// Get the columns from the query result
		columns, err := rows.Columns()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting columns from query result:", err)
			return otto.UndefinedValue()
		}

		// Prepare the result slice
		result := make([]map[string]interface{}, 0)

		// Iterate over the rows
		var length uint64
		for rows.Next() {
			// Create a map for the row
			row := make(map[string]interface{})
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range columns {
				valuePtrs[i] = &values[i]
			}

			// Scan the row into value pointers
			if err := rows.Scan(valuePtrs...); err != nil {
				return otto.UndefinedValue()
			}

			// Map the column names to values
			for i, col := range columns {
				val := values[i]
				if b, ok := val.([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = val
				}
			}

			// Append the row to the result
			result = append(result, row)
			length++
		}
		// Let's add a field to the result to indicate the number of rows returned
		result = append(result, map[string]interface{}{"rows": length})

		// Convert the result to JSON
		_, err = json.Marshal(result)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "marshaling query result to JSON:", err)
			return otto.UndefinedValue()
		}

		// Convert the JSON string to a JavaScript-compatible value
		jsResult, err := vm.ToValue(result)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "converting JSON result to JS value:", err)
			return otto.UndefinedValue()
		}
		//cmn.DebugMsg(cmn.DbgLvlDebug3, "JSON result: %s", jsResult.String())

		return jsResult
	})
	return err
}

/*
	 Example usage in JS
		// Example of generating a "crawl_completed" event
		let status = { pagesCrawled: 100, errors: 5 };
		let result = createEvent(
			"crawl_completed",    // Event type
			12345,                // Source ID
			"info",               // Severity
			status                // Details
		);
		console.log(result); // Prints a JSON document with the event details
*/
func addJSAPICreateEvent(vm *otto.Otto, db *cdb.Handler) error {
	err := vm.Set("createEvent", func(call otto.FunctionCall) otto.Value {
		// Extract arguments from JavaScript call
		eventType, err := call.Argument(0).ToString()
		if err != nil || strings.TrimSpace(eventType) == "" {
			return otto.UndefinedValue() // Event type is mandatory
		}

		sourceIDraw, err := call.Argument(1).ToInteger()
		if err != nil {
			return otto.UndefinedValue() // Source ID is mandatory
		}
		sourceID := uint64(sourceIDraw) //nolint:gosec // We are not using end-user input here

		severity, err := call.Argument(2).ToString()
		if err != nil || strings.TrimSpace(severity) == "" {
			severity = cdb.EventSeverityInfo // Default severity
		}

		detailsArg := call.Argument(3)
		var details map[string]interface{}
		if detailsArg.IsObject() {
			detailsObj, err := detailsArg.Export()
			if err == nil {
				details, _ = detailsObj.(map[string]interface{})
			}
		}
		if details == nil {
			details = make(map[string]interface{}) // Default empty details
		}

		// Create a new event object
		event := cdb.Event{
			SourceID: sourceID,
			Type:     eventType,
			Severity: severity,
			Details:  details,
		}

		// Insert the event into the database
		eID, err := cdb.CreateEvent(db, event)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "inserting event into database: %v", err)
			return otto.UndefinedValue()
		}

		// Return success status
		success, _ := vm.ToValue(map[string]interface{}{
			"status":  "success",
			"eventID": eID,
		})
		return success
	})
	return err
}

/*
Example usage in JS
// Schedule an event to occur 10 minutes from now
let scheduleTime = new Date(Date.now() + 10 * 60 * 1000).toISOString();

let status = { pagesCrawled: 100, errors: 5 };
let result = scheduleEvent(

	"crawl_completed",    // Event type
	12345,                // Source ID
	"info",               // Severity
	status,               // Details
	scheduleTime          // When the event should occur

);
console.log(result); // Prints a JSON document with the scheduling status
*/
func addJSAPIScheduleEvent(vm *otto.Otto, db *cdb.Handler) error {
	err := vm.Set("scheduleEvent", func(call otto.FunctionCall) otto.Value {
		// Extract arguments from JavaScript call
		eventType, err := call.Argument(0).ToString()
		if err != nil || strings.TrimSpace(eventType) == "" {
			return otto.UndefinedValue() // Event type is mandatory
		}

		sourceIDraw, err := call.Argument(1).ToInteger()
		if err != nil {
			return otto.UndefinedValue() // Source ID is mandatory
		}
		sourceID := uint64(sourceIDraw) //nolint:gosec // We are not using end-user input here

		severity, err := call.Argument(2).ToString()
		if err != nil || strings.TrimSpace(severity) == "" {
			severity = cdb.EventSeverityInfo // Default severity
		}

		detailsArg := call.Argument(3)
		var details map[string]interface{}
		if detailsArg.IsObject() {
			detailsObj, err := detailsArg.Export()
			if err == nil {
				details, _ = detailsObj.(map[string]interface{})
			}
		}
		if details == nil {
			details = make(map[string]interface{}) // Default empty details
		}

		scheduleTimeArg, err := call.Argument(4).ToString()
		if err != nil || strings.TrimSpace(scheduleTimeArg) == "" {
			return otto.UndefinedValue() // Schedule time is mandatory
		}

		recurrenceArg, err := call.Argument(5).ToString()
		if err != nil {
			recurrenceArg = "" // Default empty recurrence
		}

		// Create the event when the timer fires
		event := cdb.Event{
			SourceID: sourceID,
			Type:     eventType,
			Severity: severity,
			Details:  details,
		}

		// Schedule the event creation
		scheduleTime, err := cdb.ScheduleEvent(db, event, scheduleTimeArg, recurrenceArg)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "scheduling event: %v", err)
			// return the error
			return otto.UndefinedValue()
		}

		// Return success status to the JS caller
		success, _ := vm.ToValue(map[string]interface{}{
			"status":       "scheduled",
			"eventType":    eventType,
			"scheduleTime": scheduleTime.Format(time.RFC3339),
		})
		return success
	})
	return err
}

// addJSAPIDebugLevel adds a method to fetch the current debug level to the VM
func addJSAPIDebugLevel(vm *otto.Otto) error {
	return vm.Set("getDebugLevel", func(_ otto.FunctionCall) otto.Value {
		// Fetch the current debug level
		debugLevel := cmn.GetDebugLevel()

		// Convert the debug level to a JavaScript-compatible value
		jsDebugLevel, err := vm.ToValue(debugLevel)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error converting debug level to JavaScript value: %v", err)
			return otto.UndefinedValue()
		}

		return jsDebugLevel
	})
}

// addJSAPICrypto adds hashing functions like sha256 and sha1 to the VM
// Usage in JS:
// let hash = crypto.sha256("Hello, World!");
func addJSAPICrypto(vm *otto.Otto) error {
	cryptoObj, err := vm.Object(`crypto = {}`)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error creating crypto object:", err)
		return err
	}

	// Define sha256 function
	err = cryptoObj.Set("sha256", func(call otto.FunctionCall) otto.Value {
		input, err := call.Argument(0).ToString()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error converting argument to string:", err)
			return otto.UndefinedValue()
		}

		hash := sha256.Sum256([]byte(input))
		hashString := hex.EncodeToString(hash[:])
		result, err := vm.ToValue(hashString)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error converting sha256 hash to value:", err)
			return otto.UndefinedValue()
		}
		return result
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error setting sha256 function:", err)
	}

	return nil
}

// CROWler specific API

/*
	 example usage:
		let source = {
			"url": "https://example.com",
			"name": "Example",
			"category_id": 1,
			"usr_id": 1,
			"restricted": 0,
			"flags": 0
		};

		let config = {
			version: "1.0.0",
			format_version: "1.0.0",
			source_name: sourceName,
			crawling_config: {
				site: "Example",
			},
			execution_plan: [
				{
					label: "Default Plan",
					conditions: {
						url_patterns: ".*"
					}
				}
			],
			custom: {
				crawler: {
					max_depth: 1,
					max_links: 1,
				}
			},
			meta_data: {
				"key": "value"
			}
		};

		let sourceID = createSource(source, config);

		console.log(sourceID);
*/
func addJSAPICreateSource(vm *otto.Otto, db *cdb.Handler) error {
	// Implement the `createSource` function
	err := vm.Set("createSource", func(call otto.FunctionCall) otto.Value {
		// Extract the source details from the plugin call
		sourceArg := call.Argument(0)
		if !sourceArg.IsObject() {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid argument: Expected a source object")
			return otto.UndefinedValue()
		}

		sourceMap, err := sourceArg.Export()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to export source argument: %v", err)
			return otto.UndefinedValue()
		}

		sourceData, ok := sourceMap.(map[string]interface{})
		if !ok {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid source argument structure")
			return otto.UndefinedValue()
		}

		// Map sourceData to the Source struct
		var source cdb.Source
		if url, ok := sourceData["url"].(string); ok {
			source.URL = url
		}
		if name, ok := sourceData["name"].(string); ok {
			source.Name = name
		}
		if categoryID, ok := sourceData["category_id"].(float64); ok {
			source.CategoryID = uint64(categoryID)
		}
		if usrID, ok := sourceData["usr_id"].(float64); ok {
			source.UsrID = uint64(usrID)
		}
		if restricted, ok := sourceData["restricted"].(float64); ok {
			source.Restricted = uint(restricted)
		} else {
			source.Restricted = 1 // Default to restricted
		}
		if flags, ok := sourceData["flags"].(float64); ok {
			source.Flags = uint(flags)
		}

		// Extract the config object
		configArg := call.Argument(1)
		if !configArg.IsObject() {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid configuration argument")
			return otto.UndefinedValue()
		}

		configMap, err := configArg.Export()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to export config argument: %v", err)
			return otto.UndefinedValue()
		}

		// Marshal configMap to JSON
		configJSON, err := json.Marshal(configMap)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to marshal config JSON: %v", err)
			return otto.UndefinedValue()
		}

		var config cfg.SourceConfig
		err = json.Unmarshal(configJSON, &config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid configuration format: %v", err)
			return otto.UndefinedValue()
		}

		// Call CreateSource
		sourceID, err := cdb.CreateSource(db, &source, config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to create source: %v", err)
			return otto.UndefinedValue()
		}

		// Return the source ID to the JS environment
		result, err := vm.ToValue(sourceID)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to convert source ID to JavaScript value: %v", err)
			return otto.UndefinedValue()
		}

		return result
	})

	return err
}

func addJSAPIRemoveSource(vm *otto.Otto, db *cdb.Handler) error {
	// Implement the `removeSource` function
	err := vm.Set("removeSource", func(call otto.FunctionCall) otto.Value {
		sourceIDRaw, err := call.Argument(0).ToInteger()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid source ID: %v", err)
			return otto.UndefinedValue()
		}
		sourceID := uint64(sourceIDRaw) //nolint:gosec // We are not using end-user input here

		// Call DeleteSource from cdb
		err = cdb.DeleteSource(db, sourceID)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to delete source: %v", err)
			return otto.UndefinedValue()
		}

		success, _ := vm.ToValue(map[string]interface{}{
			"status":   "success",
			"sourceID": sourceID,
		})
		return success
	})
	return err
}

func addJSAPIVacuumSource(vm *otto.Otto, db *cdb.Handler) error {
	// Implement the `vacuumSource` function
	err := vm.Set("vacuumSource", func(call otto.FunctionCall) otto.Value {
		sourceIDRaw, err := call.Argument(0).ToInteger()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Invalid source ID: %v", err)
			return otto.UndefinedValue()
		}
		sourceID := uint64(sourceIDRaw) //nolint:gosec // We are not using end-user input here

		// Call VacuumSource from cdb (to be implemented)
		err = cdb.VacuumSource(db, sourceID)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to vacuum source: %v", err)
			return otto.UndefinedValue()
		}

		success, _ := vm.ToValue(map[string]interface{}{
			"status":   "success",
			"sourceID": sourceID,
		})
		return success
	})
	return err
}

/* example usage for externalDBQuery in JS:

// Postgres and MySQL example (replace db_type with "mysql" for MySQL)

let config = JSON.stringify({
	db_type: "postgres",
	host: "localhost",
	port: 5432,
	user: "dbUser",
	password: "dbPassword",
	dbname: "dbName"
});

let result = externalDBQuery(config, "SELECT * FROM users");
console.log(result);

// SQLite example

let config = JSON.stringify({
	db_type: "sqlite",
	dbname: "/path/to/db.sqlite"
});

let result = externalDBQuery(config, "SELECT * FROM users");
console.log(result);

// MongoDB example

let config = JSON.stringify({
	db_type: "mongodb",
	host: "localhost",
	port: 27017,
	user: "dbUser",
	password: "dbPassword",
	dbname: "dbName"
});

let query = JSON.stringify({
	collection: "users",
	action: "find",
	filter: { name: "John" }
});

let result = externalDBQuery(config, query);
console.log(result);
*/

// addJSAPIExternalDBQuery adds a new function "externalDBQuery" to the Otto VM,
// allowing engine plugins to query external databases (PostgreSQL, MySQL, SQLite,
// MongoDB, Neo4J) without interfering with the built-in runQuery function.
func addJSAPIExternalDBQuery(vm *otto.Otto) error {
	// Register externalDBQuery to the JS API.
	// Usage in JavaScript:
	//    var config = JSON.stringify({
	//         db_type: "postgres",
	//         host: "127.0.0.1",
	//         port: 5432,
	//         user: "dbuser",
	//         password: "secret",
	//         dbname: "mydb"
	//    });
	//    var result = externalDBQuery(config, "SELECT * FROM mytable");
	//    console.log(result);
	return vm.Set("externalDBQuery", func(call otto.FunctionCall) otto.Value {
		// Get configuration and query from arguments.
		configStr, err := call.Argument(0).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}
		query, err := call.Argument(1).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}

		// Parse configuration JSON.
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configStr), &config); err != nil {
			return otto.UndefinedValue()
		}

		// Determine the database type.
		dbTypeRaw, ok := config["db_type"]
		if !ok {
			// Default to postgres if not specified, or you may choose to error out.
			dbTypeRaw = postgresDBMS
		}
		dbType := strings.ToLower(fmt.Sprintf("%v", dbTypeRaw))

		// Switch among supported databases.
		switch dbType {
		// Relational databases:
		case postgresDBMS, mysqlDBMS, sqliteDBMS:
			var dsn, driverName string
			switch dbType {
			case postgresDBMS:
				driverName = postgresDBMS
				host := fmt.Sprintf("%v", config["host"])
				port := int(config["port"].(float64))
				user := fmt.Sprintf("%v", config["user"])
				password := fmt.Sprintf("%v", config["password"])
				dbname := fmt.Sprintf("%v", config["dbname"])
				// You might also support sslmode if provided.
				dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
					host, port, user, password, dbname)
			case mysqlDBMS:
				driverName = mysqlDBMS
				host := fmt.Sprintf("%v", config["host"])
				port := int(config["port"].(float64))
				user := fmt.Sprintf("%v", config["user"])
				password := fmt.Sprintf("%v", config["password"])
				dbname := fmt.Sprintf("%v", config["dbname"])
				// DSN for MySQL is typically: user:password@tcp(host:port)/dbname
				dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
					user, password, host, port, dbname)
			case sqliteDBMS:
				driverName = "sqlite3"
				// For SQLite, the dbname is the file path.
				dsn = fmt.Sprintf("%v", config["dbname"])
			}
			// Open the DB.
			db, err := sql.Open(driverName, dsn)
			if err != nil {
				return otto.UndefinedValue()
			}
			defer db.Close() //nolint:errcheck // We can't check error here it's a defer

			rows, err := db.Query(query)
			if err != nil {
				return otto.UndefinedValue()
			}
			defer rows.Close() // nolint:errcheck // We can't check error here it's a defer

			cols, err := rows.Columns()
			if err != nil {
				return otto.UndefinedValue()
			}

			results := []map[string]interface{}{}
			for rows.Next() {
				rowMap := make(map[string]interface{})
				// Create a slice for scanning.
				colsVals := make([]interface{}, len(cols))
				colsPtrs := make([]interface{}, len(cols))
				for i := range colsVals {
					colsPtrs[i] = &colsVals[i]
				}

				if err := rows.Scan(colsPtrs...); err != nil {
					return otto.UndefinedValue()
				}

				for i, colName := range cols {
					val := colsVals[i]
					if b, ok := val.([]byte); ok {
						rowMap[colName] = string(b)
					} else {
						rowMap[colName] = val
					}
				}
				results = append(results, rowMap)
			}

			// Convert results to a JavaScript value.
			jsResult, err := vm.ToValue(results)
			if err != nil {
				return otto.UndefinedValue()
			}
			return jsResult

		// MongoDB support.
		case "mongodb":
			const mongoSelect = "find"

			// Extract connection parameters.
			host := fmt.Sprintf("%v", config["host"])
			port := int(config["port"].(float64))
			user := fmt.Sprintf("%v", config["user"])
			password := fmt.Sprintf("%v", config["password"])
			dbname := fmt.Sprintf("%v", config["dbname"])
			// Build MongoDB URI. If authentication is needed:
			var mongoURI string
			if user == "" || password == "" {
				mongoURI = fmt.Sprintf("mongodb://%s:%d", host, port)
			} else {
				mongoURI = fmt.Sprintf("mongodb://%s:%s@%s:%d", user, password, host, port)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
			if err != nil {
				return otto.UndefinedValue()
			}
			defer client.Disconnect(ctx) // nolint:errcheck // We can't check error here it's a defer

			// Process the query object: { action: "find", filter: { name: "John" } }
			var queryJSON map[string]interface{}
			if err := json.Unmarshal([]byte(query), &queryJSON); err != nil {
				return otto.UndefinedValue()
			}

			// Extract collection name from the query object (Required field).
			collectionName := strings.TrimSpace(fmt.Sprintf("%v", queryJSON["collection"]))
			coll := client.Database(dbname).Collection(collectionName)

			// Extract requested action and filter.
			actionRaw, ok := queryJSON["action"]
			if !ok || actionRaw == nil {
				// If the action is not provided, default to a find action.
				actionRaw = mongoSelect
			}
			actionStr := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", actionRaw)))

			var jsResult otto.Value
			switch actionStr {
			case mongoSelect: // find
				filterRaw, ok := queryJSON["filter"].(map[string]interface{})
				if !ok {
					// If the filter is not provided, default to an empty filter.
					filterRaw = map[string]interface{}{}
				}
				filterString, err := json.Marshal(filterRaw)
				if err != nil {
					return otto.UndefinedValue()
				}
				// The query should be a JSON string representing a filter.
				var filter bson.M
				if err := json.Unmarshal(filterString, &filter); err != nil {
					// If parsing fails, default to empty filter.
					filter = bson.M{}
				}
				cursor, err := coll.Find(ctx, filter)
				if err != nil {
					return otto.UndefinedValue()
				}
				defer cursor.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

				var results []bson.M
				if err = cursor.All(ctx, &results); err != nil {
					return otto.UndefinedValue()
				}
				jsResult, err = vm.ToValue(results)
				if err != nil {
					return otto.UndefinedValue()
				}
			default:
				stub := map[string]interface{}{
					"error": fmt.Sprintf("Unsupported action: %s", actionStr),
				}
				jsResult, _ = vm.ToValue(stub)
			}
			return jsResult

		// Neo4J support using NewDriverWithContext.
		case "neo4j":
			host := fmt.Sprintf("%v", config["host"])
			port := int(config["port"].(float64))
			user := fmt.Sprintf("%v", config["user"])
			password := fmt.Sprintf("%v", config["password"])
			// Use the neo4j:// protocol (or bolt:// if needed)
			uri := fmt.Sprintf("neo4j://%s:%d", host, port)
			ctx := context.Background()
			driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""), nil)
			if err != nil {
				return otto.UndefinedValue()
			}
			defer driver.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

			// Create a session.
			session := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
			defer session.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

			// Execute the Cypher query.
			records, err := session.Run(ctx, query, nil)
			if err != nil {
				return otto.UndefinedValue()
			}

			var results []map[string]interface{}
			for records.Next(ctx) {
				record := records.Record()
				recMap := make(map[string]interface{})
				for _, key := range record.Keys {
					if value, found := record.Get(key); found {
						recMap[key] = value
					}
				}
				results = append(results, recMap)
			}
			if err = records.Err(); err != nil {
				return otto.UndefinedValue()
			}
			jsResult, err := vm.ToValue(results)
			if err != nil {
				return otto.UndefinedValue()
			}
			return jsResult

		default:
			stub := map[string]interface{}{
				"error": fmt.Sprintf("Unsupported database type: %s", dbType),
			}
			jsResult, _ := vm.ToValue(stub)
			return jsResult
		}
	})
}

/// Data conversion functions

// toCSV converts an array of objects into a CSV string.
// It assumes that every object in the array has the same keys.
func addJSAPIJSONToCSV(vm *otto.Otto) error {
	return vm.Set("jsonToCSV", func(call otto.FunctionCall) otto.Value {
		// Export the argument (should be an array of objects)
		dataInterface, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		dataSlice, ok := dataInterface.([]interface{})
		if !ok || len(dataSlice) == 0 {
			empty, _ := vm.ToValue("")
			return empty
		}
		// Get header keys from the first row.
		firstRow, ok := dataSlice[0].(map[string]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Collect keys (order is arbitrary; for production, you might want to enforce an order)
		var headers []string
		for key := range firstRow {
			headers = append(headers, key)
		}

		var buf bytes.Buffer
		csvWriter := csv.NewWriter(&buf)
		// Write header row.
		if err := csvWriter.Write(headers); err != nil {
			return otto.UndefinedValue()
		}
		// Write data rows.
		for _, rowInterface := range dataSlice {
			rowMap, ok := rowInterface.(map[string]interface{})
			if !ok {
				continue
			}
			var row []string
			for _, key := range headers {
				// Convert each value to string.
				val := fmt.Sprintf("%v", rowMap[key])
				row = append(row, val)
			}
			if err := csvWriter.Write(row); err != nil {
				return otto.UndefinedValue()
			}
		}
		csvWriter.Flush()
		result, err := vm.ToValue(buf.String())
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

// csvToJSON converts a CSV string into a JavaScript array of objects.
// The first row of the CSV is assumed to contain header keys.
func addJSAPICSVToJSON(vm *otto.Otto) error {
	return vm.Set("csvToJSON", func(call otto.FunctionCall) otto.Value {
		csvStr, err := call.Argument(0).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}
		r := csv.NewReader(strings.NewReader(csvStr))
		records, err := r.ReadAll()
		if err != nil {
			return otto.UndefinedValue()
		}
		if len(records) < 1 {
			return otto.UndefinedValue()
		}
		// First row is headers.
		headers := records[0]
		var results []map[string]interface{}
		for i := 1; i < len(records); i++ {
			row := records[i]
			rowMap := make(map[string]interface{})
			for j, header := range headers {
				if j < len(row) {
					rowMap[header] = row[j]
				} else {
					rowMap[header] = ""
				}
			}
			results = append(results, rowMap)
		}
		result, err := vm.ToValue(results)
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

// xmlToJSON converts an XML string into a JavaScript object.
// This uses the mxj library to convert XML to a map[string]interface{}.
func addJSAPIXMLToJSON(vm *otto.Otto) error {
	return vm.Set("xmlToJSON", func(call otto.FunctionCall) otto.Value {
		xmlStr, err := call.Argument(0).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}
		// Convert XML into a map using mxj.
		mv, err := mxj.NewMapXml([]byte(xmlStr))
		if err != nil {
			return otto.UndefinedValue()
		}
		result, err := vm.ToValue(mv)
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

// jsonToXML converts a JavaScript object (or JSON string) into an XML string.
// It uses mxj to perform the conversion.
func addJSAPIJSONToXML(vm *otto.Otto) error {
	return vm.Set("jsonToXML", func(call otto.FunctionCall) otto.Value {
		// Export the argument (which should be a JS object or JSON string)
		jsonObj, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		// Ensure we have a map; if not, wrap it.
		m, ok := jsonObj.(map[string]interface{})
		if !ok {
			m = map[string]interface{}{"root": jsonObj}
		}
		// Convert the map to XML.
		xmlBytes, err := mxj.AnyXml(m)
		if err != nil {
			return otto.UndefinedValue()
		}
		result, err := vm.ToValue(string(xmlBytes))
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

/// Data Transformation functions

/* Example usage of 2 data transformation functions (filterJSON, mapJSON) in JS:
-------------------------------------------------------------------------------
// Assume myData is a JSON object received from an ETL process.
var myData = {
    contacts: [
        { id: 1, first_name: "Alice", last_name: "Smith", email: "alice@example.com" },
        { id: 2, first_name: "Bob", last_name: "Jones", email: "bob@example.com" }
    ],
    meta_data: { total: 2, source: "contacts_db" },
    settings: { theme: "dark", version: "1.0.0" }
};

// Filter the JSON document to only include "contacts" and "meta_data".
var filtered = filterJSON(myData, ["contacts", "meta_data"]);
console.log("Filtered JSON:", JSON.Stringify(filtered));

-------------------------------------------------------------------------------

// If myData were an array of objects, filterJSON will map the filter over each element.
var arrayData = [
    { id: 1, name: "Alice", age: 30, extra: "foo" },
    { id: 2, name: "Bob", age: 25, extra: "bar" }
];
var filteredArray = filterJSON(arrayData, ["id", "name"]);
console.log("Filtered Array:", JSON.Stringify(filteredArray));

-------------------------------------------------------------------------------

// Sample data: an array of contact objects.
var contacts = [
  { id: 1, first_name: "Alice", last_name: "Smith", email: "alice@example.com" },
  { id: 2, first_name: "Bob", last_name: "Jones", email: "bob@example.com" }
];

// Use mapJSON to add a "greeting" field to each contact.
var updatedContacts = mapJSON(contacts, function(contact) {
  // Add a new property based on the contact's name.
  contact.greeting = "Hello, " + contact.first_name + " " + contact.last_name + "!";
  return contact;
});

console.log("Updated Contacts:", JSON.Stringify(updatedContacts));

-------------------------------------------------------------------------------
*/

// addJSAPIFilterJSON registers a new function "filterJSON" in the Otto VM.
// It accepts a JSON document (object or array) and an array (or comma‐separated string)
// of keys to filter, and returns a new document containing only those keys.
func addJSAPIFilterJSON(vm *otto.Otto) error {
	return vm.Set("filterJSON", func(call otto.FunctionCall) otto.Value {
		// Export the JSON document from the first argument.
		doc, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}

		// Export the filter keys from the second argument.
		keysRaw, err := call.Argument(1).Export()
		if err != nil {
			return otto.UndefinedValue()
		}

		// Convert the filter keys into a slice of strings.
		var keys []string
		switch k := keysRaw.(type) {
		case []interface{}:
			for _, v := range k {
				if s, ok := v.(string); ok {
					keys = append(keys, s)
				}
			}
		case string:
			// If a comma-separated string is provided.
			for _, s := range strings.Split(k, ",") {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" {
					keys = append(keys, trimmed)
				}
			}
		default:
			// Unsupported keys type.
			return otto.UndefinedValue()
		}

		// Filter the JSON document.
		filtered := filterJSONValue(doc, keys)

		// Convert the filtered result back to an Otto value.
		result, err := vm.ToValue(filtered)
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

// filterJSONValue recursively filters a JSON document (object or array)
// and returns only the properties that match one of the provided keys.
// For an object, it returns a new map containing only the keys in the list.
// For an array, it maps the filtering function over each element.
func filterJSONValue(doc interface{}, keys []string) interface{} {
	switch v := doc.(type) {
	case map[string]interface{}:
		filteredMap := make(map[string]interface{})
		for _, key := range keys {
			if val, exists := v[key]; exists {
				filteredMap[key] = val
			}
		}
		return filteredMap
	case []interface{}:
		var filteredArr []interface{}
		for _, elem := range v {
			// If the element is an object, filter it; otherwise, leave it as-is.
			filteredArr = append(filteredArr, filterJSONValue(elem, keys))
		}
		return filteredArr
	default:
		// For non-object, non-array types, return as is.
		return v
	}
}

func addJSAPIMapJSON(vm *otto.Otto) error {
	return vm.Set("mapJSON", func(call otto.FunctionCall) otto.Value {
		// Export the first argument (should be a JSON array)
		docInterface, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		array, ok := docInterface.([]interface{})
		if !ok {
			// Not an array; return undefined.
			return otto.UndefinedValue()
		}

		// The second argument should be a function
		callback := call.Argument(1)
		if !callback.IsFunction() {
			return otto.UndefinedValue()
		}

		// Prepare a new slice to hold the mapped results.
		var mapped []interface{}
		for _, elem := range array {
			// Call the callback with the current element.
			result, err := callback.Call(otto.UndefinedValue(), elem)
			if err != nil {
				// In case of error, skip this element.
				continue
			}
			exportResult, err := result.Export()
			if err != nil {
				continue
			}
			mapped = append(mapped, exportResult)
		}

		// Convert the mapped slice back to an Otto value.
		mappedValue, err := vm.ToValue(mapped)
		if err != nil {
			return otto.UndefinedValue()
		}
		return mappedValue
	})
}

/* example of usage of the following functions:
-------------------------------------------------------------------------------
// Example data: an array of contact objects.
var contacts = [
  { id: 1, first_name: "Alice", last_name: "Smith", email: "alice@example.com" },
  { id: 2, first_name: "Bob", last_name: "Jones", email: "bob@example.com" }
];

// Use reduceJSON to concatenate all email addresses.
var allEmails = reduceJSON(contacts, function(acc, contact) {
  return acc + (acc ? ", " : "") + contact.email;
}, "");
console.log("All Emails:", allEmails);
-------------------------------------------------------------------------------

// Use joinJSON to merge two datasets on the "id" field.
var additionalData = [
  { id: 1, phone: "123-456-7890" },
  { id: 2, phone: "987-654-3210" }
];
var merged = joinJSON(contacts, additionalData, "id");
console.log("Merged Data:", merged);
-------------------------------------------------------------------------------

// Use sortJSON to sort contacts by last name descending.
var sortedContacts = sortJSON(contacts, "last_name", "desc");
console.log("Sorted Contacts:", sortedContacts);
-------------------------------------------------------------------------------

// Compose a pipeline: first, add a greeting, then extract only id and greeting.
var transformed = pipeJSON(contacts, [
  function(arr) {
    // Map over array: add greeting to each contact.
    return arr.map(function(contact) {
      contact.greeting = "Hello " + contact.first_name;
      return contact;
    });
  },
  function(arr) {
    // Map over array: return only id and greeting.
    return arr.map(function(contact) {
      return { id: contact.id, greeting: contact.greeting };
    });
  }
]);
console.log("Transformed Data:", transformed);
-------------------------------------------------------------------------------
*/

// reduceJSON applies a callback to each element of a JSON array, aggregating a result.
// Usage in JS:
//
//	var total = reduceJSON([1,2,3,4], function(acc, val) { return acc + val; }, 0);
func addJSAPIReduceJSON(vm *otto.Otto) error {
	return vm.Set("reduceJSON", func(call otto.FunctionCall) otto.Value {
		// First argument: a JSON array.
		arrInterface, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		arr, ok := arrInterface.([]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Second argument: callback function.
		callback := call.Argument(1)
		if !callback.IsFunction() {
			return otto.UndefinedValue()
		}
		// Third argument: initial accumulator.
		accumulator := call.Argument(2)
		// Iterate over each element and call the callback.
		for _, elem := range arr {
			result, err := callback.Call(otto.UndefinedValue(), accumulator, elem)
			if err != nil {
				continue // optionally log error
			}
			accumulator = result
		}
		return accumulator
	})
}

// joinJSON performs an inner join between two JSON arrays based on a common key.
// Usage in JS:
//
//	var joined = joinJSON(leftArray, rightArray, "id");
func addJSAPIJoinJSON(vm *otto.Otto) error {
	return vm.Set("joinJSON", func(call otto.FunctionCall) otto.Value {
		// First argument: left array.
		leftInterface, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		leftArr, ok := leftInterface.([]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Second argument: right array.
		rightInterface, err := call.Argument(1).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		rightArr, ok := rightInterface.([]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Third argument: join key.
		joinKey, err := call.Argument(2).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}

		// Build an index for the right array.
		rightIndex := make(map[string][]map[string]interface{})
		for _, r := range rightArr {
			rMap, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			keyVal, exists := rMap[joinKey]
			if !exists {
				continue
			}
			keyStr := fmt.Sprintf("%v", keyVal)
			rightIndex[keyStr] = append(rightIndex[keyStr], rMap)
		}

		// For each element in the left array, find matching right records.
		var results []map[string]interface{}
		for _, l := range leftArr {
			lMap, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			keyVal, exists := lMap[joinKey]
			if !exists {
				continue
			}
			keyStr := fmt.Sprintf("%v", keyVal)
			if matches, found := rightIndex[keyStr]; found {
				for _, rMap := range matches {
					joined := make(map[string]interface{})
					// Merge left and right maps.
					for k, v := range lMap {
						joined[k] = v
					}
					for k, v := range rMap {
						joined[k] = v
					}
					results = append(results, joined)
				}
			}
		}
		jsResult, err := vm.ToValue(results)
		if err != nil {
			return otto.UndefinedValue()
		}
		return jsResult
	})
}

// sortJSON sorts an array of JSON objects based on a given key.
// Usage in JS:
//
//	var sorted = sortJSON(dataArray, "last_name", "asc");
//
// The order parameter is optional ("asc" is default, "desc" for descending).
func addJSAPISortJSON(vm *otto.Otto) error {
	return vm.Set("sortJSON", func(call otto.FunctionCall) otto.Value {
		const asc = "asc"
		const desc = "desc"

		// First argument: JSON array.
		arrInterface, err := call.Argument(0).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		arr, ok := arrInterface.([]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Second argument: sort key.
		sortKey, err := call.Argument(1).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}
		// Third argument: order ("asc" or "desc", default "asc").
		order := asc
		if call.Argument(2).IsDefined() {
			order, err = call.Argument(2).ToString()
			if err != nil {
				order = asc
			}
			order = strings.ToLower(order)
		}
		// Use sort.Slice to sort the array.
		sort.Slice(arr, func(i, j int) bool {
			var vi, vj string
			if mi, ok := arr[i].(map[string]interface{}); ok {
				vi = fmt.Sprintf("%v", mi[sortKey])
			}
			if mj, ok := arr[j].(map[string]interface{}); ok {
				vj = fmt.Sprintf("%v", mj[sortKey])
			}
			if order == desc {
				return vj < vi
			}
			return vi < vj
		})
		result, err := vm.ToValue(arr)
		if err != nil {
			return otto.UndefinedValue()
		}
		return result
	})
}

// pipeJSON applies a sequence of transformation callbacks to an initial JSON value.
// Usage in JS:
//
//	var finalValue = pipeJSON(initialValue, [fn1, fn2, fn3]);
func addJSAPIPipeJSON(vm *otto.Otto) error {
	return vm.Set("pipeJSON", func(call otto.FunctionCall) otto.Value {
		// First argument: initial JSON value.
		value := call.Argument(0)
		// Second argument: array of callback functions.
		funcArray, err := call.Argument(1).Export()
		if err != nil {
			return otto.UndefinedValue()
		}
		callbacks, ok := funcArray.([]interface{})
		if !ok {
			return otto.UndefinedValue()
		}
		// Apply each callback sequentially.
		for _, cbInterface := range callbacks {
			cbValue, err := vm.ToValue(cbInterface)
			if err != nil || !cbValue.IsFunction() {
				continue
			}
			newValue, err := cbValue.Call(otto.UndefinedValue(), value)
			if err != nil {
				continue
			}
			value = newValue
		}
		return value
	})
}

// String returns the Plugin as a string
func (p *JSPlugin) String() string {
	return p.Script
}
