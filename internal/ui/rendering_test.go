package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
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

	// Verify the streaming content was flushed and is present in entries
	foundStream := false
	for _, entry := range model.entries {
		if entry.kind == entryStreaming && strings.Contains(entry.content, "I will now read the file.") {
			foundStream = true
			break
		}
	}
	if !foundStream {
		t.Fatal("expected streaming chunk to be flushed before tool execution was recorded")
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

	// 50 streaming operations over 200 transcript entries should complete in well under 100ms
	if elapsed > 200*time.Millisecond {
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
	if strings.Contains(user, "you") || !strings.Contains(user, "hello") {
		t.Fatalf("user hierarchy = %q", user)
	}
	assistant := ansi.Strip(zone.Scan(model.renderEntry(0, transcriptEntry{kind: entryAssistant, content: "ready"})))
	if !strings.Contains(assistant, model.glyph("◆", "*")+" Supremo") {
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
		kind: entryTool, tool: "execute_command", toolStatus: "completed", content: "go test ./...",
		arguments: `{"command":"go","args":["test","./..."]}`, details: commandOutput, expanded: true,
	}
	drawer := ansi.Strip(zone.Scan(model.RenderToolEntry(0, entry, false)))
	if !strings.Contains(drawer, "$ go test ./...") || !strings.Contains(drawer, "ok") || !strings.Contains(drawer, "exit 0") || strings.Contains(drawer, `"stdout"`) {
		t.Fatalf("command drawer = %q", drawer)
	}

	long := strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, "\n")
	preview, start, end, total := visibleToolDetails(long, 1)
	if strings.Count(preview, "\n")+1 != maxVisibleToolLines || start != 1 || end != 11 || total != 12 {
		t.Fatalf("bounded details = (%q, %d, %d, %d)", preview, start, end, total)
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
	readIndex, commandIndex := strings.Index(open, "Read main.go"), strings.Index(open, "Ran go test ./...")
	if !strings.Contains(open, "Read 1 file, ran 1 command") || readIndex < 0 || commandIndex < 0 || readIndex > commandIndex || strings.Contains(open, "package main") || strings.Contains(open, "\nok\n") {
		t.Fatalf("open batch = %q", open)
	}

	model.collapseCompletedToolBatches()
	model.rebuildFeed()
	collapsed := ansi.Strip(zone.Scan(model.feed.View()))
	if !strings.Contains(collapsed, "Read 1 file, ran 1 command") || strings.Contains(collapsed, "Read main.go") || strings.Contains(collapsed, "Ran go test ./...") {
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
	if strings.Contains(rendered, "\x1b[") || !strings.Contains(rendered, "R") || !strings.Contains(rendered, "OK") || !strings.Contains(rendered, "$ go test ./...") || !strings.Contains(rendered, "+-") {
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
