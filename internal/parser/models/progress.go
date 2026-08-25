package models

// MemoryDirective represents an advisory attention hint from the model.
// Operations:
// - "retain": keep/prioritize this key/statement in active working memory.
// - "release": this key no longer needs active-context priority.
// - "supersede": replace a previous belief for this key with a newer statement.
// Directives are advisory and never delete durable transcripts, artifacts, or observations.
type MemoryDirective struct {
	Operation string   `json:"operation"`
	Key       string   `json:"key"`
	Statement string   `json:"statement,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
}

// TurnProgress represents concise, externally useful operational progress reported
// by the model in a turn, accompanying or preceding a native tool action.
type TurnProgress struct {
	Progress      string            `json:"progress"`
	NextGoal      string            `json:"next_goal"`
	EvidenceUsed  []string          `json:"evidence_used,omitempty"`
	MemoryUpdates []MemoryDirective `json:"memory_updates,omitempty"`
	PhaseState    string            `json:"phase_state,omitempty"`
}
