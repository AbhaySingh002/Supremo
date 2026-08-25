package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
)

type behaviorClient struct {
	api.Client
	submitErr  error
	keys       []string
	response   api.RespondInteractionRequest
	catalog    api.ModelCatalog
	configured api.ConfigureProviderRequest
}

type behaviorStream struct {
	events chan api.Event
	err    error
	closed bool
}

func (s *behaviorStream) Events() <-chan api.Event { return s.events }
func (s *behaviorStream) Err() error               { return s.err }
func (s *behaviorStream) Close()                   { s.closed = true }

func (c *behaviorClient) SubmitPrompt(_ context.Context, request api.SubmitPromptRequest) (api.Receipt, error) {
	c.keys = append(c.keys, request.IdempotencyKey)
	return api.Receipt{Accepted: c.submitErr == nil, RunID: "run-1", MessageID: "message-1", AcceptedCursor: 4}, c.submitErr
}

func (c *behaviorClient) RespondInteraction(_ context.Context, request api.RespondInteractionRequest) error {
	c.response = request
	return nil
}

func (c *behaviorClient) ListModels(_ context.Context, _ api.ListModelsRequest) (api.ModelCatalog, error) {
	return c.catalog, nil
}

func (c *behaviorClient) ConfigureProvider(_ context.Context, request api.ConfigureProviderRequest) (api.InitializeResult, error) {
	c.configured = request
	return api.InitializeResult{Provider: valueOr(request.Provider, "openai"), Model: valueOr(request.Model, "gpt-test"), CredentialReady: true}, nil
}

func valueOr(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

func TestPromptComposerClearsOnlyAfterAcceptance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &behaviorClient{submitErr: errors.New("offline")}
	model := New(client, ".", "chat", Options{Context: ctx, Shutdown: cancel})
	model.credentialReady = true
	model.input.SetValue("keep this request")
	model.active = &activeTask{id: 1, kind: taskAgent}
	updated, _ := model.Update(promptAcceptedMsg{id: 1, display: "keep this request", err: client.submitErr})
	model = updated.(Model)
	if got := model.input.Value(); got != "keep this request" {
		t.Fatalf("failed submit changed composer to %q", got)
	}

	model.active = &activeTask{id: 2, kind: taskAgent}
	updated, _ = model.Update(promptAcceptedMsg{id: 2, display: "keep this request", receipt: api.Receipt{Accepted: true, RunID: "run-2"}})
	model = updated.(Model)
	if model.input.Value() != "" || len(model.entries) == 0 || model.entries[len(model.entries)-2].kind != entryUser {
		t.Fatalf("accepted submit did not clear and project composer: value=%q entries=%#v", model.input.Value(), model.entries)
	}
}

func TestPromptCommandReusesItsIdempotencyKey(t *testing.T) {
	client := &behaviorClient{}
	cmd := submitPromptCmd(context.Background(), client, "chat", "hello", "hello", 1)
	_ = cmd()
	_ = cmd()
	if len(client.keys) != 2 || client.keys[0] == "" || client.keys[0] != client.keys[1] {
		t.Fatalf("idempotency keys = %#v", client.keys)
	}
}

func TestExitQuitsWhenNoRunIsActive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := newTestModel(api.Session{ID: "chat"}, ctx, cancel)
	model.input.SetValue("/exit")
	_, cmd := model.submitInput()
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("/exit did not quit the idle TUI")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("/exit did not shut down the TUI host")
	}
}

func TestIdleCancelDisablesPlanModeThroughAPI(t *testing.T) {
	session := api.Session{ID: "chat", Revision: 4, PlanMode: true}
	message := executeCommandCmd(context.Background(), sessionTestClient{}, New(nil, ".", "chat", Options{}).registry, session, "/cancel", 1)().(commandResultMsg)
	if message.err != nil || message.session.PlanMode || message.output != "Plan Mode cancelled." {
		t.Fatalf("idle cancel result = %#v", message)
	}
}

func TestDurableEventProjectionRejectsDuplicatesAndStaleSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "chat"}, ctx, cancel)
	model.sessionEpoch, model.cursor = 2, 10
	payload, _ := json.Marshal(api.AssistantChunk{Event: api.StreamEvent{Type: "text_delta", TextDelta: "fresh"}})

	updated, _ := model.Update(clientEventMsg{epoch: 1, open: true, event: api.Event{Cursor: 11, SessionID: "chat", Type: api.EventAssistantChunk, Data: payload}})
	model = updated.(Model)
	updated, _ = model.Update(clientEventMsg{epoch: 2, open: true, event: api.Event{Cursor: 10, SessionID: "chat", Type: api.EventAssistantChunk, Data: payload}})
	model = updated.(Model)
	updated, _ = model.Update(clientEventMsg{epoch: 2, open: true, event: api.Event{Cursor: 11, SessionID: "other", Type: api.EventAssistantChunk, Data: payload}})
	model = updated.(Model)
	model.active = &activeTask{runID: "current"}
	updated, _ = model.Update(clientEventMsg{epoch: 2, open: true, event: api.Event{Cursor: 11, SessionID: "chat", RunID: "stale", Type: api.EventAssistantChunk, Data: payload}})
	model = updated.(Model)
	if len(model.entries) != 0 {
		t.Fatalf("stale events reached transcript: %#v", model.entries)
	}

	updated, _ = model.Update(clientEventMsg{epoch: 2, open: true, event: api.Event{Cursor: 11, SessionID: "chat", RunID: "current", Type: api.EventAssistantChunk, Data: payload}})
	model = updated.(Model)
	model.flushStreaming()
	if len(model.entries) != 1 || !strings.Contains(model.entries[0].content, "fresh") || model.cursor != 11 {
		t.Fatalf("fresh event projection = cursor %d entries %#v", model.cursor, model.entries)
	}
}

func TestSessionSwitchAndResyncReplaceSubscriptions(t *testing.T) {
	model := New(nil, ".", "old", Options{})
	oldStream := &behaviorStream{events: make(chan api.Event)}
	model.subscription, model.sessionEpoch = oldStream, 3
	updated, cmd := model.Update(commandResultMsg{session: api.Session{ID: "new", Name: "New"}, switchSession: true})
	model = updated.(Model)
	if !oldStream.closed || model.subscription != nil || model.sessionEpoch != 4 || model.session.ID != "new" || cmd == nil {
		t.Fatalf("session switch left stale subscription: closed=%t epoch=%d session=%q", oldStream.closed, model.sessionEpoch, model.session.ID)
	}

	resyncStream := &behaviorStream{events: make(chan api.Event), err: &api.Error{Code: api.CodeResyncRequired, Message: "cursor expired"}}
	model.subscription = resyncStream
	updated, cmd = model.Update(clientEventMsg{epoch: 4, err: resyncStream.err})
	model = updated.(Model)
	if !resyncStream.closed || model.subscription != nil || model.sessionEpoch != 5 || cmd == nil {
		t.Fatalf("resync did not replace subscription: closed=%t epoch=%d", resyncStream.closed, model.sessionEpoch)
	}
}

func TestParallelToolActivityKeepsCallOrder(t *testing.T) {
	model := New(nil, ".", "chat", Options{})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-1", Tool: "read_file", ToolStatus: "running"})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-2", Tool: "search_text", ToolStatus: "running"})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-2", Tool: "search_text", ToolStatus: "completed"})
	model.recordToolEvent(progressEvent{Kind: progressTool, CallID: "call-1", Tool: "read_file", ToolStatus: "completed"})
	if len(model.activity) != 2 || model.activity[0].TaskID != "call-1" || model.activity[1].TaskID != "call-2" {
		t.Fatalf("tool activity order = %#v", model.activity)
	}
}

func TestTranscriptArtifactsRemainAvailableOnDemand(t *testing.T) {
	model := New(nil, ".", "chat", Options{})
	model.entries = transcriptFromMessages([]api.Message{{Role: "tool", Parts: []api.MessagePart{{Kind: "tool_result", Text: "full output", ArtifactID: "artifact-1", Metadata: json.RawMessage(`{"tool_name":"read_file","tool_call_id":"call-1"}`)}}}})
	if len(model.entries) != 1 || model.entries[0].artifactID != "artifact-1" || model.latestArtifactID() != "artifact-1" {
		t.Fatalf("artifact projection = %#v", model.entries)
	}
}

func TestRestoredToolCallsKeepAssistantBatchIdentity(t *testing.T) {
	messages := []api.Message{
		{ID: "assistant-1", Role: "assistant", Parts: []api.MessagePart{
			{Kind: "assistant_tool_call", Metadata: json.RawMessage(`{"id":"call-1","name":"read_file","arguments":{"path":"main.go"}}`)},
			{Kind: "assistant_tool_call", Metadata: json.RawMessage(`{"id":"call-2","name":"search_text","arguments":{"pattern":"TODO"}}`)},
		}},
		{Role: "tool", Parts: []api.MessagePart{{Kind: "tool_result", Text: `{"path":"main.go","content":"package main"}`, Metadata: json.RawMessage(`{"tool_name":"read_file","tool_call_id":"call-1"}`)}}},
		{Role: "tool", Parts: []api.MessagePart{{Kind: "tool_result", Text: `{"matches":[]}`, Metadata: json.RawMessage(`{"tool_name":"search_text","tool_call_id":"call-2"}`)}}},
	}
	entries := transcriptFromMessages(messages)
	if len(entries) != 2 || entries[0].toolBatchID != "assistant-1" || entries[1].toolBatchID != "assistant-1" || entries[0].toolCallID != "call-1" || entries[1].toolCallID != "call-2" {
		t.Fatalf("restored tool identity = %#v", entries)
	}
	activity := activityFromMessages(messages)
	if len(activity) != 2 || !strings.Contains(activity[0].Arguments, "main.go") || !strings.Contains(activity[1].Arguments, "TODO") {
		t.Fatalf("restored activity arguments = %#v", activity)
	}
}

func TestUnifiedModelCatalogAndSecretCredentialSurface(t *testing.T) {
	client := &behaviorClient{catalog: api.ModelCatalog{Providers: []api.Provider{{
		ID: "openai", Name: "OpenAI", Configured: true, MetadataState: "fresh",
		Models: []api.Model{{ID: "gpt-test", Name: "GPT Test", ContextLength: 128000}},
	}}}}
	model := New(client, ".", "chat", Options{})
	model.provider, model.modelName = "openai", "old"
	updated, _ := model.Update(modelCatalogMsg{catalog: client.catalog})
	model = updated.(Model)
	if model.surface != surfaceModel || model.modelSelector == nil {
		t.Fatalf("catalog did not open model selector: surface=%v", model.surface)
	}
	updated, cmd := model.Update(selectors.ModelSelectedMsg{ProviderID: "openai", ID: "gpt-test"})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("model selection did not issue a backend command")
	}
	for _, item := range []string{"provider", "model"} {
		if !strings.Contains(model.bodyView(), "switching") && !strings.Contains(model.bodyView(), "refreshing") {
			t.Fatalf("missing loading state after selecting %s", item)
		}
	}

	model = New(client, ".", "chat", Options{})
	model.provider = "openai"
	model.providers = []api.Provider{{ID: "openai", Name: "OpenAI", Configured: false}}
	model.input.SetValue("/auth super-secret")
	updated, _ = model.submitInput()
	model = updated.(Model)
	if model.surface != surfaceCredential || model.credential == nil || model.credential.loading {
		t.Fatalf("secure credential surface state = %#v", model.credential)
	}
	if model.credential.key.EchoMode != textinput.EchoPassword || model.credential.key.Value() != "" {
		t.Fatal("/auth did not replace visible-key entry with a fresh masked input")
	}
	if strings.Contains(strings.Join(model.inputHistory, "\n"), "super-secret") || strings.Contains(model.View().Content, "super-secret") {
		t.Fatal("credential leaked into history or rendered output")
	}
}

func TestComposerCursorFocusAndCompletionRecovery(t *testing.T) {
	model := New(nil, ".", "chat", Options{})
	for _, size := range []struct{ width, height int }{{44, 14}, {80, 24}, {130, 32}, {194, 49}} {
		model.width, model.height = size.width, size.height
		model.layout()
		view := model.View()
		if view.Cursor == nil || (model.styles.Ascii && (view.BackgroundColor != nil || view.ForegroundColor != nil || view.Cursor.Color != nil)) || (!model.styles.Ascii && (view.BackgroundColor == nil || view.ForegroundColor == nil || view.Cursor.Color == nil)) {
			t.Fatalf("%dx%d focused composer view palette/cursor = %#v", size.width, size.height, view)
		}
		lines := strings.Split(ansi.Strip(view.Content), "\n")
		if view.Cursor.Y < 0 || view.Cursor.Y >= len(lines) || !strings.Contains(lines[view.Cursor.Y], composerPlaceholder) {
			t.Fatalf("%dx%d cursor row %d is outside composer:\n%s", size.width, size.height, view.Cursor.Y, ansi.Strip(view.Content))
		}
		column := ansi.StringWidth(lines[view.Cursor.Y][:strings.Index(lines[view.Cursor.Y], composerPlaceholder)])
		if view.Cursor.X != column {
			t.Fatalf("%dx%d cursor column = %d, want %d", size.width, size.height, view.Cursor.X, column)
		}
	}
	model.focus = focusTranscript
	model.input.Blur()
	if cursor := model.View().Cursor; cursor != nil {
		t.Fatalf("blurred composer exposed cursor %#v", cursor)
	}
	model.focus = focusComposer
	model.active = &activeTask{id: 7, kind: taskCommand}
	updated, cmd := model.Update(commandResultMsg{id: 7, output: "done"})
	model = updated.(Model)
	if cmd == nil || !model.input.Focused() || model.active != nil {
		t.Fatalf("command completion did not restore composer: focused=%t active=%#v", model.input.Focused(), model.active)
	}
}

func TestEvidenceUnwrapsStorageEnvelopeIntoInlineDetails(t *testing.T) {
	artifact := api.Artifact{Previewable: true, Content: []byte(`{"tool":"list_directory","result":{"status":{"success":true},"artifact_id":"secret-hash","preview":"{\"entries\":[{\"name\":\"main.go\",\"path\":\"main.go\"}]}"}}`)}
	text := evidenceText(artifact)
	if !strings.Contains(text, "main.go") || strings.Contains(text, "artifact_id") || strings.Contains(text, `"tool"`) || looksLikeRawJSON(text) {
		t.Fatalf("normalized evidence = %q", text)
	}
	model := New(nil, ".", "chat", Options{})
	model.entries = []transcriptEntry{{kind: entryTool, tool: "list_directory", toolStatus: "completed", artifactID: "hash", arguments: `{"path":"."}`}}
	updated, _ := model.Update(artifactLoadedMsg{sessionID: "chat", index: 0, artifact: artifact})
	model = updated.(Model)
	if model.surface != surfaceNone || !model.entries[0].expanded || !strings.Contains(model.renderEntry(0, model.entries[0]), "main.go") {
		t.Fatalf("evidence did not expand inline: %#v", model.entries[0])
	}
}

func TestResponsiveChatAndActivityModes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "chat", Name: "Chat"}, ctx, cancel)
	model.provider, model.modelName = "openai", "gpt"
	model.entries = []transcriptEntry{{kind: entryUser, content: "Unicode 你好 👋"}}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 43, Height: 13})
	model = updated.(Model)
	if !strings.Contains(model.View().Content, "Terminal too small") {
		t.Fatal("missing deterministic minimum-size view")
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	model = updated.(Model)
	if strings.Contains(model.View().Content, "ACTIVITY") {
		t.Fatal("compact chat should not show activity until requested")
	}
	updated, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'b'})
	model = updated.(Model)
	if model.focus != focusActivity || !strings.Contains(model.View().Content, "ACTIVITY") {
		t.Fatal("compact activity inspector did not open")
	}

	model.activityToggled = false
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	model = updated.(Model)
	if model.activityRailWidth() < 30 || !strings.Contains(model.View().Content, "ACTIVITY") || model.contentWidth() >= model.width {
		t.Fatal("wide activity rail did not reserve chat width")
	}
	for _, size := range []struct{ width, height int }{{44, 14}, {60, 24}, {100, 28}, {130, 32}, {198, 53}} {
		model.activityToggled = false
		updated, _ = model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		model = updated.(Model)
		content := model.View().Content
		lines := strings.Split(content, "\n")
		if len(lines) > size.height {
			t.Fatalf("%dx%d render has %d rows", size.width, size.height, len(lines))
		}
		for row, line := range lines {
			if width := ansi.StringWidth(line); width > size.width {
				t.Fatalf("%dx%d render row %d is %d cells", size.width, size.height, row, width)
			}
		}
	}
}

func TestInteractionCommandCarriesExactAuthority(t *testing.T) {
	client := &behaviorClient{}
	request := api.RespondInteractionRequest{SessionID: "chat", InteractionID: "interaction-7", Decision: "deny", Reason: "no"}
	message := respondInteractionCmd(context.Background(), client, request)().(interactionResultMsg)
	if message.err != nil || client.response.SessionID != "chat" || client.response.InteractionID != "interaction-7" {
		t.Fatalf("response authority = %#v, message=%#v", client.response, message)
	}
}

func TestEditedApprovalUsesInteractionIDAndRevisedInput(t *testing.T) {
	client := &behaviorClient{}
	model := New(client, ".", "chat", Options{})
	model.session = api.Session{ID: "chat"}
	model.pendingInteraction = "interaction-edit"
	model.approval = approval.NewApprovalModel("write_file", `{"path":"a.txt"}`, rendering.NewStyles())
	updated, _ := model.updateApprovalKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	model = updated.(Model)
	updated, cmd := model.updateApprovalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated
	if cmd == nil {
		t.Fatal("edited approval did not submit")
	}
	_ = cmd()
	if client.response.InteractionID != "interaction-edit" || client.response.Decision != "edit" || string(client.response.RevisedInput) != `{"path":"a.txt"}` {
		t.Fatalf("edited approval response = %#v", client.response)
	}
}
