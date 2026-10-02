package components

import (
	"strings"

	"github.com/AbhaySingh002/supremo/internal/api"
)

// Todos renders a standing TODO list as indented bullet lines.
func Todos(items []api.TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		symbol := "○"
		switch item.Status {
		case "completed":
			symbol = "✓"
		case "in_progress":
			symbol = "●"
		}
		lines = append(lines, "  "+symbol+" "+strings.TrimSpace(item.Content))
	}
	return strings.Join(lines, "\n")
}

// ParseTodos extracts todo items from a tool JSON payload.
func ParseTodos(raw string) []api.TodoItem {
	values, ok := decodeObject(raw)
	if !ok {
		return nil
	}
	return ParseTodosFromObject(values)
}

// ParseTodosFromObject extracts todo items directly from a decoded object map.
func ParseTodosFromObject(values map[string]any) []api.TodoItem {
	list, ok := values["todos"].([]any)
	if !ok {
		return nil
	}
	out := make([]api.TodoItem, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content, _ := row["content"].(string)
		status, _ := row["status"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, api.TodoItem{Content: content, Status: status})
	}
	return out
}
