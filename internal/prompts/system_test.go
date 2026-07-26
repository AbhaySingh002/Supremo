package prompts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

type testTool struct{}

func (testTool) Name() string                                            { return "test" }
func (testTool) Description() string                                     { return "A test tool." }
func (testTool) Schema() any                                             { return map[string]string{"type": "object"} }
func (testTool) Execute(context.Context, any) (*tools.ToolResult, error) { return nil, nil }

func TestLoadSystem(t *testing.T) {
	dir := t.TempDir()
	for _, name := range systemTemplates {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}

	registry := tools.NewRegistry()
	if err := registry.Register(testTool{}); err != nil {
		t.Fatal(err)
	}
	prompt, err := LoadSystem(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "system\ncoding\ntools\n# Available Tools") || !strings.Contains(prompt, "## test") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}
