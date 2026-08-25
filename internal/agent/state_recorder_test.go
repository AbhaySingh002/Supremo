package agent

import (
	"context"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestStateRecorderStoresRawToolOutputAndReturnsEnrichment(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	raw := []byte(`{"content":"complete output"}`)
	recorder := stateRecorder{store: store, root: root, sessionID: "chat"}
	enrichment := recorder.RecordToolLifecycle(context.Background(), tools.Lifecycle{Tool: "read_file", Status: "completed", Input: map[string]any{"path": "missing.txt"}, Result: &tools.ToolResult{Success: true}, RawOutput: raw})
	if enrichment.ArtifactID == "" || enrichment.WorldRevision == "" {
		t.Fatalf("enrichment = %#v", enrichment)
	}
	stored, err := store.ReadArtifact(context.Background(), enrichment.ArtifactID)
	if err != nil || string(stored) != string(raw) {
		t.Fatalf("raw artifact = %q, %v", stored, err)
	}
}

func TestStateRecorderPublishesCheckpointAvailability(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	recorder := stateRecorder{store: store, root: root, sessionID: "chat"}
	recorder.RecordToolLifecycle(context.Background(), tools.Lifecycle{Tool: "write_file", Status: "checkpoint", Checkpoint: &tools.CheckpointSummary{ID: "checkpoint-1", Files: 1}})
	events, err := store.Events(context.Background(), state.EventQuery{SessionID: "chat", Type: "checkpoint.available"})
	if err != nil || len(events) != 1 {
		t.Fatalf("checkpoint events = %#v, %v", events, err)
	}
}
