package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

// TodoWrite replaces the current model-managed TODO checklist.
type TodoWrite struct {
	AllowParallelInProgress bool
}

func (*TodoWrite) Name() string { return "todo_write" }

func (*TodoWrite) Description() string {
	return "Replaces the current TODO checklist with an authoritative new list. Send the ENTIRE task list every time; there are no partial updates."
}

func (*TodoWrite) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "The complete replacement list of TODO items.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"content", "status"},
					"properties": map[string]any{
						"content": map[string]any{
							"type":        "string",
							"description": "Description of the task",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"pending", "in_progress", "completed"},
							"description": "Current status of the task",
						},
					},
				},
			},
		},
		"required":             []string{"todos"},
		"additionalProperties": false,
	}
}

func (*TodoWrite) Capabilities() CapabilitySet { return CapabilityWriteWorkspace }

func (t *TodoWrite) Execute(ctx context.Context, input any) (*ToolResult, error) {
	var request struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}

	if err := ParseInput(input, &request); err != nil {
		return nil, err
	}

	var validatedTodos []sessionlog.TodoItem
	seen := make(map[string]bool, len(request.Todos))
	pendingCount, inProgressCount, completedCount := 0, 0, 0

	for _, item := range request.Todos {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return BuildToolResult(false, "Invalid todo list: task content cannot be empty or whitespace only.", nil), nil
		}

		status := item.Status
		if status != "pending" && status != "in_progress" && status != "completed" {
			return BuildToolResult(false, fmt.Sprintf("Invalid todo list: invalid status %q (must be pending, in_progress, or completed).", status), nil), nil
		}

		if seen[content] {
			return BuildToolResult(false, fmt.Sprintf("Invalid todo list: duplicate task content %q.", content), nil), nil
		}
		seen[content] = true

		switch status {
		case "pending":
			pendingCount++
		case "in_progress":
			inProgressCount++
		case "completed":
			completedCount++
		}

		validatedTodos = append(validatedTodos, sessionlog.TodoItem{Content: content, Status: status})
	}

	if !t.AllowParallelInProgress && inProgressCount > 1 {
		return BuildToolResult(false, "Invalid todo list: at most one task may be in_progress.", nil), nil
	}

	scope := ProgressScopeFromContext(ctx)
	sessionID := scope.SessionID
	if sessionID == "" {
		return BuildToolResult(false, "session is required for todo_write", nil), nil
	}

	root := Workspace(ctx)
	if root == "" {
		return BuildToolResult(false, "workspace is required for todo_write", nil), nil
	}

	store, err := state.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}

	event, err := sessionlog.New(sessionlog.EventTodoWrite, sessionlog.TodoWritePayload{Todos: validatedTodos})
	if err != nil {
		return nil, fmt.Errorf("create todo event: %w", err)
	}
	_, err = sessionlog.Append(ctx, store, sessionID, event)
	if err != nil {
		return BuildToolResult(false, "failed to persist todo/write: "+err.Error(), nil), nil
	}

	msg := fmt.Sprintf("Updated todo list: %d pending, %d in progress, %d completed.", pendingCount, inProgressCount, completedCount)
	return BuildToolResult(true, msg, map[string]any{
		"todos": validatedTodos,
		"counts": map[string]int{
			"pending":     pendingCount,
			"in_progress": inProgressCount,
			"completed":   completedCount,
		},
	}), nil
}
