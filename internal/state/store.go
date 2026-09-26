package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/storage"
)

var ErrResyncRequired = errors.New("event subscriber fell behind; fetch a new snapshot")

var repositories = struct {
	sync.Mutex
	byRoot map[string]*Store
}{byRoot: make(map[string]*Store)}

// Store is the workspace-local, in-memory implementation of Repository.
// ponytail: all state is ephemeral; lost on process exit by design.
type Store struct {
	root        string
	objects     string
	workspaceID string

	mu sync.RWMutex

	events       []Event
	nextEventSeq int64
	sessions     map[string]Session
	messages     map[string][]Message // sessionID → messages
	artifacts    map[string]Artifact
	documents    map[string]Document      // key: kind:id
	files        map[string]string        // path → fileID
	fileVersions map[string][]FileVersion // path → versions

	workspaceMemory   string
	workspaceRevision *WorkspaceRevision

	// ponytail: subscribers use map+id for O(1) remove; upgrade to
	// sharded fan-out only if profiling shows publish contention.
	subscribers map[uint64]*eventSubscriber
	nextSubID   uint64
}

type eventSubscriber struct {
	id     uint64
	query  EventQuery
	events chan Event
	mu     sync.Mutex
	err    error
	closed bool
	store  *Store
}

// EventSubscription streams committed events. Initial contains the
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

var _ Repository = (*Store)(nil)

func Open(root string) (*Store, error) { return OpenContext(context.Background(), root) }

func OpenContext(ctx context.Context, root string) (*Store, error) {
	clean, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	repositories.Lock()
	defer repositories.Unlock()
	if existing := repositories.byRoot[clean]; existing != nil {
		return existing, nil
	}
	store, err := open(ctx, clean)
	if err != nil {
		return nil, err
	}
	repositories.byRoot[clean] = store
	return store, nil
}

func CloseWorkspace(root string) error {
	clean, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	repositories.Lock()
	store := repositories.byRoot[clean]
	delete(repositories.byRoot, clean)
	repositories.Unlock()
	if store == nil {
		return nil
	}
	store.closeSubscriptions()

	store.mu.Lock()
	store.events = nil
	store.sessions = nil
	store.messages = nil
	store.artifacts = nil
	store.documents = nil

	store.files = nil
	store.fileVersions = nil
	store.mu.Unlock()

	if store.objects != "" {
		_ = os.RemoveAll(store.objects)
	}
	return nil
}

func canonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("workspace root is required")
	}
	clean, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Clean(clean), nil
}

func open(_ context.Context, root string) (*Store, error) {
	clean, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(clean))
	workspaceID := "ws-" + hex.EncodeToString(hash[:8])

	objects, err := os.MkdirTemp("", "supremo-artifacts-*")
	if err != nil {
		return nil, fmt.Errorf("create temp objects directory: %w", err)
	}

	return &Store{
		root:         clean,
		objects:      objects,
		workspaceID:  workspaceID,
		sessions:     make(map[string]Session),
		messages:     make(map[string][]Message),
		artifacts:    make(map[string]Artifact),
		documents:    make(map[string]Document),
		files:        make(map[string]string),
		fileVersions: make(map[string][]FileVersion),
		subscribers:  make(map[uint64]*eventSubscriber),
	}, nil
}

func (s *Store) WorkspaceID() string { return s.workspaceID }
func (s *Store) Root() string        { return s.root }

func (s *Store) RecordLegacyImport(_ context.Context, path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := s.createEventLocked(EventInput{
		Type: "legacy.imported", Payload: data,
	})
	s.events = append(s.events, event)
	s.publishCommittedLocked([]Event{event})
	return nil
}

// --- Events ---

func (s *Store) AppendEvent(_ context.Context, input EventInput) (Event, error) {
	if input.Type == "" {
		return Event{}, errors.New("event type is required")
	}
	payload, err := jsonBytes(input.Payload)
	if err != nil {
		return Event{}, err
	}
	id := input.ID
	if id == "" {
		id, err = newID()
		if err != nil {
			return Event{}, err
		}
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		return Event{}, errors.New("store closed")
	}
	// Idempotency check
	if input.IdempotencyKey != "" {
		for _, e := range s.events {
			if e.IdempotencyKey == input.IdempotencyKey {
				return e, nil
			}
		}
	}
	s.nextEventSeq++
	event := Event{
		Sequence: s.nextEventSeq, ID: id, WorkspaceID: s.workspaceID,
		SessionID: input.SessionID, AgentID: input.AgentID, Type: input.Type,
		CorrelationID: input.CorrelationID, CausationID: input.CausationID,
		IdempotencyKey: input.IdempotencyKey, PayloadVersion: input.PayloadVersion,
		Payload: payload, CreatedAt: createdAt,
	}
	s.events = append(s.events, event)
	s.publishCommittedLocked([]Event{event})
	return event, nil
}

func (s *Store) Events(_ context.Context, query EventQuery) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filterEventsLocked(query), nil
}

func (s *Store) filterEventsLocked(query EventQuery) []Event {
	var result []Event
	for _, e := range s.events {
		if query.SessionID != "" && e.SessionID != query.SessionID {
			continue
		}
		if query.Type != "" && e.Type != query.Type {
			continue
		}
		if query.After > 0 && e.Sequence <= query.After {
			continue
		}
		result = append(result, e)
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}
	return result
}

// --- Subscriptions ---

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
	initial := s.filterEventsLocked(replayQuery)
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

// --- Sessions ---

func (s *Store) SaveSession(_ context.Context, input SessionInput) (Session, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return Session{}, err
		}
	}
	now := time.Now().UTC()
	if input.CreatedAt.IsZero() {
		input.CreatedAt = now
	}
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = now
	}
	status := input.Status
	if status == "" {
		status = "active"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		return Session{}, errors.New("store closed")
	}
	existing, exists := s.sessions[input.ID]
	var version int64
	if exists {
		if input.ExpectedVersion != 0 && input.ExpectedVersion != existing.Version {
			return Session{}, ErrConflict
		}
		version = existing.Version
		input.CreatedAt = existing.CreatedAt
	}
	version++
	session := Session{
		ID: input.ID, WorkspaceID: s.workspaceID, Name: input.Name,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt, Status: status,
		CurrentTaskID: input.CurrentTaskID, Provider: input.Provider, Model: input.Model,
		Metadata: input.Metadata, Data: input.Data, Version: version,
	}
	s.sessions[input.ID] = session

	var eventsToAppend []Event
	for _, re := range input.RelatedEvents {
		if re.Type != "" {
			eventsToAppend = append(eventsToAppend, s.createEventLocked(re))
		}
	}
	if len(eventsToAppend) > 0 {
		s.events = append(s.events, eventsToAppend...)
		s.publishCommittedLocked(eventsToAppend)
	}

	return session, nil
}

func (s *Store) Session(_ context.Context, id string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *Store) Sessions(_ context.Context, includeArchived bool) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sessions []Session
	for _, session := range s.sessions {
		if !includeArchived && session.Status == "archived" {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	return sessions, nil
}

func (s *Store) ArchiveSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		return errors.New("store closed")
	}
	session, ok := s.sessions[id]
	if !ok {
		return ErrNotFound
	}
	session.Status = "archived"
	session.UpdatedAt = time.Now().UTC()
	session.Version++
	s.sessions[id] = session
	return nil
}

func (s *Store) AppendMessage(_ context.Context, input MessageInput) (Message, error) {
	if input.SessionID == "" {
		return Message{}, errors.New("session_id is required")
	}
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return Message{}, err
		}
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	state := input.State
	if state == "" {
		state = "active"
	}
	var parts []MessagePart
	for i, p := range input.Parts {
		parts = append(parts, MessagePart{Ordinal: i, Kind: p.Kind, Text: p.Text, ArtifactID: p.ArtifactID, Metadata: p.Metadata})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.messages == nil {
		return Message{}, errors.New("store closed")
	}
	seq := int64(len(s.messages[input.SessionID])) + 1
	msg := Message{
		ID: input.ID, SessionID: input.SessionID, Sequence: seq, Role: input.Role,
		TaskID: input.TaskID, State: state, ParentID: input.ParentID,
		CreatedAt: createdAt, Parts: parts,
	}
	s.messages[input.SessionID] = append(s.messages[input.SessionID], msg)

	var eventsToAppend []Event
	if input.Event.Type != "" {
		eventsToAppend = append(eventsToAppend, s.createEventLocked(input.Event))
	}
	for _, re := range input.RelatedEvents {
		if re.Type != "" {
			eventsToAppend = append(eventsToAppend, s.createEventLocked(re))
		}
	}
	if len(eventsToAppend) > 0 {
		s.events = append(s.events, eventsToAppend...)
		s.publishCommittedLocked(eventsToAppend)
	}

	return msg, nil
}

func (s *Store) createEventLocked(input EventInput) Event {
	id := input.ID
	if id == "" {
		id, _ = newID()
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.nextEventSeq++
	payload, _ := jsonBytes(input.Payload)
	return Event{
		Sequence: s.nextEventSeq, ID: id, WorkspaceID: s.workspaceID,
		SessionID: input.SessionID, AgentID: input.AgentID, Type: input.Type,
		CorrelationID: input.CorrelationID, CausationID: input.CausationID,
		IdempotencyKey: input.IdempotencyKey, PayloadVersion: input.PayloadVersion,
		Payload: payload, CreatedAt: createdAt,
	}
}

func (s *Store) Messages(_ context.Context, sessionID string, includeArchived bool) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.messages[sessionID]
	if !includeArchived {
		var filtered []Message
		for _, m := range msgs {
			if m.State != "archived" {
				filtered = append(filtered, m)
			}
		}
		return filtered, nil
	}
	result := make([]Message, len(msgs))
	copy(result, msgs)
	return result, nil
}

func (s *Store) ArchiveMessages(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.messages[sessionID]
	for i := range msgs {
		msgs[i].State = "archived"
	}
	return nil
}

// --- Workspace ---

func (s *Store) ObserveWorkspace(_ context.Context, snap WorkspaceSnapshot) (WorkspaceRevision, error) {
	id, err := newID()
	if err != nil {
		return WorkspaceRevision{}, err
	}
	observedAt := snap.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	rev := WorkspaceRevision{
		ID: id, WorkspaceID: s.workspaceID, Head: snap.Head, Branch: snap.Branch,
		Dirty: snap.Dirty, Metadata: snap.Metadata, ObservedAt: observedAt,
	}
	s.mu.Lock()
	s.workspaceRevision = &rev
	s.mu.Unlock()
	return rev, nil
}

func (s *Store) WorkspaceMemory(_ context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workspaceMemory, nil
}

func (s *Store) SetWorkspaceMemory(_ context.Context, content string) error {
	s.mu.Lock()
	s.workspaceMemory = content
	s.mu.Unlock()
	return nil
}

// --- Repository index stubs (no-op in ephemeral store) ---

func (s *Store) RepositoryFiles(_ context.Context) ([]RepositoryFileState, error) {
	return nil, nil
}

func (s *Store) LatestRepositoryRevision(_ context.Context) (RepositoryRevision, error) {
	return RepositoryRevision{}, nil
}

func (s *Store) ApplyRepositoryFile(_ context.Context, _ RepositoryFileInput) (RepositoryFileState, error) {
	return RepositoryFileState{}, nil
}

// --- Missing methods (were in deleted session.go / events.go / snapshot.go) ---

// SessionSnapshot is one consistent frontend baseline.
type SessionSnapshot struct {
	Session  Session
	Messages []Message
	Events   []Event
	Cursor   int64
}

func (s *Store) EventByIdempotency(_ context.Context, key string) (Event, bool, error) {
	if key == "" {
		return Event{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.events {
		if e.IdempotencyKey == key {
			return e, true, nil
		}
	}
	return Event{}, false, nil
}

func (s *Store) Cursor(_ context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextEventSeq, nil
}

func (s *Store) SessionSnapshot(ctx context.Context, sessionID string) (SessionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return SessionSnapshot{}, errors.New("session not found")
	}
	var messages []Message
	for _, m := range s.messages[sessionID] {
		if m.State != "archived" {
			messages = append(messages, m)
		}
	}
	var events []Event
	for _, e := range s.events {
		if e.SessionID == sessionID {
			events = append(events, e)
		}
	}
	return SessionSnapshot{Session: session, Messages: messages, Events: events, Cursor: s.nextEventSeq}, nil
}

// --- Utilities ---

func newID() (string, error) {
	return storage.NewID()
}

func jsonBytes(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		if len(raw) == 0 {
			return []byte("{}"), nil
		}
		if !json.Valid(raw) {
			return nil, errors.New("invalid JSON payload")
		}
		return raw, nil
	}
	return json.Marshal(value)
}
