package contextcompiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestCompiler100RoundsLongHorizonContextComposition(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer state.CloseWorkspace(root)

	sessionID := "session-100-rounds"
	taskID := "task-100"

	_, err = store.SaveSession(context.Background(), state.SessionInput{
		ID:     sessionID,
		Name:   "Session 100 Rounds",
		Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create 100 historical message turns (surface history, not compiler-selected)
	var history []models.Message
	for i := 1; i <= 100; i++ {
		role := models.RoleUser
		content := fmt.Sprintf("Turn %d: user directive or progress update", i)
		if i%2 == 0 {
			role = models.RoleAssistant
			content = fmt.Sprintf("Turn %d: assistant response with details", i)
		}
		if i == 5 {
			content = "CRITICAL REQUIREMENT: must support zero-downtime database migration"
		}
		if i == 25 {
			content = "DECISION ACCEPTED: PostgreSQL 15 JSONB storage format approved and decision confirmed"
		}
		if i == 50 {
			content = "FAILURE NOTED: Connection pool exhaustion during stress test failed"
		}
		_, _ = store.AppendMessage(context.Background(), state.MessageInput{
			SessionID: sessionID,
			TaskID:    taskID,
			Role:      string(role),
			Parts:     []state.MessagePartInput{{Kind: "text", Text: content}},
		})
		history = append(history, models.Message{Role: role, Content: content})
	}

	// 2. Save WorkingMemory generation 10 document (compact continuity)
	wmPayload, _ := json.Marshal(map[string]any{
		"schema_version":  1,
		"session_id":      sessionID,
		"task_id":         taskID,
		"generation":      10,
		"objective":       "Zero downtime PostgreSQL 15 migration",
		"compact_summary": "# WORKING CONTINUITY (Generation 10)\n\nObjective: Zero downtime PostgreSQL 15 migration\nDecisions: PostgreSQL 15 JSONB format\nCompleted: Step 1 -> Step 2 -> Step 3",
		"updated_at":      time.Now().UTC(),
	})
	_, _ = store.SaveDocument(context.Background(), state.DocumentInput{
		ID:        "working-memory:" + sessionID + ":" + taskID,
		Kind:      "working_memory",
		SessionID: sessionID,
		Status:    "active",
		Payload:   wmPayload,
		Provenance: state.Provenance{
			Authority:  state.AuthorityDerived,
			ObservedAt: time.Now().UTC(),
		},
	})

	// 3. Create observations (some relevant to active files, some negative)
	pkgPath := filepath.Join(root, "package.json")
	pkgContent := `{"name":"db-migrator","version":"2.0"}`
	_ = os.WriteFile(pkgPath, []byte(pkgContent), 0644)
	fp, cArgs, path, scope := state.ComputeCallFingerprint("read_file", map[string]any{"path": "package.json"}, root)
	sum, neg, sHash := state.ExtractObservationSummary("read_file", path, map[string]any{"path": "package.json", "content": pkgContent}, true, "", "", root)
	_, _ = store.SaveObservation(context.Background(), state.Observation{
		SessionID:       sessionID,
		TaskID:          taskID,
		Tool:            "read_file",
		CallFingerprint: fp,
		CanonicalArgs:   cArgs,
		Scope:           scope,
		Path:            path,
		Summary:         sum,
		SourceHash:      sHash,
		Negative:        neg,
		CreatedAt:       time.Now().UTC(),
	})

	// Negative observation for missing AGENTS.md
	fp2, cArgs2, path2, scope2 := state.ComputeCallFingerprint("search_file_name", map[string]any{"path": ".", "pattern": "AGENTS.md"}, root)
	_, _ = store.SaveObservation(context.Background(), state.Observation{
		SessionID:       sessionID,
		TaskID:          taskID,
		Tool:            "search_file_name",
		CallFingerprint: fp2,
		CanonicalArgs:   cArgs2,
		Scope:           scope2,
		Path:            path2,
		Summary:         "No matches found for pattern AGENTS.md",
		Negative:        true,
		CreatedAt:       time.Now().UTC(),
	})

	// 4. Compile prompt
	registry := tools.NewRegistry()
	catalog, _ := registry.Catalog()
	compiler := New(store, nil)

	req := Request{
		SessionID:   sessionID,
		TaskID:      taskID,
		Objective:   "Zero downtime PostgreSQL 15 migration",
		ToolCatalog: catalog,
		ToolMode:    tools.ToolModeNormal,
		PromptMetadata: models.PromptMetadata{
			Profile: string(protocol.Conversational),
		},
		History: history,
	}

	prompt, err := compiler.Compile(context.Background(), req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Assertions:
	// A. Conversation history comes from the surface, not a 12-turn compiler window
	if len(prompt.Messages) != 100 {
		t.Fatalf("expected full 100-turn surface history, got %d", len(prompt.Messages))
	}

	// B. System prompt must contain WorkingMemory compact continuity
	if !strings.Contains(prompt.System, "WORKING CONTINUITY (Generation 10)") {
		t.Errorf("expected working memory in system prompt:\n%s", prompt.System)
	}
	if !strings.Contains(prompt.System, "Zero downtime PostgreSQL 15 migration") {
		t.Errorf("expected objective in system prompt:\n%s", prompt.System)
	}

	// C. System prompt must contain KNOWN RESEARCH EVIDENCE with package.json and negative observation
	if !strings.Contains(prompt.System, "KNOWN RESEARCH EVIDENCE") {
		t.Errorf("expected KNOWN RESEARCH EVIDENCE in system prompt:\n%s", prompt.System)
	}
	if !strings.Contains(prompt.System, "package.json") {
		t.Errorf("expected package.json in evidence:\n%s", prompt.System)
	}
	if !strings.Contains(prompt.System, "AGENTS.md absent/empty") && !strings.Contains(prompt.System, "AGENTS.md") {
		t.Errorf("expected negative observation in evidence:\n%s", prompt.System)
	}

	// D. High-signal historical messages (critical requirement, decision, failure) prioritized in conversation
	foundHighSignal := false
	for _, msg := range prompt.Messages {
		if strings.Contains(msg.Content, "CRITICAL REQUIREMENT") || strings.Contains(msg.Content, "DECISION ACCEPTED") || strings.Contains(msg.Content, "FAILURE NOTED") {
			foundHighSignal = true
			break
		}
	}
	if !foundHighSignal {
		t.Logf("Prompt messages (%d):", len(prompt.Messages))
		for idx, m := range prompt.Messages {
			t.Logf("  [%d] %s", idx, m.Content)
		}
		t.Errorf("expected at least one high-signal historical message in prompt messages")
	}

	// E. Total estimated tokens is well within default context limit
	if prompt.EstimatedInputTokens > 4000 {
		t.Errorf("estimated input tokens too high: %d (expected < 4000)", prompt.EstimatedInputTokens)
	}
}
