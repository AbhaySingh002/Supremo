package prompts

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

var systemTemplates = [...]string{"system", "coding", "tools", "planner", "response"}

//go:embed templates/system.md templates/coding.md templates/tools.md templates/planner.md templates/response.md
var templateFiles embed.FS

// LoadSystem reads the fixed system templates once during application startup.
func LoadSystem(registry *tools.Registry) (string, error) {
	toolDocs, err := generateToolDocs(registry)
	if err != nil {
		return "", err
	}

	var prompt strings.Builder
	for i, name := range systemTemplates {
		content, err := templateFiles.ReadFile("templates/" + name + ".md")
		if err != nil {
			return "", fmt.Errorf("read system template %q: %w", name, err)
		}
		if name == "tools" && toolDocs != "" {
			content = append(content, '\n')
			content = append(content, toolDocs...)
		}
		if i > 0 {
			prompt.WriteByte('\n')
		}
		prompt.Write(content)
	}
	return prompt.String(), nil
}

func generateToolDocs(registry *tools.Registry) (string, error) {
	if registry == nil {
		return "", nil
	}

	registered := registry.All()
	sort.Slice(registered, func(i, j int) bool { return registered[i].Name() < registered[j].Name() })

	var docs strings.Builder
	for _, tool := range registered {
		schema, err := json.MarshalIndent(tool.Schema(), "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal JSON schema for tool %q: %w", tool.Name(), err)
		}
		if docs.Len() == 0 {
			docs.WriteString("# Available Tools\n")
		}
		safety := "read-only"
		if tools.RequiresApproval(tool.Name()) {
			safety = "requires explicit user approval"
		}
		fmt.Fprintf(&docs, "\n## %s\n\n%s\n\nSafety: %s.\n\nArguments:\n```json\n%s\n```\n\nUsage Notes:\n- Use arguments conforming strictly to the JSON Schema.\n", tool.Name(), tool.Description(), safety, schema)
	}
	return docs.String(), nil
}
