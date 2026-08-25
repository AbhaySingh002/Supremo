package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) AppendEvent(ctx context.Context, input EventInput) (Event, error) {
	var event Event
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		event, err = s.appendEventTx(ctx, tx, input)
		return err
	})
	return event, err
}

func (s *Store) appendEventTx(ctx context.Context, tx *sql.Tx, input EventInput) (Event, error) {
	if strings.TrimSpace(input.Type) == "" {
		return Event{}, errors.New("event type is required")
	}
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return Event{}, err
		}
	}
	if input.PayloadVersion <= 0 {
		input.PayloadVersion = 1
	}
	payload, err := jsonBytes(input.Payload)
	if err != nil {
		return Event{}, err
	}
	if input.IdempotencyKey != "" {
		event, found, err := s.eventByIdempotency(ctx, tx, input.IdempotencyKey)
		if err != nil || found {
			return event, err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO events(event_id, workspace_id, session_id, agent_id, type, correlation_id, causation_id, idempotency_key, payload_version, payload, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.ID, s.workspaceID, emptyToNull(input.SessionID), emptyToNull(input.AgentID), input.Type,
		emptyToNull(input.CorrelationID), emptyToNull(input.CausationID), emptyToNull(input.IdempotencyKey), input.PayloadVersion, payload, nowUnix(input.CreatedAt))
	if err != nil {
		return Event{}, err
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return Event{}, err
	}
	event := Event{Sequence: sequence, ID: input.ID, WorkspaceID: s.workspaceID, SessionID: input.SessionID, AgentID: input.AgentID,
		Type: input.Type, CorrelationID: input.CorrelationID, CausationID: input.CausationID, IdempotencyKey: input.IdempotencyKey,
		PayloadVersion: input.PayloadVersion, Payload: payload, CreatedAt: fromUnix(nowUnix(input.CreatedAt))}
	s.pending = append(s.pending, event)
	return event, nil
}

func (s *Store) eventByIdempotency(ctx context.Context, tx *sql.Tx, key string) (Event, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT sequence, event_id, workspace_id, COALESCE(session_id, ''), COALESCE(agent_id, ''), type,
		COALESCE(correlation_id, ''), COALESCE(causation_id, ''), COALESCE(idempotency_key, ''), payload_version, payload, created_at
		FROM events WHERE workspace_id = ? AND idempotency_key = ?`, s.workspaceID, key)
	event, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return event, err == nil, err
}

func (s *Store) Events(ctx context.Context, query EventQuery) ([]Event, error) {
	args := []any{s.workspaceID}
	where := []string{"workspace_id = ?"}
	if query.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.Type != "" {
		where = append(where, "type = ?")
		args = append(args, query.Type)
	}
	if query.After > 0 {
		where = append(where, "sequence > ?")
		args = append(args, query.After)
	}
	statement := `SELECT sequence, event_id, workspace_id, COALESCE(session_id, ''), COALESCE(agent_id, ''), type,
		COALESCE(correlation_id, ''), COALESCE(causation_id, ''), COALESCE(idempotency_key, ''), payload_version, payload, created_at FROM events WHERE ` + strings.Join(where, " AND ") + " ORDER BY sequence"
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

func (s *Store) EventByIdempotency(ctx context.Context, key string) (Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback()
	event, found, err := s.eventByIdempotency(ctx, tx, key)
	if err != nil {
		return Event{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, err
	}
	return event, found, nil
}

func (s *Store) Cursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) FROM events WHERE workspace_id = ?", s.workspaceID).Scan(&cursor)
	return cursor, err
}

type scanner interface{ Scan(...any) error }

func scanEvent(row scanner) (Event, error) {
	var event Event
	var createdAt int64
	err := row.Scan(&event.Sequence, &event.ID, &event.WorkspaceID, &event.SessionID, &event.AgentID, &event.Type,
		&event.CorrelationID, &event.CausationID, &event.IdempotencyKey, &event.PayloadVersion, &event.Payload, &createdAt)
	event.CreatedAt = fromUnix(createdAt)
	return event, err
}

func emptyToNull(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// RebuildCurrentState recreates mutable workspace projections from the
// append-only event log. Artifact objects are immutable and are deliberately
// not rewritten by a rebuild.
func (s *Store) RebuildCurrentState(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range []string{
			"DELETE FROM repository_fts_state WHERE workspace_id = ?",
			"DELETE FROM repository_fts WHERE workspace_id = ?",
			"DELETE FROM repository_embeddings WHERE workspace_id = ?",
			"DELETE FROM repository_relations WHERE workspace_id = ?",
			"DELETE FROM repository_summaries WHERE workspace_id = ?",
			"DELETE FROM repository_chunks WHERE workspace_id = ?",
			"DELETE FROM repository_symbol_versions WHERE symbol_id IN (SELECT id FROM repository_symbols WHERE workspace_id = ?)",
			"DELETE FROM repository_symbols WHERE workspace_id = ?",
			"DELETE FROM repository_observations WHERE workspace_id = ?",
			"DELETE FROM repository_file_state WHERE workspace_id = ?",
			"DELETE FROM repository_revisions WHERE workspace_id = ?",
			"DELETE FROM message_parts WHERE message_id IN (SELECT id FROM messages WHERE workspace_id = ?)",
			"DELETE FROM messages WHERE workspace_id = ?",
			"DELETE FROM sessions WHERE workspace_id = ?",
			"DELETE FROM file_versions WHERE file_id IN (SELECT id FROM files WHERE workspace_id = ?)",
			"DELETE FROM file_paths WHERE file_id IN (SELECT id FROM files WHERE workspace_id = ?)",
			"DELETE FROM files WHERE workspace_id = ?",
			"DELETE FROM claims WHERE workspace_id = ?",
			"DELETE FROM workspace_revisions WHERE workspace_id = ?",
			"DELETE FROM documents_current WHERE workspace_id = ?",
			"DELETE FROM workspace_memory WHERE workspace_id = ?",
			"DELETE FROM legacy_imports WHERE workspace_id = ?",
		} {
			if _, err := tx.ExecContext(ctx, statement, s.workspaceID); err != nil {
				return err
			}
		}
		events, err := tx.QueryContext(ctx, "SELECT type, payload FROM events WHERE workspace_id = ? ORDER BY sequence", s.workspaceID)
		if err != nil {
			return err
		}
		defer events.Close()
		for events.Next() {
			var kind string
			var payload []byte
			if err := events.Scan(&kind, &payload); err != nil {
				return err
			}
			if err := s.applyProjectionEvent(ctx, tx, kind, payload); err != nil {
				return err
			}
		}
		if err := events.Err(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE claims
			SET superseded_by_id = (SELECT newer.id FROM claims newer WHERE newer.workspace_id = claims.workspace_id AND newer.supersedes_id = claims.id)
			WHERE workspace_id = ?`, s.workspaceID)
		return err
	})
}

func (s *Store) applyProjectionEvent(ctx context.Context, tx *sql.Tx, kind string, payload []byte) error {
	var document Document
	if json.Unmarshal(payload, &document) == nil && document.ID != "" && document.Kind != "" && len(document.Payload) > 0 {
		provenance, err := json.Marshal(document.Provenance)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO documents_current(id, kind, workspace_id, session_id, status, version, payload, provenance, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id, kind) DO UPDATE SET session_id=excluded.session_id, status=excluded.status, version=excluded.version, payload=excluded.payload, provenance=excluded.provenance, updated_at=excluded.updated_at`,
			document.ID, document.Kind, s.workspaceID, emptyToNull(document.SessionID), document.Status, document.Version, document.Payload, provenance, nowUnix(document.CreatedAt), nowUnix(document.UpdatedAt))
		return err
	}
	switch kind {
	case "session.created", "session.updated", "session.archived":
		var session Session
		if err := json.Unmarshal(payload, &session); err != nil {
			return fmt.Errorf("rebuild session projection: %w", err)
		}
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO sessions(id, workspace_id, name, created_at, updated_at, status, current_task_id, provider, model, metadata, data, version)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, session.ID, s.workspaceID, session.Name, nowUnix(session.CreatedAt), nowUnix(session.UpdatedAt), session.Status, emptyToNull(session.CurrentTaskID), emptyToNull(session.Provider), emptyToNull(session.Model), session.Metadata, session.Data, session.Version)
		return err
	case "conversation.archived":
		var value struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE messages SET state = 'archived' WHERE workspace_id = ? AND session_id = ?", s.workspaceID, value.SessionID)
		return err
	case "workspace.revision.observed":
		var revision WorkspaceRevision
		if err := json.Unmarshal(payload, &revision); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO workspace_revisions(id, workspace_id, head, branch, dirty, metadata, observed_at) VALUES(?, ?, ?, ?, ?, ?, ?)", revision.ID, s.workspaceID, emptyToNull(revision.Head), emptyToNull(revision.Branch), revision.Dirty, revision.Metadata, nowUnix(revision.ObservedAt))
		return err
	case "workspace.memory.updated":
		var value struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO workspace_memory(workspace_id, content, updated_at) VALUES(?, ?, ?)", s.workspaceID, value.Content, nowUnix(time.Now()))
		return err
	case "repository.semantic.updated":
		var value struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO repository_semantic_settings(workspace_id, enabled, updated_at) VALUES(?, ?, ?)", s.workspaceID, value.Enabled, nowUnix(time.Now()))
		return err
	case "legacy.imported":
		var value struct {
			Source string `json:"source"`
			Hash   string `json:"hash"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO legacy_imports(workspace_id, source, source_hash, imported_at) VALUES(?, ?, ?, ?)", s.workspaceID, value.Source, value.Hash, nowUnix(time.Now()))
		return err
	}
	if strings.HasSuffix(kind, ".message.created") {
		var message Message
		if err := json.Unmarshal(payload, &message); err != nil {
			return fmt.Errorf("rebuild message projection: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO messages(id, workspace_id, session_id, sequence, role, task_id, state, parent_id, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)", message.ID, s.workspaceID, message.SessionID, message.Sequence, message.Role, emptyToNull(message.TaskID), message.State, emptyToNull(message.ParentID), nowUnix(message.CreatedAt)); err != nil {
			return err
		}
		for _, part := range message.Parts {
			if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO message_parts(message_id, ordinal, kind, text, artifact_id, metadata) VALUES(?, ?, ?, ?, ?, ?)", message.ID, part.Ordinal, part.Kind, emptyBytes(part.Text), emptyToNull(part.ArtifactID), part.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.HasPrefix(kind, "file.") {
		return s.applyFileProjectionEvent(ctx, tx, kind, payload)
	}
	var claim Claim
	if err := json.Unmarshal(payload, &claim); err == nil && claim.ID != "" && claim.LineageID != "" && claim.Kind != "" {
		provenance, err := json.Marshal(claim.Provenance)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO claims(id, lineage_id, workspace_id, kind, statement, scope, status, confidence, provenance, supersedes_id, superseded_by_id, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, claim.ID, claim.LineageID, s.workspaceID, claim.Kind, claim.Statement, emptyToNull(claim.Scope), claim.Status, claim.Confidence, provenance, emptyToNull(claim.SupersedesID), emptyToNull(claim.SupersededByID), nowUnix(claim.CreatedAt))
		return err
	}
	return nil
}

func (s *Store) applyFileProjectionEvent(ctx context.Context, tx *sql.Tx, kind string, payload []byte) error {
	if kind == "file.renamed" {
		var move struct {
			FileID  string `json:"file_id"`
			NewPath string `json:"new_path"`
		}
		if err := json.Unmarshal(payload, &move); err != nil {
			return err
		}
		if move.FileID == "" || move.NewPath == "" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE files SET current_path = ?, deleted = 0 WHERE id = ? AND workspace_id = ?", move.NewPath, move.FileID, s.workspaceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO file_paths(file_id, path, observed_at) VALUES(?, ?, ?)", move.FileID, move.NewPath, nowUnix(time.Now()))
		return err
	}
	var version FileVersion
	if err := json.Unmarshal(payload, &version); err != nil || version.ID == "" || version.FileID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO files(id, workspace_id, current_path, deleted, created_at) VALUES(?, ?, ?, ?, ?)", version.FileID, s.workspaceID, version.Path, version.Deleted, nowUnix(version.ObservedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE files SET current_path = ?, deleted = ? WHERE id = ?", version.Path, version.Deleted, version.FileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO file_paths(file_id, path, observed_at) VALUES(?, ?, ?)", version.FileID, version.Path, nowUnix(version.ObservedAt)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO file_versions(id, file_id, path, hash, size, deleted, modified_at, workspace_revision_id, artifact_id, observed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, version.ID, version.FileID, version.Path, emptyToNull(version.Hash), version.Size, version.Deleted, nowUnix(version.ModifiedAt), emptyToNull(version.WorkspaceRevisionID), emptyToNull(version.ArtifactID), nowUnix(version.ObservedAt))
	return err
}

func (s *Store) ObserveWorkspace(ctx context.Context, snapshot WorkspaceSnapshot) (WorkspaceRevision, error) {
	id, err := newID()
	if err != nil {
		return WorkspaceRevision{}, err
	}
	metadata, err := jsonBytes(snapshot.Metadata)
	if err != nil {
		return WorkspaceRevision{}, err
	}
	observed := nowUnix(snapshot.ObservedAt)
	revision := WorkspaceRevision{ID: id, WorkspaceID: s.workspaceID, Head: snapshot.Head, Branch: snapshot.Branch, Dirty: snapshot.Dirty, Metadata: metadata, ObservedAt: fromUnix(observed)}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_revisions(id, workspace_id, head, branch, dirty, metadata, observed_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, id, s.workspaceID, emptyToNull(snapshot.Head), emptyToNull(snapshot.Branch), snapshot.Dirty, metadata, observed); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "workspace.revision.observed", Payload: revision, CreatedAt: revision.ObservedAt})
		return err
	})
	return revision, err
}

func (s *Store) WorkspaceMemory(ctx context.Context) (string, error) {
	var content []byte
	err := s.db.QueryRowContext(ctx, "SELECT content FROM workspace_memory WHERE workspace_id = ?", s.workspaceID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return string(content), err
}

func (s *Store) SetWorkspaceMemory(ctx context.Context, content string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memory(workspace_id, content, updated_at) VALUES(?, ?, ?)
			ON CONFLICT(workspace_id) DO UPDATE SET content=excluded.content, updated_at=excluded.updated_at`, s.workspaceID, content, nowUnix(time.Now())); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "workspace.memory.updated", Payload: map[string]any{"content": content}})
		return err
	})
}
