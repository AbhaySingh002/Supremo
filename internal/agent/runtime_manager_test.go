package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type runtimeLifecycle struct{}

func (runtimeLifecycle) Compile(_ context.Context, request ContextRequest) (*models.Prompt, error) {
	return &models.Prompt{System: request.Session.ID}, nil
}
func (runtimeLifecycle) RecordObjective(context.Context, string, string, string) error { return nil }
func (runtimeLifecycle) RecordUsage(context.Context, *models.Prompt, providers.Usage) error {
	return nil
}
func (runtimeLifecycle) ObserveTool(context.Context, string, string, ToolObservation) error {
	return nil
}

type runtimeGateProvider struct {
	entered chan string
	release chan struct{}
}

func (p *runtimeGateProvider) Chat(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	select {
	case p.entered <- prompt.System:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.release:
		return &providers.Completion{Text: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func runtimeTestManager(provider providers.Provider, registry *tools.Registry) *RuntimeManager {
	if registry == nil {
		registry = tools.NewRegistry()
	}
	return NewRuntimeManager(func(string) (*Agent, error) {
		runtime := newTestAgent(provider, tools.NewManager(registry), runtimeLifecycle{}, nil, nil)
		runtime.ephemeral = true
		return runtime, nil
	})
}

func runManaged(m *RuntimeManager, sessionID string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := m.Run(context.Background(), &Session{ID: sessionID}, "run")
		done <- err
	}()
	return done
}

func TestRuntimeManagerReturnsStableIsolatedRuntimes(t *testing.T) {
	m := runtimeTestManager(&runtimeGateProvider{entered: make(chan string), release: make(chan struct{})}, nil)
	a1, err := m.For("a")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := m.For("a")
	b, _ := m.For("b")
	if a1 != a2 || a1 == b || a1.toolManager == b.toolManager || a1.hooks == b.hooks {
		t.Fatalf("runtimes are not isolated: a1=%p a2=%p b=%p", a1, a2, b)
	}
	if _, err := m.For(" "); err == nil {
		t.Fatal("empty session ID was accepted")
	}
}

func TestRuntimeManagerSerializesSameSessionAndRunsDifferentSessionsConcurrently(t *testing.T) {
	provider := &runtimeGateProvider{entered: make(chan string, 4), release: make(chan struct{}, 4)}
	m := runtimeTestManager(provider, nil)

	first := runManaged(m, "same")
	if got := <-provider.entered; got != "same" {
		t.Fatalf("first session = %q", got)
	}
	second := runManaged(m, "same")
	select {
	case got := <-provider.entered:
		t.Fatalf("same-session request overlapped: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	provider.release <- struct{}{}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := <-provider.entered; got != "same" {
		t.Fatalf("second session = %q", got)
	}
	provider.release <- struct{}{}
	if err := <-second; err != nil {
		t.Fatal(err)
	}

	a, b := runManaged(m, "a"), runManaged(m, "b")
	seen := map[string]bool{<-provider.entered: true, <-provider.entered: true}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("different sessions did not overlap: %v", seen)
	}
	provider.release <- struct{}{}
	provider.release <- struct{}{}
	if err := <-a; err != nil {
		t.Fatal(err)
	}
	if err := <-b; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeManagerCancelIsSessionScoped(t *testing.T) {
	provider := &runtimeGateProvider{entered: make(chan string, 2), release: make(chan struct{}, 1)}
	m := runtimeTestManager(provider, nil)
	a, b := runManaged(m, "a"), runManaged(m, "b")
	seen := map[string]bool{<-provider.entered: true, <-provider.entered: true}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("active sessions = %v", seen)
	}
	if !m.CancelSession("a") {
		t.Fatal("session a was not cancelled")
	}
	if err := <-a; !errors.Is(err, context.Canceled) {
		t.Fatalf("session a error = %v", err)
	}
	select {
	case err := <-b:
		t.Fatalf("cancelling a stopped b: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	provider.release <- struct{}{}
	if err := <-b; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeManagerRoutesApprovalsAndDeniesAmbiguousLegacyApproval(t *testing.T) {
	registry := tools.NewRegistry()
	tool := &probeTool{name: "write_probe"}
	if err := registry.Register(tool, tools.ToolMetadata{CanonicalName: tool.Name(), Family: "workspace", Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace, RequiresApproval: true}); err != nil {
		t.Fatal(err)
	}
	m := runtimeTestManager(&runtimeGateProvider{entered: make(chan string), release: make(chan struct{})}, registry)
	a, _ := m.For("a")
	b, _ := m.For("b")

	ctx := tools.WithActiveTools(tools.WithApprovalMode(context.Background(), tools.ApprovalBatman), []string{tool.Name()})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); _, _ = a.toolManager.Execute(ctx, tool.Name(), map[string]any{}) }()
	go func() { defer wait.Done(); _, _ = b.toolManager.Execute(ctx, tool.Name(), map[string]any{}) }()
	deadline := time.Now().Add(time.Second)
	for !(a.hasPendingApproval() && b.hasPendingApproval()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !a.hasPendingApproval() || !b.hasPendingApproval() {
		t.Fatal("approval requests did not become pending")
	}
	if m.ApprovePendingTool() {
		t.Fatal("ambiguous legacy approval was accepted")
	}
	if !m.ApproveSession("a") || !m.DenySession("b", "no") {
		t.Fatal("session-scoped approval routing failed")
	}
	wait.Wait()
}
