package agent

// Session holds the persistent state and context of a user chat session.
type Session struct {
	ID               string                 `json:"id"`
	WorkingDirectory string                 `json:"working_directory"`
	RuntimeConfigRef string                 `json:"runtime_config_ref"`
	OpenFiles        []string               `json:"open_files"`
	CurrentPlanID    string                 `json:"current_plan_id,omitempty"`
	Metadata         map[string]interface{} `json:"metadata"`
}
