package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func applyAll(t *testing.T, session *Session, events []SessionEvent) {
	t.Helper()
	for _, event := range events {
		if err := session.applyEvent(event); err != nil {
			t.Fatalf("apply seq %d: %v", event.Seq, err)
		}
	}
}

func TestDeriveMessagesBasicAppend(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: "hello"}},
		{Seq: 1, Type: EventAssistantMessage, Message: models.Message{Role: models.RoleAssistant, Content: "hi"}},
		{Seq: 2, Type: EventToolResult, Message: models.Message{Role: models.RoleTool, Content: "ok", ToolCallID: "c1", ToolName: "read_file"}},
	})
	got := session.DeriveMessages()
	want := []models.Message{
		{Role: models.RoleUser, Content: "hello"},
		{Role: models.RoleAssistant, Content: "hi"},
		{Role: models.RoleTool, Content: "ok", ToolCallID: "c1", ToolName: "read_file"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveMessages() = %#v, want %#v", got, want)
	}
}

func TestDeriveMessagesOmitsNonSurfaceEvents(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventTurnStart},
		{Seq: 1, Type: EventUserMessage, Message: models.Message{Content: "do it"}},
		{Seq: 2, Type: EventStepStart},
		{Seq: 3, Type: EventAssistantMessage, Message: models.Message{Content: "calling", ToolCalls: []models.ToolCall{{ID: "a", Name: "read_file"}}}},
		{Seq: 4, Type: EventToolCall, Data: map[string]string{"id": "a"}},
		{Seq: 5, Type: EventToolResult, Message: models.Message{Content: "file", ToolCallID: "a", ToolName: "read_file"}},
		{Seq: 6, Type: EventStepEnd},
	})
	got := session.DeriveMessages()
	if len(got) != 3 {
		t.Fatalf("messages=%d, want 3: %#v", len(got), got)
	}
	if got[0].Role != models.RoleUser || got[1].Role != models.RoleAssistant || got[2].Role != models.RoleTool {
		t.Fatalf("roles = %#v", got)
	}
	if session.Nodes()[0] != 1 || session.Nodes()[1] != 3 || session.Nodes()[2] != 5 {
		t.Fatalf("nodes = %#v", session.Nodes())
	}
}

func TestDeriveMessagesNativeToolCausalPair(t *testing.T) {
	session := &Session{ID: "s"}
	call := models.ToolCall{ID: "call-a", Name: "search_text", Arguments: json.RawMessage(`{"query":"x"}`)}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventAssistantMessage, Message: models.Message{ToolCalls: []models.ToolCall{call}}},
		{Seq: 1, Type: EventToolResult, Message: models.Message{Content: "hit", ToolCallID: "call-a", ToolName: "search_text"}},
	})
	got := session.DeriveMessages()
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "call-a" {
		t.Fatalf("assistant tool calls = %#v", got[0].ToolCalls)
	}
	if got[1].ToolCallID != "call-a" || got[1].Content != "hit" {
		t.Fatalf("tool result = %#v", got[1])
	}
}

func TestDeriveMessagesToolOnlyAssistant(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventAssistantMessage, Message: models.Message{Content: "", ToolCalls: []models.ToolCall{{ID: "1", Name: "list_directory"}}}},
	})
	got := session.DeriveMessages()
	if len(got) != 1 || got[0].Content != "" || len(got[0].ToolCalls) != 1 {
		t.Fatalf("tool-only assistant dropped: %#v", got)
	}
	applyAll(t, session, []SessionEvent{{Seq: 1, Type: EventAssistantMessage, Message: models.Message{Content: ""}}})
	got = session.DeriveMessages()
	if len(got) != 1 {
		t.Fatalf("empty assistant should be omitted: %#v", got)
	}
}

func TestSurfaceReplace(t *testing.T) {
	session := &Session{ID: "s"}
	events := []SessionEvent{{Seq: 0, Type: EventTurnStart}}
	for i := 1; i <= 5; i++ {
		events = append(events, SessionEvent{Seq: int64(i), Type: EventUserMessage, Message: models.Message{Content: string(rune('0' + i))}})
	}
	applyAll(t, session, events)
	if !reflect.DeepEqual(session.Nodes(), []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("nodes = %#v", session.Nodes())
	}
	if err := session.applyEvent(SessionEvent{
		Seq: 6, Type: EventUserMessage, Message: models.Message{Content: "summary"},
		SurfaceOp:       &SurfaceOp{Kind: surfaceOpReplace, StartSeq: 2, EndSeq: 4},
		SourceEventSeqs: []int64{2, 3, 4},
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.Nodes(), []int64{1, 6, 5}) {
		t.Fatalf("after replace nodes = %#v", session.Nodes())
	}
}

func TestSurfaceReplaceRequiresProvenance(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventUserMessage, Message: models.Message{Content: "a"}},
		{Seq: 1, Type: EventUserMessage, Message: models.Message{Content: "b"}},
		{Seq: 2, Type: EventUserMessage, Message: models.Message{Content: "c"}},
	})
	err := session.applyEvent(SessionEvent{
		Seq: 3, Type: EventUserMessage, Message: models.Message{Content: "sum"},
		SurfaceOp:       &SurfaceOp{Kind: surfaceOpReplace, StartSeq: 0, EndSeq: 2},
		SourceEventSeqs: []int64{0, 2},
	})
	if err == nil {
		t.Fatal("expected provenance validation failure")
	}
}

func TestDeriveMessagesCacheTailAppend(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventUserMessage, Message: models.Message{Content: "a"}},
		{Seq: 1, Type: EventAssistantMessage, Message: models.Message{Content: "b"}},
		{Seq: 2, Type: EventToolResult, Message: models.Message{Content: "c", ToolCallID: "1"}},
	})
	_ = session.DeriveMessages()
	if session.derivedThisPass != 3 {
		t.Fatalf("initial derive count = %d", session.derivedThisPass)
	}
	applyAll(t, session, []SessionEvent{{Seq: 3, Type: EventUserMessage, Message: models.Message{Content: "d"}}})
	got := session.DeriveMessages()
	if session.derivedThisPass != 1 {
		t.Fatalf("tail derive count = %d, want 1", session.derivedThisPass)
	}
	if len(got) != 4 || got[3].Content != "d" {
		t.Fatalf("got %#v", got)
	}
}

func TestDeriveMessagesCacheInvalidationOnReplace(t *testing.T) {
	session := &Session{ID: "s"}
	applyAll(t, session, []SessionEvent{
		{Seq: 0, Type: EventUserMessage, Message: models.Message{Content: "1"}},
		{Seq: 1, Type: EventUserMessage, Message: models.Message{Content: "2"}},
		{Seq: 2, Type: EventUserMessage, Message: models.Message{Content: "3"}},
		{Seq: 3, Type: EventUserMessage, Message: models.Message{Content: "4"}},
		{Seq: 4, Type: EventUserMessage, Message: models.Message{Content: "5"}},
	})
	_ = session.DeriveMessages()
	gen := session.surface.Generation()
	if err := session.applyEvent(SessionEvent{
		Seq: 5, Type: EventUserMessage, Message: models.Message{Content: "9"},
		SurfaceOp:       &SurfaceOp{Kind: surfaceOpReplace, StartSeq: 1, EndSeq: 3},
		SourceEventSeqs: []int64{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	if session.surface.Generation() == gen {
		t.Fatal("replaceGeneration did not increment")
	}
	got := session.DeriveMessages()
	if session.derivedThisPass != 3 {
		t.Fatalf("reproject count = %d, want 3", session.derivedThisPass)
	}
	if len(got) != 3 || got[0].Content != "1" || got[1].Content != "9" || got[2].Content != "5" {
		t.Fatalf("got %#v", got)
	}
}

func TestSurfaceReplayMatchesLive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	if _, err := store.SaveSession(ctx, state.SessionInput{ID: "chat", Name: "Chat"}); err != nil {
		t.Fatal(err)
	}
	mem := newDurableMemory(store)
	live := &Session{ID: "chat"}
	messages := []models.Message{
		{Role: models.RoleUser, Content: "hello"},
		{Role: models.RoleAssistant, Content: "", ToolCalls: []models.ToolCall{{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}}},
		{Role: models.RoleTool, Content: "package a", ToolCallID: "c1", ToolName: "read_file"},
	}
	for _, msg := range messages {
		if err := mem.Append(ctx, "chat", msg); err != nil {
			t.Fatal(err)
		}
	}
	// v1 session envelopes used untyped payloads. They remain readable even
	// though every new runtime record is written through the v2 contract.
	prior, err := loadSessionEvents(ctx, store, "chat")
	if err != nil {
		t.Fatal(err)
	}
	legacy := SessionEvent{Seq: prior[len(prior)-1].Seq + 1, Type: EventTurnStart, Data: map[string]string{"why": "audit"}}
	if err := persistSessionEvent(ctx, store, "chat", legacy); err != nil {
		t.Fatal(err)
	}
	if err := live.AttachSurface(ctx, store); err != nil {
		t.Fatal(err)
	}
	liveMsgs := live.DeriveMessages()
	reloaded := &Session{ID: "chat"}
	if err := reloaded.AttachSurface(ctx, store); err != nil {
		t.Fatal(err)
	}
	replayMsgs := reloaded.DeriveMessages()
	if !reflect.DeepEqual(liveMsgs, replayMsgs) {
		t.Fatalf("replay messages %#v, live %#v", replayMsgs, liveMsgs)
	}
	if !reflect.DeepEqual(live.Nodes(), reloaded.Nodes()) {
		t.Fatalf("replay nodes %#v, live %#v", reloaded.Nodes(), live.Nodes())
	}
	if live.events[0].Time.IsZero() {
		t.Fatal("expected event timestamps")
	}
}
