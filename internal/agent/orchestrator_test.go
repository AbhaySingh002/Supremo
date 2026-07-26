package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type scriptedProvider struct {
	responses []string
	calls     int
}

type streamedProvider struct{ chunks []string }

func (p *streamedProvider) Chat(context.Context, *models.Prompt) (*providers.Completion, error) {
	return nil, fmt.Errorf("Chat should not be used when streaming is available")
}

func (p *streamedProvider) Stream(_ context.Context, _ *models.Prompt, receive func(string)) (*providers.Completion, error) {
	for _, chunk := range p.chunks {
		receive(chunk)
	}
	return &providers.Completion{Raw: strings.Join(p.chunks, "")}, nil
}

func (p *scriptedProvider) Chat(context.Context, *models.Prompt) (*providers.Completion, error) {
	if p.calls == len(p.responses) {
		return nil, fmt.Errorf("unexpected provider call")
	}
	response := p.responses[p.calls]
	p.calls++
	return &providers.Completion{Raw: response}, nil
}

type testContextBuilder struct{}

func (testContextBuilder) Build(context.Context, *Session) (*models.Prompt, error) {
	return &models.Prompt{System: "test system"}, nil
}

type testTool struct {
	calls int
	fail  int
}

func (t *testTool) Name() string        { return "test_tool" }
func (t *testTool) Description() string { return "test tool" }
func (t *testTool) Schema() any         { return map[string]any{"type": "object"} }
func (t *testTool) Execute(context.Context, any) (*tools.ToolResult, error) {
	t.calls++
	if t.calls <= t.fail {
		return tools.BuildToolResult(false, "failed", nil), nil
	}
	return tools.BuildToolResult(true, "completed", nil), nil
}

func newPlanAgent(root string, provider *scriptedProvider, tool *testTool) *Agent {
	registry := tools.NewRegistry()
	_ = registry.Register(tool)
	return &Agent{
		provider:       provider,
		toolManager:    tools.NewManager(registry),
		parser:         parser.NewParser(),
		contextBuilder: testContextBuilder{},
		memory:         newInMemoryMemory(root),
		workspace:      root,
	}
}

func plannerResponse(tool string) string {
	return fmt.Sprintf(`{"description":"test task","steps":[{"id":"step-one","description":"run tool","tool":%q,"arguments":{},"status":"pending"}]}`, tool)
}

func TestPlanModeBuildAndApprove(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProvider{responses: []string{
		plannerResponse("test_tool"),
		`{"approved":true,"reason":"all steps passed","retry_steps":[]}`,
	}}
	tool := &testTool{}
	agent := newPlanAgent(root, provider, tool)
	var events []ProgressEvent
	agent.SetProgress(func(event ProgressEvent) { events = append(events, event) })
	session := &Session{ID: "session", PlanMode: true}

	output, err := agent.Run(context.Background(), session, "do the task")
	if err != nil || !strings.Contains(output, "approved") || tool.calls != 1 || provider.calls != 2 {
		t.Fatalf("unexpected plan result: output=%q err=%v toolCalls=%d providerCalls=%d", output, err, tool.calls, provider.calls)
	}
	plan, err := session.ActivePlan(root)
	if err != nil || plan == nil || plan.Steps[0].Status != StepCompleted {
		t.Fatalf("expected persisted completed plan: %#v, %v", plan, err)
	}
	seenPlan, seenBuild, seenCompletion := false, false, false
	for _, event := range events {
		seenPlan = seenPlan || event.Kind == ProgressPlan
		seenBuild = seenBuild || event.Kind == ProgressPlanStep
		seenCompletion = seenCompletion || event.Kind == ProgressCompletion
	}
	if !seenPlan || !seenBuild || !seenCompletion {
		t.Fatalf("missing lifecycle events: plan=%t build=%t completion=%t", seenPlan, seenBuild, seenCompletion)
	}
	progress, err := os.ReadFile(filepath.Join(root, ".memory", "progress.md"))
	if err != nil || !strings.Contains(string(progress), "recorded tool output") {
		t.Fatalf("expected progress entry: %v", err)
	}
}

func TestAgentStreamsVisibleFinalAnswer(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	worker := &Agent{
		provider:       &streamedProvider{chunks: []string{"<final_answer>Fin", "ished</final_answer>"}},
		toolManager:    tools.NewManager(registry),
		parser:         parser.NewParser(),
		contextBuilder: testContextBuilder{},
		memory:         newInMemoryMemory(root),
		workspace:      root,
	}
	var events []ProgressEvent
	worker.SetProgress(func(event ProgressEvent) { events = append(events, event) })

	output, err := worker.Run(context.Background(), &Session{ID: "stream"}, "finish")
	if err != nil || output != "Finished" {
		t.Fatalf("streamed run: output=%q err=%v", output, err)
	}
	seen := false
	for _, event := range events {
		seen = seen || event.Kind == ProgressStream && event.Message == "Finished"
	}
	if !seen {
		t.Fatalf("missing final stream event: %#v", events)
	}
}

func TestAgentDoesNotStreamReActThoughts(t *testing.T) {
	root := t.TempDir()
	worker := &Agent{
		provider:       &streamedProvider{chunks: []string{"private reasoning", "<final_answer>Done</final_answer>"}},
		toolManager:    tools.NewManager(tools.NewRegistry()),
		parser:         parser.NewParser(),
		contextBuilder: testContextBuilder{},
		memory:         newInMemoryMemory(root),
		workspace:      root,
	}
	var events []ProgressEvent
	worker.SetProgress(func(event ProgressEvent) { events = append(events, event) })
	if output, err := worker.Run(context.Background(), &Session{ID: "private-thought"}, "finish"); err != nil || output != "Done" {
		t.Fatalf("run = %q, %v", output, err)
	}
	for _, event := range events {
		if event.Kind == ProgressStream && strings.Contains(event.Message, "private reasoning") {
			t.Fatalf("thought leaked to progress stream: %#v", event)
		}
	}
}

func TestPlanModeRetriesFailedStepOnce(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProvider{responses: []string{
		plannerResponse("test_tool"),
		`{"approved":false,"reason":"retry the failed tool","retry_steps":["step-one"]}`,
		`{"approved":true,"reason":"retry passed","retry_steps":[]}`,
	}}
	tool := &testTool{fail: 1}
	agent := newPlanAgent(root, provider, tool)
	session := &Session{ID: "session", PlanMode: true}

	if _, err := agent.Run(context.Background(), session, "do the task"); err != nil {
		t.Fatal(err)
	}
	plan, err := session.ActivePlan(root)
	if err != nil || plan.Steps[0].Status != StepCompleted || tool.calls != 2 || provider.calls != 3 {
		t.Fatalf("expected one successful retry: plan=%#v err=%v toolCalls=%d providerCalls=%d", plan, err, tool.calls, provider.calls)
	}
}

func TestPlanModeRejectsInvalidPlannerOutput(t *testing.T) {
	for _, response := range []string{"not json", plannerResponse("missing_tool"), plannerResponse("test_tool") + " trailing", `{"description":"task","extra":true,"steps":[{"id":"step-one","description":"run","tool":"test_tool","arguments":{},"status":"pending"}]}`, `{"description":"task","steps":[{"id":"step-one","description":"missing args","tool":"test_tool","status":"pending"}]}`} {
		t.Run(response, func(t *testing.T) {
			root := t.TempDir()
			agent := newPlanAgent(root, &scriptedProvider{responses: []string{response}}, &testTool{})
			session := &Session{ID: "session", PlanMode: true}
			if _, err := agent.Run(context.Background(), session, "do the task"); err == nil {
				t.Fatal("expected planner output to fail")
			}
			if session.CurrentPlanID != "" {
				t.Fatal("invalid planner output must not activate a plan")
			}
			if _, err := os.Stat(filepath.Join(root, ".session", "plans")); !os.IsNotExist(err) {
				t.Fatalf("invalid planner output must not persist a plan: %v", err)
			}
		})
	}
}

func TestPlanModeKeepsPreviousPlanWhenReplacementIsInvalid(t *testing.T) {
	root := t.TempDir()
	agent := newPlanAgent(root, &scriptedProvider{responses: []string{"not json"}}, &testTool{})
	session := &Session{ID: "session", PlanMode: true}
	if err := session.SetPlan(root, &Plan{ID: "old", Description: "old plan", Steps: []Step{{ID: "old-step", Description: "old", Status: StepPending}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Run(context.Background(), session, "replace it"); err == nil {
		t.Fatal("expected invalid replacement to fail")
	}
	plan, err := session.ActivePlan(root)
	if err != nil || session.CurrentPlanID != "old" || plan == nil || plan.Description != "old plan" {
		t.Fatalf("replacement lost active plan: session=%#v plan=%#v err=%v", session, plan, err)
	}
}

func TestPlanModeRejectsMalformedAuditorOutput(t *testing.T) {
	root := t.TempDir()
	agent := newPlanAgent(root, &scriptedProvider{responses: []string{plannerResponse("test_tool"), "not json"}}, &testTool{})
	session := &Session{ID: "session", PlanMode: true}
	if _, err := agent.Run(context.Background(), session, "do the task"); err == nil {
		t.Fatal("expected malformed auditor response to fail")
	}
	plan, err := session.ActivePlan(root)
	if err != nil || plan == nil || plan.Steps[0].Status != StepCompleted {
		t.Fatalf("expected completed plan to remain intact: %#v, %v", plan, err)
	}
}

func TestPlanModeRetainsPreviousPlanAndStopsAtFailure(t *testing.T) {
	root := t.TempDir()
	planJSON := `{"description":"test task","steps":[{"id":"first","description":"fail first","tool":"test_tool","arguments":{},"status":"pending"},{"id":"second","description":"stay pending","tool":"test_tool","arguments":{},"status":"pending"}]}`
	provider := &scriptedProvider{responses: []string{
		planJSON,
		`{"approved":false,"reason":"retry first","retry_steps":["first"]}`,
		`{"approved":true,"reason":"first recovered","retry_steps":[]}`,
		`{"approved":true,"reason":"resumed","retry_steps":[]}`,
	}}
	tool := &testTool{fail: 1}
	agent := newPlanAgent(root, provider, tool)
	session := &Session{ID: "session", PlanMode: true}
	if err := session.SetPlan(root, &Plan{ID: "old", Description: "old", Steps: []Step{{ID: "old-step", Description: "old", Status: StepPending}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Run(context.Background(), session, "do the task"); err != nil {
		t.Fatal(err)
	}
	plan, err := session.ActivePlan(root)
	if err != nil || plan.Steps[0].Status != StepCompleted || plan.Steps[1].Status != StepPending || tool.calls != 2 {
		t.Fatalf("failed build did not stop: plan=%#v calls=%d err=%v", plan, tool.calls, err)
	}
	if _, err := agent.ResumePlan(context.Background(), session); err != nil || tool.calls != 3 {
		t.Fatalf("resume did not run only pending step: calls=%d err=%v", tool.calls, err)
	}
	plan, err = session.ActivePlan(root)
	if err != nil || plan.Steps[1].Status != StepCompleted {
		t.Fatalf("resume did not persist completion: %#v, %v", plan, err)
	}
}

func TestPlanModeRejectsStrictAuditorJSON(t *testing.T) {
	root := t.TempDir()
	for _, verdict := range []string{
		`{"approved":true,"reason":"ok","retry_steps":[],"extra":true}`,
		`{"approved":true,"reason":"ok","retry_steps":[]} trailing`,
		`{"reason":"missing approved","retry_steps":[]}`,
	} {
		agent := newPlanAgent(root, &scriptedProvider{responses: []string{plannerResponse("test_tool"), verdict}}, &testTool{})
		session := &Session{ID: "session", PlanMode: true}
		if _, err := agent.Run(context.Background(), session, "do task"); err == nil {
			t.Fatalf("accepted malformed verdict %q", verdict)
		}
	}
}

func TestPlanModeHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	agent := newPlanAgent(root, &scriptedProvider{}, &testTool{})
	session := &Session{ID: "session", PlanMode: true}
	if _, err := agent.Run(ctx, session, "do the task"); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".session", "plans")); !os.IsNotExist(err) {
		t.Fatalf("canceled run must not persist a plan: %v", err)
	}
}

func TestNormalModeSkipsOrchestration(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProvider{responses: []string{"<final_answer>done</final_answer>"}}
	agent := newPlanAgent(root, provider, &testTool{})
	session := &Session{ID: "session"}
	output, err := agent.Run(context.Background(), session, "do the task")
	if err != nil || output != "done" || provider.calls != 1 {
		t.Fatalf("normal execution changed: output=%q err=%v calls=%d", output, err, provider.calls)
	}
	if _, err := os.Stat(filepath.Join(root, ".session", "plans")); !os.IsNotExist(err) {
		t.Fatalf("normal mode must not create a plan: %v", err)
	}
}

func TestResumePlanRunsOnlyUnfinishedSteps(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProvider{responses: []string{`{"approved":true,"reason":"resumed","retry_steps":[]}`}}
	tool := &testTool{}
	agent := newPlanAgent(root, provider, tool)
	session := &Session{ID: "session"}
	plan := &Plan{ID: "saved-plan", Description: "saved", Steps: []Step{
		{ID: "done", Description: "done", Tool: "test_tool", Arguments: map[string]any{}, Status: StepCompleted},
		{ID: "pending", Description: "pending", Tool: "test_tool", Arguments: map[string]any{}, Status: StepPending},
	}}
	if err := session.SetPlan(root, plan); err != nil {
		t.Fatal(err)
	}
	output, err := agent.ResumePlan(context.Background(), session)
	if err != nil || !strings.Contains(output, "approved") || tool.calls != 1 {
		t.Fatalf("unexpected resume: output=%q err=%v calls=%d", output, err, tool.calls)
	}
	active, err := session.ActivePlan(root)
	if err != nil || active.Steps[0].Status != StepCompleted || active.Steps[1].Status != StepCompleted {
		t.Fatalf("unexpected resumed plan: %#v, %v", active, err)
	}
}
