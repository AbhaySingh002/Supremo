package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func backendTestStore(t *testing.T) (*state.Store, string) {
	t.Helper()
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	session := agent.Session{ID: "chat", Name: "Chat", CreatedAt: time.Now().UTC(), Status: "active"}
	data, _ := json.Marshal(session)
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: session.ID, Name: session.Name, CreatedAt: session.CreatedAt, Status: session.Status, Data: data}); err != nil {
		t.Fatal(err)
	}
	return store, root
}

func TestSubmitPromptIsDurableAndIdempotentBeforeDispatch(t *testing.T) {
	store, _ := backendTestStore(t)
	service := &Service{store: store, started: true, ctx: context.Background(), workers: map[string]bool{"chat": true}, active: make(map[string]string)}
	request := api.SubmitPromptRequest{SessionID: "chat", Prompt: "inspect the repository", IdempotencyKey: "request-1"}
	first, err := service.SubmitPrompt(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.SubmitPrompt(context.Background(), request)
	if err != nil || again != first {
		t.Fatalf("idempotent receipt = %#v, %v; want %#v", again, err, first)
	}
	records, err := sessionlog.Load(context.Background(), store, "chat")
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for _, record := range records {
		if record.Type == sessionlog.EventRunQueued {
			queued++
		}
	}
	if queued != 1 || first.AcceptedCursor == 0 || first.RunID == "" || first.MessageID == "" {
		t.Fatalf("queued=%d receipt=%#v", queued, first)
	}
	request.Prompt = "different"
	_, err = service.SubmitPrompt(context.Background(), request)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeConflict {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRecoveryCompletesOnlyCorrelatedFinishedTurn(t *testing.T) {
	store, _ := backendTestStore(t)
	service := &Service{store: store, ctx: context.Background(), workers: make(map[string]bool), active: make(map[string]string)}
	meta := sessionlog.EventMeta{CorrelationID: "run-1", CausationID: "message-1"}
	if _, err := service.appendRecord(context.Background(), "chat", sessionlog.EventRunQueued, sessionlog.RunQueuedPayload{
		RunID: "run-1", MessageID: "message-1", Content: "hello", RequestDigest: "digest",
	}, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := service.appendRecord(context.Background(), "chat", sessionlog.EventRunStart, sessionlog.RunStartPayload{RunID: "run-1", MessageID: "message-1"}, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := service.appendRecord(context.Background(), "chat", sessionlog.EventTurnEnd, sessionlog.TurnEndPayload{Turn: 1, Reason: "completed"}, meta); err != nil {
		t.Fatal(err)
	}
	if err := service.recoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, end, err := service.findRun(context.Background(), "chat", "run-1")
	if err != nil || end == nil || end.Status != "completed" || !end.Recovered {
		t.Fatalf("recovered end = %#v, %v", end, err)
	}
}

func TestSessionSubscriptionDeliversIdentifiedAssistantChunksOnly(t *testing.T) {
	store, _ := backendTestStore(t)
	service := &Service{store: store}
	other := agent.Session{ID: "other", Name: "Other", CreatedAt: time.Now().UTC(), Status: "active"}
	data, _ := json.Marshal(other)
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: other.ID, Name: other.Name, CreatedAt: other.CreatedAt, Status: other.Status, Data: data}); err != nil {
		t.Fatal(err)
	}
	appendChunk := func(sessionID, runID, text string) {
		t.Helper()
		_, err := service.appendRecord(context.Background(), sessionID, sessionlog.EventAssistantChunk, sessionlog.AssistantChunkPayload{
			Turn: 1, Step: 2, Attempt: 1, Event: providers.StreamEvent{Type: providers.StreamEventTextDelta, TextDelta: text},
		}, sessionlog.EventMeta{CorrelationID: runID, CausationID: "message-1"})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendChunk("other", "run-other", "ignore")
	appendChunk("chat", "run-chat", "hello")
	stream, err := service.Subscribe(context.Background(), api.SubscribeRequest{SessionID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for event := range stream.Events() {
		if event.Type != api.EventAssistantChunk {
			continue
		}
		if event.SessionID != "chat" || event.RunID != "run-chat" || event.MessageID != "message-1" {
			t.Fatalf("chunk identity = %#v", event)
		}
		var payload api.AssistantChunk
		if err := json.Unmarshal(event.Data, &payload); err != nil || payload.Event.TextDelta != "hello" {
			t.Fatalf("chunk payload = %#v, %v", payload, err)
		}
		return
	}
	t.Fatal("assistant chunk was not delivered")
}

func TestAPIEventProjectsTypedToolResult(t *testing.T) {
	record, err := sessionlog.New(sessionlog.EventToolResult, nil)
	if err != nil {
		t.Fatal(err)
	}
	record.Message = models.Message{Role: models.RoleTool, Content: "contents", ToolCallID: "call-1", ToolName: "read_file"}
	input, err := sessionlog.ToEventInput("chat", record)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		t.Fatal(err)
	}
	event := apiEvent(state.Event{Type: input.Type, Payload: payload})
	var result api.ToolResult
	if err := json.Unmarshal(event.Data, &result); err != nil || result.ToolCallID != "call-1" || result.ToolName != "read_file" || result.Content != "contents" {
		t.Fatalf("tool result event = %#v, %v", result, err)
	}
}

func TestSessionEventsExposeOnlyFrontendMetadata(t *testing.T) {
	payload, err := json.Marshal(state.Session{ID: "chat", Name: "Chat", Status: "active", Version: 4, Data: json.RawMessage(`{"api_key":"secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	event := apiEvent(state.Event{Type: api.EventSessionUpdated, Payload: payload})
	if strings.Contains(string(event.Data), "secret") || strings.Contains(string(event.Data), "api_key") {
		t.Fatalf("session event exposed backend state: %s", event.Data)
	}
	var metadata api.SessionMetadata
	if err := json.Unmarshal(event.Data, &metadata); err != nil || metadata.ID != "chat" || metadata.Revision != 4 {
		t.Fatalf("session metadata = %#v, %v", metadata, err)
	}
}

func TestPublicEndpointRemovesEmbeddedCredentials(t *testing.T) {
	got := publicEndpoint("https://user:password@example.com/v1?api_key=secret&region=us#token")
	if got != "https://example.com/v1?region=us" {
		t.Fatalf("public endpoint = %q", got)
	}
}

func TestInteractionResponseCannotCrossSessions(t *testing.T) {
	store, _ := backendTestStore(t)
	other := agent.Session{ID: "other", Name: "Other", CreatedAt: time.Now().UTC(), Status: "active"}
	data, _ := json.Marshal(other)
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: other.ID, Name: other.Name, CreatedAt: other.CreatedAt, Status: other.Status, Data: data}); err != nil {
		t.Fatal(err)
	}
	service := &Service{store: store, started: true}
	if _, err := service.appendRecord(context.Background(), "chat", sessionlog.EventInteractionRequest, sessionlog.InteractionRequestedPayload{
		InteractionID: "interaction-1", RunID: "run-1", Kind: "approval", Data: json.RawMessage(`{"tool":"write_file","arguments":{}}`),
	}, sessionlog.EventMeta{CorrelationID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	err := service.RespondInteraction(context.Background(), api.RespondInteractionRequest{SessionID: "other", InteractionID: "interaction-1", Decision: "deny"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeNotFound {
		t.Fatalf("cross-session interaction response = %v", err)
	}
}
