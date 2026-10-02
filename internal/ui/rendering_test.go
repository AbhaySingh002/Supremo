package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestStreamCoalescingNoTextLoss(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-stream", Name: "Test Stream"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	// Simulate streaming multiple fast chunks
	chunks := []string{"Hello ", "world, ", "this ", "is ", "a ", "streamed ", "response."}
	for _, chunk := range chunks {
		_ = model.applyProgress(progressEvent{
			Kind:    progressStream,
			Message: chunk,
		})
	}

	// Flush any pending stream
	model.flushStreaming()

	if model.streamingEntry < 0 || model.streamingEntry >= len(model.entries) {
		t.Fatalf("expected active streamingEntry, got %d", model.streamingEntry)
	}

	expected := "Hello world, this is a streamed response."
	actual := model.entries[model.streamingEntry].content
	if actual != expected {
		t.Fatalf("expected accumulated stream %q, got %q", expected, actual)
	}
}

func TestStreamFlushBeforeToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-tool-flush", Name: "Test Tool Flush"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	// Stream part of a response
	_ = model.applyProgress(progressEvent{
		Kind:    progressStream,
		Message: "I will now read the file. ",
	})

	// Followed immediately by a tool event
	_ = model.applyProgress(progressEvent{
		Kind:       progressTool,
		Tool:       "read_file",
		ToolStatus: "running",
		Arguments:  `{"path":"main.go"}`,
	})

	// Narration must fold into the transient activity row, never persist as
	// a streaming transcript line.
	for _, entry := range model.entries {
		if entry.kind == entryStreaming {
			t.Fatalf("narration persisted as %v entry: %q", entry.kind, entry.content)
		}
	}
	foundActivity := false
	for _, entry := range model.entries {
		if entry.kind == entryStatus && strings.Contains(entry.content, "I will now read the file.") {
			foundActivity = true
			break
		}
	}
	if !foundActivity {
		t.Fatalf("expected narration folded into the activity row, entries: %#v", model.entries)
	}
}

func TestStreamFlushOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-cancel-flush", Name: "Test Cancel Flush"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.active = &activeTask{id: 1, ctx: ctx, cancel: cancel, kind: taskAgent}
	model.layout()

	// Stream some text into the buffer
	_ = model.applyProgress(progressEvent{
		Kind:    progressStream,
		Message: "Partial content before cancellation",
	})

	// Send user interrupt
	updated, _ := model.Update(InterruptMsg{Terminate: false})
	model = updated.(Model)

	// Verify text is present in transcript
	foundText := false
	for _, entry := range model.entries {
		if strings.Contains(entry.content, "Partial content before cancellation") {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Fatal("expected streaming buffer to be flushed upon cancellation")
	}
}

func TestHistoricalRenderedCacheAvoidsRerendering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-cache", Name: "Test Cache"}, ctx, cancel)
	model.width, model.height = 100, 40
	model.layout()

	// Append historical messages
	model.appendEntry(entryUser, "What is the capital of France?")
	model.appendEntry(entryAssistant, "The capital of France is Paris.")
	model.appendEntry(entryUser, "Tell me more about it.")

	// Initial render
	model.rebuildFeed()

	// Verify all 3 historical entries have renderedCache populated
	if len(model.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(model.entries))
	}
	for i := 0; i < 3; i++ {
		if model.entries[i].renderedCache == "" {
			t.Fatalf("entry %d renderedCache is empty", i)
		}
	}

	// Record the renderedCache of entry 0
	cached0 := model.entries[0].renderedCache

	// Mutate entry 0 renderedCache to a sentinel marker to verify it is NOT overwritten during streaming
	model.entries[0].renderedCache = "SENTINEL_CACHED_ENTRY_0"
	model.historyPrefix = ""
	model.historyPrefixCount = 0

	// Trigger rebuild
	model.rebuildFeed()

	// Stream new tokens
	for i := 0; i < 10; i++ {
		_ = model.applyProgress(progressEvent{
			Kind:    progressStream,
			Message: " Paris is known for the Eiffel Tower.",
		})
	}
	model.flushStreaming()

	// The historical entry should STILL have used the cached value
	feedView := model.feed.View()
	if !strings.Contains(feedView, "SENTINEL_CACHED_ENTRY_0") {
		t.Fatal("expected rebuildFeed to reuse cached rendered representation for historical entries")
	}

	// Restore real cached
	model.entries[0].renderedCache = cached0
}

func TestWindowResizeInvalidatesRenderCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-resize", Name: "Test Resize"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	model.appendEntry(entryUser, "A very long line that should wrap differently at different terminal widths.")
	model.rebuildFeed()

	initialWidth := model.entries[0].renderedWidth
	if initialWidth != 100 {
		t.Fatalf("expected renderedWidth 100, got %d", initialWidth)
	}

	// Resize terminal window to 60
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	model = updated.(Model)

	if model.entries[0].renderedWidth != 60 {
		t.Fatalf("expected renderedWidth to be updated to 60 after resize, got %d", model.entries[0].renderedWidth)
	}
}

func TestViewportScrollLockDuringStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-scroll", Name: "Test Scroll"}, ctx, cancel)
	model.width, model.height = 80, 10
	model.layout()

	// Populate feed with many lines
	for i := 0; i < 30; i++ {
		model.appendEntry(entryUser, "History line")
	}
	model.rebuildFeed()

	// User is initially at bottom
	if !model.followTail {
		t.Fatal("expected followTail to be true initially at bottom")
	}

	// User scrolls up (PgUp)
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	model = updated.(Model)

	if model.followTail {
		t.Fatal("expected followTail to be false after scrolling up")
	}

	// Stream new content while scrolled up
	for i := 0; i < 5; i++ {
		_ = model.applyProgress(progressEvent{
			Kind:    progressStream,
			Message: " New streaming content arriving.",
		})
	}
	model.flushStreaming()

	// Verify followTail remained false and unread updates were tracked
	if model.followTail {
		t.Fatal("expected scroll lock to remain active while new output arrives")
	}
	if model.newOutput == 0 {
		t.Fatal("expected newOutput counter to increment while scrolled up")
	}

	// User presses End to return to bottom
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	model = updated.(Model)

	if !model.followTail {
		t.Fatal("expected followTail to be restored after KeyEnd")
	}
	if model.newOutput != 0 {
		t.Fatalf("expected newOutput to reset to 0 after KeyEnd, got %d", model.newOutput)
	}
}

func TestUnicodeAndEmojiDisplayWidthTruncation(t *testing.T) {
	// Wide emoji: "🚀" (2 cells)
	// CJK: "日本語" (6 cells)
	text := "🚀 Launching 日本語 agent workflow"
	truncated := truncate(text, 15)

	if !strings.HasSuffix(truncated, "…") {
		t.Fatalf("expected truncated string to end with ellipsis, got %q", truncated)
	}

	// Ensure no broken UTF-8 bytes
	if !strings.Contains(truncated, "🚀") {
		t.Fatalf("expected emoji to be preserved intact, got %q", truncated)
	}

	// Short limit
	zero := truncate("Test", 0)
	if zero != "Test" {
		t.Fatalf("expected non-truncated string for 0 limit, got %q", zero)
	}
}

func TestLongSessionPerformanceScaling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-scale", Name: "Test Scale"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	// Populate 100 transcript entries
	for i := 0; i < 100; i++ {
		model.appendEntry(entryUser, "User prompt query")
		model.appendEntry(entryAssistant, "Assistant detailed answer explaining code.")
	}

	start := time.Now()
	// Stream 50 chunks into active stream
	for i := 0; i < 50; i++ {
		_ = model.applyProgress(progressEvent{
			Kind:    progressStream,
			Message: " Streaming token batch",
		})
	}
	model.flushStreaming()
	elapsed := time.Since(start)

	// 50 streaming operations over 200 transcript entries should scale sub-linearly and complete promptly
	if elapsed > 1*time.Second {
		t.Fatalf("streaming over 200 entries took too long: %v", elapsed)
	}
}

func TestNoColorGlyphFallbacks(t *testing.T) {
	m := Model{}
	m.styles.Ascii = true

	glyphOk := m.glyph("✓", "OK")
	if glyphOk != "OK" {
		t.Fatalf("expected ASCII fallback 'OK', got %q", glyphOk)
	}

	glyphRunning := m.glyph("●", "*")
	if glyphRunning != "*" {
		t.Fatalf("expected ASCII fallback '*', got %q", glyphRunning)
	}
}

func TestInkSignalChatHierarchyAndToolDrawers(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "ink-signal"}, ctx, cancel)
	model.width, model.height = 100, 28
	model.layout()

	user := ansi.Strip(zone.Scan(model.renderEntry(0, transcriptEntry{kind: entryUser, content: "hello"})))
	if !strings.Contains(user, "You") || !strings.Contains(user, "hello") {
		t.Fatalf("user hierarchy = %q", user)
	}
	assistant := ansi.Strip(zone.Scan(model.renderEntry(0, transcriptEntry{kind: entryAssistant, content: "ready"})))
	if !strings.Contains(assistant, "Supremo") {
		t.Fatalf("assistant hierarchy = %q", assistant)
	}

	icons := map[string]string{
		"execute_command": "$", "read_file": "◫", "write_file": "✎", "search_text": "⌕",
		"list_directory": "☰", "delete_file": "−", "git_diff": "±", "subagent": "◇",
	}
	for tool, expected := range icons {
		if icon := ansi.Strip(model.toolIcon(tool)); !strings.Contains(icon, expected) {
			t.Errorf("%s icon = %q, want %q", tool, icon, expected)
		}
	}

	directory := toolResultDetails("list_directory", `{"entries":[{"name":"main.go","path":"/tmp/main.go","type":"file"},{"name":"internal","path":"/tmp/internal","type":"directory"}]}`)
	if !strings.Contains(directory, "main.go") || !strings.Contains(directory, "internal/") || strings.Contains(directory, "Field") || strings.Contains(directory, `"entries"`) {
		t.Fatalf("directory details = %q", directory)
	}
	commandOutput := toolResultDetails("execute_command", `{"stdout":"ok\n","stderr":"","exit_code":0}`)
	nestedCommandOutput := toolResultDetails("execute_command", `{"tool":"execute_command","result":{"status":"completed","preview":"{\"stdout\":\"nested\\n\",\"stderr\":\"\",\"exit_code\":0}"}}`)
	if !strings.Contains(nestedCommandOutput, "nested") || !strings.Contains(nestedCommandOutput, "exit 0") || strings.Contains(nestedCommandOutput, "preview") {
		t.Fatalf("nested command details = %q", nestedCommandOutput)
	}
	if !toolResultFailed(`{"result":{"success":false,"message":"command failed"}}`) {
		t.Fatal("nested failed result was projected as successful")
	}
	entry := transcriptEntry{
		kind: entryTool, tool: "execute_command", toolStatus: "completed", content: "Ran command",
		arguments: `{"command":"go","args":["test","./..."]}`, details: commandOutput, expanded: true,
	}
	drawer := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if (!strings.Contains(drawer, "Ran") && !strings.Contains(drawer, "Run")) || !strings.Contains(drawer, "$ go test ./...") || !strings.Contains(drawer, "ok") || !strings.Contains(drawer, "exit 0") || strings.Contains(drawer, `"stdout"`) {
		t.Fatalf("command drawer = %q", drawer)
	}
	entry.expanded = false
	collapsed := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if (!strings.Contains(collapsed, "Ran") && !strings.Contains(collapsed, "Run")) || !strings.Contains(collapsed, "go test ./...") || strings.Contains(collapsed, "details") || strings.Contains(collapsed, `"stdout"`) {
		t.Fatalf("collapsed command row = %q", collapsed)
	}
	directoryEntry := transcriptEntry{
		kind: entryTool, tool: "list_directory", toolStatus: "completed", content: "Listed /tmp",
		arguments: `{"path":"/tmp"}`, details: directory, expanded: false,
	}
	directoryRow := ansi.Strip(zone.Scan(model.RenderToolEntry(1, directoryEntry, false)))
	if !strings.Contains(directoryRow, "Read") || !strings.Contains(directoryRow, "/tmp") {
		t.Fatalf("directory row = %q", directoryRow)
	}
	directoryEntry.expanded = true
	directoryDrawer := ansi.Strip(zone.Scan(model.RenderToolEntry(1, directoryEntry, false)))
	if !strings.Contains(directoryDrawer, "$ ls -a -- /tmp") || !strings.Contains(directoryDrawer, "main.go") || !strings.Contains(directoryDrawer, "╭") || !strings.Contains(directoryDrawer, "╯") {
		t.Fatalf("directory drawer = %q", directoryDrawer)
	}

	long := strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, "\n")
	preview, start, end, total := visibleToolDetails(long, 1)
	if strings.Count(preview, "\n")+1 != maxVisibleToolLines || start != 1 || end != 9 || total != 12 {
		t.Fatalf("bounded details = (%q, %d, %d, %d)", preview, start, end, total)
	}
}

func TestSearchToolRowsUseSemanticInlineProgress(t *testing.T) {
	model := New(nil, ".", "search-row", Options{})
	model.width, model.height = 100, 28
	model.layout()

	for _, test := range []struct {
		name      string
		tool      string
		running   string
		completed string
		arguments string
		output    string
		match     string
	}{
		{
			name:      "glob",
			tool:      "glob",
			running:   "Search",
			completed: "Search",
			arguments: `{"path":"/workspace","pattern":"*.go"}`,
			output:    `{"matches":[{"path":"internal/main.go","name":"main.go","type":"file"}]}`,
			match:     "internal/main.go",
		},
		{
			name:      "grep",
			tool:      "grep",
			running:   "Search",
			completed: "Search",
			arguments: `{"path":"/workspace","pattern":"needle"}`,
			output:    `{"matches":[{"file":"main.go","line":3,"content":"needle"}]}`,
			match:     "main.go:3  needle",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			running := transcriptEntry{
				kind:       entryTool,
				tool:       test.tool,
				toolStatus: "running",
				content:    model.formatToolSummary(test.tool, "running", test.arguments),
				arguments:  test.arguments,
			}
			renderedRunning := ansi.Strip(zone.Scan(model.RenderToolEntry(0, running, true)))
			if !strings.Contains(renderedRunning, test.running) || strings.Contains(renderedRunning, "path=") || strings.Contains(renderedRunning, "pattern=") || strings.Contains(renderedRunning, "running") {
				t.Fatalf("running row = %q", renderedRunning)
			}
			if spinner := ansi.Strip(model.spinner.View()); spinner == "" || !strings.Contains(renderedRunning, spinner) {
				t.Fatalf("running row did not render the spinner: %q", renderedRunning)
			}

			details := toolResultDetails(test.tool, test.output)
			if !strings.Contains(details, test.match) {
				t.Fatalf("formatted %s details = %q", test.tool, details)
			}
			completed := transcriptEntry{
				kind:       entryTool,
				tool:       test.tool,
				toolStatus: "completed",
				content:    model.formatToolSummary(test.tool, "completed", test.arguments),
				arguments:  test.arguments,
				details:    details,
			}
			renderedCompleted := ansi.Strip(zone.Scan(model.RenderToolEntry(0, completed, false)))
			if !strings.Contains(renderedCompleted, test.completed) || strings.Contains(renderedCompleted, "path=") || strings.Contains(renderedCompleted, "pattern=") {
				t.Fatalf("completed row = %q", renderedCompleted)
			}
			completed.expanded = true
			drawer := ansi.Strip(zone.Scan(model.RenderToolEntry(0, completed, false)))
			if !strings.Contains(drawer, test.match) {
				t.Fatalf("expanded %s drawer = %q", test.tool, drawer)
			}
		})
	}
}

func TestToolRowsRenderWorkspaceRelativePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	workspace := "/tmp/supremo-test/workspace"
	model := New(nil, workspace, "tool-paths", Options{})
	model.width, model.height = 100, 28
	model.layout()

	if got := model.formatToolSummary("read_file", "completed", `{"path":"/tmp/supremo-test/workspace/internal/ui/model.go"}`); got != "Read internal/ui/model.go" {
		t.Fatalf("in-workspace summary = %q", got)
	}
	if got := model.formatToolSummary("write_file", "completed", `{"path":"`+filepath.Join(home, "notes/todo.txt")+`"}`); got != "Updated ~/notes/todo.txt" {
		t.Fatalf("home summary = %q", got)
	}
	if got := model.formatToolSummary("list_directory", "completed", `{"path":"/opt/data"}`); got != "Listed /opt/data" {
		t.Fatalf("outside summary = %q", got)
	}

	entry := transcriptEntry{
		kind: entryTool, tool: "read_file", toolStatus: "completed",
		arguments: `{"path":"/tmp/supremo-test/workspace/PROJECT.md"}`,
	}
	row := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if !strings.Contains(row, "PROJECT.md") || strings.Contains(row, "/tmp/supremo-test/workspace") {
		t.Fatalf("tool row leaked absolute path: %q", row)
	}
}

func TestApprovalDocksAboveFooterWithTranscriptVisible(t *testing.T) {
	model := newTestModel(api.Session{ID: "approval-dock"}, context.Background(), func() {})
	model.width, model.height = 100, 28
	model.layout()
	model.appendEntry(entryAssistant, "earlier transcript context")
	model.approval = approval.NewApprovalModel("execute_command", `{"command":"find internal/ui -type f"}`, rendering.NewStyles())
	model.surface = surfaceApproval
	model.layout()

	body := ansi.Strip(model.bodyView())
	if !strings.Contains(body, "earlier transcript context") {
		t.Fatalf("approval hid the transcript: %q", body)
	}
	if strings.Contains(body, "Permission required") {
		t.Fatal("approval card rendered in the body instead of the composer slot")
	}
	dock := ansi.Strip(model.inputView())
	if !strings.Contains(dock, "Permission required") || !strings.Contains(dock, "Allow once") || !strings.Contains(dock, "Deny") || !strings.Contains(dock, "find internal/ui -type f") {
		t.Fatalf("docked approval card = %q", dock)
	}
	if height := lipgloss.Height(dock); height > model.approvalMaxHeight() {
		t.Fatalf("approval card height %d exceeds budget %d", height, model.approvalMaxHeight())
	}

	view := ansi.Strip(model.View().Content)
	transcriptAt := strings.Index(view, "earlier transcript context")
	cardAt := strings.Index(view, "Permission required")
	footerAt := strings.LastIndex(view, "↑↓ select · enter confirm · esc deny")
	if transcriptAt < 0 || cardAt < 0 || footerAt < 0 || transcriptAt >= cardAt || cardAt >= footerAt {
		t.Fatalf("view ordering transcript=%d card=%d footer=%d", transcriptAt, cardAt, footerAt)
	}
}

func TestToolBatchGroupsInModelOrderAndCollapsesForNextTurn(t *testing.T) {
	model := New(nil, ".", "tool-batch", Options{})
	model.width, model.height = 100, 28
	model.layout()
	model.recordToolEvent(progressEvent{Kind: progressTool, Turn: 1, Step: 2, CallID: "call-1", Tool: "read_file", ToolStatus: "running", Arguments: `{"path":"main.go"}`})
	model.recordToolEvent(progressEvent{Kind: progressTool, Turn: 1, Step: 2, CallID: "call-2", Tool: "execute_command", ToolStatus: "running", Arguments: `{"command":"go","args":["test","./..."]}`})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-2", Tool: "execute_command", ToolStatus: "completed", ToolOutput: `{"stdout":"ok\n","exit_code":0}`})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-1", Tool: "read_file", ToolStatus: "completed", ToolOutput: `{"path":"main.go","content":"package main"}`})

	indices := model.toolBatchIndices("1:2")
	if len(indices) != 2 || indices[0] >= indices[1] {
		t.Fatalf("batch indices = %v", indices)
	}
	open := ansi.Strip(zone.Scan(model.feed.View()))
	readIndex, commandIndex := strings.Index(open, "Read"), strings.Index(open, "Ran")
	if commandIndex < 0 {
		commandIndex = strings.Index(open, "Run")
	}
	if !strings.Contains(open, "Read 1 file, ran 1 command") || readIndex < 0 || commandIndex < 0 || readIndex > commandIndex || strings.Contains(open, "package main") || strings.Contains(open, "\nok\n") {
		t.Fatalf("open batch = %q", open)
	}

	model.collapseCompletedToolBatches()
	model.rebuildFeed()
	collapsed := ansi.Strip(zone.Scan(model.feed.View()))
	if !strings.Contains(collapsed, "Read 1 file, ran 1 command") || strings.Contains(collapsed, "Read  main.go") || strings.Contains(collapsed, "Ran   go test ./...") {
		t.Fatalf("collapsed batch = %q", collapsed)
	}
	if !model.toggleLatestToolBatch() || model.collapsedToolBatches["1:2"] {
		t.Fatal("Space-style batch toggle did not reopen the latest group")
	}
	if !model.toggleLatestTool() || !model.entries[indices[1]].expanded {
		t.Fatal("Enter-style tool toggle did not open the latest drawer")
	}
}

func TestNoColorToolRowsUseASCIIWithoutANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	model := New(nil, ".", "ascii-tools", Options{})
	model.width, model.height = 60, 24
	model.entries = []transcriptEntry{
		{kind: entryTool, tool: "read_file", toolStatus: "completed", content: "Read main.go"},
		{kind: entryTool, tool: "execute_command", toolStatus: "completed", content: "Ran go test ./...", arguments: `{"command":"go","args":["test","./..."]}`, details: "ok\nexit 0", expanded: true},
	}
	model.layout()
	rendered := model.View().Content
	if strings.Contains(rendered, "\x1b[") || !strings.Contains(rendered, "OK") || (!strings.Contains(rendered, "Ran") && !strings.Contains(rendered, "Run")) || !strings.Contains(rendered, "go test ./...") {
		t.Fatalf("ASCII tool row = %q", rendered)
	}
}

func TestAssistantMarkdownRendering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-md", Name: "Test Markdown"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	markdownText := "# Plan: make a folder supremo_rudra\n\nState: PLAN_COMPLETED\n\n## Ordered steps\n- [succeeded] make a folder `supremo_rudra`"
	model.appendEntry(entryAssistant, markdownText)
	model.rebuildFeed()

	view := model.feed.View()
	plainView := ansi.Strip(view)
	// Glamour renders headers and formatted markdown rather than verbatim raw '# Plan:'
	if !strings.Contains(plainView, "Plan: make a folder supremo_rudra") {
		t.Fatalf("expected rendered feed to contain header text, got:\n%s", plainView)
	}
	if strings.Contains(plainView, "# Plan: make a folder") {
		t.Fatalf("expected markdown header '# Plan:' to be rendered by Glamour, but found raw text:\n%s", plainView)
	}
}

func TestSpinnerTickDoesNotRebuildHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "spinner-feed"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.appendEntry(entryUser, "first")
	model.appendEntry(entryAssistant, "second")
	model.appendEntry(entryStatus, "Thinking…")
	model.liveEntry = len(model.entries) - 1
	model.active = &activeTask{id: 1, cancel: func() {}}
	model.rebuildFeed()
	historic := model.entries[0].renderedCache
	if historic == "" {
		t.Fatal("expected historical cache")
	}
	tick := model.spinner.Tick()
	if _, ok := tick.(spinner.TickMsg); !ok {
		t.Fatalf("expected spinner.TickMsg, got %T", tick)
	}
	updated, _ := model.Update(tick)
	model = updated.(Model)
	if model.entries[0].renderedCache != historic {
		t.Fatal("spinner tick rebuilt historical transcript entries")
	}
}

func TestCanonicalStyles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "styles"}, ctx, cancel)
	if got := model.styles.Title.Render("SUPREMO"); !strings.Contains(got, "SUPREMO") {
		t.Fatalf("expected rendering.Styles title, got %q", got)
	}
}

func TestLiveActivityRowUpdatesInPlaceAboveToolHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-live-activity", Name: "Test Live Activity"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()
	model.entries = []transcriptEntry{{kind: entryUser, content: "count the files"}}
	model.active = &activeTask{id: 1, ctx: ctx, cancel: cancel, kind: taskAgent}

	countStatus := func() int {
		n := 0
		for _, entry := range model.entries {
			if entry.kind == entryStatus {
				n++
			}
		}
		return n
	}

	// Narration streams, then a tool starts: one activity row, one tool row.
	_ = model.applyProgress(progressEvent{Kind: progressStream, Message: "Analyzing directory structure for UI files..."})
	_ = model.applyProgress(progressEvent{
		Kind: progressTool, CallID: "call-1", Tool: "execute_command", ToolStatus: "running",
		Arguments: `{"command":"bash","args":["-c","find internal/ui -type f"]}`,
	})
	if countStatus() != 0 {
		t.Fatalf("expected exactly zero status rows, got %d: %#v", countStatus(), model.entries)
	}
	if !strings.Contains(model.activityText, "Analyzing directory structure") {
		t.Fatalf("activity text not anchored: %q", model.activityText)
	}
	if model.entries[1].kind != entryTool {
		t.Fatalf("expected tool row at index 1, got %#v", model.entries[1])
	}

	// More narration, then the next tool starts: the SAME row updates in place.
	_ = model.applyProgress(progressEvent{Kind: progressStream, Message: "Calculating recursive counts..."})
	_ = model.applyProgress(progressEvent{
		Kind: progressTool, CallID: "call-2", Tool: "execute_command", ToolStatus: "running",
		Arguments: `{"command":"python3","args":["count.py"]}`,
	})
	if countStatus() != 0 {
		t.Fatalf("repeated narration appended a second activity row: %#v", model.entries)
	}
	if !strings.Contains(model.activityText, "Calculating recursive counts") {
		t.Fatalf("activity row did not update in place: %q", model.activityText)
	}

	// Completed tools accumulate below the activity row.
	_ = model.applyProgress(progressEvent{
		Kind: progressTool, CallID: "call-2", Tool: "execute_command", ToolStatus: "completed",
		Arguments: `{"command":"python3","args":["count.py"]}`, ToolOutput: `{"stdout":"3","exit_code":0}`,
	})
	_ = model.applyProgress(progressEvent{Kind: progressStream, Message: "Preparing summary..."})
	if countStatus() != 0 {
		t.Fatalf("expected exactly zero activity rows after tools, got %d", countStatus())
	}
	tools := 0
	for _, entry := range model.entries {
		if entry.kind == entryTool {
			tools++
		}
	}
	if tools != 2 {
		t.Fatalf("expected 2 persistent tool rows, got %d", tools)
	}

	// Snapshot rebuild mid-run re-anchors the activity row and drops narration.
	messages := []api.Message{
		{ID: "turn-1", Role: "assistant", Parts: []api.MessagePart{
			{Kind: "text", Text: "Calculating recursive counts..."},
			{Kind: "assistant_tool_call", Metadata: json.RawMessage(`{"id":"call-1","name":"execute_command","arguments":{"command":"bash","args":["-c","find internal/ui -type f"]}}`)},
		}},
		{Role: "tool", Parts: []api.MessagePart{
			{Kind: "tool_result", Text: `{"stdout":"file1.go","exit_code":0}`, Metadata: json.RawMessage(`{"tool_name":"execute_command","tool_call_id":"call-1"}`)},
		}},
	}
	restored := model.transcriptFromMessages(messages)
	if len(restored) != 1 || restored[0].kind != entryTool {
		t.Fatalf("expected narration skipped and tool preserved on replay, got %#v", restored)
	}

	// Run end removes the transient row; tools stay.
	model.finishStreaming(entryAssistant, "Here is the breakdown.")
	model.active = nil
	model.clearLiveStatus()
	model.activityText = ""
	if countStatus() != 0 {
		t.Fatalf("activity row survived run end: %#v", model.entries)
	}
}

func TestCommandRowsStaySingleLine(t *testing.T) {
	model := New(nil, ".", "single-line", Options{})
	model.width, model.height = 100, 28
	model.layout()

	entry := transcriptEntry{
		kind: entryTool, tool: "execute_command", toolStatus: "completed", content: "Ran command",
		arguments: `{"command":"python3 -c","args":["import os\nimport sys\n\nroot = '/workspace'\nfor dirpath, dirnames, filenames in os.walk(root):\n    print(dirpath, len(dirnames), len(filenames))"]}`,
		details:   "workspace 12 40\nexit 0",
	}
	row := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if strings.Contains(strings.Split(row, "\n")[0]+"\n", "print(dirpath") {
		t.Fatalf("command row leaked the full multi-line script: %q", row)
	}
	if strings.Count(strings.TrimRight(row, "\n"), "\n") != 0 {
		t.Fatalf("command row spans multiple lines:\n%s", row)
	}
	if !strings.Contains(row, "python3 -c import os") {
		t.Fatalf("command row lost the command head: %q", row)
	}

	entry.expanded = true
	drawer := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if !strings.Contains(drawer, "os.walk(root)") {
		t.Fatalf("expanded drawer lost the full command: %q", drawer)
	}
}

func TestInlineIntentLifecycleAndCleanComposer(t *testing.T) {
	model := New(nil, ".", "inline-intent-test", Options{})
	model.width, model.height = 100, 30
	model.layout()

	// 1. When idle, composer has 3 lines: rule, input prompt, and status line.
	input := model.inputView()
	lines := strings.Split(input, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in inputView (rule, prompt, status), got %d lines:\n%s", len(lines), input)
	}

	// 2. Cursor Y is always at composerTopRow + 1 (on prompt row).
	cursorIdle := model.nativeComposerCursor()
	if cursorIdle == nil || cursorIdle.Y != model.composerTopRow+1 {
		t.Fatalf("idle cursor Y = %v, want %d", cursorIdle, model.composerTopRow+1)
	}

	// 3. When a task starts, an entryStatus is created directly under entryUser.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.ctx = ctx
	_ = model.startTask("count the files")

	if len(model.entries) < 2 {
		t.Fatalf("expected at least 2 entries (user and intent), got %d", len(model.entries))
	}
	if model.entries[0].kind != entryUser || model.entries[1].kind != entryStatus {
		t.Fatalf("expected entryUser followed by entryStatus, got %#v", model.entries)
	}
	if model.entries[1].content != "Thinking…" && model.entries[1].content != "Working..." {
		t.Fatalf("expected initial intent text, got %q", model.entries[1].content)
	}

	// 4. Intent updates in-place when new intent arrives from model.
	model.setStatus("Counting files in all folders…")
	if model.entries[1].content != "Counting files in all folders…" {
		t.Fatalf("intent line did not update in place: %q", model.entries[1].content)
	}
	if len(model.entries) != 2 {
		t.Fatalf("expected entry count to remain 2, got %d", len(model.entries))
	}

	// 5. While active, composer remains clean (3 lines) and cursor stays stable.
	activeInput := model.inputView()
	if len(strings.Split(activeInput, "\n")) != 3 {
		t.Fatalf("expected composer to remain 3 lines during active turn, got:\n%s", activeInput)
	}
	_ = model.input.Focus()
	cursorActive := model.nativeComposerCursor()
	if cursorActive == nil || cursorActive.Y != model.composerTopRow+1 {
		t.Fatalf("active cursor Y = %v, want %d", cursorActive, model.composerTopRow+1)
	}

	// 6. Tools run and snapshot refreshes mid-run: intent line stays at index 1 directly under entryUser.
	_ = model.applyProgress(progressEvent{
		Kind: progressTool, CallID: "call-1", Tool: "execute_command", ToolStatus: "running",
		Arguments: `{"command":"bash","args":["-c","find . -type f | wc -l"]}`,
	})
	midRunSnapshot := api.SessionSnapshot{
		Session: api.Session{ID: "inline-intent-test"},
		Messages: []api.Message{
			{Role: "user", Parts: []api.MessagePart{{Kind: "text", Text: "count the files"}}},
			{Role: "tool", Parts: []api.MessagePart{{Kind: "tool_result", Text: `{"stdout":"42","exit_code":0}`, Metadata: json.RawMessage(`{"tool_call_id":"call-1","tool_name":"execute_command"}`)}}},
		},
		Runs: []api.Run{{RunID: "run-1", Status: "running"}},
	}
	model.applySnapshot(midRunSnapshot)
	if len(model.entries) != 3 {
		t.Fatalf("expected 3 entries after snapshot (user, intent, tool), got %d: %#v", len(model.entries), model.entries)
	}
	if model.entries[0].kind != entryUser || model.entries[1].kind != entryStatus || model.entries[2].kind != entryTool {
		t.Fatalf("expected entryUser -> entryStatus -> entryTool, got kinds: %v, %v, %v", model.entries[0].kind, model.entries[1].kind, model.entries[2].kind)
	}
	if model.entries[1].content != "Counting files in all folders…" {
		t.Fatalf("intent text lost across snapshot: %q", model.entries[1].content)
	}
	if model.intentEntry != 1 || model.liveEntry != 1 {
		t.Fatalf("intentEntry=%d, liveEntry=%d, want 1, 1", model.intentEntry, model.liveEntry)
	}
	liveRendered := model.renderEntry(1, model.entries[1])
	if !strings.Contains(liveRendered, model.spinner.View()) {
		t.Fatalf("expected live spinner in intent row, got: %q", liveRendered)
	}

	// 7. When run ends, intent line completes with checkmark.
	event := api.Event{Type: api.EventRunEnd, Data: []byte(`{"status":"completed"}`)}
	_ = (&model).applyAPIEvent(event)
	if model.entries[1].toolStatus != "completed" {
		t.Fatalf("expected intent entry to be marked completed, got %q", model.entries[1].toolStatus)
	}
	rendered := model.renderEntry(1, model.entries[1])
	if !strings.Contains(rendered, "✓") && !strings.Contains(rendered, "OK") {
		t.Fatalf("expected completed checkmark in rendered intent row, got:\n%s", rendered)
	}

	// 8. Subsequent snapshot refresh preserves the completed intent line.
	finalSnapshot := api.SessionSnapshot{
		Session:  api.Session{ID: "inline-intent-test"},
		Messages: midRunSnapshot.Messages,
		Runs:     []api.Run{{RunID: "run-1", Status: "completed"}},
	}
	model.applySnapshot(finalSnapshot)
	if len(model.entries) != 3 || model.entries[1].kind != entryStatus || model.entries[1].toolStatus != "completed" {
		t.Fatalf("completed intent row not preserved across final snapshot: %#v", model.entries)
	}

	// 9. When next task starts, prior turn's ephemeral completed intent is cleaned up.
	_ = model.startTask("second prompt")
	// Now entries: user1, tool1, user2, intent2 (working...)
	foundOldCompletedIntent := false
	for _, entry := range model.entries[:len(model.entries)-1] {
		if entry.kind == entryStatus && entry.toolStatus == "completed" {
			foundOldCompletedIntent = true
		}
	}
	if foundOldCompletedIntent {
		t.Fatalf("prior completed intent was not cleaned up on new turn: %#v", model.entries)
	}
	latestIntent := model.entries[len(model.entries)-1]
	if latestIntent.kind != entryStatus || (latestIntent.content != "Thinking…" && latestIntent.content != "Working...") {
		t.Fatalf("expected fresh live intent for new turn, got: %#v", latestIntent)
	}
}
