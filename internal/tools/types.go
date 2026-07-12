package tools

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	// Success indicates whether the tool execution was successful.
	Success bool `json:"success"`
	// Message provides a human-readable message about the execution result.
	Message string `json:"message"`
	// Data contains the output data from the tool execution.
	Data map[string]interface{} `json:"data,omitempty"`
}
