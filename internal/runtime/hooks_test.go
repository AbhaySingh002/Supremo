package runtime

import (
	"errors"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type beforeFn func(BeforeToolEvent) (BeforeToolDecision, error)
type afterFn func(AfterToolEvent) (AfterToolDecision, error)

func (f beforeFn) BeforeTool(e BeforeToolEvent) (BeforeToolDecision, error) { return f(e) }
func (f afterFn) AfterTool(e AfterToolEvent) (AfterToolDecision, error)     { return f(e) }

func TestBeforeToolShortCircuitsOnReusableResult(t *testing.T) {
	var order []string
	hooks := NewHookSet()
	hooks.AddBeforeTool(beforeFn(func(BeforeToolEvent) (BeforeToolDecision, error) {
		order = append(order, "a")
		return BeforeToolDecision{}, nil
	}))
	hooks.AddBeforeTool(beforeFn(func(BeforeToolEvent) (BeforeToolDecision, error) {
		order = append(order, "b")
		return BeforeToolDecision{Reused: true, Result: &tools.ToolResult{Success: true, Message: "cached"}}, nil
	}))
	hooks.AddBeforeTool(beforeFn(func(BeforeToolEvent) (BeforeToolDecision, error) {
		order = append(order, "c")
		return BeforeToolDecision{}, nil
	}))
	decision, err := hooks.RunBeforeTool(BeforeToolEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Result == nil || !decision.Reused || decision.Result.Message != "cached" {
		t.Fatalf("expected reusable result, got %#v", decision)
	}
	if got := join(order); got != "a,b" {
		t.Fatalf("order %s", got)
	}
}

func TestAfterToolAccumulatesNextStepInRegistrationOrder(t *testing.T) {
	var order []string
	hooks := NewHookSet()
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "a")
		return AfterToolDecision{NextStep: []models.Message{{Content: "A"}}}, nil
	}))
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "b")
		return AfterToolDecision{NextStep: []models.Message{{Content: "B"}}}, nil
	}))
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "c")
		return AfterToolDecision{NextStep: []models.Message{{Content: "C"}}}, nil
	}))
	decision, err := hooks.RunAfterTool(AfterToolEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if got := join(order); got != "a,b,c" {
		t.Fatalf("order %s", got)
	}
	if len(decision.NextStep) != 3 || decision.NextStep[0].Content != "A" || decision.NextStep[1].Content != "B" || decision.NextStep[2].Content != "C" {
		t.Fatalf("next step %#v", decision.NextStep)
	}
}

func TestAfterToolStopsOnErrorAfterCollectingPriorNextStep(t *testing.T) {
	var order []string
	hooks := NewHookSet()
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "a")
		return AfterToolDecision{NextStep: []models.Message{{Content: "A"}}}, nil
	}))
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "b")
		return AfterToolDecision{}, errors.New("boom")
	}))
	hooks.AddAfterTool(afterFn(func(AfterToolEvent) (AfterToolDecision, error) {
		order = append(order, "c")
		return AfterToolDecision{NextStep: []models.Message{{Content: "C"}}}, nil
	}))
	decision, err := hooks.RunAfterTool(AfterToolEvent{})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if got := join(order); got != "a,b" {
		t.Fatalf("order %s", got)
	}
	if len(decision.NextStep) != 1 || decision.NextStep[0].Content != "A" {
		t.Fatalf("next step %#v", decision.NextStep)
	}
}

func TestNilHookSetIsSafe(t *testing.T) {
	var hooks *HookSet
	if _, err := hooks.RunBeforeTool(BeforeToolEvent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.RunAfterTool(AfterToolEvent{}); err != nil {
		t.Fatal(err)
	}
	hooks.NotifyUserInput(UserInputRun)
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
