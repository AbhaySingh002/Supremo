package git_tools

import (
	"encoding/json"
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ParseInput parses input into the target struct using JSON marshaling.
func ParseInput(input any, target any) error {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return tools.ErrInvalidInput
	}
	if err := json.Unmarshal(inputBytes, target); err != nil {
		return tools.ErrInvalidInput
	}
	return nil
}

// ValidateDirectory validates that a directory is not empty.
func ValidateDirectory(directory string) error {
	if directory == "" {
		return tools.ErrInvalidInput
	}
	return nil
}

// IsGitRepository checks if a directory is a git repository.
func IsGitRepository(directory string) error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = directory
	if err := cmd.Run(); err != nil {
		return tools.ErrToolNotFound
	}
	return nil
}

// SerializeOutput converts a struct to a map[string]interface{} for ToolResult.
func SerializeOutput(output any) (map[string]interface{}, error) {
	outputMap, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}

	var dataMap map[string]interface{}
	if err := json.Unmarshal(outputMap, &dataMap); err != nil {
		return nil, err
	}

	return dataMap, nil
}

// BuildToolResult creates a ToolResult with the given parameters.
func BuildToolResult(success bool, message string, data map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		Success: success,
		Message: message,
		Data:    data,
	}
}
