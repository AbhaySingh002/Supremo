package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestWorkingMemorySaveLoad(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	ctx := context.Background()
	mgr := NewWorkingMemoryManager(store)

	sessionID := "test-session"
	taskID := "task-1"

	memory := &WorkingMemory{
		SessionID: sessionID,
		TaskID:    taskID,
		Objective: "Upgrade storage layer",
		CurrentFocus: &CurrentFocus{
			NextGoal: "verify storage",
		},
	}

	// Save memory
	if err := mgr.Save(ctx, memory); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load memory
	loaded, err := mgr.Load(ctx, sessionID, taskID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.TaskID != taskID || loaded.CurrentFocus == nil || loaded.CurrentFocus.NextGoal != "verify storage" {
		t.Errorf("loaded memory mismatch: %#v", loaded)
	}
}

func TestTruncateObservationBounded(t *testing.T) {
	short := "short test output"
	if out := truncateObservationBounded(short, 100, "hash123"); out != short {
		t.Errorf("expected short output unchanged, got %q", out)
	}

	var long strings.Builder
	for i := 0; i < 500; i++ {
		long.WriteString("line item ")
		long.WriteByte(byte('A' + (i % 26)))
		long.WriteByte('\n')
	}
	longStr := long.String()

	bounded := truncateObservationBounded(longStr, 20, "art-abc")
	if !strings.Contains(bounded, "[... Omitted") {
		t.Errorf("expected omission marker, got: %s", bounded)
	}
	if !strings.Contains(bounded, "art-abc") {
		t.Errorf("expected artifact citation, got: %s", bounded)
	}
}

func TestUpdateFocusAfterTurnStallVsProgress(t *testing.T) {
	wm := &WorkingMemory{SessionID: "s", TaskID: "t"}
	wm.UpdateFocusAfterTurn(&models.TurnProgress{Progress: "read page", NextGoal: "find router", EvidenceUsed: []string{"h1"}}, "read_file", true, "")
	if wm.CurrentFocus == nil || wm.CurrentFocus.EvidenceStatus != "new" || wm.CurrentFocus.Established != "read page" {
		t.Fatalf("progress focus = %#v", wm.CurrentFocus)
	}
	wm.UpdateFocusAfterTurn(&models.TurnProgress{NextGoal: "find router"}, "read_file", false, "")
	if wm.CurrentFocus.EvidenceStatus != "unchanged" || wm.CurrentFocus.PreviousStrategy != "read_file" || wm.CurrentFocus.NextGoal != "find router" {
		t.Fatalf("stall focus = %#v", wm.CurrentFocus)
	}
	wm.UpdateFocusAfterTurn(nil, "verify", false, "tests red")
	if wm.CurrentFocus.LastFailure != "tests red" {
		t.Fatalf("failure focus = %#v", wm.CurrentFocus)
	}
}

func TestAppendBounded(t *testing.T) {
	// Empty string is ignored
	s := appendBounded([]string{"a"}, "", 3)
	if len(s) != 1 || s[0] != "a" {
		t.Fatalf("expected [a], got %v", s)
	}

	// limit <= 0 is no-op
	s = appendBounded([]string{"a"}, "b", 0)
	if len(s) != 1 || s[0] != "a" {
		t.Fatalf("expected [a], got %v", s)
	}

	// Appending within limit
	s = appendBounded([]string{"a"}, "b", 3)
	if len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Fatalf("expected [a, b], got %v", s)
	}

	// Appending at limit rotates oldest out
	s = appendBounded(s, "c", 3) // [a, b, c]
	s = appendBounded(s, "d", 3) // [b, c, d]
	if len(s) != 3 || s[0] != "b" || s[1] != "c" || s[2] != "d" {
		t.Fatalf("expected [b, c, d], got %v", s)
	}
}
