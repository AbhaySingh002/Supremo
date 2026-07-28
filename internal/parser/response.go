package parser

import "github.com/AbhaySingh002/supremo/internal/parser/models"

// Response represents the parsed structured output from the LLM.
type Response struct {
	Thought     string            `json:"thought,omitempty"`
	ToolCalls   []models.ToolCall `json:"tool_calls,omitempty"`
	FinalAnswer string            `json:"final_answer,omitempty"`
}
