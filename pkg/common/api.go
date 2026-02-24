// Package common package is used to store common functions and variables
package common

import (
	"sync"
)

// APIRoute represents the structure of an API route, including its path, supported methods, description, and other metadata.
type APIRoute struct {
	Path        string   `json:"path"`
	Methods     []string `json:"methods"`
	Description string   `json:"description"`
	RequiresQ   bool     `json:"requires_q"`
	ConsoleOnly bool     `json:"console_only"`
	Plugin      bool     `json:"plugin"`
}

var apiRegistry []APIRoute
var apiRegistryMutex sync.Mutex

// RegisterRoute registers a new API route with the given parameters.
func RegisterRoute(path string, methods []string, description string, requiresQ bool, consoleOnly bool, plugin bool) {
	apiRegistryMutex.Lock()
	defer apiRegistryMutex.Unlock()

	apiRegistry = append(apiRegistry, APIRoute{
		Path:        path,
		Methods:     methods,
		Description: description,
		RequiresQ:   requiresQ,
		ConsoleOnly: consoleOnly,
		Plugin:      plugin,
	})
}

// GetAPIRoutes returns the list of registered API routes.
func GetAPIRoutes() []APIRoute {
	apiRegistryMutex.Lock()
	defer apiRegistryMutex.Unlock()

	return apiRegistry
}
