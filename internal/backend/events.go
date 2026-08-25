package backend

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

type Subscription struct {
	events <-chan api.Event
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

// NewSubscription adapts an event channel for transport and frontend tests.
// Production subscriptions are created by Service.Subscribe.
func NewSubscription(events <-chan api.Event) *Subscription {
	return &Subscription{events: events}
}

func (s *Subscription) Events() <-chan api.Event {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *Subscription) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

func (s *Subscription) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Subscription) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *Service) Subscribe(ctx context.Context, request api.SubscribeRequest) (api.EventStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	durable, err := s.store.SubscribeEvents(ctx, state.EventQuery{SessionID: request.SessionID, After: request.AfterCursor}, 256, 2_000)
	if err != nil {
		cancel()
		if errors.Is(err, state.ErrResyncRequired) {
			return nil, apiError(api.CodeResyncRequired, err.Error(), false)
		}
		return nil, err
	}
	out := make(chan api.Event, 256)
	subscription := &Subscription{events: out, cancel: cancel}
	go func() {
		defer close(out)
		defer durable.Close()
		send := func(event api.Event) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, event := range durable.Initial {
			if !send(apiEvent(event)) {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-durable.Events:
				if !ok {
					if err := durable.Err(); err != nil {
						subscription.setErr(apiError(api.CodeResyncRequired, err.Error(), false))
					}
					return
				}
				if !send(apiEvent(event)) {
					return
				}
			}
		}
	}()
	return subscription, nil
}

func apiEvent(event state.Event) api.Event {
	data := event.Payload
	sessionEvent := event.Type == api.EventSessionCreated || event.Type == api.EventSessionUpdated || event.Type == api.EventSessionArchived
	if sessionEvent {
		var session state.Session
		if json.Unmarshal(event.Payload, &session) == nil {
			data, _ = json.Marshal(api.SessionMetadata{ID: session.ID, Name: session.Name, Status: session.Status, Revision: session.Version, Provider: session.Provider, Model: session.Model, UpdatedAt: session.UpdatedAt})
		}
	}
	var envelope struct {
		Data    json.RawMessage `json:"data"`
		Message struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
		} `json:"message"`
	}
	if json.Unmarshal(event.Payload, &envelope) == nil && len(envelope.Data) > 0 && !sessionEvent {
		data = envelope.Data
	}
	if (event.Type == api.EventUserMessage || event.Type == api.EventAssistantMessage || event.Type == api.EventToolResult) && (len(envelope.Data) == 0 || string(envelope.Data) == "null") {
		if event.Type == api.EventToolResult {
			data, _ = json.Marshal(api.ToolResult{Content: envelope.Message.Content, ToolCallID: envelope.Message.ToolCallID, ToolName: envelope.Message.ToolName})
		} else {
			data, _ = json.Marshal(api.CompletedMessage{Role: envelope.Message.Role, Content: envelope.Message.Content})
		}
	}
	value := api.Event{V: api.Version, Cursor: event.Sequence, EventID: event.ID, Type: event.Type, Durable: true,
		Ignorable: !frontendEvent(event.Type), Time: event.CreatedAt, WorkspaceID: event.WorkspaceID, SessionID: event.SessionID,
		RunID: event.CorrelationID, MessageID: event.CausationID, Data: data}
	var common struct {
		RunID     string `json:"run_id"`
		MessageID string `json:"message_id"`
		Turn      int    `json:"turn"`
		Step      int    `json:"step"`
		CallID    string `json:"call_id"`
	}
	if json.Unmarshal(data, &common) == nil {
		if value.RunID == "" {
			value.RunID = common.RunID
		}
		if value.MessageID == "" {
			value.MessageID = common.MessageID
		}
		value.Turn, value.Step, value.CallID = common.Turn, common.Step, common.CallID
	}
	return value
}

func frontendEvent(kind string) bool {
	switch sessionlog.EventType(kind) {
	case sessionlog.EventUserMessage, sessionlog.EventAssistantMessage, sessionlog.EventToolResult,
		sessionlog.EventTurnStart, sessionlog.EventTurnEnd, sessionlog.EventStepStart, sessionlog.EventStepEnd,
		sessionlog.EventAssistantChunk, sessionlog.EventUsage, sessionlog.EventRetry, sessionlog.EventError, sessionlog.EventFinish,
		sessionlog.EventToolCall, sessionlog.EventTodoWrite, sessionlog.EventPlanMode,
		sessionlog.EventRunQueued, sessionlog.EventRunStart, sessionlog.EventRunEnd,
		sessionlog.EventInteractionRequest, sessionlog.EventInteractionResolve,
		sessionlog.EventSubagentDescriptor, sessionlog.EventSubagentQueued, sessionlog.EventSubagentRunStart, sessionlog.EventSubagentRunEnd:
		return true
	default:
		return kind == api.EventSessionCreated || kind == api.EventSessionUpdated || kind == api.EventSessionArchived ||
			kind == api.EventCheckpointAvailable || kind == api.EventArtifactAvailable
	}
}
