package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/storage"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Session holds the persistent state and context of a user chat session.
type Session struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at,omitempty"`
	Status          string             `json:"status,omitempty"`
	Version         int64              `json:"version,omitempty"`
	Provider        string             `json:"provider,omitempty"`
	Model           string             `json:"model,omitempty"`
	NeedsName       bool               `json:"needs_name,omitempty"`
	ActiveTaskID    string             `json:"active_task_id,omitempty"`
	DryRun          bool               `json:"dry_run,omitempty"`
	ApprovalMode    tools.ApprovalMode `json:"approval_mode,omitempty"`
	ParentSessionID string             `json:"parent_session_id,omitempty"`
	Origin          string             `json:"origin,omitempty"`
	DelegationLabel string             `json:"delegation_label,omitempty"`
	DelegationDepth int                `json:"delegation_depth,omitempty"`
	DelegationScope SubagentScope      `json:"delegation_scope,omitempty"`
	Features        *FeatureConfig     `json:"features,omitempty"`
	// LegacyBudget preserves retired persisted settings verbatim so opening and
	// saving an older session does not delete its audit data. It has no runtime
	// semantics.
	LegacyBudget json.RawMessage `json:"budget,omitempty"`

	events            []SessionEvent         `json:"-"`
	surface           SurfaceManager         `json:"-"`
	replay            sessionlog.ReplayState `json:"-"`
	derived           []models.Message       `json:"-"`
	derivedNodes      int                    `json:"-"`
	derivedGeneration uint64                 `json:"-"`
	derivedThisPass   int                    `json:"-"`
	surfaceReady      bool                   `json:"-"`
	planModeActive    uint32                 `json:"-"`
}

// FeatureConfig holds non-planning UI/runtime preferences. Historical planning
// flags are decoded only by the legacy importer, never by a live session.
type FeatureConfig struct {
	Retry RetryConfig `json:"retry,omitempty"`
	UX    UXConfig    `json:"ux,omitempty"`
}

type RetryConfig struct {
	Response *bool `json:"response,omitempty"`
}

type UXConfig struct {
	Checklist *bool `json:"checklist,omitempty"`
	Rewind    *bool `json:"rewind,omitempty"`
}

// ChecklistEnabled defaults the display-only progress protocol on.
func (s *Session) ChecklistEnabled() bool {
	return s == nil || s.Features == nil || s.Features.UX.Checklist == nil || *s.Features.UX.Checklist
}

func (s *Session) SetChecklistEnabled(root string, enabled bool) error {
	if s.Features == nil {
		s.Features = &FeatureConfig{}
	}
	s.Features.UX.Checklist = &enabled
	return s.Save(root)
}

// RewindEnabled defaults file-scoped mutation checkpoints on.
func (s *Session) RewindEnabled() bool {
	return s == nil || s.Features == nil || s.Features.UX.Rewind == nil || *s.Features.UX.Rewind
}

func (s *Session) SetRewindEnabled(root string, enabled bool) error {
	if s.Features == nil {
		s.Features = &FeatureConfig{}
	}
	s.Features.UX.Rewind = &enabled
	return s.Save(root)
}

// ResponseRetryEnabled controls retries for transient provider/API failures.
func (s *Session) ResponseRetryEnabled() bool {
	return s == nil || s.Features == nil || s.Features.Retry.Response == nil || *s.Features.Retry.Response
}

func (s *Session) SetResponseRetryEnabled(root string, enabled bool) error {
	if s.Features == nil {
		s.Features = &FeatureConfig{}
	}
	s.Features.Retry.Response = &enabled
	return s.Save(root)
}

// PlanModeActive reports whether Plan Mode is active on this session based on durable event folding.
func (s *Session) PlanModeActive() bool {
	if s == nil {
		return false
	}
	return atomic.LoadUint32(&s.planModeActive) != 0
}

// Events returns the durable session events.
func (s *Session) Events() []SessionEvent {
	if s == nil {
		return nil
	}
	out := make([]SessionEvent, len(s.events))
	copy(out, s.events)
	return out
}

// setPlanMode appends an EventPlanMode event and refreshes the durable fold.
// Agent owns callers of this transition.
func (s *Session) setPlanMode(ctx context.Context, root string, active bool) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	store, err := state.Open(root)
	if err != nil {
		return err
	}
	event, err := sessionlog.New(EventPlanMode, sessionlog.PlanModePayload{Active: active})
	if err != nil {
		return err
	}
	if err := persistSessionEvent(ctx, store, s.ID, event); err != nil {
		return err
	}
	return s.PullSurface(ctx, store)
}

// LoadOrCreateSession restores a session checkpoint or creates an empty one.
func LoadOrCreateSession(root, id string) (*Session, error) {
	session, err := LoadSession(root, id)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	session = newSession(id, time.Now())
	return session, session.Save(root)
}

// NewSession creates a blank chat with a unique ID and timestamp-based name.
func NewSession(root string) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	session := newSession(id, now)
	return session, session.Save(root)
}

// newSessionID returns an RFC 4122 UUIDv4 identifier.
func newSessionID() (string, error) {
	return storage.NewID()
}

// LoadSession restores an existing session checkpoint.
func LoadSession(root, id string) (*Session, error) {
	return loadSession(root, id, true)
}

// loadSession may inspect a checkpoint without migrating it. Session lists use
// the read-only path so legacy data changes only after an explicit resume.
func loadSession(root, id string, migrate bool) (*Session, error) {
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	store, err := state.Open(root)
	if err != nil {
		return nil, err
	}
	var session Session
	durableState := false
	path := ""
	var legacyData []byte
	legacy := false
	if saved, err := store.Session(context.Background(), id); err == nil {
		if err := json.Unmarshal(saved.Data, &session); err != nil {
			return nil, fmt.Errorf("load durable session: %w", err)
		}
		if session.ID != id {
			return nil, fmt.Errorf("durable session ID %q does not match %q", session.ID, id)
		}
		session.CreatedAt, session.UpdatedAt, session.Status, session.Version = saved.CreatedAt, saved.UpdatedAt, saved.Status, saved.Version
		if session.Provider == "" {
			session.Provider = saved.Provider
		}
		if session.Model == "" {
			session.Model = saved.Model
		}
		if session.Status == "" {
			session.Status = "active"
		}
		durableState = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	} else {
		path = sessionStatePath(root, id)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			path = legacySessionStatePath(root, id)
			data, err = os.ReadFile(path)
			legacy = err == nil
		}
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, fmt.Errorf("load session: %w", err)
		}
		legacyData = data
		if session.ID != id {
			return nil, fmt.Errorf("session checkpoint ID %q does not match %q", session.ID, id)
		}
	}
	changed := legacy && migrate
	if session.CreatedAt.IsZero() {
		if durableState {
			return nil, fmt.Errorf("durable session %q is missing a creation timestamp", id)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		session.CreatedAt = info.ModTime()
		changed = true
	}
	if strings.TrimSpace(session.Name) == "" {
		session.Name = defaultSessionName(session.CreatedAt)
		changed = true
	}
	name, err := validatedSessionName(session.Name)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if name != session.Name {
		session.Name = name
		changed = true
	}
	// Session listing inspects only checkpoint metadata. Importing a task or
	// plan here would turn a read-only list into a legacy migration.
	if !migrate {
		return &session, nil
	}
	if !durableState {
		if err := validateLegacyImport(root, session.ID); err != nil {
			return nil, err
		}
	}
	if migrate && (!durableState || changed) {
		if err := session.Save(root); err != nil {
			return nil, err
		}
	}
	if migrate {
		if err := importLegacyMemory(root, session.ID); err != nil {
			return nil, err
		}
		if path != "" && len(legacyData) > 0 {
			if err := store.RecordLegacyImport(context.Background(), path, legacyData); err != nil {
				return nil, err
			}
		}
	}
	return &session, nil
}

// ListSessions returns saved sessions, newest first.
func ListSessions(root string) ([]Session, error) {
	seen := map[string]bool{}
	var sessions []Session
	store, err := state.Open(root)
	if err != nil {
		return nil, err
	}
	durable, err := store.Sessions(context.Background(), false)
	if err != nil {
		return nil, err
	}
	for _, saved := range durable {
		var session Session
		if err := json.Unmarshal(saved.Data, &session); err != nil {
			return nil, fmt.Errorf("list durable sessions: %w", err)
		}
		session.CreatedAt, session.UpdatedAt, session.Status, session.Version = saved.CreatedAt, saved.UpdatedAt, saved.Status, saved.Version
		seen[session.ID] = true
		sessions = append(sessions, session)
	}
	for _, directory := range []string{sessionDirectory(root), legacySessionDirectory(root)} {
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".state.json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".state.json")
			if seen[id] {
				continue
			}
			session, err := loadSession(root, id, false)
			if err != nil {
				return nil, err
			}
			seen[id] = true
			sessions = append(sessions, *session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

// Save persists the session state alongside its memory checkpoint.
func (s *Session) Save(root string) error {
	if err := validateSessionID(s.ID); err != nil {
		return err
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = time.Now().UTC()
	if s.Status == "" {
		s.Status = "active"
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = defaultSessionName(s.CreatedAt)
	}
	name, err := validatedSessionName(s.Name)
	if err != nil {
		return err
	}
	s.Name = name
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	store, err := state.Open(root)
	if err != nil {
		return err
	}
	saved, err := store.SaveSession(context.Background(), state.SessionInput{ID: s.ID, Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Status: s.Status, CurrentTaskID: s.ActiveTaskID, Provider: s.Provider, Model: s.Model, Data: data, ExpectedVersion: s.Version})
	if err != nil {
		return err
	}
	s.Version, s.UpdatedAt, s.Status = saved.Version, saved.UpdatedAt, saved.Status
	return nil
}

// Rename updates the user-facing session name.
func (s *Session) Rename(root, name string) error {
	name, err := validatedSessionName(name)
	if err != nil {
		return err
	}
	previous := s.Name
	previousNeedsName := s.NeedsName
	s.Name = name
	s.NeedsName = false
	if err := s.Save(root); err != nil {
		s.Name = previous
		s.NeedsName = previousNeedsName
		return err
	}
	return nil
}

func validatedSessionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("session name cannot be empty")
	}
	if utf8.RuneCountInString(name) > 80 {
		return "", fmt.Errorf("session name must be at most 80 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("session name cannot contain control characters")
		}
	}
	return name, nil
}

func sessionStatePath(root, id string) string {
	return filepath.Join(sessionDirectory(root), safeSessionID(id)+".state.json")
}

func legacySessionStatePath(root, id string) string {
	return filepath.Join(legacySessionDirectory(root), safeSessionID(id)+".state.json")
}

func sessionDirectory(root string) string       { return filepath.Join(root, ".sessions") }
func legacySessionDirectory(root string) string { return filepath.Join(root, ".session") }

func newSession(id string, createdAt time.Time) *Session {
	return &Session{ID: id, Name: defaultSessionName(createdAt), CreatedAt: createdAt.UTC(), UpdatedAt: createdAt.UTC(), Status: "active", NeedsName: true, ApprovalMode: tools.ApprovalBatman}
}

func defaultSessionName(createdAt time.Time) string {
	return "Session " + createdAt.Local().Format("2006-01-02 15:04")
}

func validateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session must have an ID")
	}
	if len(id) > 64 {
		return fmt.Errorf("session ID must be at most 64 bytes")
	}
	if safeSessionID(id) != id {
		return fmt.Errorf("session ID may contain only letters, numbers, '-' and '_'")
	}
	return nil
}
