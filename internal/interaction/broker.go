package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/storage"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

var ErrInteractionNotFound = errors.New("interaction is not pending")

type questionWaiter struct {
	sessionID string
	runID     string
	answers   chan questionResult
}

type questionResult struct {
	answers questions.AnswerSet
	err     error
}

// Broker persists human-owned decisions before exposing or releasing them.
type Broker struct {
	store     *state.Store
	mu        sync.Mutex
	questions map[string]*questionWaiter
}

func NewBroker(store *state.Store) *Broker {
	return &Broker{store: store, questions: make(map[string]*questionWaiter)}
}

func (b *Broker) RecordApprovalRequested(ctx context.Context, request tools.ApprovalRequest) error {
	data, err := json.Marshal(map[string]any{"tool": request.Tool, "call_id": request.CallID, "arguments": request.Arguments})
	if err != nil {
		return err
	}
	return b.append(ctx, request.SessionID, sessionlog.EventInteractionRequest, sessionlog.InteractionRequestedPayload{
		InteractionID: request.InteractionID, RunID: request.RunID, Kind: "approval", Data: data,
	}, sessionlog.EventMeta{CorrelationID: request.RunID, CausationID: request.InteractionID})
}

func (b *Broker) RecordApprovalResolved(ctx context.Context, request tools.ApprovalRequest, resolution tools.ApprovalResolution) error {
	data, err := json.Marshal(map[string]any{"decision": resolution.Decision, "reason": resolution.Reason, "input": resolution.Input})
	if err != nil {
		return err
	}
	return b.append(ctx, request.SessionID, sessionlog.EventInteractionResolve, sessionlog.InteractionResolvedPayload{
		InteractionID: request.InteractionID, RunID: request.RunID, Kind: "approval", Status: resolution.Decision, Data: data,
	}, sessionlog.EventMeta{CorrelationID: request.RunID, CausationID: request.InteractionID})
}

func (b *Broker) Ask(ctx context.Context, request questions.Request) (questions.AnswerSet, error) {
	if request.SessionID == "" {
		return questions.AnswerSet{}, errors.New("question session is required")
	}
	id, err := storage.NewID()
	if err != nil {
		return questions.AnswerSet{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return questions.AnswerSet{}, err
	}
	waiter := &questionWaiter{sessionID: request.SessionID, runID: request.RunID, answers: make(chan questionResult, 1)}
	b.mu.Lock()
	b.questions[id] = waiter
	if err := b.append(ctx, request.SessionID, sessionlog.EventInteractionRequest, sessionlog.InteractionRequestedPayload{
		InteractionID: id, RunID: request.RunID, Kind: "question", Data: data,
	}, sessionlog.EventMeta{CorrelationID: request.RunID, CausationID: id}); err != nil {
		delete(b.questions, id)
		b.mu.Unlock()
		return questions.AnswerSet{}, err
	}
	b.mu.Unlock()
	select {
	case result := <-waiter.answers:
		return result.answers, result.err
	case <-ctx.Done():
		terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		resolveErr := b.resolveQuestion(terminalCtx, request.SessionID, id, questions.AnswerSet{}, "cancelled", ctx.Err().Error(), false)
		cancel()
		return questions.AnswerSet{}, errors.Join(ctx.Err(), resolveErr)
	}
}

func (b *Broker) ResolveQuestion(ctx context.Context, sessionID, interactionID string, answers questions.AnswerSet) error {
	return b.resolveQuestion(ctx, sessionID, interactionID, answers, "answered", "", true)
}

func (b *Broker) resolveQuestion(ctx context.Context, sessionID, interactionID string, answers questions.AnswerSet, status, reason string, deliver bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter := b.questions[interactionID]
	if waiter == nil || waiter.sessionID != sessionID {
		return ErrInteractionNotFound
	}
	data, err := json.Marshal(map[string]any{"answers": answers.Answers, "reason": reason})
	if err != nil {
		return err
	}
	if err := b.append(ctx, sessionID, sessionlog.EventInteractionResolve, sessionlog.InteractionResolvedPayload{
		InteractionID: interactionID, RunID: waiter.runID, Kind: "question", Status: status, Data: data,
	}, sessionlog.EventMeta{CorrelationID: waiter.runID, CausationID: interactionID}); err != nil {
		return err
	}
	delete(b.questions, interactionID)
	if deliver {
		waiter.answers <- questionResult{answers: answers}
	}
	return nil
}

func (b *Broker) append(ctx context.Context, sessionID string, kind sessionlog.EventType, data any, meta sessionlog.EventMeta) error {
	if b == nil || b.store == nil {
		return fmt.Errorf("interaction store is required")
	}
	record, err := sessionlog.New(kind, data)
	if err != nil {
		return err
	}
	input, err := sessionlog.ToEventInput(sessionID, record)
	if err != nil {
		return err
	}
	_, err = b.store.AppendEvent(ctx, sessionlog.ApplyEventMeta(input, meta))
	return err
}
