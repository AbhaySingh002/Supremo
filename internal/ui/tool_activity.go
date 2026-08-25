package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/ui/components"
)

const maxVisibleToolLines = 10

type toolFamily int

const (
	toolOther toolFamily = iota
	toolCommand
	toolRead
	toolWrite
	toolSearch
	toolList
	toolCreate
	toolDelete
	toolRename
	toolGit
	toolWeb
	toolAgent
)

func toolFamilyFor(tool string) toolFamily {
	switch tool {
	case "execute_command", "local_shell":
		return toolCommand
	case "read_file", "file_info":
		return toolRead
	case "write_file", "replace_in_file":
		return toolWrite
	case "search_file_name", "search_text", "find_symbol", "find_references", "repository_query":
		return toolSearch
	case "list_directory":
		return toolList
	case "create_directory":
		return toolCreate
	case "delete_file":
		return toolDelete
	case "rename_file":
		return toolRename
	case "git_status", "git_diff", "git_log":
		return toolGit
	case "web_fetch":
		return toolWeb
	case "subagent", "list_agents", "send_message", "wait_agent", "interrupt_agent":
		return toolAgent
	default:
		return toolOther
	}
}

func (m Model) toolIcon(tool string) string {
	symbol, fallback, style := "·", "-", m.styles.Tool
	switch toolFamilyFor(tool) {
	case toolCommand:
		symbol, fallback, style = "$", "$", m.styles.ToolCommand
	case toolRead:
		symbol, fallback, style = "◫", "R", m.styles.ToolRead
	case toolWrite:
		symbol, fallback, style = "✎", "W", m.styles.ToolWrite
	case toolSearch:
		symbol, fallback, style = "⌕", "S", m.styles.ToolSearch
	case toolList:
		symbol, fallback, style = "☰", "L", m.styles.ToolRead
	case toolCreate:
		symbol, fallback, style = "+", "+", m.styles.ToolWrite
	case toolDelete:
		symbol, fallback, style = "−", "-", m.styles.ToolFailure
	case toolRename:
		symbol, fallback, style = "↪", ">", m.styles.ToolWrite
	case toolGit:
		symbol, fallback, style = "±", "G", m.styles.ToolGit
	case toolWeb:
		symbol, fallback, style = "↗", "W", m.styles.ToolRead
	case toolAgent:
		symbol, fallback, style = "◇", "A", m.styles.ToolAgent
	}
	return style.Width(2).Render(m.glyph(symbol, fallback))
}

// formatToolSummary produces a clean, high-signal single line summary for a tool execution.
func formatToolSummary(tool, status, arguments string) string {
	path := toolLocation(arguments)
	switch tool {
	case "read_file":
		return withTarget("Read", path)
	case "write_file", "replace_in_file":
		return withTarget("Updated", path)
	case "delete_file":
		return withTarget("Removed", path)
	case "rename_file":
		return "Renamed files"
	case "create_directory":
		return withTarget("Created folder", path)
	case "list_directory":
		return withTarget("Listed", path)
	case "search_file_name":
		return withTarget("Found files", quotedToolTarget(firstToolArg(arguments, "pattern", "query")))
	case "search_text":
		return withTarget("Searched", quotedToolTarget(firstToolArg(arguments, "pattern", "query")))
	case "find_symbol":
		return withTarget("Found symbol", quotedToolTarget(toolArgument(arguments, "symbol")))
	case "find_references":
		return withTarget("Found references to", quotedToolTarget(toolArgument(arguments, "symbol")))
	case "repository_query":
		return withTarget("Queried repository", quotedToolTarget(toolArgument(arguments, "query")))
	case "git_status":
		return "Checked git status"
	case "git_diff":
		return "Reviewed git diff"
	case "git_log":
		return "Read git log"
	case "execute_command", "local_shell":
		cmd := toolArgument(arguments, "command")
		if args := toolArguments(arguments, "args"); len(args) > 0 {
			cmd += " " + strings.Join(args, " ")
		}
		return withTarget("Ran", truncate(strings.TrimSpace(cmd), 60))
	case "subagent":
		return withTarget("Delegated", quotedToolTarget(toolArgument(arguments, "label")))
	case "list_agents":
		return "Listed agents"
	case "send_message":
		return withTarget("Messaged agent", quotedToolTarget(firstToolArg(arguments, "agent_id", "id")))
	case "wait_agent":
		return withTarget("Waited for agent", quotedToolTarget(firstToolArg(arguments, "agent_id", "id")))
	case "interrupt_agent":
		return withTarget("Interrupted agent", quotedToolTarget(firstToolArg(arguments, "agent_id", "id")))
	case "web_fetch":
		return "Fetched " + truncate(toolArgument(arguments, "url"), 50)
	default:
		return strings.ReplaceAll(tool, "_", " ")
	}
}

// toolLocation extracts the primary file or directory path from JSON arguments.
func toolLocation(arguments string) string {
	for _, name := range []string{"path", "directory"} {
		if value := toolArgument(arguments, name); value != "" {
			return value
		}
	}
	return ""
}

func firstToolArg(arguments string, names ...string) string {
	for _, name := range names {
		if value := toolArgument(arguments, name); value != "" {
			return value
		}
	}
	return ""
}

// toolArgument parses a single string field from JSON arguments.
func toolArgument(arguments, key string) string {
	start := strings.Index(arguments, "{")
	if start < 0 {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(arguments[start:]), &payload) != nil {
		return ""
	}
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

// toolArguments parses an array of string fields from JSON arguments.
func toolArguments(arguments, key string) []string {
	start := strings.Index(arguments, "{")
	if start < 0 {
		return nil
	}
	var payload map[string]any
	if json.Unmarshal([]byte(arguments[start:]), &payload) != nil {
		return nil
	}
	items, ok := payload[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func withTarget(action, target string) string {
	if target == "" {
		return action
	}
	return fmt.Sprintf("%s %s", action, target)
}

func quotedToolTarget(target string) string {
	if target == "" {
		return ""
	}
	return fmt.Sprintf("%q", target)
}

// ToolStatusBadge returns a semantic, styled badge for the tool status.
func (m Model) ToolStatusBadge(status string, live bool) string {
	state := strings.ToLower(status)
	switch {
	case live || state == "running":
		return m.styles.ToolRunning.Render(m.glyph("…", "...") + " running")
	case state == "completed" || state == "approved":
		if state == "approved" {
			return m.styles.ToolSuccess.Render(m.glyph("✓", "OK") + " approved")
		}
		return m.styles.ToolSuccess.Render(m.glyph("✓", "OK"))
	case state == "dry run":
		return m.styles.Warning.Render(m.glyph("○", "-") + " dry run")
	case state == "canceled" || state == "timed out":
		return m.styles.Warning.Render(m.glyph("×", "X") + " " + state)
	case state == "failed" || state == "denied":
		return m.styles.ToolFailure.Render(m.glyph("×", "X") + " " + state)
	case state == "waiting approval":
		return m.styles.Warning.Render("? approval")
	case state == "queued":
		return m.styles.Muted.Render(m.glyph("○", "-") + " queued")
	default:
		if state == "" {
			state = "queued"
		}
		return m.styles.Muted.Render(m.glyph("○", "-") + " " + state)
	}
}

func (m Model) toolBatchIndices(batchID string) []int {
	if batchID == "" {
		return nil
	}
	indices := make([]int, 0, 4)
	for index := range m.entries {
		if m.entries[index].kind == entryTool && m.entries[index].toolBatchID == batchID {
			indices = append(indices, index)
		}
	}
	return indices
}

func (m Model) renderToolBatch(indices []int) string {
	if len(indices) == 0 {
		return ""
	}
	batchID := m.entries[indices[0]].toolBatchID
	collapsed := m.collapsedToolBatches[batchID]
	arrow := m.glyph("▸", ">")
	if !collapsed {
		arrow = m.glyph("▾", "v")
	}
	icon := m.toolIcon(m.batchIconTool(indices))
	status := m.ToolStatusBadge(toolBatchStatus(m.entries, indices), false)
	summaryWidth := max(8, m.contentWidth()-ansi.StringWidth(icon)-ansi.StringWidth(status)-7)
	summary := ansi.Truncate(toolBatchSummary(m.entries, indices), summaryWidth, "…")
	header := icon + " " + m.styles.Muted.Render(summary) + "  " + status + "  " + m.styles.Muted.Render(arrow)
	header = zone.Mark(fmt.Sprintf("tool-batch-%d", indices[0]), header)
	if collapsed {
		return header
	}
	children := make([]string, 0, len(indices))
	for _, index := range indices {
		children = append(children, m.renderToolEntry(index, m.entries[index], m.active != nil && index == m.liveEntry, true))
	}
	return header + "\n" + m.styles.Text.PaddingLeft(2).Render(strings.Join(children, "\n"))
}

func (m Model) batchIconTool(indices []int) string {
	for _, family := range []toolFamily{toolWrite, toolCreate, toolRename, toolDelete, toolCommand, toolSearch, toolRead, toolList, toolGit, toolWeb, toolAgent} {
		for _, index := range indices {
			if toolFamilyFor(m.entries[index].tool) == family {
				return m.entries[index].tool
			}
		}
	}
	return m.entries[indices[0]].tool
}

func toolBatchSummary(entries []transcriptEntry, indices []int) string {
	counts := make(map[toolFamily]int)
	for _, index := range indices {
		counts[toolFamilyFor(entries[index].tool)]++
	}
	parts := make([]string, 0, 6)
	changes := counts[toolWrite] + counts[toolCreate] + counts[toolRename] + counts[toolDelete]
	if changes > 0 {
		parts = append(parts, countedAction(changes, "edited 1 file", "edited %d files"))
	}
	reads := counts[toolRead] + counts[toolList]
	if reads > 0 {
		parts = append(parts, countedAction(reads, "read 1 file", "read %d files"))
	}
	if count := counts[toolSearch]; count > 0 {
		parts = append(parts, countedAction(count, "ran 1 search", "ran %d searches"))
	}
	if count := counts[toolCommand]; count > 0 {
		parts = append(parts, countedAction(count, "ran 1 command", "ran %d commands"))
	}
	if count := counts[toolGit]; count > 0 {
		parts = append(parts, countedAction(count, "inspected Git", "ran %d Git inspections"))
	}
	if count := counts[toolWeb]; count > 0 {
		parts = append(parts, countedAction(count, "fetched 1 page", "fetched %d pages"))
	}
	if count := counts[toolAgent]; count > 0 {
		parts = append(parts, countedAction(count, "used 1 subagent", "used %d subagents"))
	}
	if count := counts[toolOther]; count > 0 {
		parts = append(parts, countedAction(count, "ran 1 tool", "ran %d tools"))
	}
	if len(parts) == 0 {
		return "Tool activity"
	}
	parts[0] = strings.ToUpper(parts[0][:1]) + parts[0][1:]
	return strings.Join(parts, ", ")
}

func countedAction(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return fmt.Sprintf(plural, count)
}

func toolBatchStatus(entries []transcriptEntry, indices []int) string {
	status := "completed"
	for _, index := range indices {
		switch strings.ToLower(entries[index].toolStatus) {
		case "failed", "denied":
			return entries[index].toolStatus
		case "waiting approval":
			status = "waiting approval"
		case "running", "queued":
			if status != "waiting approval" {
				status = entries[index].toolStatus
			}
		case "canceled", "timed out":
			if status == "completed" {
				status = entries[index].toolStatus
			}
		}
	}
	return status
}

// RenderToolEntry formats a tool transcript row with clean visual hierarchy and click zones.
func (m Model) RenderToolEntry(index int, entry transcriptEntry, live bool) string {
	return m.renderToolEntry(index, entry, live, false)
}

func (m Model) renderToolEntry(index int, entry transcriptEntry, live, nested bool) string {
	badge := m.ToolStatusBadge(entry.toolStatus, live)
	icon := m.toolIcon(entry.tool)
	labelStyle := m.styles.Text
	if strings.EqualFold(entry.toolStatus, "completed") || strings.EqualFold(entry.toolStatus, "approved") {
		labelStyle = m.styles.Muted
	}
	runningCommand := toolFamilyFor(entry.tool) == toolCommand && (live || strings.EqualFold(entry.toolStatus, "running"))
	hasDetails := entry.artifactID != "" || entry.details != "" || runningCommand
	reserved := ansi.StringWidth(icon) + ansi.StringWidth(badge) + 5
	if hasDetails {
		reserved += 3
	}
	if nested {
		reserved += 2
	}
	label := ansi.Truncate(entry.content, max(8, m.contentWidth()-reserved), "…")
	header := icon + " " + labelStyle.Render(label) + "  " + badge
	if hasDetails {
		disclosure := m.glyph("▸", ">")
		if entry.expanded {
			disclosure = m.glyph("▾", "v")
		}
		if entry.artifactID != "" {
			header += "  " + zone.Mark(fmt.Sprintf("artifact-%d", index), m.styles.Muted.Render(disclosure))
		} else {
			header += "  " + m.styles.Muted.Render(disclosure)
		}
	}
	header = zone.Mark(fmt.Sprintf("tool-%d", index), header)
	rawDetails := entry.details
	exitCode := ""
	if toolFamilyFor(entry.tool) == toolCommand {
		rawDetails, exitCode = splitExitStatus(rawDetails)
	}
	rawDetails = m.wrapToolDetails(rawDetails)
	details, start, end, total := visibleToolDetails(rawDetails, entry.detailOffset)
	if total > maxVisibleToolLines {
		details += fmt.Sprintf("\n%s lines %d–%d of %d", m.glyph("…", "..."), start+1, end, total)
	}
	if entry.expanded && (details != "" || exitCode != "" || runningCommand) {
		details = zone.Mark(fmt.Sprintf("tool-details-%d", index), m.terminalToolDetails(entry, details, exitCode))
	} else if !entry.expanded {
		details = ""
	}
	if rawDetails == "" && exitCode == "" && !runningCommand {
		details = ""
	}
	if nested && entry.expanded && details != "" {
		return header + "\n" + details
	}
	return components.ToolView(header, "", details, entry.expanded, "", m.glyph)
}

func (m Model) terminalToolDetails(entry transcriptEntry, output, exitCode string) string {
	title := toolFamilyTitle(toolFamilyFor(entry.tool))
	lines := []string{m.styles.Muted.Render(title)}
	if invocation := strings.TrimSpace(toolInvocation(entry.tool, entry.arguments)); invocation != "" {
		invocationStyle := m.styles.Tool
		switch toolFamilyFor(entry.tool) {
		case toolCommand:
			invocationStyle = m.styles.ToolCommand
		case toolRead, toolList, toolWeb:
			invocationStyle = m.styles.ToolRead
		case toolWrite, toolCreate, toolRename:
			invocationStyle = m.styles.ToolWrite
		case toolDelete:
			invocationStyle = m.styles.ToolFailure
		case toolSearch:
			invocationStyle = m.styles.ToolSearch
		case toolGit:
			invocationStyle = m.styles.ToolGit
		case toolAgent:
			invocationStyle = m.styles.ToolAgent
		}
		lines = append(lines, "", invocationStyle.Render(invocation))
	}
	if strings.TrimSpace(output) != "" {
		lines = append(lines, "", output)
	}
	footer := m.ToolStatusBadge(entry.toolStatus, false)
	switch strings.ToLower(entry.toolStatus) {
	case "completed", "approved":
		footer = m.styles.ToolSuccess.Render(m.glyph("✓", "OK") + " Success")
	case "failed", "denied":
		footer = m.styles.ToolFailure.Render(m.glyph("×", "X") + " Failed")
	case "canceled", "timed out":
		footer = m.styles.Warning.Render(m.glyph("×", "X") + " " + entry.toolStatus)
	}
	if exitCode != "" {
		footer += m.styles.Muted.Render(" · exit " + exitCode)
	}
	lines = append(lines, "", footer)
	outerWidth := max(20, min(96, m.contentWidth()-6))
	return m.styles.ToolDrawer.Width(max(1, outerWidth-m.styles.ToolDrawer.GetHorizontalFrameSize())).Render(strings.Join(lines, "\n"))
}

func (m Model) wrapToolDetails(details string) string {
	if strings.TrimSpace(details) == "" {
		return ""
	}
	return ansi.Hardwrap(details, max(12, min(88, m.contentWidth()-12)), true)
}

func toolFamilyTitle(family toolFamily) string {
	switch family {
	case toolCommand:
		return "Shell"
	case toolRead:
		return "Read"
	case toolWrite, toolCreate, toolDelete, toolRename:
		return "Change"
	case toolSearch:
		return "Search"
	case toolList:
		return "Directory"
	case toolGit:
		return "Git"
	case toolWeb:
		return "Web"
	case toolAgent:
		return "Subagent"
	default:
		return "Tool result"
	}
}

func splitExitStatus(output string) (string, string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return "", ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(last, "exit ") {
		code := strings.TrimSpace(strings.TrimPrefix(last, "exit "))
		if _, err := strconv.Atoi(code); err == nil {
			return strings.TrimSpace(strings.Join(lines[:len(lines)-1], "\n")), code
		}
	}
	return strings.TrimSpace(output), ""
}

func newLocalShellEntry(command string) transcriptEntry {
	arguments, _ := json.Marshal(map[string]string{"command": command})
	return transcriptEntry{
		kind: entryTool, content: formatToolSummary("local_shell", "running", string(arguments)), tool: "local_shell", toolStatus: "running",
		arguments: string(arguments), dirty: true, expanded: true,
	}
}

func toolInvocation(tool, arguments string) string {
	if tool == "execute_command" || tool == "local_shell" {
		command := toolArgument(arguments, "command")
		if args := toolArguments(arguments, "args"); len(args) > 0 {
			command = strings.TrimSpace(command + " " + strings.Join(args, " "))
		}
		if command != "" {
			return "$ " + command
		}
	}
	label := strings.ReplaceAll(tool, "_", " ")
	start := strings.Index(arguments, "{")
	if start < 0 {
		return label
	}
	var values map[string]any
	if json.Unmarshal([]byte(arguments[start:]), &values) != nil {
		return label
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		switch key {
		case "content", "replacement", "prompt", "message", "environment", "api_key", "key":
			continue
		}
		switch value.(type) {
		case string, bool, float64:
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, fmt.Sprintf("%s=%v", key, values[key]))
	}
	if len(fields) > 0 {
		label += "  " + strings.Join(fields, "  ")
	}
	return label
}

// RenderDiffEntry formats a diff row with clickable zone and clean layout.
func (m Model) RenderDiffEntry(index int, entry transcriptEntry) string {
	row := m.styles.Info.Render("changed " + diffSummary(entry.content))
	return zone.Mark(fmt.Sprintf("diff-%d", index), row)
}
