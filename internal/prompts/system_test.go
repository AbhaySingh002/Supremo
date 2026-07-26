package prompts

import (
	"context"
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
	registry := tools.NewRegistry()
	if err := registry.Register(testTool{}); err != nil {
		t.Fatal(err)
	}
	prompt, err := LoadSystem(registry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "# System Instructions") || !strings.Contains(prompt, "## test") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}
