// Package main (events) implements the CROWler Events Handler engine.
package main

// HealthCheck is a struct that holds the health status of the application.
type HealthCheck struct {
	Status string `json:"status"`
}

// ReadyCheck is a struct that holds the readiness status of the application.
type ReadyCheck struct {
	Status string `json:"status"`
}

// PluginResponse is a struct that holds the response from a plugin.
type PluginResponse struct {
	Success     bool        `json:"success"`
	Message     string      `json:"message"`
	APIResponse interface{} `json:"apiResponse,omitempty"` // Use `interface{}` to allow flexibility in the API response structure
}
