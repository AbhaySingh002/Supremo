package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// StreamEventType defines the standard semantic event taxonomy for all provider streams.
type StreamEventType string

const (
	StreamEventTextDelta      StreamEventType = "text_delta"
	StreamEventReasoningDelta StreamEventType = "reasoning_delta"
	StreamEventToolCallDelta  StreamEventType = "tool_call_delta"
	StreamEventUsage          StreamEventType = "usage"
	StreamEventFinish         StreamEventType = "finish"
	StreamEventError          StreamEventType = "error"
)

// ToolCallDelta contains incremental or complete fragments of a streaming tool call.
type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// StreamEvent represents a single normalized event emitted by any provider stream adapter.
type StreamEvent struct {
	Type           StreamEventType `json:"type"`
	TextDelta      string          `json:"text_delta,omitempty"`
	ReasoningDelta string          `json:"reasoning_delta,omitempty"`
	ToolCall       *ToolCallDelta  `json:"tool_call,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`
	FinishReason   FinishReason    `json:"finish_reason,omitempty"`
	Err            error           `json:"-"`
}

type rawToolCallAccumulator struct {
	index     int
	id        string
	name      string
	arguments strings.Builder
}

// AssistantAssembler is the canonical assembler that accumulates normalized stream events
// into exactly one valid assistant Completion.
type AssistantAssembler struct {
	activeTools []string
	listener    func(string)

	textBuilder      strings.Builder
	reasoningBuilder strings.Builder
	toolCalls        map[int]*rawToolCallAccumulator
	orderedIndices   []int
	usage            Usage
	finishReason     FinishReason
	err              error
}

// NewAssistantAssembler constructs a new stream assembler.
func NewAssistantAssembler(activeTools []string, listener func(string)) *AssistantAssembler {
	return &AssistantAssembler{
		activeTools: activeTools,
		listener:    listener,
		toolCalls:   make(map[int]*rawToolCallAccumulator),
	}
}

// Feed consumes a normalized stream event.
func (a *AssistantAssembler) Feed(event StreamEvent) error {
	if event.Err != nil {
		a.err = event.Err
		return event.Err
	}
	switch event.Type {
	case StreamEventTextDelta:
		if event.TextDelta != "" {
			a.textBuilder.WriteString(event.TextDelta)
			if a.listener != nil {
				a.listener(event.TextDelta)
			}
		}
	case StreamEventReasoningDelta:
		if event.ReasoningDelta != "" {
			a.reasoningBuilder.WriteString(event.ReasoningDelta)
		}
	case StreamEventToolCallDelta:
		if event.ToolCall != nil {
			tc, ok := a.toolCalls[event.ToolCall.Index]
			if !ok {
				tc = &rawToolCallAccumulator{index: event.ToolCall.Index}
				a.toolCalls[event.ToolCall.Index] = tc
				a.orderedIndices = append(a.orderedIndices, event.ToolCall.Index)
			}
			if event.ToolCall.ID != "" {
				tc.id = event.ToolCall.ID
			}
			if event.ToolCall.Name != "" {
				tc.name = event.ToolCall.Name
			}
			if event.ToolCall.ArgumentsDelta != "" {
				tc.arguments.WriteString(event.ToolCall.ArgumentsDelta)
			}
		}
	case StreamEventUsage:
		if event.Usage != nil {
			a.usage = *event.Usage
		}
	case StreamEventFinish:
		if event.FinishReason != "" {
			a.finishReason = event.FinishReason
		}
	case StreamEventError:
		if event.Err != nil {
			a.err = event.Err
			return event.Err
		}
	}
	return nil
}

// Assemble constructs the canonical Completion after stream termination.
func (a *AssistantAssembler) Assemble() (*Completion, error) {
	if a.err != nil {
		return nil, a.err
	}

	finish := a.finishReason
	if finish == "" {
		if len(a.toolCalls) > 0 {
			finish = FinishToolCalls
		} else {
			finish = FinishStop
		}
	}

	completion := &Completion{
		Text:         a.textBuilder.String(),
		FinishReason: string(finish),
		Usage:        a.usage,
	}

	// Assemble tool calls in emitted order
	for _, idx := range a.orderedIndices {
		raw := a.toolCalls[idx]
		if raw == nil {
			continue
		}
		rawArgs := raw.arguments.String()
		if strings.TrimSpace(rawArgs) == "" {
			rawArgs = "{}"
		}
		argsBytes := []byte(rawArgs)

		// A truncated call may end in the middle of JSON. Do not execute it;
		// complete calls remain raw until the tool runtime parses them.
		if finish == FinishMaxTokens && !isCompleteJSON(argsBytes) {
			continue
		}

		id, synthetic := normalizeToolCallID(raw.id)
		name := canonicalToolName(raw.name, a.activeTools)

		completion.ToolCalls = append(completion.ToolCalls, models.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: json.RawMessage(argsBytes),
			Synthetic: synthetic,
		})
	}

	// Check for genuine empty completion.
	// Hard Invariant: If len(completion.ToolCalls) > 0, it is NOT empty!
	if len(completion.ToolCalls) == 0 && strings.TrimSpace(completion.Text) == "" {
		return nil, &ProviderFailure{
			Code:    FailureEmptyResponse,
			Message: fmt.Sprintf("provider returned empty response (finish_reason=%s)", finish),
		}
	}

	return completion, nil
}

func isCompleteJSON(raw []byte) bool {
	var value any
	return json.Unmarshal(raw, &value) == nil
}

// EmitCompletion makes a non-streaming provider completion travel through the
// same canonical event path as a streamed response. Assembly remains owned by
// the caller (the agent), never by a provider adapter.
func EmitCompletion(completion *Completion, receive func(StreamEvent) error) error {
	if completion == nil {
		return &ProviderFailure{Code: FailureEmptyResponse, Message: "provider returned no completion"}
	}
	emit := func(event StreamEvent) error {
		if receive == nil {
			return nil
		}
		return receive(event)
	}
	if completion.Text != "" {
		if err := emit(StreamEvent{Type: StreamEventTextDelta, TextDelta: completion.Text}); err != nil {
			return err
		}
	}
	for index, call := range completion.ToolCalls {
		if err := emit(StreamEvent{Type: StreamEventToolCallDelta, ToolCall: &ToolCallDelta{
			Index: index, ID: call.ID, Name: call.Name, ArgumentsDelta: string(call.Arguments),
		}}); err != nil {
			return err
		}
	}
	if completion.Usage.InputTokens != 0 || completion.Usage.OutputTokens != 0 || completion.Usage.CostUSD != nil {
		usage := completion.Usage
		if err := emit(StreamEvent{Type: StreamEventUsage, Usage: &usage}); err != nil {
			return err
		}
	}
	return emit(StreamEvent{Type: StreamEventFinish, FinishReason: NormalizeFinishReason(completion.FinishReason)})
}
