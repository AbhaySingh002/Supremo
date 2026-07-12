package parser

import "errors"

var (
	// ErrMalformedTool is returned when a tool block has invalid structure.
	ErrMalformedTool = errors.New("malformed tool block")

	// ErrInvalidJSON is returned when JSON decoding fails.
	ErrInvalidJSON = errors.New("invalid JSON in tool block")

	// ErrMissingToolName is returned when the tool name key is absent or empty.
	ErrMissingToolName = errors.New("missing tool name in block")

	// ErrMissingArguments is returned when the arguments key is absent.
	ErrMissingArguments = errors.New("missing arguments in block")
)
