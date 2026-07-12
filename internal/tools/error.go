package tools

import "errors"

// Common errors that can be returned by tool operations.
var (
	// ErrToolNotFound is returned when a requested tool does not exist in the registry.
	ErrToolNotFound = errors.New("tool not found")
	// ErrInvalidInput is returned when the input provided to a tool fails validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrPermission is returned when a tool execution is denied due to insufficient permissions.
	ErrPermission = errors.New("permission denied")
)
