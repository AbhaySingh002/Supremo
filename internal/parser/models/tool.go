package models

import "encoding/json"

// ToolCall represents a parsed request to execute a tool.
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Synthetic bool            `json:"synthetic,omitempty"`
	// ProviderMetadata carries opaque replay data required by a native tool
	// protocol (for example Gemini thought signatures). The runtime never
	// interprets it and durable memory retains it with the call.
	ProviderMetadata json.RawMessage `json:"provider_metadata,omitempty"`
}
