package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractObservationSummary_PopulatedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "meal_tracker")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipe.ts"), []byte("export const recipe = {};"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nutrition.ts"), []byte("export const calories = 100;"), 0644); err != nil {
		t.Fatal(err)
	}

	// Case 1: resultData is nil, rawOutput contains serialized entries (simulating NormalizeToolResult output)
	rawOutput := `{"entries":[{"name":"nutrition.ts","type":"file","size":28},{"name":"recipe.ts","type":"file","size":25}]}`
	summary, negative, sourceHash := ExtractObservationSummary("list_directory", "meal_tracker", nil, true, "Directory listed successfully", rawOutput, root)

	if negative {
		t.Fatalf("expected negative=false for populated directory, got true (summary: %q)", summary)
	}
	if summary == "Directory \"meal_tracker\" is empty (0 entries)" {
		t.Fatalf("unexpected empty directory summary for populated directory: %q", summary)
	}
	if sourceHash == "" {
		t.Fatalf("expected non-empty directory sourceHash, got empty")
	}

	// Case 2: from Observation envelope preview
	obsEnvelope := `{"tool":"list_directory","result":{"status":"completed","success":true,"preview":"{\"entries\":[{\"name\":\"recipe.ts\",\"type\":\"file\"}]}"}}`
	summary2, negative2, sourceHash2 := ExtractObservationSummary("list_directory", "meal_tracker", nil, true, "Directory listed successfully", obsEnvelope, root)
	if negative2 {
		t.Fatalf("expected negative=false for preview envelope, got true (summary: %q)", summary2)
	}
	if sourceHash2 == "" {
		t.Fatalf("expected non-empty sourceHash for preview envelope")
	}
}

func TestExtractObservationSummary_EmptyDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "empty_dir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	rawOutput := `{"entries":[]}`
	summary, negative, sourceHash := ExtractObservationSummary("list_directory", "empty_dir", nil, true, "Directory listed successfully", rawOutput, root)

	if !negative {
		t.Fatalf("expected negative=true for empty directory, got false (summary: %q)", summary)
	}
	if summary != "Directory \"empty_dir\" is empty (0 entries)" {
		t.Fatalf("expected empty directory summary, got: %q", summary)
	}
	if sourceHash == "" {
		t.Fatalf("expected deterministic sourceHash for empty directory")
	}
}

func TestExtractObservationSummary_MalformedRawOutputNeverBecomesNegative(t *testing.T) {
	root := t.TempDir()
	malformedOutput := `{not valid json`

	summary, negative, _ := ExtractObservationSummary("list_directory", "nonexistent_unknown", nil, true, "completed", malformedOutput, root)
	if negative {
		t.Fatalf("CRITICAL BUG: malformed raw output was interpreted as negative observation! (summary: %q)", summary)
	}
	if summary == "Directory \"nonexistent_unknown\" is empty (0 entries)" {
		t.Fatalf("CRITICAL BUG: malformed raw output claimed directory is empty: %q", summary)
	}

	searchSummary, searchNegative, _ := ExtractObservationSummary("search_file_name", "src", nil, true, "completed", malformedOutput, root)
	if searchNegative {
		t.Fatalf("CRITICAL BUG: malformed raw output was interpreted as negative search observation: %q", searchSummary)
	}
}

func TestDirectoryFingerprintInvalidationOnMutation(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseWorkspace(root)
	ctx := context.Background()

	fp1 := ComputeDirectoryFingerprint(srcDir)
	if fp1 == "" {
		t.Fatal("expected non-empty initial fingerprint")
	}

	obs := Observation{
		SessionID:       "test-session",
		Tool:            "list_directory",
		CallFingerprint: "list_directory:{\"path\":\"src\"}",
		Path:            "src",
		Scope:           "src",
		Summary:         "Directory \"src\" (1 entries): main.go",
		SourceHash:      fp1,
		Version:         CurrentObservationVersion,
		CreatedAt:       time.Now().UTC(),
	}
	saved, err := store.SaveObservation(ctx, obs)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Before mutation: observation must be valid
	if !IsObservationValid(ctx, saved, store, root) {
		t.Fatal("expected observation to be valid before mutation")
	}

	// 2. Mutate directory: create a new file
	time.Sleep(10 * time.Millisecond) // ensure timestamp granularity
	if err := os.WriteFile(filepath.Join(srcDir, "new.go"), []byte("package main\nfunc New() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	fp2 := ComputeDirectoryFingerprint(srcDir)
	if fp1 == fp2 {
		t.Fatal("expected directory fingerprint to change after adding new.go")
	}

	// 3. After mutation: observation must be invalid (stale)
	if IsObservationValid(ctx, saved, store, root) {
		t.Fatal("expected observation to be invalid after creating src/new.go")
	}
}

func TestObservation_RejectsLegacyVersionObservations(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseWorkspace(root)
	ctx := context.Background()

	legacyObs := Observation{
		SessionID:       "test-session",
		Tool:            "list_directory",
		CallFingerprint: "list_directory:{\"path\":\"legacy\"}",
		Path:            "legacy",
		Summary:         "Directory \"legacy\" is empty (0 entries)",
		Negative:        true,
		Version:         1, // older version
		CreatedAt:       time.Now().UTC(),
	}

	if IsObservationValid(ctx, legacyObs, store, root) {
		t.Fatal("expected legacy observation with version 1 to be rejected by current version 2 validator")
	}
}

func TestScopeContainment_AppDoesNotMatchApple(t *testing.T) {
	if isInScope("apple/lib/nutrition.ts", "app") {
		t.Fatal("CRITICAL: scope 'app' matched 'apple/lib/nutrition.ts' prefix!")
	}
	if !isInScope("app/lib/nutrition.ts", "app") {
		t.Fatal("expected scope 'app' to match 'app/lib/nutrition.ts'")
	}
	if !isInScope("app/lib/nutrition.ts", "app/lib") {
		t.Fatal("expected scope 'app/lib' to match 'app/lib/nutrition.ts'")
	}
	if !isInScope("anything/at/all.ts", ".") {
		t.Fatal("expected root scope '.' to match any file")
	}
}
