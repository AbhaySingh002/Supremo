package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	status := ToolStatusCompleted
	if !success {
		status = ToolStatusFailed
	}
	return &ToolResult{
		Status:  status,
		Success: success,
		Message: message,
		Data:    data,
	}
}

// BuildSerializedToolResult builds a result from a JSON-serializable output.
func BuildSerializedToolResult(success bool, message string, output any) *ToolResult {
	data, err := SerializeOutput(output)
	if err != nil {
		return BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil)
	}
	return BuildToolResult(success, message, data)
}

const toolPreviewBytes = 12_000

// NormalizeToolResult applies the universal result boundary once, after every
// concrete tool returns. The original data becomes a content-addressed
// lifecycle artifact; the model only receives this bounded envelope.
func NormalizeToolResult(name string, input any, result *ToolResult, executionErr error) (*ToolResult, []byte) {
	if result == nil {
		result = BuildToolResult(false, "no execution result returned", nil)
	}
	if executionErr != nil {
		result.Success, result.Status = false, ToolStatusFailed
		result.Message = executionErr.Error()
		result.Error = &ToolError{Class: classifyToolError(executionErr), Message: executionErr.Error()}
	}
	if result.Status == "" {
		if result.Success {
			result.Status = ToolStatusCompleted
		} else {
			result.Status = ToolStatusFailed
		}
	}
	raw, _ := json.Marshal(result.Data)
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		sum := sha256.Sum256(raw)
		result.ArtifactID = hex.EncodeToString(sum[:])
		preview := raw
		if len(preview) > toolPreviewBytes {
			notice := []byte("\n… output truncated; full output is in artifact " + result.ArtifactID)
			preview = append(append([]byte(nil), preview[:toolPreviewBytes-len(notice)]...), notice...)
		}
		result.Preview = string(preview)
	} else {
		raw = nil
	}
	if result.Preview == "" {
		result.Preview = result.Message
	}
	result.Metadata = map[string]any{"tool": name}
	result.AffectedEntities = observedEntities(input, result.Data)
	if result.Error == nil && !result.Success {
		result.Error = &ToolError{Class: "tool_failed", Message: result.Message}
	}
	if result.Error != nil {
		switch result.Error.Class {
		case "canceled", "temporary", "conflict", "recoverable", ErrorClassToolExecution, "tool_failed":
			result.Retryable = true
		}
	}
	result.Data = nil
	return result, raw
}

func classifyToolError(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, ErrInvalidInput):
		return ErrorClassToolArgument

	case errorClass(err) == ErrorClassPermission:
		return ErrorClassPermission
	case errors.Is(err, ErrToolNotFound):
		return "not_found"
	default:
		return ErrorClassToolExecution
	}
}

func errorClass(err error) string {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Class
	}

	return ""
}

// ClassifyToolOutcome categorizes the execution result or error of a tool into
// standard outcome classes: Success, Recoverable, ApprovalRequired, PermissionBlocked,
// Cancelled, or Fatal.
func ClassifyToolOutcome(result *ToolResult, err error) ToolOutcomeClass {
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return ToolOutcomeCancelled
		case errorClass(err) == ErrorClassPermission:
			return ToolOutcomePermissionBlocked
		case errorClass(err) == ErrorClassProvider:
			return ToolOutcomeFatal
		case strings.Contains(err.Error(), "panicked:"):
			return ToolOutcomeFatal
		case errorClass(err) == ErrorClassToolArgument, errors.Is(err, ErrInvalidInput), errors.Is(err, ErrToolNotFound):
			return ToolOutcomeRecoverable
		default:
			var classified *ClassifiedError
			if errors.As(err, &classified) && classified.Class == ErrorClassToolExecution {
				return ToolOutcomeRecoverable
			}
			return ToolOutcomeFatal
		}
	}
	if result == nil {
		return ToolOutcomeFatal
	}
	if result.Success {
		return ToolOutcomeSuccess
	}
	if result.Status == ToolStatusDenied {
		return ToolOutcomePermissionBlocked
	}
	if result.Error != nil {
		switch result.Error.Class {
		case "canceled":
			return ToolOutcomeCancelled
		case ErrorClassPermission, "permission_denied", "denied":
			return ToolOutcomePermissionBlocked

		case "conflict", "recoverable":
			return ToolOutcomeRecoverable
		default:
			if strings.Contains(result.Error.Message, "panicked:") {
				return ToolOutcomeFatal
			}
			return ToolOutcomeRecoverable
		}
	}
	return ToolOutcomeRecoverable
}

func observedEntities(input any, output map[string]interface{}) []AffectedEntity {
	seen := map[string]bool{}
	entities := []AffectedEntity{}
	var visit func(any, string)
	visit = func(value any, key string) {
		switch value := value.(type) {
		case map[string]any:
			for childKey, child := range value {
				visit(child, strings.ToLower(childKey))
			}
		case []any:
			for _, child := range value {
				visit(child, key)
			}
		case string:
			if (strings.Contains(key, "path") || key == "file" || key == "directory") && !seen[value] {
				seen[value] = true
				entities = append(entities, AffectedEntity{Kind: "file", Path: value})
			}
		}
	}
	visit(jsonAny(input), "")
	visit(jsonAny(output), "")
	return entities
}

func jsonAny(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized any
	if json.Unmarshal(data, &normalized) != nil {
		return value
	}
	return normalized
}

func ValidateAndResolvePath(ctx context.Context, path string) (string, error) {
	root := Workspace(ctx)
	if root == "" {
		return "", fmt.Errorf("workspace is required")
	}
	if path == "" || path == "." {
		return root, nil
	}
	candidate := path
	if candidate == "/workspace" || strings.HasPrefix(candidate, "/workspace/") {
		candidate = filepath.Join(root, strings.TrimPrefix(candidate, "/workspace"))
	}
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
