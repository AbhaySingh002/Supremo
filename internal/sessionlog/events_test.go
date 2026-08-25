package sessionlog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: "session", Name: "Session"}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAppendWritesV2AndLegacyV1RemainsReadable(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	legacy := Record{Seq: 0, Type: EventTurnStart, Data: map[string]any{"turn": 1}}
	if _, err := Append(ctx, store, "session", legacy); err != nil {
		t.Fatal(err)
	}
	message, err := New(EventUserMessage, nil)
	if err != nil {
		t.Fatal(err)
	}
	message.Message = models.Message{Role: models.RoleUser, Content: "hello"}
	message.SurfaceOp = &SurfaceOp{Kind: SurfaceAppend}
	stored, err := Append(ctx, store, "session", message)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != CurrentVersion || stored.Seq == 0 {
		t.Fatalf("stored v2 event = %#v", stored)
	}

	events, err := Load(ctx, store, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Version != 1 || events[0].Seq != 0 || events[1].Version != CurrentVersion {
		t.Fatalf("replayed events = %#v", events)
	}
	raw, err := store.Events(ctx, state.EventQuery{SessionID: "session", Type: string(EventUserMessage)})
	if err != nil || len(raw) != 1 || raw[0].PayloadVersion != CurrentVersion {
		t.Fatalf("v2 durable envelope = %#v, err=%v", raw, err)
	}
}

func TestV2CompatibilityRejectsUnknownRequiredAndSkipsIgnorable(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	appendRaw := func(ignorable bool) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"type": "future/event", "version": CurrentVersion, "ignorable": ignorable, "data": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvent(ctx, state.EventInput{SessionID: "session", Type: "future/event", PayloadVersion: CurrentVersion, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	appendRaw(false)
	if _, err := Load(ctx, store, "session"); err == nil || !strings.Contains(err.Error(), "unknown required event") {
		t.Fatalf("unknown required v2 event error = %v", err)
	}

	store = openStore(t)
	appendRaw = func(ignorable bool) {
		payload, err := json.Marshal(map[string]any{
			"type": "future/event", "version": CurrentVersion, "ignorable": ignorable, "data": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvent(ctx, state.EventInput{SessionID: "session", Type: "future/event", PayloadVersion: CurrentVersion, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	appendRaw(true)
	events, err := Load(ctx, store, "session")
	if err != nil || len(events) != 0 {
		t.Fatalf("ignorable event replay = %#v, err=%v", events, err)
	}
}

func TestLegacyUnknownEventRemainsIgnored(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	payload := json.RawMessage(`{"seq":0,"type":"legacy/workspace","data":{"value":true}}`)
	if _, err := store.AppendEvent(ctx, state.EventInput{SessionID: "session", Type: "legacy/workspace", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	events, err := Load(ctx, store, "session")
	if err != nil || len(events) != 0 {
		t.Fatalf("legacy non-session event replay = %#v, err=%v", events, err)
	}
}

func TestReplayProjectsRuntimeState(t *testing.T) {
	user, _ := New(EventUserMessage, nil)
	user.Seq, user.Message, user.SurfaceOp = 1, models.Message{Role: models.RoleUser, Content: "inspect"}, &SurfaceOp{Kind: SurfaceAppend}
	start, _ := New(EventTurnStart, TurnStartPayload{Turn: 3})
	start.Seq = 2
	step, _ := New(EventStepStart, StepStartPayload{Turn: 3, Step: 2})
	step.Seq = 3
	assistant, _ := New(EventAssistantMessage, nil)
	assistant.Seq = 4
	assistant.Message = models.Message{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{{
		ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`),
	}}}
	assistant.SurfaceOp = &SurfaceOp{Kind: SurfaceAppend}
	dispatched, _ := New(EventToolCall, ToolCallPayload{Turn: 3, Step: 2, CallID: "call-1", Tool: "read_file", Arguments: `{"path":"README.md"}`})
	dispatched.Seq = 5
	plan, _ := New(EventPlanMode, PlanModePayload{Active: true})
	plan.Seq = 6
	todos, _ := New(EventTodoWrite, TodoWritePayload{Todos: []TodoItem{{Content: "Read README", Status: "in_progress"}}})
	todos.Seq = 7

	projection, err := Replay([]Record{user, start, step, assistant, dispatched, plan, todos})
	if err != nil {
		t.Fatal(err)
	}
	if projection.ActiveTurn != 3 || projection.ActiveStep != 2 || !projection.PlanModeActive {
		t.Fatalf("runtime projection = %#v", projection)
	}
	if len(projection.Surface.Nodes()) != 2 || len(projection.PendingToolCalls) != 1 || projection.PendingToolCalls[0].ID != "call-1" {
		t.Fatalf("surface/pending projection = %#v", projection)
	}
	if len(projection.DispatchedToolCallIDs) != 1 || projection.DispatchedToolCallIDs[0] != "call-1" || len(projection.Todos) != 1 {
		t.Fatalf("tool/todo projection = %#v", projection)
	}
}

func TestNewRejectsUntypedPayload(t *testing.T) {
	if _, err := New(EventTurnStart, map[string]any{"turn": 1}); err == nil {
		t.Fatal("untyped v2 payload was accepted")
	}
}

func TestSubagentEventsRoundTripAsTypedNonSurfaceRecords(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	inputs := []struct {
		kind EventType
		data any
	}{
		{EventSubagentDescriptor, SubagentDescriptorPayload{Version: 1, ParentSessionID: "parent", Label: "inspect", Depth: 1, Scope: "local_read", Provider: "mock", Model: "m1", ApprovalMode: "batman", DryRun: true}},
		{EventSubagentQueued, SubagentQueuedPayload{MessageID: "message", SenderSessionID: "parent", Content: "inspect the repository"}},
		{EventSubagentRunStart, SubagentRunStartPayload{RunID: "run", MessageID: "message"}},
		{EventSubagentRunEnd, SubagentRunEndPayload{RunID: "run", MessageID: "message", Status: "completed", Output: "done"}},
	}
	for _, input := range inputs {
		record, err := New(input.kind, input.data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Append(ctx, store, "session", record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := Load(ctx, store, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(inputs) || records[0].SurfaceOp != nil || IsSurfaceType(records[0].Type) {
		t.Fatalf("subagent records = %#v", records)
	}
	descriptor, ok := records[0].Data.(SubagentDescriptorPayload)
	if !ok || descriptor.ApprovalMode != "batman" || !descriptor.DryRun {
		t.Fatalf("descriptor payload = %#v", records[0].Data)
	}
}

func TestV2PayloadMismatchFailsSafely(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	payload := json.RawMessage(`{"type":"turn/start","version":2,"ignorable":false,"data":{"active":true}}`)
	if _, err := store.AppendEvent(ctx, state.EventInput{SessionID: "session", Type: string(EventTurnStart), PayloadVersion: CurrentVersion, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, store, "session"); err == nil || !strings.Contains(err.Error(), "decode turn/start") {
		t.Fatalf("v2 payload mismatch error = %v", err)
	}
}

func TestRepairTailClosesInterruptedCompaction(t *testing.T) {
	start := Record{Seq: 10, Type: EventCompactionStart, Data: CompactionPayload{CompactionID: "c1", StartSeq: 1, EndSeq: 4}}
	extra := RepairTail([]Record{start})
	if len(extra) != 1 || extra[0].Type != EventCompactionEnd {
		t.Fatalf("interrupted compaction repair = %#v", extra)
	}
	payload, ok := extra[0].Data.(CompactionPayload)
	if !ok || payload.Status != "failed" || !strings.Contains(payload.Error, "interrupted") {
		t.Fatalf("interrupted compaction payload = %#v", extra[0].Data)
	}

	replacement := Record{Seq: 11, Type: EventUserMessage, SurfaceOp: &SurfaceOp{Kind: SurfaceReplace, StartSeq: 1, EndSeq: 4}, Message: models.Message{Role: models.RoleUser, Content: "summary"}}
	extra = RepairTail([]Record{start, replacement})
	if len(extra) != 1 {
		t.Fatalf("recovered compaction repair = %#v", extra)
	}
	payload, ok = extra[0].Data.(CompactionPayload)
	if !ok || payload.Status != "recovered-completed" || payload.Error != "" {
		t.Fatalf("recovered compaction payload = %#v", extra[0].Data)
	}
}
