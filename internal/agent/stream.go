package agent

import "time"

// EventType represents the category of the streamed event.
type EventType string

const (
	EventThinking      EventType = "thinking"
	EventToolStarted   EventType = "tool_started"
	EventToolFinished  EventType = "tool_finished"
	EventFinalResponse EventType = "final_response"
)

// Event holds streaming event metadata and payload data.
type Event struct {
	Type      EventType `json:"type"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// EventStream defines the interface for emitting/handling execution stream events.
type EventStream interface {
	Emit(event Event)
}
