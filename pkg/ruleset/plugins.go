package ruleset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/robertkrimen/otto"
	"github.com/tebeka/selenium"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	httpMethodGet = "GET"
	vdiPlugin     = "vdi_plugin"
)

// NewJSPlugin returns a new JS plugin
func NewJSPlugin(script string) *JSPlugin {
	pNameRegEx := "^//\\s+[@]*name\\:?\\s+([^\n]+)"
	pDescRegEx := "^//\\s+[@]*description\\:?\\s+([^\n]+)"
	pTypeRegEx := "^//\\s+[@]*type\\:?\\s+([^\n]+)"
	re1 := regexp.MustCompile(pNameRegEx)
	re2 := regexp.MustCompile(pDescRegEx)
	re3 := regexp.MustCompile(pTypeRegEx)
	// Extract the "// @name" comment from the script (usually on the first line)
	pName := ""
	pDesc := ""
	pType := vdiPlugin
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
	}

	return &JSPlugin{
		name:        pName,
		description: pDesc,
		pType:       pType,
		script:      script,
	}
}

// NewJSPluginRegister returns a new JSPluginRegister
func NewJSPluginRegister() *JSPluginRegister {
	return &JSPluginRegister{}
}

// Register registers a new JS plugin
func (reg *JSPluginRegister) Register(name string, plugin JSPlugin) {
	// Check if the register is initialized
	if reg.registry == nil {
		reg.registry = make(map[string]JSPlugin)
	}

	// Check if the name is empty
	if strings.TrimSpace(name) == "" {
		name = plugin.name
	}
	name = strings.TrimSpace(name)

	// Register the plugin
	reg.registry[name] = plugin
}

// GetPlugin returns a JS plugin
func (reg *JSPluginRegister) GetPlugin(name string) (JSPlugin, bool) {
	plugin, exists := reg.registry[name]
	return plugin, exists
}

// Execute executes the JS plugin
func (p *JSPlugin) Execute(wd *selenium.WebDriver, timeout int, params map[string]interface{}) (map[string]interface{}, error) {
	if p.pType == vdiPlugin {
		return execVDIPlugin(p, timeout, params, wd)
	}
	return execEnginePlugin(p, timeout, params)
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
	result, err := (*wd).ExecuteScript(p.script, paramsArr)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
		return nil, err
	}

	// Get the result
	resultMap := cmn.ConvertInfToMap(result)

	return resultMap, nil
}

func execEnginePlugin(p *JSPlugin, timeout int, params map[string]interface{}) (map[string]interface{}, error) {
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
	err = setCrowlerJSAPI(vm)
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
			panic("JavaScript execution timeout")
		}
	}(time.Duration(timeout))

	// Run the script
	rval, err := vm.Run(p.script)
	if err != nil {
		return nil, err
	}

	// Get the result
	result, err := vm.Get("result")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg01, err)
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
func setCrowlerJSAPI(vm *otto.Otto) error {
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

	return err
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
func addJSAPIClient(vm *otto.Otto) error {
	// Register the apiClient function
	err := vm.Set("apiClient", func(call otto.FunctionCall) otto.Value {
		// Extract arguments
		method, err := call.Argument(0).ToString() // HTTP method
		if err != nil {
			method = httpMethodGet
		}
		url, err := call.Argument(1).ToString() // URL
		if err != nil {
			return otto.Value{}
		}
		headersArg := call.Argument(2).Object()        // Headers (optional)
		bodyArg := call.Argument(3).Object()           // Body (optional)
		timeoutMs, err := call.Argument(4).ToInteger() // Timeout in milliseconds (optional)
		if err != nil {
			timeoutMs = 30
		}

		// Set default timeout
		timeout := time.Duration(timeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}

		client := &http.Client{
			Timeout: timeout,
		}

		// Prepare the request body
		var body io.Reader
		if bodyArg.Value().IsDefined() {
			goBody, err := bodyArg.Value().Export()
			if err != nil {
				// ottoErr := vm.MakeCustomError("TypeError", "Invalid body argument")
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Error exporting apiClient request's body:", err)
			}
			bodyBytes, err := json.Marshal(goBody)
			if err != nil {
				// ottoErr := vm.MakeCustomError("TypeError", "Error marshaling body to JSON")
				cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Error marshaling apiClient request's body to JSON:", err)
			}
			body = bytes.NewReader(bodyBytes)
		}

		// Create the request
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			//ottoErr, _ := vm.MakeCustomError("RequestError", err.Error())
			cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Error creating request:", err)
		}

		// Set headers
		if headersArg.Value().IsDefined() {
			headersObj := headersArg.Value().Object()
			for _, key := range headersObj.Keys() {
				value, err := headersObj.Get(key)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Error getting header value for key", key, ":", err)
					continue
				}
				valueStr, err := value.ToString()
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "EngineJS: Header value for key", key, "is not a string:", err)
					continue
				}
				req.Header.Set(key, valueStr)
			}
		}

		// Make the HTTP request
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("Error making request:", err)
			return otto.Value{}
		}
		defer resp.Body.Close() //nolint:errcheck // We can't check error here it's a defer

		// Read the response
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("Error reading response body:", err)
			return otto.Value{}
		}

		// Build a response object
		respObject, _ := vm.Object(`({})`)
		err = respObject.Set("status", resp.StatusCode)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error setting status:", err)
		}
		err = respObject.Set("headers", resp.Header)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error setting headers:", err)
		}
		err = respObject.Set("body", string(respBody))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error setting body:", err)
		}

		return respObject.Value()
	})
	return err
}

// addJSAPIFetch adds the fetch function to the VM
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

// String returns the Plugin as a string
func (p *JSPlugin) String() string {
	return p.script
}
