package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
)

type sessionItem struct{ session api.Session }

func (i sessionItem) Title() string {
	if i.session.Name != "" {
		return i.session.Name
	}
	return i.session.ID
}
func (i sessionItem) Description() string { return i.session.ID }
func (i sessionItem) FilterValue() string { return i.session.Name + " " + i.session.ID }

type checkpointItem struct{ checkpoint api.Checkpoint }

func (i checkpointItem) Title() string {
	return truncate(i.checkpoint.Action, 64)
}
func (i checkpointItem) Description() string {
	label := fmt.Sprintf("%s · %d files", i.checkpoint.CreatedAt.Local().Format("Jan 02 15:04"), i.checkpoint.Files)
	if i.checkpoint.Partial {
		label += " · partial"
	}
	return label
}
func (i checkpointItem) FilterValue() string { return i.checkpoint.Action + " " + i.checkpoint.ID }

type sessionsLoadedMsg struct {
	sessions []api.Session
	err      error
}

type conversationLoadedMsg struct {
	session  api.Session
	messages []api.Message
	err      error
}

type checkpointsLoadedMsg struct {
	checkpoints []api.Checkpoint
	err         error
}

type sessionDeletedMsg struct {
	deletedID string
	session   *api.Session
	err       error
}

type rewindResultMsg struct {
	result api.RewindResult
	err    error
}

type sideAnswerMsg struct {
	answer string
	err    error
}

type kryptonResultMsg struct{ err error }

func (m *Model) openSessionsOverlay(deleting bool) tea.Cmd {
	m.paletteOpen = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	m.surface = surfaceSessions
	if deleting {
		m.surface = surfaceDeleteSession
	}
	m.overlayTarget, m.overlayCheckpoint = nil, nil
	m.overlayConfirm, m.overlayForce, m.overlayError = false, false, ""
	m.overlayList.Title = map[bool]string{true: "Delete chat session", false: "Switch chat session"}[deleting]
	m.layout()
	return loadSessionsCmd(m.ctx, m.client)
}

func (m *Model) openRewindOverlay() tea.Cmd {
	m.paletteOpen = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	m.surface = surfaceRewind
	m.overlayTarget, m.overlayCheckpoint = nil, nil
	m.overlayConfirm, m.overlayForce, m.overlayError = false, false, ""
	m.overlayList.Title = "Rewind checkpoint"
	m.layout()
	return loadCheckpointsCmd(m.ctx, m.client, m.session.ID)
}

func (m *Model) openSideOverlay(query string) tea.Cmd {
	m.paletteOpen = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.surface = surfaceSideQuestion
	m.sideAnswer, m.sideLoading, m.overlayError = "", false, ""
	m.input.Blur()
	m.overlayInput.Reset()
	m.overlayInput.Placeholder = "Ask about this chat without taking action"
	m.layout()
	if query != "" {
		m.overlayInput.SetValue(query)
		m.sideLoading = true
		return tea.Batch(sideAnswerCmd(m.ctx, m.client, m.session.ID, query), m.spinner.Tick)
	}
	return m.overlayInput.Focus()
}

func (m *Model) openKryptonOverlay() tea.Cmd {
	m.paletteOpen = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.surface = surfaceKrypton
	m.overlayError = ""
	m.input.Blur()
	m.overlayInput.Reset()
	m.overlayInput.Placeholder = "Type KRYPTON to confirm"
	m.layout()
	return m.overlayInput.Focus()
}

func (m *Model) closeOverlay() tea.Cmd {
	m.surface = surfaceNone
	m.overlayTarget, m.overlayCheckpoint = nil, nil
	m.overlayConfirm, m.overlayForce, m.overlayError = false, false, ""
	m.sideLoading = false
	m.overlayInput.Reset()
	m.overlayInput.Blur()
	m.layout()
	return m.restoreFocus()
}

func loadSessionsCmd(ctx context.Context, client api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return sessionsLoadedMsg{err: errors.New("backend is unavailable")}
		}
		sessions, err := client.ListSessions(ctx)
		return sessionsLoadedMsg{sessions: sessions, err: err}
	}
}

func loadConversationCmd(ctx context.Context, client api.Client, session api.Session) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return conversationLoadedMsg{session: session, err: errors.New("backend is unavailable")}
		}
		snapshot, err := client.GetSession(ctx, session.ID)
		return conversationLoadedMsg{session: snapshot.Session, messages: snapshot.Messages, err: err}
	}
}

func loadCheckpointsCmd(ctx context.Context, client api.Client, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return checkpointsLoadedMsg{err: errors.New("backend is unavailable")}
		}
		checkpoints, err := client.ListCheckpoints(ctx, api.SessionRequest{SessionID: sessionID})
		return checkpointsLoadedMsg{checkpoints: checkpoints, err: err}
	}
}

func deleteSessionCmd(ctx context.Context, client api.Client, target, current api.Session) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return sessionDeletedMsg{err: errors.New("backend is unavailable")}
		}
		if err := client.DeleteSession(ctx, target.ID); err != nil {
			return sessionDeletedMsg{deletedID: target.ID, err: err}
		}
		if target.ID != current.ID {
			return sessionDeletedMsg{deletedID: target.ID}
		}
		next, err := client.CreateSession(ctx, api.CreateSessionRequest{})
		return sessionDeletedMsg{deletedID: target.ID, session: &next, err: err}
	}
}

func rewindCmd(ctx context.Context, client api.Client, sessionID, checkpointID string, force bool) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return rewindResultMsg{err: errors.New("backend is unavailable")}
		}
		result, err := client.RewindSession(ctx, api.RewindRequest{SessionID: sessionID, Checkpoint: checkpointID, Force: force})
		return rewindResultMsg{result: result, err: err}
	}
}

func sideAnswerCmd(ctx context.Context, client api.Client, sessionID, question string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return sideAnswerMsg{err: errors.New("backend is unavailable")}
		}
		answer, err := client.AnswerSideQuestion(ctx, api.SideQuestionRequest{SessionID: sessionID, Question: question})
		return sideAnswerMsg{answer: answer.Answer, err: err}
	}
}

func kryptonCmd(ctx context.Context, purge func(context.Context) error, _ string) tea.Cmd {
	return func() tea.Msg {
		if purge != nil {
			return kryptonResultMsg{err: purge(ctx)}
		}
		return kryptonResultMsg{err: errors.New("workspace purge is unavailable in this frontend")}
	}
}

type durableToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	BatchID   string          `json:"-"`
}

func durableToolCalls(messages []api.Message) map[string]durableToolCall {
	calls := make(map[string]durableToolCall)
	for _, message := range messages {
		batchID := message.ID
		if batchID == "" {
			batchID = fmt.Sprintf("message:%d", message.Sequence)
		}
		for _, part := range message.Parts {
			if part.Kind != "assistant_tool_call" {
				continue
			}
			var call durableToolCall
			if json.Unmarshal(part.Metadata, &call) == nil && call.ID != "" {
				call.BatchID = batchID
				calls[call.ID] = call
			}
		}
	}
	return calls
}

func transcriptFromMessages(messages []api.Message) []transcriptEntry {
	calls := durableToolCalls(messages)
	entries := make([]transcriptEntry, 0, len(messages))
	for _, message := range messages {
		var text strings.Builder
		for _, part := range message.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
		}
		content := safeText(text.String())
		if content == "" && message.Role != "tool" {
			continue
		}
		switch message.Role {
		case "user":
			entries = append(entries, transcriptEntry{kind: entryUser, content: content})
		case "assistant":
			entries = append(entries, transcriptEntry{kind: entryAssistant, content: content})
		case "tool":
			tool, callID, artifactID := toolMessageIdentity(message)
			summary := formatToolSummary(tool, "completed", "")
			arguments := ""
			if call, ok := calls[callID]; ok {
				arguments = string(call.Arguments)
				if summary = formatToolSummary(call.Name, "completed", arguments); summary == "" {
					summary = formatToolSummary(tool, "completed", arguments)
				}
			}
			details := toolResultDetails(tool, content)
			if call, ok := calls[callID]; ok && call.Name != "" {
				tool = call.Name
				details = toolResultDetails(tool, content)
			}
			entry := transcriptEntry{
				kind:       entryTool,
				content:    summary,
				tool:       tool,
				toolStatus: "completed",
				details:    details,
				arguments:  arguments,
				toolCallID: callID,
				artifactID: artifactID,
			}
			if call, ok := calls[callID]; ok {
				entry.toolBatchID = call.BatchID
			}
			entries = append(entries, entry)
		case "system":
			entries = append(entries, transcriptEntry{kind: entryStatus, content: content})
		}
	}
	return entries
}

func activityFromMessages(messages []api.Message) []activityEvent {
	calls := durableToolCalls(messages)
	items := make([]activityEvent, 0)
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		tool, callID, _ := toolMessageIdentity(message)
		var output strings.Builder
		for _, part := range message.Parts {
			if part.Text != "" {
				output.WriteString(part.Text)
			}
		}
		arguments := ""
		if call, ok := calls[callID]; ok {
			arguments = string(call.Arguments)
			if call.Name != "" {
				tool = call.Name
			}
		}
		items = append(items, activityEvent{Time: message.CreatedAt, TaskID: callID, Tool: tool, Status: "completed", Arguments: arguments, Output: output.String()})
	}
	return items
}

func toolMessageIdentity(message api.Message) (tool, callID, artifactID string) {
	tool = "tool"
	for _, part := range message.Parts {
		if part.Kind != "tool_result" {
			continue
		}
		artifactID = part.ArtifactID
		var metadata struct {
			ToolName   string `json:"tool_name"`
			ToolCallID string `json:"tool_call_id"`
		}
		if json.Unmarshal(part.Metadata, &metadata) == nil {
			if metadata.ToolName != "" {
				tool = metadata.ToolName
			}
			callID = metadata.ToolCallID
		}
	}
	return tool, callID, artifactID
}

func (m Model) updateOverlayKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || msg.Code == tea.KeyEsc {
		return m, m.closeOverlay()
	}
	switch m.surface {
	case surfaceSessions:
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			item, ok := m.overlayList.SelectedItem().(sessionItem)
			if !ok {
				return m, nil
			}
			m.surface = surfaceNone
			m.setStatus("Switching to " + item.session.Name + "…")
			m.layout()
			return m, loadConversationCmd(m.ctx, m.client, item.session)
		}
	case surfaceDeleteSession:
		if m.overlayConfirm {
			if msg.String() == "enter" || msg.Code == tea.KeyEnter || msg.String() == "y" {
				if m.overlayTarget == nil {
					return m, nil
				}
				return m, deleteSessionCmd(m.ctx, m.client, *m.overlayTarget, m.session)
			}
			if msg.String() == "n" {
				m.overlayConfirm, m.overlayTarget = false, nil
				return m, nil
			}
			return m, nil
		}
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			item, ok := m.overlayList.SelectedItem().(sessionItem)
			if ok {
				selected := item.session
				m.overlayTarget, m.overlayConfirm = &selected, true
			}
			return m, nil
		}
	case surfaceRewind:
		if m.overlayConfirm {
			if m.overlayForce {
				if msg.String() == "enter" || msg.Code == tea.KeyEnter {
					if strings.TrimSpace(m.overlayInput.Value()) != "FORCE" {
						m.overlayError = "Type FORCE exactly to overwrite post-checkpoint changes."
						return m, nil
					}
					m.overlayError = ""
					return m, rewindCmd(m.ctx, m.client, m.session.ID, m.overlayCheckpoint.ID, true)
				}
				var cmd tea.Cmd
				m.overlayInput, cmd = m.overlayInput.Update(msg)
				return m, cmd
			}
			if msg.String() == "enter" || msg.Code == tea.KeyEnter || msg.String() == "y" {
				if m.overlayCheckpoint == nil {
					return m, nil
				}
				return m, rewindCmd(m.ctx, m.client, m.session.ID, m.overlayCheckpoint.ID, false)
			}
			if msg.String() == "n" {
				m.overlayConfirm, m.overlayCheckpoint = false, nil
			}
			return m, nil
		}
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			item, ok := m.overlayList.SelectedItem().(checkpointItem)
			if ok {
				selected := item.checkpoint
				m.overlayCheckpoint, m.overlayConfirm = &selected, true
			}
			return m, nil
		}
	case surfaceSideQuestion:
		if m.sideLoading {
			return m, nil
		}
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			question := strings.TrimSpace(m.overlayInput.Value())
			if question == "" {
				m.overlayError = "Ask a question before sending it."
				return m, nil
			}
			m.sideLoading, m.overlayError = true, ""
			return m, sideAnswerCmd(m.ctx, m.client, m.session.ID, question)
		}
		var cmd tea.Cmd
		m.overlayInput, cmd = m.overlayInput.Update(msg)
		return m, cmd
	case surfaceKrypton:
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			if strings.TrimSpace(m.overlayInput.Value()) != "KRYPTON" {
				m.overlayError = "Type KRYPTON exactly to confirm workspace-state removal."
				return m, nil
			}
			m.overlayError = ""
			return m, kryptonCmd(m.ctx, m.purge, m.workspace)
		}
		var cmd tea.Cmd
		m.overlayInput, cmd = m.overlayInput.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.overlayList, cmd = m.overlayList.Update(msg)
	return m, cmd
}

func (m Model) overlayView() string {
	width := max(24, min(m.width-8, 76))
	switch m.surface {
	case surfaceSessions:
		content := m.overlayList.View()
		if m.overlayError != "" {
			content += "\n" + m.styles.Warning.Render(m.overlayError)
		}
		return components.Card(m.styles.Overlay, width, "", content)
	case surfaceDeleteSession:
		if m.overlayConfirm && m.overlayTarget != nil {
			message := "Delete " + m.overlayTarget.Name + " and its private history?"
			if m.overlayTarget.ID == m.session.ID {
				message += " A new empty session will be created."
			}
			return components.Card(m.styles.Modal, width, m.styles.Error.Render("DELETE SESSION"), message)
		}
		content := m.overlayList.View()
		if m.overlayError != "" {
			content += "\n" + m.styles.Warning.Render(m.overlayError)
		}
		return components.Card(m.styles.Modal, width, "", content)
	case surfaceRewind:
		if m.overlayConfirm && m.overlayCheckpoint != nil {
			if m.overlayForce {
				body := "Files changed after this checkpoint.\n\n" + m.overlayInput.View()
				if m.overlayError != "" {
					body += "\n" + m.styles.Error.Render(m.overlayError)
				}
				return components.Card(m.styles.Modal, width, m.styles.Error.Render("REWIND CONFLICT"), body)
			}
			body := truncate(m.overlayCheckpoint.Action, 72) + fmt.Sprintf("\n%d covered files", m.overlayCheckpoint.Files)
			return components.Card(m.styles.Modal, width, m.styles.Warning.Render("REWIND CHECKPOINT"), body)
		}
		content := m.overlayList.View()
		if m.overlayError != "" {
			content += "\n" + m.styles.Warning.Render(m.overlayError)
		}
		return components.Card(m.styles.Modal, width, "", content)
	case surfaceSideQuestion:
		content := []string{m.styles.Muted.Render("Answers use this chat only. No tools or workspace changes."), m.overlayInput.View()}
		if m.sideLoading {
			content = append(content, m.spinner.View()+" answering…")
		}
		if m.sideAnswer != "" {
			content = append(content, m.styles.Accent.Render("Answer"), m.styles.Text.Render(m.sideAnswer))
		}
		if m.overlayError != "" {
			content = append(content, m.styles.Error.Render(m.overlayError))
		}
		return components.Card(m.styles.Overlay, width, m.styles.Title.Render("SIDE QUESTION"), strings.Join(content, "\n"))
	case surfaceKrypton:
		body := "This permanently removes .session, .sessions, .scratchpad, and .supremo state/objects from this workspace.\nGlobal credentials are kept.\n\n" + m.overlayInput.View()
		if m.overlayError != "" {
			body += "\n" + m.styles.Error.Render(m.overlayError)
		}
		return components.Card(m.styles.Modal, width, m.styles.Error.Render("KRYPTON"), body)
	default:
		return ""
	}
}
