package agent

import "strings"

// TodoStatus is the lifecycle state of one model-managed TODO item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem is a single task item authored explicitly by the model.
type TodoItem struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// CurrentTodos returns the active standing TODO checklist for the session.
func (s *Session) CurrentTodos() []TodoItem {
	if s == nil {
		return nil
	}
	todos := make([]TodoItem, len(s.replay.Todos))
	for i, todo := range s.replay.Todos {
		todos[i] = TodoItem{Content: todo.Content, Status: TodoStatus(todo.Status)}
	}
	return cloneTodos(todos)
}

func cloneTodos(todos []TodoItem) []TodoItem {
	if todos == nil {
		return nil
	}
	out := make([]TodoItem, len(todos))
	for i, item := range todos {
		out[i] = TodoItem{
			Content: strings.TrimSpace(item.Content),
			Status:  item.Status,
		}
	}
	return out
}
