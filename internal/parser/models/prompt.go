package models

import "encoding/json"

// PromptTemplate identifies an immutable prompt fragment used for one model
// request. Content is retained in source; manifests retain only this metadata.
type PromptTemplate struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

// PromptSection reports an approximate input contribution without persisting
// the prompt itself.
type PromptSection struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// PromptMetadata is provider-neutral reproduction metadata for a compiled
// request. Provider adapters deliberately ignore it.
type PromptMetadata struct {
	Profile         string           `json:"profile,omitempty"`
	ProtocolVersion string           `json:"protocol_version,omitempty"`
	Templates       []PromptTemplate `json:"templates,omitempty"`
	Sections        []PromptSection  `json:"sections,omitempty"`
	SelectedTools   []string         `json:"selected_tools,omitempty"`
}

// ToolDefinition is the provider-neutral schema for one prompt-scoped tool.
// Runtime execution still requires ActiveTools and the tool manager's policy.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ContextSection describes one contextual component selected for the prompt, preserving provenance.
type ContextSection struct {
	ID              string `json:"id"`
	Layer           string `json:"layer"`
	Kind            string `json:"kind"`
	Authority       string `json:"authority,omitempty"`
	Provenance      string `json:"provenance,omitempty"`
	Freshness       string `json:"freshness,omitempty"`
	Content         string `json:"content"`
	EstimatedTokens int    `json:"estimated_tokens"`
	SourceHash      string `json:"source_hash,omitempty"`
	ArtifactID      string `json:"artifact_id,omitempty"`
	Pinned          bool   `json:"pinned,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
}

// RequestBudget tracks token allocation for a compiled request.
type RequestBudget struct {
	ContextLimit  int `json:"context_limit"`
	OutputReserve int `json:"output_reserve"`
	SafetyReserve int `json:"safety_reserve"`
	InputBudget   int `json:"input_budget"`
	EstimatedUsed int `json:"estimated_used"`
}

// Interaction represents an atomic causal conversation unit: an assistant turn
// (which may include native tool calls) paired with its corresponding tool results.
type Interaction struct {
	ID           string        `json:"id,omitempty"`
	Sequence     int64         `json:"sequence,omitempty"`
	Assistant    Message       `json:"assistant"`
	ToolResults  []Message     `json:"tool_results,omitempty"`
	TurnProgress *TurnProgress `json:"turn_progress,omitempty"`
}

// ContextRejection describes a candidate item excluded during context budget allocation.
type ContextRejection struct {
	ID      string             `json:"id"`
	Reason  string             `json:"reason"`
	Signals map[string]float64 `json:"signals,omitempty"`
}

// AgentRequest is the canonical, provider-neutral internal representation of
// everything the model will see before serialization.
type AgentRequest struct {
	RequestID    string             `json:"request_id"`
	SessionID    string             `json:"session_id"`
	TaskID       string             `json:"task_id,omitempty"`
	TurnID       string             `json:"turn_id,omitempty"`
	StepID       string             `json:"step_id,omitempty"`
	Profile      string             `json:"profile"`
	Sections     []ContextSection   `json:"sections,omitempty"`
	Rejected     []ContextRejection `json:"rejected,omitempty"`
	Interactions []Interaction      `json:"interactions,omitempty"`
	Tools        []ToolDefinition   `json:"tools,omitempty"`
	Budget       RequestBudget      `json:"budget"`
	SystemPrompt string             `json:"system_prompt"`
	Messages     []Message          `json:"messages"`
}

// Prompt represents a compiled LLM prompt containing system instructions and chat history.
type Prompt struct {
	System   string    `json:"system"`
	Messages []Message `json:"messages"`
	// Canonical Request IR describing everything the model will see
	Request *AgentRequest `json:"request,omitempty"`

	// ManifestID and the budget fields are compiler metadata. They deliberately
	// stay out of provider payloads while letting adapters reserve output space
	// and report actual usage against the durable manifest.
	ManifestID           string `json:"-"`
	OutputReserve        int    `json:"-"`
	EstimatedInputTokens int    `json:"-"`
	// ActiveTools is the execution boundary installed before model calls run.
	// ToolDefinitions lets capable providers expose the same selected schemas
	// natively; it grants no execution permission by itself.
	ActiveTools      []string         `json:"-"`
	ToolDefinitions  []ToolDefinition `json:"-"`
	Metadata         PromptMetadata   `json:"-"`
	Interactions     []Interaction    `json:"-"`
	FrozenEnvelope   []byte           `json:"-"`
	RequestDigest    string           `json:"-"`
	HeaderDigest     string           `json:"-"`
	SystemDigest     string           `json:"-"`
	ToolSchemaDigest string           `json:"-"`
}
