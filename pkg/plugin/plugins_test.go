// Package plugin provides the plugin functionality for the CROWler.
package plugin

import (
	"testing"

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
