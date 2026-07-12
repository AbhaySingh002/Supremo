// Package tools provides interfaces and types for implementing extensible tools.
package tools

import "context"

// Tool defines the interface that all tools must implement.
// Tools are executable units that perform specific operations.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string
	// Description returns a human-readable description of what this tool does.
	Description() string
	// Schema returns the JSON schema for validating tool input.
	Schema() any
	// Execute runs the tool with the given input and context.
	Execute(ctx context.Context, input any) (*ToolResult, error)
}
