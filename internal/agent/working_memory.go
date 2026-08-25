package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

const (
	WorkingMemorySchemaVersion = 1
	workingMemoryDocumentKind  = "working_memory"
)

// CurrentFocus records the latest turn's established progress, active goal, and evidence.
type CurrentFocus struct {
	Established      string   `json:"established,omitempty"`
	NextGoal         string   `json:"next_goal,omitempty"`
	Evidence         []string `json:"evidence,omitempty"`
	LastAction       string   `json:"last_action,omitempty"`
	EvidenceStatus   string   `json:"evidence_status,omitempty"`
	PreviousStrategy string   `json:"previous_strategy,omitempty"`
	LastFailure      string   `json:"last_failure,omitempty"`
}

// WorkingMemory represents the structured, compact working state of an agent
// during long-running tasks. It is model working memory, not ground truth.
type WorkingMemory struct {
	SchemaVersion           int           `json:"schema_version"`
	SessionID               string        `json:"session_id"`
	TaskID                  string        `json:"task_id,omitempty"`
	Generation              int64         `json:"generation"`
	Objective               string        `json:"objective"`
	ActiveStepID            string        `json:"active_step_id,omitempty"`
	ActiveStepObjective     string        `json:"active_step_objective,omitempty"`
	UserRequirements        []string      `json:"user_requirements,omitempty"`
	HardConstraints         []string      `json:"hard_constraints,omitempty"`
	AcceptedDecisions       []string      `json:"accepted_decisions,omitempty"`
	UnresolvedQuestions     []string      `json:"unresolved_questions,omitempty"`
	CompletedStepSummaries  []string      `json:"completed_step_summaries,omitempty"`
	CurrentWork             string        `json:"current_work,omitempty"`
	CurrentFocus            *CurrentFocus `json:"current_focus,omitempty"`
	RemainingWork           []string      `json:"remaining_work,omitempty"`
	KnownRepositoryFacts    []string      `json:"known_repository_facts,omitempty"`
	ImportantObservations   []string      `json:"important_observations,omitempty"`
	NegativeObservations    []string      `json:"negative_observations,omitempty"`
	ImportantFailures       []string      `json:"important_failures,omitempty"`
	LatestVerificationState string        `json:"latest_verification_state,omitempty"`
	RelevantFilesSymbols    []string      `json:"relevant_files_symbols,omitempty"`
	EvidenceArtifactIDs     []string      `json:"evidence_artifact_ids,omitempty"`
	WorkspaceRevision       string        `json:"workspace_revision,omitempty"`
	NextIntendedActions     []string      `json:"next_intended_actions,omitempty"`
	CompactSummary          string        `json:"compact_summary,omitempty"`
	UpdatedAt               time.Time     `json:"updated_at"`
}

// ApplyDirectives applies advisory model memory updates to the working memory state.
// Directives affect working memory prioritization and never delete durable state.
func (w *WorkingMemory) ApplyDirectives(directives []models.MemoryDirective) {
	for _, dir := range directives {
		key := strings.TrimSpace(dir.Key)
		stmt := strings.TrimSpace(dir.Statement)
		if key == "" && stmt == "" {
			continue
		}
		item := key
		if stmt != "" {
			if key != "" {
				item = key + ": " + stmt
			} else {
				item = stmt
			}
		}
		switch strings.ToLower(dir.Operation) {
		case "retain":
			w.KnownRepositoryFacts = appendBounded(w.KnownRepositoryFacts, item, 24)
			for _, ev := range dir.Evidence {
				w.EvidenceArtifactIDs = appendBounded(w.EvidenceArtifactIDs, ev, 48)
			}
		case "release":
			filtered := make([]string, 0, len(w.KnownRepositoryFacts))
			for _, fact := range w.KnownRepositoryFacts {
				if !strings.HasPrefix(fact, key+":") && !strings.Contains(fact, key) {
					filtered = append(filtered, fact)
				}
			}
			w.KnownRepositoryFacts = filtered
		case "supersede":
			found := false
			for i, fact := range w.KnownRepositoryFacts {
				if strings.HasPrefix(fact, key+":") || strings.Contains(fact, key) {
					w.KnownRepositoryFacts[i] = item
					found = true
					break
				}
			}
			if !found {
				w.KnownRepositoryFacts = appendBounded(w.KnownRepositoryFacts, item, 24)
			}
		}
	}
}

func (w *WorkingMemory) UpdateFocusAfterTurn(progress *models.TurnProgress, lastAction string, newEvidence bool, lastFailure string) {
	if w == nil {
		return
	}
	if w.CurrentFocus == nil {
		w.CurrentFocus = &CurrentFocus{}
	}
	f := w.CurrentFocus
	if lastFailure != "" {
		f.LastFailure = lastFailure
	}
	if lastAction != "" {
		f.LastAction = lastAction
	}
	if !newEvidence && lastAction != "" {
		f.EvidenceStatus = "unchanged"
		f.PreviousStrategy = lastAction
		if progress != nil && progress.NextGoal != "" {
			f.NextGoal = progress.NextGoal
		}
		return
	}
	f.EvidenceStatus = "new"
	f.PreviousStrategy = ""
	if progress == nil {
		return
	}
	if progress.Progress != "" {
		f.Established = progress.Progress
	}
	if progress.NextGoal != "" {
		f.NextGoal = progress.NextGoal
	}
	if len(progress.EvidenceUsed) > 0 {
		f.Evidence = progress.EvidenceUsed
	}
}

func (a *Agent) persistFocusAfterTurn(ctx context.Context, sessionID, taskID string, progress *models.TurnProgress, lastAction string, newEvidence bool, lastFailure string) {
	wmMgr := a.WorkingMemory()
	if wmMgr == nil {
		return
	}
	wm, err := wmMgr.Load(ctx, sessionID, taskID)
	if err != nil {
		return
	}
	if wm == nil {
		wm = &WorkingMemory{SessionID: sessionID, TaskID: taskID}
	}
	wm.UpdateFocusAfterTurn(progress, lastAction, newEvidence, lastFailure)
	if progress != nil && len(progress.MemoryUpdates) > 0 {
		wm.ApplyDirectives(progress.MemoryUpdates)
	}
	_ = wmMgr.Save(ctx, wm)
}

// WorkingMemoryManager coordinates compaction and post-compaction re-grounding.
type WorkingMemoryManager struct {
	store state.Repository
}

func NewWorkingMemoryManager(store state.Repository) *WorkingMemoryManager {
	return &WorkingMemoryManager{store: store}
}

func workingMemoryDocumentID(sessionID, taskID string) string {
	if taskID != "" {
		return "working-memory:" + sessionID + ":" + taskID
	}
	return "working-memory:" + sessionID
}

// Load retrieves the active working memory checkpoint for a task or session.
func (m *WorkingMemoryManager) Load(ctx context.Context, sessionID, taskID string) (*WorkingMemory, error) {
	if m.store == nil || sessionID == "" {
		return nil, errors.New("state store and session id required")
	}
	doc, err := m.store.Document(ctx, workingMemoryDocumentKind, workingMemoryDocumentID(sessionID, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var memory WorkingMemory
	if err := json.Unmarshal(doc.Payload, &memory); err != nil {
		return nil, fmt.Errorf("unmarshal working memory: %w", err)
	}
	return &memory, nil
}

// Save persists a working memory checkpoint.
func (m *WorkingMemoryManager) Save(ctx context.Context, memory *WorkingMemory) error {
	if m.store == nil || memory == nil || memory.SessionID == "" {
		return errors.New("state store and valid working memory required")
	}
	memory.SchemaVersion = WorkingMemorySchemaVersion
	memory.UpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(memory)
	if err != nil {
		return err
	}
	id := workingMemoryDocumentID(memory.SessionID, memory.TaskID)
	doc, err := m.store.Document(ctx, workingMemoryDocumentKind, id)
	version := int64(0)
	if err == nil {
		version = doc.Version
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = m.store.SaveDocument(ctx, state.DocumentInput{
		ID:              id,
		Kind:            workingMemoryDocumentKind,
		SessionID:       memory.SessionID,
		Status:          "active",
		Payload:         payload,
		ExpectedVersion: version,
		Provenance: state.Provenance{
			Authority:           state.AuthorityDerived,
			WorkspaceRevisionID: memory.WorkspaceRevision,
			EvidenceArtifactIDs: memory.EvidenceArtifactIDs,
			ObservedAt:          memory.UpdatedAt,
		},
		Event: state.EventInput{
			SessionID: memory.SessionID,
			Type:      "working_memory.compacted",
			Payload: map[string]any{
				"task_id":            memory.TaskID,
				"generation":         memory.Generation,
				"active_step_id":     memory.ActiveStepID,
				"workspace_revision": memory.WorkspaceRevision,
			},
		},
	})
	return err
}

func appendBounded(slice []string, item string, limit int) []string {
	if item == "" || limit <= 0 {
		return slice
	}
	if len(slice) >= limit {
		slice = slice[len(slice)-limit+1:]
	}
	return append(slice, item)
}
