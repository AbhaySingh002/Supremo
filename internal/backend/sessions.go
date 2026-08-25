package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func (s *Service) Initialize(ctx context.Context) (api.InitializeResult, error) {
	cursor, err := s.store.Cursor(ctx)
	if err != nil {
		return api.InitializeResult{}, err
	}
	providerName, modelName := s.providers.ModelInfo()
	registrations := s.providers.Providers()
	result := api.InitializeResult{
		APIVersion: api.Version, ServerVersion: s.version, WorkspaceID: s.store.WorkspaceID(), Workspace: s.workspace, Cursor: cursor,
		Capabilities: api.Capabilities{AsyncRuns: true, Interactions: true, Subagents: true, SSE: true}, Provider: providerName, Model: modelName,
	}
	var currentModels []api.Model
	if runtime := s.providers.GetRuntimeConfig(); runtime != nil {
		_, _, endpoint, _, _ := runtime.Get()
		result.Endpoint = publicEndpoint(endpoint)
		result.CredentialReady = runtime.CredentialConfigured()
		for _, model := range runtime.Metadata().Models {
			currentModels = append(currentModels, api.Model{ID: model.ID, Name: model.Name, ContextLength: model.ContextLength})
		}
	}
	for _, registration := range registrations {
		provider := api.Provider{
			ID: registration.Type, Name: registration.DisplayName,
			Configured: s.providers.ProviderConfigured(registration.Type),
			Endpoint:   publicEndpoint(s.providers.ProviderEndpoint(registration.Type)), RequiresEndpoint: registration.RequiresEndpoint,
		}
		if registration.Type == strings.SplitN(providerName, ":", 2)[0] {
			provider.Models = currentModels
		}
		result.Providers = append(result.Providers, provider)
	}
	return result, nil
}

func publicEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "auth") || strings.Contains(lower, "signature") || strings.Contains(lower, "credential") {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Service) ListSessions(ctx context.Context) ([]api.Session, error) {
	sessions, err := agent.ListSessions(s.workspace)
	if err != nil {
		return nil, err
	}
	result := make([]api.Session, 0, len(sessions))
	for i := range sessions {
		if sessions[i].Origin != "subagent" {
			if err := sessions[i].AttachSurface(ctx, s.store); err != nil {
				return nil, err
			}
			result = append(result, sessionDTO(&sessions[i]))
		}
	}
	return result, nil
}

func (s *Service) CreateSession(ctx context.Context, request api.CreateSessionRequest) (api.Session, error) {
	var session *agent.Session
	var err error
	if strings.TrimSpace(request.ID) != "" {
		session, err = agent.LoadOrCreateSession(s.workspace, strings.TrimSpace(request.ID))
	} else {
		session, err = agent.NewSession(s.workspace)
	}
	if err != nil {
		return api.Session{}, apiError(api.CodeInvalidArgument, err.Error(), false)
	}
	if strings.TrimSpace(request.Name) != "" {
		if err := session.Rename(s.workspace, request.Name); err != nil {
			return api.Session{}, apiError(api.CodeInvalidArgument, err.Error(), false)
		}
	}
	if err := session.AttachSurface(ctx, s.store); err != nil {
		return api.Session{}, err
	}
	return sessionDTO(session), nil
}

func (s *Service) UpdateSession(ctx context.Context, request api.UpdateSessionRequest) (api.Session, error) {
	session, err := agent.LoadSession(s.workspace, request.SessionID)
	if err != nil {
		return api.Session{}, mapStateError(err, "session")
	}
	if request.ExpectedRevision <= 0 || request.ExpectedRevision != session.Version {
		return api.Session{}, apiError(api.CodeConflict, "session revision changed", false)
	}
	if err := session.AttachSurface(ctx, s.store); err != nil {
		return api.Session{}, err
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if err := validateSessionName(name); err != nil {
			return api.Session{}, apiError(api.CodeInvalidArgument, err.Error(), false)
		}
		session.Name, session.NeedsName = name, false
	}
	if request.ApprovalMode != nil {
		mode := tools.ApprovalMode(*request.ApprovalMode)
		if mode != tools.ApprovalStrict && mode != tools.ApprovalBatman && mode != tools.ApprovalSuperman {
			return api.Session{}, apiError(api.CodeInvalidArgument, "approval_mode must be strict, batman, or superman", false)
		}
		session.ApprovalMode = mode
	}
	if request.DryRun != nil {
		session.DryRun = *request.DryRun
	}
	if request.Checklist != nil || request.Rewind != nil || request.ProviderRetry != nil {
		if session.Features == nil {
			session.Features = &agent.FeatureConfig{}
		}
		if request.Checklist != nil {
			session.Features.UX.Checklist = request.Checklist
		}
		if request.Rewind != nil {
			session.Features.UX.Rewind = request.Rewind
		}
		if request.ProviderRetry != nil {
			session.Features.Retry.Response = request.ProviderRetry
		}
	}
	var related []state.EventInput
	if request.PlanMode != nil && *request.PlanMode != session.PlanModeActive() {
		record, err := sessionlog.New(sessionlog.EventPlanMode, sessionlog.PlanModePayload{Active: *request.PlanMode})
		if err != nil {
			return api.Session{}, err
		}
		input, err := sessionlog.ToEventInput(session.ID, record)
		if err != nil {
			return api.Session{}, err
		}
		related = append(related, input)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return api.Session{}, err
	}
	saved, err := s.store.SaveSession(ctx, state.SessionInput{
		ID: session.ID, Name: session.Name, CreatedAt: session.CreatedAt, Status: session.Status, CurrentTaskID: session.ActiveTaskID,
		Provider: session.Provider, Model: session.Model, Data: data, ExpectedVersion: request.ExpectedRevision, RelatedEvents: related,
	})
	if err != nil {
		return api.Session{}, mapStateError(err, "session")
	}
	session.Version, session.UpdatedAt = saved.Version, saved.UpdatedAt
	if request.PlanMode != nil {
		if err := session.AttachSurface(ctx, s.store); err != nil {
			return api.Session{}, err
		}
	}
	return sessionDTO(session), nil
}

func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return apiError(api.CodeInvalidArgument, "session_id is required", false)
	}
	if err := s.runtimes.DeleteSession(ctx, s.workspace, sessionID); err != nil {
		return mapStateError(err, "session")
	}
	return nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (api.SessionSnapshot, error) {
	snapshot, err := s.store.SessionSnapshot(ctx, sessionID)
	if err != nil {
		return api.SessionSnapshot{}, mapStateError(err, "session")
	}
	var session agent.Session
	if err := json.Unmarshal(snapshot.Session.Data, &session); err != nil {
		return api.SessionSnapshot{}, err
	}
	session.CreatedAt, session.UpdatedAt, session.Status, session.Version = snapshot.Session.CreatedAt, snapshot.Session.UpdatedAt, snapshot.Session.Status, snapshot.Session.Version
	result := api.SessionSnapshot{AsOfCursor: snapshot.Cursor, Revision: snapshot.Session.Version}
	result.Messages = make([]api.Message, 0, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		value := api.Message{ID: message.ID, Sequence: message.Sequence, Role: message.Role, TaskID: message.TaskID, State: message.State, CreatedAt: message.CreatedAt}
		for _, part := range message.Parts {
			value.Parts = append(value.Parts, api.MessagePart{Kind: part.Kind, Text: part.Text, ArtifactID: part.ArtifactID, Metadata: part.Metadata})
		}
		result.Messages = append(result.Messages, value)
	}
	runs := make(map[string]api.Run)
	var runOrder []string
	interactions := make(map[string]api.Interaction)
	planMode := false
	for _, event := range snapshot.Events {
		switch event.Type {
		case string(sessionlog.EventPlanMode):
			if payload, err := decodeRecordData[sessionlog.PlanModePayload](event); err == nil {
				planMode = payload.Active
			}
		case string(sessionlog.EventRunQueued):
			if payload, err := decodeRecordData[sessionlog.RunQueuedPayload](event); err == nil {
				runs[payload.RunID] = api.Run{RunID: payload.RunID, MessageID: payload.MessageID, SessionID: sessionID, Status: "queued"}
				runOrder = append(runOrder, payload.RunID)
			}
		case string(sessionlog.EventRunStart):
			if payload, err := decodeRecordData[sessionlog.RunStartPayload](event); err == nil {
				run := runs[payload.RunID]
				run.Status = "running"
				runs[payload.RunID] = run
			}
		case string(sessionlog.EventRunEnd):
			if payload, err := decodeRecordData[sessionlog.RunEndPayload](event); err == nil {
				runs[payload.RunID] = runDTO(sessionID, payload)
			}
		case string(sessionlog.EventInteractionRequest):
			if payload, err := decodeRecordData[sessionlog.InteractionRequestedPayload](event); err == nil {
				interactions[payload.InteractionID] = api.Interaction{ID: payload.InteractionID, SessionID: sessionID, RunID: payload.RunID, Kind: payload.Kind, Status: "pending", Data: payload.Data}
			}
		case string(sessionlog.EventInteractionResolve):
			if payload, err := decodeRecordData[sessionlog.InteractionResolvedPayload](event); err == nil {
				delete(interactions, payload.InteractionID)
			}
		}
	}
	sessionPlan := planMode
	result.Session = sessionDTO(&session)
	result.Session.PlanMode = sessionPlan
	for _, runID := range runOrder {
		result.Runs = append(result.Runs, runs[runID])
	}
	for _, interaction := range interactions {
		result.PendingInteractions = append(result.PendingInteractions, interaction)
	}
	sort.Slice(result.PendingInteractions, func(i, j int) bool { return result.PendingInteractions[i].ID < result.PendingInteractions[j].ID })
	agents, err := s.ListAgents(ctx, api.AgentControlRequest{ParentSessionID: sessionID})
	if err == nil {
		result.Agents = agents
	}
	return result, nil
}

func sessionDTO(session *agent.Session) api.Session {
	return api.Session{ID: session.ID, Name: session.Name, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt, Status: session.Status,
		Revision: session.Version, Provider: session.Provider, Model: session.Model, ApprovalMode: string(session.ApprovalMode), DryRun: session.DryRun,
		PlanMode: session.PlanModeActive(), ParentSessionID: session.ParentSessionID, Origin: session.Origin,
		Checklist: session.ChecklistEnabled(), Rewind: session.RewindEnabled(), ProviderRetry: session.ResponseRetryEnabled()}
}

func validateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if utf8.RuneCountInString(name) > 80 {
		return fmt.Errorf("session name must be at most 80 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("session name cannot contain control characters")
		}
	}
	return nil
}
