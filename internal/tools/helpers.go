package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxFileBytes     int64 = 1 << 20
	MaxSearchDepth         = 10
	MaxSearchResults       = 1_000
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

func ValidateAndResolvePath(ctx context.Context, path string) (string, error) {
	if path == "" {
		return "", ErrInvalidInput
	}
	root := Workspace(ctx)
	if root == "" {
		return "", fmt.Errorf("workspace is required")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if os.IsNotExist(err) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absPath))
		if parentErr != nil {
			return "", parentErr
		}
		resolvedPath = filepath.Join(parent, filepath.Base(absPath))
	} else if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be inside the workspace")
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
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf", ".zip":
		return true
	}
	return false
}

func ValidateDirectory(ctx context.Context, directory string) (string, error) {
	if directory == "" {
		return "", ErrInvalidInput
	}
	return ValidateAndResolvePath(ctx, directory)
}

// ReadLimitedFile prevents one source file from exhausting tool memory.
func ReadLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

// ReadSearchFile applies the same size and binary policy to every source search.
func ReadSearchFile(path string) ([]byte, error) {
	data, err := ReadLimitedFile(path, MaxFileBytes)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, ErrBinaryFile
	}
	return data, nil
}

func SearchDepth(root, path string) (int, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0, err
	}
	return len(strings.Split(rel, string(filepath.Separator))), nil
}

func SearchDepthLimit(requested int) int {
	if requested <= 0 || requested > MaxSearchDepth {
		return MaxSearchDepth
	}
	return requested
}
