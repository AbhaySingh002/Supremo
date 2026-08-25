package models

const (
	TaskStepPending    = "pending"
	TaskStepInProgress = "in_progress"
	TaskStepDone       = "done"
	TaskStepFailed     = "failed"
)

// TaskChecklist is display-only progress for a normal agent turn.
type TaskChecklist struct {
	Title string     `json:"title,omitempty"`
	Steps []TaskStep `json:"steps"`
}

type TaskStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}
