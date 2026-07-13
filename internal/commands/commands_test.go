package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommands_Registry(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 7 {
		t.Fatalf("expected 7 commands, got %d", len(list))
	}

	expectedNames := []string{"/help", "/clear", "/reset", "/auth", "/model", "/config", "/exit"}
	for i, cmd := range list {
		if cmd.Name != expectedNames[i] {
			t.Errorf("expected command %d to be %s, got %s", i, expectedNames[i], cmd.Name)
		}
	}
}

func TestCommands_Handle_NonCommand(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	out, handled, err := r.Handle(ctx, nil, nil, "hello there")
	if handled {
		t.Error("expected handled to be false for non-command")
	}
	if out != "" || err != nil {
		t.Errorf("unexpected output/error: %q, %v", out, err)
	}
}

func TestCommands_Handle_UnknownCommand(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	out, handled, err := r.Handle(ctx, nil, nil, "/unknown")
	if !handled {
		t.Error("expected handled to be true for unknown command")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", out)
	}
}

func TestCommands_Exit(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	_, handled, err := r.Handle(ctx, nil, nil, "/exit")
	if !handled {
		t.Error("expected handled to be true")
	}
	if !errors.Is(err, ErrExit) {
		t.Errorf("expected ErrExit, got %v", err)
	}
}
