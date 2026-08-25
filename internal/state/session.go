package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) SaveSession(ctx context.Context, input SessionInput) (Session, error) {
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Name) == "" {
		return Session{}, errors.New("session ID and name are required")
	}
	metadata, err := jsonBytes(input.Metadata)
	if err != nil {
		return Session{}, err
	}
	data, err := jsonBytes(input.Data)
	if err != nil {
		return Session{}, err
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = time.Now().UTC()
	}
	var saved Session
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var currentVersion int64
		var createdAt int64
		err := tx.QueryRowContext(ctx, "SELECT version, created_at FROM sessions WHERE id = ? AND workspace_id = ?", input.ID, s.workspaceID).Scan(&currentVersion, &createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			if input.ExpectedVersion != 0 {
				return ErrConflict
			}
			currentVersion = 0
		} else if err != nil {
			return err
		} else {
			if input.ExpectedVersion != 0 && input.ExpectedVersion != currentVersion {
				return ErrConflict
			}
			input.CreatedAt = fromUnix(createdAt)
		}
		version := currentVersion + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, workspace_id, name, created_at, updated_at, status, current_task_id, provider, model, metadata, data, version)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at, status=excluded.status, current_task_id=excluded.current_task_id,
			provider=excluded.provider, model=excluded.model, metadata=excluded.metadata, data=excluded.data, version=excluded.version`,
			input.ID, s.workspaceID, input.Name, nowUnix(input.CreatedAt), nowUnix(input.UpdatedAt), input.Status, emptyToNull(input.CurrentTaskID), emptyToNull(input.Provider), emptyToNull(input.Model), metadata, data, version); err != nil {
			return err
		}
		saved = Session{ID: input.ID, WorkspaceID: s.workspaceID, Name: input.Name, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.UpdatedAt.UTC(), Status: input.Status,
			CurrentTaskID: input.CurrentTaskID, Provider: input.Provider, Model: input.Model, Metadata: metadata, Data: data, Version: version}
		event := input.Event
		if event.Type == "" {
			event.Type = "session.updated"
			if currentVersion == 0 {
				event.Type = "session.created"
			}
		}
		event.SessionID = input.ID
		event.Payload = saved
		if _, err = s.appendEventTx(ctx, tx, event); err != nil {
			return err
		}
		for _, related := range input.RelatedEvents {
			if related.SessionID == "" {
				related.SessionID = input.ID
			}
			if related.SessionID != input.ID {
				return fmt.Errorf("related event session %q does not match session %q", related.SessionID, input.ID)
			}
			if _, err := s.appendEventTx(ctx, tx, related); err != nil {
				return err
			}
		}
		return nil
	})
	return saved, err
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, created_at, updated_at, status, COALESCE(current_task_id, ''), COALESCE(provider, ''), COALESCE(model, ''), metadata, data, version
		FROM sessions WHERE id = ? AND workspace_id = ?`, id, s.workspaceID)
	return scanSession(row)
}

func (s *Store) Sessions(ctx context.Context, includeArchived bool) ([]Session, error) {
	statement := `SELECT id, workspace_id, name, created_at, updated_at, status, COALESCE(current_task_id, ''), COALESCE(provider, ''), COALESCE(model, ''), metadata, data, version FROM sessions WHERE workspace_id = ?`
	args := []any{s.workspaceID}
	if !includeArchived {
		statement += " AND status != 'archived'"
	}
	statement += " ORDER BY updated_at DESC, id"
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func scanSession(row scanner) (Session, error) {
	var session Session
	var createdAt, updatedAt int64
	err := row.Scan(&session.ID, &session.WorkspaceID, &session.Name, &createdAt, &updatedAt, &session.Status, &session.CurrentTaskID, &session.Provider, &session.Model, &session.Metadata, &session.Data, &session.Version)
	session.CreatedAt, session.UpdatedAt = fromUnix(createdAt), fromUnix(updatedAt)
	return session, err
}

func (s *Store) ArchiveSession(ctx context.Context, id string) error {
	session, err := s.Session(ctx, id)
	if err != nil {
		return err
	}
	session.Status = "archived"
	_, err = s.SaveSession(ctx, SessionInput{ID: session.ID, Name: session.Name, CreatedAt: session.CreatedAt, UpdatedAt: time.Now(), Status: session.Status,
		CurrentTaskID: session.CurrentTaskID, Provider: session.Provider, Model: session.Model, Metadata: session.Metadata, Data: session.Data, ExpectedVersion: session.Version,
		Event: EventInput{Type: "session.archived", SessionID: session.ID}})
	return err
}

func (s *Store) AppendMessage(ctx context.Context, input MessageInput) (Message, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return Message{}, err
		}
	}
	if input.SessionID == "" || input.Role == "" {
		return Message{}, errors.New("message session and role are required")
	}
	if input.State == "" {
		input.State = "active"
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if len(input.Parts) == 0 {
		return Message{}, errors.New("message needs at least one part")
	}
	var message Message
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ?", input.ID).Scan(&exists)
		if err == nil {
			return fmt.Errorf("message %q already exists", input.ID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var sequence int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) + 1 FROM messages WHERE session_id = ?", input.SessionID).Scan(&sequence); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id, workspace_id, session_id, sequence, role, task_id, state, parent_id, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, s.workspaceID, input.SessionID, sequence, input.Role, emptyToNull(input.TaskID), input.State, emptyToNull(input.ParentID), nowUnix(input.CreatedAt)); err != nil {
			return err
		}
		message = Message{ID: input.ID, SessionID: input.SessionID, Sequence: sequence, Role: input.Role, TaskID: input.TaskID, State: input.State, ParentID: input.ParentID, CreatedAt: input.CreatedAt.UTC()}
		for index, part := range input.Parts {
			if strings.TrimSpace(part.Kind) == "" {
				return errors.New("message part kind is required")
			}
			metadata, err := jsonBytes(part.Metadata)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO message_parts(message_id, ordinal, kind, text, artifact_id, metadata) VALUES(?, ?, ?, ?, ?, ?)", input.ID, index, part.Kind, emptyBytes(part.Text), emptyToNull(part.ArtifactID), metadata); err != nil {
				return err
			}
			message.Parts = append(message.Parts, MessagePart{Ordinal: index, Kind: part.Kind, Text: part.Text, ArtifactID: part.ArtifactID, Metadata: metadata})
		}
		event := input.Event
		if event.Type == "" {
			event.Type = input.Role + ".message.created"
		}
		event.SessionID, event.Payload = input.SessionID, message
		if _, err = s.appendEventTx(ctx, tx, event); err != nil {
			return err
		}
		for _, related := range input.RelatedEvents {
			if related.SessionID == "" {
				related.SessionID = input.SessionID
			}
			if related.SessionID != input.SessionID {
				return fmt.Errorf("related event session %q does not match message session %q", related.SessionID, input.SessionID)
			}
			if _, err := s.appendEventTx(ctx, tx, related); err != nil {
				return err
			}
		}
		return nil
	})
	return message, err
}

func (s *Store) Messages(ctx context.Context, sessionID string, includeArchived bool) ([]Message, error) {
	statement := `SELECT id, session_id, sequence, role, COALESCE(task_id, ''), state, COALESCE(parent_id, ''), created_at FROM messages WHERE workspace_id = ? AND session_id = ?`
	if !includeArchived {
		statement += " AND state != 'archived'"
	}
	statement += " ORDER BY sequence"
	rows, err := s.db.QueryContext(ctx, statement, s.workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	var messages []Message
	for rows.Next() {
		var message Message
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Sequence, &message.Role, &message.TaskID, &message.State, &message.ParentID, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt = fromUnix(createdAt)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range messages {
		parts, err := s.messageParts(ctx, messages[index].ID)
		if err != nil {
			return nil, err
		}
		messages[index].Parts = parts
	}
	return messages, nil
}

func (s *Store) messageParts(ctx context.Context, messageID string) ([]MessagePart, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT ordinal, kind, COALESCE(text, ''), COALESCE(artifact_id, ''), metadata FROM message_parts WHERE message_id = ? ORDER BY ordinal", messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parts []MessagePart
	for rows.Next() {
		var part MessagePart
		if err := rows.Scan(&part.Ordinal, &part.Kind, &part.Text, &part.ArtifactID, &part.Metadata); err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, rows.Err()
}

func (s *Store) ArchiveMessages(ctx context.Context, sessionID string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE messages SET state = 'archived' WHERE workspace_id = ? AND session_id = ? AND state != 'archived'", s.workspaceID, sessionID); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "conversation.archived", SessionID: sessionID, Payload: map[string]string{"session_id": sessionID}})
		return err
	})
}

func emptyBytes(value string) any {
	if value == "" {
		return nil
	}
	return value
}
