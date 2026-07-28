package parser

import (
	"encoding/json"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// jsonToolCall helper matches the raw JSON format of tool execution requests.
type jsonToolCall struct {
	Tool      *string `json:"tool"`
	Arguments any     `json:"arguments"`
}

// ParseToolBlock parses a raw tool block string into a models.ToolCall.
func ParseToolBlock(rawBlock string) (models.ToolCall, error) {
	if rawBlock == "" {
		return models.ToolCall{}, ErrMalformedTool
	}

	var jt jsonToolCall
	if err := json.Unmarshal([]byte(rawBlock), &jt); err != nil {
		return models.ToolCall{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	if jt.Tool == nil || *jt.Tool == "" {
		return models.ToolCall{}, ErrMissingToolName
	}

	if jt.Arguments == nil {
		return models.ToolCall{}, ErrMissingArguments
	}

	return models.ToolCall{
		Name:      *jt.Tool,
		Arguments: jt.Arguments,
	}, nil
}
