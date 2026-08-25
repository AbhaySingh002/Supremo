package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

	requestID := ""
	sessionID := ""
	taskID := ""
	turnID := ""
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

	var sb strings.Builder
	sb.WriteString("==================== EXACT AGENT TURN REQUEST START ====================\n")
	sb.WriteString(fmt.Sprintf("Timestamp:              %s\n", ts.Format(time.RFC3339Nano)))
	sb.WriteString(fmt.Sprintf("RequestID:              %s\n", requestID))
	sb.WriteString(fmt.Sprintf("SessionID:              %s\n", sessionID))
	sb.WriteString(fmt.Sprintf("TaskID:                 %s\n", taskID))
	sb.WriteString(fmt.Sprintf("TurnID:                 %s\n", turnID))
	sb.WriteString(fmt.Sprintf("Active Profile:         %s\n", profile))
	sb.WriteString(fmt.Sprintf("Provider:               %s\n", provider))
	sb.WriteString(fmt.Sprintf("Model:                  %s\n", model))
	sb.WriteString(fmt.Sprintf("Context Limit:          %d tokens\n", contextLimit))
	sb.WriteString(fmt.Sprintf("Estimated Input Tokens: %d tokens\n", estimatedInput))
	sb.WriteString(fmt.Sprintf("Output Reserve:         %d tokens\n", outputReserve))
	sb.WriteString(fmt.Sprintf("Stream:                 %v\n", params.Stream))
	sb.WriteString(fmt.Sprintf("Active Tools (%d):       %s\n", len(prompt.ActiveTools), strings.Join(prompt.ActiveTools, ", ")))

	if prompt.Request != nil && len(prompt.Request.Sections) > 0 {
		buckets := map[string]int{}
		total := 0
		for _, s := range prompt.Request.Sections {
			slot := s.SelectionReason
			if slot == "" {
				slot = "unspecified"
			}
			buckets[slot] += s.EstimatedTokens
			total += s.EstimatedTokens
		}
		sb.WriteString("\n-------------------- SLOT TOKEN TOTALS --------------------\n")
		for _, name := range []string{"control", "constraints", "focus", "verified_fact", "exact_source", "latest_failure", "latest_feedback", "user_turn", "conversation", "phase_tool"} {
			if n := buckets[name]; n > 0 {
				sb.WriteString(fmt.Sprintf("%s: %d\n", name, n))
				delete(buckets, name)
			}
		}
		for name, n := range buckets {
			sb.WriteString(fmt.Sprintf("%s: %d\n", name, n))
		}
		sb.WriteString(fmt.Sprintf("estimated_total: %d\n", total))
	}

	// 1. System Prompt
	if prompt.System != "" {
		sb.WriteString("\n-------------------- SANITIZED SYSTEM PROMPT --------------------\n")
		sb.WriteString(logging.Redact(prompt.System))
		sb.WriteString("\n")
	}

	// 2. Selected Context Sections Grouped by L0-L8 Layer
	if prompt.Request != nil && len(prompt.Request.Sections) > 0 {
		sb.WriteString("\n-------------------- SELECTED CONTEXT SECTIONS (L0-L8) --------------------\n")
		// Group sections by layer
		layerOrder := []string{"L0", "L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8"}
		layerNames := map[string]string{
			"L0": "Control (Instructions, Constraints, Objectives)",
			"L1": "Pinned (Requirements, Constraints, Decisions, Preferences)",
			"L2": "State (Task, Plan, WorkingMemory, Research Progress)",
			"L3": "Durable Observations (Verified Research Evidence)",
			"L4": "Repository (Architecture, Symbols, Structural Evidence)",
			"L5": "Exact Source (Active Source Snippets)",
			"L6": "Conversation (Causal Interactions)",
			"L7": "Observation (Runtime notices, tool feedback)",
			"L8": "Tools (Tool definitions & routing)",
		}

		byLayer := make(map[string][]models.ContextSection)
		for _, s := range prompt.Request.Sections {
			byLayer[s.Layer] = append(byLayer[s.Layer], s)
		}

		for _, l := range layerOrder {
			items, ok := byLayer[l]
			if !ok || len(items) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("\n[Layer %s: %s] (%d items):\n", l, layerNames[l], len(items)))
			for idx, item := range items {
				sb.WriteString(fmt.Sprintf("  (%d) ID: %s | Kind: %s | Authority: %s | Provenance: %s | Freshness: %s | Tokens: ~%d | Reason: %s\n",
					idx+1, item.ID, item.Kind, item.Authority, item.Provenance, item.Freshness, item.EstimatedTokens, item.SelectionReason))
				if item.SourceHash != "" || item.ArtifactID != "" {
					sb.WriteString(fmt.Sprintf("      SourceHash: %s | ArtifactID: %s\n", item.SourceHash, item.ArtifactID))
				}
				if item.Content != "" {
					sb.WriteString("      Content:\n")
					sb.WriteString(item.Content)
					sb.WriteString("\n")
				}
			}
		}
	}

	// 3. Rejected Candidates
	if prompt.Request != nil && len(prompt.Request.Rejected) > 0 {
		sb.WriteString("\n-------------------- REJECTED CANDIDATES --------------------\n")
		for i, rej := range prompt.Request.Rejected {
			signalsJSON, _ := json.Marshal(rej.Signals)
			sb.WriteString(fmt.Sprintf("[%d] Candidate: %s | Rejection Reason: %s | Signals: %s\n", i+1, rej.ID, rej.Reason, string(signalsJSON)))
		}
	}

	// 4. Causal Interactions
	if len(prompt.Interactions) > 0 {
		sb.WriteString(fmt.Sprintf("\n-------------------- CAUSAL INTERACTIONS (%d) --------------------\n", len(prompt.Interactions)))
		for i, inter := range prompt.Interactions {
			sb.WriteString(fmt.Sprintf("Interaction [%d] (ID: %s, Seq: %d):\n", i+1, inter.ID, inter.Sequence))
			if inter.TurnProgress != nil {
				sb.WriteString(fmt.Sprintf("  TurnProgress: Progress=%q NextGoal=%q Directives=%d\n",
					inter.TurnProgress.Progress, inter.TurnProgress.NextGoal, len(inter.TurnProgress.MemoryUpdates)))
			}
			if inter.Assistant.Content != "" {
				sb.WriteString(fmt.Sprintf("  Assistant Text: %s\n", inter.Assistant.Content))
			}
			for j, tc := range inter.Assistant.ToolCalls {
				sb.WriteString(fmt.Sprintf("  ToolCall[%d]: ID=%s Name=%s Args=%s\n", j+1, tc.ID, tc.Name, string(tc.Arguments)))
			}
			for k, tr := range inter.ToolResults {
				sb.WriteString(fmt.Sprintf("  ToolResult[%d]: ToolName=%s ToolCallID=%s Content=%s\n", k+1, tr.ToolName, tr.ToolCallID, tr.Content))
			}
		}
	}

	// 5. Final Canonical Messages List
	if len(prompt.Messages) > 0 {
		sb.WriteString(fmt.Sprintf("\n-------------------- CANONICAL MESSAGES (%d) --------------------\n", len(prompt.Messages)))
		for i, msg := range prompt.Messages {
			sb.WriteString(fmt.Sprintf("[%d] Role: %s", i+1, msg.Role))
			if msg.ToolName != "" || msg.ToolCallID != "" {
				sb.WriteString(fmt.Sprintf(" (ToolName: %s, ToolCallID: %s)", msg.ToolName, msg.ToolCallID))
			}
			sb.WriteString("\n")
			if msg.Content != "" {
				sb.WriteString(fmt.Sprintf("  Content: %s\n", msg.Content))
			}
			for j, tc := range msg.ToolCalls {
				sb.WriteString(fmt.Sprintf("  ToolCall[%d]: ID=%s Name=%s Args=%s\n", j+1, tc.ID, tc.Name, string(tc.Arguments)))
			}
			if msg.TurnProgress != nil {
				sb.WriteString(fmt.Sprintf("  TurnProgress: Progress=%q NextGoal=%q Directives=%d\n",
					msg.TurnProgress.Progress, msg.TurnProgress.NextGoal, len(msg.TurnProgress.MemoryUpdates)))
			}
		}
	}

	// 6. Tool Definitions
	if len(prompt.ToolDefinitions) > 0 {
		sb.WriteString(fmt.Sprintf("\n-------------------- TOOL DEFINITIONS (%d) --------------------\n", len(prompt.ToolDefinitions)))
		for i, td := range prompt.ToolDefinitions {
			schemaBytes, _ := json.Marshal(td.InputSchema)
			sb.WriteString(fmt.Sprintf("[%d] %s: %s\n    Schema: %s\n", i+1, td.Name, td.Description, string(schemaBytes)))
		}
	}

	sb.WriteString("==================== EXACT AGENT TURN REQUEST END ====================")
	logging.Debug("%s", sb.String())
}

// LogTurnResponse formats and logs the model completion and extracted progress.
func LogTurnResponse(completion *providers.Completion, parsed *parser.Response) {
	if !logging.IsEnabled() || completion == nil {
		return
	}

	var sb strings.Builder
	sb.WriteString("==================== EXACT AGENT TURN RESPONSE START ====================\n")
	sb.WriteString(fmt.Sprintf("Finish Reason: %s\n", completion.FinishReason))
	sb.WriteString(fmt.Sprintf("Token Usage:   Input=%d | Output=%d\n", completion.Usage.InputTokens, completion.Usage.OutputTokens))

	if completion.Text != "" {
		sb.WriteString("\n-------------------- ASSISTANT RAW CONTENT --------------------\n")
		sb.WriteString(completion.Text)
		sb.WriteString("\n")
	}

	// Parsed TurnProgress
	var tp *models.TurnProgress
	if parsed != nil && parsed.TurnProgress != nil {
		tp = parsed.TurnProgress
	} else {
		tp = parser.ExtractAssistantTurnProgress(completion.Text)
	}

	if tp != nil {
		sb.WriteString("\n-------------------- PARSED TURN PROGRESS --------------------\n")
		sb.WriteString(fmt.Sprintf("Progress:          %s\n", tp.Progress))
		sb.WriteString(fmt.Sprintf("Next Goal:         %s\n", tp.NextGoal))
		if len(tp.EvidenceUsed) > 0 {
			sb.WriteString(fmt.Sprintf("Evidence Used:     %s\n", strings.Join(tp.EvidenceUsed, ", ")))
		}
		if len(tp.MemoryUpdates) > 0 {
			sb.WriteString(fmt.Sprintf("Memory Directives (%d):\n", len(tp.MemoryUpdates)))
			for idx, dir := range tp.MemoryUpdates {
				sb.WriteString(fmt.Sprintf("  [%d] Op=%s Key=%q Statement=%q Evidence=%s\n",
					idx+1, dir.Operation, dir.Key, dir.Statement, strings.Join(dir.Evidence, ", ")))
			}
		}
	}

	// Native Tool Calls
	toolCalls := completion.ToolCalls
	if len(toolCalls) == 0 && parsed != nil {
		toolCalls = parsed.ToolCalls
	}
	if len(toolCalls) > 0 {
		sb.WriteString(fmt.Sprintf("\n-------------------- NATIVE TOOL CALLS (%d) --------------------\n", len(toolCalls)))
		for i, tc := range toolCalls {
			sb.WriteString(fmt.Sprintf("[%d] ToolCallID: %s | Name: %s\n    Arguments: %s\n", i+1, tc.ID, tc.Name, string(tc.Arguments)))
		}
	}

	sb.WriteString("==================== EXACT AGENT TURN RESPONSE END ====================")
	logging.Debug("%s", sb.String())
}

// ToolExecutionLogParams encapsulates all metadata needed to log a tool execution.
type ToolExecutionLogParams struct {
	ToolName              string
	ToolCallID            string
	RawArguments          string
	CanonicalArguments    string
	ExecutionMode         string // "physical" or "cached"
	ObservationID         string
	SourceHash            string
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

	var sb strings.Builder
	sb.WriteString("==================== TOOL EXECUTION LIFECYCLE ====================\n")
	sb.WriteString(fmt.Sprintf("Tool Name:              %s\n", params.ToolName))
	sb.WriteString(fmt.Sprintf("ToolCallID:             %s\n", params.ToolCallID))
	sb.WriteString(fmt.Sprintf("Execution Mode:         %s\n", params.ExecutionMode))
	sb.WriteString(fmt.Sprintf("Success:                %v\n", params.Success))
	sb.WriteString(fmt.Sprintf("Duration:               %v\n", params.Duration))
	sb.WriteString(fmt.Sprintf("ObservationID:          %s\n", params.ObservationID))
	sb.WriteString(fmt.Sprintf("Source Hash:            %s\n", params.SourceHash))
	sb.WriteString(fmt.Sprintf("ArtifactID:             %s\n", params.ArtifactID))
	sb.WriteString(fmt.Sprintf("Raw Arguments:          %s\n", params.RawArguments))
	if params.CanonicalArguments != "" && params.CanonicalArguments != params.RawArguments {
		sb.WriteString(fmt.Sprintf("Canonical Arguments:    %s\n", params.CanonicalArguments))
	}
	if params.Diagnostics != "" {
		sb.WriteString(fmt.Sprintf("Diagnostics/Error:      %s\n", params.Diagnostics))
	}
	if len(params.Mutations) > 0 {
		sb.WriteString(fmt.Sprintf("Mutations (%d):          %s\n", len(params.Mutations), strings.Join(params.Mutations, ", ")))
	}
	if len(params.FreshnessInvalidation) > 0 {
		sb.WriteString(fmt.Sprintf("Invalidated Scopes (%d): %s\n", len(params.FreshnessInvalidation), strings.Join(params.FreshnessInvalidation, ", ")))
	}
	sb.WriteString("==================================================================")
	if params.Success {
		logging.Info("%s", sb.String())
	} else {
		logging.Warn("%s", sb.String())
	}
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

	var sb strings.Builder
	sb.WriteString("==================== POST-TURN STATE TRANSITION ====================\n")
	sb.WriteString(fmt.Sprintf("SessionID:              %s | TaskID: %s | Turn: %d\n", params.SessionID, params.TaskID, params.TurnSequence))

	// Current Focus
	if params.CurrentFocus != nil {
		sb.WriteString("\n--- UPDATED CURRENT FOCUS ---\n")
		sb.WriteString(fmt.Sprintf("Established:            %s\n", params.CurrentFocus.Established))
		sb.WriteString(fmt.Sprintf("Next Goal:              %s\n", params.CurrentFocus.NextGoal))
		sb.WriteString(fmt.Sprintf("Last Action:            %s\n", params.CurrentFocus.LastAction))
		if len(params.CurrentFocus.Evidence) > 0 {
			sb.WriteString(fmt.Sprintf("Evidence:               %s\n", strings.Join(params.CurrentFocus.Evidence, ", ")))
		}
		if params.CurrentFocus.EvidenceStatus != "" {
			sb.WriteString(fmt.Sprintf("Evidence Status:        %s\n", params.CurrentFocus.EvidenceStatus))
		}
		if params.CurrentFocus.PreviousStrategy != "" {
			sb.WriteString(fmt.Sprintf("Previous Strategy:      %s\n", params.CurrentFocus.PreviousStrategy))
		}
		if params.CurrentFocus.LastFailure != "" {
			sb.WriteString(fmt.Sprintf("Last Failure:           %s\n", params.CurrentFocus.LastFailure))
		}
	}

	// Working Memory
	if params.WorkingMemory != nil {
		sb.WriteString("\n--- UPDATED WORKING MEMORY ---\n")
		sb.WriteString(fmt.Sprintf("Active Constraints (%d): %s\n", len(params.WorkingMemory.HardConstraints), strings.Join(params.WorkingMemory.HardConstraints, "; ")))
		sb.WriteString(fmt.Sprintf("Known Facts (%d):        %s\n", len(params.WorkingMemory.KnownRepositoryFacts), strings.Join(params.WorkingMemory.KnownRepositoryFacts, "; ")))
		sb.WriteString(fmt.Sprintf("Decisions Made (%d):     %s\n", len(params.WorkingMemory.AcceptedDecisions), strings.Join(params.WorkingMemory.AcceptedDecisions, "; ")))
		sb.WriteString(fmt.Sprintf("Evidence Artifacts (%d): %s\n", len(params.WorkingMemory.EvidenceArtifactIDs), strings.Join(params.WorkingMemory.EvidenceArtifactIDs, ", ")))
	}

	// Repository Changes
	if len(params.RepositoryChanges) > 0 {
		sb.WriteString("\n--- REPOSITORY MUTATIONS ---\n")
		for _, ch := range params.RepositoryChanges {
			sb.WriteString(fmt.Sprintf("  • %s\n", ch))
		}
	}

	// Request N+1 Conditioning
	if params.NextRequestReadiness != "" {
		sb.WriteString("\n--- REQUEST N+1 CONDITIONING ---\n")
		sb.WriteString(params.NextRequestReadiness)
		sb.WriteString("\n")
	}

	sb.WriteString("====================================================================")
	logging.Debug("%s", sb.String())
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
