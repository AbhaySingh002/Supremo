package state

import (
	"encoding/json"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// ToModelMessage converts a durable state.Message to the canonical models.Message.
// It unpacks text, assistant tool calls, tool results, and turn progress metadata.
func (m Message) ToModelMessage() models.Message {
	var text strings.Builder
	msg := models.Message{
		Role:   models.Role(m.Role),
		TaskID: m.TaskID,
	}

	for _, part := range m.Parts {
		switch part.Kind {
		case "assistant_tool_call":
			var call models.ToolCall
			if err := json.Unmarshal(part.Metadata, &call); err == nil {
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
		case "tool_result":
			var meta struct {
				ToolCallID string `json:"tool_call_id"`
				ToolName   string `json:"tool_name"`
			}
			_ = json.Unmarshal(part.Metadata, &meta)
			msg.ToolCallID = meta.ToolCallID
			msg.ToolName = meta.ToolName
			text.WriteString(part.Text)
		case "turn_progress":
			var progress models.TurnProgress
			if err := json.Unmarshal(part.Metadata, &progress); err == nil {
				msg.TurnProgress = &progress
			}
		default:
			text.WriteString(part.Text)
		}
	}

	msg.Content = text.String()
	return msg
}

// ToModelMessages converts a slice of durable state.Message to models.Message.
func ToModelMessages(messages []Message) []models.Message {
	result := make([]models.Message, 0, len(messages))
	for _, m := range messages {
		result = append(result, m.ToModelMessage())
	}
	return result
}
