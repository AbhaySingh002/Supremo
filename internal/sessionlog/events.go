// Package sessionlog owns Supremo's durable, session-local execution log.
// It is intentionally internal: the agent loop remains the only owner of
// completion, context construction, and execution policy.
package sessionlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/state"
)

// EventType names a durable session event category.
type EventType string

const (
	EventUserMessage        EventType = "user/message"
	EventAssistantMessage   EventType = "assistant/message"
	EventToolResult         EventType = "tool/result"
	EventTurnStart          EventType = "turn/start"
	EventTurnEnd            EventType = "turn/end"
	EventStepStart          EventType = "step/start"
	EventStepEnd            EventType = "step/end"
	EventRequestHeader      EventType = "request/header"
	EventRequestContext     EventType = "request/context"
	EventAssistantChunk     EventType = "assistant/chunk"
	EventUsage              EventType = "usage"
	EventFinish             EventType = "finish"
	EventError              EventType = "error"
	EventRetry              EventType = "retry"
	EventToolCall           EventType = "tool/call"
	EventTodoWrite          EventType = "todo/write"
	EventPlanMode           EventType = "plan/mode"
	EventCompactionPrune    EventType = "compaction/prune"
	EventCompactionStart    EventType = "compaction/start"
	EventCompactionSummary  EventType = "compaction/summary"
	EventCompactionEnd      EventType = "compaction/end"
	EventSubagentDescriptor EventType = "subagent/descriptor"
	EventSubagentQueued     EventType = "subagent/message.queued"
	EventSubagentRunStart   EventType = "subagent/run.start"
	EventSubagentRunEnd     EventType = "subagent/run.end"
	EventRunQueued          EventType = "run/message.queued"
	EventRunStart           EventType = "run/start"
	EventRunEnd             EventType = "run/end"
	EventInteractionRequest EventType = "interaction/requested"
	EventInteractionResolve EventType = "interaction/resolved"
)

const CurrentVersion = 2

const (
	SurfaceAppend  = "append"
	SurfaceReplace = "replace"
)

// SurfaceOp is the only model-surface mutation recorded by an event.
type SurfaceOp struct {
	Kind     string `json:"kind"`
	StartSeq int64  `json:"start_seq,omitempty"`
	EndSeq   int64  `json:"end_seq,omitempty"`
}

type TurnStartPayload struct {
	Turn int `json:"turn"`
}

type TurnEndPayload struct {
	Turn   int    `json:"turn"`
	Reason string `json:"reason"`
}

type StepStartPayload struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

type StepEndPayload struct {
	Turn   int    `json:"turn"`
	Step   int    `json:"step"`
	Reason string `json:"reason,omitempty"`
}

type RequestContextPayload struct {
	Turn              int      `json:"turn"`
	Step              int      `json:"step"`
	Attempt           int      `json:"attempt"`
	Provider          string   `json:"provider,omitempty"`
	Model             string   `json:"model,omitempty"`
	Profile           string   `json:"profile,omitempty"`
	MessageEventSeqs  []int64  `json:"message_event_seqs,omitempty"`
	SystemDigest      string   `json:"system_digest,omitempty"`
	ToolSchemaDigest  string   `json:"tool_schema_digest,omitempty"`
	RequestDigest     string   `json:"request_digest,omitempty"`
	HeaderDigest      string   `json:"header_digest,omitempty"`
	PromptArtifactID  string   `json:"prompt_artifact_id,omitempty"`
	SelectedToolNames []string `json:"selected_tool_names,omitempty"`
}

type RequestHeaderPayload struct {
	Turn             int    `json:"turn"`
	Step             int    `json:"step"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	Profile          string `json:"profile,omitempty"`
	HeaderDigest     string `json:"header_digest"`
	SystemDigest     string `json:"system_digest"`
	ToolSchemaDigest string `json:"tool_schema_digest"`
	Reason           string `json:"reason"`
}

// AssistantChunkPayload contains one provider-neutral, non-reasoning stream event.
type AssistantChunkPayload struct {
	Turn    int                   `json:"turn"`
	Step    int                   `json:"step"`
	Attempt int                   `json:"attempt"`
	Event   providers.StreamEvent `json:"event"`
}

type UsagePayload struct {
	Turn    int             `json:"turn"`
	Step    int             `json:"step"`
	Attempt int             `json:"attempt"`
	Usage   providers.Usage `json:"usage"`
}

type FinishPayload struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	Attempt      int    `json:"attempt"`
	FinishReason string `json:"finish_reason"`
}

type ErrorPayload struct {
	Turn    int    `json:"turn"`
	Step    int    `json:"step"`
	Attempt int    `json:"attempt"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type RetryPayload struct {
	Turn        int    `json:"turn"`
	Step        int    `json:"step"`
	Attempt     int    `json:"attempt"`
	DelayMillis int64  `json:"delay_millis"`
	Code        string `json:"code,omitempty"`
}

type ToolCallPayload struct {
	Turn      int    `json:"turn"`
	Step      int    `json:"step"`
	CallID    string `json:"call_id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Skipped   bool   `json:"skipped,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoWritePayload struct {
	Todos []TodoItem `json:"todos"`
}
type PlanModePayload struct {
	Active bool `json:"active"`
}

type CompactionPayload struct {
	CompactionID    string  `json:"compaction_id,omitempty"`
	Provider        string  `json:"provider,omitempty"`
	Model           string  `json:"model,omitempty"`
	FinishReason    string  `json:"finish_reason,omitempty"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	StartSeq        int64   `json:"start_seq,omitempty"`
	EndSeq          int64   `json:"end_seq,omitempty"`
	ShadowedSeqs    []int64 `json:"shadowed_seqs,omitempty"`
	ShadowedTokens  int     `json:"shadowed_tokens,omitempty"`
	SummaryTokens   int     `json:"summary_tokens,omitempty"`
	TokensSaved     int     `json:"tokens_saved,omitempty"`
	Summary         string  `json:"summary,omitempty"`
	Status          string  `json:"status,omitempty"`
	Error           string  `json:"error,omitempty"`
	TargetSeq       int64   `json:"target_seq,omitempty"`
	ToolCallID      string  `json:"tool_call_id,omitempty"`
	ToolName        string  `json:"tool_name,omitempty"`
	OriginalChars   int     `json:"original_chars,omitempty"`
	PrunedChars     int     `json:"pruned_chars,omitempty"`
	CharactersSaved int     `json:"characters_saved,omitempty"`
}

type SubagentDescriptorPayload struct {
	Version         int    `json:"version"`
	ParentSessionID string `json:"parent_session_id"`
	Label           string `json:"label"`
	Depth           int    `json:"depth"`
	Scope           string `json:"scope"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	ApprovalMode    string `json:"approval_mode,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

type SubagentQueuedPayload struct {
	MessageID       string `json:"message_id"`
	SenderSessionID string `json:"sender_session_id"`
	Content         string `json:"content"`
	RequestDigest   string `json:"request_digest,omitempty"`
}

type SubagentRunStartPayload struct {
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
}

type SubagentRunEndPayload struct {
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Recovered bool   `json:"recovered,omitempty"`
}

type RunQueuedPayload struct {
	RunID         string `json:"run_id"`
	MessageID     string `json:"message_id"`
	Content       string `json:"content"`
	RequestDigest string `json:"request_digest"`
}

type RunStartPayload struct {
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
}

type RunEndPayload struct {
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Recovered bool   `json:"recovered,omitempty"`
}

type InteractionRequestedPayload struct {
	InteractionID string          `json:"interaction_id"`
	RunID         string          `json:"run_id,omitempty"`
	Kind          string          `json:"kind"`
	Data          json.RawMessage `json:"data"`
}

type InteractionResolvedPayload struct {
	InteractionID string          `json:"interaction_id"`
	RunID         string          `json:"run_id,omitempty"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	Data          json.RawMessage `json:"data,omitempty"`
}

// Record is the session-local record encoded in state.Event.Payload. New v2
// records use state.Event.Sequence as their durable ordering key; v1 retains
// the former embedded sequence for compatibility.
type Record struct {
	Seq             int64          `json:"seq,omitempty"`
	Time            time.Time      `json:"time"`
	Type            EventType      `json:"type"`
	Version         int            `json:"version,omitempty"`
	Ignorable       bool           `json:"ignorable"`
	Data            any            `json:"data,omitempty"`
	SurfaceOp       *SurfaceOp     `json:"surface_op,omitempty"`
	SourceEventSeqs []int64        `json:"source_event_seqs,omitempty"`
	Message         models.Message `json:"message,omitempty"`
}

// ModelSurface is the replayed list of event IDs visible to the model. Raw
// diagnostics and lifecycle records never become context messages.
type ModelSurface struct {
	nodes             []int64
	replaceGeneration uint64
	lastProcessedSeq  int64
}

func NewModelSurface() ModelSurface { return ModelSurface{lastProcessedSeq: -1} }

func (s *ModelSurface) Apply(event Record) error {
	if s.lastProcessedSeq >= 0 && event.Seq <= s.lastProcessedSeq {
		return fmt.Errorf("session event seq %d is not ordered after %d", event.Seq, s.lastProcessedSeq)
	}
	op := event.SurfaceOp
	if op == nil {
		if IsSurfaceType(event.Type) {
			op = &SurfaceOp{Kind: SurfaceAppend}
		} else {
			s.lastProcessedSeq = event.Seq
			return nil
		}
	}
	switch op.Kind {
	case SurfaceAppend:
		if IsSurfaceType(event.Type) {
			s.nodes = append(s.nodes, event.Seq)
		}
	case SurfaceReplace:
		if err := s.replace(op.StartSeq, op.EndSeq, event); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown surface op %q", op.Kind)
	}
	s.lastProcessedSeq = event.Seq
	return nil
}

func (s *ModelSurface) replace(start, end int64, event Record) error {
	startIndex, endIndex := -1, -1
	for index, seq := range s.nodes {
		if seq == start {
			startIndex = index
		}
		if seq == end {
			endIndex = index
		}
	}
	if startIndex < 0 || endIndex < 0 {
		return fmt.Errorf("replace range %d..%d is not on the surface", start, end)
	}
	if startIndex > endIndex {
		return fmt.Errorf("replace start %d occurs after end %d", start, end)
	}
	if !containsAll(event.SourceEventSeqs, s.nodes[startIndex:endIndex+1]) {
		return fmt.Errorf("replace provenance missing shadowed sequence IDs")
	}
	next := make([]int64, 0, len(s.nodes)-(endIndex-startIndex))
	next = append(next, s.nodes[:startIndex]...)
	next = append(next, event.Seq)
	next = append(next, s.nodes[endIndex+1:]...)
	s.nodes = next
	s.replaceGeneration++
	return nil
}

func containsAll(have, need []int64) bool {
	set := make(map[int64]struct{}, len(have))
	for _, seq := range have {
		set[seq] = struct{}{}
	}
	for _, seq := range need {
		if _, ok := set[seq]; !ok {
			return false
		}
	}
	return true
}

func (s ModelSurface) Nodes() []int64 {
	nodes := make([]int64, len(s.nodes))
	copy(nodes, s.nodes)
	return nodes
}

func (s ModelSurface) LastSeq() int64     { return s.lastProcessedSeq }
func (s ModelSurface) Generation() uint64 { return s.replaceGeneration }

// MessageForEvent returns the context message represented by one surface event.
func MessageForEvent(event Record) (*models.Message, bool) {
	message := event.Message
	if emptyMessage(message) {
		if decoded, ok := messageFromData(event.Data); ok {
			message = decoded
		}
	}
	switch event.Type {
	case EventUserMessage:
		message.Role = models.RoleUser
		return &message, true
	case EventAssistantMessage:
		message.Role = models.RoleAssistant
		if message.Content == "" && len(message.ToolCalls) == 0 {
			return nil, false
		}
		return &message, true
	case EventToolResult:
		message.Role = models.RoleTool
		return &message, true
	default:
		return nil, false
	}
}

func emptyMessage(message models.Message) bool {
	return message.Content == "" && len(message.ToolCalls) == 0 && message.ToolCallID == "" && message.ToolName == ""
}

func messageFromData(data any) (models.Message, bool) {
	if data == nil {
		return models.Message{}, false
	}
	if message, ok := data.(models.Message); ok {
		return message, true
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return models.Message{}, false
	}
	var message models.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		return models.Message{}, false
	}
	return message, true
}

// ReplayState is the explicit durable projection the agent consumes at startup.
// Working memory deliberately stays in state documents, not this execution log.
type ReplayState struct {
	Events                []Record
	Surface               ModelSurface
	ActiveTurn            int
	ActiveStep            int
	PendingToolCalls      []models.ToolCall
	DispatchedToolCallIDs []string
	PlanModeActive        bool
	Todos                 []TodoItem
}

// Replay folds a complete ordered session log without consulting runtime state.
func Replay(events []Record) (ReplayState, error) {
	state := ReplayState{Events: append([]Record(nil), events...), Surface: NewModelSurface()}
	pending := make(map[string]models.ToolCall)
	dispatched := make(map[string]bool)
	for _, event := range events {
		if err := state.Surface.Apply(event); err != nil {
			return ReplayState{}, err
		}
		switch event.Type {
		case EventTurnStart:
			state.ActiveTurn = payloadTurn(event.Data, state.ActiveTurn+1)
			state.ActiveStep = 0
			state.Todos = nil
		case EventTurnEnd:
			state.ActiveTurn = 0
			state.ActiveStep = 0
		case EventStepStart:
			state.ActiveStep = payloadStep(event.Data, state.ActiveStep+1)
		case EventStepEnd:
			state.ActiveStep = 0
		case EventAssistantMessage:
			if message, ok := MessageForEvent(event); ok {
				for _, call := range message.ToolCalls {
					if call.ID != "" {
						pending[call.ID] = call
					}
				}
			}
		case EventToolResult:
			if message, ok := MessageForEvent(event); ok {
				delete(pending, message.ToolCallID)
				delete(dispatched, message.ToolCallID)
			}
		case EventToolCall:
			if payload, ok := toolCall(event.Data); ok && payload.CallID != "" {
				dispatched[payload.CallID] = true
			}
		case EventPlanMode:
			if active, ok := planMode(event.Data); ok {
				state.PlanModeActive = active
			}
		case EventTodoWrite:
			state.Todos = todos(event.Data)
		}
	}
	for _, event := range events {
		if event.Type != EventAssistantMessage {
			continue
		}
		message, ok := MessageForEvent(event)
		if !ok {
			continue
		}
		for _, call := range message.ToolCalls {
			if pendingCall, ok := pending[call.ID]; ok {
				state.PendingToolCalls = append(state.PendingToolCalls, pendingCall)
			}
		}
	}
	for _, event := range events {
		if event.Type != EventToolCall {
			continue
		}
		if payload, ok := toolCall(event.Data); ok && dispatched[payload.CallID] {
			state.DispatchedToolCallIDs = append(state.DispatchedToolCallIDs, payload.CallID)
		}
	}
	return state, nil
}

func payloadTurn(data any, fallback int) int {
	switch payload := data.(type) {
	case TurnStartPayload:
		if payload.Turn > 0 {
			return payload.Turn
		}
	case map[string]any:
		if value, ok := payload["turn"].(float64); ok && value > 0 {
			return int(value)
		}
	}
	return fallback
}

func payloadStep(data any, fallback int) int {
	switch payload := data.(type) {
	case StepStartPayload:
		if payload.Step > 0 {
			return payload.Step
		}
	case map[string]any:
		if value, ok := payload["step"].(float64); ok && value > 0 {
			return int(value)
		}
	}
	return fallback
}

func planMode(data any) (bool, bool) {
	switch payload := data.(type) {
	case PlanModePayload:
		return payload.Active, true
	case bool:
		return payload, true
	case map[string]any:
		active, ok := payload["active"].(bool)
		return active, ok
	default:
		return false, false
	}
}

func todos(data any) []TodoItem {
	switch payload := data.(type) {
	case TodoWritePayload:
		return append([]TodoItem(nil), payload.Todos...)
	case map[string]any:
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil
		}
		var decoded TodoWritePayload
		if json.Unmarshal(raw, &decoded) == nil {
			return decoded.Todos
		}
	}
	return nil
}

// RepairTail synthesizes the durable boundary records needed after a process
// stops between an assistant tool decision and its result. The agent persists
// the returned events through the ordinary append path before resuming.
func RepairTail(events []Record) []Record {
	extra := make([]Record, 0)
	seq := int64(len(events))
	openTurn, openStep := false, false
	var pendingCalls []models.ToolCall
	dispatched, resulted := map[string]bool{}, map[string]bool{}
	openCompactions := map[string]CompactionPayload{}
	var compactionOrder []string
	var replacements []SurfaceOp
	for _, event := range events {
		switch event.Type {
		case EventTurnStart:
			openTurn = true
		case EventTurnEnd:
			openTurn = false
		case EventStepStart:
			openStep = true
		case EventStepEnd:
			openStep = false
		case EventAssistantMessage:
			if message, ok := MessageForEvent(event); ok {
				pendingCalls = append([]models.ToolCall(nil), message.ToolCalls...)
			}
		case EventToolCall:
			if payload, ok := toolCall(event.Data); ok && payload.CallID != "" {
				dispatched[payload.CallID] = true
			}
		case EventToolResult:
			if message, ok := MessageForEvent(event); ok {
				resulted[message.ToolCallID] = true
			}
		case EventCompactionStart:
			if payload, ok := compactionPayload(event.Data); ok {
				key := payload.CompactionID
				if key == "" {
					key = fmt.Sprintf("compaction@%d", event.Seq)
				}
				openCompactions[key] = payload
				compactionOrder = append(compactionOrder, key)
			}
		case EventCompactionEnd:
			if payload, ok := compactionPayload(event.Data); ok {
				delete(openCompactions, payload.CompactionID)
			}
		}
		if event.SurfaceOp != nil && event.SurfaceOp.Kind == SurfaceReplace {
			replacements = append(replacements, *event.SurfaceOp)
		}
	}
	for _, call := range pendingCalls {
		if call.ID == "" || resulted[call.ID] {
			continue
		}
		content := "Error: tool call aborted before dispatch (not started)"
		if dispatched[call.ID] {
			content = "Error: tool execution interrupted by process restart (unknown outcome)"
		}
		extra = append(extra, Record{Seq: seq, Type: EventToolResult, Message: models.Message{
			Role: models.RoleTool, Content: content, ToolCallID: call.ID, ToolName: call.Name,
		}, SurfaceOp: &SurfaceOp{Kind: SurfaceAppend}})
		seq++
	}
	for _, key := range compactionOrder {
		payload, ok := openCompactions[key]
		if !ok {
			continue
		}
		payload.Status, payload.Error = "failed", "interrupted by process restart"
		for _, replacement := range replacements {
			if replacement.StartSeq == payload.StartSeq && replacement.EndSeq == payload.EndSeq {
				payload.Status, payload.Error = "recovered-completed", ""
				break
			}
		}
		extra = append(extra, Record{Seq: seq, Type: EventCompactionEnd, Data: payload})
		seq++
	}
	if openStep {
		extra = append(extra, Record{Seq: seq, Type: EventStepEnd, Data: StepEndPayload{Reason: "repaired"}})
		seq++
	}
	if openTurn {
		extra = append(extra, Record{Seq: seq, Type: EventTurnEnd, Data: TurnEndPayload{Reason: "repaired"}})
	}
	return extra
}

func compactionPayload(data any) (CompactionPayload, bool) {
	switch payload := data.(type) {
	case CompactionPayload:
		return payload, true
	case map[string]any:
		raw, err := json.Marshal(payload)
		if err != nil {
			return CompactionPayload{}, false
		}
		var decoded CompactionPayload
		if json.Unmarshal(raw, &decoded) != nil {
			return CompactionPayload{}, false
		}
		return decoded, true
	default:
		return CompactionPayload{}, false
	}
}

func toolCall(data any) (ToolCallPayload, bool) {
	switch payload := data.(type) {
	case ToolCallPayload:
		return payload, true
	case map[string]any:
		callID, _ := payload["call_id"].(string)
		return ToolCallPayload{CallID: callID}, callID != ""
	default:
		return ToolCallPayload{}, false
	}
}

func IsKnownType(kind EventType) bool {
	switch kind {
	case EventUserMessage, EventAssistantMessage, EventToolResult,
		EventTurnStart, EventTurnEnd, EventStepStart, EventStepEnd,
		EventRequestHeader, EventRequestContext, EventAssistantChunk, EventUsage, EventFinish,
		EventError, EventRetry, EventToolCall, EventTodoWrite, EventPlanMode,
		EventCompactionPrune, EventCompactionStart, EventCompactionSummary, EventCompactionEnd,
		EventSubagentDescriptor, EventSubagentQueued, EventSubagentRunStart, EventSubagentRunEnd,
		EventRunQueued, EventRunStart, EventRunEnd, EventInteractionRequest, EventInteractionResolve:
		return true
	default:
		return false
	}
}

func IsSurfaceType(kind EventType) bool {
	return kind == EventUserMessage || kind == EventAssistantMessage || kind == EventToolResult
}

// New builds a v2 event and rejects map payloads before they enter the log.
func New(kind EventType, data any) (Record, error) {
	if !IsKnownType(kind) {
		return Record{}, fmt.Errorf("unknown session event type %q", kind)
	}
	if err := validatePayload(kind, data); err != nil {
		return Record{}, err
	}
	return Record{Type: kind, Version: CurrentVersion, Data: data, Time: time.Now().UTC()}, nil
}

func validatePayload(kind EventType, data any) error {
	if data == nil && (kind == EventUserMessage || kind == EventAssistantMessage || kind == EventToolResult) {
		return nil
	}
	valid := false
	switch kind {
	case EventUserMessage, EventAssistantMessage, EventToolResult:
		_, valid = data.(models.Message)
	case EventTurnStart:
		_, valid = data.(TurnStartPayload)
	case EventTurnEnd:
		_, valid = data.(TurnEndPayload)
	case EventStepStart:
		_, valid = data.(StepStartPayload)
	case EventStepEnd:
		_, valid = data.(StepEndPayload)
	case EventRequestHeader:
		_, valid = data.(RequestHeaderPayload)
	case EventRequestContext:
		_, valid = data.(RequestContextPayload)
	case EventAssistantChunk:
		_, valid = data.(AssistantChunkPayload)
	case EventUsage:
		_, valid = data.(UsagePayload)
	case EventFinish:
		_, valid = data.(FinishPayload)
	case EventError:
		_, valid = data.(ErrorPayload)
	case EventRetry:
		_, valid = data.(RetryPayload)
	case EventToolCall:
		_, valid = data.(ToolCallPayload)
	case EventTodoWrite:
		_, valid = data.(TodoWritePayload)
	case EventPlanMode:
		_, valid = data.(PlanModePayload)
	case EventCompactionPrune, EventCompactionStart, EventCompactionSummary, EventCompactionEnd:
		_, valid = data.(CompactionPayload)
	case EventSubagentDescriptor:
		_, valid = data.(SubagentDescriptorPayload)
	case EventSubagentQueued:
		_, valid = data.(SubagentQueuedPayload)
	case EventSubagentRunStart:
		_, valid = data.(SubagentRunStartPayload)
	case EventSubagentRunEnd:
		_, valid = data.(SubagentRunEndPayload)
	case EventRunQueued:
		_, valid = data.(RunQueuedPayload)
	case EventRunStart:
		_, valid = data.(RunStartPayload)
	case EventRunEnd:
		_, valid = data.(RunEndPayload)
	case EventInteractionRequest:
		_, valid = data.(InteractionRequestedPayload)
	case EventInteractionResolve:
		_, valid = data.(InteractionResolvedPayload)
	}
	if !valid {
		return fmt.Errorf("invalid payload %T for session event %q", data, kind)
	}
	return nil
}

// ToEventInput validates and encodes a session record for an atomic state write.
func ToEventInput(sessionID string, event Record) (state.EventInput, error) {
	if sessionID == "" {
		return state.EventInput{}, fmt.Errorf("session ID is required")
	}
	if event.Version == 0 {
		// Struct literals are retained by existing callers and stored sessions;
		// treat them as the pre-typed v1 envelope rather than rewriting history.
		event.Version = 1
	}
	if event.Version != 1 && event.Version != CurrentVersion {
		return state.EventInput{}, fmt.Errorf("cannot append session event version %d", event.Version)
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.Version == CurrentVersion {
		if err := validatePayload(event.Type, event.Data); err != nil {
			return state.EventInput{}, err
		}
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return state.EventInput{}, err
	}
	return state.EventInput{
		SessionID: sessionID, Type: string(event.Type), PayloadVersion: event.Version,
		Payload: json.RawMessage(payload), CreatedAt: event.Time,
	}, nil
}

type eventMetaKey struct{}

type EventMeta struct {
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
}

func WithEventMeta(ctx context.Context, meta EventMeta) context.Context {
	return context.WithValue(ctx, eventMetaKey{}, meta)
}

func EventMetaFromContext(ctx context.Context) EventMeta {
	meta, _ := ctx.Value(eventMetaKey{}).(EventMeta)
	return meta
}

func ApplyEventMeta(input state.EventInput, meta EventMeta) state.EventInput {
	input.CorrelationID = meta.CorrelationID
	input.CausationID = meta.CausationID
	input.IdempotencyKey = meta.IdempotencyKey
	return input
}

// Append persists a v2 session event. SQLite allocates the event sequence
// atomically; that sequence is the source reference used by all new records.
func Append(ctx context.Context, store state.EventStore, sessionID string, event Record) (Record, error) {
	if store == nil {
		return Record{}, fmt.Errorf("session event store is required")
	}
	input, err := ToEventInput(sessionID, event)
	if err != nil {
		return Record{}, err
	}
	input = ApplyEventMeta(input, EventMetaFromContext(ctx))
	stored, err := store.AppendEvent(ctx, input)
	if err != nil {
		return Record{}, err
	}
	event.Version = input.PayloadVersion
	event.Time = input.CreatedAt
	if input.PayloadVersion == CurrentVersion {
		event.Seq = stored.Sequence
	}
	return event, nil
}

// Load returns only session-log events. Legacy workspace events are ignored;
// an unknown v2 session envelope must be marked ignorable to be skipped.
func Load(ctx context.Context, store state.EventStore, sessionID string) ([]Record, error) {
	raw, err := store.Events(ctx, state.EventQuery{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	events := make([]Record, 0, len(raw))
	for _, stored := range raw {
		event, sessionRecord, err := decode(stored)
		if err != nil {
			return nil, err
		}
		if !sessionRecord {
			continue
		}
		if !IsKnownType(event.Type) {
			// v1 had no compatibility marker and historically shared the state
			// table with unrelated workspace records, so preserve its ignore
			// behavior. New v2 records must opt into being safely ignorable.
			if event.Version < CurrentVersion || event.Ignorable {
				continue
			}
			return nil, fmt.Errorf("session %s contains unknown required event %q", sessionID, event.Type)
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			return nil, fmt.Errorf("session events are not strictly ordered")
		}
	}
	return events, nil
}

func decode(stored state.Event) (Record, bool, error) {
	var envelope struct {
		Seq             int64           `json:"seq"`
		Time            time.Time       `json:"time"`
		Type            EventType       `json:"type"`
		Version         int             `json:"version"`
		Ignorable       bool            `json:"ignorable"`
		Data            json.RawMessage `json:"data"`
		SurfaceOp       *SurfaceOp      `json:"surface_op"`
		SourceEventSeqs []int64         `json:"source_event_seqs"`
		Message         models.Message  `json:"message"`
	}
	if len(stored.Payload) == 0 || json.Unmarshal(stored.Payload, &envelope) != nil {
		return Record{}, false, nil
	}
	if envelope.Type == "" {
		return Record{}, false, nil
	}
	if envelope.Type != EventType(stored.Type) {
		return Record{}, false, nil
	}
	event := Record{Seq: envelope.Seq, Time: envelope.Time, Type: envelope.Type, Version: envelope.Version,
		Ignorable: envelope.Ignorable, SurfaceOp: envelope.SurfaceOp, SourceEventSeqs: envelope.SourceEventSeqs, Message: envelope.Message}
	if event.Version == 0 {
		event.Version = 1
	} else if event.Version >= CurrentVersion {
		event.Seq = stored.Sequence
	}
	if event.Time.IsZero() {
		event.Time = stored.CreatedAt
	}
	data, err := decodePayload(event.Type, event.Version, envelope.Data)
	if err != nil {
		return Record{}, true, err
	}
	event.Data = data
	if event.Message.Content == "" && len(event.Message.ToolCalls) == 0 {
		if msg, ok := data.(models.Message); ok {
			event.Message = msg
		}
	}
	return event, true, nil
}

func decodePayload(kind EventType, version int, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if version == 1 {
		var legacy any
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, err
		}
		return legacy, nil
	}
	var target any
	switch kind {
	case EventUserMessage, EventAssistantMessage, EventToolResult:
		target = &models.Message{}
	case EventTurnStart:
		target = &TurnStartPayload{}
	case EventTurnEnd:
		target = &TurnEndPayload{}
	case EventStepStart:
		target = &StepStartPayload{}
	case EventStepEnd:
		target = &StepEndPayload{}
	case EventRequestHeader:
		target = &RequestHeaderPayload{}
	case EventRequestContext:
		target = &RequestContextPayload{}
	case EventAssistantChunk:
		target = &AssistantChunkPayload{}
	case EventUsage:
		target = &UsagePayload{}
	case EventFinish:
		target = &FinishPayload{}
	case EventError:
		target = &ErrorPayload{}
	case EventRetry:
		target = &RetryPayload{}
	case EventToolCall:
		target = &ToolCallPayload{}
	case EventTodoWrite:
		target = &TodoWritePayload{}
	case EventPlanMode:
		target = &PlanModePayload{}
	case EventCompactionPrune, EventCompactionStart, EventCompactionSummary, EventCompactionEnd:
		target = &CompactionPayload{}
	case EventSubagentDescriptor:
		target = &SubagentDescriptorPayload{}
	case EventSubagentQueued:
		target = &SubagentQueuedPayload{}
	case EventSubagentRunStart:
		target = &SubagentRunStartPayload{}
	case EventSubagentRunEnd:
		target = &SubagentRunEndPayload{}
	case EventRunQueued:
		target = &RunQueuedPayload{}
	case EventRunStart:
		target = &RunStartPayload{}
	case EventRunEnd:
		target = &RunEndPayload{}
	case EventInteractionRequest:
		target = &InteractionRequestedPayload{}
	case EventInteractionResolve:
		target = &InteractionResolvedPayload{}
	default:
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("decode %s v%d payload: %w", kind, version, err)
	}
	return dereference(target), nil
}

func dereference(value any) any {
	switch v := value.(type) {
	case *models.Message:
		return *v
	case *TurnStartPayload:
		return *v
	case *TurnEndPayload:
		return *v
	case *StepStartPayload:
		return *v
	case *StepEndPayload:
		return *v
	case *RequestHeaderPayload:
		return *v
	case *RequestContextPayload:
		return *v
	case *AssistantChunkPayload:
		return *v
	case *UsagePayload:
		return *v
	case *FinishPayload:
		return *v
	case *ErrorPayload:
		return *v
	case *RetryPayload:
		return *v
	case *ToolCallPayload:
		return *v
	case *TodoWritePayload:
		return *v
	case *PlanModePayload:
		return *v
	case *CompactionPayload:
		return *v
	case *SubagentDescriptorPayload:
		return *v
	case *SubagentQueuedPayload:
		return *v
	case *SubagentRunStartPayload:
		return *v
	case *SubagentRunEndPayload:
		return *v
	case *RunQueuedPayload:
		return *v
	case *RunStartPayload:
		return *v
	case *RunEndPayload:
		return *v
	case *InteractionRequestedPayload:
		return *v
	case *InteractionResolvedPayload:
		return *v
	default:
		return value
	}
}
