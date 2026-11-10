package agent

import (
	"errors"
	"strings"
)

// ToolExecutionFailure distinguishes a completed transport/protocol exchange
// from a tool that reported failure at the application level (for example an
// MCP CallToolResult with IsError=true). Observation carries bounded,
// sanitized diagnostics for the model, but it must never be recorded as
// successful evidence.
type ToolExecutionFailure struct {
	Code        string
	Observation map[string]any
}

// Error implements error. The code is an internal stable reason code, never
// remote content.
func (e *ToolExecutionFailure) Error() string {
	if e == nil {
		return "tool execution failed"
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		return "tool execution failed"
	}
	return "tool execution failed: " + code
}

// asToolExecutionFailure extracts the typed failure without treating ordinary
// transport, authorization, timeout or cancellation errors as tool-level
// failures.
func asToolExecutionFailure(err error) (*ToolExecutionFailure, bool) {
	var failure *ToolExecutionFailure
	if errors.As(err, &failure) && failure != nil {
		return failure, true
	}
	return nil, false
}
