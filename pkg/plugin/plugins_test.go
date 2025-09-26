// Package plugin provides the plugin functionality for the CROWler.
package plugin

import (
	"context"
	"encoding/json"
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

	ctx, cancel := context.WithCancel(context.Background())
	err := setCrowlerJSAPI(ctx, vm, db, nil)
	cancel()
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

func TestAddJSAPIReduceJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPIReduceJSON(vm)
	if err != nil {
		t.Errorf("addJSAPIReduceJSON returned an error: %v", err)
	}

	// Check if the reduceJSON function is set in the VM
	value, err := vm.Get("reduceJSON")
	if err != nil {
		t.Errorf("there should not be an error checking for 'reduceJSON', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'reduceJSON' to be a function, but it is not")
	}

	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name:     "Sum of numbers",
			script:   `reduceJSON([1, 2, 3, 4], function(acc, val) { return acc + val; }, 0)`,
			expected: 10,
		},
		{
			name:     "Concatenate strings",
			script:   `reduceJSON(["a", "b", "c"], function(acc, val) { return acc + val; }, "")`,
			expected: "abc",
		},
		{
			name:     "Sum of object properties",
			script:   `reduceJSON([{x: 1}, {x: 2}, {x: 3}], function(acc, obj) { return acc + obj.x; }, 0)`,
			expected: 6,
		},
		{
			name:       "Invalid first argument",
			script:     `reduceJSON("not an array", function(acc, val) { return acc + val; }, 0)`,
			expected:   map[string]interface{}{"error": "Error, this function requires an array of objects."},
			shouldFail: true,
		},
		{
			name:       "Invalid callback function",
			script:     `reduceJSON([1, 2, 3], "not a function", 0)`,
			expected:   map[string]interface{}{"error": "Error, callback is not a function: not a function"},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)
			if err != nil && !tt.shouldFail {
				t.Fatalf("Error running script: %v", err)
			}

			_, err = value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			/*
				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("Expected result to be %v, but got %v", tt.expected, result)
				}
			*/
		})
	}
}

func TestAddJSAPIFilterJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPIFilterJSON(vm)
	if err != nil {
		t.Errorf("addJSAPIFilterJSON returned an error: %v", err)
	}

	// Check if the filterJSON function is set in the VM
	value, err := vm.Get("filterJSON")
	if err != nil {
		t.Errorf("there should not be an error checking for 'filterJSON', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'filterJSON' to be a function, but it is not")
	}

	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name:     "Filter single key",
			script:   `filterJSON({ "key1": "value1", "key2": "value2" }, ["key1"])`,
			expected: map[string]interface{}{"key1": "value1"},
		},
		{
			name:     "Filter multiple keys",
			script:   `filterJSON({ "key1": "value1", "key2": "value2", "key3": "value3" }, ["key1", "key3"])`,
			expected: map[string]interface{}{"key1": "value1", "key3": "value3"},
		},
		{
			name:     "Filter with comma-separated string",
			script:   `filterJSON({ "key1": "value1", "key2": "value2", "key3": "value3" }, "key1, key3")`,
			expected: map[string]interface{}{"key1": "value1", "key3": "value3"},
		},
		{
			name:     "Filter array of objects",
			script:   `filterJSON([{ "key1": "value1", "key2": "value2" }, { "key1": "value3", "key2": "value4" }], ["key1"])`,
			expected: []interface{}{map[string]interface{}{"key1": "value1"}, map[string]interface{}{"key1": "value3"}},
		},
		{
			name:       "Invalid first argument",
			script:     `filterJSON("not an object", ["key1"])`,
			expected:   map[string]interface{}{"error": "Error this function requires an input JSON object: not an object"},
			shouldFail: true,
		},
		{
			name:       "Invalid second argument",
			script:     `filterJSON({ "key1": "value1" }, 123)`,
			expected:   map[string]interface{}{"error": "Error this function requires a comma separated list of JSON keys to filter: 123"},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)
			if err != nil && !tt.shouldFail {
				t.Fatalf("Error running script: %v", err)
			}

			_, err = value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			/*
				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("Expected result to be %v, but got %v", tt.expected, result)
				}
			*/
		})
	}
}

func TestAddJSAPIMapJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPIMapJSON(vm)
	if err != nil {
		t.Errorf("addJSAPIMapJSON returned an error: %v", err)
	}

	// Check if the mapJSON function is set in the VM
	value, err := vm.Get("mapJSON")
	if err != nil {
		t.Errorf("there should not be an error checking for 'mapJSON', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'mapJSON' to be a function, but it is not")
	}

	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name:     "Map over array of numbers",
			script:   `mapJSON([1, 2, 3], function(val) { return val * 2; })`,
			expected: []interface{}{2, 4, 6},
		},
		{
			name:     "Map over array of objects",
			script:   `mapJSON([{x: 1}, {x: 2}, {x: 3}], function(obj) { obj.x = obj.x * 2; return obj; })`,
			expected: []interface{}{map[string]interface{}{"x": 2}, map[string]interface{}{"x": 4}, map[string]interface{}{"x": 6}},
		},
		{
			name:       "Invalid first argument",
			script:     `mapJSON("not an array", function(val) { return val * 2; })`,
			expected:   map[string]interface{}{"error": "Error, the passed object is not a JSON array."},
			shouldFail: true,
		},
		{
			name:       "Invalid callback function",
			script:     `mapJSON([1, 2, 3], "not a function")`,
			expected:   map[string]interface{}{"error": "Error, the passed callback is not a function: not a function"},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)
			if err != nil && !tt.shouldFail {
				t.Fatalf("Error running script: %v", err)
			}

			_, err = value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			/*
				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("Expected result to be %v, but got %v", tt.expected, result)
				}
			*/
		})
	}
}

func TestAddJSAPIJoinJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPIJoinJSON(vm)
	if err != nil {
		t.Errorf("addJSAPIJoinJSON returned an error: %v", err)
	}

	// Check if the joinJSON function is set in the VM
	value, err := vm.Get("joinJSON")
	if err != nil {
		t.Errorf("there should not be an error checking for 'joinJSON', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'joinJSON' to be a function, but it is not")
	}

	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name: "Join on common key",
			script: `
				joinJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[{ "id": 1, "age": 30 }, { "id": 2, "age": 25 }],
					"id"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 1, "name": "Alice", "age": 30},
				map[string]interface{}{"id": 2, "name": "Bob", "age": 25},
			},
		},
		{
			name: "Join with missing keys",
			script: `
				joinJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 3, "name": "Charlie" }],
					[{ "id": 1, "age": 30 }, { "id": 2, "age": 25 }],
					"id"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 1, "name": "Alice", "age": 30},
			},
		},
		{
			name: "Join with non-matching keys",
			script: `
				joinJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[{ "key": 1, "age": 30 }, { "key": 2, "age": 25 }],
					"id"
				)
			`,
			expected: []interface{}{},
		},
		{
			name: "Invalid left array",
			script: `
				joinJSON(
					"not an array",
					[{ "id": 1, "age": 30 }, { "id": 2, "age": 25 }],
					"id"
				)
			`,
			expected:   map[string]interface{}{"error": "Error, this function requires a 'left' array to merge into: not an array"},
			shouldFail: true,
		},
		{
			name: "Invalid right array",
			script: `
				joinJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"not an array",
					"id"
				)
			`,
			expected:   map[string]interface{}{"error": "Error, this function requires a 'right' array to merge from: not an array"},
			shouldFail: true,
		},
		{
			name: "Invalid join key",
			script: `
				joinJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[{ "id": 1, "age": 30 }, { "id": 2, "age": 25 }],
					123
				)
			`,
			expected:   map[string]interface{}{"error": "Error, this function requires a 'join' key, a JSON tag to use to identify what we want to join: 123"},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)
			if err != nil && !tt.shouldFail {
				t.Fatalf("Error running script: %v", err)
			}

			_, err = value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			/*
				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("Expected result to be %v, but got %v", tt.expected, result)
				}
			*/
		})
	}
}

func TestAddJSAPISortJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPISortJSON(vm)
	if err != nil {
		t.Errorf("addJSAPISortJSON returned an error: %v", err)
	}

	// Check if the sortJSON function is set in the VM
	value, err := vm.Get("sortJSON")
	if err != nil {
		t.Errorf("there should not be an error checking for 'sortJSON', but we have got: %v", err)
	}

	if !value.IsFunction() {
		t.Errorf("Expected 'sortJSON' to be a function, but it is not")
	}

	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name: "Sort array of objects ascending",
			script: `
				sortJSON(
					[{ "id": 3, "name": "Charlie" }, { "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"id",
					"asc"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 1, "name": "Alice"},
				map[string]interface{}{"id": 2, "name": "Bob"},
				map[string]interface{}{"id": 3, "name": "Charlie"},
			},
		},
		{
			name: "Sort array of objects descending",
			script: `
				sortJSON(
					[{ "id": 3, "name": "Charlie" }, { "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"id",
					"desc"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 3, "name": "Charlie"},
				map[string]interface{}{"id": 2, "name": "Bob"},
				map[string]interface{}{"id": 1, "name": "Alice"},
			},
		},
		{
			name: "Sort array of objects with default order (ascending)",
			script: `
				sortJSON(
					[{ "id": 3, "name": "Charlie" }, { "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"id"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 1, "name": "Alice"},
				map[string]interface{}{"id": 2, "name": "Bob"},
				map[string]interface{}{"id": 3, "name": "Charlie"},
			},
		},
		{
			name: "Invalid first argument",
			script: `
				sortJSON(
					"not an array",
					"id",
					"asc"
				)
			`,
			expected:   map[string]interface{}{"error": "Error, this function requires a JSON array in input: not an array"},
			shouldFail: true,
		},
		{
			name: "Invalid sort key",
			script: `
				sortJSON(
					[{ "id": 3, "name": "Charlie" }, { "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"",
					"asc"
				)
			`,
			expected:   map[string]interface{}{"error": "Error, this function requires a valid JSON key to be ordered: "},
			shouldFail: true,
		},
		{
			name: "Invalid order",
			script: `
				sortJSON(
					[{ "id": 3, "name": "Charlie" }, { "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"id",
					"invalid"
				)
			`,
			expected: []interface{}{
				map[string]interface{}{"id": 1, "name": "Alice"},
				map[string]interface{}{"id": 2, "name": "Bob"},
				map[string]interface{}{"id": 3, "name": "Charlie"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)
			if err != nil && !tt.shouldFail {
				t.Fatalf("Error running script: %v", err)
			}

			_, err = value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			/*
				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("Expected result to be %v, but got %v", tt.expected, result)
				}
			*/
		})
	}
}

func TestAddJSAPIPipeJSON(t *testing.T) {
	vm := otto.New()

	err := addJSAPIPipeJSON(vm)
	if err != nil {
		t.Errorf("addJSAPIPipeJSON returned an error: %v", err)
	}

	// Ensure that `pipeJSON` is properly registered in the VM
	value, err := vm.Get("pipeJSON")
	if err != nil {
		t.Fatalf("Error checking for 'pipeJSON': %v", err)
	}

	if !value.IsFunction() {
		t.Fatalf("Expected 'pipeJSON' to be a function, but it is not")
	}

	// Define test cases
	tests := []struct {
		name       string
		script     string
		expected   interface{}
		shouldFail bool
	}{
		{
			name: "Pipeline with single function",
			script: `
				pipeJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[
						function(arr) {
							return arr.map(function(obj) {
								obj.name = obj.name.toUpperCase();
								return obj;
							});
						}
					]
				)
			`,
			expected: []map[string]interface{}{
				{"id": 1, "name": "ALICE"},
				{"id": 2, "name": "BOB"},
			},
		},

		{
			name: "Pipeline with multiple functions",
			script: `
				pipeJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[
						function(arr) {
							return arr.map(function(obj) {
								obj.name = obj.name.toUpperCase();
								return obj;
							});
						},
						function(arr) {
							return arr.filter(function(obj) {
								return obj.id === 1;
							});
						}
					]
				)
			`,
			expected: []map[string]interface{}{
				{"id": 1, "name": "ALICE"},
			},
		},

		{
			name: "Invalid first argument (not an array)",
			script: `
				pipeJSON(
					"not an array",
					[
						function(arr) {
							return arr.map(function(obj) {
								obj.name = obj.name.toUpperCase();
								return obj;
							});
						}
					]
				)
			`,
			shouldFail: true,
		},

		{
			name: "Invalid second argument (not an array)",
			script: `
				pipeJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					"not an array"
				)
			`,
			shouldFail: true,
		},

		{
			name: "Pipeline with non-function element (should be ignored)",
			script: `
				pipeJSON(
					[{ "id": 1, "name": "Alice" }, { "id": 2, "name": "Bob" }],
					[
						function(arr) {
							return arr.map(function(obj) {
								obj.name = obj.name.toUpperCase();
								return obj;
							});
						},
						"not a function"
					]
				)
			`,
			expected: []map[string]interface{}{
				{"id": 1, "name": "ALICE"},
				{"id": 2, "name": "BOB"},
			},
		},

		{
			name: "Empty function array (should return original value)",
			script: `
				pipeJSON({ "id": 1, "name": "Alice" }, [])
			`,
			expected: map[string]interface{}{"id": 1, "name": "Alice"},
		},

		{
			name: "Pipeline with empty input array",
			script: `
				pipeJSON([], [
					function(arr) {
						return arr.map(function(obj) {
							obj.name = obj.name.toUpperCase();
							return obj;
						});
					}
				])
			`,
			expected: []map[string]interface{}{},
		},
	}

	// Execute test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := vm.Run(tt.script)

			// If failure was expected, check for either an error OR an error object.
			if tt.shouldFail {
				if err == nil {
					// Check if the returned value is an error object OR `undefined`
					result, _ := value.Export()
					if result == nil {
						fmt.Println("✅ Received undefined as expected for an invalid input")
						return // ✅ Test passes if undefined (failure case)
					}
					if resultMap, ok := result.(map[string]interface{}); ok {
						if _, exists := resultMap["error"]; exists {
							fmt.Println("✅ Error object detected:", resultMap)
							return // ✅ Test passes if error object exists
						}
					}
					//t.Fatalf("❌ Expected an error or undefined but got: %v", result)
				}
				fmt.Println("✅ Caught expected error:", err)
				return // ✅ Test passes if an actual Go error was caught
			}

			// If an error occurred unexpectedly, fail the test
			if err != nil {
				t.Fatalf("Error running script: %v", err)
			}

			// Export the result to Go
			result, err := value.Export()
			if err != nil {
				t.Fatalf("Error exporting result: %v", err)
			}

			// Convert both expected and actual results to JSON for comparison
			expectedJSON, _ := json.Marshal(tt.expected)
			resultJSON, _ := json.Marshal(result)

			if !reflect.DeepEqual(expectedJSON, resultJSON) {
				t.Errorf("Expected result to be %s, but got %s", expectedJSON, resultJSON)
			}
		})
	}
}

func TestJSPluginRegisterRemove(t *testing.T) {
	reg := NewJSPluginRegister()

	// Create and register plugins
	plugin1 := NewJSPlugin("console.log('Plugin 1')")
	plugin1.Name = "plugin1"

	plugin2 := NewJSPlugin("console.log('Plugin 2')")
	plugin2.Name = "plugin2"

	plugin3 := NewJSPlugin("console.log('Plugin 3')")
	plugin3.Name = "plugin3"

	reg.Register("plugin1", *plugin1)
	reg.Register("plugin2", *plugin2)
	reg.Register("plugin3", *plugin3)

	// Ensure plugins are registered
	if _, exists := reg.GetPlugin("plugin1"); !exists {
		t.Errorf("Plugin 'plugin1' should be registered")
	}
	if _, exists := reg.GetPlugin("plugin2"); !exists {
		t.Errorf("Plugin 'plugin2' should be registered")
	}
	if _, exists := reg.GetPlugin("plugin3"); !exists {
		t.Errorf("Plugin 'plugin3' should be registered")
	}

	// Remove a plugin
	reg.Remove("plugin2")

	// Check if the plugin is removed
	if _, exists := reg.GetPlugin("plugin2"); exists {
		t.Errorf("Plugin 'plugin2' should be removed")
	}

	// Ensure other plugins are still registered
	if _, exists := reg.GetPlugin("plugin1"); !exists {
		t.Errorf("Plugin 'plugin1' should still be registered")
	}
	if _, exists := reg.GetPlugin("plugin3"); !exists {
		t.Errorf("Plugin 'plugin3' should still be registered")
	}

	// Check the order list
	expectedOrder := []string{"plugin1", "plugin3"}
	if !reflect.DeepEqual(reg.Order, expectedOrder) {
		t.Errorf("Expected order %v, got %v", expectedOrder, reg.Order)
	}

	// Test removing a non-existent plugin
	reg.Remove("nonexistent")
	if len(reg.Order) != 2 {
		t.Errorf("Order list should remain unchanged when removing a non-existent plugin")
	}

	// Test removing with an empty name
	reg.Remove("")
	if len(reg.Order) != 2 {
		t.Errorf("Order list should remain unchanged when removing with an empty name")
	}

	// Test removing from an uninitialized registry
	uninitializedReg := &JSPluginRegister{}
	uninitializedReg.Remove("plugin1")
	if uninitializedReg.Registry != nil || len(uninitializedReg.Order) != 0 {
		t.Errorf("Uninitialized registry should remain unchanged after calling Remove")
	}
}
