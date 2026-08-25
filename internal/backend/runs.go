package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/storage"
)

func (s *Service) SubmitPrompt(ctx context.Context, request api.SubmitPromptRequest) (api.Receipt, error) {
	if err := s.ready(); err != nil {
		return api.Receipt{}, apiError(api.CodeBusy, err.Error(), true)
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.SessionID == "" || request.Prompt == "" || request.IdempotencyKey == "" {
		return api.Receipt{}, apiError(api.CodeInvalidArgument, "session_id, prompt, and idempotency_key are required", false)
	}
	if _, err := s.store.Session(ctx, request.SessionID); err != nil {
		return api.Receipt{}, mapStateError(err, "session")
	}
	digest := requestDigest(request.SessionID, request.Prompt)
	key := "api:v1:run.submit:" + request.IdempotencyKey
	if existing, found, err := s.store.EventByIdempotency(ctx, key); err != nil {
		return api.Receipt{}, err
	} else if found {
		return receiptFromQueuedEvent(existing, digest)
	}
	runID, err := storage.NewID()
	if err != nil {
		return api.Receipt{}, err
	}
	messageID, err := storage.NewID()
	if err != nil {
		return api.Receipt{}, err
	}
	payload := sessionlog.RunQueuedPayload{RunID: runID, MessageID: messageID, Content: request.Prompt, RequestDigest: digest}
	record, err := sessionlog.New(sessionlog.EventRunQueued, payload)
	if err != nil {
		return api.Receipt{}, err
	}
	input, err := sessionlog.ToEventInput(request.SessionID, record)
	if err != nil {
		return api.Receipt{}, err
	}
	input = sessionlog.ApplyEventMeta(input, sessionlog.EventMeta{CorrelationID: runID, CausationID: messageID, IdempotencyKey: key})
	stored, err := s.store.AppendEvent(ctx, input)
	if err != nil {
		return api.Receipt{}, err
	}
	receipt, err := receiptFromQueuedEvent(stored, digest)
	if err != nil {
		return api.Receipt{}, err
	}
	s.ensureWorker(request.SessionID)
	return receipt, nil
}

func requestDigest(sessionID, prompt string) string {
	data, _ := json.Marshal(struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
	}{sessionID, prompt})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func receiptFromQueuedEvent(event state.Event, digest string) (api.Receipt, error) {
	payload, err := decodeRecordData[sessionlog.RunQueuedPayload](event)
	if err != nil {
		return api.Receipt{}, err
	}
	if payload.RequestDigest != digest {
		return api.Receipt{}, apiError(api.CodeConflict, "idempotency key was already used with different parameters", false)
	}
	return api.Receipt{Accepted: true, RunID: payload.RunID, MessageID: payload.MessageID, AcceptedCursor: event.Sequence}, nil
}

func decodeRecordData[T any](event state.Event) (T, error) {
	var zero T
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		return zero, err
	}
	var payload T
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		return zero, err
	}
	return payload, nil
}

func (s *Service) ensureWorker(sessionID string) {
	s.mu.Lock()
	if s.closed || s.workers[sessionID] {
		s.mu.Unlock()
		return
	}
	s.workers[sessionID] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.work(sessionID)
}

func (s *Service) work(sessionID string) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.workers, sessionID)
		delete(s.active, sessionID)
		s.mu.Unlock()
	}()
	for {
		queued, ok, err := s.firstPendingRun(s.ctx, sessionID)
		if err != nil || !ok || s.ctx.Err() != nil {
			return
		}
		meta := sessionlog.EventMeta{CorrelationID: queued.RunID, CausationID: queued.MessageID}
		if _, err := s.appendRecord(s.ctx, sessionID, sessionlog.EventRunStart, sessionlog.RunStartPayload{RunID: queued.RunID, MessageID: queued.MessageID}, meta); err != nil {
			return
		}
		s.mu.Lock()
		s.active[sessionID] = queued.RunID
		s.mu.Unlock()

		runCtx := sessionlog.WithEventMeta(s.ctx, meta)
		session, loadErr := agent.LoadSession(s.workspace, sessionID)
		var output string
		runErr := loadErr
		if runErr == nil {
			output, runErr = s.runtimes.RunAccepted(runCtx, session, agent.RunRequest{RunID: queued.RunID, MessageID: queued.MessageID, Content: queued.Content})
		}
		status := "completed"
		if runErr != nil {
			status = "failed"
			if errors.Is(runErr, context.Canceled) {
				status = "cancelled"
			}
		}
		errorText := ""
		if runErr != nil {
			errorText = runErr.Error()
		}
		terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 2*time.Second)
		_, endErr := s.appendRecord(terminalCtx, sessionID, sessionlog.EventRunEnd, sessionlog.RunEndPayload{
			RunID: queued.RunID, MessageID: queued.MessageID, Status: status, Output: output, Error: errorText,
		}, meta)
		cancel()
		s.mu.Lock()
		delete(s.active, sessionID)
		s.mu.Unlock()
		if endErr != nil {
			return
		}
	}
}

func (s *Service) appendRecord(ctx context.Context, sessionID string, kind sessionlog.EventType, data any, meta sessionlog.EventMeta) (state.Event, error) {
	record, err := sessionlog.New(kind, data)
	if err != nil {
		return state.Event{}, err
	}
	input, err := sessionlog.ToEventInput(sessionID, record)
	if err != nil {
		return state.Event{}, err
	}
	return s.store.AppendEvent(ctx, sessionlog.ApplyEventMeta(input, meta))
}

func (s *Service) firstPendingRun(ctx context.Context, sessionID string) (sessionlog.RunQueuedPayload, bool, error) {
	records, err := sessionlog.Load(ctx, s.store, sessionID)
	if err != nil {
		return sessionlog.RunQueuedPayload{}, false, err
	}
	ended := make(map[string]bool)
	for _, record := range records {
		if record.Type == sessionlog.EventRunEnd {
			ended[record.Data.(sessionlog.RunEndPayload).MessageID] = true
		}
	}
	for _, record := range records {
		if record.Type == sessionlog.EventRunQueued {
			queued := record.Data.(sessionlog.RunQueuedPayload)
			if !ended[queued.MessageID] {
				return queued, true, nil
			}
		}
	}
	return sessionlog.RunQueuedPayload{}, false, nil
}

func (s *Service) CancelRun(ctx context.Context, request api.CancelRunRequest) (api.Run, error) {
	if err := s.ready(); err != nil {
		return api.Run{}, apiError(api.CodeBusy, err.Error(), true)
	}
	queued, started, ended, err := s.findRun(ctx, request.SessionID, request.RunID)
	if err != nil {
		return api.Run{}, err
	}
	if ended != nil {
		return runDTO(request.SessionID, *ended), nil
	}
	meta := sessionlog.EventMeta{CorrelationID: queued.RunID, CausationID: queued.MessageID}
	if !started {
		end := sessionlog.RunEndPayload{RunID: queued.RunID, MessageID: queued.MessageID, Status: "cancelled"}
		if _, err := s.appendRecord(ctx, request.SessionID, sessionlog.EventRunEnd, end, meta); err != nil {
			return api.Run{}, err
		}
		return runDTO(request.SessionID, end), nil
	}
	s.mu.Lock()
	active := s.active[request.SessionID] == request.RunID
	s.mu.Unlock()
	if active {
		s.runtimes.CancelSession(request.SessionID)
		return api.Run{RunID: queued.RunID, MessageID: queued.MessageID, SessionID: request.SessionID, Status: "cancelling"}, nil
	}
	return api.Run{}, apiError(api.CodeConflict, "run is not active", false)
}

func (s *Service) findRun(ctx context.Context, sessionID, runID string) (sessionlog.RunQueuedPayload, bool, *sessionlog.RunEndPayload, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runID) == "" {
		return sessionlog.RunQueuedPayload{}, false, nil, apiError(api.CodeInvalidArgument, "session_id and run_id are required", false)
	}
	records, err := sessionlog.Load(ctx, s.store, sessionID)
	if err != nil {
		return sessionlog.RunQueuedPayload{}, false, nil, err
	}
	var queued sessionlog.RunQueuedPayload
	started := false
	var ended *sessionlog.RunEndPayload
	for _, record := range records {
		switch record.Type {
		case sessionlog.EventRunQueued:
			value := record.Data.(sessionlog.RunQueuedPayload)
			if value.RunID == runID {
				queued = value
			}
		case sessionlog.EventRunStart:
			started = started || record.Data.(sessionlog.RunStartPayload).RunID == runID
		case sessionlog.EventRunEnd:
			value := record.Data.(sessionlog.RunEndPayload)
			if value.RunID == runID {
				copy := value
				ended = &copy
			}
		}
	}
	if queued.RunID == "" {
		return queued, false, nil, apiError(api.CodeNotFound, "run not found", false)
	}
	return queued, started, ended, nil
}

func runDTO(sessionID string, payload sessionlog.RunEndPayload) api.Run {
	return api.Run{RunID: payload.RunID, MessageID: payload.MessageID, SessionID: sessionID, Status: payload.Status,
		Output: payload.Output, Error: payload.Error, Recovered: payload.Recovered}
}

func (s *Service) recoverRuns(ctx context.Context) error {
	sessions, err := s.store.Sessions(ctx, false)
	if err != nil {
		return err
	}
	for _, saved := range sessions {
		var session agent.Session
		if json.Unmarshal(saved.Data, &session) != nil || session.Origin == "subagent" {
			continue
		}
		records, err := sessionlog.Load(ctx, s.store, saved.ID)
		if err != nil {
			return err
		}
		started := make(map[string]sessionlog.RunStartPayload)
		ended := make(map[string]bool)
		for _, record := range records {
			switch record.Type {
			case sessionlog.EventRunStart:
				value := record.Data.(sessionlog.RunStartPayload)
				started[value.RunID] = value
			case sessionlog.EventRunEnd:
				ended[record.Data.(sessionlog.RunEndPayload).RunID] = true
			}
		}
		raw, err := s.store.Events(ctx, state.EventQuery{SessionID: saved.ID})
		if err != nil {
			return err
		}
		for runID, start := range started {
			if ended[runID] {
				continue
			}
			status := "interrupted"
			if hasCompletedTurn(raw, runID) {
				status = "completed"
			} else if err := s.interruptRunInteractions(ctx, saved.ID, runID, raw); err != nil {
				return err
			}
			meta := sessionlog.EventMeta{CorrelationID: runID, CausationID: start.MessageID}
			if _, err := s.appendRecord(ctx, saved.ID, sessionlog.EventRunEnd, sessionlog.RunEndPayload{
				RunID: runID, MessageID: start.MessageID, Status: status, Recovered: true,
			}, meta); err != nil {
				return fmt.Errorf("repair run %s: %w", runID, err)
			}
		}
		if _, pending, err := s.firstPendingRun(ctx, saved.ID); err != nil {
			return err
		} else if pending {
			s.ensureWorker(saved.ID)
		}
	}
	return nil
}

func (s *Service) interruptRunInteractions(ctx context.Context, sessionID, runID string, events []state.Event) error {
	requests := make(map[string]sessionlog.InteractionRequestedPayload)
	resolved := make(map[string]bool)
	for _, event := range events {
		if event.CorrelationID != runID {
			continue
		}
		switch event.Type {
		case string(sessionlog.EventInteractionRequest):
			if value, err := decodeRecordData[sessionlog.InteractionRequestedPayload](event); err == nil {
				requests[value.InteractionID] = value
			}
		case string(sessionlog.EventInteractionResolve):
			if value, err := decodeRecordData[sessionlog.InteractionResolvedPayload](event); err == nil {
				resolved[value.InteractionID] = true
			}
		}
	}
	data, _ := json.Marshal(map[string]string{"reason": "backend restarted while interaction was pending"})
	for id, request := range requests {
		if resolved[id] {
			continue
		}
		meta := sessionlog.EventMeta{CorrelationID: runID, CausationID: id}
		if _, err := s.appendRecord(ctx, sessionID, sessionlog.EventInteractionResolve, sessionlog.InteractionResolvedPayload{
			InteractionID: id, RunID: runID, Kind: request.Kind, Status: "interrupted", Data: data,
		}, meta); err != nil {
			return err
		}
	}
	return nil
}

func hasCompletedTurn(events []state.Event, runID string) bool {
	for _, event := range events {
		if event.Type != string(sessionlog.EventTurnEnd) || event.CorrelationID != runID {
			continue
		}
		var envelope struct {
			Data sessionlog.TurnEndPayload `json:"data"`
		}
		if json.Unmarshal(event.Payload, &envelope) == nil && envelope.Data.Reason == "completed" {
			return true
		}
	}
	return false
}

func mapStateError(err error, noun string) error {
	if errors.Is(err, state.ErrConflict) {
		return apiError(api.CodeConflict, noun+" revision changed", false)
	}
	if strings.Contains(strings.ToLower(err.Error()), "no rows") {
		return apiError(api.CodeNotFound, noun+" not found", false)
	}
	return err
}
