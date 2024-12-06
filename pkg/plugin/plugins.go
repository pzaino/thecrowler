// Package plugin provides the plugin functionality for the CROWler.
package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/robertkrimen/otto"
	"github.com/tebeka/selenium"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	httpMethodGet = "GET"
	vdiPlugin     = "vdi_plugin"
	enginePlugin  = "engine_plugin"
	eventPlugin   = "event_plugin"
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
	for _, plugin := range reg.Registry {
		if plugin.EventType == "" || plugin.EventType == "none" {
			continue
		}
		if plugin.EventType == eventType || plugin.EventType == "all" {
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
				return nil, fmt.Errorf("failed to load plugin from %s: %v", path, err)
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
	pNameRegEx := "^//\\s+[@]*name\\:?\\s+([^\n]+)"
	pDescRegEx := "^//\\s+[@]*description\\:?\\s+([^\n]+)"
	pTypeRegEx := "^//\\s+[@]*type\\:?\\s+([^\n]+)"
	pEventTypeRegEx := "^//\\s+[@]*event_type\\:?\\s+([^\n]+)"
	re1 := regexp.MustCompile(pNameRegEx)
	re2 := regexp.MustCompile(pDescRegEx)
	re3 := regexp.MustCompile(pTypeRegEx)
	re4 := regexp.MustCompile(pEventTypeRegEx)
	// Extract the "// @name" comment from the script (usually on the first line)
	pName := ""
	pDesc := ""
	pType := vdiPlugin
	pEventType := ""
	lines := strings.Split(script, "\n")
	for _, line := range lines {
		if re1.MatchString(line) {
			pName = strings.TrimSpace(re1.FindStringSubmatch(line)[1])
		}
		if re2.MatchString(line) {
			pDesc = strings.TrimSpace(re2.FindStringSubmatch(line)[1])
		}
		if re3.MatchString(line) {
			pType = strings.TrimSpace(re3.FindStringSubmatch(line)[1])
		}
		if re4.MatchString(line) {
			pEventType = strings.ToLower(strings.TrimSpace(re4.FindStringSubmatch(line)[1]))
		}
	}

	return &JSPlugin{
		Name:        pName,
		Description: pDesc,
		PType:       pType,
		Script:      script,
		EventType:   pEventType,
	}
}

// Execute executes the JS plugin
func (p *JSPlugin) Execute(wd *selenium.WebDriver, db *cdb.Handler, timeout int, params map[string]interface{}) (map[string]interface{}, error) {
	if p.PType == vdiPlugin {
		return execVDIPlugin(p, timeout, params, wd)
	}
	return execEnginePlugin(p, timeout, params, db)
}

func execVDIPlugin(p *JSPlugin, timeout int, params map[string]interface{}, wd *selenium.WebDriver) (map[string]interface{}, error) {
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
	// Add the CROWler JS API functions
	err := addJSHTTPRequest(vm)
	if err != nil {
		return err
	}

	err = addJSAPIClient(vm)
	if err != nil {
		return err
	}

	err = addJSAPIFetch(vm)
	if err != nil {
		return err
	}

	err = addJSAPIConsoleLog(vm)
	if err != nil {
		return err
	}

	err = addJSAPIRunQuery(vm, db)
	if err != nil {
		return err
	}

	err = addJSAPICreateEvent(vm, db)
	if err != nil {
		return err
	}

	err = addJSAPIScheduleEvent(vm, db)
	if err != nil {
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
	apiClientObject.Set("post", func(call otto.FunctionCall) otto.Value {
		method := "POST"
		url, _ := call.Argument(0).ToString()
		headersArg := call.Argument(1)
		bodyArg := call.Argument(2)
		timeoutMs, err := call.Argument(3).ToInteger()
		if err != nil {
			timeoutMs = 30000
		}

		timeout := time.Duration(timeoutMs) * time.Millisecond
		client := &http.Client{Timeout: timeout}

		var body io.Reader
		if bodyArg.IsDefined() {
			goBody, err := bodyArg.Export()
			if err == nil {
				bodyBytes, err := json.Marshal(goBody)
				if err == nil {
					body = bytes.NewReader(bodyBytes)
				}
			}
		}

		req, err := http.NewRequest(method, url, body)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "creating request:", err)
			return otto.UndefinedValue()
		}

		if headersArg.IsDefined() {
			headersObj := headersArg.Object()
			for _, key := range headersObj.Keys() {
				value, _ := headersObj.Get(key)
				valueStr, _ := value.ToString()
				req.Header.Set(strings.TrimSpace(key), strings.TrimSpace(valueStr))
			}
		}

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
			severity = "info" // Default severity
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
			severity = "info" // Default severity
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

		// Create the event when the timer fires
		event := cdb.Event{
			SourceID: sourceID,
			Type:     eventType,
			Severity: severity,
			Details:  details,
		}

		// Schedule the event creation
		scheduleTime, err := cdb.ScheduleEvent(db, event, scheduleTimeArg)
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

// String returns the Plugin as a string
func (p *JSPlugin) String() string {
	return p.Script
}
