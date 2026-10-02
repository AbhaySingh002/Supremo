package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

// TurnRequestLogParams encapsulates all metadata needed to log an exact model request.
type TurnRequestLogParams struct {
	Session   *Session
	Prompt    *models.Prompt
	Provider  string
	Model     string
	Stream    bool
	Timestamp time.Time
}

// LogTurnRequest formats and logs the complete model request lifecycle in debug mode.
func LogTurnRequest(params TurnRequestLogParams) {
	if !logging.IsEnabled() || params.Prompt == nil {
		return
	}

	prompt := params.Prompt
	session := params.Session
	ts := params.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	requestID, sessionID, taskID, turnID := "", "", "", ""
	profile := prompt.Metadata.Profile
	contextLimit := 0
	estimatedInput := prompt.EstimatedInputTokens
	outputReserve := prompt.OutputReserve
	if prompt.Request != nil {
		requestID = prompt.Request.RequestID
		sessionID = prompt.Request.SessionID
		taskID = prompt.Request.TaskID
		turnID = prompt.Request.TurnID
		if prompt.Request.Profile != "" {
			profile = prompt.Request.Profile
		}
		contextLimit = prompt.Request.Budget.ContextLimit
		if estimatedInput == 0 {
			estimatedInput = prompt.Request.Budget.EstimatedUsed
		}
		if outputReserve == 0 {
			outputReserve = prompt.Request.Budget.OutputReserve
		}
	} else if session != nil {
		sessionID = session.ID
		taskID = session.ActiveTaskID
	}
	provider := params.Provider
	model := params.Model
	if provider == "" && session != nil {
		provider = session.Provider
	}
	if model == "" && session != nil {
		model = session.Model
	}

	var sections []map[string]any
	if prompt.Request != nil {
		sections = make([]map[string]any, 0, len(prompt.Request.Sections))
		for _, s := range prompt.Request.Sections {
			sections = append(sections, map[string]any{
				"id": s.ID, "kind": s.Kind, "layer": s.Layer, "authority": s.Authority,
				"provenance": s.Provenance, "freshness": s.Freshness, "tokens": s.EstimatedTokens,
				"reason": s.SelectionReason, "source_hash": s.SourceHash, "artifact_id": s.ArtifactID,
			})
		}
	}

	view := struct {
		Timestamp       string                    `json:"timestamp"`
		RequestID       string                    `json:"request_id,omitempty"`
		SessionID       string                    `json:"session_id,omitempty"`
		TaskID          string                    `json:"task_id,omitempty"`
		TurnID          string                    `json:"turn_id,omitempty"`
		Profile         string                    `json:"profile"`
		Provider        string                    `json:"provider,omitempty"`
		Model           string                    `json:"model,omitempty"`
		ContextLimit    int                       `json:"context_limit,omitempty"`
		EstimatedInput  int                       `json:"estimated_input_tokens"`
		OutputReserve   int                       `json:"output_reserve,omitempty"`
		Stream          bool                      `json:"stream"`
		ActiveTools     []string                  `json:"active_tools,omitempty"`
		System          string                    `json:"system,omitempty"`
		Sections        []map[string]any          `json:"sections,omitempty"`
		Rejected        []models.ContextRejection `json:"rejected,omitempty"`
		Interactions    []models.Interaction      `json:"interactions,omitempty"`
		Messages        []models.Message          `json:"messages,omitempty"`
		ToolDefinitions []models.ToolDefinition   `json:"tool_definitions,omitempty"`
	}{
		Timestamp: ts.Format(time.RFC3339Nano), RequestID: requestID, SessionID: sessionID, TaskID: taskID, TurnID: turnID,
		Profile: profile, Provider: provider, Model: model, ContextLimit: contextLimit,
		EstimatedInput: estimatedInput, OutputReserve: outputReserve, Stream: params.Stream,
		ActiveTools: prompt.ActiveTools, System: logging.Redact(prompt.System),
		Sections: sections, Messages: prompt.Messages, ToolDefinitions: prompt.ToolDefinitions,
	}
	if prompt.Request != nil {
		view.Rejected = prompt.Request.Rejected
	}
	view.Interactions = prompt.Interactions
	logJSON("turn request", view)
}

// LogTurnResponse formats and logs the model completion and extracted progress.
func LogTurnResponse(completion *providers.Completion, parsed *parser.Response) {
	if !logging.IsEnabled() || completion == nil {
		return
	}
	var tp *models.TurnProgress
	if parsed != nil && parsed.TurnProgress != nil {
		tp = parsed.TurnProgress
	} else {
		tp = parser.ExtractAssistantTurnProgress(completion.Text)
	}
	toolCalls := completion.ToolCalls
	if len(toolCalls) == 0 && parsed != nil {
		toolCalls = parsed.ToolCalls
	}
	logJSON("turn response", struct {
		FinishReason string               `json:"finish_reason"`
		InputTokens  int                  `json:"input_tokens"`
		OutputTokens int                  `json:"output_tokens"`
		Text         string               `json:"text,omitempty"`
		TurnProgress *models.TurnProgress `json:"turn_progress,omitempty"`
		ToolCalls    []models.ToolCall    `json:"tool_calls,omitempty"`
	}{completion.FinishReason, completion.Usage.InputTokens, completion.Usage.OutputTokens, completion.Text, tp, toolCalls})
}

// ToolExecutionLogParams encapsulates all metadata needed to log a tool execution.
type ToolExecutionLogParams struct {
	ToolName              string
	ToolCallID            string
	RawArguments          string
	CanonicalArguments    string
	ExecutionMode         string // "physical" or "cached"
	ArtifactID            string
	Success               bool
	Diagnostics           string
	Duration              time.Duration
	Mutations             []string
	FreshnessInvalidation []string
}

// LogToolExecution formats and logs physical or cached tool execution.
func LogToolExecution(params ToolExecutionLogParams) {
	if !logging.IsEnabled() {
		return
	}
	if params.Success {
		logJSON("tool execution", params)
		return
	}
	data, _ := json.Marshal(params)
	logging.Warn("tool execution: %s", data)
}

// StateTransitionLogParams encapsulates all metadata needed to log post-turn state transitions.
type StateTransitionLogParams struct {
	SessionID            string
	TaskID               string
	TurnSequence         int
	WorkingMemory        *WorkingMemory
	CurrentFocus         *CurrentFocus
	RepositoryChanges    []string
	NextRequestReadiness string
}

// LogStateTransition logs how the turn outcome changed WorkingMemory, CurrentFocus, and conditions Request N+1.
func LogStateTransition(params StateTransitionLogParams) {
	if !logging.IsEnabled() {
		return
	}
	logJSON("state transition", params)
}

// LogPostTurnStateTransition is a helper to extract and log state transitions from session and memory stores.
func (a *Agent) LogPostTurnStateTransition(session *Session, taskID string, turnSeq int, repoChanges []string, nextGoal string) {
	if !logging.IsEnabled() || session == nil {
		return
	}

	var wm *WorkingMemory
	var cf *CurrentFocus
	if mgr := a.WorkingMemory(); mgr != nil {
		if loaded, err := mgr.Load(context.Background(), session.ID, taskID); err == nil && loaded != nil {
			wm = loaded
			cf = loaded.CurrentFocus
		}
	}

	readiness := "Ready for next canonical model turn."
	if nextGoal != "" {
		readiness = fmt.Sprintf("Next turn conditioned on unresolved goal: %q", nextGoal)
	}

	LogStateTransition(StateTransitionLogParams{
		SessionID:            session.ID,
		TaskID:               taskID,
		TurnSequence:         turnSeq,
		WorkingMemory:        wm,
		CurrentFocus:         cf,
		RepositoryChanges:    repoChanges,
		NextRequestReadiness: readiness,
	})
}

func observationRepoChanges(observations []Observation) []string {
	var changes []string
	for _, obs := range observations {
		if obs.Result == nil {
			continue
		}
		for _, entity := range obs.Result.AffectedEntities {
			if entity.Path == "" {
				continue
			}
			if entity.Kind != "" {
				changes = append(changes, entity.Kind+":"+entity.Path)
			} else {
				changes = append(changes, entity.Path)
			}
		}
		if obs.Result.WorldRevision != "" {
			changes = append(changes, "world_revision:"+obs.Result.WorldRevision)
		}
	}
	return changes
}

func nextGoalFrom(parsed *parser.Response) string {
	if parsed != nil && parsed.TurnProgress != nil {
		return parsed.TurnProgress.NextGoal
	}
	return ""
}

// logJSON marshals one debug event and emits it as a single line-bearing record.
func logJSON(event string, view any) {
	data, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		logging.Debug("%s: unavailable (%v)", event, err)
		return
	}
	logging.Debug("%s: %s", event, data)
}
