package components

import (
	"strings"

	"charm.land/lipgloss/v2/tree"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// Todos renders a standing TODO list as a lipgloss tree.
func Todos(items []api.TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	root := tree.Root("Todos")
	for _, item := range items {
		mark := "[ ]"
		switch item.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		}
		root.Child(mark + " " + strings.TrimSpace(item.Content))
	}
	return root.String()
}

// ParseTodos extracts todo items from a tool JSON payload.
func ParseTodos(raw string) []api.TodoItem {
	values, ok := decodeObject(raw)
	if !ok {
		return nil
	}
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

// Checklist renders a turn checklist as a bubbles table.
func Checklist(list *models.TaskChecklist) string {
	if list == nil || len(list.Steps) == 0 {
		return ""
	}
	rows := make([][]string, 0, len(list.Steps))
	for _, step := range list.Steps {
		rows = append(rows, []string{step.Status, step.Label})
	}
	title := strings.TrimSpace(list.Title)
	if title == "" {
		title = "Checklist"
	}
	return title + "\n" + renderTable([]string{"Status", "Step"}, rows)
}
