package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func openDurableTranscript(t *testing.T) (string, *Session, *DurableMemory) {
	t.Helper()
	root := t.TempDir()
	session := newSession("chat", time.Now())
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	memory, err := NewDurableMemory(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, session, memory
}

func TestDurableTranscriptReadAllDoesNotMutateHistory(t *testing.T) {
	_, session, transcript := openDurableTranscript(t)
	ctx := context.Background()
	for _, message := range []models.Message{
		{Role: models.RoleUser, Content: "first request"},
		{Role: models.RoleTool, Content: strings.Repeat("output ", 200)},
		{Role: models.RoleAssistant, Content: "latest answer"},
	} {
		if err := transcript.Append(ctx, session.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	before, err := transcript.ReadAllTranscript(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := transcript.ReadAllTranscript(ctx, session.ID)
	if err != nil || len(before) != 3 || len(after) != len(before) || after[1].Content != before[1].Content {
		t.Fatalf("transcript changed after read: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestDurableTranscriptToolArtifact(t *testing.T) {
	root, session, transcript := openDurableTranscript(t)
	ctx := context.Background()
	if err := transcript.Append(ctx, session.ID, models.Message{Role: models.RoleTool, Content: strings.Repeat("tool output ", 2_000)}); err != nil {
		t.Fatal(err)
	}
	messages, err := transcript.ReadAllTranscript(ctx, session.ID)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Content, "cite result.artifact_id below as evidence") {
		t.Fatalf("transcript preview = %#v, %v", messages, err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Messages(ctx, session.ID, false)
	if err != nil || len(records) != 1 || records[0].Parts[0].ArtifactID == "" {
		t.Fatalf("artifact message = %#v, %v", records, err)
	}
	artifact, err := store.ReadArtifact(ctx, records[0].Parts[0].ArtifactID)
	if err != nil || !strings.Contains(string(artifact), "tool output") {
		t.Fatalf("artifact = %d bytes, %v", len(artifact), err)
	}
	availability, err := store.Events(ctx, state.EventQuery{SessionID: session.ID, Type: "artifact.created"})
	if err != nil || len(availability) != 1 {
		t.Fatalf("artifact availability events = %#v, %v", availability, err)
	}
}

func TestDurableTranscriptArchiveAndLegacyImport(t *testing.T) {
	root, session, transcript := openDurableTranscript(t)
	ctx := context.Background()
	if err := transcript.Append(ctx, session.ID, models.Message{Role: models.RoleUser, Content: "archive me"}); err != nil {
		t.Fatal(err)
	}
	if err := transcript.Clear(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if messages, err := transcript.ReadAllTranscript(ctx, session.ID); err != nil || len(messages) != 0 {
		t.Fatalf("archived transcript = %#v, %v", messages, err)
	}

	legacy := newSession("legacy", time.Now())
	if err := os.MkdirAll(legacySessionDirectory(root), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacySessionStatePath(root, legacy.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := json.Marshal(legacySessionCheckpoint{SessionID: legacy.ID, Messages: []models.Message{{Role: models.RoleUser, Content: "legacy history"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySessionDirectory(root), legacy.ID+".json"), checkpoint, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".scratchpad", legacy.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scratchpad", legacy.ID, "tool.txt"), []byte("legacy tool output"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(root, legacy.ID); err != nil {
		t.Fatal(err)
	}
	memory, err := NewDurableMemory(root)
	if err != nil {
		t.Fatal(err)
	}
	if messages, err := memory.ReadAllTranscript(ctx, legacy.ID); err != nil || len(messages) != 1 || messages[0].Content != "legacy history" {
		t.Fatalf("legacy transcript = %#v, %v", messages, err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if events, err := store.Events(ctx, state.EventQuery{Type: "legacy.imported"}); err != nil || len(events) < 2 {
		t.Fatalf("legacy ledger = %#v, %v", events, err)
	}
}
