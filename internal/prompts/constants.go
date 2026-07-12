package prompts

const (
	// DefaultTemplateDir is the default location for prompt markdown templates.
	DefaultTemplateDir = "internal/prompts/templates"

	// PlaceholderStart defines the starting delimiter of a template placeholder.
	PlaceholderStart = "{{"

	// PlaceholderEnd defines the ending delimiter of a template placeholder.
	PlaceholderEnd = "}}"
)

// Known placeholders
const (
	VarSystem    = "SYSTEM"
	VarTools     = "TOOLS"
	VarWorkspace = "WORKSPACE"
	VarModel     = "MODEL"
	VarTask      = "TASK"
	VarDate      = "DATE"
	VarPlan      = "PLAN"
	VarMemory    = "MEMORY"
)

// Known template identifiers
const (
	TemplateSystem   = "system"
	TemplateCoding   = "coding"
	TemplateTools    = "tools"
	TemplatePlanner  = "planner"
	TemplateReview   = "review"
	TemplateResponse = "response"
)
