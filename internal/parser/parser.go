package parser

import "github.com/AbhaySingh002/supremo/internal/models"

// Parser defines the interface for parsing raw LLM outputs.
type Parser interface {
	Parse(raw string) (*Response, error)
}

// DefaultParser implements the Parser interface.
type DefaultParser struct{}

// NewParser creates a new DefaultParser instance.
func NewParser() *DefaultParser {
	return &DefaultParser{}
}

// Parse converts a raw LLM output string into a structured Response containing
// reasoning, ordered tool calls, and the final answer.
func (p *DefaultParser) Parse(raw string) (*Response, error) {
	thought, blocks, finalAnswer := ExtractToolBlocks(raw)

	var toolCalls []models.ToolCall
	for _, block := range blocks {
		tc, err := ParseToolBlock(block)
		if err != nil {
			return nil, err
		}
		toolCalls = append(toolCalls, tc)
	}

	return &Response{
		Thought:     thought,
		ToolCalls:   toolCalls,
		FinalAnswer: finalAnswer,
	}, nil
}
