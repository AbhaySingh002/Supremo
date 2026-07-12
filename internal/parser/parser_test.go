package parser

import (
	"errors"
	"testing"
)

func TestParser_Parse_NoToolBlock(t *testing.T) {
	p := NewParser()
	raw := "Hello, I am ready to help you. Let's finish the task."
	resp, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Thought != "" {
		t.Errorf("expected empty thought, got %q", resp.Thought)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.FinalAnswer != raw {
		t.Errorf("expected final answer to equal raw input, got %q", resp.FinalAnswer)
	}
}

func TestParser_Parse_SingleToolBlock(t *testing.T) {
	p := NewParser()
	raw := `I will read the configuration file first.
<tool_call>
{
	"tool": "read_file",
	"arguments": {
		"path": "config.yaml"
	}
}
</tool_call>
I hope this works.`

	resp, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedThought := "I will read the configuration file first.\nI hope this works."

	if resp.Thought != expectedThought {
		t.Errorf("expected thought %q, got %q", expectedThought, resp.Thought)
	}
	if resp.FinalAnswer != "" {
		t.Errorf("expected empty final answer, got %q", resp.FinalAnswer)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", resp.ToolCalls[0].Name)
	}

	argsMap, ok := resp.ToolCalls[0].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("expected arguments to be map[string]any")
	}
	if argsMap["path"] != "config.yaml" {
		t.Errorf("expected path 'config.yaml', got %v", argsMap["path"])
	}
}

func TestParser_Parse_MultipleToolBlocks(t *testing.T) {
	p := NewParser()
	raw := `Let's perform multiple actions.
<tool_call>
{
	"tool": "create_file",
	"arguments": {"path": "test.txt"}
}
</tool_call>
Intermediate thought.
<tool_call>
{
	"tool": "write_file",
	"arguments": {"path": "test.txt", "content": "hello"}
}
</tool_call>
All actions scheduled.`

	resp, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedThought := "Let's perform multiple actions.\nIntermediate thought.\nAll actions scheduled."

	if resp.Thought != expectedThought {
		t.Errorf("expected thought %q, got %q", expectedThought, resp.Thought)
	}
	if resp.FinalAnswer != "" {
		t.Errorf("expected empty final answer, got %q", resp.FinalAnswer)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "create_file" || resp.ToolCalls[1].Name != "write_file" {
		t.Errorf("unexpected tool sequence: %s, %s", resp.ToolCalls[0].Name, resp.ToolCalls[1].Name)
	}
}

func TestParser_Parse_FinalAnswer(t *testing.T) {
	p := NewParser()
	raw := `<final_answer>
Task completed successfully. Created the HTTP server in the test directory.
</final_answer>`

	resp, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.FinalAnswer != "Task completed successfully. Created the HTTP server in the test directory." {
		t.Errorf("unexpected final answer: %q", resp.FinalAnswer)
	}
}

func TestParser_Parse_ToolsAndFinalAnswer(t *testing.T) {
	p := NewParser()
	raw := `I will create the file.
<tool_call>
{
	"tool": "create_file",
	"arguments": {"path": "test.txt"}
}
</tool_call>
<final_answer>
Done.
</final_answer>`

	resp, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.FinalAnswer != "Done." {
		t.Errorf("expected final answer 'Done.', got %q", resp.FinalAnswer)
	}
}

func TestParser_Parse_InvalidJSON(t *testing.T) {
	p := NewParser()
	raw := "<tool_call>\n{invalid json}\n</tool_call>"
	_, err := p.Parse(raw)
	if !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("expected ErrInvalidJSON, got %v", err)
	}
}

func TestParser_Parse_MalformedTag(t *testing.T) {
	p := NewParser()
	// Missing ending tag
	raw := "<tool_call>\n{\"tool\": \"test\", \"arguments\": {}}"
	_, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error for missing end tag: %v", err)
	}
}

func TestParser_Parse_EmptyResponse(t *testing.T) {
	p := NewParser()
	resp, err := p.Parse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Thought != "" || len(resp.ToolCalls) != 0 || resp.FinalAnswer != "" {
		t.Errorf("expected empty response, got %+v", resp)
	}
}

func TestParser_Parse_ValidationErrors(t *testing.T) {
	p := NewParser()

	// Missing tool name
	rawMissingName := "<tool_call>\n{\"arguments\": {}}\n</tool_call>"
	_, err := p.Parse(rawMissingName)
	if !errors.Is(err, ErrMissingToolName) {
		t.Errorf("expected ErrMissingToolName, got %v", err)
	}

	// Missing arguments
	rawMissingArgs := "<tool_call>\n{\"tool\": \"test_tool\"}\n</tool_call>"
	_, err = p.Parse(rawMissingArgs)
	if !errors.Is(err, ErrMissingArguments) {
		t.Errorf("expected ErrMissingArguments, got %v", err)
	}
}
