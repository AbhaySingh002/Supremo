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
	workspaceKey     contextKey = "workspace"
	dryRunKey        contextKey = "dry-run"
	approvalModeKey  contextKey = "approval-mode"
	readOnlyKey      contextKey = "read-only"
	checkpointKey    contextKey = "checkpoint"
	activeToolsKey   contextKey = "active-tools"
	researchOnlyKey  contextKey = "research-only"
	progressScopeKey contextKey = "progress-scope"
	delegatedKey     contextKey = "delegated"
)

// ProgressScope identifies the user-visible unit responsible for a tool
// event. It is presentation metadata only; permissions and execution still
// come exclusively from the request context.
type ProgressScope struct {
	SessionID string
	TaskID    string
	RunID     string
	MessageID string
	CallID    string
}

func WithProgressScope(ctx context.Context, scope ProgressScope) context.Context {
	return context.WithValue(ctx, progressScopeKey, scope)
}

func ProgressScopeFromContext(ctx context.Context) ProgressScope {
	scope, _ := ctx.Value(progressScopeKey).(ProgressScope)
	return scope
}

// ApprovalMode controls how aggressively tool execution pauses for confirmation.
type ApprovalMode string

const (
	ApprovalStrict   ApprovalMode = "strict"
	ApprovalBatman   ApprovalMode = "batman"
	ApprovalSuperman ApprovalMode = "superman"
)

// approvalPolicy is shared by an interactive task and its descendants. A
// pointer in the request context lets the TUI promote a pending task to
// auto-approve without racing the next tool call.
type approvalPolicy struct {
	mu   sync.RWMutex
	mode ApprovalMode
}

func (p *approvalPolicy) get() ApprovalMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *approvalPolicy) set(mode ApprovalMode) {
	p.mu.Lock()
	p.mode = NormalizeApprovalMode(mode)
	p.mu.Unlock()
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

// WithReadOnly restricts a tool context to inspection-only operations.
func WithReadOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, readOnlyKey, true)
}

func IsReadOnly(ctx context.Context) bool {
	readOnly, _ := ctx.Value(readOnlyKey).(bool)
	return readOnly
}

// WithDelegated marks execution as owned by a child agent. The marker is
// enforcement metadata and cannot grant capabilities.
func WithDelegated(ctx context.Context) context.Context {
	return context.WithValue(ctx, delegatedKey, true)
}

func IsDelegated(ctx context.Context) bool {
	delegated, _ := ctx.Value(delegatedKey).(bool)
	return delegated
}

// WithResearchOnly enables the local blueprinting boundary. Manager permits
// only declared local inspection descriptors and rejects process, network,
// workspace-changing, and destructive tools.
func WithResearchOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, researchOnlyKey, true)
}

func IsResearchOnly(ctx context.Context) bool {
	enabled, _ := ctx.Value(researchOnlyKey).(bool)
	return enabled
}

// WithActiveTools installs the prompt-scoped execution allowlist. An absent
// value preserves direct, user-approved plan execution; a present list blocks
// model-fabricated names before validation, approval, or side effects.
func WithActiveTools(ctx context.Context, names []string) context.Context {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	return context.WithValue(ctx, activeToolsKey, allowed)
}

func ToolIsActive(ctx context.Context, name string) bool {
	allowed, scoped := ctx.Value(activeToolsKey).(map[string]bool)
	return !scoped || allowed[name]
}

func WithApprovalMode(ctx context.Context, mode ApprovalMode) context.Context {
	if policy, ok := ctx.Value(approvalModeKey).(*approvalPolicy); ok {
		policy.set(mode)
		return ctx
	}
	return context.WithValue(ctx, approvalModeKey, &approvalPolicy{mode: NormalizeApprovalMode(mode)})
}

// WithDetachedApprovalMode gives an isolated worker its own approval policy.
// Interactive descendants normally share their parent's policy so a user can
// change an active task from the TUI. Background workers must not share it:
// their internal auto-approval for a restricted tool set must never weaken the
// parent task's policy.
func WithDetachedApprovalMode(ctx context.Context, mode ApprovalMode) context.Context {
	return context.WithValue(ctx, approvalModeKey, &approvalPolicy{mode: NormalizeApprovalMode(mode)})
}

func ApprovalModeFromContext(ctx context.Context) ApprovalMode {
	policy, ok := ctx.Value(approvalModeKey).(*approvalPolicy)
	if !ok {
		return ""
	}
	return policy.get()
}

// SetApprovalMode updates a policy previously installed with WithApprovalMode.
// It returns false for ordinary contexts so background callers cannot silently
// acquire an approval policy.
func SetApprovalMode(ctx context.Context, mode ApprovalMode) bool {
	policy, ok := ctx.Value(approvalModeKey).(*approvalPolicy)
	if !ok {
		return false
	}
	policy.set(mode)
	return true
}

func NormalizeApprovalMode(mode ApprovalMode) ApprovalMode {
	switch mode {
	case ApprovalBatman, ApprovalSuperman:
		return mode
	default:
		return ApprovalStrict
	}
}

type approvalRequest struct {
	id        string
	decision  chan approvalDecision
	tool      string
	arguments string
	input     any
	scope     ProgressScope
}

type approvalDecision struct {
	err     error
	input   any
	revised bool
}

// RequiresApprovalFor applies the selected session policy to one tool invocation.
func RequiresApprovalFor(ctx context.Context, desc ToolDescriptor, input any) bool {
	return RequiresApprovalInMode(ApprovalModeFromContext(ctx), desc, input)
}

func RequiresApprovalInMode(mode ApprovalMode, desc ToolDescriptor, input any) bool {
	switch NormalizeApprovalMode(mode) {
	case ApprovalSuperman:
		return false
	case ApprovalBatman:
		return batmanRequiresApproval(desc, input)
	default:
		return desc.RequiresApproval
	}
}

func ApprovalPolicyLabel(mode ApprovalMode, desc ToolDescriptor) string {
	if NormalizeApprovalMode(mode) == ApprovalBatman && desc.BatmanManifest {
		return "dependency files ask"
	}
	if RequiresApprovalInMode(mode, desc, nil) {
		return "approval required"
	}
	return "runs automatically"
}

func batmanRequiresApproval(desc ToolDescriptor, input any) bool {
	if !desc.RequiresApproval {
		return false
	}
	if desc.BatmanManifest {
		return dependencyManifest(inputValue(input, "path"))
	}
	return true
}

func dependencyManifest(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "requirements.txt", "pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock", "composer.json", "composer.lock", "gemfile", "gemfile.lock":
		return true
	default:
		return false
	}
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
	Time       time.Time
	SessionID  string
	TaskID     string
	Tool       string
	Status     string
	Message    string
	Arguments  string
	Output     string
	Diff       string
	Checkpoint *CheckpointSummary
}

func renderToolCall(name string, input any) string {
	data, err := json.Marshal(input)
	if err != nil {
		return name
	}
	return fmt.Sprintf("%s %s", name, data)
}
