package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

// Run executes the core ReAct loop orchestrating LLM queries and tool execution.
func (a *Agent) Run(
	ctx context.Context,
	session *Session,
	userInput string,
) (string, error) {
	ctx, cancel := a.taskContext(ctx, session)
	defer cancel()
	if session.PlanMode {
		return a.runPlanMode(ctx, session, userInput)
	}

	// Append user input to memory
	if err := a.memory.Append(ctx, session.ID, models.Message{
		Role:    models.RoleUser,
		Content: userInput,
	}); err != nil {
		return "", fmt.Errorf("failed to append user message to memory: %w", err)
	}

	const maxIterations = 15
	for iteration := 1; iteration <= maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		a.emit(ProgressEvent{Kind: ProgressIteration, Iteration: iteration})

		// 1. Build prompt context using ContextBuilder
		prompt, err := a.contextBuilder.Build(ctx, session)
		if err != nil {
			return "", fmt.Errorf("failed to build context: %w", err)
		}

		if a.debug {
			a.emit(ProgressEvent{Kind: ProgressDebug, Message: fmt.Sprintf("Iteration %d system prompt (%d chars):\n%s", iteration, len(prompt.System), prompt.System)})
			for i, msg := range prompt.Messages {
				a.emit(ProgressEvent{Kind: ProgressDebug, Message: fmt.Sprintf("Message %d (%s, %d chars):\n%s", i, msg.Role, len(msg.Content), msg.Content)})
			}
		}

		// 2. Provider Call
		completion, err := a.complete(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("provider request: %w", err)
		}
		if completion == nil {
			return "", fmt.Errorf("provider returned no completion")
		}

		response := completion.Raw

		if a.debug {
			a.emit(ProgressEvent{Kind: ProgressDebug, Message: fmt.Sprintf("Raw response (%s):\n%s", completion.FinishReason, response)})
		}

		// Append assistant response to memory
		if err := a.memory.Append(ctx, session.ID, models.Message{
			Role:    models.RoleAssistant,
			Content: response,
		}); err != nil {
			return "", fmt.Errorf("failed to append assistant message to memory: %w", err)
		}

		// 4. Parse response
		parsed, err := a.parser.Parse(response)
		if err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if a.debug {
			a.emit(ProgressEvent{Kind: ProgressDebug, Message: fmt.Sprintf("Parsed response: thought=%q, tool calls=%d, final answer=%q", parsed.Thought, len(parsed.ToolCalls), parsed.FinalAnswer)})
		}

		// 4. Execute Tool Calls if present
		if len(parsed.ToolCalls) > 0 {
			observations, execErr := a.executeAll(ctx, parsed)
			if execErr != nil {
				return "", execErr
			}

			for i, obs := range observations {
				if a.debug {
					a.emit(ProgressEvent{Kind: ProgressDebug, Message: fmt.Sprintf("Observation %d (%s):\n%s", i, obs.ToolName, obs.Output)})
				}

				// Append observation to memory
				if err := a.memory.Append(ctx, session.ID, models.Message{
					Role:    models.RoleTool,
					Content: obs.Output,
				}); err != nil {
					return "", fmt.Errorf("failed to append tool observation to memory: %w", err)
				}
			}
			continue
		}

		// 5. Handle Final Response
		if parsed.FinalAnswer != "" {
			return parsed.FinalAnswer, nil
		}

		// Fallback if provider response didn't produce tools or final answer
		return "", fmt.Errorf("provider response did not request tool or provide final answer")
	}

	return "", fmt.Errorf("agent reached max iterations limit (%d)", maxIterations)
}

func (a *Agent) complete(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	streamer, ok := a.provider.(providers.StreamProvider)
	if !ok {
		return a.provider.Chat(ctx, prompt)
	}
	var raw strings.Builder
	lastVisible := ""
	return streamer.Stream(ctx, prompt, func(chunk string) {
		raw.WriteString(chunk)
		response := raw.String()
		if !strings.Contains(response, "<final_answer>") {
			return
		}
		_, _, visible := parser.ExtractToolBlocks(response)
		if visible != "" && visible != lastVisible {
			lastVisible = visible
			a.emit(ProgressEvent{Kind: ProgressStream, Message: visible})
		}
	})
}
