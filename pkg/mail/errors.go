package mail

import (
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies failures without exposing provider-specific or secret
// details to callers.
type ErrorKind string

const (
	// ErrorTransient identifies a temporary provider or network failure.
	ErrorTransient ErrorKind = "transient"
	// ErrorAuthentication identifies rejected or expired credentials.
	ErrorAuthentication ErrorKind = "authentication"
	// ErrorPermission identifies a permanent authorization failure.
	ErrorPermission ErrorKind = "permission"
	// ErrorConfiguration identifies invalid or unsupported source settings.
	ErrorConfiguration ErrorKind = "configuration"
	// ErrorMalformed identifies unsafe or unrecoverable message content.
	ErrorMalformed ErrorKind = "malformed"
	// ErrorPolicy identifies content rejected by configured resource limits.
	ErrorPolicy ErrorKind = "policy"
	// ErrorCheckpointReset identifies provider state that requires full
	// reconciliation instead of an incremental retry.
	ErrorCheckpointReset ErrorKind = "checkpoint_reset"
)

// Error is a provider-neutral mail failure. Message must be safe to expose in
// logs and status records; Cause is available for internal inspection only.
type Error struct {
	Kind       ErrorKind
	Operation  string
	Message    string
	RetryAfter time.Duration
	Cause      error
}

// Error returns a redacted, provider-neutral description of the failure.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Operation == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Operation
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

// Unwrap exposes the internal cause to errors.Is and errors.As without adding
// it to the redacted Error string.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Retryable reports whether err represents a transient failure.
func Retryable(err error) bool {
	var mailErr *Error
	return errors.As(err, &mailErr) && mailErr.Kind == ErrorTransient
}
