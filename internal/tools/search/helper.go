package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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

// ValidateAndResolvePath validates and resolves an absolute path.
func ValidateAndResolvePath(path string) (string, error) {
	if path == "" {
		return "", tools.ErrInvalidInput
	}

	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

// PathExists checks if a path exists and returns the file info.
func PathExists(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tools.ErrToolNotFound
		}
		return nil, err
	}
	return info, nil
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

// IsHidden checks if a file/directory name is hidden (starts with . on Unix).
func IsHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// ShouldSkipFile checks if a file should be skipped during operations.
// Skips hidden files and common binary files.
func ShouldSkipFile(path string) bool {
	baseName := filepath.Base(path)
	if IsHidden(baseName) {
		return true
	}

	ext := strings.ToLower(filepath.Ext(path))
	skipExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".bin": true, ".png": true, ".jpg": true, ".jpeg": true,
		".gif": true, ".ico": true, ".pdf": true, ".zip": true,
	}
	return skipExts[ext]
}
