package tools

import "errors"

// Common errors that can be returned by tool operations.
var (
	// ErrToolNotFound is returned when a requested tool does not exist in the registry.
	ErrToolNotFound = errors.New("tool not found")
	// ErrInvalidInput is returned when the input provided to a tool fails validation.
	ErrInvalidInput = errors.New("invalid input")
	ErrFileTooLarge = errors.New("file exceeds size limit")
	ErrBinaryFile   = errors.New("binary file")
	ErrSearchLimit  = errors.New("search result limit reached")
)
