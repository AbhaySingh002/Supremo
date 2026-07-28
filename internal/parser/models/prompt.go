package models

// Prompt represents a compiled LLM prompt containing system instructions and chat history.
type Prompt struct {
	System   string    `json:"system"`
	Messages []Message `json:"messages"`
}
