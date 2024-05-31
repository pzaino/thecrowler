package ruleset

import (
	"time"

	"github.com/robertkrimen/otto"
)

// NewJSPlugin returns a new JS plugin
func NewJSPlugin(script string) *JSPlugin {
	return &JSPlugin{script: script}
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
	// Register the plugin
	reg.registry[name] = plugin
}

// GetPlugin returns a JS plugin
func (reg *JSPluginRegister) GetPlugin(name string) (JSPlugin, bool) {
	plugin, exists := reg.registry[name]
	return plugin, exists
}

// Execute executes the JS plugin
func (p *JSPlugin) Execute(timeout int, params map[string]interface{}) (map[string]interface{}, error) {
	// Create a new VM
	vm := otto.New()
	err := removeJSFunctions(vm)
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
		return nil, err
	}
	resultMap, err := result.Export()
	if err != nil {
		return nil, err
	}
	resultValue, ok := resultMap.(map[string]interface{})
	if !ok {
		return nil, err
	}
	resultValue["rval"] = rval

	return resultValue, nil
}

// removeJSFunctions removes the JS functions from the VM
func removeJSFunctions(vm *otto.Otto) error {
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

	for _, functionName := range functionsToRemove {
		err := vm.Set(functionName, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// String returns the Plugin as a string
func (p *JSPlugin) String() string {
	return p.script
}
