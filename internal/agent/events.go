package agent

import "github.com/AbhaySingh002/supremo/internal/tools"

// ProgressKind identifies a lifecycle event that an interactive client can render.
type ProgressKind string

const (
	ProgressIteration  ProgressKind = "iteration"
	ProgressStream     ProgressKind = "stream"
	ProgressTool       ProgressKind = "tool"
	ProgressApproval   ProgressKind = "approval"
	ProgressPlan       ProgressKind = "plan"
	ProgressPlanStep   ProgressKind = "plan_step"
	ProgressPhase      ProgressKind = "phase"
	ProgressCompletion ProgressKind = "completion"
	ProgressDebug      ProgressKind = "debug"
)

// ProgressEvent is immutable UI-facing state emitted by the agent worker.
type ProgressEvent struct {
	Kind       ProgressKind
	Message    string
	Phase      string
	Iteration  int
	Tool       string
	ToolStatus string
	Arguments  string
	ToolOutput string
	StepID     string
	Plan       *Plan
}

func (a *Agent) emit(event ProgressEvent) {
	if a.progress != nil {
		a.progress(event)
	}
}

func (a *Agent) reportTool(event tools.Event) {
	kind := ProgressTool
	if event.Status == "waiting approval" || event.Status == "approved" || event.Status == "denied" {
		kind = ProgressApproval
	}
	a.emit(ProgressEvent{
		Kind:       kind,
		Message:    event.Message,
		Tool:       event.Tool,
		ToolStatus: event.Status,
		Arguments:  event.Arguments,
		ToolOutput: event.Output,
	})
}

func clonePlan(plan *Plan) *Plan {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Steps = append([]Step(nil), plan.Steps...)
	return &copy
}
