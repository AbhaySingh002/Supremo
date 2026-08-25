package state

import (
	"context"
	"database/sql"
)

// SessionSnapshot is one transactionally consistent frontend baseline.
type SessionSnapshot struct {
	Session  Session
	Messages []Message
	Events   []Event
	Cursor   int64
}

func (s *Store) SessionSnapshot(ctx context.Context, sessionID string) (SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionSnapshot{}, err
	}
	defer tx.Rollback()

	session, err := scanSession(tx.QueryRowContext(ctx, `SELECT id, workspace_id, name, created_at, updated_at, status, COALESCE(current_task_id, ''), COALESCE(provider, ''), COALESCE(model, ''), metadata, data, version
		FROM sessions WHERE id = ? AND workspace_id = ?`, sessionID, s.workspaceID))
	if err != nil {
		return SessionSnapshot{}, err
	}
	messages, err := snapshotMessages(ctx, tx, s.workspaceID, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	events, err := snapshotEvents(ctx, tx, s.workspaceID, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	var cursor int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) FROM events WHERE workspace_id = ?", s.workspaceID).Scan(&cursor); err != nil {
		return SessionSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionSnapshot{}, err
	}
	return SessionSnapshot{Session: session, Messages: messages, Events: events, Cursor: cursor}, nil
}

func snapshotMessages(ctx context.Context, tx *sql.Tx, workspaceID, sessionID string) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.id, m.session_id, m.sequence, m.role, COALESCE(m.task_id, ''), m.state, COALESCE(m.parent_id, ''), m.created_at,
		p.ordinal, p.kind, COALESCE(p.text, ''), COALESCE(p.artifact_id, ''), p.metadata
		FROM messages m JOIN message_parts p ON p.message_id = m.id
		WHERE m.workspace_id = ? AND m.session_id = ? AND m.state != 'archived'
		ORDER BY m.sequence, p.ordinal`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	var current *Message
	for rows.Next() {
		var message Message
		var part MessagePart
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Sequence, &message.Role, &message.TaskID, &message.State, &message.ParentID, &createdAt,
			&part.Ordinal, &part.Kind, &part.Text, &part.ArtifactID, &part.Metadata); err != nil {
			return nil, err
		}
		message.CreatedAt = fromUnix(createdAt)
		if current == nil || current.ID != message.ID {
			messages = append(messages, message)
			current = &messages[len(messages)-1]
		}
		current.Parts = append(current.Parts, part)
	}
	return messages, rows.Err()
}

func snapshotEvents(ctx context.Context, tx *sql.Tx, workspaceID, sessionID string) ([]Event, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sequence, event_id, workspace_id, COALESCE(session_id, ''), COALESCE(agent_id, ''), type,
		COALESCE(correlation_id, ''), COALESCE(causation_id, ''), COALESCE(idempotency_key, ''), payload_version, payload, created_at
		FROM events WHERE workspace_id = ? AND session_id = ? ORDER BY sequence`, workspaceID, sessionID)
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
