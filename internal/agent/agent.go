package agent

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Provider defines the interface for communicating with LLM providers.
type Provider interface {
	Chat(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error)
}

// Parser defines the interface for parsing LLM output into tool calls or final answers.
type Parser interface {
	Parse(response string) (*parser.Response, error)
}

// ContextBuilder defines the interface for constructing conversation context.
type ContextBuilder interface {
	Build(
		ctx context.Context,
		session *Session,
		userInput string,
		state *State,
	) (*models.Prompt, error)
}

// Memory defines the interface for managing and appending conversation history.
type Memory interface {
	Append(ctx context.Context, sessionID string, msg models.Message) error
	Get(ctx context.Context, sessionID string) ([]models.Message, error)
	GetWindow(ctx context.Context, sessionID string, messageBudget, toolBudget int) ([]models.Message, error)
	GetSummary(ctx context.Context, sessionID string, budget int) (string, error)
	PersistentContext(budget int) (string, error)
	Clear(ctx context.Context, sessionID string) error
}

// Agent coordinates the execution of the ReAct loop across different subsystems.
type Agent struct {
	provider       Provider
	toolManager    *tools.Manager
	parser         Parser
	contextBuilder ContextBuilder
	memory         Memory
	debug          bool
}

// NewAgent constructs a new Agent instance.
func NewAgent(
	provider Provider,
	toolManager *tools.Manager,
	parser Parser,
	contextBuilder ContextBuilder,
	memory Memory,
) *Agent {
	return &Agent{
		provider:       provider,
		toolManager:    toolManager,
		parser:         parser,
		contextBuilder: contextBuilder,
		memory:         memory,
	}
}

// Memory returns the memory manager of the agent.
func (a *Agent) Memory() Memory {
	return a.memory
}

// SetDebug enables or disables debug logging for the agent loop.
func (a *Agent) SetDebug(enabled bool) {
	a.debug = enabled
}

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
