// Package api contains the versioned, transport-safe backend contract.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const Version = 1

// Client is Supremo's transport-neutral frontend contract. In-process and
// remote frontends depend on this interface rather than the application
// composition root or concrete agent runtimes.
type Client interface {
	Initialize(context.Context) (InitializeResult, error)
	ListSessions(context.Context) ([]Session, error)
	CreateSession(context.Context, CreateSessionRequest) (Session, error)
	GetSession(context.Context, string) (SessionSnapshot, error)
	UpdateSession(context.Context, UpdateSessionRequest) (Session, error)
	DeleteSession(context.Context, string) error
	ClearSession(context.Context, SessionRequest) (SessionSnapshot, error)
	ResetSession(context.Context, SessionRequest) (SessionSnapshot, error)
	ListCheckpoints(context.Context, SessionRequest) ([]Checkpoint, error)
	RewindSession(context.Context, RewindRequest) (RewindResult, error)
	AnswerSideQuestion(context.Context, SideQuestionRequest) (SideQuestionResult, error)
	GetArtifact(context.Context, ArtifactRequest) (Artifact, error)
	ListModels(context.Context, ListModelsRequest) (ModelCatalog, error)
	ConfigureProvider(context.Context, ConfigureProviderRequest) (InitializeResult, error)
	RefreshProviderMetadata(context.Context) (InitializeResult, error)
	ProviderUsage(context.Context) (Usage, error)
	ConfigureEmbeddings(context.Context, ConfigureEmbeddingsRequest) error
	ReloadConfiguration(context.Context) (InitializeResult, error)
	ListTools(context.Context, SessionRequest) ([]Tool, error)
	ToolActivity(context.Context, SessionRequest) ([]ToolActivity, error)
	WorkspaceStatus(context.Context) (WorkspaceStatus, error)
	WorkspaceDiff(context.Context) (Diff, error)
	Health(context.Context) (HealthReport, error)
	ContextStatus(context.Context, ContextStatusRequest) (ContextStatus, error)
	IndexStatus(context.Context) (IndexStatus, error)
	UpdateIndex(context.Context, UpdateIndexRequest) (IndexStatus, error)
	InitializeWorkspace(context.Context) (WorkspaceStatus, error)
	SubmitPrompt(context.Context, SubmitPromptRequest) (Receipt, error)
	CancelRun(context.Context, CancelRunRequest) (Run, error)
	RespondInteraction(context.Context, RespondInteractionRequest) error
	StartAgent(context.Context, StartAgentRequest) (Run, error)
	ListAgents(context.Context, AgentControlRequest) ([]Agent, error)
	SendAgentMessage(context.Context, AgentMessageRequest) (Run, error)
	WaitAgent(context.Context, AgentControlRequest) (Run, error)
	InterruptAgent(context.Context, AgentControlRequest) error
	Subscribe(context.Context, SubscribeRequest) (EventStream, error)
}

// EventStream is the common shape used by the in-process subscription and an
// eventual HTTP/SSE client.
type EventStream interface {
	Events() <-chan Event
	Err() error
	Close()
}

const (
	EventUserMessage         = "user/message"
	EventAssistantMessage    = "assistant/message"
	EventToolResult          = "tool/result"
	EventTurnStart           = "turn/start"
	EventTurnEnd             = "turn/end"
	EventStepStart           = "step/start"
	EventStepEnd             = "step/end"
	EventAssistantChunk      = "assistant/chunk"
	EventUsage               = "usage"
	EventFinish              = "finish"
	EventError               = "error"
	EventRetry               = "retry"
	EventToolCall            = "tool/call"
	EventTodoWrite           = "todo/write"
	EventPlanMode            = "plan/mode"
	EventRunQueued           = "run/message.queued"
	EventRunStart            = "run/start"
	EventRunEnd              = "run/end"
	EventInteractionRequest  = "interaction/requested"
	EventInteractionResolve  = "interaction/resolved"
	EventCheckpointAvailable = "checkpoint.available"
	EventArtifactAvailable   = "artifact.created"
	EventSessionCreated      = "session.created"
	EventSessionUpdated      = "session.updated"
	EventSessionArchived     = "session.archived"
	EventSubagentDescriptor  = "subagent/descriptor"
	EventSubagentQueued      = "subagent/message.queued"
	EventSubagentRunStart    = "subagent/run.start"
	EventSubagentRunEnd      = "subagent/run.end"
)

type ErrorCode string

const (
	CodeInvalidArgument   ErrorCode = "INVALID_ARGUMENT"
	CodeNotFound          ErrorCode = "NOT_FOUND"
	CodeConflict          ErrorCode = "CONFLICT"
	CodeBusy              ErrorCode = "BUSY"
	CodeUnauthorized      ErrorCode = "UNAUTHORIZED"
	CodeForbidden         ErrorCode = "FORBIDDEN"
	CodeCancelled         ErrorCode = "CANCELLED"
	CodeResyncRequired    ErrorCode = "RESYNC_REQUIRED"
	CodeProviderAuth      ErrorCode = "PROVIDER_AUTH"
	CodeProviderRateLimit ErrorCode = "PROVIDER_RATE_LIMIT"
	CodeProviderDown      ErrorCode = "PROVIDER_UNAVAILABLE"
	CodeContextLimit      ErrorCode = "CONTEXT_LIMIT"
	CodeToolDenied        ErrorCode = "TOOL_DENIED"
	CodeInternal          ErrorCode = "INTERNAL"
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type RPCRequest struct {
	V              int             `json:"v"`
	ID             string          `json:"id"`
	Method         string          `json:"method"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Params         json.RawMessage `json:"params,omitempty"`
}

type RPCResponse struct {
	V      int    `json:"v"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

type Capabilities struct {
	AsyncRuns    bool `json:"async_runs"`
	Interactions bool `json:"interactions"`
	Subagents    bool `json:"subagents"`
	SSE          bool `json:"sse"`
}

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length,omitempty"`
}

type Provider struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Configured       bool      `json:"configured"`
	Endpoint         string    `json:"endpoint,omitempty"`
	RequiresEndpoint bool      `json:"requires_endpoint,omitempty"`
	MetadataState    string    `json:"metadata_state,omitempty"`
	MetadataWarning  string    `json:"metadata_warning,omitempty"`
	FetchedAt        time.Time `json:"fetched_at,omitempty"`
	Models           []Model   `json:"models,omitempty"`
}

type ListModelsRequest struct {
	Refresh bool `json:"refresh,omitempty"`
}

type ModelCatalog struct {
	Providers []Provider `json:"providers"`
}

type InitializeResult struct {
	APIVersion      int          `json:"api_version"`
	ServerVersion   string       `json:"server_version"`
	WorkspaceID     string       `json:"workspace_id"`
	Workspace       string       `json:"workspace"`
	Cursor          int64        `json:"cursor"`
	Capabilities    Capabilities `json:"capabilities"`
	Provider        string       `json:"provider,omitempty"`
	Model           string       `json:"model,omitempty"`
	Endpoint        string       `json:"endpoint,omitempty"`
	CredentialReady bool         `json:"credential_ready"`
	Providers       []Provider   `json:"providers"`
}

type Session struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Status          string    `json:"status"`
	Revision        int64     `json:"revision"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	ApprovalMode    string    `json:"approval_mode,omitempty"`
	DryRun          bool      `json:"dry_run"`
	PlanMode        bool      `json:"plan_mode"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	Origin          string    `json:"origin,omitempty"`
	Checklist       bool      `json:"checklist"`
	Rewind          bool      `json:"rewind"`
	ProviderRetry   bool      `json:"provider_retry"`
}

type SessionMetadata struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Revision  int64     `json:"revision"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s Session) PlanModeActive() bool       { return s.PlanMode }
func (s Session) ChecklistEnabled() bool     { return s.Checklist }
func (s Session) RewindEnabled() bool        { return s.Rewind }
func (s Session) ResponseRetryEnabled() bool { return s.ProviderRetry }

type MessagePart struct {
	Kind       string          `json:"kind"`
	Text       string          `json:"text,omitempty"`
	ArtifactID string          `json:"artifact_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type Message struct {
	ID        string        `json:"id"`
	Sequence  int64         `json:"sequence"`
	Role      string        `json:"role"`
	TaskID    string        `json:"task_id,omitempty"`
	State     string        `json:"state"`
	CreatedAt time.Time     `json:"created_at"`
	Parts     []MessagePart `json:"parts"`
}

type Run struct {
	AgentID   string `json:"agent_id,omitempty"`
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Recovered bool   `json:"recovered,omitempty"`
}

type Interaction struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id,omitempty"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
}

type Agent struct {
	ID              string `json:"id"`
	ParentSessionID string `json:"parent_session_id"`
	Label           string `json:"label"`
	Depth           int    `json:"depth"`
	Scope           string `json:"scope"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Status          string `json:"status"`
}

type SessionSnapshot struct {
	Session             Session       `json:"session"`
	Messages            []Message     `json:"messages"`
	PendingInteractions []Interaction `json:"pending_interactions"`
	Runs                []Run         `json:"runs"`
	Agents              []Agent       `json:"agents"`
	AsOfCursor          int64         `json:"as_of_cursor"`
	Revision            int64         `json:"revision"`
}

type CreateSessionRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type UpdateSessionRequest struct {
	SessionID        string  `json:"session_id"`
	ExpectedRevision int64   `json:"expected_revision"`
	Name             *string `json:"name,omitempty"`
	ApprovalMode     *string `json:"approval_mode,omitempty"`
	DryRun           *bool   `json:"dry_run,omitempty"`
	PlanMode         *bool   `json:"plan_mode,omitempty"`
	Checklist        *bool   `json:"checklist,omitempty"`
	Rewind           *bool   `json:"rewind,omitempty"`
	ProviderRetry    *bool   `json:"provider_retry,omitempty"`
}

type SessionRequest struct {
	SessionID string `json:"session_id"`
}

type CheckpointWarning struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

type Checkpoint struct {
	ID        string              `json:"id"`
	CreatedAt time.Time           `json:"created_at"`
	Action    string              `json:"action"`
	Files     int                 `json:"files"`
	Partial   bool                `json:"partial,omitempty"`
	Warnings  []CheckpointWarning `json:"warnings,omitempty"`
}

type CheckpointAvailable struct {
	Tool       string     `json:"tool"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

type RewindRequest struct {
	SessionID  string `json:"session_id"`
	Checkpoint string `json:"checkpoint_id"`
	Force      bool   `json:"force,omitempty"`
}

type RewindResult struct {
	Restored int                 `json:"restored"`
	Partial  bool                `json:"partial,omitempty"`
	Warnings []CheckpointWarning `json:"warnings,omitempty"`
	Backup   *Checkpoint         `json:"backup,omitempty"`
}

type SideQuestionRequest struct {
	SessionID string `json:"session_id"`
	Question  string `json:"question"`
}

type SideQuestionResult struct {
	Answer string `json:"answer"`
}

type ArtifactRequest struct {
	Hash string `json:"hash"`
}

type Artifact struct {
	Hash        string    `json:"hash"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type,omitempty"`
	Origin      string    `json:"origin,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Content     []byte    `json:"content,omitempty"`
	Previewable bool      `json:"previewable"`
}

type ConfigureProviderRequest struct {
	Provider *string `json:"provider,omitempty"`
	Model    *string `json:"model,omitempty"`
	Endpoint *string `json:"endpoint,omitempty"`
	APIKey   *string `json:"api_key,omitempty"`
	Verify   bool    `json:"verify,omitempty"`
}

type ConfigureEmbeddingsRequest struct {
	CredentialProvider string `json:"credential_provider"`
	Endpoint           string `json:"endpoint"`
	Model              string `json:"model"`
}

type Usage struct {
	InputTokens   int      `json:"input_tokens"`
	OutputTokens  int      `json:"output_tokens"`
	CostUSD       *float64 `json:"cost_usd,omitempty"`
	ContextLimit  int      `json:"context_limit,omitempty"`
	TotalCredits  *float64 `json:"total_credits,omitempty"`
	CreditsUsed   *float64 `json:"credits_used,omitempty"`
	CreditsRemain *float64 `json:"credits_remaining,omitempty"`
}

type Tool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Approval     string `json:"approval"`
	ParallelSafe bool   `json:"parallel_safe"`
}

type ToolActivity struct {
	Time    time.Time `json:"time"`
	Tool    string    `json:"tool"`
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
}

type WorkspaceStatus struct {
	Workspace string `json:"workspace"`
	Branch    string `json:"branch,omitempty"`
	Changed   int    `json:"changed"`
	Git       bool   `json:"git"`
	Ready     bool   `json:"ready"`
	Error     string `json:"error,omitempty"`
}

type Diff struct {
	Content string `json:"content"`
	Summary string `json:"summary"`
}

type HealthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HealthReport struct {
	Workspace string        `json:"workspace"`
	Checks    []HealthCheck `json:"checks"`
}

type ContextStatusRequest struct {
	SessionID string `json:"session_id"`
	Detailed  bool   `json:"detailed,omitempty"`
}

type ContextItem struct {
	Layer  string `json:"layer"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Tokens int    `json:"tokens"`
	Reason string `json:"reason,omitempty"`
}

type ContextStatus struct {
	RequestID       string        `json:"request_id,omitempty"`
	EstimatedUsed   int           `json:"estimated_used"`
	InputBudget     int           `json:"input_budget"`
	OutputReserve   int           `json:"output_reserve"`
	SafetyReserve   int           `json:"safety_reserve"`
	WorkingSetItems int           `json:"working_set_items"`
	Generation      int           `json:"generation"`
	Rejected        int           `json:"rejected"`
	ArtifactID      string        `json:"artifact_id,omitempty"`
	Items           []ContextItem `json:"items,omitempty"`
}

type IndexStatus struct {
	Ready      bool   `json:"ready"`
	Dirty      bool   `json:"dirty"`
	Semantic   bool   `json:"semantic"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
}

type UpdateIndexRequest struct {
	Semantic bool `json:"semantic"`
}

type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type StreamEvent struct {
	Type           string         `json:"type"`
	TextDelta      string         `json:"text_delta,omitempty"`
	ReasoningDelta string         `json:"reasoning_delta,omitempty"`
	ToolCall       *ToolCallDelta `json:"tool_call,omitempty"`
	Usage          *Usage         `json:"usage,omitempty"`
	FinishReason   string         `json:"finish_reason,omitempty"`
}

type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

type AssistantChunk struct {
	Turn    int         `json:"turn"`
	Step    int         `json:"step"`
	Attempt int         `json:"attempt"`
	Event   StreamEvent `json:"event"`
}

type LifecycleDetail struct {
	Turn   int    `json:"turn"`
	Step   int    `json:"step,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type RetryDetail struct {
	Turn        int    `json:"turn"`
	Step        int    `json:"step"`
	Attempt     int    `json:"attempt"`
	DelayMillis int64  `json:"delay_millis"`
	Code        string `json:"code,omitempty"`
}

type ErrorDetail struct {
	Turn    int    `json:"turn"`
	Step    int    `json:"step"`
	Attempt int    `json:"attempt"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type FinishDetail struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	Attempt      int    `json:"attempt"`
	FinishReason string `json:"finish_reason"`
}

type UsageDetail struct {
	Turn    int   `json:"turn"`
	Step    int   `json:"step"`
	Attempt int   `json:"attempt"`
	Usage   Usage `json:"usage"`
}

type ToolCall struct {
	Turn      int    `json:"turn"`
	Step      int    `json:"step"`
	CallID    string `json:"call_id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Skipped   bool   `json:"skipped,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ToolResult is the transport-safe projection of a durable tool/result
// surface event. It intentionally mirrors only fields useful to frontends.
type ToolResult struct {
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
}

type CompletedMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
}

type TodoUpdate struct {
	Todos []TodoItem `json:"todos"`
}

type PlanModeUpdate struct {
	Active bool `json:"active"`
}

type InteractionEvent struct {
	InteractionID string          `json:"interaction_id"`
	RunID         string          `json:"run_id,omitempty"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type ApprovalRequestData struct {
	Tool      string          `json:"tool"`
	CallID    string          `json:"call_id,omitempty"`
	Arguments json.RawMessage `json:"arguments"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Detail      string           `json:"detail,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Intent      string           `json:"intent,omitempty"`
}

type QuestionRequest struct {
	SessionID string     `json:"session_id,omitempty"`
	RunID     string     `json:"run_id,omitempty"`
	Questions []Question `json:"questions"`
}

type QuestionAnswer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected,omitempty"`
	Custom   string   `json:"custom,omitempty"`
}

type QuestionAnswers struct {
	Answers []QuestionAnswer `json:"answers"`
}

type SubmitPromptRequest struct {
	SessionID      string `json:"session_id"`
	Prompt         string `json:"prompt"`
	IdempotencyKey string `json:"-"`
}

type Receipt struct {
	Accepted       bool   `json:"accepted"`
	RunID          string `json:"run_id"`
	MessageID      string `json:"message_id"`
	AcceptedCursor int64  `json:"accepted_cursor"`
}

type CancelRunRequest struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
}

type RespondInteractionRequest struct {
	SessionID     string          `json:"session_id"`
	InteractionID string          `json:"interaction_id"`
	Decision      string          `json:"decision,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	RevisedInput  json.RawMessage `json:"revised_input,omitempty"`
	Answers       json.RawMessage `json:"answers,omitempty"`
}

type StartAgentRequest struct {
	ParentSessionID string `json:"parent_session_id"`
	Label           string `json:"label"`
	Prompt          string `json:"prompt"`
	Scope           string `json:"scope"`
	RunInBackground *bool  `json:"run_in_background,omitempty"`
	IdempotencyKey  string `json:"-"`
}

type AgentMessageRequest struct {
	ParentSessionID string `json:"parent_session_id"`
	AgentID         string `json:"agent_id"`
	Message         string `json:"message"`
	IdempotencyKey  string `json:"-"`
}

type AgentControlRequest struct {
	ParentSessionID string `json:"parent_session_id"`
	AgentID         string `json:"agent_id"`
	MessageID       string `json:"message_id,omitempty"`
	Descendants     bool   `json:"descendants,omitempty"`
	TimeoutMillis   int64  `json:"timeout_ms,omitempty"`
}

type Event struct {
	V           int             `json:"v"`
	Cursor      int64           `json:"cursor,omitempty"`
	EventID     string          `json:"event_id"`
	Type        string          `json:"type"`
	Durable     bool            `json:"durable"`
	Ignorable   bool            `json:"ignorable"`
	Time        time.Time       `json:"time"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	Turn        int             `json:"turn,omitempty"`
	Step        int             `json:"step,omitempty"`
	CallID      string          `json:"call_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

type SubscribeRequest struct {
	AfterCursor int64  `json:"after_cursor"`
	SessionID   string `json:"session_id,omitempty"`
}
