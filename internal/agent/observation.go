package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Observation represents the result of a tool execution formatted for LLM context.
type Observation struct {
	ToolName   string            `json:"tool_name"`
	Success    bool              `json:"success"`
	Status     tools.ToolStatus  `json:"status"`
	ArtifactID string            `json:"artifact_id,omitempty"`
	Output     string            `json:"output"`
	Result     *tools.ToolResult `json:"-"`
}

// NewObservation converts a ToolResult or an execution error into a standard Observation.
func NewObservation(toolName string, result *tools.ToolResult, err error) Observation {
	if err != nil {
		return buildObservation(toolName, tools.ToolStatusFailed, false, "", nil, "", err)
	}
	if result == nil {
		return buildObservation(toolName, tools.ToolStatusFailed, false, "no execution result returned from tool", nil, "", nil)
	}
	payload := struct {
		Tool   string            `json:"tool"`
		Result *tools.ToolResult `json:"result"`
	}{Tool: toolName, Result: result}
	out, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		return buildObservation(toolName, result.Status, result.Success, result.Message, nil, result.ArtifactID, nil)
	}
	return Observation{ToolName: toolName, Success: result.Success, Status: result.Status, ArtifactID: result.ArtifactID, Output: string(out), Result: result}
}

func buildObservation(toolName string, status tools.ToolStatus, success bool, message string, data map[string]interface{}, artifactID string, err error) Observation {
	payload := struct {
		Tool       string                 `json:"tool"`
		Success    bool                   `json:"success"`
		Status     tools.ToolStatus       `json:"status"`
		Message    string                 `json:"message"`
		Data       map[string]interface{} `json:"data,omitempty"`
		Error      string                 `json:"error,omitempty"`
		ArtifactID string                 `json:"artifact_id,omitempty"`
	}{
		Tool:       toolName,
		Success:    success,
		Status:     status,
		Message:    message,
		Data:       data,
		ArtifactID: artifactID,
	}
	if err != nil {
		payload.Error = err.Error()
	}

	out, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		// Fallback to plain text if JSON marshal fails
		return Observation{
			ToolName:   toolName,
			Success:    success,
			Status:     status,
			ArtifactID: artifactID,
			Output:     fmt.Sprintf("Tool: %s\nSuccess: %v\nMessage: %s", toolName, success, message),
		}
	}

	return Observation{
		ToolName:   toolName,
		Success:    success,
		Status:     status,
		ArtifactID: artifactID,
		Output:     string(out),
	}
}

// ExtractObservationSummary extracts a concise summary from a tool result.
func ExtractObservationSummary(toolName, path string, result *tools.ToolResult, rawOutput string, root string) (string, bool, string) {
	var data map[string]any
	success := false
	msg := ""
	if result != nil {
		data = result.Data
		success = result.Success
		msg = result.Message
		if result.Error != nil && result.Error.Message != "" {
			msg = result.Error.Message
		}
	}
	return state.ExtractObservationSummary(toolName, path, data, success, msg, rawOutput, root)
}

func mutatingInvalidation(scope string, obs Observation) []string {
	if scope != "" {
		return []string{scope}
	}
	if obs.Result == nil {
		return nil
	}
	var scopes []string
	for _, entity := range obs.Result.AffectedEntities {
		if entity.Path != "" {
			scopes = append(scopes, entity.Path)
		}
	}
	return scopes
}

func logResolvedToolExecution(call models.ToolCall, canonical string, mode string, obs Observation, observationID, sourceHash string, err error, duration time.Duration, invalidation []string) {
	diagnostics := ""
	var mutations []string
	artifactID := obs.ArtifactID
	if obs.Result != nil {
		if artifactID == "" {
			artifactID = obs.Result.ArtifactID
		}
		for _, d := range obs.Result.Diagnostics {
			if diagnostics != "" {
				diagnostics += "; "
			}
			diagnostics += d.Code + ": " + d.Message
		}
		if obs.Result.Error != nil && obs.Result.Error.Message != "" {
			if diagnostics != "" {
				diagnostics += "; "
			}
			diagnostics += obs.Result.Error.Message
		} else if obs.Result.Message != "" && !obs.Success {
			if diagnostics != "" {
				diagnostics += "; "
			}
			diagnostics += obs.Result.Message
		}
		for _, entity := range obs.Result.AffectedEntities {
			label := entity.Kind
			if entity.Path != "" {
				if label != "" {
					label += ":"
				}
				label += entity.Path
			}
			if label != "" {
				mutations = append(mutations, label)
			}
		}
	}
	if err != nil {
		if diagnostics != "" {
			diagnostics += "; "
		}
		diagnostics += err.Error()
	}
	LogToolExecution(ToolExecutionLogParams{
		ToolName:              call.Name,
		ToolCallID:            call.ID,
		RawArguments:          string(call.Arguments),
		CanonicalArguments:    canonical,
		ExecutionMode:         mode,
		ObservationID:         observationID,
		SourceHash:            sourceHash,
		ArtifactID:            artifactID,
		Success:               obs.Success && err == nil,
		Diagnostics:           diagnostics,
		Duration:              duration,
		Mutations:             mutations,
		FreshnessInvalidation: invalidation,
	})
}
