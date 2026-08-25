// Package tools provides interfaces and types for implementing extensible tools.
package tools

import "context"

// CapabilitySet describes the side effects a tool may have. It is deliberately
// declared by the tool, rather than inferred from its name, so restricted
// contexts remain correct when tools are renamed or added.
type CapabilitySet uint8

const (
	CapabilityReadWorkspace CapabilitySet = 1 << iota
	CapabilityWriteWorkspace
	CapabilityExecuteProcess
	CapabilityUseNetwork
	CapabilityChangeGitState
)

// InspectionOnly reports whether this tool can be safely used while a task is
// planning. A tool must explicitly declare a workspace read; an empty or
// side-effect-only declaration is never implicitly safe.
func (c CapabilitySet) InspectionOnly() bool {
	return c&CapabilityReadWorkspace != 0 && c&(CapabilityWriteWorkspace|CapabilityExecuteProcess|CapabilityUseNetwork|CapabilityChangeGitState) == 0
}

// Tool defines the interface that all tools must implement.
// Tools are executable units that perform specific operations.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string
	// Description returns a human-readable description of what this tool does.
	Description() string
	// Schema returns the JSON schema for validating tool input.
	Schema() any
	// Capabilities declares every effect the tool may have.
	Capabilities() CapabilitySet
	// Execute runs the tool with the given input and context.
	Execute(ctx context.Context, input any) (*ToolResult, error)
}
