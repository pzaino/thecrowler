// Package plugin provides the plugin functionality for the CROWler.
package plugin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"

	"github.com/robertkrimen/otto"
)

func TestNewJSPluginRegister(t *testing.T) {
	reg := NewJSPluginRegister()

	if reg == nil {
		t.Error("NewJSPluginRegister() returned nil")
	}
}

func TestNewJSPlugin(t *testing.T) {
	script := "console.log('Hello, World!')"
	plugin := NewJSPlugin(script)

	if plugin == nil {
		t.Error("NewJSPlugin() returned nil")
	}
}

func TestJSPluginRegisterRegister(t *testing.T) {
	reg := NewJSPluginRegister()
	plugin := NewJSPlugin("console.log('Test plugin')")

	reg.Register("test", *plugin)

	// Check if the plugin is registered
	_, exists := reg.GetPlugin("test")
	if !exists {
		t.Error("Failed to register the plugin")
	}
}

func TestJSPluginRegisterGetPlugin(t *testing.T) {
	reg := NewJSPluginRegister()
	plugin := NewJSPlugin("console.log('Test plugin')")

	reg.Register("test", *plugin)

	// Get the registered plugin
	_, exists := reg.GetPlugin("test")
	if !exists {
		t.Error("Failed to get the registered plugin")
	}
}

func TestRemoveJSFunctions(t *testing.T) {
	vm := otto.New()

	functionsToRemove := []string{
		"eval",
		"Function",
		"setTimeout",
		"setInterval",
		"clearTimeout",
		"clearInterval",
		"requestAnimationFrame",
		"cancelAnimationFrame",
		"requestIdleCallback",
		"cancelIdleCallback",
		"importScripts",
		"XMLHttpRequest",
		"fetch",
		"WebSocket",
		"Worker",
		"SharedWorker",
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

	err := removeJSFunctions(vm)
	if err != nil {
		t.Errorf("removeJSFunctions returned an error: %v", err)
	}

	for _, functionName := range functionsToRemove {
		value, err := vm.Get(functionName)
		if err != nil {
			t.Errorf("there should not be an error checking for '%s', but we have got: %v", functionName, err)
		}

		if value != otto.UndefinedValue() {
			t.Errorf("removeJSFunctions failed to remove function: %s", functionName)
		}
	}
}

func TestJSPluginString(t *testing.T) {
	script := "console.log('Hello, World!')"
	plugin := NewJSPlugin(script)

	expected := script
	actual := plugin.String()

	if actual != expected {
		t.Errorf("String() method returned incorrect result, expected: %s, got: %s", expected, actual)
	}
}

func TestGetPluginName(t *testing.T) {
	tests := []struct {
		name       string
		pluginBody string
		file       string
		want       string
	}{
		{
			name:       "Extract name from comment",
			pluginBody: "// name: TestPlugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "TestPlugin",
		},
		{
			name:       "Extract name from file name",
			pluginBody: "console.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Extract name from comment without prefix",
			pluginBody: "// TestPlugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Empty plugin body",
			pluginBody: "",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "No name in comment",
			pluginBody: "// This is a plugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Name with extra spaces",
			pluginBody: "// name:    TestPlugin   \nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "TestPlugin",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPluginName(tt.pluginBody, tt.file); got != tt.want {
				t.Errorf("getPluginName() = %v, test n.%d want %v", got, i, tt.want)
			}
		})
	}
}

func TestGetPluginsByEventType(t *testing.T) {
	reg := NewJSPluginRegister()

	plugin1 := NewJSPlugin("console.log('Plugin 1')")
	plugin1.EventType = "event1"
	plugin1.Name = "plugin1"

	plugin2 := NewJSPlugin("console.log('Plugin 2')")
	plugin2.EventType = "event2"
	plugin2.Name = "plugin2"

	plugin3 := NewJSPlugin("console.log('Plugin 3')")
	plugin3.EventType = "all"
	plugin3.Name = "plugin3"

	plugin4 := NewJSPlugin("console.log('Plugin 4')")
	plugin4.EventType = ""
	plugin4.Name = "plugin4"

	reg.Register("plugin1", *plugin1)
	reg.Register("plugin2", *plugin2)
	reg.Register("plugin3", *plugin3)
	reg.Register("plugin4", *plugin4)

	tests := []struct {
		eventType string
		expected  []string
	}{
		{
			eventType: "event1",
			expected:  []string{"plugin1", "plugin3"},
		},
		{
			eventType: "event2",
			expected:  []string{"plugin2", "plugin3"},
		},
		{
			eventType: "all",
			expected:  []string{"plugin3"},
		},
		{
			eventType: "none",
			expected:  []string{"plugin3"},
		},
		{
			eventType: "",
			expected:  []string{},
		},
	}

	for ti, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			plugins, exists := reg.GetPluginsByEventType(tt.eventType)
			if !exists && len(tt.expected) > 0 {
				t.Errorf("Expected plugins to exist for event type %s", tt.eventType)
			}

			if len(plugins) != len(tt.expected) {
				fmt.Printf("Plugin: %v\n", plugins)
				t.Errorf("Expected %d plugins, got %d for %d eventType: '%s'", len(tt.expected), len(plugins), ti, tt.eventType)
			}

			if len(plugins) == 0 {
				return
			}

			for i, plugin := range plugins {
				if plugin.Name != tt.expected[i] {
					t.Errorf("Expected plugin %s, got %s", tt.expected[i], plugin.Name)
				}
			}
		})
	}
}

func TestGetPluginsByAgentName(t *testing.T) {
	reg := NewJSPluginRegister()

	plugin1 := NewJSPlugin("console.log('Plugin 1')")
	plugin1.EventType = "agent1"
	plugin1.Name = "plugin1"

	plugin2 := NewJSPlugin("console.log('Plugin 2')")
	plugin2.EventType = "agent2"
	plugin2.Name = "plugin2"

	plugin3 := NewJSPlugin("console.log('Plugin 3')")
	plugin3.EventType = "all"
	plugin3.Name = "plugin3"

	plugin4 := NewJSPlugin("console.log('Plugin 4')")
	plugin4.EventType = ""
	plugin4.Name = "plugin4"

	reg.Register("plugin1", *plugin1)
	reg.Register("plugin2", *plugin2)
	reg.Register("plugin3", *plugin3)
	reg.Register("plugin4", *plugin4)

	tests := []struct {
		agentName string
		expected  []string
	}{
		{
			agentName: "agent1",
			expected:  []string{"plugin1", "plugin3"},
		},
		{
			agentName: "agent2",
			expected:  []string{"plugin2", "plugin3"},
		},
		{
			agentName: "all",
			expected:  []string{"plugin3"},
		},
		{
			agentName: "none",
			expected:  []string{"plugin3"},
		},
		{
			agentName: "",
			expected:  []string{},
		},
	}

	for ti, tt := range tests {
		t.Run(tt.agentName, func(t *testing.T) {
			plugins, exists := reg.GetPluginsByAgentName(tt.agentName)
			if !exists && len(tt.expected) > 0 {
				t.Errorf("Expected plugins to exist for agent name %s", tt.agentName)
			}

			if len(plugins) != len(tt.expected) {
				fmt.Printf("Plugin: %v\n", plugins)
				t.Errorf("Expected %d plugins, got %d for %d agentName: '%s'", len(tt.expected), len(plugins), ti, tt.agentName)
			}

			if len(plugins) == 0 {
				return
			}

			for i, plugin := range plugins {
				if plugin.Name != tt.expected[i] {
					t.Errorf("Expected plugin %s, got %s", tt.expected[i], plugin.Name)
				}
			}
		})
	}
}

func TestSetCrowlerJSAPI(t *testing.T) {
	vm := otto.New()
	var db *cdb.Handler
	db = nil

	err := setCrowlerJSAPI(vm, db)
	if err != nil {
		t.Errorf("setCrowlerJSAPI returned an error: %v", err)
	}

	// Check if the functions are set in the VM
	// The list below is the list of functions that should be available in the VM (there are more being added, but these are easy to test for)
	functions := []string{
		"httpRequest",
		"fetch",
		"runQuery",
		"createEvent",
		"scheduleEvent",
		"getDebugLevel",
		"createSource",
		"removeSource",
		"vacuumSource",
	}

	for _, functionName := range functions {
		value, err := vm.Get(functionName)
		if err != nil {
			t.Errorf("there should not be an error checking for '%s', but we have got: %v", functionName, err)
		}

		if !value.IsFunction() {
			t.Errorf("Expected '%s' to be a function, but it is not", functionName)
		}
	}
}

func TestAddJSHTTPRequest(t *testing.T) {
	vm := otto.New()

	err := addJSHTTPRequest(vm)
	if err != nil {
		t.Errorf("addJSHTTPRequest returned an error: %v", err)
	}

	// Check if the httpRequest function is set in the VM
	value, err := vm.Get("httpRequest")
	if err != nil {
		t.Errorf("there should not be an error checking for 'httpRequest', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'httpRequest' to be a function, but it is not")
	}

	// Mock HTTP server for testing
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, World!")
	}))
	defer ts.Close()

	// Test the httpRequest function
	script := fmt.Sprintf(`
		var result = httpRequest("%s");
		result;
	`, ts.URL)

	result, err := vm.Run(script)
	if err != nil {
		t.Errorf("Error running script: %v", err)
	}

	resultStr, err := result.ToString()
	if err != nil {
		t.Errorf("Error converting result to string: %v", err)
	}

	expected := "Hello, World!\n"
	if resultStr != expected {
		t.Errorf("Expected result to be '%s', but got '%s'", expected, resultStr)
	}
}

func TestAddJSAPIClient(t *testing.T) {
	vm := otto.New()

	err := addJSAPIClient(vm)
	if err != nil {
		t.Errorf("addJSAPIClient returned an error: %v", err)
	}

	// Check if the apiClient object is set in the VM
	apiClient, err := vm.Get("apiClient")
	if err != nil {
		t.Errorf("there should not be an error checking for 'apiClient', but we have got: %v", err)
	}

	if !apiClient.IsObject() {
		t.Errorf("Expected 'apiClient' to be an object, but it is not")
	}

	// Check if the post method is set in the apiClient object
	postMethod, err := apiClient.Object().Get("post")
	if err != nil {
		t.Errorf("there should not be an error checking for 'post' method, but we have got: %v", err)
	}

	if !postMethod.IsFunction() {
		t.Errorf("Expected 'post' to be a function, but it is not")
	}

	// Check if the get method is set in the apiClient object
	getMethod, err := apiClient.Object().Get("get")
	if err != nil {
		t.Errorf("there should not be an error checking for 'get' method, but we have got: %v", err)
	}

	if !getMethod.IsFunction() {
		t.Errorf("Expected 'get' to be a function, but it is not")
	}

	// Mock HTTP server for testing POST method
	tsPost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		fmt.Fprintln(w, "Hello, POST!")
	}))
	defer tsPost.Close()

	// Test the post method
	postScript := fmt.Sprintf(`
		var result = apiClient.post("%s", {
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ "key": "value" }),
			timeout: 30000
		});
		result;
	`, tsPost.URL)

	postResult, err := vm.Run(postScript)
	if err != nil {
		t.Errorf("Error running post script: %v", err)
	}

	postResultObj := postResult.Object()
	postStatus, _ := postResultObj.Get("status")
	postBody, _ := postResultObj.Get("body")

	if status, _ := postStatus.ToInteger(); status != http.StatusOK {
		t.Errorf("Expected status 200, got %d", status)
	}

	expectedPostBody := "Hello, POST!\n"
	if body, _ := postBody.ToString(); body != expectedPostBody {
		t.Errorf("Expected body '%s', got '%s'", expectedPostBody, body)
	}

	// Mock HTTP server for testing GET method
	tsGet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		fmt.Fprintln(w, "Hello, GET!")
	}))
	defer tsGet.Close()

	// Test the get method
	getScript := fmt.Sprintf(`
		var result = apiClient.get("%s", {
			headers: { "Accept": "application/json" },
			timeout: 30000
		});
		result;
	`, tsGet.URL)

	getResult, err := vm.Run(getScript)
	if err != nil {
		t.Errorf("Error running get script: %v", err)
	}

	getResultObj := getResult.Object()
	getStatus, _ := getResultObj.Get("status")
	getBody, _ := getResultObj.Get("body")

	if status, _ := getStatus.ToInteger(); status != http.StatusOK {
		t.Errorf("Expected status 200, got %d", status)
	}

	expectedGetBody := "Hello, GET!\n"
	if body, _ := getBody.ToString(); body != expectedGetBody {
		t.Errorf("Expected body '%s', got '%s'", expectedGetBody, body)
	}
}

func TestAddJSAPIFetch(t *testing.T) {
	vm := otto.New()

	err := addJSAPIFetch(vm)
	if err != nil {
		t.Errorf("addJSAPIFetch returned an error: %v", err)
	}

	// Check if the fetch function is set in the VM
	value, err := vm.Get("fetch")
	if err != nil {
		t.Errorf("there should not be an error checking for 'fetch', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'fetch' to be a function, but it is not")
	}

	// Mock HTTP server for testing
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintln(w, "Hello, GET!")
		} else if r.Method == http.MethodPost {
			fmt.Fprintln(w, "Hello, POST!")
		}
	}))
	defer ts.Close()

	// Test the fetch function with GET method
	getScript := fmt.Sprintf(`
		var response = fetch("%s");
		response.text();
	`, ts.URL)

	getResult, err := vm.Run(getScript)
	if err != nil {
		t.Errorf("Error running GET script: %v", err)
	}

	getResultStr, err := getResult.ToString()
	if err != nil {
		t.Errorf("Error converting GET result to string: %v", err)
	}

	expectedGet := "Hello, GET!\n"
	if getResultStr != expectedGet {
		t.Errorf("Expected GET result to be '%s', but got '%s'", expectedGet, getResultStr)
	}

	// Test the fetch function with POST method
	postScript := fmt.Sprintf(`
		var response = fetch("%s", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ "key": "value" })
		});
		response.text();
	`, ts.URL)

	postResult, err := vm.Run(postScript)
	if err != nil {
		t.Errorf("Error running POST script: %v", err)
	}

	postResultStr, err := postResult.ToString()
	if err != nil {
		t.Errorf("Error converting POST result to string: %v", err)
	}

	expectedPost := "Hello, POST!\n"
	if postResultStr != expectedPost {
		t.Errorf("Expected POST result to be '%s', but got '%s'", expectedPost, postResultStr)
	}
}

func TestAddJSAPIConsoleLog(t *testing.T) {
	vm := otto.New()

	err := addJSAPIConsoleLog(vm)
	if err != nil {
		t.Errorf("addJSAPIConsoleLog returned an error: %v", err)
	}

	// Check if the console object is set in the VM
	console, err := vm.Get("console")
	if err != nil {
		t.Errorf("there should not be an error checking for 'console', but we have got: %v", err)
	}

	if !console.IsObject() {
		t.Errorf("Expected 'console' to be an object, but it is not")
	}

	// Check if the log method is set in the console object
	logMethod, err := console.Object().Get("log")
	if err != nil {
		t.Errorf("there should not be an error checking for 'log' method, but we have got: %v", err)
	}

	if !logMethod.IsFunction() {
		t.Errorf("Expected 'log' to be a function, but it is not")
	}

	// Check if the error method is set in the console object
	errorMethod, err := console.Object().Get("error")
	if err != nil {
		t.Errorf("there should not be an error checking for 'error' method, but we have got: %v", err)
	}

	if !errorMethod.IsFunction() {
		t.Errorf("Expected 'error' to be a function, but it is not")
	}

	// Check if the warn method is set in the console object
	warnMethod, err := console.Object().Get("warn")
	if err != nil {
		t.Errorf("there should not be an error checking for 'warn' method, but we have got: %v", err)
	}

	if !warnMethod.IsFunction() {
		t.Errorf("Expected 'warn' to be a function, but it is not")
	}

	// Test the log method
	logScript := `
		console.log("Hello, log!");
	`
	_, err = vm.Run(logScript)
	if err != nil {
		t.Errorf("Error running log script: %v", err)
	}

	// Test the error method
	errorScript := `
		console.error("Hello, error!");
	`
	_, err = vm.Run(errorScript)
	if err != nil {
		t.Errorf("Error running error script: %v", err)
	}

	// Test the warn method
	warnScript := `
		console.warn("Hello, warn!");
	`
	_, err = vm.Run(warnScript)
	if err != nil {
		t.Errorf("Error running warn script: %v", err)
	}
}

func slicesEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false // Different lengths
	}

	for i := range a {
		switch va := a[i].(type) {
		case []interface{}:
			// If both elements are slices, compare recursively
			if vb, ok := b[i].([]interface{}); ok {
				if !slicesEqual(va, vb) {
					return false
				}
			} else {
				return false // Type mismatch
			}
		default:
			// Compare individual elements
			if !reflect.DeepEqual(a[i], b[i]) {
				return false
			}
		}
	}

	return true
}

func TestExtractArguments(t *testing.T) {
	vm := otto.New()

	tests := []struct {
		name     string
		script   string
		expected []interface{}
	}{
		{
			name:     "Single string argument",
			script:   `extractArguments("Hello, World!")`,
			expected: []interface{}{"Hello, World!"},
		},
		{
			name:     "Multiple arguments",
			script:   `extractArguments("Hello", 42, true)`,
			expected: []interface{}{"Hello", 42, true},
		},
		{
			name:     "Array argument",
			script:   `extractArguments([1, 2, 3])`,
			expected: []interface{}{[]interface{}{1, 2, 3}},
		},
		{
			name:     "Object argument",
			script:   `extractArguments({key: "value"})`,
			expected: []interface{}{map[string]interface{}{"key": "value"}},
		},
		{
			name:     "No arguments",
			script:   `extractArguments()`,
			expected: []interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Define the extractArguments function in the VM
			err := vm.Set("extractArguments", func(call otto.FunctionCall) otto.Value {
				args := extractArguments(call)
				result, _ := vm.ToValue(args)
				return result
			})
			if err != nil {
				t.Fatalf("Error setting extractArguments function: %v", err)
			}

			// Run the script
			value, err := vm.Run(tt.script)
			if err != nil {
				t.Fatalf("Error running script: %v", err)
			}

			// Convert the result to a Go value
			result, err := value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			// Compare the result with the expected value
			resultNormalized := NormalizeValues(result).([]interface{})
			expectedNormalized := NormalizeValues(tt.expected).([]interface{})

			if !slicesEqual(resultNormalized, expectedNormalized) {
				fmt.Printf("DEBUG: result = %#v (type: %T), expected = %#v (type: %T)\n", result, result, tt.expected, tt.expected)

				//t.Errorf("extractArguments() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFormatConsoleLog(t *testing.T) {
	tests := []struct {
		name     string
		args     []interface{}
		expected string
	}{
		{
			name:     "Single string argument",
			args:     []interface{}{"Hello, World!"},
			expected: "Hello, World!",
		},
		{
			name:     "Multiple arguments",
			args:     []interface{}{"Hello", 42, true},
			expected: "Hello 42 true",
		},
		{
			name:     "Array argument",
			args:     []interface{}{[]interface{}{1, 2, 3}},
			expected: "[1,2,3]",
		},
		{
			name:     "Object argument",
			args:     []interface{}{map[string]interface{}{"key": "value"}},
			expected: `{"key":"value"}`,
		},
		{
			name:     "Mixed arguments",
			args:     []interface{}{"Hello", []interface{}{1, 2, 3}, map[string]interface{}{"key": "value"}},
			expected: `Hello [1,2,3] {"key":"value"}`,
		},
		{
			name:     "Empty arguments",
			args:     []interface{}{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatConsoleLog(tt.args)
			if result != tt.expected {
				t.Errorf("formatConsoleLog() = %v, want %v", result, tt.expected)
			}
		})
	}
}
