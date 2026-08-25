package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/commands"
	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
	"github.com/AbhaySingh002/supremo/internal/ui/terminal"
)

type streamFlushMsg struct{}
type commandResultMsg struct {
	id             int
	input          string
	session        api.Session
	intent         commands.Intent
	snapshot       *api.SessionSnapshot
	initialize     *api.InitializeResult
	followupPrompt string
	switchSession  bool
	output         string
	err            error
}
type workspaceStatusMsg struct {
	info workspaceState
	err  error
}
type markdownRenderedMsg struct {
	run      int
	rendered map[int]string
}
type approvalModeChangedMsg struct {
	session api.Session
	err     error
}
type terminationDeadlineMsg struct{}

func (m Model) cancelOrQuit(terminate bool) (tea.Model, tea.Cmd) {
	m.flushStreaming()
	var cmds []tea.Cmd
	if m.active != nil {
		if m.active.kind == taskAgent && m.active.runID != "" {
			cmds = append(cmds, cancelRunCmd(m.ctx, m.client, m.session.ID, m.active.runID))
		} else if m.active.cancel != nil {
			m.active.cancel()
		}
		m.cancelling = true
		m.quitWhenIdle = m.quitWhenIdle || terminate
		status := "Cancellation requested."
		if terminate {
			status = "Termination requested; stopping task…"
		}
		m.appendEntry(entryStatus, status)
		if terminate {
			cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return terminationDeadlineMsg{} }))
		}
		return m, tea.Batch(cmds...)
	}
	if m.shutdown != nil {
		m.shutdown()
	}
	return m, tea.Quit
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case InterruptMsg:
		return m.cancelOrQuit(msg.Terminate)
	case streamFlushMsg:
		m.flushStreaming()
		return m, nil
	case terminationDeadlineMsg:
		if !m.quitWhenIdle || m.active == nil {
			return m, nil
		}
		if m.shutdown != nil {
			m.shutdown()
		}
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.activityToggled {
			m.showActivity = m.width >= 120
		}
		if m.width >= 120 && m.focus == focusActivity {
			m.focus = focusComposer
			m.surface = surfaceNone
			m.input.Focus()
		}
		m.selection = nil
		m.invalidateRenderCache()
		m.layout()
		return m, tea.Batch(m.renderMarkdown(), m.renderActiveDiff())
	case clientReadyMsg:
		if msg.epoch != m.sessionEpoch {
			if msg.subscription != nil {
				msg.subscription.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.appendEntry(entryError, "Backend connection failed: "+msg.err.Error())
			return m, m.input.Focus()
		}
		if m.subscription != nil {
			m.subscription.Close()
		}
		m.subscription = msg.subscription
		m.applyInitialize(msg.initialize)
		m.applySnapshot(msg.snapshot)
		m.workspaceInfo = workspaceState{branch: msg.workspace.Branch, changed: msg.workspace.Changed, ready: msg.workspace.Ready, err: msg.workspace.Error}
		m.inputTokens, m.outputTokens, m.contextLimit = msg.usage.InputTokens, msg.usage.OutputTokens, msg.usage.ContextLimit
		return m, tea.Batch(m.renderMarkdown(), waitClientEventCmd(m.subscription, m.sessionEpoch), m.input.Focus())
	case clientEventMsg:
		if msg.epoch != m.sessionEpoch {
			return m, nil
		}
		if !msg.open {
			if msg.err != nil {
				var apiErr *api.Error
				if errors.As(msg.err, &apiErr) && apiErr.Code == api.CodeResyncRequired {
					if m.subscription != nil {
						m.subscription.Close()
						m.subscription = nil
					}
					m.sessionEpoch++
					return m, initializeClientCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
				}
				m.appendEntry(entryError, "Event stream stopped: "+msg.err.Error())
			}
			return m, nil
		}
		if msg.event.Cursor > 0 && msg.event.Cursor <= m.cursor {
			return m, waitClientEventCmd(m.subscription, m.sessionEpoch)
		}
		if msg.event.SessionID != "" && msg.event.SessionID != m.session.ID {
			return m, waitClientEventCmd(m.subscription, m.sessionEpoch)
		}
		if m.active != nil && m.active.runID != "" && msg.event.RunID != "" && msg.event.RunID != m.active.runID && runScopedEvent(msg.event.Type) {
			return m, waitClientEventCmd(m.subscription, m.sessionEpoch)
		}
		if msg.event.Cursor > 0 {
			m.cursor = msg.event.Cursor
		}
		return m, tea.Batch(m.applyAPIEvent(msg.event), waitClientEventCmd(m.subscription, m.sessionEpoch))
	case promptAcceptedMsg:
		if m.active == nil || m.active.id != msg.id {
			return m, nil
		}
		if msg.err != nil {
			m.active = nil
			m.appendEntry(entryError, m.recoveryError(msg.err))
			m.input.SetValue(msg.display)
			m.input.CursorEnd()
			m.resizeComposer()
			return m, m.input.Focus()
		}
		m.active.runID = msg.receipt.RunID
		m.pendingInput = ""
		m.resetComposer()
		m.collapseCompletedToolBatches()
		m.appendEntry(entryUser, msg.display)
		m.setStatus("Queued for execution")
		return m, m.spinner.Tick
	case cancelRunResultMsg:
		if msg.err != nil {
			m.cancelling = false
			m.appendEntry(entryError, "Cancellation failed: "+msg.err.Error())
		}
		return m, nil
	case interactionResultMsg:
		if msg.interactionID != m.pendingInteraction {
			return m, nil
		}
		if msg.err != nil {
			if m.approval != nil {
				m.approval.SetDeciding(false)
				m.approval.SetError(msg.err.Error())
			}
			return m, nil
		}
		return m, nil
	case snapshotRefreshMsg:
		if msg.epoch != m.sessionEpoch {
			return m, nil
		}
		if msg.err != nil {
			m.appendEntry(entryError, "Refresh failed: "+msg.err.Error())
			return m, nil
		}
		m.applySnapshot(msg.snapshot)
		return m, tea.Batch(m.renderMarkdown(), m.restoreComposerAfterWork())
	case spinner.TickMsg:
		credentialBusy := m.credential != nil && m.credential.loading
		if m.active == nil && (m.approval == nil || !m.approval.IsDeciding()) && !m.sideLoading && !m.catalogBusy && !credentialBusy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.refreshLiveFeed()
		return m, cmd
	case tea.MouseMsg:
		mouse := msg.Mouse()
		if m.diffOpen() {
			var cmd tea.Cmd
			m.diffViewport, cmd = m.diffViewport.Update(msg)
			return m, cmd
		}
		if _, isWheel := msg.(tea.MouseWheelMsg); isWheel {
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -1
			case tea.MouseWheelDown:
				delta = 1
			}
			if delta != 0 {
				if zoneInBounds("composer-input", mouse.X, mouse.Y) && m.scrollComposer(delta) {
					return m, nil
				}
				for i := range m.entries {
					if zoneInBounds(fmt.Sprintf("tool-details-%d", i), mouse.X, mouse.Y) && m.scrollToolDetails(i, delta) {
						return m, nil
					}
				}
			}
		}
		isClick := false
		if _, ok := msg.(tea.MouseClickMsg); ok {
			isClick = true
		} else if _, ok := msg.(tea.MouseReleaseMsg); ok {
			isClick = true
		}
		if isClick && (mouse.Button == tea.MouseLeft || mouse.Button == tea.MouseNone) {
			// 1. Approval modal mouse actions
			if m.approval != nil {
				if zoneInBounds("approval-allow", mouse.X, mouse.Y) {
					return m.updateApprovalKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
				}
				if zoneInBounds("approval-deny", mouse.X, mouse.Y) {
					return m.updateApprovalKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
				}
				if zoneInBounds("approval-edit", mouse.X, mouse.Y) {
					return m.updateApprovalKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
				}
				if zoneInBounds("approval-auto", mouse.X, mouse.Y) {
					return m.updateApprovalKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
				}
				return m, nil
			}

			// 2. Plan Question option selection clicks
			if m.planQuestion != nil {
				req := m.planQuestion.Request()
				if m.planQuestion.Question() < len(req.Questions) {
					for i := range req.Questions[m.planQuestion.Question()].Options {
						if zoneInBounds(fmt.Sprintf("plan-option-%d", i), mouse.X, mouse.Y) {
							m.planQuestion.SetOption(i)
							return m.updatePlanQuestionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
						}
					}
				}
				return m, nil
			}

			// 3. Send button click
			if zoneInBounds("send-button", mouse.X, mouse.Y) {
				if strings.TrimSpace(m.input.Value()) != "" {
					return m.submitInput()
				}
				return m, nil
			}

			// 4. Unread updates pill click -> jump to latest
			if zoneInBounds("unread-pill", mouse.X, mouse.Y) {
				m.feed.GotoBottom()
				m.followTail = true
				m.newOutput = 0
				return m, nil
			}

			// 5. Tool & Diff row clicks in feed
			for i := range m.entries {
				if m.entries[i].kind == entryTool {
					batchID := m.entries[i].toolBatchID
					if batchID != "" && (zoneInBounds(fmt.Sprintf("tool-batch-%d", i), mouse.X, mouse.Y) || m.zoneInRow(fmt.Sprintf("tool-batch-%d", i), mouse.X, mouse.Y)) {
						if m.collapsedToolBatches == nil {
							m.collapsedToolBatches = make(map[string]bool)
						}
						m.collapsedToolBatches[batchID] = !m.collapsedToolBatches[batchID]
						m.invalidateFeedPrefix()
						m.rebuildFeed()
						return m, nil
					}
					if m.entries[i].artifactID != "" && zoneInBounds(fmt.Sprintf("artifact-%d", i), mouse.X, mouse.Y) {
						if m.entries[i].expanded {
							m.entries[i].expanded = false
							m.invalidateFeedPrefix()
							m.rebuildFeed()
							return m, nil
						}
						return m, loadArtifactCmd(m.ctx, m.client, m.session.ID, i, m.entries[i].artifactID)
					}
					if zoneInBounds(fmt.Sprintf("tool-%d", i), mouse.X, mouse.Y) || m.zoneInRow(fmt.Sprintf("tool-%d", i), mouse.X, mouse.Y) {
						m.entries[i].expanded = !m.entries[i].expanded
						m.entries[i].detailOffset = 0
						m.invalidateFeedPrefix()
						m.rebuildFeed()
						return m, nil
					}
				}
				if m.entries[i].kind == entryDiff && (zoneInBounds(fmt.Sprintf("diff-%d", i), mouse.X, mouse.Y) || m.zoneInRow(fmt.Sprintf("diff-%d", i), mouse.X, mouse.Y)) {
					return m, m.openDiffInspector(i)
				}
			}

			// 6. Activity rail tool clicks -> toggle corresponding tool entry
			for i := range m.activity {
				if zoneInBounds(fmt.Sprintf("activity-tool-%d", i), mouse.X, mouse.Y) {
					for j := len(m.entries) - 1; j >= 0; j-- {
						if m.entries[j].kind == entryTool && m.entries[j].tool == m.activity[i].Tool {
							m.entries[j].expanded = !m.entries[j].expanded
							m.entries[j].detailOffset = 0
							m.invalidateFeedPrefix()
							m.rebuildFeed()
							return m, nil
						}
					}
				}
			}
		}
		if m.surface == surfacePlanQuestion && m.planQuestion != nil {
			var cmd tea.Cmd
			m.planQuestion, cmd = m.planQuestion.Update(msg)
			return m, cmd
		}
		if m.surface == surfaceApproval && m.approval != nil {
			var cmd tea.Cmd
			m.approval, cmd = m.approval.Update(msg)
			return m, cmd
		}

		if handled, cmd := m.updateMouseSelection(msg); handled {
			return m, cmd
		}
		if handled, cmd := m.positionComposerCursor(msg); handled {
			return m, cmd
		}
		if m.surface != surfaceNone || m.paletteOpen {
			return m, nil
		}
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		m.syncFollowTail()
		return m, cmd
	case terminal.ShellResultMsg:
		if m.active == nil || m.active.id != msg.ID {
			return m, nil
		}
		m.clearLiveStatus()
		m.active = nil
		m.approval = nil
		if msg.Err != nil {
			if index := m.runningToolIndex(progressEvent{Tool: "local_shell"}); index >= 0 {
				m.entries[index].toolStatus = "failed"
				m.entries[index].details = msg.Err.Error()
				m.entries[index].expanded = true
				m.entries[index].dirty = true
				m.liveEntry = -1
				m.noteOutput()
				m.rebuildFeed()
			} else {
				m.appendEntry(entryError, "Local shell failed: "+msg.Err.Error())
			}
			return m, m.restoreComposerAfterWork()
		}
		details := string(msg.Output.Stdout)
		if len(msg.Output.Stderr) > 0 {
			if details != "" {
				details += "\n"
			}
			details += string(msg.Output.Stderr)
		}
		status := "completed"
		if msg.Output.Canceled {
			status = "canceled"
		} else if msg.Output.TimedOut {
			status = "timed out"
		} else if msg.Output.ExitCode != 0 {
			status = "failed"
		}
		if details != "" {
			details += "\n"
		}
		details += fmt.Sprintf("exit %d", msg.Output.ExitCode)
		if index := m.runningToolIndex(progressEvent{Tool: "local_shell"}); index >= 0 {
			m.entries[index].toolStatus = status
			m.entries[index].details = shellToolDetails(details)
			m.entries[index].expanded = true
			m.entries[index].dirty = true
			if m.liveEntry == index {
				m.liveEntry = -1
			}
		} else {
			m.entries = append(m.entries, transcriptEntry{kind: entryTool, content: "Local shell", tool: "local_shell", toolStatus: status, details: shellToolDetails(details), expanded: true})
		}
		m.noteOutput()
		m.rebuildFeed()
		return m, m.restoreComposerAfterWork()
	case sessionsLoadedMsg:
		if msg.err != nil {
			m.surface = surfaceNone
			m.appendEntry(entryError, "load sessions: "+msg.err.Error())
			m.layout()
			return m, m.input.Focus()
		}
		items := make([]list.Item, 0, len(msg.sessions))
		for _, session := range msg.sessions {
			items = append(items, sessionItem{session: session})
		}
		if len(items) == 0 {
			m.overlayError = "No saved chat sessions."
		}
		return m, m.overlayList.SetItems(items)
	case conversationLoadedMsg:
		m.session = msg.session
		m.entries = transcriptFromMessages(msg.messages)
		m.collapsedToolBatches = make(map[string]bool)
		m.todos = todosFromMessages(msg.messages)
		if len(m.todos) > 0 {
			m.entries = append(m.entries, transcriptEntry{kind: entryTodos, content: components.Todos(m.todos), dirty: true})
		}
		m.streamingEntry, m.liveEntry = -1, -1
		m.followTail, m.newOutput = true, 0
		m.refreshProvider()
		m.rebuildFeed()
		if msg.err != nil {
			m.appendEntry(entryError, "load chat history: "+msg.err.Error())
		} else {
			m.setStatus("Switched to " + msg.session.Name + ".")
		}
		return m, m.renderMarkdown()
	case checkpointsLoadedMsg:
		if msg.err != nil {
			m.surface = surfaceNone
			m.appendEntry(entryError, "load checkpoints: "+msg.err.Error())
			m.layout()
			return m, m.input.Focus()
		}
		items := make([]list.Item, 0, len(msg.checkpoints))
		for _, checkpoint := range msg.checkpoints {
			items = append(items, checkpointItem{checkpoint: checkpoint})
		}
		if len(items) == 0 {
			m.overlayError = "No checkpoints are available for this chat."
		}
		return m, m.overlayList.SetItems(items)
	case sessionDeletedMsg:
		if msg.err != nil {
			m.overlayError = msg.err.Error()
			return m, nil
		}
		m.surface = surfaceNone
		if msg.session != nil {
			m.session = *msg.session
			m.entries = nil
			m.appendEntry(entryStatus, "Deleted the active session and created a new chat.")
		} else {
			m.appendEntry(entryStatus, "Deleted chat session "+msg.deletedID+".")
		}
		return m, m.closeOverlay()
	case rewindResultMsg:
		if msg.err != nil {
			var conflict *api.Error
			if errors.As(msg.err, &conflict) && conflict.Code == api.CodeConflict {
				m.overlayForce = true
				m.overlayError = "Workspace paths changed after this checkpoint."
				m.input.Reset()
				m.input.Placeholder = "Type FORCE to overwrite post-checkpoint changes"
				return m, m.input.Focus()
			}
			m.overlayError = msg.err.Error()
			return m, nil
		}
		m.surface = surfaceNone
		m.appendEntry(entryStatus, fmt.Sprintf("Rewound %d files%s.", msg.result.Restored, map[bool]string{true: " (partial)", false: ""}[msg.result.Partial]))
		return m, m.closeOverlay()
	case sideAnswerMsg:
		m.sideLoading = false
		if msg.err != nil {
			m.overlayError = msg.err.Error()
			return m, nil
		}
		m.sideAnswer, m.overlayError = msg.answer, ""
		m.input.Reset()
		m.input.Placeholder = "Ask another side question"
		return m, m.input.Focus()
	case kryptonResultMsg:
		if msg.err != nil {
			m.overlayError = msg.err.Error()
			return m, nil
		}
		if m.shutdown != nil {
			m.shutdown()
		}
		return m, tea.Quit
	case workspaceDiffLoadedMsg:
		if msg.err != nil {
			m.appendEntry(entryError, "Failed to load workspace diff: "+msg.err.Error())
			return m, nil
		}
		if strings.TrimSpace(msg.diff) == "" {
			m.appendEntry(entryStatus, "No uncommitted git changes in this workspace.")
			return m, nil
		}
		return m, m.openWorkspaceDiffInspector(msg.diff, msg.summary)

	case terminal.ClipboardPasteMsg:
		if msg.Err != nil {
			m.appendEntry(entryError, "Paste failed: "+msg.Err.Error())
		}
		return m, tea.Batch(m.insertPastedText(msg.Text), m.updatePalette(), m.updateMentionMenu())
	case plan.PlanDecisionCompletedMsg:
		answers, _ := json.Marshal(answersFromMap(msg.Answers))
		interactionID := m.pendingInteraction
		m.planQuestion = nil
		m.surface = surfaceNone
		m.layout()
		return m, respondInteractionCmd(m.ctx, m.client, api.RespondInteractionRequest{SessionID: m.session.ID, InteractionID: interactionID, Answers: answers})
	case plan.PlanDecisionCancelledMsg:
		m.planQuestion = nil
		m.surface = surfaceNone
		m.layout()
		if m.active != nil && m.active.runID != "" {
			return m, cancelRunCmd(m.ctx, m.client, m.session.ID, m.active.runID)
		}
		return m, nil
	case commandResultMsg:
		m.flushStreaming()
		if msg.id != 0 && (m.active == nil || m.active.id != msg.id) {
			return m, nil
		}
		if msg.id != 0 {
			m.active = nil
			m.liveEntry = -1
		}
		if msg.initialize != nil {
			m.applyInitialize(*msg.initialize)
		}
		if msg.session.ID != "" {
			m.session = msg.session
		}
		if msg.snapshot != nil {
			m.applySnapshot(*msg.snapshot)
		}
		if msg.err != nil {
			m.appendEntry(entryError, m.recoveryError(msg.err))
		} else if msg.output != "" {
			m.setStatus(conciseCommandOutput(msg.input, msg.output))
		}
		if msg.switchSession && msg.err == nil {
			if m.subscription != nil {
				m.subscription.Close()
				m.subscription = nil
			}
			m.sessionEpoch++
			return m, initializeClientCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
		}
		if msg.err == nil && msg.intent.Kind == commands.Models && len(msg.intent.Args) == 1 {
			m.openModelSelector()
		}
		if msg.followupPrompt != "" && msg.err == nil {
			return m, m.startTask(msg.followupPrompt)
		}
		return m, tea.Batch(workspaceStatusAPICmd(m.ctx, m.client), m.restoreComposerAfterWork())
	case approvalModeChangedMsg:
		if msg.err != nil {
			m.appendEntry(entryError, "set approval mode: "+msg.err.Error())
			return m, nil
		}
		m.session = msg.session
		m.appendEntry(entryStatus, approvalModeStatus(msg.session.ApprovalMode))
		return m, nil
	case workspaceStatusMsg:
		if msg.err != nil {
			m.workspaceInfo = workspaceState{err: msg.err.Error()}
		} else {
			m.workspaceInfo = msg.info
		}
		return m, nil
	case markdownRenderedMsg:
		if msg.run != m.markdownRun {
			return m, nil
		}
		for index, rendered := range msg.rendered {
			if index >= 0 && index < len(m.entries) {
				m.entries[index].rendered = rendered
				m.entries[index].renderedCache = ""
				m.entries[index].dirty = true
			}
		}
		m.historyPrefix = ""
		m.historyPrefixCount = 0
		m.rebuildFeed()
		return m, nil
	case diffRenderedMsg:
		if msg.run != m.diffRun || msg.index < 0 || msg.index >= len(m.entries) || m.entries[msg.index].kind != entryDiff {
			return m, nil
		}
		m.entries[msg.index].rendered, m.entries[msg.index].renderedWidth = msg.rendered, msg.width
		if m.diffOpen() && m.diffEntry == msg.index {
			m.diffViewport.SetContent(msg.rendered)
		}
		return m, nil
	case artifactLoadedMsg:
		if msg.sessionID != m.session.ID {
			return m, nil
		}
		if msg.index < 0 || msg.index >= len(m.entries) || m.entries[msg.index].kind != entryTool {
			return m, nil
		}
		if m.collapsedToolBatches == nil {
			m.collapsedToolBatches = make(map[string]bool)
		}
		if msg.err != nil {
			m.entries[msg.index].details = "Could not load retained evidence: " + msg.err.Error()
			m.entries[msg.index].expanded = true
			if batchID := m.entries[msg.index].toolBatchID; batchID != "" {
				m.collapsedToolBatches[batchID] = false
			}
			m.invalidateFeedPrefix()
			m.rebuildFeed()
			return m, nil
		}
		m.entries[msg.index].details = evidenceTextForTool(m.entries[msg.index].tool, msg.artifact)
		m.entries[msg.index].expanded = true
		m.entries[msg.index].detailOffset = 0
		if batchID := m.entries[msg.index].toolBatchID; batchID != "" {
			m.collapsedToolBatches[batchID] = false
		}
		m.invalidateFeedPrefix()
		m.rebuildFeed()
		return m, nil
	case modelCatalogMsg:
		m.catalogBusy = false
		if msg.err != nil {
			m.surface, m.modelSelector = surfaceNone, nil
			m.appendEntry(entryError, "Model refresh failed: "+msg.err.Error())
			m.layout()
			return m, m.restoreFocus()
		}
		m.modelCatalog = append([]api.Provider(nil), msg.catalog.Providers...)
		if !m.openModelSelector() {
			m.surface = surfaceNone
			m.appendEntry(entryError, "No text-generation models are available for the configured providers.")
			m.layout()
			return m, m.restoreFocus()
		}
		return m, nil
	case credentialSubmittedMsg:
		provider, key := msg.provider, msg.key
		request := api.ConfigureProviderRequest{Provider: &provider, APIKey: &key, Verify: true}
		if msg.endpoint != "" {
			endpoint := msg.endpoint
			request.Endpoint = &endpoint
		}
		return m, tea.Batch(configureProviderCmd(m.ctx, m.client, request, true), m.spinner.Tick)
	case credentialCancelledMsg:
		if m.credential != nil {
			m.credential.clear()
		}
		m.credential, m.surface = nil, surfaceNone
		m.layout()
		return m, m.restoreFocus()
	case providerConfiguredMsg:
		m.catalogBusy = false
		if msg.err != nil {
			if m.credential != nil && m.surface == surfaceCredential {
				m.credential.loading, m.credential.err = false, msg.err.Error()
				return m, m.credential.key.Focus()
			}
			m.surface, m.providerSelector, m.modelSelector = surfaceNone, nil, nil
			m.appendEntry(entryError, "Provider configuration failed: "+msg.err.Error())
			m.layout()
			return m, m.restoreFocus()
		}
		m.applyInitialize(msg.initialize)
		if m.credential != nil {
			m.credential.clear()
			m.credential = nil
		}
		if msg.catalog != nil {
			m.modelCatalog = append([]api.Provider(nil), msg.catalog.Providers...)
			if m.openModelSelector() {
				return m, nil
			}
		}
		m.surface, m.providerSelector, m.modelSelector = surfaceNone, nil, nil
		m.setStatus("Using " + m.provider + " · " + m.modelName)
		m.layout()
		return m, m.restoreFocus()
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	case tea.PasteMsg:
		return m, tea.Batch(m.insertPastedText(msg.Content), m.updatePalette(), m.updateMentionMenu())
	case selectors.ProviderSelectedMsg:
		provider, ok := m.providerChoice(msg.ID)
		if !ok {
			m.providerSelector, m.surface = nil, surfaceNone
			m.appendEntry(entryError, "Unknown provider "+msg.ID+".")
			m.layout()
			return m, m.restoreFocus()
		}
		if !provider.Configured {
			return m, m.openCredential(provider)
		}
		m.providerSelector = nil
		providerID := provider.ID
		m.surface, m.catalogBusy = surfaceProvider, true
		return m, tea.Batch(configureProviderCmd(m.ctx, m.client, api.ConfigureProviderRequest{Provider: &providerID}, false), m.spinner.Tick)
	case selectors.ProviderSelectorDismissedMsg:
		m.providerSelector, m.modelSelector = nil, nil
		m.surface = surfaceNone
		m.layout()
		return m, m.restoreFocus()
	case selectors.ModelSelectedMsg:
		m.modelSelector = nil
		m.surface, m.catalogBusy = surfaceModel, true
		providerID, modelID := msg.ProviderID, msg.ID
		return m, tea.Batch(configureProviderCmd(m.ctx, m.client, api.ConfigureProviderRequest{Provider: &providerID, Model: &modelID}, false), m.spinner.Tick)
	case selectors.CommandQueryMsg:
		if m.paletteOpen {
			updated, cmd := m.palette.Update(msg)
			m.palette = updated.(selectors.CommandMenu)
			return m, cmd
		}
		return m, nil
	}

	if m.paletteOpen {
		updated, cmd := m.palette.Update(msg)
		m.palette = updated.(selectors.CommandMenu)
		return m, cmd
	}

	if m.transcriptFocused() {
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
	cmdPalette := m.updatePalette()
	cmdMention := m.updateMentionMenu()
	return m, tea.Batch(cmd, cmdPalette, cmdMention)
}

func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.selection != nil {
		if msg.String() == "esc" || msg.Code == tea.KeyEsc {
			m.selection = nil
			return m, nil
		}
		if m.selection.active() && selectionCopyKey(msg) {
			m.selection.copied = true
			return m, copySelectionCmd(m.selectedText())
		}
		if m.selection.active() && m.selection.input {
			switch {
			case selectionDeleteKey(msg, m.input):
				m.deleteInputSelection()
				return m, tea.Batch(m.updatePalette(), m.updateMentionMenu())
			case msg.Text != "" || isNewlineKey(msg, m.keys, m.input) || key.Matches(msg, m.input.KeyMap.Paste):
				m.deleteInputSelection()
			default:
				m.selection = nil
			}
		} else {
			m.selection = nil
		}
	}
	if msg.String() == "ctrl+c" {
		return m.cancelOrQuit(false)
	}
	if m.surface == surfaceProvider && m.providerSelector != nil {
		updated, cmd := m.providerSelector.Update(msg)
		selector := updated.(selectors.ProviderSelector)
		m.providerSelector = &selector
		return m, cmd
	}
	if m.surface == surfaceModel && m.modelSelector != nil {
		updated, cmd := m.modelSelector.Update(msg)
		selector := updated.(selectors.ProviderSelector)
		m.modelSelector = &selector
		return m, cmd
	}
	if m.surface == surfaceCredential && m.credential != nil {
		return m, m.credential.Update(msg)
	}
	if m.surface >= surfaceSessions && m.surface <= surfaceKrypton {
		return m.updateOverlayKey(msg)
	}
	if m.surface == surfacePlanQuestion {
		return m.updatePlanQuestionKey(msg)
	}
	if m.surface == surfaceApproval {
		return m.updateApprovalKey(msg)
	}
	if m.surface == surfaceDiff {
		return m.updateDiffInspector(msg)
	}
	if m.surface == surfaceHelp {
		if msg.String() == "esc" || msg.Code == tea.KeyEsc || msg.String() == "?" {
			m.surface = surfaceNone
			m.layout()
			return m, m.restoreFocus()
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Composer.Plans) {
		newMode := !m.session.PlanModeActive()
		return m, setPlanModeCmd(m.ctx, m.client, m.session, newMode)
	}
	if msg.String() == "ctrl+b" {
		m.activityToggled = true
		if m.width < 120 {
			if m.surface == surfaceActivity {
				m.showActivity = false
				m.surface = surfaceNone
				m.focus = focusComposer
				m.layout()
				return m, m.input.Focus()
			}
			m.showActivity = true
			m.surface = surfaceActivity
			m.priorFocus, m.focus = m.focus, focusActivity
			m.input.Blur()
		} else {
			m.showActivity = !m.showActivity
		}
		m.layout()
		return m, nil
	}
	if m.focus == focusActivity {
		if msg.String() == "esc" || msg.Code == tea.KeyEsc {
			m.showActivity = false
			m.surface = surfaceNone
			m.focus = m.priorFocus
			if m.focus != focusTranscript {
				m.focus = focusComposer
				m.layout()
				return m, m.input.Focus()
			}
			m.layout()
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Composer.ToggleMode) {
		return m.cycleApprovalMode()
	}
	if m.planDraft && (msg.String() == "esc" || msg.Code == tea.KeyEsc) && !m.paletteOpen && !m.mentionOpen {
		m.setPlanDraft(false)
		return m, m.input.Focus()
	}
	if (key.Matches(msg, m.keys.Composer.Help) || msg.String() == "?") && strings.TrimSpace(m.input.Value()) == "" {
		m.priorFocus, m.focus = m.focus, focusOverlay
		m.surface = surfaceHelp
		m.input.Blur()
		m.layout()
		return m, nil
	}
	if key.Matches(msg, m.keys.Composer.ToggleDebug) {
		m.showDebug = !m.showDebug
		m.rebuildFeed()
		return m, nil
	}
	if key.Matches(msg, m.keys.Composer.Clear) || msg.String() == "ctrl+l" {
		m.entries = nil
		m.collapsedToolBatches = make(map[string]bool)
		m.feed.SetContent("")
		m.newOutput = 0
		m.followTail = true
		return m, nil
	}
	if msg.String() == "shift+tab" || (msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift)) {
		m.focus = focusTranscript
		m.input.Blur()
		return m, nil
	}
	if key.Matches(msg, m.keys.Feed.FocusInput) {
		m.focus = focusComposer
		return m, m.input.Focus()
	}
	if m.transcriptFocused() {
		if msg.String() == "esc" || msg.Code == tea.KeyEsc {
			m.focus = focusComposer
			return m, m.input.Focus()
		}
		if msg.String() == "space" || msg.Code == tea.KeySpace {
			if m.toggleLatestToolBatch() {
				return m, nil
			}
		}
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			if index := m.latestDetailIndex(); index >= 0 && m.entries[index].kind == entryDiff {
				return m, m.openDiffInspector(index)
			}
			if m.toggleLatestTool() {
				return m, nil
			}
		}
		if key.Matches(msg, m.keys.Feed.Evidence) {
			if index := m.latestArtifactIndex(); index >= 0 {
				return m, loadArtifactCmd(m.ctx, m.client, m.session.ID, index, m.entries[index].artifactID)
			}
			return m, nil
		}
		if (msg.String() == "up" || msg.Code == tea.KeyUp) && m.scrollLatestExpandedTool(-1) || (msg.String() == "down" || msg.Code == tea.KeyDown) && m.scrollLatestExpandedTool(1) {
			return m, nil
		}
		switch msg.String() {
		case "up", "pgup", "home":
			m.followTail = false
		case "end":
			m.feed.GotoBottom()
			m.syncFollowTail()
			return m, nil
		}
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		m.syncFollowTail()
		return m, cmd
	}
	if m.mentionOpen {
		switch msg.String() {
		case "up", "down":
			var cmd tea.Cmd
			m.mentionMenu, cmd = m.mentionMenu.Update(msg)
			return m, cmd
		case "tab", "enter":
			if m.selectMention() {
				return m, m.input.Focus()
			}
			m.closeMentionMenu()
			if msg.String() == "enter" || msg.Code == tea.KeyEnter {
				return m.submitInput()
			}
		case "esc":
			m.closeMentionMenu()
			return m, nil
		case "space":
			m.closeMentionMenu()
			m.input.InsertString(" ")
			m.input, _ = m.input.Update(nil)
			m.resizeComposer()
			return m, nil
		}
	}
	if (msg.String() == "tab" || msg.Code == tea.KeyTab) && !m.paletteOpen {
		m.appendEntry(entryStatus, "Start durable planning with /plan <objective>.")
		return m, nil
	}
	if key.Matches(msg, m.input.KeyMap.Paste) {
		return m, clipboardPasteCmd()
	}
	if transcriptNavigation(msg, m.input.Value()) {
		return m.scrollTranscript(msg)
	}
	if m.paletteOpen && (msg.String() == "up" || msg.String() == "down" || msg.Code == tea.KeyUp || msg.Code == tea.KeyDown) {
		updated, cmd := m.palette.Update(msg)
		m.palette = updated.(selectors.CommandMenu)
		return m, cmd
	}
	if msg.String() == "ctrl+r" {
		return m.reverseSearchHistory()
	}
	if handled, cmd := m.navigateInputHistory(msg); handled {
		return m, cmd
	}
	if (msg.String() == "up" || msg.Code == tea.KeyUp) && m.moveComposerVisualCursor(-1) || (msg.String() == "down" || msg.Code == tea.KeyDown) && m.moveComposerVisualCursor(1) {
		return m, nil
	}
	if m.paletteOpen && key.Matches(msg, m.keys.Composer.Complete) {
		return m.completeSelectedCommand()
	}
	if m.paletteOpen && key.Matches(msg, m.keys.Composer.Submit) && !m.exactCommandInput() {
		return m.completeSelectedCommand()
	}
	if isNewlineKey(msg, m.keys, m.input) {
		m.input.InsertString("\n")
		m.resizeComposer()
		return m, tea.Batch(m.updatePalette(), m.updateMentionMenu())
	}
	if key.Matches(msg, m.keys.Composer.Submit) {
		if m.checkBackslashNewline() {
			return m, tea.Batch(m.updatePalette(), m.updateMentionMenu())
		}
		return m.submitInput()
	}
	if (msg.String() == "esc" || msg.Code == tea.KeyEsc) && m.paletteOpen {
		m.paletteOpen = false
		m.layout()
		return m, nil
	}
	value := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
	if m.input.Value() != value {
		m.historyIndex = len(m.inputHistory)
		m.historyDraft = ""
		m.historyQuery = ""
	}
	cmdPalette := m.updatePalette()
	cmdMention := m.updateMentionMenu()
	return m, tea.Batch(cmd, cmdPalette, cmdMention)
}

func (m *Model) checkBackslashNewline() bool {
	val := m.input.Value()
	if strings.HasSuffix(val, "\\") {
		m.input.SetValue(strings.TrimSuffix(val, "\\") + "\n")
		m.input.CursorEnd()
		m.resizeComposer()
		return true
	}
	cursor := composerCursorOffset(m.input)
	runes := []rune(val)
	if cursor > 0 && cursor <= len(runes) && runes[cursor-1] == '\\' {
		newRunes := make([]rune, 0, len(runes))
		newRunes = append(newRunes, runes[:cursor-1]...)
		newRunes = append(newRunes, '\n')
		newRunes = append(newRunes, runes[cursor:]...)
		m.input.SetValue(string(newRunes))
		setComposerCursorOffset(&m.input, cursor)
		m.resizeComposer()
		return true
	}
	return false
}

func isNewlineKey(msg tea.KeyPressMsg, keys KeyMap, input textarea.Model) bool {
	if key.Matches(msg, keys.Composer.Newline, input.KeyMap.InsertNewline) {
		return true
	}
	s := strings.ToLower(msg.String())
	switch s {
	case "shift+enter", "alt+enter", "ctrl+enter", "cmd+enter", "super+enter", "meta+enter",
		"opt+enter", "option+enter", "esc+enter", "esc+return",
		"shift+return", "alt+return", "ctrl+return", "cmd+return", "super+return", "meta+return",
		"opt+return", "option+return", "ctrl+j", "ctrl+o", "\n", "\r\n":
		return true
	}
	if (msg.Code == tea.KeyEnter || msg.Code == 13 || msg.Code == 10) &&
		(msg.Mod.Contains(tea.ModShift) || msg.Mod.Contains(tea.ModAlt) || msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModSuper)) {
		return true
	}
	if msg.Code == '\n' || msg.Code == 10 || msg.Text == "\n" {
		return true
	}
	return false
}

func selectionCopyKey(msg tea.KeyPressMsg) bool {
	return msg.String() == "ctrl+c" || (msg.Code == 'c' && (msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModSuper)))
}

func selectionDeleteKey(msg tea.KeyPressMsg, input textarea.Model) bool {
	return msg.String() == "backspace" || msg.String() == "delete" || msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete || key.Matches(msg, input.KeyMap.DeleteCharacterBackward, input.KeyMap.DeleteCharacterForward)
}

func (m Model) scrollTranscript(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.Feed.Top) || keyMsg.String() == "home":
			m.followTail = false
			m.feed.GotoTop()
			return m, nil
		case key.Matches(keyMsg, m.keys.Feed.Bottom) || keyMsg.String() == "end":
			m.followTail = true
			m.newOutput = 0
			m.feed.GotoBottom()
			return m, nil
		case key.Matches(keyMsg, m.keys.Feed.PgUp, m.keys.Feed.PgDown) || keyMsg.String() == "pgup" || keyMsg.String() == "pgdown":
			m.followTail = false
		}
	}
	var cmd tea.Cmd
	m.feed, cmd = m.feed.Update(msg)
	m.syncFollowTail()
	return m, cmd
}

func transcriptNavigation(msg tea.KeyPressMsg, input string) bool {
	switch msg.String() {
	case "pgup", "pgdown":
		return true
	case "home", "end":
		return strings.TrimSpace(input) == ""
	default:
		return false
	}
}

func (m *Model) navigateInputHistory(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.filterInputHistory()
	isUp := msg.String() == "up" || msg.Code == tea.KeyUp
	isDown := msg.String() == "down" || msg.Code == tea.KeyDown
	if len(m.inputHistory) == 0 || (!isUp && !isDown) {
		return false, nil
	}
	navigating := m.historyIndex < len(m.inputHistory)
	if isUp {
		info := m.input.LineInfo()
		if !navigating && (m.input.Line() != 0 || info.RowOffset != 0) {
			return false, nil
		}
		if m.historyIndex == len(m.inputHistory) {
			m.historyDraft = m.input.Value()
		}
		if m.historyIndex == 0 {
			return true, nil
		}
		m.historyIndex--
	} else if isDown {
		if !navigating {
			return false, nil
		}
		if m.historyIndex >= len(m.inputHistory)-1 {
			m.historyIndex = len(m.inputHistory)
			m.input.SetValue(m.historyDraft)
			m.input.CursorEnd()
			m.input, _ = m.input.Update(nil)
			m.resizeComposer()
			return true, tea.Batch(m.updatePalette(), m.updateMentionMenu())
		}
		m.historyIndex++
	}
	value := m.historyDraft
	if m.historyIndex < len(m.inputHistory) {
		value = m.inputHistory[m.historyIndex]
	}
	m.input.SetValue(value)
	m.paletteOpen = false
	m.historyQuery = ""
	m.resizeComposer()
	return true, m.input.Focus()
}

func (m Model) reverseSearchHistory() (tea.Model, tea.Cmd) {
	m.filterInputHistory()
	if len(m.inputHistory) == 0 {
		return m, nil
	}
	if m.historyQuery == "" {
		m.historyQuery = m.input.Value()
		m.historyIndex = len(m.inputHistory)
	}
	query := strings.ToLower(m.historyQuery)
	for index := m.historyIndex - 1; index >= 0; index-- {
		if !strings.Contains(strings.ToLower(m.inputHistory[index]), query) {
			continue
		}
		m.historyIndex = index
		m.input.SetValue(m.inputHistory[index])
		m.paletteOpen = false
		m.resizeComposer()
		return m, tea.Batch(m.input.Focus(), m.updateMentionMenu())
	}
	m.appendEntry(entryStatus, "No earlier matching request in history.")
	return m, nil
}

func (m Model) completeSelectedCommand() (tea.Model, tea.Cmd) {
	if command, ok := m.palette.Selected(); ok {
		m.input.SetValue(command.Name + " ")
		m.historyIndex = len(m.inputHistory)
		m.historyDraft = ""
		m.resizeComposer()
		return m, tea.Batch(m.input.Focus(), m.updatePalette(), m.updateMentionMenu())
	}
	return m, nil
}

func (m Model) exactCommandInput() bool {
	input := strings.TrimSpace(m.input.Value())
	for _, command := range m.registry.List() {
		if input == command.Name {
			return true
		}
	}
	return false
}

func (m Model) updatePlanQuestionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.planQuestion == nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.planQuestion, cmd = m.planQuestion.Update(msg)
	return m, cmd
}

func (m Model) updateApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.approval == nil || m.approval.IsDeciding() {
		return m, nil
	}
	var cmd tea.Cmd
	m.approval, cmd = m.approval.Update(msg)
	if cmd != nil {
		actionMsg := cmd()
		if act, ok := actionMsg.(approval.ApprovalActionMsg); ok {
			m.approval.SetDeciding(true)
			request := api.RespondInteractionRequest{SessionID: m.session.ID, InteractionID: m.pendingInteraction}
			switch act.Action {
			case "approve":
				request.Decision = "approve"
				return m, respondInteractionCmd(m.ctx, m.client, request)
			case "edit":
				revised := json.RawMessage(act.Arguments)
				var object map[string]any
				if err := json.Unmarshal(revised, &object); err != nil {
					m.approval.SetDeciding(false)
					m.approval.SetError("Arguments must be a JSON object: " + err.Error())
					return m, nil
				}
				request.Decision, request.RevisedInput = "edit", revised
				return m, respondInteractionCmd(m.ctx, m.client, request)
			case "deny":
				request.Decision, request.Reason = "deny", "denied in Supremo TUI"
				return m, respondInteractionCmd(m.ctx, m.client, request)
			case "auto":
				request.Decision = "approve"
				mode := "superman"
				return m, tea.Batch(
					respondInteractionCmd(m.ctx, m.client, request),
					func() tea.Msg {
						updated, err := m.client.UpdateSession(m.ctx, api.UpdateSessionRequest{SessionID: m.session.ID, ExpectedRevision: m.session.Revision, ApprovalMode: &mode})
						return approvalModeChangedMsg{session: updated, err: err}
					},
				)
			}
		}
		return m, cmd
	}
	return m, nil
}

func approvalJSON(arguments string) string {
	if start := strings.Index(arguments, "{"); start >= 0 {
		return arguments[start:]
	}
	return "{}"
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return m, nil
	}
	if commandIs(input, "/auth") {
		if m.active != nil {
			m.appendEntry(entryStatus, "Wait for the active task to finish before changing credentials.")
			return m, nil
		}
		provider, ok := m.providerChoice(m.provider)
		if !ok {
			m.appendEntry(entryError, "Select a provider with /provider before adding a credential.")
			return m, nil
		}
		m.resetComposer()
		return m, m.openCredential(provider)
	}
	m.rememberInput(input)
	if commandIs(input, "/exit") {
		return m.cancelOrQuit(true)
	}
	if m.active != nil {
		if _, ok := approvalModeCommand(input); ok {
			m.resetComposer()
			return m, executeCommandCmd(m.ctx, m.client, m.registry, m.session, input, 0)
		}
		switch {
		case commandIs(input, "/cancel"):
			m.cancelling = true
			m.resetComposer()
			m.appendEntry(entryStatus, "Cancellation requested.")
			return m, cancelRunCmd(m.ctx, m.client, m.session.ID, m.active.runID)
		case commandIs(input, "/approve"):
			m.resetComposer()
			return m, respondInteractionCmd(m.ctx, m.client, api.RespondInteractionRequest{SessionID: m.session.ID, InteractionID: m.pendingInteraction, Decision: "approve"})
		case commandIs(input, "/deny"):
			m.resetComposer()
			return m, respondInteractionCmd(m.ctx, m.client, api.RespondInteractionRequest{SessionID: m.session.ID, InteractionID: m.pendingInteraction, Decision: "deny", Reason: strings.TrimSpace(strings.TrimPrefix(input, "/deny"))})
		case commandIs(input, "/help") || commandIs(input, "/activity"):
			m.resetComposer()
			m.appendEntry(entryCommand, displayCommand(input))
			return m, executeCommandCmd(m.ctx, m.client, m.registry, m.session, input, 0)
		case m.cancelling:
			if m.pendingInput != "" {
				m.appendEntry(entryStatus, "One message is already queued until cancellation is complete.")
				return m, nil
			}
			m.pendingInput = input
			m.resetComposer()
			m.appendEntry(entryStatus, "Message queued until cancellation is complete.")
			return m, nil
		default:
			m.appendEntry(entryStatus, "A task is running; use /approve, /deny, /cancel, /strict, /batman, /superman, or /exit.")
			return m, nil
		}
	}
	if input == "/plan" {
		m.setPlanDraft(!m.planDraft)
		return m, m.input.Focus()
	}
	if m.planDraft {
		if input == "/cancel" {
			m.setPlanDraft(false)
			return m, m.input.Focus()
		}
		if planObjectiveCommand(input) {
			m.planDraft = false
			return m, m.startCommand(input)
		}
		if !strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "!") {
			m.planDraft = false
			return m, m.startCommand("/plan " + input)
		}
	}
	if input == "/session" || input == "/session list" {
		m.resetComposer()
		return m, m.openSessionsOverlay(false)
	}
	if input == "/delete-session" {
		m.resetComposer()
		return m, m.openSessionsOverlay(true)
	}
	if input == "/rewind" {
		m.resetComposer()
		return m, m.openRewindOverlay()
	}
	if strings.HasPrefix(input, "/side") {
		query := strings.TrimSpace(strings.TrimPrefix(input, "/side"))
		return m, m.openSideOverlay(query)
	}
	if input == "/krypton" {
		return m, m.openKryptonOverlay()
	}
	if input == "/provider" || input == "/providers" {
		m.resetComposer()
		m.openProviderSelector()
		return m, nil
	}
	if commandIs(input, "/provider") {
		parts := strings.Fields(input)
		if len(parts) >= 2 {
			if provider, ok := m.providerChoice(parts[1]); ok && !provider.Configured {
				if len(parts) >= 3 {
					provider.Endpoint = parts[2]
				}
				m.resetComposer()
				return m, m.openCredential(provider)
			}
		}
	}
	if input == "/copy" {
		m.resetComposer()
		return m, m.copyLastAssistantResponse()
	}
	if input == "/diff" {
		m.resetComposer()
		return m, loadWorkspaceDiffCmd(m.ctx, m.client)
	}
	if input == "/mode" {
		m.resetComposer()
		return m.cycleApprovalMode()
	}
	if input == "/model" || input == "/models" || input == "/models refresh" {
		m.resetComposer()
		m.appendEntry(entryCommand, "/model")
		return m, m.refreshModelCatalog()
	}
	if strings.HasPrefix(input, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(input, "!"))
		if command == "" {
			m.appendEntry(entryError, "Provide a local shell command after !.")
			return m, nil
		}
		return m, m.startShell(command)
	}
	if strings.HasPrefix(input, "/") {
		return m, m.startCommand(input)
	}
	return m, m.startTask(input)
}

func planObjectiveCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) < 2 || parts[0] != "/plan" {
		return false
	}
	if len(parts) != 2 {
		return true
	}
	switch parts[1] {
	case "status", "show", "execute", "resume", "cancel":
		return false
	default:
		return true
	}
}

func (m *Model) rememberInput(input string) {
	input = strings.TrimSpace(input)
	if !requestHistoryInput(input) {
		return
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != input {
		m.inputHistory = append(m.inputHistory, input)
	}
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
}

func requestHistoryInput(input string) bool {
	input = strings.TrimSpace(input)
	return input != "" && !strings.HasPrefix(input, "/")
}

func (m *Model) filterInputHistory() {
	kept := m.inputHistory[:0]
	for _, input := range m.inputHistory {
		if requestHistoryInput(input) {
			kept = append(kept, input)
		}
	}
	m.inputHistory = kept
	m.historyIndex = min(m.historyIndex, len(m.inputHistory))
}

func (m *Model) updatePalette() tea.Cmd {
	value := strings.TrimSpace(strings.ToLower(m.input.Value()))
	if !strings.HasPrefix(value, "/") {
		if m.paletteOpen {
			m.paletteOpen = false
			m.layout()
		}
		return nil
	}
	cmd := m.palette.SetQuery(value)
	m.paletteOpen = len(m.palette.Items()) > 0
	m.layout()
	return cmd
}

func (m *Model) applyProgress(event progressEvent) tea.Cmd {
	if event.Kind != progressStream {
		m.flushStreaming()
	}
	switch event.Kind {
	case progressStream:
		return m.appendStreamingChunk(event.Message)
	case progressDebug:
		m.appendEntry(entryDebug, event.Message)
	case progressActivity:
		m.setStatus(event.Message)
	case progressError:
		m.appendEntry(entryError, event.Message)
	case progressSessionName:
		if event.Message != "" {
			m.session.Name = event.Message
		}
	case progressChecklist:
		m.setTodos(event.Todos)
	case progressCheckpoint:
		if event.Checkpoint != nil {
			m.appendEntry(entryStatus, "Checkpoint saved: "+event.Checkpoint.ID)
		}
		if event.Phase != "" {
			m.phase = event.Phase
		}
	case progressPhase:
		m.phase = event.Phase
		message := event.Message
		if message == "" {
			message = phaseLabel(event.Phase)
		}
		m.setStatus(message)
	case progressCompletion:
		m.phase = "completion"
		m.setStatus("Complete")
	case progressApproval:
		if event.ToolStatus == "waiting approval" {
			m.approval = approval.NewApprovalModel(event.Tool, truncate(event.Arguments, 4_000), rendering.NewStyles())
			m.planQuestion = nil
			m.surface = surfaceApproval
			m.priorFocus, m.focus = m.focus, focusOverlay
			m.input.Blur()
			return nil
		}
		m.recordToolEvent(event)
		if event.ToolStatus == "approved" || event.ToolStatus == "denied" {
			m.approval = nil
			m.surface = surfaceNone
			m.focus = focusComposer
			m.layout()
			return m.input.Focus()
		}
	case progressTool:
		m.recordToolEvent(event)
	case progressRetry:
		m.setStatus(event.Message)
	case progressIteration:
		return m.waitForProvider()
	}
	return nil
}

func (m Model) recoveryError(err error) string {
	message := strings.ToLower(err.Error())
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case api.CodeProviderAuth:
			return "The provider rejected the configured credential. Run /auth to replace it securely."
		case api.CodeProviderDown, api.CodeProviderRateLimit:
			return "The provider is temporarily unavailable. Retry or switch providers. Details: " + truncate(apiErr.Message, 260)
		case api.CodeContextLimit:
			return "The request exceeded the model context limit after compaction. Details: " + truncate(apiErr.Message, 260)
		case api.CodeCancelled:
			return "Run cancelled."
		}
	}
	if strings.Contains(message, "invalid response") {
		return "Supremo rejected malformed model output after one safe format-repair retry; no new tool ran. Try again or switch models with /provider. Details: " + truncate(err.Error(), 360)
	}
	return err.Error()
}

func (m *Model) recordToolEvent(event progressEvent) {
	m.invalidateFeedPrefix()
	toolEvent := activityEvent{Time: time.Now().UTC(), SessionID: event.SessionID, TaskID: event.CallID, Tool: event.Tool, Status: event.ToolStatus, Message: event.Message, Arguments: event.Arguments, Output: event.ToolOutput, Diff: event.Diff}
	updated := false
	if event.CallID != "" {
		for index := len(m.activity) - 1; index >= 0; index-- {
			if m.activity[index].TaskID != event.CallID {
				continue
			}
			previous := m.activity[index]
			if toolEvent.Tool == "" {
				toolEvent.Tool = previous.Tool
			}
			if toolEvent.Arguments == "" {
				toolEvent.Arguments = previous.Arguments
			}
			m.activity[index] = toolEvent
			updated = true
			break
		}
	}
	if !updated {
		m.activity = append(m.activity, toolEvent)
	}
	if len(m.activity) > 32 {
		m.activity = m.activity[len(m.activity)-32:]
	}
	label := m.conciseToolLabel(event.Tool, event.ToolStatus, event.Arguments)
	if event.Tool == "todo_write" {
		if items := components.ParseTodos(event.ToolOutput); len(items) > 0 {
			m.setTodos(items)
		} else if items := components.ParseTodos(event.Arguments); len(items) > 0 {
			m.setTodos(items)
		}
	}
	switch event.ToolStatus {
	case "waiting approval":
		entry := newToolEntry("Approval required — "+label, event)
		entry.details = approvalToolDetails(event)
		m.entries = append(m.entries, entry)
		m.noteOutput()
		m.rebuildFeed()
	case "approved":
		if index := m.pendingApprovalIndex(event); index >= 0 {
			entry := &m.entries[index]
			entry.toolStatus = event.ToolStatus
			entry.content = "Approved — " + label
			m.noteOutput()
			m.rebuildFeed()
			return
		}
		m.entries = append(m.entries, newToolEntry("Approved — "+label, event))
		m.noteOutput()
		m.rebuildFeed()
	case "running":
		m.clearLiveStatus()
		m.entries = append(m.entries, newToolEntry(label, event))
		m.noteOutput()
		if m.active != nil {
			m.liveEntry = len(m.entries) - 1
		}
		m.rebuildFeed()
	case "completed", "failed", "denied":
		if event.ToolStatus == "denied" {
			if index := m.pendingApprovalIndex(event); index >= 0 {
				entry := &m.entries[index]
				entry.toolStatus = event.ToolStatus
				entry.content = "Denied — " + label
				m.noteOutput()
				m.rebuildFeed()
				return
			}
		}
		if index := m.runningToolIndex(event); index >= 0 {
			entry := &m.entries[index]
			if event.Tool != "" {
				entry.tool = event.Tool
			}
			entry.toolStatus = event.ToolStatus
			if event.Arguments != "" {
				entry.arguments = event.Arguments
			}
			entry.content = formatToolSummary(entry.tool, entry.toolStatus, entry.arguments)
			details := toolDetails(event)
			if details != "" || entry.details == "" {
				entry.details = details
			}
			m.noteOutput()
			if m.liveEntry == index {
				m.liveEntry = -1
			}
			m.appendDiff(event.Diff)
			m.rebuildFeed()
			return
		}
		m.entries = append(m.entries, newToolEntry(label, event))
		m.noteOutput()
		m.appendDiff(event.Diff)
		m.rebuildFeed()
	case "dry run":
		m.entries = append(m.entries, newToolEntry("Dry run — "+label, event))
		m.noteOutput()
		m.rebuildFeed()
	}
}

func (m Model) runningToolIndex(event progressEvent) int {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		identityMatches := event.CallID != "" && entry.toolCallID == event.CallID
		if entry.kind == entryTool && (identityMatches || (entry.toolCallID == "" && entry.tool == event.Tool)) && entry.toolStatus == "running" {
			return index
		}
	}
	return -1
}

func (m Model) pendingApprovalIndex(event progressEvent) int {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		identityMatches := event.CallID != "" && entry.toolCallID == event.CallID
		if entry.kind == entryTool && (identityMatches || (entry.toolCallID == "" && entry.tool == event.Tool)) && entry.toolStatus == "waiting approval" {
			return index
		}
	}
	return -1
}

func appendToolDetails(previous, next string) string {
	previous, next = strings.TrimSpace(previous), strings.TrimSpace(next)
	if previous == "" {
		return next
	}
	if next == "" {
		return previous
	}
	return previous + "\n" + next
}

func (m *Model) appendDiff(diff string) {
	diff = truncate(safeText(strings.TrimSpace(diff)), maxVisibleDiffBytes)
	if diff == "" {
		return
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryDiff, content: diff})
	m.noteOutput()
}

func toolDetails(event progressEvent) string {
	if output := strings.TrimSpace(event.ToolOutput); output != "" {
		if summary := strings.TrimSpace(toolResultDetails(event.Tool, output)); summary != "" {
			return truncate(safeText(summary), 12_000)
		}
	}
	if message := strings.TrimSpace(event.Message); message != "" {
		return truncate(safeText(message), 12_000)
	}
	return ""
}

func zoneInBounds(id string, x, y int) bool {
	z := zone.Get(id)
	if z == nil || z.IsZero() {
		return false
	}
	return x >= z.StartX && x <= z.EndX && y >= z.StartY && y <= z.EndY
}

func (m Model) zoneInRow(id string, x, y int) bool {
	z := zone.Get(id)
	if z == nil || z.IsZero() {
		return false
	}
	feedWidth := m.contentWidth()
	if railWidth := m.activityRailWidth(); railWidth > 0 {
		feedWidth -= railWidth + 1
	}
	return y >= z.StartY && y <= z.EndY && x >= 0 && x <= max(z.EndX, feedWidth)
}

func approvalToolDetails(event progressEvent) string {
	title := approval.FormatPrompt(event.Tool, event.Arguments)
	if toolFamilyFor(event.Tool) == toolCommand {
		return title + "\n" + toolInvocation(event.Tool, event.Arguments)
	}
	summary := formatToolSummary(event.Tool, event.ToolStatus, event.Arguments)
	if summary != "" && summary != event.Tool && summary != strings.ReplaceAll(event.Tool, "_", " ") {
		return title + "\n" + summary
	}
	details := strings.TrimSpace(event.Arguments)
	if details == "" {
		return title
	}
	return title + "\n" + approval.FormatArguments(event.Tool, details)
}

func newToolEntry(label string, event progressEvent) transcriptEntry {
	return transcriptEntry{kind: entryTool, content: label, tool: event.Tool, toolStatus: event.ToolStatus, details: toolDetails(event), arguments: event.Arguments, toolCallID: event.CallID, toolBatchID: event.toolBatchID()}
}

func (event progressEvent) toolBatchID() string {
	if event.Turn <= 0 && event.Step <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", event.Turn, event.Step)
}

func shellToolDetails(output string) string {
	if output == "" {
		return ""
	}
	return truncate(safeText(components.Pretty(output)), 12_000)
}

func (m *Model) toggleLatestTool() bool {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := &m.entries[index]
		if entry.kind == entryTool && (entry.details != "" || toolFamilyFor(entry.tool) == toolCommand && entry.toolStatus == "running") {
			if indices := m.toolBatchIndices(entry.toolBatchID); len(indices) > 1 && m.collapsedToolBatches[entry.toolBatchID] {
				m.collapsedToolBatches[entry.toolBatchID] = false
				m.invalidateFeedPrefix()
				m.rebuildFeed()
				return true
			}
			entry.expanded = !entry.expanded
			entry.detailOffset = 0
			m.invalidateFeedPrefix()
			m.rebuildFeed()
			return true
		}
	}
	return false
}

func (m *Model) toggleLatestToolBatch() bool {
	if m.collapsedToolBatches == nil {
		m.collapsedToolBatches = make(map[string]bool)
	}
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		if entry.kind != entryTool || entry.toolBatchID == "" || len(m.toolBatchIndices(entry.toolBatchID)) < 2 {
			continue
		}
		m.collapsedToolBatches[entry.toolBatchID] = !m.collapsedToolBatches[entry.toolBatchID]
		m.invalidateFeedPrefix()
		m.rebuildFeed()
		return true
	}
	return false
}

func (m *Model) scrollLatestExpandedTool(delta int) bool {
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.scrollToolDetails(index, delta) {
			return true
		}
	}
	return false
}

func (m *Model) scrollToolDetails(index, delta int) bool {
	if index < 0 || index >= len(m.entries) {
		return false
	}
	entry := &m.entries[index]
	if entry.kind != entryTool || !entry.expanded || entry.details == "" {
		return false
	}
	details := entry.details
	if toolFamilyFor(entry.tool) == toolCommand {
		details, _ = splitExitStatus(details)
	}
	maximum := max(0, len(strings.Split(m.wrapToolDetails(details), "\n"))-maxVisibleToolLines)
	next := min(max(0, entry.detailOffset+delta), maximum)
	if next == entry.detailOffset {
		return false
	}
	entry.detailOffset = next
	m.followTail = false
	m.invalidateFeedPrefix()
	m.rebuildFeed()
	return true
}

func (m *Model) updateMouseSelection(msg tea.MouseMsg) (bool, tea.Cmd) {
	if m.surface != surfaceNone || m.transcriptFocused() {
		return false, nil
	}
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		if mouse.Button == tea.MouseLeft {
			m.selection = &textSelection{
				startX:   mouse.X,
				startY:   mouse.Y,
				endX:     mouse.X,
				endY:     mouse.Y,
				dragging: true,
			}
			handled, cmd := m.positionComposerCursor(msg)
			if handled {
				m.selection.input = true
				m.selection.inputLeft = m.styles.Input.GetPaddingLeft() + lipgloss.Width(m.input.Prompt)
				m.selection.inputTop = m.composerTop()
				m.selection.inputBottom = m.selection.inputTop + m.input.Height() - 1
				m.selection.anchor = composerCursorOffset(m.input)
				m.selection.head = m.selection.anchor
			}
			return true, cmd
		}
	case tea.MouseMotionMsg:
		if m.selection != nil && m.selection.dragging {
			if m.selection.input {
				target, handled, cmd := m.positionComposerDragCursor(msg)
				if handled {
					m.selection.endX, m.selection.endY = target.Mouse().X, target.Mouse().Y
					m.selection.head = composerCursorOffset(m.input)
					return true, cmd
				}
				return true, nil
			}
			m.selection.endX, m.selection.endY = mouse.X, mouse.Y
			return true, nil
		}
	case tea.MouseReleaseMsg:
		if m.selection != nil && m.selection.dragging {
			selection := m.selection
			selection.dragging = false
			if selection.input {
				drag := msg
				target, handled, cmd := m.positionComposerDragCursor(drag)
				if handled {
					selection.endX, selection.endY = target.Mouse().X, target.Mouse().Y
					selection.head = composerCursorOffset(m.input)
				}
				if selection.active() {
					return true, cmd
				}
			} else {
				selection.endX, selection.endY = mouse.X, mouse.Y
				if selection.active() {
					text := m.selectedText()
					if text != "" {
						return true, nil
					}
					m.selection = nil
					return true, nil
				}
			}
			m.selection = nil
			return true, nil
		}
	}
	return false, nil
}

func (m Model) composerTop() int {
	return m.composerTopRow + m.styles.ComposerFocused.GetBorderTopSize() + m.styles.ComposerFocused.GetPaddingTop() + 1
}

func (m *Model) positionComposerDragCursor(msg tea.MouseMsg) (tea.MouseMsg, bool, tea.Cmd) {
	mouse := msg.Mouse()
	top := m.composerTop()
	bottom := top + m.input.Height() - 1
	rows, _ := composerMetrics(m.input)
	oldOffset := m.inputOffset
	curY := mouse.Y
	if curY < top {
		m.inputOffset = max(0, m.inputOffset-1)
		curY = top
	} else if curY > bottom {
		m.inputOffset = min(max(0, rows-m.input.Height()), m.inputOffset+1)
		curY = bottom
	}
	curX := min(max(0, mouse.X), m.contentWidth()-1)
	newMsg := tea.MouseMotionMsg(tea.Mouse{X: curX, Y: curY, Button: mouse.Button, Mod: mouse.Mod})
	handled, cmd := m.positionComposerCursor(newMsg)
	if handled && m.selection != nil {
		m.selection.startY -= m.inputOffset - oldOffset
	}
	return newMsg, handled, cmd
}

func (m *Model) positionComposerCursor(msg tea.MouseMsg) (bool, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft ||
		m.surface != surfaceNone || m.transcriptFocused() {
		return false, nil
	}
	top := m.composerTop()
	row := mouse.Y - top
	if mouse.X < 0 || mouse.X >= m.contentWidth() || row < 0 || row >= m.input.Height() {
		return false, nil
	}

	layout := composerLayout(m.input)
	index := m.inputOffset + row
	if index >= len(layout.rows) {
		return false, nil
	}
	target := layout.rows[index]
	x := max(0, mouse.X-m.styles.Input.GetPaddingLeft()-lipgloss.Width(m.input.Prompt))
	display := target.Start
	for display < target.End && lipgloss.Width(string(layout.projection.Runes[target.Start:display+1])) <= x {
		display++
	}
	setComposerCursorOffset(&m.input, layout.projection.DisplayToRaw[display])
	focus := m.input.Focus()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(nil)
	rows, cursorRow := composerMetrics(m.input)
	m.syncComposerOffset(rows, cursorRow)
	return true, tea.Batch(focus, cmd)
}

func composerCursorOffset(input textarea.Model) int {
	offset := input.LineInfo().StartColumn + input.LineInfo().ColumnOffset
	for _, line := range strings.Split(input.Value(), "\n")[:input.Line()] {
		offset += len([]rune(line)) + 1
	}
	return offset
}

func setComposerCursorOffset(input *textarea.Model, offset int) {
	offset = max(0, offset)
	for line, text := range strings.Split(input.Value(), "\n") {
		width := len([]rune(text))
		if offset <= width {
			moveComposerCursor(input, line)
			input.SetCursorColumn(offset)
			updated, _ := input.Update(nil)
			*input = updated
			return
		}
		offset -= width + 1
	}
	input.CursorEnd()
	updated, _ := input.Update(nil)
	*input = updated
}

func (m Model) selectedText() string {
	if !m.selection.active() {
		return ""
	}
	if m.selection.input {
		value := []rune(m.input.Value())
		start, end := min(m.selection.anchor, m.selection.head), max(m.selection.anchor, m.selection.head)
		return string(value[start:end])
	}
	startX, startY, endX, endY := orderedSelection(m.selection)
	lines := strings.Split(m.View().Content, "\n")
	startY, endY = max(0, startY), min(endY, len(lines)-1)
	if startY > endY {
		return ""
	}
	selected := make([]string, 0, endY-startY+1)
	for row := startY; row <= endY; row++ {
		line := lines[row]
		left, right := 0, ansi.StringWidth(line)
		if row == startY {
			left = min(max(0, startX), right)
		}
		if row == endY {
			right = min(max(0, endX), right)
		}
		selected = append(selected, strings.TrimRight(ansiText(line, left, right), " "))
	}
	return strings.TrimRight(strings.Join(selected, "\n"), "\n")
}

func ansiText(line string, left, right int) string {
	return ansi.Strip(ansi.Cut(line, left, right))
}

func copySelectionCmd(text string) tea.Cmd {
	return terminal.CopyTextCmd(text)
}

func clipboardPasteCmd() tea.Cmd {
	return terminal.ReadClipboardCmd()
}

func (m *Model) insertPastedText(text string) tea.Cmd {
	text = strings.ToValidUTF8(ansi.Strip(text), "")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	m.input.InsertString(text)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(nil)
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.resizeComposer()
	return cmd
}

func (m *Model) deleteInputSelection() {
	value := []rune(m.input.Value())
	start, end := min(m.selection.anchor, m.selection.head), max(m.selection.anchor, m.selection.head)
	m.input.SetValue(string(append(value[:start], value[end:]...)))
	moveComposerCursor(&m.input, 0)
	offset := start
	for line, text := range strings.Split(m.input.Value(), "\n") {
		width := len([]rune(text))
		if offset <= width {
			moveComposerCursor(&m.input, line)
			m.input.SetCursorColumn(offset)
			break
		}
		offset -= width + 1
	}
	m.selection = nil
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.resizeComposer()
}

func phaseLabel(phase string) string {
	switch phase {
	case "planning":
		return "Planning…"
	case "build":
		return "Building…"
	case "audit":
		return "Checking work…"
	case "completion":
		return "Complete"
	default:
		return "Working…"
	}
}

func conciseCommandOutput(input, output string) string {
	switch {
	case commandIs(input, "/help"):
		return "Command palette ready — type /."
	case commandIs(input, "/models"):
		if start := strings.Index(output, "Cached models ("); start >= 0 {
			count := strings.TrimSuffix(strings.Split(strings.TrimPrefix(output[start:], "Cached models ("), ")")[0], ":")
			return "Models ready — " + count + " available."
		}
		return "Models ready."
	case commandIs(input, "/tools"):
		return "Tool catalog ready — approvals follow the selected mode."
	case commandIs(input, "/activity"):
		return "Recent activity is available with Ctrl+B."
	case commandIs(input, "/doctor"):
		return "Setup check complete."
	case commandIs(input, "/config"):
		return "Configuration ready."
	case commandIs(input, "/usage"):
		return "Usage updated."
	default:
		return output
	}
}

// planWorkflowOutput is deliberately based on the renderer's stable heading,
// not the command spelling: /plan, /plan show, and a resumed plan can all
// return the same durable blueprint.
func planWorkflowOutput(output string) bool {
	output = strings.TrimSpace(output)
	return strings.HasPrefix(output, "# Plan:") || strings.Contains(output, "\n# Plan:")
}

func (m *Model) cycleApprovalMode() (tea.Model, tea.Cmd) {
	next := "batman"
	switch m.session.ApprovalMode {
	case "strict":
		next = "batman"
	case "batman":
		next = "superman"
	case "superman":
		next = "strict"
	}
	return m, executeCommandCmd(m.ctx, m.client, m.registry, m.session, "/mode "+next, 0)
}

func (m *Model) copyLastAssistantResponse() tea.Cmd {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind == entryAssistant {
			text := m.entries[i].content
			m.appendEntry(entryStatus, "Copied last Supremo response to clipboard.")
			return terminal.CopyTextCmd(text)
		}
	}
	m.appendEntry(entryStatus, "No Supremo response found in this session to copy.")
	return nil
}

func answersFromMap(answers map[string]string) api.QuestionAnswers {
	out := api.QuestionAnswers{Answers: make([]api.QuestionAnswer, 0, len(answers))}
	for id, selected := range answers {
		item := api.QuestionAnswer{ID: id}
		if selected != "" {
			item.Selected = []string{selected}
		}
		out.Answers = append(out.Answers, item)
	}
	return out
}

func todosFromMessages(messages []api.Message) []api.TodoItem {
	var latest []api.TodoItem
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		if items := components.ParseTodos(messageText(message)); len(items) > 0 {
			latest = items
		}
	}
	return latest
}
