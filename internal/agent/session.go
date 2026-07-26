package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/storage"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Session holds the persistent state and context of a user chat session.
type Session struct {
	ID            string             `json:"id"`
	CurrentPlanID string             `json:"current_plan_id,omitempty"`
	PlanMode      bool               `json:"plan_mode,omitempty"`
	DryRun        bool               `json:"dry_run,omitempty"`
	ApprovalMode  tools.ApprovalMode `json:"approval_mode,omitempty"`
}

// LoadOrCreateSession restores a session checkpoint or creates an empty one.
func LoadOrCreateSession(root, id string) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("session must have an ID")
	}
	path := sessionStatePath(root, id)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		session := &Session{ID: id}
		return session, session.Save(root)
	}
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return &session, nil
}

// Save persists the session state alongside its memory checkpoint.
func (s *Session) Save(root string) error {
	if s.ID == "" {
		return fmt.Errorf("session must have an ID")
	}
	if err := os.MkdirAll(filepath.Join(root, ".session"), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(sessionStatePath(root, s.ID), data, 0600)
}

// SetPlan persists a plan and marks it active for this session.
func (s *Session) SetPlan(root string, plan *Plan) error {
	if err := SavePlan(root, plan); err != nil {
		return err
	}
	previousID := s.CurrentPlanID
	s.CurrentPlanID = plan.ID
	if err := s.Save(root); err != nil {
		s.CurrentPlanID = previousID
		return err
	}
	return nil
}

// ActivePlan loads the session's active plan.
func (s *Session) ActivePlan(root string) (*Plan, error) {
	if s.CurrentPlanID == "" {
		return nil, nil
	}
	return LoadPlan(root, s.CurrentPlanID)
}

func sessionStatePath(root, id string) string {
	return filepath.Join(root, ".session", safeSessionID(id)+".state.json")
}
