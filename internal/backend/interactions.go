package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/api"
	interactionbroker "github.com/AbhaySingh002/supremo/internal/interaction"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func (s *Service) RespondInteraction(ctx context.Context, request api.RespondInteractionRequest) error {
	if err := s.ready(); err != nil {
		return apiError(api.CodeBusy, err.Error(), true)
	}
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.InteractionID) == "" {
		return apiError(api.CodeInvalidArgument, "session_id and interaction_id are required", false)
	}
	requested, resolved, err := s.findInteraction(ctx, request.SessionID, request.InteractionID)
	if err != nil {
		return err
	}
	responseData, resolution, answers, err := interactionResponse(request, requested.Kind)
	if err != nil {
		return err
	}
	if resolved != nil {
		if jsonEqual(resolved.Data, responseData) {
			return nil
		}
		return apiError(api.CodeConflict, "interaction was already resolved differently", false)
	}
	if requested.Kind == "question" {
		if err := s.interactions.ResolveQuestion(ctx, request.SessionID, request.InteractionID, answers); err != nil {
			if errors.Is(err, interactionbroker.ErrInteractionNotFound) {
				return apiError(api.CodeConflict, "question is no longer pending", false)
			}
			return err
		}
		return nil
	}
	if err := s.runtimes.ResolveApprovalSession(request.SessionID, request.InteractionID, resolution); err != nil {
		return apiError(api.CodeConflict, err.Error(), false)
	}
	return nil
}

func (s *Service) findInteraction(ctx context.Context, sessionID, interactionID string) (sessionlog.InteractionRequestedPayload, *sessionlog.InteractionResolvedPayload, error) {
	events, err := s.store.Events(ctx, state.EventQuery{SessionID: sessionID})
	if err != nil {
		return sessionlog.InteractionRequestedPayload{}, nil, err
	}
	var requested sessionlog.InteractionRequestedPayload
	var resolved *sessionlog.InteractionResolvedPayload
	for _, event := range events {
		switch event.Type {
		case string(sessionlog.EventInteractionRequest):
			value, err := decodeRecordData[sessionlog.InteractionRequestedPayload](event)
			if err == nil && value.InteractionID == interactionID {
				requested = value
			}
		case string(sessionlog.EventInteractionResolve):
			value, err := decodeRecordData[sessionlog.InteractionResolvedPayload](event)
			if err == nil && value.InteractionID == interactionID {
				copy := value
				resolved = &copy
			}
		}
	}
	if requested.InteractionID == "" {
		return requested, nil, apiError(api.CodeNotFound, "interaction not found", false)
	}
	return requested, resolved, nil
}

func interactionResponse(request api.RespondInteractionRequest, kind string) (json.RawMessage, tools.ApprovalResolution, questions.AnswerSet, error) {
	if kind == "question" {
		var answers questions.AnswerSet
		if len(request.Answers) == 0 || json.Unmarshal(request.Answers, &answers) != nil {
			return nil, tools.ApprovalResolution{}, answers, apiError(api.CodeInvalidArgument, "answers must contain a valid answer set", false)
		}
		data, _ := json.Marshal(map[string]any{"answers": answers.Answers, "reason": ""})
		return data, tools.ApprovalResolution{}, answers, nil
	}
	resolution := tools.ApprovalResolution{Reason: request.Reason}
	switch request.Decision {
	case "approve", "approved":
		resolution.Decision = "approved"
	case "deny", "denied":
		resolution.Decision = "denied"
	case "edit", "edited":
		if len(request.RevisedInput) == 0 {
			return nil, resolution, questions.AnswerSet{}, apiError(api.CodeInvalidArgument, "revised_input is required for edit", false)
		}
		if err := json.Unmarshal(request.RevisedInput, &resolution.Input); err != nil {
			return nil, resolution, questions.AnswerSet{}, apiError(api.CodeInvalidArgument, "revised_input must be valid JSON", false)
		}
		resolution.Decision = "edited"
	default:
		return nil, resolution, questions.AnswerSet{}, apiError(api.CodeInvalidArgument, "decision must be approve, deny, or edit", false)
	}
	data, _ := json.Marshal(map[string]any{"decision": resolution.Decision, "reason": resolution.Reason, "input": resolution.Input})
	return data, resolution, questions.AnswerSet{}, nil
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && bytes.Equal(mustCanonical(a), mustCanonical(b))
}

func mustCanonical(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
