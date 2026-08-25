package httptransport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AbhaySingh002/supremo/internal/api"
)

const maxResponseBytes = 2 << 20

// Client is the HTTP implementation of the same transport-neutral contract
// used by the in-process TUI.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	nextID  atomic.Uint64
}

var _ api.Client = (*Client)(nil)

func NewClient(baseURL, token string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("backend URL must use http or https and include a host")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(parsed.String(), "/"), token: token, http: client}, nil
}

func rpcCall[T any](ctx context.Context, client *Client, method, idempotencyKey string, params any) (T, error) {
	var zero T
	if client == nil {
		return zero, errors.New("HTTP backend client is nil")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return zero, err
	}
	requestID := strconv.FormatUint(client.nextID.Add(1), 10)
	payload, err := json.Marshal(api.RPCRequest{V: api.Version, ID: requestID, Method: method, IdempotencyKey: idempotencyKey, Params: raw})
	if err != nil {
		return zero, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/v1/rpc", bytes.NewReader(payload))
	if err != nil {
		return zero, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()
	var envelope struct {
		V      int             `json:"v"`
		ID     string          `json:"id"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *api.Error      `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return zero, fmt.Errorf("decode backend response: %w", err)
	}
	if envelope.Error != nil {
		return zero, envelope.Error
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || !envelope.OK {
		return zero, &api.Error{Code: api.CodeInternal, Message: response.Status, Retryable: response.StatusCode >= 500}
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return zero, nil
	}
	if err := json.Unmarshal(envelope.Result, &zero); err != nil {
		return zero, fmt.Errorf("decode %s result: %w", method, err)
	}
	return zero, nil
}

func (c *Client) Initialize(ctx context.Context) (api.InitializeResult, error) {
	return rpcCall[api.InitializeResult](ctx, c, "initialize", "", struct{}{})
}
func (c *Client) ListSessions(ctx context.Context) ([]api.Session, error) {
	return rpcCall[[]api.Session](ctx, c, "session.list", "", struct{}{})
}
func (c *Client) CreateSession(ctx context.Context, value api.CreateSessionRequest) (api.Session, error) {
	return rpcCall[api.Session](ctx, c, "session.create", "", value)
}
func (c *Client) GetSession(ctx context.Context, sessionID string) (api.SessionSnapshot, error) {
	return rpcCall[api.SessionSnapshot](ctx, c, "session.get", "", api.SessionRequest{SessionID: sessionID})
}
func (c *Client) UpdateSession(ctx context.Context, value api.UpdateSessionRequest) (api.Session, error) {
	return rpcCall[api.Session](ctx, c, "session.update", "", value)
}
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := rpcCall[struct{}](ctx, c, "session.delete", "", api.SessionRequest{SessionID: sessionID})
	return err
}
func (c *Client) ClearSession(ctx context.Context, value api.SessionRequest) (api.SessionSnapshot, error) {
	return rpcCall[api.SessionSnapshot](ctx, c, "session.clear", "", value)
}
func (c *Client) ResetSession(ctx context.Context, value api.SessionRequest) (api.SessionSnapshot, error) {
	return rpcCall[api.SessionSnapshot](ctx, c, "session.reset", "", value)
}
func (c *Client) ListCheckpoints(ctx context.Context, value api.SessionRequest) ([]api.Checkpoint, error) {
	return rpcCall[[]api.Checkpoint](ctx, c, "checkpoint.list", "", value)
}
func (c *Client) RewindSession(ctx context.Context, value api.RewindRequest) (api.RewindResult, error) {
	return rpcCall[api.RewindResult](ctx, c, "checkpoint.rewind", "", value)
}
func (c *Client) AnswerSideQuestion(ctx context.Context, value api.SideQuestionRequest) (api.SideQuestionResult, error) {
	return rpcCall[api.SideQuestionResult](ctx, c, "side.answer", "", value)
}
func (c *Client) GetArtifact(ctx context.Context, value api.ArtifactRequest) (api.Artifact, error) {
	return rpcCall[api.Artifact](ctx, c, "artifact.get", "", value)
}
func (c *Client) ListModels(ctx context.Context, value api.ListModelsRequest) (api.ModelCatalog, error) {
	return rpcCall[api.ModelCatalog](ctx, c, "model.list", "", value)
}
func (c *Client) ConfigureProvider(ctx context.Context, value api.ConfigureProviderRequest) (api.InitializeResult, error) {
	return rpcCall[api.InitializeResult](ctx, c, "provider.configure", "", value)
}
func (c *Client) RefreshProviderMetadata(ctx context.Context) (api.InitializeResult, error) {
	return rpcCall[api.InitializeResult](ctx, c, "provider.refresh", "", struct{}{})
}
func (c *Client) ProviderUsage(ctx context.Context) (api.Usage, error) {
	return rpcCall[api.Usage](ctx, c, "provider.usage", "", struct{}{})
}
func (c *Client) ConfigureEmbeddings(ctx context.Context, value api.ConfigureEmbeddingsRequest) error {
	_, err := rpcCall[struct{}](ctx, c, "provider.embeddings.configure", "", value)
	return err
}
func (c *Client) ReloadConfiguration(ctx context.Context) (api.InitializeResult, error) {
	return rpcCall[api.InitializeResult](ctx, c, "config.reload", "", struct{}{})
}
func (c *Client) ListTools(ctx context.Context, value api.SessionRequest) ([]api.Tool, error) {
	return rpcCall[[]api.Tool](ctx, c, "tool.list", "", value)
}
func (c *Client) ToolActivity(ctx context.Context, value api.SessionRequest) ([]api.ToolActivity, error) {
	return rpcCall[[]api.ToolActivity](ctx, c, "tool.activity", "", value)
}
func (c *Client) WorkspaceStatus(ctx context.Context) (api.WorkspaceStatus, error) {
	return rpcCall[api.WorkspaceStatus](ctx, c, "workspace.status", "", struct{}{})
}
func (c *Client) WorkspaceDiff(ctx context.Context) (api.Diff, error) {
	return rpcCall[api.Diff](ctx, c, "workspace.diff", "", struct{}{})
}
func (c *Client) Health(ctx context.Context) (api.HealthReport, error) {
	return rpcCall[api.HealthReport](ctx, c, "workspace.health", "", struct{}{})
}
func (c *Client) ContextStatus(ctx context.Context, value api.ContextStatusRequest) (api.ContextStatus, error) {
	return rpcCall[api.ContextStatus](ctx, c, "context.status", "", value)
}
func (c *Client) IndexStatus(ctx context.Context) (api.IndexStatus, error) {
	return rpcCall[api.IndexStatus](ctx, c, "index.status", "", struct{}{})
}
func (c *Client) UpdateIndex(ctx context.Context, value api.UpdateIndexRequest) (api.IndexStatus, error) {
	return rpcCall[api.IndexStatus](ctx, c, "index.update", "", value)
}
func (c *Client) InitializeWorkspace(ctx context.Context) (api.WorkspaceStatus, error) {
	return rpcCall[api.WorkspaceStatus](ctx, c, "workspace.initialize", "", struct{}{})
}
func (c *Client) SubmitPrompt(ctx context.Context, value api.SubmitPromptRequest) (api.Receipt, error) {
	return rpcCall[api.Receipt](ctx, c, "run.submit", value.IdempotencyKey, value)
}
func (c *Client) CancelRun(ctx context.Context, value api.CancelRunRequest) (api.Run, error) {
	return rpcCall[api.Run](ctx, c, "run.cancel", "", value)
}
func (c *Client) RespondInteraction(ctx context.Context, value api.RespondInteractionRequest) error {
	_, err := rpcCall[struct{}](ctx, c, "interaction.respond", "", value)
	return err
}
func (c *Client) StartAgent(ctx context.Context, value api.StartAgentRequest) (api.Run, error) {
	return rpcCall[api.Run](ctx, c, "agent.start", value.IdempotencyKey, value)
}
func (c *Client) ListAgents(ctx context.Context, value api.AgentControlRequest) ([]api.Agent, error) {
	return rpcCall[[]api.Agent](ctx, c, "agent.list", "", value)
}
func (c *Client) SendAgentMessage(ctx context.Context, value api.AgentMessageRequest) (api.Run, error) {
	return rpcCall[api.Run](ctx, c, "agent.send", value.IdempotencyKey, value)
}
func (c *Client) WaitAgent(ctx context.Context, value api.AgentControlRequest) (api.Run, error) {
	return rpcCall[api.Run](ctx, c, "agent.wait", "", value)
}
func (c *Client) InterruptAgent(ctx context.Context, value api.AgentControlRequest) error {
	_, err := rpcCall[struct{}](ctx, c, "agent.interrupt", "", value)
	return err
}

type eventStream struct {
	events chan api.Event
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

func (s *eventStream) Events() <-chan api.Event { return s.events }
func (s *eventStream) Close()                   { s.cancel() }
func (s *eventStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
func (s *eventStream) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (c *Client) Subscribe(ctx context.Context, value api.SubscribeRequest) (api.EventStream, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	endpoint, _ := url.Parse(c.baseURL + "/api/v1/events")
	query := endpoint.Query()
	query.Set("after_cursor", strconv.FormatInt(value.AfterCursor, 10))
	if value.SessionID != "" {
		query.Set("session_id", value.SessionID)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		cancel()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		var envelope api.RPCResponse
		if json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&envelope) == nil && envelope.Error != nil {
			cancel()
			return nil, envelope.Error
		}
		cancel()
		return nil, fmt.Errorf("subscribe: %s", response.Status)
	}
	stream := &eventStream{events: make(chan api.Event, 256), cancel: cancel}
	go stream.read(streamCtx, response.Body)
	return stream, nil
}

func (s *eventStream) read(ctx context.Context, body io.ReadCloser) {
	defer close(s.events)
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4<<10), maxResponseBytes)
	eventType := ""
	var data strings.Builder
	dispatch := func() bool {
		if data.Len() == 0 {
			eventType = ""
			return true
		}
		raw := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if eventType == "error" {
			var value api.Error
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				s.setErr(err)
			} else {
				s.setErr(&value)
			}
			return false
		}
		eventType = ""
		var event api.Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			s.setErr(err)
			return false
		}
		select {
		case s.events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !dispatch() {
				return
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	if data.Len() > 0 {
		_ = dispatch()
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		s.setErr(err)
	}
}
