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
