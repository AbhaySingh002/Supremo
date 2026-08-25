package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) SaveDocument(ctx context.Context, input DocumentInput) (Document, error) {
	if input.ID == "" || input.Kind == "" {
		return Document{}, errors.New("document ID and kind are required")
	}
	payload, err := jsonBytes(input.Payload)
	if err != nil {
		return Document{}, err
	}
	provenance, err := json.Marshal(input.Provenance)
	if err != nil {
		return Document{}, err
	}
	if input.Status == "" {
		input.Status = "active"
	}
	var document Document
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var version int64
		var createdAt int64
		err := tx.QueryRowContext(ctx, "SELECT version, created_at FROM documents_current WHERE id = ? AND kind = ? AND workspace_id = ?", input.ID, input.Kind, s.workspaceID).Scan(&version, &createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			if input.ExpectedVersion != 0 {
				return ErrConflict
			}
			version, createdAt = 0, nowUnix(time.Now())
		} else if err != nil {
			return err
		} else if input.ExpectedVersion != 0 && input.ExpectedVersion != version {
			return ErrConflict
		}
		version++
		now := nowUnix(time.Now())
		document = Document{ID: input.ID, Kind: input.Kind, SessionID: input.SessionID, Status: input.Status, Payload: payload, Provenance: input.Provenance, Version: version, CreatedAt: fromUnix(createdAt), UpdatedAt: fromUnix(now)}
		if _, err := tx.ExecContext(ctx, `INSERT INTO documents(id, kind, workspace_id, session_id, status, version, payload, provenance, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			document.ID, document.Kind, s.workspaceID, emptyToNull(document.SessionID), document.Status, document.Version, document.Payload, provenance, createdAt, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO documents_current(id, kind, workspace_id, session_id, status, version, payload, provenance, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id, kind) DO UPDATE SET session_id=excluded.session_id, status=excluded.status, version=excluded.version, payload=excluded.payload, provenance=excluded.provenance, updated_at=excluded.updated_at`,
			document.ID, document.Kind, s.workspaceID, emptyToNull(document.SessionID), document.Status, document.Version, document.Payload, provenance, createdAt, now); err != nil {
			return err
		}
		events := append([]EventInput{input.Event}, input.Events...)
		for _, event := range events {
			if event.Type == "" {
				event.Type = "document.saved"
			}
			event.SessionID, event.Payload = input.SessionID, document
			if _, err = s.appendEventTx(ctx, tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	return document, err
}

// DeleteDocument permanently removes the current document and its version
// history. It intentionally leaves event and artifact records intact, because
// they can be shared evidence for other durable state.
func (s *Store) DeleteDocument(ctx context.Context, input DocumentDeleteInput) error {
	if input.ID == "" || input.Kind == "" || input.ExpectedVersion <= 0 {
		return errors.New("document ID, kind, and current version are required")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var version int64
		var sessionID string
		err := tx.QueryRowContext(ctx, `SELECT version, COALESCE(session_id, '') FROM documents_current WHERE workspace_id = ? AND kind = ? AND id = ?`, s.workspaceID, input.Kind, input.ID).Scan(&version, &sessionID)
		if err != nil {
			return err
		}
		if input.ExpectedVersion != version {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM documents_current WHERE workspace_id = ? AND kind = ? AND id = ?`, s.workspaceID, input.Kind, input.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE workspace_id = ? AND kind = ? AND id = ?`, s.workspaceID, input.Kind, input.ID); err != nil {
			return err
		}
		event := input.Event
		if event.Type == "" {
			event.Type = "document.deleted"
		}
		event.SessionID = sessionID
		event.Payload = map[string]any{"id": input.ID, "kind": input.Kind, "version": version}
		_, err = s.appendEventTx(ctx, tx, event)
		return err
	})
}

func (s *Store) Document(ctx context.Context, kind, id string) (Document, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, COALESCE(session_id, ''), status, payload, provenance, version, created_at, updated_at FROM documents_current WHERE workspace_id = ? AND kind = ? AND id = ?`, s.workspaceID, kind, id)
	return scanDocument(row)
}

func (s *Store) Documents(ctx context.Context, kind, sessionID string) ([]Document, error) {
	where, args := []string{"workspace_id = ?", "kind = ?"}, []any{s.workspaceID, kind}
	if sessionID != "" {
		where, args = append(where, "session_id = ?"), append(args, sessionID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, COALESCE(session_id, ''), status, payload, provenance, version, created_at, updated_at FROM documents_current WHERE `+strings.Join(where, " AND ")+" ORDER BY updated_at DESC, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var documents []Document
	for rows.Next() {
		document, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func scanDocument(row scanner) (Document, error) {
	var document Document
	var provenance []byte
	var createdAt, updatedAt int64
	err := row.Scan(&document.ID, &document.Kind, &document.SessionID, &document.Status, &document.Payload, &provenance, &document.Version, &createdAt, &updatedAt)
	if err == nil {
		err = json.Unmarshal(provenance, &document.Provenance)
	}
	document.CreatedAt, document.UpdatedAt = fromUnix(createdAt), fromUnix(updatedAt)
	return document, err
}

func (s *Store) CreateClaim(ctx context.Context, input ClaimInput) (Claim, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return Claim{}, err
		}
	}
	if input.Kind == "" || strings.TrimSpace(input.Statement) == "" {
		return Claim{}, errors.New("claim kind and statement are required")
	}
	if input.Status == "" {
		input.Status = "active"
	}
	provenance, err := json.Marshal(input.Provenance)
	if err != nil {
		return Claim{}, err
	}
	claim := Claim{ID: input.ID, LineageID: input.ID, Kind: input.Kind, Statement: input.Statement, Scope: input.Scope, Status: input.Status, Confidence: input.Confidence, Provenance: input.Provenance, CreatedAt: time.Now().UTC()}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims(id, lineage_id, workspace_id, kind, statement, scope, status, confidence, provenance, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			claim.ID, claim.LineageID, s.workspaceID, claim.Kind, claim.Statement, emptyToNull(claim.Scope), claim.Status, claim.Confidence, provenance, nowUnix(claim.CreatedAt)); err != nil {
			return err
		}
		event := input.Event
		if event.Type == "" {
			event.Type = input.Kind + ".created"
		}
		event.Payload = claim
		_, err = s.appendEventTx(ctx, tx, event)
		return err
	})
	return claim, err
}

func (s *Store) SupersedeClaim(ctx context.Context, oldID string, input ClaimInput) (Claim, error) {
	if oldID == "" {
		return Claim{}, errors.New("claim to supersede is required")
	}
	var created Claim
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var old Claim
		var provenance []byte
		var createdAt int64
		if err := tx.QueryRowContext(ctx, `SELECT id, lineage_id, kind, statement, COALESCE(scope, ''), status, confidence, provenance, COALESCE(supersedes_id, ''), COALESCE(superseded_by_id, ''), created_at FROM claims WHERE id = ? AND workspace_id = ?`, oldID, s.workspaceID).Scan(&old.ID, &old.LineageID, &old.Kind, &old.Statement, &old.Scope, &old.Status, &old.Confidence, &provenance, &old.SupersedesID, &old.SupersededByID, &createdAt); err != nil {
			return err
		}
		if old.SupersededByID != "" {
			return ErrConflict
		}
		if err := json.Unmarshal(provenance, &old.Provenance); err != nil {
			return err
		}
		old.CreatedAt = fromUnix(createdAt)
		if input.ID == "" {
			var err error
			input.ID, err = newID()
			if err != nil {
				return err
			}
		}
		if input.Kind == "" {
			input.Kind = old.Kind
		}
		provenance, err := json.Marshal(input.Provenance)
		if err != nil {
			return err
		}
		created = Claim{ID: input.ID, LineageID: old.LineageID, Kind: input.Kind, Statement: input.Statement, Scope: input.Scope, Status: input.Status, Confidence: input.Confidence, Provenance: input.Provenance, SupersedesID: old.ID, CreatedAt: time.Now().UTC()}
		if created.Status == "" {
			created.Status = "active"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims(id, lineage_id, workspace_id, kind, statement, scope, status, confidence, provenance, supersedes_id, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, created.ID, created.LineageID, s.workspaceID, created.Kind, created.Statement, emptyToNull(created.Scope), created.Status, created.Confidence, provenance, old.ID, nowUnix(created.CreatedAt)); err != nil {
			return err
		}
		if result, err := tx.ExecContext(ctx, "UPDATE claims SET superseded_by_id = ? WHERE id = ? AND workspace_id = ? AND superseded_by_id IS NULL", created.ID, old.ID, s.workspaceID); err != nil {
			return err
		} else if count, _ := result.RowsAffected(); count != 1 {
			return ErrConflict
		}
		event := input.Event
		if event.Type == "" {
			event.Type = input.Kind + ".superseded"
		}
		event.Payload = created
		_, err = s.appendEventTx(ctx, tx, event)
		return err
	})
	return created, err
}

func (s *Store) Claims(ctx context.Context, kind string, includeHistorical bool) ([]Claim, error) {
	where, args := []string{"workspace_id = ?"}, []any{s.workspaceID}
	if kind != "" {
		where, args = append(where, "kind = ?"), append(args, kind)
	}
	if !includeHistorical {
		where = append(where, "superseded_by_id IS NULL")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, lineage_id, kind, statement, COALESCE(scope, ''), status, confidence, provenance, COALESCE(supersedes_id, ''), COALESCE(superseded_by_id, ''), created_at FROM claims WHERE `+strings.Join(where, " AND ")+" ORDER BY created_at", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claims []Claim
	for rows.Next() {
		var claim Claim
		var provenance []byte
		var createdAt int64
		if err := rows.Scan(&claim.ID, &claim.LineageID, &claim.Kind, &claim.Statement, &claim.Scope, &claim.Status, &claim.Confidence, &provenance, &claim.SupersedesID, &claim.SupersededByID, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(provenance, &claim.Provenance); err != nil {
			return nil, err
		}
		claim.CreatedAt = fromUnix(createdAt)
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func (s *Store) PutArtifact(ctx context.Context, input ArtifactInput) (Artifact, error) {
	hashValue := sha256.Sum256(input.Data)
	hash := hex.EncodeToString(hashValue[:])
	artifact := Artifact{Hash: hash, Size: int64(len(input.Data)), ContentType: input.ContentType, Origin: input.Origin, CreatedAt: time.Now().UTC()}
	if artifact.ContentType == "" {
		artifact.ContentType = "application/octet-stream"
	}
	if artifact.Origin == "" {
		artifact.Origin = "unknown"
	}
	created, err := s.writeObject(hash, input.Data)
	if err != nil {
		return Artifact{}, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(hash, workspace_id, size, content_type, origin, created_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`, hash, s.workspaceID, artifact.Size, artifact.ContentType, artifact.Origin, nowUnix(artifact.CreatedAt)); err != nil {
			return err
		}
		event := input.Event
		if event.Type == "" {
			event.Type = "artifact.created"
			event.IdempotencyKey = "artifact:" + hash
		}
		event.Payload = artifact
		_, err := s.appendEventTx(ctx, tx, event)
		return err
	})
	if err != nil && created {
		_ = os.Remove(s.objectPath(hash))
	}
	return artifact, err
}

func (s *Store) Artifact(ctx context.Context, hash string) (Artifact, error) {
	var artifact Artifact
	var createdAt int64
	err := s.db.QueryRowContext(ctx, "SELECT hash, size, content_type, origin, created_at FROM artifacts WHERE hash = ? AND workspace_id = ?", hash, s.workspaceID).Scan(&artifact.Hash, &artifact.Size, &artifact.ContentType, &artifact.Origin, &createdAt)
	artifact.CreatedAt = fromUnix(createdAt)
	return artifact, err
}

func (s *Store) ReadArtifact(ctx context.Context, hash string) ([]byte, error) {
	if _, err := s.Artifact(ctx, hash); err != nil {
		return nil, err
	}
	file, err := os.Open(s.objectPath(hash))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != hash {
		return nil, fmt.Errorf("artifact %s failed hash verification", hash)
	}
	return data, nil
}

func (s *Store) writeObject(hash string, data []byte) (bool, error) {
	directory := filepath.Dir(s.objectPath(hash))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return false, err
	}
	path := s.objectPath(hash)
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	file, err := os.CreateTemp(directory, ".tmp-*")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return false, err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	// Link is an atomic no-clobber publish in the same object directory. Rename
	// would overwrite a concurrently published object on POSIX.
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) objectPath(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(s.objects, "invalid")
	}
	return filepath.Join(s.objects, hash[:2], hash)
}

func (s *Store) ObserveFile(ctx context.Context, input FileObservation) (FileVersion, error) {
	var version FileVersion
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		version, err = s.observeFileTx(ctx, tx, input)
		return err
	})
	return version, err
}

func (s *Store) observeFileTx(ctx context.Context, tx *sql.Tx, input FileObservation) (FileVersion, error) {
	path := filepath.ToSlash(filepath.Clean(input.Path))
	if path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(input.Path) {
		return FileVersion{}, errors.New("file observation path must be workspace-relative")
	}
	if input.ModifiedAt.IsZero() {
		input.ModifiedAt = time.Now().UTC()
	}
	var fileID string
	err := tx.QueryRowContext(ctx, "SELECT id FROM files WHERE workspace_id = ? AND current_path = ?", s.workspaceID, path).Scan(&fileID)
	if errors.Is(err, sql.ErrNoRows) {
		fileID, err = newID()
		if err != nil {
			return FileVersion{}, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO files(id, workspace_id, current_path, deleted, created_at) VALUES(?, ?, ?, ?, ?)", fileID, s.workspaceID, path, input.Deleted, nowUnix(input.ModifiedAt)); err != nil {
			return FileVersion{}, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO file_paths(file_id, path, observed_at) VALUES(?, ?, ?)", fileID, path, nowUnix(input.ModifiedAt)); err != nil {
			return FileVersion{}, err
		}
	} else if err != nil {
		return FileVersion{}, err
	} else if _, err := tx.ExecContext(ctx, "UPDATE files SET deleted = ? WHERE id = ?", input.Deleted, fileID); err != nil {
		return FileVersion{}, err
	}
	id, err := newID()
	if err != nil {
		return FileVersion{}, err
	}
	version := FileVersion{ID: id, FileID: fileID, Path: path, Deleted: input.Deleted, ModifiedAt: input.ModifiedAt.UTC(), WorkspaceRevisionID: input.WorkspaceRevisionID, ObservedAt: time.Now().UTC()}
	if !input.Deleted {
		hash := sha256.Sum256(input.Data)
		version.Hash, version.Size = hex.EncodeToString(hash[:]), int64(len(input.Data))
		artifact, err := s.putArtifactTx(ctx, tx, ArtifactInput{Data: input.Data, ContentType: "application/octet-stream", Origin: "file:" + path})
		if err != nil {
			return FileVersion{}, err
		}
		version.ArtifactID = artifact.Hash
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_versions(id, file_id, path, hash, size, deleted, modified_at, workspace_revision_id, artifact_id, observed_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.FileID, version.Path, emptyToNull(version.Hash), version.Size, version.Deleted, nowUnix(version.ModifiedAt), emptyToNull(version.WorkspaceRevisionID), emptyToNull(version.ArtifactID), nowUnix(version.ObservedAt)); err != nil {
		return FileVersion{}, err
	}
	event := input.Event
	if event.Type == "" {
		event.Type = "file.read"
		if input.Deleted {
			event.Type = "file.deleted"
		}
	}
	event.Payload = version
	if _, err := s.appendEventTx(ctx, tx, event); err != nil {
		return FileVersion{}, err
	}
	return version, nil
}

// RenameFile moves a known file identity to a new workspace-relative path.
// A first-seen rename is harmless: the post-rename observation creates it.
func (s *Store) RenameFile(ctx context.Context, input FileRename) error {
	oldPath, err := cleanWorkspacePath(input.OldPath)
	if err != nil {
		return err
	}
	newPath, err := cleanWorkspacePath(input.NewPath)
	if err != nil {
		return err
	}
	if oldPath == newPath {
		return nil
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var fileID string
		err := tx.QueryRowContext(ctx, "SELECT id FROM files WHERE workspace_id = ? AND current_path = ?", s.workspaceID, oldPath).Scan(&fileID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var destinationID string
		err = tx.QueryRowContext(ctx, "SELECT id FROM files WHERE workspace_id = ? AND current_path = ?", s.workspaceID, newPath).Scan(&destinationID)
		if err == nil && destinationID != fileID {
			return ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE files SET current_path = ?, deleted = 0 WHERE id = ?", newPath, fileID); err != nil {
			return err
		}
		now := nowUnix(time.Now())
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO file_paths(file_id, path, observed_at) VALUES(?, ?, ?)", fileID, newPath, now); err != nil {
			return err
		}
		event := input.Event
		if event.Type == "" {
			event.Type = "file.renamed"
		}
		event.Payload = map[string]any{"file_id": fileID, "old_path": oldPath, "new_path": newPath, "workspace_revision_id": input.WorkspaceRevisionID}
		_, err = s.appendEventTx(ctx, tx, event)
		return err
	})
}

func (s *Store) putArtifactTx(ctx context.Context, tx *sql.Tx, input ArtifactInput) (Artifact, error) {
	hashValue := sha256.Sum256(input.Data)
	hash := hex.EncodeToString(hashValue[:])
	artifact := Artifact{Hash: hash, Size: int64(len(input.Data)), ContentType: input.ContentType, Origin: input.Origin, CreatedAt: time.Now().UTC()}
	if artifact.ContentType == "" {
		artifact.ContentType = "application/octet-stream"
	}
	if artifact.Origin == "" {
		artifact.Origin = "unknown"
	}
	if _, err := s.writeObject(hash, input.Data); err != nil {
		return Artifact{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(hash, workspace_id, size, content_type, origin, created_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`, hash, s.workspaceID, artifact.Size, artifact.ContentType, artifact.Origin, nowUnix(artifact.CreatedAt)); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func (s *Store) FileVersions(ctx context.Context, path string) ([]FileVersion, error) {
	path, err := cleanWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT fv.id, fv.file_id, fv.path, COALESCE(fv.hash, ''), fv.size, fv.deleted, fv.modified_at, COALESCE(fv.workspace_revision_id, ''), COALESCE(fv.artifact_id, ''), fv.observed_at FROM file_versions fv JOIN files f ON f.id = fv.file_id WHERE f.workspace_id = ? AND (fv.path = ? OR f.id IN (SELECT file_id FROM file_paths WHERE path = ?)) ORDER BY fv.observed_at`, s.workspaceID, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []FileVersion
	for rows.Next() {
		var version FileVersion
		var modifiedAt, observedAt int64
		if err := rows.Scan(&version.ID, &version.FileID, &version.Path, &version.Hash, &version.Size, &version.Deleted, &modifiedAt, &version.WorkspaceRevisionID, &version.ArtifactID, &observedAt); err != nil {
			return nil, err
		}
		version.ModifiedAt, version.ObservedAt = fromUnix(modifiedAt), fromUnix(observedAt)
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func cleanWorkspacePath(path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return "", errors.New("file path must be workspace-relative")
	}
	return path, nil
}
