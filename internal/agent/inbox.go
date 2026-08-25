package agent

import (
	"context"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// TurnRequest is one primary next-turn unit of work.
type TurnRequest struct {
	Session *Session
	Message models.Message
	Config  turnConfig
	ctx     context.Context
	done    chan TurnResult
}

// TurnResult is delivered to the TurnRequest that started the Turn.
type TurnResult struct {
	Text    string
	Blocked bool
	Err     error
}

type turnConfig struct {
	makeRequest func() ContextRequest
	stream      bool
	taskID      string
	sideAnswer  bool
}

// Inbox holds pending next-turn and next-step work.
type Inbox struct {
	mu       sync.Mutex
	nextTurn []*TurnRequest
	nextStep []models.Message
}

func newTurnRequest(session *Session, msg models.Message, cfg turnConfig) *TurnRequest {
	return &TurnRequest{Session: session, Message: msg, Config: cfg, done: make(chan TurnResult, 1)}
}

func (r *TurnRequest) complete(result TurnResult) {
	if r == nil || r.done == nil {
		return
	}
	select {
	case r.done <- result:
	default:
	}
}

func (r *TurnRequest) wait() TurnResult {
	if r == nil || r.done == nil {
		return TurnResult{}
	}
	return <-r.done
}

func (in *Inbox) EnqueueTurn(req *TurnRequest) {
	in.mu.Lock()
	in.nextTurn = append(in.nextTurn, req)
	in.mu.Unlock()
}

func (in *Inbox) StageNextStep(message models.Message) {
	in.mu.Lock()
	in.nextStep = append(in.nextStep, message)
	in.mu.Unlock()
}

func (in *Inbox) ClaimTurn() *TurnRequest {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.nextTurn) == 0 {
		return nil
	}
	req := in.nextTurn[0]
	in.nextTurn = in.nextTurn[1:]
	return req
}

func (in *Inbox) ClaimNextStep() []models.Message {
	in.mu.Lock()
	defer in.mu.Unlock()
	msgs := in.nextStep
	in.nextStep = nil
	return msgs
}

func (in *Inbox) HasNextStep() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.nextStep) > 0
}
