package agent

import (
	"encoding/json"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Observation represents the result of a tool execution formatted for LLM context.
type Observation struct {
	ToolName string `json:"tool_name"`
	Success  bool   `json:"success"`
	Output   string `json:"output"`
}

// NewObservation converts a ToolResult or an execution error into a standard Observation.
func NewObservation(toolName string, result *tools.ToolResult, err error) Observation {
	if err != nil {
		return buildObservation(toolName, false, "", nil, err)
	}
	if result == nil {
		return buildObservation(toolName, false, "no execution result returned from tool", nil, nil)
	}
	return buildObservation(toolName, result.Success, result.Message, result.Data, nil)
}

func buildObservation(toolName string, success bool, message string, data map[string]interface{}, err error) Observation {
	payload := struct {
		Tool    string                 `json:"tool"`
		Success bool                   `json:"success"`
		Message string                 `json:"message"`
		Data    map[string]interface{} `json:"data,omitempty"`
		Error   string                 `json:"error,omitempty"`
	}{
		Tool:    toolName,
		Success: success,
		Message: message,
		Data:    data,
	}
	if err != nil {
		payload.Error = err.Error()
	}

	out, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		// Fallback to plain text if JSON marshal fails
		return Observation{
			ToolName: toolName,
			Success:  success,
			Output:   fmt.Sprintf("Tool: %s\nSuccess: %v\nMessage: %s", toolName, success, message),
		}
	}

	return Observation{
		ToolName: toolName,
		Success:  success,
		Output:   string(out),
	}
}
