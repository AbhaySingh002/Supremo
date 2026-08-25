package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/api"
)

type progressKind string

const (
	progressIteration   progressKind = "iteration"
	progressRetry       progressKind = "retry"
	progressStream      progressKind = "stream"
	progressTool        progressKind = "tool"
	progressApproval    progressKind = "approval"
	progressSessionName progressKind = "session_name"
	progressPhase       progressKind = "phase"
	progressCompletion  progressKind = "completion"
	progressChecklist   progressKind = "checklist"
	progressCheckpoint  progressKind = "checkpoint"
	progressDebug       progressKind = "debug"
	progressActivity    progressKind = "activity"
	progressError       progressKind = "error"
)

type progressEvent struct {
	Kind       progressKind
	Message    string
	SessionID  string
	Phase      string
	Iteration  int
	Tool       string
	ToolStatus string
	Arguments  string
	ToolOutput string
	Diff       string
	Turn       int
	Step       int
	CallID     string
	Todos      []api.TodoItem
	Checkpoint *api.Checkpoint
}

func progressFromAPI(event api.Event) []progressEvent {
	switch event.Type {
	case api.EventAssistantChunk:
		var payload api.AssistantChunk
		if json.Unmarshal(event.Data, &payload) == nil && payload.Event.Type == "text_delta" && payload.Event.TextDelta != "" {
			return []progressEvent{{Kind: progressStream, Message: payload.Event.TextDelta, SessionID: event.SessionID}}
		}
	case api.EventToolCall:
		var payload api.ToolCall
		if json.Unmarshal(event.Data, &payload) == nil {
			turn, step, callID := payload.Turn, payload.Step, payload.CallID
			if event.Turn != 0 {
				turn = event.Turn
			}
			if event.Step != 0 {
				step = event.Step
			}
			if event.CallID != "" {
				callID = event.CallID
			}
			return []progressEvent{{Kind: progressTool, SessionID: event.SessionID, Turn: turn, Step: step, CallID: callID, Tool: payload.Tool, ToolStatus: "running", Arguments: payload.Arguments}}
		}
	case api.EventToolResult:
		var payload api.ToolResult
		if json.Unmarshal(event.Data, &payload) == nil {
			status := "completed"
			if toolResultFailed(payload.Content) {
				status = "failed"
			}
			return []progressEvent{{Kind: progressTool, SessionID: event.SessionID, Turn: event.Turn, Step: event.Step, CallID: payload.ToolCallID, Tool: payload.ToolName, ToolStatus: status, ToolOutput: payload.Content}}
		}
	case api.EventTodoWrite:
		var payload api.TodoUpdate
		if json.Unmarshal(event.Data, &payload) == nil {
			return []progressEvent{{Kind: progressChecklist, SessionID: event.SessionID, Todos: payload.Todos}}
		}
	case api.EventCheckpointAvailable:
		var payload api.CheckpointAvailable
		if json.Unmarshal(event.Data, &payload) == nil {
			return []progressEvent{{Kind: progressCheckpoint, SessionID: event.SessionID, Tool: payload.Tool, Checkpoint: &payload.Checkpoint}}
		}
	case api.EventRetry:
		var payload api.RetryDetail
		_ = json.Unmarshal(event.Data, &payload)
		message := "Provider retry scheduled"
		if payload.Code != "" {
			message += " after " + payload.Code
		}
		if payload.DelayMillis > 0 {
			message += fmt.Sprintf(" in %dms", payload.DelayMillis)
		}
		return []progressEvent{{Kind: progressRetry, SessionID: event.SessionID, Message: message + "."}}
	case api.EventError:
		var payload api.ErrorDetail
		if json.Unmarshal(event.Data, &payload) == nil && payload.Message != "" {
			return []progressEvent{{Kind: progressError, SessionID: event.SessionID, Message: payload.Message}}
		}
	case api.EventFinish:
		var payload api.FinishDetail
		_ = json.Unmarshal(event.Data, &payload)
		message := "Provider response finished"
		if payload.FinishReason != "" {
			message += " · " + payload.FinishReason
		}
		return []progressEvent{{Kind: progressPhase, SessionID: event.SessionID, Phase: "finalizing", Message: message}}
	case api.EventTurnStart:
		return []progressEvent{{Kind: progressPhase, SessionID: event.SessionID, Phase: "thinking", Message: "Preparing response"}}
	case api.EventStepStart:
		return []progressEvent{{Kind: progressPhase, SessionID: event.SessionID, Phase: "working"}}
	case api.EventTurnEnd:
		return []progressEvent{{Kind: progressPhase, SessionID: event.SessionID, Phase: "finalizing", Message: "Finalizing response"}}
	case api.EventRunEnd:
		return []progressEvent{{Kind: progressCompletion, SessionID: event.SessionID}}
	}
	return nil
}

func toolResultFailed(content string) bool {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return false
	}
	var failed func(any) (bool, bool)
	failed = func(value any) (bool, bool) {
		object, ok := value.(map[string]any)
		if !ok {
			return false, false
		}
		if success, ok := object["success"].(bool); ok {
			return !success, true
		}
		if status, ok := object["status"].(string); ok {
			switch strings.ToLower(status) {
			case "failed", "denied", "canceled", "cancelled", "timed_out":
				return true, true
			case "completed", "success", "succeeded":
				return false, true
			}
		}
		for _, key := range []string{"result", "data", "payload"} {
			if nested, exists := object[key]; exists {
				if result, known := failed(nested); known {
					return result, true
				}
			}
		}
		return false, false
	}
	result, _ := failed(value)
	return result
}
