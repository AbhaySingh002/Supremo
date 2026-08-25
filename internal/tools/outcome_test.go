package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyToolOutcome(t *testing.T) {
	tests := []struct {
		name     string
		result   *ToolResult
		err      error
		expected ToolOutcomeClass
	}{
		{
			name:     "successful tool result",
			result:   &ToolResult{Success: true, Status: ToolStatusCompleted},
			err:      nil,
			expected: ToolOutcomeSuccess,
		},
		{
			name:     "recoverable failed tool result",
			result:   &ToolResult{Success: false, Status: ToolStatusFailed, Message: "file not found"},
			err:      nil,
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "recoverable failed build result",
			result:   &ToolResult{Success: false, Status: ToolStatusFailed, Message: "build failed", Error: &ToolError{Class: "tool_failed", Message: "exit code 1"}},
			err:      nil,
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "recoverable invalid arguments",
			result:   &ToolResult{Success: false, Status: ToolStatusFailed, Error: &ToolError{Class: ErrorClassToolArgument, Message: "missing required field"}},
			err:      nil,
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "recoverable classified error",
			result:   nil,
			err:      classify(ErrorClassToolExecution, errors.New("command execution failed")),
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "recoverable invalid input error",
			result:   nil,
			err:      ErrInvalidInput,
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "permission blocked from error",
			result:   nil,
			err:      classify(ErrorClassPermission, errors.New("not allowed in read-only mode")),
			expected: ToolOutcomePermissionBlocked,
		},
		{
			name:     "permission blocked from result status",
			result:   &ToolResult{Success: false, Status: ToolStatusDenied, Error: &ToolError{Class: ErrorClassPermission, Message: "approval denied"}},
			err:      nil,
			expected: ToolOutcomePermissionBlocked,
		},
		{
			name:     "cancelled context error",
			result:   nil,
			err:      context.Canceled,
			expected: ToolOutcomeCancelled,
		},
		{
			name:     "checkpoint corruption fatal error",
			result:   nil,
			err:      classify(ErrorClassCheckpoint, errors.New("checkpoint restore failed")),
			expected: ToolOutcomeFatal,
		},
		{
			name:     "internal panic fatal error",
			result:   nil,
			err:      fmt.Errorf("tool %q panicked: nil pointer dereference", "read_file"),
			expected: ToolOutcomeFatal,
		},
		{
			name:     "file conflict result is recoverable",
			result:   &ToolResult{Success: false, Status: ToolStatusFailed, Error: &ToolError{Class: "conflict", Message: "stale file"}},
			err:      nil,
			expected: ToolOutcomeRecoverable,
		},
		{
			name:     "internal panic in result error message",
			result:   &ToolResult{Success: false, Status: ToolStatusFailed, Error: &ToolError{Class: "panic", Message: "tool panicked: out of bounds"}},
			err:      nil,
			expected: ToolOutcomeFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyToolOutcome(tt.result, tt.err)
			if got != tt.expected {
				t.Fatalf("ClassifyToolOutcome() = %v (%s), expected %v (%s)", got, got, tt.expected, tt.expected)
			}
		})
	}
}

func TestBuildSerializedToolResult(t *testing.T) {
	result := BuildSerializedToolResult(true, "done", struct {
		Value string `json:"value"`
	}{Value: "ok"})
	if !result.Success || result.Message != "done" || result.Data["value"] != "ok" {
		t.Fatalf("serialized success = %#v", result)
	}

	result = BuildSerializedToolResult(true, "done", map[string]any{"invalid": make(chan int)})
	if result.Success || result.Data != nil || !strings.Contains(result.Message, "Failed to serialize output") {
		t.Fatalf("serialization failure = %#v", result)
	}
}

func TestUnifiedTextDiffMakesWorkspaceChangesVisible(t *testing.T) {
	diff := unifiedTextDiff("web/index.html", "web/index.html", []byte("<h1>Old</h1>\n"), []byte("<h1>New</h1>\n"), true, true)
	for _, want := range []string{"--- a/web/index.html", "+++ b/web/index.html", "-<h1>Old</h1>", "+<h1>New</h1>"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
}
