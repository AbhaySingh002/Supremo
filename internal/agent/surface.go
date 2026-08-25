package agent

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

// Session-event vocabulary and encoding live in sessionlog. These aliases
// preserve the agent's internal call sites while avoiding a parallel contract.
type EventType = sessionlog.EventType

const (
	EventUserMessage       = sessionlog.EventUserMessage
	EventAssistantMessage  = sessionlog.EventAssistantMessage
	EventToolResult        = sessionlog.EventToolResult
	EventTurnStart         = sessionlog.EventTurnStart
	EventTurnEnd           = sessionlog.EventTurnEnd
	EventStepStart         = sessionlog.EventStepStart
	EventStepEnd           = sessionlog.EventStepEnd
	EventRequestHeader     = sessionlog.EventRequestHeader
	EventRequestContext    = sessionlog.EventRequestContext
	EventAssistantChunk    = sessionlog.EventAssistantChunk
	EventUsage             = sessionlog.EventUsage
	EventFinish            = sessionlog.EventFinish
	EventError             = sessionlog.EventError
	EventRetry             = sessionlog.EventRetry
	EventToolCall          = sessionlog.EventToolCall
	EventTodoWrite         = sessionlog.EventTodoWrite
	EventPlanMode          = sessionlog.EventPlanMode
	EventCompactionPrune   = sessionlog.EventCompactionPrune
	EventCompactionStart   = sessionlog.EventCompactionStart
	EventCompactionSummary = sessionlog.EventCompactionSummary
	EventCompactionEnd     = sessionlog.EventCompactionEnd
	surfaceOpAppend        = sessionlog.SurfaceAppend
	surfaceOpReplace       = sessionlog.SurfaceReplace
)

type SurfaceOp = sessionlog.SurfaceOp
type SessionEvent = sessionlog.Record

// SurfaceManager is the replay-owned model-visible surface.
type SurfaceManager = sessionlog.ModelSurface

func newSurfaceManager() SurfaceManager { return sessionlog.NewModelSurface() }

func deriveEventMessage(event SessionEvent) (*models.Message, bool) {
	return sessionlog.MessageForEvent(event)
}

func eventTypeForRole(role models.Role) (EventType, bool) {
	switch role {
	case models.RoleUser:
		return EventUserMessage, true
	case models.RoleAssistant:
		return EventAssistantMessage, true
	case models.RoleTool:
		return EventToolResult, true
	default:
		return "", false
	}
}

func appendSessionEvent(ctx context.Context, store state.EventStore, sessionID string, event SessionEvent) (SessionEvent, error) {
	return sessionlog.Append(ctx, store, sessionID, event)
}

func persistSessionEvent(ctx context.Context, store state.EventStore, sessionID string, event SessionEvent) error {
	_, err := appendSessionEvent(ctx, store, sessionID, event)
	return err
}

// appendAndApplySessionEvent keeps live and recovered sessions on the exact
// same durable sequence. Ephemeral test sessions retain a local monotonic key.
func appendAndApplySessionEvent(ctx context.Context, store state.EventStore, session *Session, event SessionEvent) error {
	if session == nil {
		return fmt.Errorf("session is required")
	}
	if store != nil {
		stored, err := appendSessionEvent(ctx, store, session.ID, event)
		if err != nil {
			return err
		}
		event = stored
	} else {
		if len(session.events) == 0 {
			event.Seq = 0
		} else {
			event.Seq = session.events[len(session.events)-1].Seq + 1
		}
	}
	return session.applyEvent(event)
}

func loadSessionEvents(ctx context.Context, store state.EventStore, sessionID string) ([]SessionEvent, error) {
	return sessionlog.Load(ctx, store, sessionID)
}

// AttachSurface rebuilds the model-visible surface from durable session events.
func (s *Session) AttachSurface(ctx context.Context, store state.EventStore) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	events, err := loadSessionEvents(ctx, store, s.ID)
	if err != nil {
		return err
	}
	replay, err := sessionlog.Replay(events)
	if err != nil {
		return err
	}
	s.events = replay.Events
	s.surface = replay.Surface
	s.replay = replay
	atomic.StoreUint32(&s.planModeActive, boolUint32(replay.PlanModeActive))
	s.derived = nil
	s.derivedNodes = 0
	s.derivedGeneration = replay.Surface.Generation()
	s.surfaceReady = true
	return nil
}

func (s *Session) ensureSurface() {
	if s.surfaceReady {
		return
	}
	s.surface = newSurfaceManager()
	s.surfaceReady = true
}

func (s *Session) applyEvent(event SessionEvent) error {
	s.ensureSurface()
	if err := s.surface.Apply(event); err != nil {
		return err
	}
	s.events = append(s.events, event)
	replay, err := sessionlog.Replay(s.events)
	if err != nil {
		return err
	}
	s.replay = replay
	atomic.StoreUint32(&s.planModeActive, boolUint32(replay.PlanModeActive))
	return nil
}

func boolUint32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

// PullSurface applies durable events appended after the last folded seq.
func (s *Session) PullSurface(ctx context.Context, store state.EventStore) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	events, err := loadSessionEvents(ctx, store, s.ID)
	if err != nil {
		return err
	}
	s.ensureSurface()
	for _, event := range events {
		if event.Seq <= s.surface.LastSeq() {
			continue
		}
		if err := s.applyEvent(event); err != nil {
			return err
		}
	}
	s.surfaceReady = true
	return nil
}

// DeriveMessages returns the model-visible conversation from the active surface.
func (s *Session) DeriveMessages() []models.Message {
	if s == nil {
		return nil
	}
	s.ensureSurface()
	s.derivedThisPass = 0
	if s.surface.Generation() != s.derivedGeneration {
		s.derived = s.derived[:0]
		s.derivedNodes = 0
		s.derivedGeneration = s.surface.Generation()
	}
	nodes := s.surface.Nodes()
	for i := s.derivedNodes; i < len(nodes); i++ {
		event, ok := s.eventBySeq(nodes[i])
		if !ok {
			continue
		}
		s.derivedThisPass++
		msg, ok := deriveEventMessage(event)
		if !ok {
			continue
		}
		s.derived = append(s.derived, *msg)
	}
	s.derivedNodes = len(nodes)
	out := make([]models.Message, len(s.derived))
	copy(out, s.derived)
	return out
}

func (s *Session) eventBySeq(seq int64) (SessionEvent, bool) {
	if seq >= 0 && int(seq) < len(s.events) && s.events[seq].Seq == seq {
		return s.events[seq], true
	}
	for _, event := range s.events {
		if event.Seq == seq {
			return event, true
		}
	}
	return SessionEvent{}, false
}

func (s *Session) Nodes() []int64 {
	if s == nil {
		return nil
	}
	return s.surface.Nodes()
}
