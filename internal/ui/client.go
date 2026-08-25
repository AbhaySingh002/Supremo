package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

type clientReadyMsg struct {
	epoch        int
	initialize   api.InitializeResult
	snapshot     api.SessionSnapshot
	workspace    api.WorkspaceStatus
	usage        api.Usage
	subscription api.EventStream
	err          error
}

type clientEventMsg struct {
	epoch int
	event api.Event
	err   error
	open  bool
}

type promptAcceptedMsg struct {
	id      int
	prompt  string
	display string
	receipt api.Receipt
	err     error
}

type cancelRunResultMsg struct{ err error }

type interactionResultMsg struct {
	interactionID string
	err           error
}

type snapshotRefreshMsg struct {
	epoch    int
	snapshot api.SessionSnapshot
	err      error
}

type modelCatalogMsg struct {
	catalog api.ModelCatalog
	err     error
}

type providerConfiguredMsg struct {
	initialize api.InitializeResult
	catalog    *api.ModelCatalog
	err        error
}

func initializeClientCmd(ctx context.Context, client api.Client, sessionID string, epoch int) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return clientReadyMsg{epoch: epoch, err: errors.New("backend is unavailable")}
		}
		initialized, err := client.Initialize(ctx)
		if err != nil {
			return clientReadyMsg{epoch: epoch, err: err}
		}
		snapshot, err := client.GetSession(ctx, sessionID)
		if err != nil {
			return clientReadyMsg{epoch: epoch, initialize: initialized, err: err}
		}
		workspace, _ := client.WorkspaceStatus(ctx)
		usage, _ := client.ProviderUsage(ctx)
		subscription, err := client.Subscribe(ctx, api.SubscribeRequest{SessionID: sessionID, AfterCursor: snapshot.AsOfCursor})
		return clientReadyMsg{epoch: epoch, initialize: initialized, snapshot: snapshot, workspace: workspace, usage: usage, subscription: subscription, err: err}
	}
}

func waitClientEventCmd(stream api.EventStream, epoch int) tea.Cmd {
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-stream.Events()
		if !ok {
			return clientEventMsg{epoch: epoch, err: stream.Err()}
		}
		return clientEventMsg{epoch: epoch, event: event, open: true}
	}
}

func refreshSnapshotCmd(ctx context.Context, client api.Client, sessionID string, epoch int) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return snapshotRefreshMsg{epoch: epoch, err: errors.New("backend is unavailable")}
		}
		snapshot, err := client.GetSession(ctx, sessionID)
		return snapshotRefreshMsg{epoch: epoch, snapshot: snapshot, err: err}
	}
}

func listModelsCmd(ctx context.Context, client api.Client, refresh bool) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return modelCatalogMsg{err: errors.New("backend is unavailable")}
		}
		catalog, err := client.ListModels(ctx, api.ListModelsRequest{Refresh: refresh})
		return modelCatalogMsg{catalog: catalog, err: err}
	}
}

func configureProviderCmd(ctx context.Context, client api.Client, request api.ConfigureProviderRequest, openModels bool) tea.Cmd {
	secret := ""
	if request.APIKey != nil {
		secret = *request.APIKey
	}
	return func() tea.Msg {
		if client == nil {
			return providerConfiguredMsg{err: errors.New("backend is unavailable")}
		}
		initialized, err := client.ConfigureProvider(ctx, request)
		if err != nil {
			message := err.Error()
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[redacted]")
			}
			return providerConfiguredMsg{err: errors.New(message)}
		}
		result := providerConfiguredMsg{initialize: initialized}
		if openModels {
			catalog, catalogErr := client.ListModels(ctx, api.ListModelsRequest{})
			result.catalog, result.err = &catalog, catalogErr
		}
		return result
	}
}

func submitPromptCmd(ctx context.Context, client api.Client, sessionID, prompt, display string, id int) tea.Cmd {
	key := idempotencyKey()
	return func() tea.Msg {
		if client == nil {
			return promptAcceptedMsg{id: id, prompt: prompt, display: display, err: errors.New("backend is unavailable")}
		}
		receipt, err := client.SubmitPrompt(ctx, api.SubmitPromptRequest{SessionID: sessionID, Prompt: prompt, IdempotencyKey: key})
		return promptAcceptedMsg{id: id, prompt: prompt, display: display, receipt: receipt, err: err}
	}
}

func cancelRunCmd(ctx context.Context, client api.Client, sessionID, runID string) tea.Cmd {
	return func() tea.Msg {
		if client == nil || runID == "" {
			return cancelRunResultMsg{err: errors.New("no active backend run")}
		}
		_, err := client.CancelRun(ctx, api.CancelRunRequest{SessionID: sessionID, RunID: runID})
		return cancelRunResultMsg{err: err}
	}
}

func respondInteractionCmd(ctx context.Context, client api.Client, request api.RespondInteractionRequest) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return interactionResultMsg{interactionID: request.InteractionID, err: errors.New("backend is unavailable")}
		}
		err := client.RespondInteraction(ctx, request)
		return interactionResultMsg{interactionID: request.InteractionID, err: err}
	}
}

func idempotencyKey() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err == nil {
		return hex.EncodeToString(data)
	}
	return fmt.Sprintf("tui-%d", time.Now().UnixNano())
}

func decodeEvent[T any](event api.Event) (T, error) {
	var value T
	err := json.Unmarshal(event.Data, &value)
	return value, err
}

func (m *Model) applyInitialize(value api.InitializeResult) {
	m.workspace = value.Workspace
	m.provider, m.modelName, m.credentialReady = value.Provider, value.Model, value.CredentialReady
	m.providers = append([]api.Provider(nil), value.Providers...)
	if m.provider == "" {
		m.provider = "unconfigured"
	}
}

func (m *Model) applySnapshot(snapshot api.SessionSnapshot) {
	pendingActivity := append([]activityEvent(nil), m.activity...)
	m.session = snapshot.Session
	m.cursor = max(m.cursor, snapshot.AsOfCursor)
	m.entries = transcriptFromMessages(snapshot.Messages)
	m.collapsedToolBatches = make(map[string]bool)
	m.todos = todosFromMessages(snapshot.Messages)
	if len(m.todos) > 0 {
		m.setTodos(m.todos)
	}
	m.agents = append([]api.Agent(nil), snapshot.Agents...)
	m.runs = append([]api.Run(nil), snapshot.Runs...)
	m.activity = activityFromMessages(snapshot.Messages)
	completed := make(map[string]bool, len(m.activity))
	for _, item := range m.activity {
		completed[item.TaskID] = item.TaskID != ""
	}
	for _, item := range pendingActivity {
		if (item.Status == "running" || item.Status == "queued" || item.Status == "waiting approval") && !completed[item.TaskID] {
			m.activity = append(m.activity, item)
		}
	}
	if len(m.activity) > 32 {
		m.activity = m.activity[len(m.activity)-32:]
	}
	m.streamingEntry, m.liveEntry = -1, -1
	m.followTail, m.newOutput = true, 0
	m.active = nil
	m.approval, m.planQuestion = nil, nil
	if m.surface == surfaceApproval || m.surface == surfacePlanQuestion {
		m.surface = surfaceNone
		m.layout()
	}
	m.pendingInteraction = ""
	for index := range snapshot.Runs {
		run := snapshot.Runs[index]
		if run.Status == "running" || run.Status == "queued" || run.Status == "cancelling" {
			ctx, cancel := context.WithCancel(m.ctx)
			m.nextTaskID++
			m.active = &activeTask{id: m.nextTaskID, ctx: ctx, cancel: cancel, kind: taskAgent, runID: run.RunID}
			m.phase = run.Status
		}
	}
	if len(snapshot.Runs) > 0 && m.active == nil {
		last := snapshot.Runs[len(snapshot.Runs)-1]
		switch last.Status {
		case "failed":
			message := strings.TrimSpace(last.Error)
			if message == "" {
				message = "Run failed."
			}
			m.entries = append(m.entries, transcriptEntry{kind: entryError, content: message, dirty: true})
		case "cancelled", "interrupted":
			m.entries = append(m.entries, transcriptEntry{kind: entryStatus, content: "Run " + last.Status + ".", dirty: true})
		}
	}
	if len(snapshot.PendingInteractions) > 0 {
		m.openInteraction(snapshot.PendingInteractions[0])
	}
	m.rebuildFeed()
}

func (m *Model) applyAPIEvent(event api.Event) tea.Cmd {
	for _, progress := range progressFromAPI(event) {
		_ = m.applyProgress(progress)
	}
	switch event.Type {
	case api.EventRunQueued:
		m.phase = "queued"
		if m.active == nil {
			ctx, cancel := context.WithCancel(m.ctx)
			m.nextTaskID++
			m.active = &activeTask{id: m.nextTaskID, ctx: ctx, cancel: cancel, kind: taskAgent, runID: event.RunID}
		} else if m.active.runID == "" {
			m.active.runID = event.RunID
		}
	case api.EventRunStart:
		m.phase = "running"
		if m.active != nil && m.active.runID == "" {
			m.active.runID = event.RunID
		}
	case api.EventRunEnd:
		var run api.Run
		_ = json.Unmarshal(event.Data, &run)
		m.flushStreaming()
		m.clearLiveStatus()
		m.active = nil
		m.cancelling = false
		m.approval = nil
		if m.surface == surfaceApproval {
			m.surface = surfaceNone
			m.layout()
		}
		m.pendingInteraction = ""
		if run.Status == "cancelled" || run.Status == "interrupted" {
			m.finishStreaming(entryStatus, "")
			m.appendEntry(entryStatus, "Run "+run.Status+".")
		} else if run.Status == "failed" {
			m.finishStreaming(entryStatus, "")
			m.appendEntry(entryError, run.Error)
		}
		return refreshSnapshotCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
	case api.EventAssistantMessage, api.EventToolResult:
		return refreshSnapshotCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
	case api.EventInteractionRequest:
		var payload api.InteractionEvent
		if json.Unmarshal(event.Data, &payload) == nil {
			m.openInteraction(api.Interaction{ID: payload.InteractionID, SessionID: event.SessionID, RunID: payload.RunID, Kind: payload.Kind, Status: "pending", Data: payload.Data})
		}
	case api.EventInteractionResolve:
		var payload api.InteractionEvent
		if json.Unmarshal(event.Data, &payload) == nil && payload.InteractionID == m.pendingInteraction {
			m.pendingInteraction = ""
			m.approval, m.planQuestion = nil, nil
			if m.surface == surfaceApproval || m.surface == surfacePlanQuestion {
				m.surface = surfaceNone
			}
			m.layout()
			return m.restoreFocus()
		}
	case api.EventPlanMode:
		var payload api.PlanModeUpdate
		if json.Unmarshal(event.Data, &payload) == nil {
			m.session.PlanMode = payload.Active
		}
	case api.EventUsage:
		var payload api.UsageDetail
		if json.Unmarshal(event.Data, &payload) == nil {
			m.inputTokens, m.outputTokens = payload.Usage.InputTokens, payload.Usage.OutputTokens
		}
	case api.EventSubagentDescriptor, api.EventSubagentQueued, api.EventSubagentRunStart, api.EventSubagentRunEnd:
		return refreshSnapshotCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
	case api.EventSessionUpdated, api.EventSessionCreated, api.EventSessionArchived:
		return refreshSnapshotCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch)
	}
	return nil
}

func runScopedEvent(kind string) bool {
	switch kind {
	case api.EventAssistantMessage, api.EventAssistantChunk, api.EventToolCall, api.EventToolResult,
		api.EventTurnStart, api.EventTurnEnd, api.EventStepStart, api.EventStepEnd,
		api.EventUsage, api.EventRetry, api.EventError, api.EventFinish,
		api.EventRunStart, api.EventRunEnd, api.EventInteractionRequest, api.EventInteractionResolve:
		return true
	default:
		return false
	}
}

func (m *Model) openInteraction(interaction api.Interaction) {
	switch interaction.Kind {
	case "approval":
		var data api.ApprovalRequestData
		if json.Unmarshal(interaction.Data, &data) != nil {
			return
		}
		arguments := string(data.Arguments)
		var encoded string
		if json.Unmarshal(data.Arguments, &encoded) == nil && strings.TrimSpace(encoded) != "" {
			arguments = encoded
		}
		if arguments == "" || arguments == "null" {
			arguments = "{}"
		}
		arguments = approvalJSON(arguments)
		m.pendingInteraction = interaction.ID
		m.priorFocus, m.focus = m.focus, focusOverlay
		m.input.Blur()
		m.approval = approval.NewApprovalModel(data.Tool, arguments, rendering.NewStyles())
		m.planQuestion = nil
		m.surface = surfaceApproval
		m.layout()
	case "question":
		var request api.QuestionRequest
		if json.Unmarshal(interaction.Data, &request) != nil {
			return
		}
		m.pendingInteraction = interaction.ID
		m.priorFocus, m.focus = m.focus, focusOverlay
		m.input.Blur()
		m.planQuestion = plan.NewPlanQuestionModel(request, rendering.NewStyles(), m.contentWidth())
		m.approval = nil
		m.surface = surfacePlanQuestion
		m.layout()
	}
}
