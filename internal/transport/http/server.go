package httptransport

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

type Server struct {
	backend      api.Client
	version      string
	token        string
	listener     net.Listener
	http         *http.Server
	allowedHosts map[string]bool
}

func Listen(address, token, version string, service api.Client) (*Server, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address: %w", err)
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("server listen address must be loopback")
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if token == "" {
		token, err = NewToken()
		if err != nil {
			listener.Close()
			return nil, err
		}
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	allowed := map[string]bool{listener.Addr().String(): true, net.JoinHostPort("127.0.0.1", port): true, net.JoinHostPort("localhost", port): true}
	server := &Server{backend: service, version: version, token: token, listener: listener, allowedHosts: allowed}
	server.http = &http.Server{Handler: server.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	return server, nil
}

func NewToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Server) Token() string { return s.token }
func (s *Server) URL() string   { return "http://" + s.listener.Addr().String() }

func (s *Server) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) Serve(ctx context.Context) error {
	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.http.Shutdown(shutdownCtx)
			cancel()
		case <-shutdownDone:
		}
	}()
	err := s.http.Serve(s.listener)
	close(shutdownDone)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("POST /api/v1/rpc", s.rpc)
	mux.HandleFunc("GET /api/v1/events", s.events)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !s.allowedHosts[strings.ToLower(r.Host)] {
			writeHTTPError(w, "", apiError(api.CodeForbidden, "invalid Host header", false))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.allowedHosts[strings.ToLower(strings.TrimPrefix(origin, "http://"))] {
			writeHTTPError(w, "", apiError(api.CodeForbidden, "cross-origin requests are forbidden", false))
			return
		}
		if r.URL.Path != "/api/v1/health" && !s.authorized(r) {
			writeHTTPError(w, "", apiError(api.CodeUnauthorized, "invalid bearer token", false))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return len(provided) == len(s.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.version, "api_version": api.Version})
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	if media := r.Header.Get("Content-Type"); !strings.HasPrefix(media, "application/json") {
		writeHTTPError(w, "", apiError(api.CodeInvalidArgument, "Content-Type must be application/json", false))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request api.RPCRequest
	if err := decoder.Decode(&request); err != nil {
		writeHTTPError(w, request.ID, apiError(api.CodeInvalidArgument, "invalid request: "+err.Error(), false))
		return
	}
	if err := ensureEOF(decoder); err != nil || request.V != api.Version || request.ID == "" || request.Method == "" {
		writeHTTPError(w, request.ID, apiError(api.CodeInvalidArgument, "v=1, id, and method are required", false))
		return
	}
	result, err := s.dispatch(r.Context(), request)
	if err != nil {
		writeHTTPError(w, request.ID, normalizeError(err))
		return
	}
	writeJSON(w, http.StatusOK, api.RPCResponse{V: api.Version, ID: request.ID, OK: true, Result: result})
}

func (s *Server) dispatch(ctx context.Context, request api.RPCRequest) (any, error) {
	switch request.Method {
	case "initialize":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.Initialize(ctx)
	case "session.list":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.ListSessions(ctx)
	case "session.create":
		var params api.CreateSessionRequest
		return call(ctx, request.Params, &params, s.backend.CreateSession)
	case "session.get":
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.backend.GetSession(ctx, params.SessionID)
	case "session.update":
		var params api.UpdateSessionRequest
		return call(ctx, request.Params, &params, s.backend.UpdateSession)
	case "session.delete":
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return accepted(s.backend.DeleteSession(ctx, params.SessionID))
	case "session.clear":
		var params api.SessionRequest
		return call(ctx, request.Params, &params, s.backend.ClearSession)
	case "session.reset":
		var params api.SessionRequest
		return call(ctx, request.Params, &params, s.backend.ResetSession)
	case "checkpoint.list":
		var params api.SessionRequest
		return call(ctx, request.Params, &params, s.backend.ListCheckpoints)
	case "checkpoint.rewind":
		var params api.RewindRequest
		return call(ctx, request.Params, &params, s.backend.RewindSession)
	case "side.answer":
		var params api.SideQuestionRequest
		return call(ctx, request.Params, &params, s.backend.AnswerSideQuestion)
	case "artifact.get":
		var params api.ArtifactRequest
		return call(ctx, request.Params, &params, s.backend.GetArtifact)
	case "model.list":
		var params api.ListModelsRequest
		return call(ctx, request.Params, &params, s.backend.ListModels)
	case "provider.configure":
		var params api.ConfigureProviderRequest
		return call(ctx, request.Params, &params, s.backend.ConfigureProvider)
	case "provider.refresh":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.RefreshProviderMetadata(ctx)
	case "provider.usage":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.ProviderUsage(ctx)
	case "provider.embeddings.configure":
		var params api.ConfigureEmbeddingsRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return accepted(s.backend.ConfigureEmbeddings(ctx, params))
	case "config.reload":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.ReloadConfiguration(ctx)
	case "tool.list":
		var params api.SessionRequest
		return call(ctx, request.Params, &params, s.backend.ListTools)
	case "tool.activity":
		var params api.SessionRequest
		return call(ctx, request.Params, &params, s.backend.ToolActivity)
	case "workspace.status":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.WorkspaceStatus(ctx)
	case "workspace.diff":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.WorkspaceDiff(ctx)
	case "workspace.health":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.Health(ctx)
	case "context.status":
		var params api.ContextStatusRequest
		return call(ctx, request.Params, &params, s.backend.ContextStatus)
	case "index.status":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.IndexStatus(ctx)
	case "index.update":
		var params api.UpdateIndexRequest
		return call(ctx, request.Params, &params, s.backend.UpdateIndex)
	case "workspace.initialize":
		if err := noParams(request.Params); err != nil {
			return nil, err
		}
		return s.backend.InitializeWorkspace(ctx)
	case "run.submit":
		var params api.SubmitPromptRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		params.IdempotencyKey = request.IdempotencyKey
		return s.backend.SubmitPrompt(ctx, params)
	case "run.cancel":
		var params api.CancelRunRequest
		return call(ctx, request.Params, &params, s.backend.CancelRun)
	case "interaction.respond":
		var params api.RespondInteractionRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return accepted(s.backend.RespondInteraction(ctx, params))
	case "agent.start":
		var params api.StartAgentRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		params.IdempotencyKey = request.IdempotencyKey
		return s.backend.StartAgent(ctx, params)
	case "agent.list":
		var params api.AgentControlRequest
		return call(ctx, request.Params, &params, s.backend.ListAgents)
	case "agent.send":
		var params api.AgentMessageRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		params.IdempotencyKey = request.IdempotencyKey
		return s.backend.SendAgentMessage(ctx, params)
	case "agent.wait":
		var params api.AgentControlRequest
		return call(ctx, request.Params, &params, s.backend.WaitAgent)
	case "agent.interrupt":
		var params api.AgentControlRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return accepted(s.backend.InterruptAgent(ctx, params))
	default:
		return nil, apiError(api.CodeNotFound, "unknown method", false)
	}
}

func noParams(raw json.RawMessage) error {
	var params struct{}
	return decodeParams(raw, &params)
}

func call[P any, R any](ctx context.Context, raw json.RawMessage, params *P, fn func(context.Context, P) (R, error)) (any, error) {
	if err := decodeParams(raw, params); err != nil {
		return nil, err
	}
	return fn(ctx, *params)
}

func accepted(err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]bool{"accepted": true}, nil
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apiError(api.CodeInvalidArgument, "invalid params: "+err.Error(), false)
	}
	if err := ensureEOF(decoder); err != nil {
		return apiError(api.CodeInvalidArgument, "invalid params: trailing data", false)
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON")
	}
	return err
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("after_cursor")
	}
	if value != "" {
		var err error
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			writeHTTPError(w, "", apiError(api.CodeInvalidArgument, "invalid event cursor", false))
			return
		}
	}
	subscription, err := s.backend.Subscribe(r.Context(), api.SubscribeRequest{AfterCursor: after, SessionID: r.URL.Query().Get("session_id")})
	if err != nil {
		writeHTTPError(w, "", normalizeError(err))
		return
	}
	defer subscription.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeHTTPError(w, "", apiError(api.CodeInternal, "streaming is unsupported", false))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	flusher.Flush()
	write := func(data string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.WriteString(w, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !write(": heartbeat\n\n") {
				return
			}
		case event, ok := <-subscription.Events():
			if !ok {
				if err := subscription.Err(); err != nil {
					data, _ := json.Marshal(normalizeError(err))
					_ = write(fmt.Sprintf("event: error\ndata: %s\n\n", data))
				}
				return
			}
			data, _ := json.Marshal(event)
			prefix := ""
			if event.Cursor > 0 {
				prefix = fmt.Sprintf("id: %d\n", event.Cursor)
			}
			if !write(fmt.Sprintf("%sevent: %s\ndata: %s\n\n", prefix, event.Type, data)) {
				return
			}
		}
	}
}

func apiError(code api.ErrorCode, message string, retryable bool) *api.Error {
	return &api.Error{Code: code, Message: message, Retryable: retryable}
}

func normalizeError(err error) *api.Error {
	var value *api.Error
	if errors.As(err, &value) {
		return value
	}
	var failure *providers.ProviderFailure
	if errors.As(err, &failure) {
		switch failure.Code {
		case providers.FailureAuth, providers.FailureInvalidCredential:
			return apiError(api.CodeProviderAuth, failure.Message, false)
		case providers.FailureRateLimit, providers.FailureQuota:
			return apiError(api.CodeProviderRateLimit, failure.Message, true)
		case providers.FailureContextWindowExceeded:
			return apiError(api.CodeContextLimit, failure.Message, false)
		case providers.FailureAborted:
			return apiError(api.CodeCancelled, failure.Message, false)
		case providers.FailureServer, providers.FailureTimeout, providers.FailureTransport:
			return apiError(api.CodeProviderDown, failure.Message, true)
		}
	}
	if errors.Is(err, context.Canceled) {
		return apiError(api.CodeCancelled, "request cancelled", false)
	}
	return apiError(api.CodeInternal, err.Error(), false)
}

func writeHTTPError(w http.ResponseWriter, id string, err *api.Error) {
	status := http.StatusInternalServerError
	switch err.Code {
	case api.CodeInvalidArgument:
		status = http.StatusBadRequest
	case api.CodeUnauthorized:
		status = http.StatusUnauthorized
	case api.CodeForbidden:
		status = http.StatusForbidden
	case api.CodeNotFound:
		status = http.StatusNotFound
	case api.CodeConflict, api.CodeBusy:
		status = http.StatusConflict
	case api.CodeProviderRateLimit:
		status = http.StatusTooManyRequests
	}
	writeJSON(w, status, api.RPCResponse{V: api.Version, ID: id, OK: false, Error: err})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
