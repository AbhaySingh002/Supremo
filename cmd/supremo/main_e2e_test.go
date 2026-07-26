package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/ui"
)

func TestTUIProgramRunsWithInjectedTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	model := ui.New(nil, &agent.Session{ID: "test"}, ctx, cancel)
	program := tea.NewProgram(model, tea.WithInput(bytes.NewBufferString("\x03")), tea.WithOutput(&output), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err := program.Run()
	if !errors.Is(err, tea.ErrProgramKilled) && err != nil {
		t.Fatalf("run TUI: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("expected TUI output")
	}
}
