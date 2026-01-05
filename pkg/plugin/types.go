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

// APIMetadata struct to hold Plugins API metadata (for api_plugin type)
type APIMetadata struct {
	EndPoint string   `json:"api_endpoint" yaml:"api_endpoint"`   // API endpoint for the plugin
	Methods  []string `json:"api_methods" yaml:"api_methods"`     // API method for the plugin
	Auth     string   `json:"api_auth" yaml:"api_auth"`           // API authentication: required, optional, none
	AuthType string   `json:"api_auth_type" yaml:"api_auth_type"` // API authentication type for the plugin: jwt, apikey
}

// JSPlugin struct to hold the JS plugin
type JSPlugin struct {
	Name        string       `json:"name" yaml:"name"`                                     // Name of the plugin
	Description string       `json:"description" yaml:"description"`                       // Description of the plugin
	Version     string       `json:"version" yaml:"version"`                               // Version of the plugin
	PType       string       `json:"type" yaml:"type"`                                     // Type of the plugin
	Async       bool         `json:"async" yaml:"async"`                                   // Is the plugin asynchronous
	Script      string       `json:"script" yaml:"script"`                                 // Script for the plugin
	EventType   string       `json:"event_type" yaml:"event_type"`                         // Event type for the plugin. Plugins can register to handle an event.
	API         *APIMetadata `json:"api_metadata,omitempty" yaml:"api_metadata,omitempty"` // API metadata for api_plugin type
}

// JSPluginRegister struct to hold the JS plugins
type JSPluginRegister struct {
	Registry map[string]JSPlugin // Registry of JS plugins
	Order    []string            // Order of the plugins in registration order
}
