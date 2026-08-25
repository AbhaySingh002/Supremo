package contextcompiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestCompilerDurableObservationsSurviveLongHorizon(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	sessionID := "long-horizon-session"

	if _, err := store.SaveSession(ctx, state.SessionInput{ID: sessionID, Name: "Long Horizon"}); err != nil {
		t.Fatal(err)
	}

	// 1. Create a dummy file on disk in the workspace root
	pkgContent := `{"name":"meal-tracker","dependencies":{"next":"14.0.0","react":"18.2.0","tailwindcss":"3.3.0"}}`
	pkgPath := filepath.Join(store.Root(), "package.json")
	if err := os.WriteFile(pkgPath, []byte(pkgContent), 0644); err != nil {
		t.Fatal(err)
	}
	pkgHash := sha256.Sum256([]byte(pkgContent))
	pkgHashHex := hex.EncodeToString(pkgHash[:])

	// 2. Save durable observations:
	// A: Inspected package.json
	obs1 := state.Observation{
		SessionID:       sessionID,
		Tool:            "read_file",
		CallFingerprint: `read_file:{"path":"package.json"}`,
		Path:            "package.json",
		Scope:           "package.json",
		Summary:         "package.json (98 bytes): Next.js project with React and Tailwind configured",
		SourceHash:      pkgHashHex,
		Negative:        false,
		CreatedAt:       time.Now().UTC(),
	}
	if _, err := store.SaveObservation(ctx, obs1); err != nil {
		t.Fatalf("save observation 1: %v", err)
	}

	// B: Negative observation for missing AGENTS.md
	obs2 := state.Observation{
		SessionID:       sessionID,
		Tool:            "search_file_name",
		CallFingerprint: `search_file_name:{"path":".","pattern":"AGENTS.md"}`,
		Path:            "AGENTS.md",
		Scope:           ".",
		Summary:         "AGENTS.md absent under meal_tracker/ (0 matches)",
		Negative:        true,
		CreatedAt:       time.Now().UTC(),
	}
	if _, err := store.SaveObservation(ctx, obs2); err != nil {
		t.Fatalf("save observation 2: %v", err)
	}

	// 3. Conversation history is supplied by DeriveMessages, not reconstructed here.
	history := make([]models.Message, 0, 25)
	for i := 0; i < 25; i++ {
		role := models.RoleUser
		if i%2 == 1 {
			role = models.RoleAssistant
		}
		if _, err := store.AppendMessage(ctx, state.MessageInput{
			ID:        "msg-" + strconv.Itoa(i),
			SessionID: sessionID,
			Role:      string(role),
			Parts: []state.MessagePartInput{
				{Kind: "text", Text: "Turn message " + strconv.Itoa(i)},
			},
		}); err != nil {
			t.Fatal(err)
		}
		history = append(history, models.Message{Role: role, Content: "Turn message " + strconv.Itoa(i)})
	}

	compiler := New(store, nil)
	prompt, err := compiler.Compile(ctx, Request{
		SessionID:    sessionID,
		Objective:    "Upgrade meal tracker to React",
		Control:      "You are a coding assistant.",
		ContextLimit: 32000,
		History:      history,
	})
	if err != nil {
		t.Fatalf("compile context: %v", err)
	}

	if len(prompt.Messages) != 25 {
		t.Fatalf("expected full surface history (25), got %d", len(prompt.Messages))
	}
	if prompt.Messages[0].Content != "Turn message 0" {
		t.Fatalf("expected turn 0 to remain on the surface, got %#v", prompt.Messages[0])
	}

	// 7. Assert that KNOWN RESEARCH EVIDENCE survives in system prompt with both observations
	if !strings.Contains(prompt.System, "KNOWN RESEARCH EVIDENCE") {
		t.Fatalf("expected system prompt to contain KNOWN RESEARCH EVIDENCE, got:\n%s", prompt.System)
	}
	if !strings.Contains(prompt.System, "package.json @ hash") || !strings.Contains(prompt.System, "Next.js project with React and Tailwind") {
		t.Fatalf("expected package.json observation to survive in system prompt, got:\n%s", prompt.System)
	}
	if !strings.Contains(prompt.System, "AGENTS.md absent/empty") || !strings.Contains(prompt.System, "0 matches") {
		t.Fatalf("expected AGENTS.md negative observation to survive in system prompt, got:\n%s", prompt.System)
	}
}

func TestCompilerObservationInvalidationOnMutation(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	sessionID := "mutation-session"

	if _, err := store.SaveSession(ctx, state.SessionInput{ID: sessionID, Name: "Mutation Session"}); err != nil {
		t.Fatal(err)
	}

	// 1. Create file on disk
	initialContent := "package main\n\nfunc main() {}\n"
	mainPath := filepath.Join(store.Root(), "main.go")
	if err := os.WriteFile(mainPath, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}
	initialHash := sha256.Sum256([]byte(initialContent))
	initialHashHex := hex.EncodeToString(initialHash[:])

	// 2. Save observation with initial hash
	obs := state.Observation{
		SessionID:       sessionID,
		Tool:            "read_file",
		CallFingerprint: `read_file:{"path":"main.go"}`,
		Path:            "main.go",
		Scope:           "main.go",
		Summary:         "main.go (28 bytes): initial implementation",
		SourceHash:      initialHashHex,
		Negative:        false,
		CreatedAt:       time.Now().UTC(),
	}
	if _, err := store.SaveObservation(ctx, obs); err != nil {
		t.Fatal(err)
	}

	compiler := New(store, nil)

	// 3. Compile context before mutation -> should include observation
	prompt1, err := compiler.Compile(ctx, Request{
		SessionID:    sessionID,
		Objective:    "Check main.go",
		Control:      "System control",
		ContextLimit: 32000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt1.System, "main.go @ hash") {
		t.Fatalf("expected main.go observation before mutation, got:\n%s", prompt1.System)
	}

	// 4. Mutate file on disk
	modifiedContent := "package main\n\nfunc main() { println(\"mutated!\") }\n"
	if err := os.WriteFile(mainPath, []byte(modifiedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 5. Recompile context -> stale observation must be pruned!
	prompt2, err := compiler.Compile(ctx, Request{
		SessionID:    sessionID,
		Objective:    "Check main.go after edit",
		Control:      "System control",
		ContextLimit: 32000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt2.System, "main.go @ hash") {
		t.Fatalf("expected stale main.go observation to be pruned after mutation, got:\n%s", prompt2.System)
	}
}

func TestCompilerWorkingMemoryCoexistsWithActivePlan(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	sessionID := "plan-wm-session"
	taskID := "task-1"

	if _, err := store.SaveSession(ctx, state.SessionInput{ID: sessionID, Name: "Plan WM Session", CurrentTaskID: taskID}); err != nil {
		t.Fatal(err)
	}

	// Save active task document
	taskPayload, _ := json.Marshal(map[string]any{"id": taskID, "plan_id": "plan-1", "objective": "Build feature"})
	if _, err := store.SaveDocument(ctx, state.DocumentInput{
		ID:        taskID,
		Kind:      "task",
		SessionID: sessionID,
		Status:    "active",
		Payload:   taskPayload,
	}); err != nil {
		t.Fatal(err)
	}

	// Save active plan document
	planPayload, _ := json.Marshal(map[string]any{"id": "plan-1", "state": "plan_research", "objective": "Build feature"})
	if _, err := store.SaveDocument(ctx, state.DocumentInput{
		ID:        "plan-1",
		Kind:      "plan",
		SessionID: sessionID,
		Status:    "active",
		Payload:   planPayload,
	}); err != nil {
		t.Fatal(err)
	}

	// Save working_memory document
	wmPayload, _ := json.Marshal(map[string]any{
		"session_id":        sessionID,
		"task_id":           taskID,
		"compact_summary":   "Completed initial repository scan. 3 critical dependencies discovered.",
		"user_requirements": []string{"Must be backward compatible"},
	})
	if _, err := store.SaveDocument(ctx, state.DocumentInput{
		ID:        "working-memory:" + sessionID + ":" + taskID,
		Kind:      "working_memory",
		SessionID: sessionID,
		Status:    "active",
		Payload:   wmPayload,
	}); err != nil {
		t.Fatal(err)
	}

	compiler := New(store, nil)
	prompt, err := compiler.Compile(ctx, Request{
		SessionID:    sessionID,
		TaskID:       taskID,
		Objective:    "Build feature",
		Control:      "System control",
		ContextLimit: 32000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Assert working_memory is present in system prompt despite active plan
	if !strings.Contains(prompt.System, "working_memory") || !strings.Contains(prompt.System, "Completed initial repository scan") {
		t.Fatalf("expected working_memory to be present in prompt alongside active plan, got:\n%s", prompt.System)
	}
}

func TestSWERequestUsesObservationFactNotToolBody(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	sessionID, taskID := "swe-obs", "task-1"
	if _, err := store.SaveSession(ctx, state.SessionInput{ID: sessionID, Name: "SWE"}); err != nil {
		t.Fatal(err)
	}
	body := "export default function Page() { return <div>meal tracker home</div> }"
	pagePath := filepath.Join(store.Root(), "page.tsx")
	if err := os.WriteFile(pagePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	fp, cArgs, path, scope := state.ComputeCallFingerprint("read_file", map[string]any{"path": "page.tsx"}, store.Root())
	digest := sha256.Sum256([]byte(body))
	sum := "page.tsx layout: default Page export"
	if _, err := store.SaveObservation(ctx, state.Observation{
		SessionID: sessionID, TaskID: taskID, Tool: "read_file", CallFingerprint: fp, CanonicalArgs: cArgs,
		Scope: scope, Path: path, Summary: sum, SourceHash: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	call := models.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"page.tsx"}`)}
	history := []models.Message{
		{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{call}},
		{Role: models.RoleTool, Content: body, ToolCallID: "call-1", ToolName: "read_file"},
	}

	prompt, err := New(store, nil).Compile(ctx, Request{
		SessionID: sessionID, TaskID: taskID, Objective: "Inspect page.tsx", Control: "control",
		ContextLimit: 32000, PromptMetadata: models.PromptMetadata{Profile: "plan_research"},
		History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.System, "page.tsx @ hash") || !strings.Contains(prompt.System, sum) {
		t.Fatalf("expected verified fact+hash, got:\n%s", prompt.System)
	}
	if len(prompt.Messages) != 2 || prompt.Messages[1].Content != body {
		t.Fatalf("native tool result must stay on the surface: %#v", prompt.Messages)
	}
	if strings.Contains(prompt.System, body) {
		t.Fatalf("tool body leaked into system prompt:\n%s", prompt.System)
	}
}

func TestSWEStallCompileOmitsRereadAndNamesStrategy(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	sessionID, taskID := "swe-stall", "task-1"
	if _, err := store.SaveSession(ctx, state.SessionInput{ID: sessionID, Name: "Stall"}); err != nil {
		t.Fatal(err)
	}
	wmPayload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "task_id": taskID,
		"current_focus": map[string]any{
			"next_goal": "Find routing", "evidence_status": "unchanged", "previous_strategy": "read_file",
		},
	})
	if _, err := store.SaveDocument(ctx, state.DocumentInput{ID: "working-memory:" + sessionID + ":" + taskID, Kind: "working_memory", SessionID: sessionID, Status: "active", Payload: wmPayload}); err != nil {
		t.Fatal(err)
	}
	prompt, err := New(store, nil).Compile(ctx, Request{
		SessionID: sessionID, TaskID: taskID, Objective: "Research app", Control: "control", PlanStep: "Research the repository",
		ContextLimit: 32000, PromptMetadata: models.PromptMetadata{Profile: "execution"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.System, "Previous strategy: read_file") || !strings.Contains(strings.ToLower(prompt.System), "different strategy") {
		t.Fatalf("expected stall guidance in focus, got:\n%s", prompt.System)
	}
}
