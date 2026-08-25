package models

// Role represents the sender role in a conversation.
type Role string

const (
	// RoleSystem is durable runtime context. Context builders must lift these
	// messages into a provider's system instruction rather than forwarding them
	// as ordinary chat turns.
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message turn in the conversation.
type Message struct {
	// LocalID is transcript-only identity and is never provider-visible.
	LocalID      string        `json:"-"`
	Role         Role          `json:"role"`
	Content      string        `json:"content"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	ToolName     string        `json:"tool_name,omitempty"`
	TurnProgress *TurnProgress `json:"turn_progress,omitempty"`
	// TaskID is local memory metadata. Provider adapters intentionally ignore
	// it; the context builder uses it to keep terminal task work out of a new
	// general chat prompt.
	TaskID string `json:"task_id,omitempty"`
}
