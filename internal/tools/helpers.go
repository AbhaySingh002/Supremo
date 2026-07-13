package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func ParseInput(input any, target any) error {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return ErrInvalidInput
	}
	if err := json.Unmarshal(inputBytes, target); err != nil {
		return ErrInvalidInput
	}
	return nil
}

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

func BuildToolResult(success bool, message string, data map[string]interface{}) *ToolResult {
	return &ToolResult{
		Success: success,
		Message: message,
		Data:    data,
	}
}

func ValidateAndResolvePath(path string) (string, error) {
	if path == "" {
		return "", ErrInvalidInput
	}
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

func PathExists(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrToolNotFound
		}
		return nil, err
	}
	return info, nil
}

func IsHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

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

func ValidateDirectory(directory string) error {
	if directory == "" {
		return ErrInvalidInput
	}
	return nil
}
