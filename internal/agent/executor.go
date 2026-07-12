package agent

import (
	"context"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Executor executes parsed tool calls using the tools.Manager.
type Executor struct {
	toolManager *tools.Manager
}

// NewExecutor creates a new Executor.
func NewExecutor(tm *tools.Manager) *Executor {
	return &Executor{
		toolManager: tm,
	}
}

// Execute performs the execution of the specified tool.
func (e *Executor) Execute(ctx context.Context, toolName string, arguments any) (*tools.ToolResult, error) {
	return e.toolManager.Execute(ctx, toolName, arguments)
}

// ExecuteAll executes all tool calls inside the parser.Response sequentially.
// It stops execution on the first tool error or execution failure.
func (e *Executor) ExecuteAll(ctx context.Context, resp *parser.Response, stream EventStream) ([]Observation, error) {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return nil, nil
	}

	var observations []Observation
	for _, tc := range resp.ToolCalls {
		if stream != nil {
			stream.Emit(Event{
				Type:      EventToolStarted,
				Payload:   tc.Name,
				Timestamp: time.Now(),
			})
		}

		res, err := e.Execute(ctx, tc.Name, tc.Arguments)
		obs := NewObservation(tc.Name, res, err)
		observations = append(observations, obs)

		if stream != nil {
			stream.Emit(Event{
				Type:      EventToolFinished,
				Payload:   obs,
				Timestamp: time.Now(),
			})
		}

		// Stop executing further tools if this one failed
		if err != nil || !obs.Success {
			break
		}
	}

	return observations, nil
}
