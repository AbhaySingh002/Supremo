package state

import (
	"context"
	"errors"
	"sync"
)

var ErrResyncRequired = errors.New("event subscriber fell behind; fetch a new snapshot")

type eventSubscriber struct {
	id     uint64
	query  EventQuery
	events chan Event
	mu     sync.Mutex
	err    error
	closed bool
	store  *Store
}

// EventSubscription streams only committed events. Initial contains the
// atomic replay collected before live delivery was registered.
type EventSubscription struct {
	Initial []Event
	Events  <-chan Event
	sub     *eventSubscriber
	cancel  context.CancelFunc
}

func (s *EventSubscription) Close() {
	if s == nil || s.sub == nil || s.sub.store == nil {
		return
	}
	store := s.sub.store
	if s.cancel != nil {
		s.cancel()
	}
	store.mu.Lock()
	store.removeSubscriberLocked(s.sub, nil)
	store.mu.Unlock()
}

func (s *EventSubscription) Err() error {
	if s == nil || s.sub == nil {
		return nil
	}
	s.sub.mu.Lock()
	defer s.sub.mu.Unlock()
	return s.sub.err
}

// SubscribeEvents atomically replays events after the requested cursor and
// registers live delivery.
func (s *Store) SubscribeEvents(ctx context.Context, query EventQuery, buffer, maxReplay int) (*EventSubscription, error) {
	if buffer <= 0 {
		buffer = 256
	}
	if maxReplay <= 0 {
		maxReplay = 2_000
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	replayQuery := query
	replayQuery.Limit = maxReplay + 1
	initial, err := s.eventsLocked(ctx, replayQuery)
	if err != nil {
		s.mu.Unlock()
		cancel()
		return nil, err
	}
	if len(initial) > maxReplay {
		s.mu.Unlock()
		cancel()
		return nil, ErrResyncRequired
	}
	s.nextSubID++
	sub := &eventSubscriber{id: s.nextSubID, query: query, events: make(chan Event, buffer), store: s}
	s.subscribers[sub.id] = sub
	s.mu.Unlock()

	result := &EventSubscription{Initial: initial, Events: sub.events, sub: sub, cancel: cancel}
	go func() {
		<-ctx.Done()
		result.Close()
	}()
	return result, nil
}

func (s *Store) eventsLocked(ctx context.Context, query EventQuery) ([]Event, error) {
	args := []any{s.workspaceID}
	statement := `SELECT sequence, event_id, workspace_id, COALESCE(session_id, ''), COALESCE(agent_id, ''), type,
		COALESCE(correlation_id, ''), COALESCE(causation_id, ''), COALESCE(idempotency_key, ''), payload_version, payload, created_at
		FROM events WHERE workspace_id = ?`
	if query.SessionID != "" {
		statement += " AND session_id = ?"
		args = append(args, query.SessionID)
	}
	if query.Type != "" {
		statement += " AND type = ?"
		args = append(args, query.Type)
	}
	if query.After > 0 {
		statement += " AND sequence > ?"
		args = append(args, query.After)
	}
	statement += " ORDER BY sequence"
	if query.Limit > 0 {
		statement += " LIMIT ?"
		args = append(args, query.Limit)
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) publishCommittedLocked(events []Event) {
	for _, event := range events {
		for _, sub := range s.subscribers {
			if sub.query.SessionID != "" && sub.query.SessionID != event.SessionID {
				continue
			}
			if sub.query.Type != "" && sub.query.Type != event.Type {
				continue
			}
			select {
			case sub.events <- event:
			default:
				s.removeSubscriberLocked(sub, ErrResyncRequired)
			}
		}
	}
}

func (s *Store) removeSubscriberLocked(sub *eventSubscriber, err error) {
	if sub == nil {
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	sub.err = err
	delete(s.subscribers, sub.id)
	close(sub.events)
}

func (s *Store) closeSubscriptions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subscribers {
		s.removeSubscriberLocked(sub, nil)
	}
}
