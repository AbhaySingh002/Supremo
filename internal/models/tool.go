package models

// ToolCall represents a parsed request to execute a tool.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}
