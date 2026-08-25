package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type scheduledProbe struct {
	name    string
	started chan string
	mu      sync.Mutex
	release map[string]chan struct{}
}

func (p *scheduledProbe) Name() string                      { return p.name }
func (p *scheduledProbe) Description() string               { return p.name }
func (p *scheduledProbe) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }
func (p *scheduledProbe) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}
}
func (p *scheduledProbe) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	p.started <- args.ID
	p.mu.Lock()
	release := p.release[args.ID]
	p.mu.Unlock()
	select {
	case <-release:
		return tools.BuildToolResult(true, args.ID, nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestParallelSchedulerOverlapsSafeCallsAndKeepsBarriersAndResultOrder(t *testing.T) {
	started := make(chan string, 4)
	releases := map[string]chan struct{}{
		"first": make(chan struct{}), "second": make(chan struct{}), "barrier": make(chan struct{}),
	}
	parallel := &scheduledProbe{name: "parallel", started: started, release: releases}
	exclusive := &scheduledProbe{name: "exclusive", started: started, release: releases}
	registry := tools.NewRegistry()
	if err := registry.Register(parallel, tools.ToolMetadata{CanonicalName: "parallel", Family: "test", CapabilityTags: []string{"test"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, ParallelSafe: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(exclusive, tools.ToolMetadata{CanonicalName: "exclusive", Family: "test", CapabilityTags: []string{"test"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolManager: tools.NewManager(registry), hooks: nil, maxParallelTools: 2}
	calls := []models.ToolCall{
		{ID: "c1", Name: "parallel", Arguments: json.RawMessage(`{"id":"first"}`)},
		{ID: "c2", Name: "parallel", Arguments: json.RawMessage(`{"id":"second"}`)},
		{ID: "c3", Name: "exclusive", Arguments: json.RawMessage(`{"id":"barrier"}`)},
	}
	done := make(chan ToolExecutionSummary, 1)
	var committed []string
	go func() {
		done <- a.executeAll(context.Background(), nil, calls, ToolExecutionOptions{AfterTool: func(call models.ToolCall, _ Observation) error {
			committed = append(committed, call.ID)
			return nil
		}})
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("parallel calls did not overlap")
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("unexpected parallel starts: %#v", seen)
	}
	select {
	case id := <-started:
		t.Fatalf("exclusive barrier started before the parallel group drained: %s", id)
	default:
	}
	close(releases["second"])
	close(releases["first"])
	select {
	case id := <-started:
		if id != "barrier" {
			t.Fatalf("unexpected barrier start %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("exclusive barrier never started")
	}
	close(releases["barrier"])
	summary := <-done
	if summary.Err != nil || len(summary.Results) != 3 {
		t.Fatalf("scheduler result: %#v", summary)
	}
	for i, id := range []string{"c1", "c2", "c3"} {
		if summary.Results[i].CallID != id {
			t.Fatalf("results not committed in model order: %#v", summary.Results)
		}
	}
	if got := strings.Join(committed, ","); got != "c1,c2,c3" {
		t.Fatalf("post-commit hooks not model ordered: %s", got)
	}
}

func TestParallelSchedulerUsesBoundedRollingPool(t *testing.T) {
	started := make(chan string, 5)
	releases := make(map[string]chan struct{}, 5)
	var calls []models.ToolCall
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("call-%d", i)
		releases[id] = make(chan struct{})
		calls = append(calls, models.ToolCall{ID: id, Name: "parallel", Arguments: json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))})
	}
	probe := &scheduledProbe{name: "parallel", started: started, release: releases}
	registry := tools.NewRegistry()
	if err := registry.Register(probe, tools.ToolMetadata{CanonicalName: "parallel", Family: "test", CapabilityTags: []string{"test"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, ParallelSafe: true}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolManager: tools.NewManager(registry), maxParallelTools: 2}
	done := make(chan ToolExecutionSummary, 1)
	go func() { done <- a.executeAll(context.Background(), nil, calls, ToolExecutionOptions{}) }()

	first, second := <-started, <-started
	select {
	case id := <-started:
		t.Fatalf("pool exceeded limit before a slot opened: %s", id)
	case <-time.After(50 * time.Millisecond):
	}
	close(releases[second])
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("rolling pool did not replenish")
	}
	for id, release := range releases {
		if id != second {
			close(release)
		}
	}
	if summary := <-done; summary.Err != nil || len(summary.Results) != len(calls) {
		t.Fatalf("bounded scheduler result = %#v (first=%s)", summary, first)
	}
}

func TestParallelSchedulerCancellationDrainsStartedAndSynthesizesUnstarted(t *testing.T) {
	started := make(chan string, 4)
	releases := map[string]chan struct{}{}
	var calls []models.ToolCall
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("call-%d", i)
		releases[id] = make(chan struct{})
		calls = append(calls, models.ToolCall{ID: id, Name: "parallel", Arguments: json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))})
	}
	probe := &scheduledProbe{name: "parallel", started: started, release: releases}
	registry := tools.NewRegistry()
	if err := registry.Register(probe, tools.ToolMetadata{CanonicalName: "parallel", Family: "test", CapabilityTags: []string{"test"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, ParallelSafe: true}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolManager: tools.NewManager(registry), maxParallelTools: 2}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ToolExecutionSummary, 1)
	go func() { done <- a.executeAll(ctx, nil, calls, ToolExecutionOptions{}) }()
	<-started
	<-started
	cancel()
	select {
	case summary := <-done:
		if summary.Outcome != tools.ToolOutcomeCancelled || len(summary.Results) != len(calls) {
			t.Fatalf("cancelled scheduler = %#v", summary)
		}
		for i, result := range summary.Results {
			if result.CallID != calls[i].ID {
				t.Fatalf("cancelled results not ordered: %#v", summary.Results)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("started calls were not drained after cancellation")
	}
}

func TestRecoverableToolFailureStopsLaterExclusiveDispatch(t *testing.T) {
	failing := &probeTool{name: "recoverable", fn: func(context.Context) (*tools.ToolResult, error) {
		return &tools.ToolResult{Status: tools.ToolStatusFailed, Retryable: true, Message: "retry after inspection"}, nil
	}}
	laterRuns := 0
	later := &probeTool{name: "later", fn: func(context.Context) (*tools.ToolResult, error) {
		laterRuns++
		return tools.BuildToolResult(true, "must not run", nil), nil
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(failing); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(later); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolManager: tools.NewManager(registry)}
	summary := a.executeAll(context.Background(), nil, []models.ToolCall{
		{ID: "failed", Name: failing.Name(), Arguments: json.RawMessage(`{}`)},
		{ID: "unstarted", Name: later.Name(), Arguments: json.RawMessage(`{}`)},
	}, ToolExecutionOptions{})
	if summary.Outcome != tools.ToolOutcomeRecoverable || laterRuns != 0 || len(summary.Results) != 2 {
		t.Fatalf("recoverable barrier = %#v, later runs=%d", summary, laterRuns)
	}
	if !strings.Contains(summary.Results[1].Output, "aborted before dispatch") {
		t.Fatalf("unstarted result is not causal: %#v", summary.Results[1])
	}
}
