package prompts

import (
	_ "embed"
)

//go:embed templates/swe.md
var sweProfile string

//go:embed templates/side_answer.md
var sideAnswer string
