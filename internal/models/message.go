package models

// Role represents the sender role in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message turn in the conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}
