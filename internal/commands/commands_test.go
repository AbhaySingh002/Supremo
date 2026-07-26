package commands

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestCommands_Registry(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 24 {
		t.Fatalf("expected 24 commands, got %d", len(list))
	}

	expectedNames := []string{"/activity", "/approve", "/auth", "/batman", "/cancel", "/clear", "/config", "/deny", "/doctor", "/dry-run", "/endpoint", "/exit", "/help", "/init", "/krypton", "/model", "/models", "/plan", "/provider", "/reset", "/strict", "/superman", "/tools", "/usage"}
	for i, command := range list {
		if command.Name != expectedNames[i] {
			t.Fatalf("command %d = %q, want %q", i, command.Name, expectedNames[i])
		}
	}
}

func TestCommands_InitCreatesWorkspaceMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	output, handled, err := NewRegistry().Handle(context.Background(), nil, nil, "/init")
	if err != nil || !handled || !strings.Contains(output, "initialized") {
		t.Fatalf("unexpected init result: output=%q handled=%v err=%v", output, handled, err)
	}
	memory, err := os.ReadFile(filepath.Join(root, ".memory", "MEMORY.md"))
	if err != nil || !strings.Contains(string(memory), "example.com/test") {
		t.Fatalf("workspace memory was not created: %q, %v", memory, err)
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

func TestCommands_ApprovalModesPersist(t *testing.T) {
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
	for _, input := range []string{"/strict mode", "/batman mode", "/superman"} {
		if _, handled, err := NewRegistry().Handle(context.Background(), nil, session, input); err != nil || !handled {
			t.Fatalf("%s: handled=%v err=%v", input, handled, err)
		}
	}
	if session.ApprovalMode != tools.ApprovalSuperman {
		t.Fatalf("approval mode = %q", session.ApprovalMode)
	}
	restored, err := agent.LoadOrCreateSession(".", "test")
	if err != nil || restored.ApprovalMode != tools.ApprovalSuperman {
		t.Fatalf("expected persisted superman mode: session=%#v err=%v", restored, err)
	}
}

func TestCommands_CancelWithoutInteractiveTask(t *testing.T) {
	output, handled, err := NewRegistry().Handle(context.Background(), nil, &agent.Session{}, "/cancel")
	if err != nil || !handled || output != "No active task." {
		t.Fatalf("unexpected cancellation: output=%q handled=%v err=%v", output, handled, err)
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

func TestCommands_PlanResumeRequiresInteractiveTUI(t *testing.T) {
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
	if err := session.SetPlan(".", &agent.Plan{ID: "saved", Description: "Saved task", Steps: []agent.Step{{ID: "one", Description: "pending", Status: agent.StepPending}}}); err != nil {
		t.Fatal(err)
	}
	_, handled, err := NewRegistry().Handle(context.Background(), nil, session, "/plan resume")
	if !handled || err == nil || !strings.Contains(err.Error(), "interactive TUI") {
		t.Fatalf("plan resume should require the TUI: handled=%v err=%v", handled, err)
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

func TestCommands_AuthSavesAndVerifiesKey(t *testing.T) {
	verified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer rejected-key" {
			http.Error(w, "invalid key", http.StatusUnauthorized)
			return
		}
		verified = r.Header.Get("Authorization") == "Bearer replacement-key"
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	if err := providers.SaveConfig(dir, &providers.Config{ProviderName: "openai", Model: "test-model", Endpoint: server.URL}); err != nil {
		t.Fatal(err)
	}
	store := providers.NewFileCredentialStore(dir)
	if err := store.SetAPIKey("openai", "old-key"); err != nil {
		t.Fatal(err)
	}
	manager := providers.NewManager(dir, store)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	output, handled, err := NewRegistry().Handle(context.Background(), &app.App{ProviderManager: manager}, &agent.Session{}, "/auth replacement-key")
	if err != nil || !handled || output != "API key saved and verified." {
		t.Fatalf("unexpected auth result: output=%q handled=%v err=%v", output, handled, err)
	}
	if !verified || !manager.GetRuntimeConfig().CredentialConfigured() {
		t.Fatal("updated key was not verified through the active provider")
	}

	output, handled, err = NewRegistry().Handle(context.Background(), &app.App{ProviderManager: manager}, &agent.Session{}, "/auth rejected-key")
	if err != nil || !handled || !strings.Contains(output, "verification failed") {
		t.Fatalf("unexpected rejected-key result: output=%q handled=%v err=%v", output, handled, err)
	}
	_, _, _, apiKey, _ := manager.GetRuntimeConfig().Get()
	if apiKey != "rejected-key" {
		t.Fatalf("rejected key should remain saved for recovery, got %q", apiKey)
	}
}
