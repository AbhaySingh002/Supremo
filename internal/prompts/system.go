package prompts

import "embed"

//go:embed templates/system.md templates/tools.md templates/swe.md
var templateFiles embed.FS
