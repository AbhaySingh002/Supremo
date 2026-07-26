package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

func TestCommands_Registry(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 13 {
		t.Fatalf("expected 13 commands, got %d", len(list))
	}

	expectedNames := []string{"/help", "/clear", "/reset", "/krypton", "/plan", "/approve", "/deny", "/dry-run", "/cancel", "/auth", "/model", "/config", "/exit"}
	for i, cmd := range list {
		if cmd.Name != expectedNames[i] {
			t.Errorf("expected command %d to be %s, got %s", i, expectedNames[i], cmd.Name)
		}
	}
}

func TestCommands_KryptonRemovesOnlyWorkspaceState(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	for _, name := range []string{".memory", ".session", ".scratchpad", ".supremo"} {
		if err := os.MkdirAll(filepath.Join(name, "nested"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(".supremo", "nested", "credentials.json"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	session := &agent.Session{ID: "test", CurrentPlanID: "plan", PlanMode: true, DryRun: true}
	output, handled, err := NewRegistry().Handle(context.Background(), nil, session, "/krypton")
	if err != nil || !handled || !strings.Contains(output, "credentials were kept") {
		t.Fatalf("unexpected krypton result: output=%q handled=%v err=%v", output, handled, err)
	}
	for _, name := range []string{".memory", ".session", ".scratchpad"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed: %v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(".supremo", "nested", "credentials.json")); err != nil || string(data) != "keep" {
		t.Fatalf("credentials were removed: %q, %v", data, err)
	}
	if session.CurrentPlanID != "" || session.PlanMode || session.DryRun {
		t.Fatalf("session state was not cleared: %#v", session)
	}
}

func TestCommands_DryRunPersists(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	session := &agent.Session{ID: "test"}
	if _, handled, err := NewRegistry().Handle(context.Background(), nil, session, "/dry-run"); err != nil || !handled || !session.DryRun {
		t.Fatalf("expected dry run to be enabled: handled=%v err=%v", handled, err)
	}
	restored, err := agent.LoadOrCreateSession(".", "test")
	if err != nil || !restored.DryRun {
		t.Fatalf("expected persisted dry run: %v", err)
	}
}

func TestCommands_CancelUsesShellCallback(t *testing.T) {
	called := false
	registry := NewRegistry()
	registry.SetCancel(func() bool { called = true; return true })
	output, handled, err := registry.Handle(context.Background(), nil, &agent.Session{}, "/cancel")
	if err != nil || !handled || !called || output != "Cancellation requested." {
		t.Fatalf("unexpected cancellation: output=%q handled=%v called=%v err=%v", output, handled, called, err)
	}
}

func TestCommands_PlanStatusAndShow(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	session := &agent.Session{ID: "test", PlanMode: true}
	plan := &agent.Plan{ID: "saved", Description: "Saved task", Steps: []agent.Step{{ID: "one", Description: "done", Status: agent.StepCompleted}}}
	if err := session.SetPlan(".", plan); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"/plan status", "/plan show"} {
		output, handled, err := NewRegistry().Handle(context.Background(), nil, session, input)
		if err != nil || !handled || !strings.Contains(output, "Saved task") {
			t.Fatalf("unexpected %s result: output=%q handled=%v err=%v", input, output, handled, err)
		}
	}
}

func TestCommands_PlanTogglePersists(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	session := &agent.Session{ID: "test"}
	if err := session.Save("."); err != nil {
		t.Fatal(err)
	}
	_, handled, err := NewRegistry().Handle(context.Background(), nil, session, "/plan")
	if err != nil || !handled || !session.PlanMode {
		t.Fatalf("expected plan mode to be enabled: handled=%v err=%v", handled, err)
	}
	restored, err := agent.LoadOrCreateSession(".", "test")
	if err != nil || !restored.PlanMode {
		t.Fatalf("expected persisted plan mode: %v", err)
	}
}

func TestCommands_Handle_NonCommand(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	out, handled, err := r.Handle(ctx, nil, nil, "hello there")
	if handled {
		t.Error("expected handled to be false for non-command")
	}
	if out != "" || err != nil {
		t.Errorf("unexpected output/error: %q, %v", out, err)
	}
}

func TestCommands_Handle_UnknownCommand(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	out, handled, err := r.Handle(ctx, nil, nil, "/unknown")
	if !handled {
		t.Error("expected handled to be true for unknown command")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", out)
	}
}

func TestCommands_Exit(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	_, handled, err := r.Handle(ctx, nil, nil, "/exit")
	if !handled {
		t.Error("expected handled to be true")
	}
	if !errors.Is(err, ErrExit) {
		t.Errorf("expected ErrExit, got %v", err)
	}
}
