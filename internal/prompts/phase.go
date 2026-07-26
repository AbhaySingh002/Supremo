package prompts

import _ "embed"

//go:embed templates/plan_mode.md
var planMode string

//go:embed templates/audit.md
var audit string

func PlanMode() string { return planMode }

func Audit() string { return audit }
