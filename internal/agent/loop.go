package agent

import (
	"context"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// Run executes the core ReAct loop orchestrating LLM queries and tool execution.
func (a *Agent) Run(
	ctx context.Context,
	session *Session,
	userInput string,
) (string, error) {

	state := &State{
		CurrentIteration: 0,
		MaxIterations:    15, // Default maximum loop execution steps
		Finished:         false,
	}

	// Append user input to memory
	if err := a.memory.Append(ctx, session.ID, models.Message{
		Role:    models.RoleUser,
		Content: userInput,
	}); err != nil {
		state.LastError = err
		return "", fmt.Errorf("failed to append user message to memory: %w", err)
	}

	for !state.Finished && state.CurrentIteration < state.MaxIterations {
		select {
		case <-ctx.Done():
			state.LastError = ctx.Err()
			return "", ctx.Err()
		default:
		}

		state.CurrentIteration++

		// 1. Build prompt context using ContextBuilder
		prompt, err := a.contextBuilder.Build(ctx, session, userInput, state)
		if err != nil {
			state.LastError = err
			return "", fmt.Errorf("failed to build context: %w", err)
		}

		if a.debug {
			fmt.Printf("\n==================================================\n")
			fmt.Printf("[DEBUG] Iteration: %d\n", state.CurrentIteration)
			fmt.Printf("==================================================\n")
			fmt.Printf("[DEBUG] SYSTEM PROMPT (%d chars):\n%s\n", len(prompt.System), prompt.System)
			for i, msg := range prompt.Messages {
				fmt.Printf("[DEBUG] MSG[%d] (%s, %d chars):\n%s\n", i, msg.Role, len(msg.Content), msg.Content)
			}
		}

		// 2. Provider Call
		completion, err := a.provider.Chat(ctx, prompt)
		if err != nil {
			state.LastError = err
			if a.debug {
				fmt.Printf("[DEBUG] Provider error (will retry): %v\n", err)
			}
			continue // Retry on next iteration instead of aborting
		}

		response := completion.Raw

		if a.debug {
			fmt.Printf("[DEBUG] RAW RESPONSE (finish_reason=%s):\n%s\n", completion.FinishReason, response)
		}

		// Append assistant response to memory
		if err := a.memory.Append(ctx, session.ID, models.Message{
			Role:    models.RoleAssistant,
			Content: response,
		}); err != nil {
			state.LastError = err
			return "", fmt.Errorf("failed to append assistant message to memory: %w", err)
		}

		// 4. Parse response
		parsed, err := a.parser.Parse(response)
		if err != nil {
			state.LastError = err
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if a.debug {
			fmt.Printf("[DEBUG] PARSED: thought=%q, tool_calls=%d, final_answer=%q\n",
				parsed.Thought, len(parsed.ToolCalls), parsed.FinalAnswer)
			for i, tc := range parsed.ToolCalls {
				fmt.Printf("[DEBUG] TOOL_CALL[%d]: %s args=%+v\n", i, tc.Name, tc.Arguments)
			}
		}

		// 4. Execute Tool Calls if present
		if len(parsed.ToolCalls) > 0 {
			observations, execErr := a.executeAll(ctx, parsed)
			if execErr != nil {
				state.LastError = execErr
				return "", execErr
			}

			for i, obs := range observations {
				if a.debug {
					fmt.Printf("[DEBUG] OBSERVATION[%d] (%s):\n%s\n", i, obs.ToolName, obs.Output)
				}

				// Append observation to memory
				if err := a.memory.Append(ctx, session.ID, models.Message{
					Role:    models.RoleTool,
					Content: obs.Output,
				}); err != nil {
					state.LastError = err
					return "", fmt.Errorf("failed to append tool observation to memory: %w", err)
				}
			}
			continue
		}

		// 5. Handle Final Response
		if parsed.FinalAnswer != "" {
			state.Finished = true

			return parsed.FinalAnswer, nil
		}

		// Fallback if provider response didn't produce tools or final answer
		return "", fmt.Errorf("provider response did not request tool or provide final answer")
	}

	if !state.Finished && state.CurrentIteration >= state.MaxIterations {
		return "", fmt.Errorf("agent reached max iterations limit (%d)", state.MaxIterations)
	}

	return "", nil
}
