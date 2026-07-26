package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type contextKey string

const (
	workspaceKey    contextKey = "workspace"
	dryRunKey       contextKey = "dry-run"
	approvalModeKey contextKey = "approval-mode"
	budgetKey       contextKey = "tool-budget"
)

// ApprovalMode controls how aggressively tool execution pauses for confirmation.
type ApprovalMode string

const (
	ApprovalStrict   ApprovalMode = "strict"
	ApprovalBatman   ApprovalMode = "batman"
	ApprovalSuperman ApprovalMode = "superman"
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

func WithApprovalMode(ctx context.Context, mode ApprovalMode) context.Context {
	return context.WithValue(ctx, approvalModeKey, NormalizeApprovalMode(mode))
}

func ApprovalModeFromContext(ctx context.Context) ApprovalMode {
	mode, ok := ctx.Value(approvalModeKey).(ApprovalMode)
	if !ok {
		return ""
	}
	return NormalizeApprovalMode(mode)
}

func NormalizeApprovalMode(mode ApprovalMode) ApprovalMode {
	switch mode {
	case ApprovalBatman, ApprovalSuperman:
		return mode
	default:
		return ApprovalStrict
	}
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
	decision  chan error
	tool      string
	arguments string
}

// RequiresApproval reports whether a tool can change the workspace or run arbitrary code.
func RequiresApproval(name string) bool {
	switch name {
	case "create_directory", "create_file", "delete_file", "rename_file", "write_file", "run_formatter", "execute_command":
		return true
	}
	return false
}

// RequiresApprovalFor applies the selected session policy to one tool invocation.
func RequiresApprovalFor(ctx context.Context, name string, input any) bool {
	switch ApprovalModeFromContext(ctx) {
	case ApprovalSuperman:
		return false
	case ApprovalBatman:
		return batmanRequiresApproval(name, input)
	case ApprovalStrict:
		return true
	default:
		return RequiresApproval(name)
	}
}

func batmanRequiresApproval(name string, input any) bool {
	switch name {
	case "delete_file":
		return true
	case "create_file", "write_file", "rename_file":
		return dependencyManifest(inputValue(input, "path"))
	case "execute_command":
		command, args := inputValue(input, "command"), inputValues(input, "args")
		return riskyCommand(command, args) || !batmanSafeCommand(command, args)
	default:
		return false
	}
}

func dependencyManifest(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "requirements.txt", "pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock", "composer.json", "composer.lock", "gemfile", "gemfile.lock":
		return true
	default:
		return false
	}
}

func riskyCommand(command string, args []string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return true
	}
	joined := strings.ToLower(strings.Join(args, " "))
	switch command {
	case "rm", "rmdir", "sudo", "dd", "mkfs", "chmod", "chown", "kill", "shutdown", "reboot", "curl", "wget", "ssh", "scp", "docker", "kubectl", "terraform":
		return true
	case "sh", "bash", "zsh", "fish", "powershell":
		return true
	case "go":
		return startsWithWord(joined, "get") || startsWithWord(joined, "install") || startsWithWord(joined, "mod") || startsWithWord(joined, "run")
	case "npm", "yarn", "pnpm", "bun":
		return startsWithWord(joined, "install") || startsWithWord(joined, "i") || startsWithWord(joined, "add") || startsWithWord(joined, "remove") || startsWithWord(joined, "uninstall") || startsWithWord(joined, "update") || startsWithWord(joined, "publish") || startsWithWord(joined, "run")
	case "pip", "pip3", "poetry", "uv", "cargo", "composer", "bundle", "gem":
		return true
	case "git":
		return startsWithWord(joined, "reset") || startsWithWord(joined, "clean") || startsWithWord(joined, "restore") || startsWithWord(joined, "checkout") || startsWithWord(joined, "switch") || startsWithWord(joined, "rebase") || startsWithWord(joined, "commit") || startsWithWord(joined, "push")
	}
	return false
}

// batmanSafeCommand is deliberately small: Batman mode runs known inspection
// and verification commands, while an unfamiliar executable still asks first.
func batmanSafeCommand(command string, args []string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	joined := strings.ToLower(strings.Join(args, " "))
	switch command {
	case "pwd", "ls", "rg", "grep", "find", "cat", "head", "tail", "echo", "printf":
		return true
	case "git":
		return startsWithWord(joined, "status") || startsWithWord(joined, "diff") || startsWithWord(joined, "log") || startsWithWord(joined, "branch") || startsWithWord(joined, "show")
	case "go":
		return startsWithWord(joined, "test") || startsWithWord(joined, "build") || startsWithWord(joined, "vet") || startsWithWord(joined, "fmt") || startsWithWord(joined, "list") || startsWithWord(joined, "env") || startsWithWord(joined, "version")
	}
	return false
}

func startsWithWord(value, word string) bool {
	return value == word || strings.HasPrefix(value, word+" ")
}

func inputValue(input any, key string) string {
	values := inputValues(input, key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func inputValues(input any, key string) []string {
	data, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var values map[string]any
	if json.Unmarshal(data, &values) != nil {
		return nil
	}
	value, ok := values[key]
	if !ok {
		return nil
	}
	if text, ok := value.(string); ok {
		return []string{text}
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

// Activity is one recent tool execution, retained only for the interactive session.
type Activity struct {
	Time    time.Time
	Tool    string
	Status  string
	Message string
}

// Event is a UI-facing tool lifecycle notification.
type Event struct {
	Time      time.Time
	Tool      string
	Status    string
	Message   string
	Arguments string
	Output    string
}

func renderToolCall(name string, input any) string {
	data, err := json.Marshal(input)
	if err != nil {
		return name
	}
	return fmt.Sprintf("%s %s", name, data)
}
