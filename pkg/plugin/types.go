// Package plugin provides the plugin functionality for the CROWler.
package plugin

// JSPlugin struct to hold the JS plugin
type JSPlugin struct {
	Name        string `json:"name" yaml:"name"`               // Name of the plugin
	Description string `json:"description" yaml:"description"` // Description of the plugin
	PType       string `json:"type" yaml:"type"`               // Type of the plugin
	Script      string `json:"script" yaml:"script"`           // Script for the plugin
	EventType   string `json:"event_type" yaml:"event_type"`   // Event type for the plugin. Plugins can register to handle an event.
}

// JSPluginRegister struct to hold the JS plugins
type JSPluginRegister struct {
	Registry map[string]JSPlugin
}
