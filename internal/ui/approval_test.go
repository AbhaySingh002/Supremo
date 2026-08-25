package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/api"
)

func TestApprovalKeepsExactApprovedCommandInTranscript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "approval", Name: "Approval"}, ctx, cancel)
	event := progressEvent{Tool: "execute_command", ToolStatus: "waiting approval", Arguments: `execute_command {"command":"go","args":["test","./..."],"directory":"."}`}
	model.recordToolEvent(event)
	model.recordToolEvent(progressEvent{Tool: "execute_command", ToolStatus: "approved", Arguments: event.Arguments})
	if len(model.entries) != 1 || model.entries[0].toolStatus != "approved" || !strings.Contains(model.entries[0].content, "Approved") || !strings.Contains(model.entries[0].details, "$ go test ./...") {
		t.Fatalf("approval transcript = %#v", model.entries)
	}
}

func TestModeToggle_Cycle(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &api.Session{ID: "test-mode", Name: "Test Mode", ApprovalMode: "strict"}
	model := newTestModel(*session, ctx, cancel)
	model.workspace = root
	model.client = sessionTestClient{}
	cycle := func() {
		_, cmd := model.cycleApprovalMode()
		updated, _ := model.Update(cmd())
		model = updated.(Model)
	}

	// 1. strict -> batman
	cycle()
	if model.session.ApprovalMode != "batman" {
		t.Fatalf("expected batman, got %s", model.session.ApprovalMode)
	}

	// 2. batman -> superman
	cycle()
	if model.session.ApprovalMode != "superman" {
		t.Fatalf("expected superman, got %s", model.session.ApprovalMode)
	}

	// 3. superman -> strict
	cycle()
	if model.session.ApprovalMode != "strict" {
		t.Fatalf("expected strict, got %s", model.session.ApprovalMode)
	}
}
