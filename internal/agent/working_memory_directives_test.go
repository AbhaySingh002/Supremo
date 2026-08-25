package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestWorkingMemoryDirectivesApplication(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.Open(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	wm := &WorkingMemory{
		SessionID: "sess-dir",
		TaskID:    "task-dir",
	}

	// 1. Retain
	wm.ApplyDirectives([]models.MemoryDirective{
		{Operation: "retain", Key: "database", Statement: "PostgreSQL 16 with pgx driver", Evidence: []string{"art-123"}},
		{Operation: "retain", Key: "auth_strategy", Statement: "JWT with RS256", Evidence: []string{"art-456"}},
	})

	if len(wm.KnownRepositoryFacts) != 2 {
		t.Fatalf("expected 2 known facts, got %d", len(wm.KnownRepositoryFacts))
	}
	if len(wm.EvidenceArtifactIDs) != 2 {
		t.Fatalf("expected 2 evidence IDs, got %d", len(wm.EvidenceArtifactIDs))
	}

	// 2. Supersede
	wm.ApplyDirectives([]models.MemoryDirective{
		{Operation: "supersede", Key: "auth_strategy", Statement: "JWT with Ed25519"},
	})

	if len(wm.KnownRepositoryFacts) != 2 {
		t.Fatalf("expected 2 known facts after supersede, got %d", len(wm.KnownRepositoryFacts))
	}
	found := false
	for _, f := range wm.KnownRepositoryFacts {
		if strings.Contains(f, "Ed25519") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("superseded statement not found: %#v", wm.KnownRepositoryFacts)
	}

	// 3. Release
	wm.ApplyDirectives([]models.MemoryDirective{
		{Operation: "release", Key: "database"},
	})

	if len(wm.KnownRepositoryFacts) != 1 {
		t.Fatalf("expected 1 fact after release, got %d: %#v", len(wm.KnownRepositoryFacts), wm.KnownRepositoryFacts)
	}
	if strings.Contains(wm.KnownRepositoryFacts[0], "database") {
		t.Fatalf("released key still present: %#v", wm.KnownRepositoryFacts)
	}

	// 4. Verify save and reload through WorkingMemoryManager
	mgr := NewWorkingMemoryManager(store)
	if err := mgr.Save(context.Background(), wm); err != nil {
		t.Fatal(err)
	}

	loaded, err := mgr.Load(context.Background(), "sess-dir", "task-dir")
	if err != nil || loaded == nil {
		t.Fatalf("failed to load working memory: %v", err)
	}
	if len(loaded.KnownRepositoryFacts) != 1 || !strings.Contains(loaded.KnownRepositoryFacts[0], "Ed25519") {
		t.Fatalf("reloaded working memory mismatch: %#v", loaded)
	}
}
