package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestRealContextBuilderRequiresCompiler(t *testing.T) {
	if _, err := NewRealContextBuilder(tools.NewRegistry(), nil, nil); err == nil {
		t.Fatal("accepted an absent Context Compiler V2")
	}
}

func TestLoadProjectInstructionsPrefersSupremoFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SUPREMO.md"), []byte("supremo instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadProjectInstructions(root); got != "supremo instructions" {
		t.Fatalf("unexpected instructions: %q", got)
	}
}

func TestRealContextBuilderRecordsPromptCompilerMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	session := &Session{ID: "prompt-session", Name: "Prompt"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	builder := &RealContextBuilder{registry: tools.NewRegistry(), compiler: contextcompiler.New(store, nil), contextLimit: func() int { return 32768 }}
	prompt, err := builder.Compile(context.Background(), ContextRequest{Session: session, Objective: "Plan safely", Mode: tools.ToolModeSide, Profile: protocol.Execution})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Metadata.Profile != string(protocol.Execution) || prompt.Metadata.ProtocolVersion != "" || len(prompt.Metadata.Templates) < 2 || len(prompt.Metadata.Sections) == 0 {
		t.Fatalf("prompt metadata = %#v", prompt.Metadata)
	}
	if strings.Contains(prompt.System, "Response Protocol") || strings.Contains(prompt.System, "final_answer") || strings.Contains(prompt.System, "<tool_call>") {
		t.Fatalf("assembled prompt still has a response envelope:\n%s", prompt.System)
	}
	manifest, err := builder.compiler.LatestManifest(context.Background(), session.ID)
	if err != nil || manifest.Prompt.Profile != string(protocol.Execution) || len(manifest.Prompt.Templates) != len(prompt.Metadata.Templates) {
		t.Fatalf("manifest prompt metadata = %#v err=%v", manifest.Prompt, err)
	}
}

func TestContextToolModeUsesExecutionAndPlanProfiles(t *testing.T) {
	session := &Session{ID: "mode-session"}
	if got := contextToolMode(ContextRequest{Session: session, Profile: protocol.Execution}); got != tools.ToolModeExecution {
		t.Fatalf("execution mode = %q", got)
	}
	if got := contextToolMode(ContextRequest{Session: session}); got != tools.ToolModeNormal {
		t.Fatalf("default mode = %q", got)
	}
	if err := session.applyEvent(SessionEvent{Type: EventPlanMode, Data: map[string]any{"active": true}}); err != nil {
		t.Fatal(err)
	}
	if got := contextToolMode(ContextRequest{Session: session, Mode: tools.ToolModeNormal, Profile: protocol.Execution}); got != tools.ToolModePlanning {
		t.Fatalf("active plan mode = %q", got)
	}
}

func TestStepContextMakesPlanModeResearchOnly(t *testing.T) {
	session := &Session{ID: "plan-safety"}
	if err := session.applyEvent(SessionEvent{Type: EventPlanMode, Data: map[string]any{"active": true}}); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{workspace: t.TempDir(), ephemeral: true}
	ctx, cancel, err := agent.taskContext(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if tools.IsResearchOnly(ctx) {
		t.Fatal("Plan Mode must be evaluated per Step, not frozen for the full turn")
	}
	if !tools.IsResearchOnly(agent.stepContext(ctx, session)) {
		t.Fatal("plan step context is not research-only")
	}
}
