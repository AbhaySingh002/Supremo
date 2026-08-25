package observation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestInspectionReuseRoundTrip(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PutArtifact(context.Background(), state.ArtifactInput{Data: []byte(`{"content":"hello"}`), ContentType: "application/json", Origin: "tool:list_directory"})
	if err != nil {
		t.Fatal(err)
	}
	call := models.ToolCall{Name: "list_directory", Arguments: json.RawMessage(`{"path":"."}`)}
	fp, cArgs, path, scope := state.ComputeCallFingerprint(call.Name, call.Arguments, root)
	if _, err := store.SaveObservation(context.Background(), state.Observation{
		SessionID: "s1", Tool: call.Name, CallFingerprint: fp, CanonicalArgs: cArgs, Path: path, Scope: scope,
		ArtifactID: artifact.Hash, Summary: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	cap := New(root)
	before, err := cap.BeforeTool(runtime.BeforeToolEvent{
		Context:    tools.WithWorkspace(context.Background(), root),
		SessionID:  "s1",
		Call:       call,
		Descriptor: tools.ToolDescriptor{Name: call.Name, Inspection: true},
	})
	if err != nil || before.Result == nil || !before.Reused {
		t.Fatalf("expected reuse %#v err=%v", before, err)
	}
}

func TestNonInspectionDoesNotReuse(t *testing.T) {
	cap := New(t.TempDir())
	before, err := cap.BeforeTool(runtime.BeforeToolEvent{
		Call:       models.ToolCall{Name: "write_file"},
		Descriptor: tools.ToolDescriptor{Name: "write_file", Inspection: false},
	})
	if err != nil || before.Result != nil {
		t.Fatalf("write tools must not reuse %#v err=%v", before, err)
	}
}
