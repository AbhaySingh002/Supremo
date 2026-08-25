package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
)

func (s *Service) StartAgent(ctx context.Context, request api.StartAgentRequest) (api.Run, error) {
	if err := s.ready(); err != nil {
		return api.Run{}, apiError(api.CodeBusy, err.Error(), true)
	}
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.IdempotencyKey == "" {
		return api.Run{}, apiError(api.CodeInvalidArgument, "idempotency_key is required", false)
	}
	digest := digestJSON(struct {
		Parent string `json:"parent"`
		Label  string `json:"label"`
		Prompt string `json:"prompt"`
		Scope  string `json:"scope"`
	}{request.ParentSessionID, request.Label, request.Prompt, request.Scope})
	key := "api:v1:agent.start:" + request.IdempotencyKey
	foreground := request.RunInBackground != nil && !*request.RunInBackground
	s.admission.Lock()
	if existing, found, err := s.store.EventByIdempotency(ctx, key); err != nil {
		s.admission.Unlock()
		return api.Run{}, err
	} else if found {
		queued, err := decodeRecordData[sessionlog.SubagentQueuedPayload](existing)
		if err != nil {
			s.admission.Unlock()
			return api.Run{}, err
		}
		if queued.RequestDigest != digest {
			s.admission.Unlock()
			return api.Run{}, apiError(api.CodeConflict, "idempotency key was already used with different parameters", false)
		}
		result := api.Run{AgentID: existing.SessionID, MessageID: queued.MessageID, SessionID: existing.SessionID, Status: "queued"}
		s.admission.Unlock()
		if foreground {
			run, err := s.subagents.Wait(ctx, request.ParentSessionID, existing.SessionID, queued.MessageID)
			if err != nil {
				return api.Run{}, err
			}
			return subagentRunDTO(run), nil
		}
		return result, nil
	}
	background := true
	run, err := s.subagents.Start(ctx, agent.SubagentRequest{ParentSessionID: request.ParentSessionID, Label: request.Label, Prompt: request.Prompt,
		Scope: agent.SubagentScope(request.Scope), RunInBackground: &background, IdempotencyKey: key, RequestDigest: digest})
	s.admission.Unlock()
	if err != nil {
		return api.Run{}, apiError(api.CodeInvalidArgument, err.Error(), false)
	}
	if foreground {
		run, err = s.subagents.Wait(ctx, request.ParentSessionID, run.AgentID, run.MessageID)
		if err != nil {
			return api.Run{}, err
		}
	}
	return subagentRunDTO(run), nil
}

func (s *Service) ListAgents(ctx context.Context, request api.AgentControlRequest) ([]api.Agent, error) {
	statuses, err := s.subagents.List(ctx, request.ParentSessionID, request.Descendants)
	if err != nil {
		return nil, apiError(api.CodeForbidden, err.Error(), false)
	}
	result := make([]api.Agent, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, api.Agent{ID: status.AgentID, ParentSessionID: status.ParentSessionID, Label: status.Label, Depth: status.Depth,
			Scope: string(status.Scope), Provider: status.Provider, Model: status.Model, Status: status.Status})
	}
	return result, nil
}

func (s *Service) SendAgentMessage(ctx context.Context, request api.AgentMessageRequest) (api.Run, error) {
	if err := s.ready(); err != nil {
		return api.Run{}, apiError(api.CodeBusy, err.Error(), true)
	}
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.IdempotencyKey == "" {
		return api.Run{}, apiError(api.CodeInvalidArgument, "idempotency_key is required", false)
	}
	digest := digestJSON(request)
	key := "api:v1:agent.send:" + request.IdempotencyKey
	s.admission.Lock()
	defer s.admission.Unlock()
	if existing, found, err := s.store.EventByIdempotency(ctx, key); err != nil {
		return api.Run{}, err
	} else if found {
		queued, err := decodeRecordData[sessionlog.SubagentQueuedPayload](existing)
		if err != nil {
			return api.Run{}, err
		}
		if queued.RequestDigest != digest {
			return api.Run{}, apiError(api.CodeConflict, "idempotency key was already used with different parameters", false)
		}
		return api.Run{AgentID: existing.SessionID, MessageID: queued.MessageID, SessionID: existing.SessionID, Status: "queued"}, nil
	}
	run, err := s.subagents.SendIdempotent(ctx, request.ParentSessionID, request.AgentID, request.Message, key, digest)
	if err != nil {
		return api.Run{}, apiError(api.CodeForbidden, err.Error(), false)
	}
	return subagentRunDTO(run), nil
}

func (s *Service) WaitAgent(ctx context.Context, request api.AgentControlRequest) (api.Run, error) {
	timeout := time.Duration(request.TimeoutMillis) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run, err := s.subagents.Wait(waitCtx, request.ParentSessionID, request.AgentID, request.MessageID)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.subagentRunStatus(ctx, request.AgentID, request.MessageID)
	}
	if err != nil {
		return api.Run{}, apiError(api.CodeForbidden, err.Error(), false)
	}
	return subagentRunDTO(run), nil
}

func (s *Service) InterruptAgent(ctx context.Context, request api.AgentControlRequest) error {
	if err := s.subagents.Interrupt(ctx, request.ParentSessionID, request.AgentID); err != nil {
		return apiError(api.CodeForbidden, err.Error(), false)
	}
	return nil
}

func (s *Service) subagentRunStatus(ctx context.Context, agentID, messageID string) (api.Run, error) {
	records, err := sessionlog.Load(ctx, s.store, agentID)
	if err != nil {
		return api.Run{}, err
	}
	if messageID == "" {
		for _, record := range records {
			if record.Type == sessionlog.EventSubagentQueued {
				messageID = record.Data.(sessionlog.SubagentQueuedPayload).MessageID
			}
		}
	}
	status := "queued"
	var runID string
	for _, record := range records {
		switch record.Type {
		case sessionlog.EventSubagentRunStart:
			start := record.Data.(sessionlog.SubagentRunStartPayload)
			if start.MessageID == messageID {
				status, runID = "running", start.RunID
			}
		case sessionlog.EventSubagentRunEnd:
			end := record.Data.(sessionlog.SubagentRunEndPayload)
			if end.MessageID == messageID {
				return subagentRunDTO(agent.SubagentRun{AgentID: agentID, MessageID: messageID, RunID: end.RunID, Status: end.Status, Output: end.Output, Error: end.Error}), nil
			}
		}
	}
	return api.Run{AgentID: agentID, SessionID: agentID, MessageID: messageID, RunID: runID, Status: status}, nil
}

func subagentRunDTO(run agent.SubagentRun) api.Run {
	return api.Run{AgentID: run.AgentID, SessionID: run.AgentID, MessageID: run.MessageID, RunID: run.RunID, Status: run.Status, Output: run.Output, Error: run.Error}
}

func digestJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
