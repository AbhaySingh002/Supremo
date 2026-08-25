package tools

import "fmt"

const (
	ErrorClassProtocol      = "PROTOCOL_ERROR"
	ErrorClassToolArgument  = "TOOL_ARGUMENT_ERROR"
	ErrorClassToolExecution = "TOOL_EXECUTION_ERROR"
	ErrorClassProvider      = "PROVIDER_ERROR"
	ErrorClassPermission    = "PERMISSION_ERROR"
	ErrorClassCheckpoint    = "CHECKPOINT_ERROR"
)

type ClassifiedError struct {
	Class string
	Err   error
}

func (e *ClassifiedError) Error() string      { return fmt.Sprintf("%s: %v", e.Class, e.Err) }
func (e *ClassifiedError) Unwrap() error      { return e.Err }
func (e *ClassifiedError) ErrorClass() string { return e.Class }
func classify(class string, err error) error  { return &ClassifiedError{Class: class, Err: err} }

// ToolStatus is the normalized terminal state visible to the model and the
// durable lifecycle. Success is retained below for existing tool adapters.
type ToolStatus string

const (
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
	ToolStatusDenied    ToolStatus = "denied"
	ToolStatusDryRun    ToolStatus = "dry_run"
)

type ToolDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AffectedEntity struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

type ToolError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	Status ToolStatus `json:"status"`
	// Success indicates whether the tool execution was successful.
	Success bool `json:"success"`
	// Message provides a human-readable message about the execution result.
	Message string `json:"message"`
	// Data contains the output data from the tool execution.
	Data             map[string]interface{} `json:"data,omitempty"`
	Metadata         map[string]any         `json:"metadata,omitempty"`
	ArtifactID       string                 `json:"artifact_id,omitempty"`
	Preview          string                 `json:"preview,omitempty"`
	Diagnostics      []ToolDiagnostic       `json:"diagnostics,omitempty"`
	AffectedEntities []AffectedEntity       `json:"affected_entities,omitempty"`
	WorldRevision    string                 `json:"world_revision,omitempty"`
	Retryable        bool                   `json:"retryable,omitempty"`
	// RequestPlanModeExit is an internal request from exit_plan_mode after the
	// user approves a plan. Agent validates and persists the transition.
	RequestPlanModeExit bool       `json:"-"`
	Error               *ToolError `json:"error,omitempty"`
}

type ToolOutcomeClass int

const (
	ToolOutcomeSuccess ToolOutcomeClass = iota
	ToolOutcomeRecoverable
	ToolOutcomeApprovalRequired
	ToolOutcomePermissionBlocked
	ToolOutcomeCancelled
	ToolOutcomeFatal
)

func (c ToolOutcomeClass) String() string {
	switch c {
	case ToolOutcomeSuccess:
		return "SUCCESS"
	case ToolOutcomeRecoverable:
		return "RECOVERABLE_TOOL_FAILURE"
	case ToolOutcomeApprovalRequired:
		return "APPROVAL_REQUIRED"
	case ToolOutcomePermissionBlocked:
		return "PERMISSION_BLOCKED"
	case ToolOutcomeCancelled:
		return "CANCELLED"
	case ToolOutcomeFatal:
		return "FATAL_INTERNAL_FAILURE"
	default:
		return "UNKNOWN"
	}
}
