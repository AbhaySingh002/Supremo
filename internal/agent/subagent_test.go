package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type subagentLifecycle struct{}

func (subagentLifecycle) Compile(_ context.Context, request ContextRequest) (*models.Prompt, error) {
	return &models.Prompt{System: request.Objective}, nil
}
func (subagentLifecycle) RecordObjective(context.Context, string, string, string) error { return nil }
func (subagentLifecycle) RecordUsage(context.Context, *models.Prompt, providers.Usage) error {
	return nil
}
func (subagentLifecycle) ObserveTool(context.Context, string, string, ToolObservation) error {
	return nil
}

type subagentGateProvider struct {
	entered chan string
	release chan struct{}
}

func (p *subagentGateProvider) Chat(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	select {
	case p.entered <- prompt.System:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.release:
		return &providers.Completion{Text: "done: " + prompt.System, FinishReason: "stop"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newSubagentTestManager(t *testing.T, provider providers.Provider) (string, *state.Store, *SubagentManager) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	transcript := newDurableMemory(store)
	runtimes := NewRuntimeManager(func(string) (*Agent, error) {
		runtime := newTestAgent(provider, tools.NewManager(tools.NewRegistry()), subagentLifecycle{}, transcript, runtime.NewHookSet())
		runtime.workspace = root
		return runtime, nil
	})
	manager, err := NewSubagentManager(root, store, runtimes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = manager.Close()
		_ = runtimes.Close()
		_ = state.CloseWorkspace(root)
	})
	return root, store, manager
}

func saveSubagentTestSession(t *testing.T, root, id string) *Session {
	t.Helper()
	session := &Session{
		ID: id, Name: id, CreatedAt: time.Now().UTC(), Status: "active",
		Provider: "mock", Model: "m1", ApprovalMode: tools.ApprovalBatman, DryRun: true,
	}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	return session
}

func receiveSubagentCall(t *testing.T, provider *subagentGateProvider) string {
	t.Helper()
	select {
	case prompt := <-provider.entered:
		return prompt
	case <-time.After(2 * time.Second):
		t.Fatal("subagent provider was not called")
		return ""
	}
}

func subagentBackground(enabled bool) *bool { return &enabled }

func TestSubagentBackgroundRunIsDurableAndParentScoped(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 2), release: make(chan struct{}, 2)}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")
	other := saveSubagentTestSession(t, root, "other")

	accepted, err := manager.Start(context.Background(), SubagentRequest{
		ParentSessionID: parent.ID, Label: "inspect", Prompt: "inspect repository",
		Scope: SubagentScopeExecution,
	})
	if err != nil || accepted.Status != "queued" || accepted.AgentID == "" || accepted.MessageID == "" {
		t.Fatalf("background acceptance = %#v, %v", accepted, err)
	}
	if prompt := receiveSubagentCall(t, provider); prompt != "inspect repository" {
		t.Fatalf("provider prompt = %q", prompt)
	}
	child, err := LoadSession(root, accepted.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentSessionID != parent.ID || child.Origin != "subagent" || child.DelegationScope != SubagentScopeExecution || child.ApprovalMode != parent.ApprovalMode || child.DryRun != parent.DryRun {
		t.Fatalf("child metadata = %#v", child)
	}
	records, err := sessionlog.Load(context.Background(), manager.store, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) < 3 || records[0].Type != sessionlog.EventSubagentDescriptor || records[1].Type != sessionlog.EventSubagentQueued || records[2].Type != sessionlog.EventSubagentRunStart {
		t.Fatalf("durable child prefix = %#v", records)
	}
	if _, err := manager.Wait(context.Background(), other.ID, child.ID, accepted.MessageID); err == nil {
		t.Fatal("non-parent was allowed to wait on child")
	}
	provider.release <- struct{}{}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	completed, err := manager.Wait(waitCtx, parent.ID, child.ID, accepted.MessageID)
	if err != nil || completed.Status != "completed" || completed.Output != "done: inspect repository" {
		t.Fatalf("completed run = %#v, %v", completed, err)
	}
	children, err := manager.List(context.Background(), parent.ID, false)
	if err != nil || len(children) != 1 || children[0].AgentID != child.ID {
		t.Fatalf("children = %#v, %v", children, err)
	}
}

func TestSubagentScopeDepthAndPlanModeCannotWidenAuthority(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 8), release: make(chan struct{}, 8)}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")

	startAndFinish := func(parentID, label string, scope SubagentScope) SubagentRun {
		t.Helper()
		run, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: parentID, Label: label, Prompt: label, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		_ = receiveSubagentCall(t, provider)
		provider.release <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := manager.Wait(ctx, parentID, run.AgentID, run.MessageID); err != nil {
			t.Fatal(err)
		}
		return run
	}

	child1 := startAndFinish(parent.ID, "child-1", SubagentScopeLocalRead)
	loadedChild, err := LoadSession(root, child1.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	childRuntime, err := manager.runtimes.For(child1.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	childCtx, stopChild, err := childRuntime.taskContext(context.Background(), loadedChild)
	if err != nil {
		t.Fatal(err)
	}
	if !tools.IsDelegated(childCtx) || !tools.IsReadOnly(childCtx) || !tools.IsResearchOnly(childCtx) {
		t.Fatalf("local_read child context did not enforce delegation boundary")
	}
	stopChild()
	manager.runtimes.Release(child1.AgentID)
	if _, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: child1.AgentID, Label: "widen", Prompt: "mutate", Scope: SubagentScopeExecution}); err == nil || !strings.Contains(err.Error(), "cannot widen") {
		t.Fatalf("local_read widening error = %v", err)
	}
	child2 := startAndFinish(child1.AgentID, "child-2", SubagentScopeLocalRead)
	child3 := startAndFinish(child2.AgentID, "child-3", SubagentScopeLocalRead)
	if _, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: child3.AgentID, Label: "too-deep", Prompt: "inspect", Scope: SubagentScopeLocalRead}); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("depth error = %v", err)
	}
	if err := manager.Interrupt(context.Background(), child2.AgentID, child1.AgentID); err == nil {
		t.Fatal("descendant was allowed to interrupt its ancestor")
	}
	if err := manager.Interrupt(context.Background(), parent.ID, child3.AgentID); err != nil {
		t.Fatalf("ancestor interrupt = %v", err)
	}

	planParent := saveSubagentTestSession(t, root, "plan-parent")
	if err := planParent.setPlanMode(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	planChild := startAndFinish(planParent.ID, "plan-child", SubagentScopeExecution)
	loaded, err := LoadSession(root, planChild.AgentID)
	if err != nil || loaded.DelegationScope != SubagentScopeLocalRead {
		t.Fatalf("plan child scope = %q, %v", loaded.DelegationScope, err)
	}
}

func TestSubagentRecoveryRunsQueuedMessagesFIFOAndRepairsStartedWork(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 4), release: make(chan struct{}, 4)}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")
	child := &Session{
		ID: "recovered-child", Name: "recovered-child", CreatedAt: time.Now().UTC(), Status: "active",
		Provider: parent.Provider, Model: parent.Model, ApprovalMode: parent.ApprovalMode,
		ParentSessionID: parent.ID, Origin: "subagent", DelegationLabel: "recovered-child", DelegationDepth: 1, DelegationScope: SubagentScopeLocalRead,
	}
	descriptor := sessionlog.SubagentDescriptorPayload{Version: 1, ParentSessionID: parent.ID, Label: child.Name, Depth: 1, Scope: string(SubagentScopeLocalRead)}
	first := sessionlog.SubagentQueuedPayload{MessageID: "message-1", SenderSessionID: parent.ID, Content: "first"}
	if err := manager.createChild(context.Background(), child, descriptor, first); err != nil {
		t.Fatal(err)
	}
	secondRecord, _ := sessionlog.New(sessionlog.EventSubagentQueued, sessionlog.SubagentQueuedPayload{MessageID: "message-2", SenderSessionID: parent.ID, Content: "second"})
	if _, err := sessionlog.Append(context.Background(), manager.store, child.ID, secondRecord); err != nil {
		t.Fatal(err)
	}
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := receiveSubagentCall(t, provider); got != "first" {
		t.Fatalf("first recovered prompt = %q", got)
	}
	provider.release <- struct{}{}
	if got := receiveSubagentCall(t, provider); got != "second" {
		t.Fatalf("second recovered prompt = %q", got)
	}
	provider.release <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if run, err := manager.Wait(ctx, parent.ID, child.ID, "message-2"); err != nil || run.Status != "completed" {
		t.Fatalf("second recovered result = %#v, %v", run, err)
	}

	interrupted := &Session{
		ID: "interrupted-child", Name: "interrupted-child", CreatedAt: time.Now().UTC(), Status: "active",
		ParentSessionID: parent.ID, Origin: "subagent", DelegationLabel: "interrupted-child", DelegationDepth: 1, DelegationScope: SubagentScopeExecution,
	}
	queued := sessionlog.SubagentQueuedPayload{MessageID: "interrupted-message", SenderSessionID: parent.ID, Content: "do not replay"}
	if err := manager.createChild(context.Background(), interrupted, descriptor, queued); err != nil {
		t.Fatal(err)
	}
	start, _ := sessionlog.New(sessionlog.EventSubagentRunStart, sessionlog.SubagentRunStartPayload{RunID: "interrupted-run", MessageID: queued.MessageID})
	if _, err := sessionlog.Append(context.Background(), manager.store, interrupted.ID, start); err != nil {
		t.Fatal(err)
	}
	records, err := sessionlog.Load(context.Background(), manager.store, interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.repairRuns(context.Background(), interrupted.ID, records); err != nil {
		t.Fatal(err)
	}
	records, err = sessionlog.Load(context.Background(), manager.store, interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	end, found := findSubagentEnd(records, queued.MessageID)
	if !found || end.Status != "interrupted" || !end.Recovered {
		t.Fatalf("repaired run end = %#v, found=%v", end, found)
	}
	completed := &Session{
		ID: "completed-child", Name: "completed-child", CreatedAt: time.Now().UTC(), Status: "active",
		ParentSessionID: parent.ID, Origin: "subagent", DelegationLabel: "completed-child", DelegationDepth: 1, DelegationScope: SubagentScopeLocalRead,
	}
	completedQueue := sessionlog.SubagentQueuedPayload{MessageID: "completed-message", SenderSessionID: parent.ID, Content: "completed before restart"}
	if err := manager.createChild(context.Background(), completed, descriptor, completedQueue); err != nil {
		t.Fatal(err)
	}
	completedStart, _ := sessionlog.New(sessionlog.EventSubagentRunStart, sessionlog.SubagentRunStartPayload{RunID: "completed-run", MessageID: completedQueue.MessageID})
	if _, err := sessionlog.Append(context.Background(), manager.store, completed.ID, completedStart); err != nil {
		t.Fatal(err)
	}
	assistant, _ := sessionlog.New(sessionlog.EventAssistantMessage, nil)
	assistant.Message = models.Message{Role: models.RoleAssistant, Content: "durable answer"}
	assistant.SurfaceOp = &sessionlog.SurfaceOp{Kind: sessionlog.SurfaceAppend}
	if _, err := sessionlog.Append(context.Background(), manager.store, completed.ID, assistant); err != nil {
		t.Fatal(err)
	}
	turnEnd, _ := sessionlog.New(sessionlog.EventTurnEnd, sessionlog.TurnEndPayload{Turn: 1, Reason: "completed"})
	if _, err := sessionlog.Append(context.Background(), manager.store, completed.ID, turnEnd); err != nil {
		t.Fatal(err)
	}
	completedRecords, err := sessionlog.Load(context.Background(), manager.store, completed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.repairRuns(context.Background(), completed.ID, completedRecords); err != nil {
		t.Fatal(err)
	}
	completedRecords, _ = sessionlog.Load(context.Background(), manager.store, completed.ID)
	completedEnd, found := findSubagentEnd(completedRecords, completedQueue.MessageID)
	if !found || completedEnd.Status != "recovered-completed" || completedEnd.Output != "durable answer" {
		t.Fatalf("completed repair = %#v, found=%v", completedEnd, found)
	}
	select {
	case prompt := <-provider.entered:
		t.Fatalf("started work was replayed: %q", prompt)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubagentForegroundCancellationOnlyStopsSelectedChild(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 2), release: make(chan struct{}, 2)}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.Start(ctx, SubagentRequest{ParentSessionID: parent.ID, Label: "foreground", Prompt: "foreground", Scope: SubagentScopeExecution, RunInBackground: subagentBackground(false)})
		done <- err
	}()
	_ = receiveSubagentCall(t, provider)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("foreground cancellation = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground child did not cancel")
	}
}

func TestSubagentInterruptIsChildScoped(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 2), release: make(chan struct{}, 1)}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")
	first, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: parent.ID, Label: "first", Prompt: "first", Scope: SubagentScopeExecution})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: parent.ID, Label: "second", Prompt: "second", Scope: SubagentScopeExecution})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{receiveSubagentCall(t, provider): true, receiveSubagentCall(t, provider): true}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("sibling subagents did not overlap: %#v", seen)
	}
	if err := manager.Interrupt(context.Background(), parent.ID, first.AgentID); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if run, err := manager.Wait(waitCtx, parent.ID, first.AgentID, first.MessageID); err != nil || run.Status != "cancelled" {
		t.Fatalf("interrupted child = %#v, %v", run, err)
	}
	secondDone := make(chan SubagentRun, 1)
	go func() {
		run, _ := manager.Wait(waitCtx, parent.ID, second.AgentID, second.MessageID)
		secondDone <- run
	}()
	select {
	case run := <-secondDone:
		t.Fatalf("sibling stopped with interrupted child: %#v", run)
	case <-time.After(50 * time.Millisecond):
	}
	provider.release <- struct{}{}
	select {
	case run := <-secondDone:
		if run.Status != "completed" {
			t.Fatalf("sibling result = %#v", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("uninterrupted sibling did not finish")
	}
}

func TestSubagentShutdownClosesAdmissionAfterDurableTerminalEvent(t *testing.T) {
	provider := &subagentGateProvider{entered: make(chan string, 1), release: make(chan struct{})}
	root, _, manager := newSubagentTestManager(t, provider)
	parent := saveSubagentTestSession(t, root, "parent")
	run, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: parent.ID, Label: "shutdown", Prompt: "shutdown", Scope: SubagentScopeExecution})
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveSubagentCall(t, provider)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	records, err := sessionlog.Load(context.Background(), manager.store, run.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	end, found := findSubagentEnd(records, run.MessageID)
	if !found || end.Status != "cancelled" {
		t.Fatalf("shutdown terminal event = %#v, found=%v", end, found)
	}
	if _, err := manager.Start(context.Background(), SubagentRequest{ParentSessionID: parent.ID, Label: "late", Prompt: "late", Scope: SubagentScopeLocalRead}); err == nil {
		t.Fatal("shutdown manager accepted new child")
	}
}
