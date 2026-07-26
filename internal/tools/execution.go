package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

type contextKey string

const (
	workspaceKey contextKey = "workspace"
	dryRunKey    contextKey = "dry-run"
	budgetKey    contextKey = "tool-budget"
)

type budget struct {
	mu        sync.Mutex
	remaining int
}

func WithWorkspace(ctx context.Context, root string) context.Context {
	return context.WithValue(ctx, workspaceKey, filepath.Clean(root))
}

func Workspace(ctx context.Context) string {
	root, _ := ctx.Value(workspaceKey).(string)
	return root
}

func WithDryRun(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, dryRunKey, enabled)
}

func IsDryRun(ctx context.Context) bool {
	enabled, _ := ctx.Value(dryRunKey).(bool)
	return enabled
}

func WithToolBudget(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, budgetKey, &budget{remaining: limit})
}

func consumeToolBudget(ctx context.Context) error {
	b, _ := ctx.Value(budgetKey).(*budget)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining == 0 {
		return fmt.Errorf("tool call budget exhausted")
	}
	b.remaining--
	return nil
}

type approvalRequest struct {
	decision chan error
}

// RequiresApproval reports whether a tool can change the workspace or run arbitrary code.
func RequiresApproval(name string) bool {
	switch name {
	case "create_directory", "create_file", "delete_file", "rename_file", "write_file", "run_formatter", "execute_command":
		return true
	}
	return false
}

// Activity is one recent tool execution, retained only for the interactive session.
type Activity struct {
	Time    time.Time
	Tool    string
	Status  string
	Message string
}

func renderToolCall(name string, input any) string {
	data, err := json.Marshal(input)
	if err != nil {
		return name
	}
	return fmt.Sprintf("%s %s", name, data)
}
