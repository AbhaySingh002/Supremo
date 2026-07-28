package agent

import (
	"context"
	"os"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Parser defines the interface for parsing LLM output into tool calls or final answers.
type Parser interface {
	Parse(response string) (*parser.Response, error)
}

// ContextBuilder defines the interface for constructing conversation context.
type ContextBuilder interface {
	Build(ctx context.Context, session *Session) (*models.Prompt, error)
}

// Memory defines the interface for managing and appending conversation history.
type Memory interface {
	Append(ctx context.Context, sessionID string, msg models.Message) error
	GetWindow(ctx context.Context, sessionID string, messageBudget, toolBudget int) ([]models.Message, error)
	GetSummary(ctx context.Context, sessionID string, budget int) (string, error)
	PersistentContext(budget int) (string, error)
	Clear(ctx context.Context, sessionID string) error
}

// Agent coordinates the execution of the ReAct loop across different subsystems.
type Agent struct {
	provider       providers.Provider
	toolManager    *tools.Manager
	parser         Parser
	contextBuilder ContextBuilder
	memory         Memory
	workspace      string
	debug          bool
	progress       func(ProgressEvent)
}

// SetProgress installs an interactive lifecycle reporter.
func (a *Agent) SetProgress(report func(ProgressEvent)) {
	a.progress = report
	a.toolManager.SetReporter(a.reportTool)
}

// ApprovePendingTool releases one mutating tool call waiting for confirmation.
func (a *Agent) ApprovePendingTool() bool { return a.toolManager.Approve() }

// DenyPendingTool rejects one mutating tool call waiting for confirmation.
func (a *Agent) DenyPendingTool(reason string) bool { return a.toolManager.Deny(reason) }

func (a *Agent) taskContext(ctx context.Context, session *Session) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	ctx = tools.WithWorkspace(ctx, a.workspace)
	ctx = tools.WithDryRun(ctx, session.DryRun)
	if session.ApprovalMode != "" {
		ctx = tools.WithApprovalMode(ctx, session.ApprovalMode)
	}
	return tools.WithToolBudget(ctx, 20), cancel
}

// NewAgent constructs a new Agent instance.
func NewAgent(
	provider providers.Provider,
	toolManager *tools.Manager,
	parser Parser,
	contextBuilder ContextBuilder,
	memory Memory,
) *Agent {
	workspace, _ := os.Getwd()
	return &Agent{
		provider:       provider,
		toolManager:    toolManager,
		parser:         parser,
		contextBuilder: contextBuilder,
		memory:         memory,
		workspace:      workspace,
	}
}

// ClearMemory clears a session without exposing the memory implementation.
func (a *Agent) ClearMemory(ctx context.Context, sessionID string) error {
	return a.memory.Clear(ctx, sessionID)
}

// SetDebug enables or disables debug logging for the agent loop.
func (a *Agent) SetDebug(enabled bool) {
	a.debug = enabled
}

// Debug reports whether diagnostic lifecycle entries are enabled.
func (a *Agent) Debug() bool { return a.debug }

// executeAll runs all tool calls in a parsed response sequentially, stopping on first error.
func (a *Agent) executeAll(ctx context.Context, resp *parser.Response) ([]Observation, error) {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return nil, nil
	}

	var observations []Observation
	for _, tc := range resp.ToolCalls {
		res, err := a.toolManager.Execute(ctx, tc.Name, tc.Arguments)
		obs := NewObservation(tc.Name, res, err)
		observations = append(observations, obs)
		if err != nil || !obs.Success {
			break
		}
	}

	return observations, nil
}
