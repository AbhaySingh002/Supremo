package agent

import (
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ProgressKind identifies a lifecycle event that an interactive client can render.
type ProgressKind string

const (
	ProgressIteration   ProgressKind = "iteration"
	ProgressRetry       ProgressKind = "retry"
	ProgressStream      ProgressKind = "stream"
	ProgressTool        ProgressKind = "tool"
	ProgressApproval    ProgressKind = "approval"
	ProgressSessionName ProgressKind = "session_name"
	ProgressPhase       ProgressKind = "phase"
	ProgressCompletion  ProgressKind = "completion"
	ProgressChecklist   ProgressKind = "checklist"
	ProgressCheckpoint  ProgressKind = "checkpoint"
	ProgressDebug       ProgressKind = "debug"
	ProgressActivity    ProgressKind = "activity"
)

// ProgressEvent is immutable UI-facing state emitted by the agent worker.
type ProgressEvent struct {
	Kind       ProgressKind
	Message    string
	SessionID  string
	Phase      string
	Iteration  int
	Tool       string
	ToolStatus string
	Arguments  string
	ToolOutput string
	Diff       string
	StepID     string
	Checklist  *models.TaskChecklist
	Checkpoint *tools.CheckpointSummary
}

func (a *Agent) emit(event ProgressEvent) {
	if a.progress != nil {
		a.progress(event)
	}
}

func (a *Agent) reportTool(event tools.Event) {
	if event.Checkpoint != nil {
		a.emit(ProgressEvent{Kind: ProgressCheckpoint, Tool: event.Tool, Checkpoint: event.Checkpoint, SessionID: event.SessionID, StepID: event.TaskID})
		return
	}
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
		Diff:       event.Diff,
		SessionID:  event.SessionID,
		StepID:     event.TaskID,
	})
}
