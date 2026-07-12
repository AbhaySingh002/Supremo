package prompts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// GenerateToolDocs generates a Markdown documentation string for all registered tools in the registry.
func GenerateToolDocs(registry *tools.Registry) (string, error) {
	if registry == nil {
		return "", nil
	}

	// Safely extract the unexported r.tools map using reflection and unsafe pointer arithmetic.
	// This maintains package boundaries without modifying the existing internal/tools package.
	val := reflect.ValueOf(registry).Elem()
	field := val.FieldByName("tools")
	if !field.IsValid() {
		return "", fmt.Errorf("tools field not found in registry")
	}

	ptr := unsafe.Pointer(field.UnsafeAddr())
	toolsMap := *(*map[string]tools.Tool)(ptr)

	if len(toolsMap) == 0 {
		return "", nil
	}

	// Sort tools by name to ensure deterministic prompt compilation.
	names := make([]string, 0, len(toolsMap))
	for name := range toolsMap {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("# Available Tools\n")

	for _, name := range names {
		t := toolsMap[name]
		sb.WriteString(fmt.Sprintf("\n## %s\n\n", t.Name()))
		sb.WriteString(fmt.Sprintf("%s\n\n", t.Description()))
		sb.WriteString("Arguments:\n")

		schemaBytes, err := json.MarshalIndent(t.Schema(), "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON schema for tool %q: %w", t.Name(), err)
		}
		sb.WriteString(fmt.Sprintf("```json\n%s\n```\n\n", string(schemaBytes)))
		sb.WriteString("Usage Notes:\n- Use arguments conforming strictly to the JSON Schema.\n")
	}

	return sb.String(), nil
}
