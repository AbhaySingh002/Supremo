package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
)

func TestBubbleZoneMouseInteractions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "zone-test"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.entries = []transcriptEntry{
		{kind: entryUser, content: "Test prompt"},
		{kind: entryTool, tool: "read_file", content: "Read main.go", details: "package main\n\nfunc main() {}", toolStatus: "completed", expanded: false},
	}
	model.layout()

	// 1. Render view to populate BubbleZone scan
	view := model.View()
	if !strings.Contains(view.Content, "SUPREMO") {
		t.Fatalf("expected view with title, got:\n%s", view.Content)
	}

	// 2. Click tool row expands tool details
	toolZone := zone.Get("tool-1")
	if toolZone != nil {
		clickMsg := tea.MouseClickMsg(tea.Mouse{
			X:      toolZone.StartX + 1,
			Y:      toolZone.StartY,
			Button: tea.MouseLeft,
		})
		updated, _ := model.Update(clickMsg)
		model = updated.(Model)
		if !model.entries[1].expanded {
			t.Fatal("expected tool entry to be expanded after mouse click")
		}
	}

	// 3. Unread updates pill click jumps to bottom and clears unread counter
	model.followTail = false
	model.newOutput = 8
	model.layout()
	_ = model.View() // scan new zones

	unreadZone := zone.Get("unread-pill")
	if unreadZone != nil {
		clickMsg := tea.MouseClickMsg(tea.Mouse{
			X:      unreadZone.StartX + 1,
			Y:      unreadZone.StartY,
			Button: tea.MouseLeft,
		})
		updated, _ := model.Update(clickMsg)
		model = updated.(Model)
		if !model.followTail || model.newOutput != 0 {
			t.Fatalf("expected unread pill click to follow tail (followTail=%v, newOutput=%d)", model.followTail, model.newOutput)
		}
	}
}

func TestCopyLastAssistantResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "copy-test"}, ctx, cancel)
	model.entries = append(model.entries, transcriptEntry{kind: entryUser, content: "hello"})
	model.entries = append(model.entries, transcriptEntry{kind: entryAssistant, content: "world answer"})

	_ = model.copyLastAssistantResponse()
	if len(model.entries) < 3 || !strings.Contains(model.entries[len(model.entries)-1].content, "Copied last Supremo response") {
		t.Fatalf("unexpected transcript after copy: %#v", model.entries)
	}
}

func TestBlinkingCursorInitializationAndRendering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "cursor-test"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	// 1. Cursor mode is Blink
	if !model.input.Styles().Cursor.Blink {
		t.Fatal("expected cursor blink enabled")
	}

	// 2. Initial focus returns a non-nil Blink command
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("expected Init() to return batch command including cursor focus/blink")
	}

	// 3. Composer view renders with cursor in empty and non-empty states
	emptyView := model.inputView()
	if !strings.Contains(emptyView, composerPlaceholder) {
		t.Fatalf("expected composer placeholder in empty view, got: %q", emptyView)
	}

	model.input.SetValue("check @file")
	model.input.CursorEnd()
	nonEmptyView := model.inputView()
	if !strings.Contains(nonEmptyView, "check @file") {
		t.Fatalf("expected composer text in rendered view, got: %q", nonEmptyView)
	}
}

func TestInitRequestsWindowSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "init-size"}, ctx, cancel)
	if model.Init() == nil {
		t.Fatal("expected Init to return commands including window size")
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(Model)
	if model.width != 120 || model.height != 40 {
		t.Fatalf("layout after WindowSizeMsg: %dx%d", model.width, model.height)
	}
}

func TestCancelOrQuitSharedByCtrlCAndInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	busy := newTestModel(api.Session{ID: "cancel-busy"}, ctx, cancel)
	taskCtx, taskCancel := context.WithCancel(ctx)
	busy.active = &activeTask{id: 1, ctx: taskCtx, cancel: taskCancel}
	updated, cmd := busy.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	busy = updated.(Model)
	if cmd != nil {
		t.Fatal("ctrl+c during task should not quit")
	}
	if !busy.cancelling || busy.active == nil {
		t.Fatal("ctrl+c should cancel the active task")
	}

	sig := newTestModel(api.Session{ID: "cancel-sig"}, ctx, cancel)
	taskCtx2, taskCancel2 := context.WithCancel(ctx)
	sig.active = &activeTask{id: 2, ctx: taskCtx2, cancel: taskCancel2}
	updated, cmd = sig.Update(InterruptMsg{Terminate: false})
	sig = updated.(Model)
	if cmd != nil {
		t.Fatal("SIGINT during task should not quit")
	}
	if !sig.cancelling {
		t.Fatal("InterruptMsg should use the same cancel path")
	}

	idle := newTestModel(api.Session{ID: "cancel-idle"}, ctx, cancel)
	_, cmd = idle.Update(InterruptMsg{Terminate: false})
	if cmd == nil {
		t.Fatal("idle interrupt should quit")
	}

	term := newTestModel(api.Session{ID: "cancel-term"}, ctx, cancel)
	taskCtx3, taskCancel3 := context.WithCancel(ctx)
	term.active = &activeTask{id: 3, ctx: taskCtx3, cancel: taskCancel3}
	updated, cmd = term.Update(InterruptMsg{Terminate: true})
	term = updated.(Model)
	if !term.quitWhenIdle || cmd == nil {
		t.Fatal("SIGTERM should cancel with a termination deadline")
	}
}

func TestToolRowClickAndAttachedFilesDisplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "click-attach-test"}, ctx, cancel)
	model.width, model.height = 120, 35
	model.showActivity = true
	model.entries = []transcriptEntry{
		{
			kind:    entryUser,
			content: "read the @main.go\n\n---\n### Attached Context Files\n\n#### `main.go`\n```\npackage main\n```\n",
		},
		{
			kind:       entryTool,
			tool:       "list_directory",
			content:    "Listed .",
			details:    "main.go\nREADME.md",
			toolStatus: "completed",
			expanded:   false,
		},
	}
	model.activity = []activityEvent{
		{Tool: "list_directory", Status: "completed", Arguments: `{"path":"."}`},
	}
	model.layout()

	// 1. Verify User entry does not contain raw attached context files in the UI render
	renderedUser := model.renderEntry(0, model.entries[0])
	if strings.Contains(renderedUser, "### Attached Context Files") || strings.Contains(renderedUser, "package main") {
		t.Fatalf("user prompt still renders raw attached file contents: %q", renderedUser)
	}
	if !strings.Contains(renderedUser, "main.go") {
		t.Fatalf("expected main.go badge or mention in user prompt, got: %q", renderedUser)
	}

	// 2. Render view to populate zones
	_ = model.View()

	// 3. Click tool row using zoneInRow (e.g. clicking anywhere on row)
	toolZone := zone.Get("tool-1")
	if toolZone != nil {
		clickMsg := tea.MouseClickMsg(tea.Mouse{
			X:      toolZone.StartX + 50, // click past text
			Y:      toolZone.StartY,
			Button: tea.MouseLeft,
		})
		updated, _ := model.Update(clickMsg)
		model = updated.(Model)
		if !model.entries[1].expanded {
			t.Fatal("expected tool row click anywhere on row to expand details")
		}
	}
}
