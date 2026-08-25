package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) RepositoryFiles(ctx context.Context) ([]RepositoryFileState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT file_id, COALESCE(file_version_id, ''), path, COALESCE(hash, ''), size, modified_at, language,
		COALESCE(repository_revision_id, ''), deleted FROM repository_file_state WHERE workspace_id = ?`, s.workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []RepositoryFileState
	for rows.Next() {
		var file RepositoryFileState
		var modifiedAt int64
		if err := rows.Scan(&file.FileID, &file.FileVersionID, &file.Path, &file.Hash, &file.Size, &modifiedAt, &file.Language, &file.RepositoryRevisionID, &file.Deleted); err != nil {
			return nil, err
		}
		file.ModifiedAt = fromUnix(modifiedAt)
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *Store) LatestRepositoryRevision(ctx context.Context) (RepositoryRevision, error) {
	var revision RepositoryRevision
	var observedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(workspace_revision_id, ''), COALESCE(head, ''), COALESCE(branch, ''), dirty, scanner_version, observed_at
		FROM repository_revisions WHERE workspace_id = ? ORDER BY observed_at DESC LIMIT 1`, s.workspaceID).Scan(
		&revision.ID, &revision.WorkspaceRevisionID, &revision.Head, &revision.Branch, &revision.Dirty, &revision.ScannerVersion, &observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryRevision{}, nil
	}
	revision.ObservedAt = fromUnix(observedAt)
	return revision, err
}

// TouchRepositoryFile records checked metadata without manufacturing a source
// version when the content hash is unchanged.
func (s *Store) TouchRepositoryFile(ctx context.Context, file RepositoryFileState) error {
	if file.FileID == "" {
		return errors.New("repository file ID is required")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE repository_file_state SET path = ?, size = ?, modified_at = ?, language = ?, updated_at = ?
			WHERE workspace_id = ? AND file_id = ?`, file.Path, file.Size, nowUnix(file.ModifiedAt), file.Language, nowUnix(time.Now()), s.workspaceID, file.FileID)
		return err
	})
}

func (s *Store) BeginRepositoryRevision(ctx context.Context, input RepositoryRevisionInput) (RepositoryRevision, error) {
	id, err := newID()
	if err != nil {
		return RepositoryRevision{}, err
	}
	if input.ScannerVersion == "" {
		input.ScannerVersion = "v1"
	}
	revision := RepositoryRevision{ID: id, WorkspaceRevisionID: input.WorkspaceRevisionID, Head: input.Head, Branch: input.Branch, Dirty: input.Dirty, ScannerVersion: input.ScannerVersion, ObservedAt: fromUnix(nowUnix(input.ObservedAt))}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_revisions(id, workspace_id, workspace_revision_id, head, branch, dirty, scanner_version, observed_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, revision.ID, s.workspaceID, emptyToNull(revision.WorkspaceRevisionID), emptyToNull(revision.Head), emptyToNull(revision.Branch), revision.Dirty, revision.ScannerVersion, nowUnix(revision.ObservedAt)); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "repository.revision.indexed", Payload: revision, CreatedAt: revision.ObservedAt})
		return err
	})
	return revision, err
}

// ApplyRepositoryFile creates one immutable file version and replaces only its
// derived current projections in the same transaction.
func (s *Store) ApplyRepositoryFile(ctx context.Context, input RepositoryFileInput) (RepositoryFileState, error) {
	if input.RepositoryRevisionID == "" {
		return RepositoryFileState{}, errors.New("repository revision is required")
	}
	if input.Language == "" {
		input.Language = "text"
	}
	var state RepositoryFileState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		version, err := s.observeFileTx(ctx, tx, input.Observation)
		if err != nil {
			return err
		}
		if err := s.invalidateRepositoryFileTx(ctx, tx, version.FileID); err != nil {
			return err
		}
		state = RepositoryFileState{FileID: version.FileID, FileVersionID: version.ID, Path: version.Path, Hash: version.Hash, Size: version.Size, ModifiedAt: version.ModifiedAt, Language: input.Language, RepositoryRevisionID: input.RepositoryRevisionID, Deleted: version.Deleted}
		now := nowUnix(time.Now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_file_state(file_id, workspace_id, file_version_id, path, hash, size, modified_at, language, repository_revision_id, deleted, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(file_id) DO UPDATE SET file_version_id=excluded.file_version_id, path=excluded.path, hash=excluded.hash, size=excluded.size,
			modified_at=excluded.modified_at, language=excluded.language, repository_revision_id=excluded.repository_revision_id, deleted=excluded.deleted, updated_at=excluded.updated_at`,
			state.FileID, s.workspaceID, state.FileVersionID, state.Path, emptyToNull(state.Hash), state.Size, nowUnix(state.ModifiedAt), state.Language, state.RepositoryRevisionID, state.Deleted, now); err != nil {
			return err
		}
		observationID, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_observations(id, workspace_id, repository_revision_id, file_id, file_version_id, path, hash, size, modified_at, language, deleted, observed_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, observationID, s.workspaceID, input.RepositoryRevisionID, state.FileID, state.FileVersionID, state.Path, emptyToNull(state.Hash), state.Size, nowUnix(state.ModifiedAt), state.Language, state.Deleted, now); err != nil {
			return err
		}
		if !state.Deleted {
			if err := s.insertRepositoryDerivedTx(ctx, tx, state, input); err != nil {
				return err
			}
		}
		_, err = s.appendEventTx(ctx, tx, EventInput{Type: "repository.file.indexed", CausationID: input.Observation.Event.ID, Payload: state})
		return err
	})
	return state, err
}

func (s *Store) MarkRepositoryFileDeleted(ctx context.Context, input RepositoryDeleteInput) error {
	if input.RepositoryRevisionID == "" {
		return errors.New("repository revision is required")
	}
	_, err := s.ApplyRepositoryFile(ctx, RepositoryFileInput{RepositoryRevisionID: input.RepositoryRevisionID, Language: "text", Observation: FileObservation{Path: input.Path, Deleted: true, Event: input.Event}})
	return err
}

func (s *Store) invalidateRepositoryFileTx(ctx context.Context, tx *sql.Tx, fileID string) error {
	now := nowUnix(time.Now())
	updates := []struct {
		statement string
		args      []any
	}{
		{"UPDATE repository_symbol_versions SET current = 0, stale_at = ? WHERE current = 1 AND symbol_id IN (SELECT id FROM repository_symbols WHERE file_id = ?)", []any{now, fileID}},
		{"UPDATE repository_chunks SET current = 0, stale_at = ? WHERE current = 1 AND file_id = ?", []any{now, fileID}},
		{"UPDATE repository_relations SET current = 0, stale_at = ? WHERE current = 1 AND (source_file_id = ? OR evidence_file_version_id IN (SELECT id FROM file_versions WHERE file_id = ?))", []any{now, fileID, fileID}},
		{"UPDATE repository_summaries SET current = 0, stale_at = ? WHERE current = 1 AND file_version_id IN (SELECT id FROM file_versions WHERE file_id = ?)", []any{now, fileID}},
		{"UPDATE repository_embeddings SET current = 0, stale_at = ? WHERE current = 1 AND chunk_id IN (SELECT id FROM repository_chunks WHERE file_id = ?)", []any{now, fileID}},
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, update.statement, update.args...); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE repository_fts_state SET current = 0, stale_at = ? WHERE workspace_id = ? AND file_id = ? AND current = 1", now, s.workspaceID, fileID)
	return err
}

func (s *Store) insertRepositoryDerivedTx(ctx context.Context, tx *sql.Tx, file RepositoryFileState, input RepositoryFileInput) error {
	now := nowUnix(time.Now())
	symbolIDs := make(map[string]string, len(input.Symbols))
	for _, symbol := range input.Symbols {
		if symbol.StableKey == "" || symbol.Name == "" || symbol.Kind == "" {
			continue
		}
		id, err := s.repositorySymbolIDTx(ctx, tx, file.FileID, symbol)
		if err != nil {
			return err
		}
		symbolIDs[symbol.StableKey] = id
		versionID, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_symbol_versions(id, symbol_id, file_version_id, source_hash, start_line, start_column, end_line, end_column, signature, doc_comment, current, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, versionID, id, file.FileVersionID, file.Hash, symbol.StartLine, symbol.StartColumn, symbol.EndLine, symbol.EndColumn, symbol.Signature, symbol.DocComment, now); err != nil {
			return err
		}
		if err := s.insertFTSTx(ctx, tx, versionID, file, symbol.QualifiedName, strings.TrimSpace(symbol.Signature+"\n"+symbol.DocComment)); err != nil {
			return err
		}
	}
	for _, chunk := range input.Chunks {
		if chunk.StableKey == "" || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_chunks(id, workspace_id, file_id, file_version_id, symbol_id, stable_key, kind, source_hash, start_line, start_column, end_line, end_column, content, current, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, id, s.workspaceID, file.FileID, file.FileVersionID, emptyToNull(symbolIDs[chunk.SymbolKey]), chunk.StableKey, chunk.Kind, file.Hash, chunk.StartLine, chunk.StartColumn, chunk.EndLine, chunk.EndColumn, chunk.Content, now); err != nil {
			return err
		}
		if err := s.insertFTSTx(ctx, tx, id, file, chunk.StableKey, chunk.Content); err != nil {
			return err
		}
	}
	for _, relation := range input.Relations {
		if relation.Type == "" {
			continue
		}
		id, err := newID()
		if err != nil {
			return err
		}
		provenance, err := json.Marshal(relation.Provenance)
		if err != nil {
			return err
		}
		sourceID := symbolIDs[relation.SourceSymbolKey]
		if sourceID == "" && relation.SourceSymbolKey != "" {
			sourceID, _ = s.repositorySymbolByKeyTx(ctx, tx, relation.SourceSymbolKey)
		}
		targetID := symbolIDs[relation.TargetSymbolKey]
		if targetID == "" && relation.TargetSymbolKey != "" {
			targetID, _ = s.repositorySymbolByKeyTx(ctx, tx, relation.TargetSymbolKey)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_relations(id, workspace_id, source_file_id, source_symbol_id, target_symbol_id, target_name, type, evidence_file_version_id, confidence, provenance, current, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, id, s.workspaceID, file.FileID, emptyToNull(sourceID), emptyToNull(targetID), relation.TargetName, relation.Type, file.FileVersionID, relation.Confidence, provenance, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repository_relations SET target_symbol_id = (
		SELECT id FROM repository_symbols WHERE workspace_id = ? AND stable_key = repository_relations.target_name
	) WHERE workspace_id = ? AND current = 1 AND target_symbol_id IS NULL AND target_name != ''`, s.workspaceID, s.workspaceID); err != nil {
		return err
	}
	for _, summary := range input.Summaries {
		if summary.Scope == "" || summary.TargetStableKey == "" || strings.TrimSpace(summary.Content) == "" {
			continue
		}
		id, err := newID()
		if err != nil {
			return err
		}
		if summary.GenerationMethod == "" {
			summary.GenerationMethod = "deterministic"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_summaries(id, workspace_id, scope, target_stable_key, file_version_id, source_hash, content, generation_method, generation_model, confidence, current, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, id, s.workspaceID, summary.Scope, summary.TargetStableKey, file.FileVersionID, file.Hash, summary.Content, summary.GenerationMethod, summary.GenerationModel, summary.Confidence, now); err != nil {
			return err
		}
		if err := s.insertFTSTx(ctx, tx, id, file, summary.TargetStableKey, summary.Content); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) repositorySymbolByKeyTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM repository_symbols WHERE workspace_id = ? AND stable_key = ?", s.workspaceID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) repositorySymbolIDTx(ctx context.Context, tx *sql.Tx, fileID string, input RepositorySymbolInput) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM repository_symbols WHERE workspace_id = ? AND stable_key = ?", s.workspaceID, input.StableKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var createErr error
		id, createErr = newID()
		if createErr != nil {
			return "", createErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO repository_symbols(id, workspace_id, file_id, stable_key, name, qualified_name, kind, exported, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, s.workspaceID, fileID, input.StableKey, input.Name, input.QualifiedName, input.Kind, input.Exported, nowUnix(time.Now()))
		return id, err
	}
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "UPDATE repository_symbols SET file_id = ?, name = ?, qualified_name = ?, kind = ?, exported = ? WHERE id = ?", fileID, input.Name, input.QualifiedName, input.Kind, input.Exported, id)
	return id, err
}

func (s *Store) insertFTSTx(ctx context.Context, tx *sql.Tx, docID string, file RepositoryFileState, identifier, content string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO repository_fts(doc_id, workspace_id, file_id, file_version_id, path, identifier, content) VALUES(?, ?, ?, ?, ?, ?, ?)", docID, s.workspaceID, file.FileID, file.FileVersionID, file.Path, identifier, content)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO repository_fts_state(doc_id, workspace_id, file_id, file_version_id, current) VALUES(?, ?, ?, ?, 1)", docID, s.workspaceID, file.FileID, file.FileVersionID)
	return err
}

func (s *Store) RepositoryCandidates(ctx context.Context, input RepositoryLookup) ([]RepositoryCandidate, error) {
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, nil
	}
	candidates := make([]RepositoryCandidate, 0, limit)
	seen := make(map[string]bool)
	appendCandidate := func(candidate RepositoryCandidate) {
		if len(candidates) < limit && !seen[candidate.ID] {
			candidate.Provenance = Provenance{Authority: AuthorityDerived}
			seen[candidate.ID] = true
			candidates = append(candidates, candidate)
		}
	}
	pathLike := "%" + query + "%"
	if input.Prefix {
		pathLike = query + "%"
	}
	if input.Kind != "symbol" {
		rows, err := s.db.QueryContext(ctx, `SELECT r.file_id, r.file_version_id, r.path, COALESCE(r.hash, '') FROM repository_file_state r
			WHERE r.workspace_id = ? AND r.deleted = 0 AND r.path LIKE ? ORDER BY CASE WHEN r.path = ? THEN 0 ELSE 1 END, r.path LIMIT ?`, s.workspaceID, pathLike, query, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var candidate RepositoryCandidate
			if err := rows.Scan(&candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Hash); err != nil {
				rows.Close()
				return nil, err
			}
			candidate.ID, candidate.Type, candidate.Score = candidate.FileID, "file", 1
			candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR2, RepresentationR5}
			appendCandidate(candidate)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if input.Kind != "file" {
		symbolRows, err := s.db.QueryContext(ctx, `SELECT s.id, s.file_id, r.file_version_id, r.path, COALESCE(r.hash, ''), s.name, s.qualified_name, s.kind,
			v.start_line, v.start_column, v.end_line, v.end_column, v.signature
			FROM repository_symbols s JOIN repository_symbol_versions v ON v.symbol_id = s.id AND v.current = 1
			JOIN repository_file_state r ON r.file_id = s.file_id
			WHERE s.workspace_id = ? AND r.deleted = 0 AND (s.name LIKE ? OR s.qualified_name LIKE ?) ORDER BY CASE WHEN s.qualified_name = ? OR s.name = ? THEN 0 ELSE 1 END, s.qualified_name LIMIT ?`,
			s.workspaceID, pathLike, pathLike, query, query, limit)
		if err != nil {
			return nil, err
		}
		for symbolRows.Next() {
			var candidate RepositoryCandidate
			if err := symbolRows.Scan(&candidate.SymbolID, &candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Hash, &candidate.Name, &candidate.QualifiedName, &candidate.Kind, &candidate.StartLine, &candidate.StartColumn, &candidate.EndLine, &candidate.EndColumn, &candidate.Signature); err != nil {
				symbolRows.Close()
				return nil, err
			}
			candidate.ID, candidate.Type, candidate.Score = candidate.SymbolID, "symbol", 2
			candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR2, RepresentationR3, RepresentationR4, RepresentationR5}
			appendCandidate(candidate)
		}
		if err := symbolRows.Err(); err != nil {
			symbolRows.Close()
			return nil, err
		}
		if err := symbolRows.Close(); err != nil {
			return nil, err
		}
	}
	if !input.FullText || input.ExactOnly || len(candidates) >= limit {
		return candidates, nil
	}
	ftsRows, err := s.db.QueryContext(ctx, `SELECT f.doc_id, f.file_id, f.file_version_id, f.path, f.content, bm25(repository_fts) FROM repository_fts f
		JOIN repository_fts_state d ON d.doc_id = f.doc_id
		WHERE f.workspace_id = ? AND d.current = 1 AND repository_fts MATCH ? LIMIT ?`, s.workspaceID, ftsSafeQuery(query), limit)
	if err != nil {
		return candidates, nil // malformed local FTS input must not break exact lookup.
	}
	defer ftsRows.Close()
	for ftsRows.Next() {
		var candidate RepositoryCandidate
		if err := ftsRows.Scan(&candidate.ID, &candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Content, &candidate.BM25); err != nil {
			return nil, err
		}
		candidate.Type, candidate.Score = "chunk", -candidate.BM25
		candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR4, RepresentationR5}
		appendCandidate(candidate)
	}
	return candidates, ftsRows.Err()
}

func (s *Store) RepositoryCandidatesByID(ctx context.Context, ids []string) ([]RepositoryCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, s.workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.file_id, c.file_version_id, r.path, COALESCE(r.hash, ''), c.stable_key,
		c.start_line, c.start_column, c.end_line, c.end_column, c.content
		FROM repository_chunks c JOIN repository_file_state r ON r.file_id = c.file_id
		WHERE c.workspace_id = ? AND c.current = 1 AND r.deleted = 0 AND c.id IN (`+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[string]RepositoryCandidate, len(ids))
	for rows.Next() {
		var candidate RepositoryCandidate
		if err := rows.Scan(&candidate.ID, &candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Hash, &candidate.Name,
			&candidate.StartLine, &candidate.StartColumn, &candidate.EndLine, &candidate.EndColumn, &candidate.Content); err != nil {
			return nil, err
		}
		candidate.Type = "chunk"
		candidate.Provenance = Provenance{Authority: AuthorityDerived}
		candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR4, RepresentationR5}
		byID[candidate.ID] = candidate
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	candidates := make([]RepositoryCandidate, 0, len(ids))
	for _, id := range ids {
		if candidate, ok := byID[id]; ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func (s *Store) RepositorySymbolCandidatesByID(ctx context.Context, ids []string) ([]RepositoryCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, s.workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.file_id, r.file_version_id, r.path, COALESCE(r.hash, ''), s.name, s.qualified_name, s.kind,
		v.start_line, v.start_column, v.end_line, v.end_column, v.signature
		FROM repository_symbols s JOIN repository_symbol_versions v ON v.symbol_id = s.id AND v.current = 1
		JOIN repository_file_state r ON r.file_id = s.file_id
		WHERE s.workspace_id = ? AND r.deleted = 0 AND s.id IN (`+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[string]RepositoryCandidate, len(ids))
	for rows.Next() {
		var candidate RepositoryCandidate
		if err := rows.Scan(&candidate.SymbolID, &candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Hash, &candidate.Name, &candidate.QualifiedName, &candidate.Kind,
			&candidate.StartLine, &candidate.StartColumn, &candidate.EndLine, &candidate.EndColumn, &candidate.Signature); err != nil {
			return nil, err
		}
		candidate.ID, candidate.Type = candidate.SymbolID, "symbol"
		candidate.Provenance = Provenance{Authority: AuthorityDerived}
		candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR2, RepresentationR3, RepresentationR4, RepresentationR5}
		byID[candidate.ID] = candidate
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	candidates := make([]RepositoryCandidate, 0, len(ids))
	for _, id := range ids {
		if candidate, ok := byID[id]; ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func (s *Store) RepositoryCurrentChunks(ctx context.Context) ([]RepositoryCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.file_id, c.file_version_id, r.path, COALESCE(r.hash, ''), c.stable_key,
		c.start_line, c.start_column, c.end_line, c.end_column, c.content
		FROM repository_chunks c JOIN repository_file_state r ON r.file_id = c.file_id
		WHERE c.workspace_id = ? AND c.current = 1 AND r.deleted = 0`, s.workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []RepositoryCandidate
	for rows.Next() {
		var candidate RepositoryCandidate
		if err := rows.Scan(&candidate.ID, &candidate.FileID, &candidate.FileVersionID, &candidate.Path, &candidate.Hash, &candidate.Name,
			&candidate.StartLine, &candidate.StartColumn, &candidate.EndLine, &candidate.EndColumn, &candidate.Content); err != nil {
			return nil, err
		}
		candidate.Type = "chunk"
		candidate.Provenance = Provenance{Authority: AuthorityDerived}
		candidate.Representations = []RepresentationLevel{RepresentationR0, RepresentationR1, RepresentationR4, RepresentationR5}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func ftsSafeQuery(query string) string {
	terms := strings.Fields(query)
	for index, term := range terms {
		escaped := strings.ReplaceAll(term, `"`, `""`)
		escaped = strings.ReplaceAll(escaped, `\`, `\\`)
		terms[index] = `"` + escaped + `"`
	}
	return strings.Join(terms, " AND ")
}

func (s *Store) RepositoryNeighbors(ctx context.Context, symbolID string, direction RelationDirection, limit int) ([]RepositoryRelation, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	where := "source_symbol_id = ?"
	if direction == RelationIncoming {
		where = "target_symbol_id = ?"
	} else if direction == RelationBoth {
		where = "(source_symbol_id = ? OR target_symbol_id = ?)"
	}
	args := []any{s.workspaceID, symbolID}
	if direction == RelationBoth {
		args = append(args, symbolID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id, type, COALESCE(source_symbol_id, ''), COALESCE(target_symbol_id, ''), target_name,
		evidence_file_version_id, confidence, provenance FROM repository_relations WHERE workspace_id = ? AND current = 1 AND `+where+" ORDER BY confidence DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RepositoryRelation
	for rows.Next() {
		var relation RepositoryRelation
		var provenance []byte
		if err := rows.Scan(&relation.ID, &relation.Type, &relation.SourceSymbolID, &relation.TargetSymbolID, &relation.TargetName, &relation.EvidenceFileVersionID, &relation.Confidence, &provenance); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(provenance, &relation.Provenance); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (s *Store) RepositoryRepresentations(ctx context.Context, id string) ([]RepositoryRepresentation, error) {
	var path, hash, artifact string
	err := s.db.QueryRowContext(ctx, `SELECT r.path, COALESCE(r.hash, ''), COALESCE(fv.artifact_id, '') FROM repository_file_state r
		JOIN file_versions fv ON fv.id = r.file_version_id WHERE r.workspace_id = ? AND r.file_id = ?`, s.workspaceID, id).Scan(&path, &hash, &artifact)
	if err == nil {
		return []RepositoryRepresentation{{Level: RepresentationR0, Content: path, SourceHash: hash}, {Level: RepresentationR5, ArtifactID: artifact, SourceHash: hash}}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var qualifiedName, signature, docComment, sourceHash string
	var startLine, endLine int
	err = s.db.QueryRowContext(ctx, `SELECT s.qualified_name, v.signature, v.doc_comment, v.start_line, v.end_line, v.source_hash
		FROM repository_symbols s JOIN repository_symbol_versions v ON v.symbol_id = s.id AND v.current = 1 WHERE s.workspace_id = ? AND s.id = ?`, s.workspaceID, id).
		Scan(&qualifiedName, &signature, &docComment, &startLine, &endLine, &sourceHash)
	if err != nil {
		return nil, err
	}
	return []RepositoryRepresentation{
		{Level: RepresentationR0, Content: qualifiedName, StartLine: startLine, EndLine: endLine, SourceHash: sourceHash},
		{Level: RepresentationR2, Content: strings.TrimSpace(qualifiedName + "\n" + docComment), StartLine: startLine, EndLine: endLine, SourceHash: sourceHash},
		{Level: RepresentationR3, Content: signature, StartLine: startLine, EndLine: endLine, SourceHash: sourceHash},
		{Level: RepresentationR4, Content: strings.TrimSpace(signature + "\n" + docComment), StartLine: startLine, EndLine: endLine, SourceHash: sourceHash},
	}, nil
}

func (s *Store) RepositorySemanticSettings(ctx context.Context) (SemanticSettings, error) {
	var settings SemanticSettings
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, "SELECT enabled, updated_at FROM repository_semantic_settings WHERE workspace_id = ?", s.workspaceID).Scan(&settings.Enabled, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	settings.UpdatedAt = fromUnix(updatedAt)
	return settings, err
}

func (s *Store) SetRepositorySemanticSettings(ctx context.Context, settings SemanticSettings) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		now := nowUnix(time.Now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO repository_semantic_settings(workspace_id, enabled, updated_at) VALUES(?, ?, ?)
			ON CONFLICT(workspace_id) DO UPDATE SET enabled=excluded.enabled, updated_at=excluded.updated_at`, s.workspaceID, settings.Enabled, now); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "repository.semantic.updated", Payload: map[string]bool{"enabled": settings.Enabled}})
		return err
	})
}

func (s *Store) PutRepositoryEmbeddings(ctx context.Context, inputs []RepositoryEmbeddingInput) error {
	if len(inputs) == 0 {
		return nil
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		now := nowUnix(time.Now())
		for _, input := range inputs {
			if input.ChunkID == "" || input.SourceHash == "" || input.Model == "" || input.Dimensions <= 0 || len(input.Vector) == 0 {
				return errors.New("invalid repository embedding")
			}
			if _, err := tx.ExecContext(ctx, "UPDATE repository_embeddings SET current = 0, stale_at = ? WHERE workspace_id = ? AND chunk_id = ? AND model = ?", now, s.workspaceID, input.ChunkID, input.Model); err != nil {
				return err
			}
			id, err := newID()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO repository_embeddings(id, workspace_id, chunk_id, source_hash, model, vector, dimensions, current, created_at)
				VALUES(?, ?, ?, ?, ?, ?, ?, 1, ?) ON CONFLICT(workspace_id, chunk_id, model, source_hash) DO UPDATE SET vector=excluded.vector, dimensions=excluded.dimensions, current=1, stale_at=NULL`, id, s.workspaceID, input.ChunkID, input.SourceHash, input.Model, input.Vector, input.Dimensions, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) RepositoryEmbeddings(ctx context.Context, model string) ([]RepositoryEmbedding, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, chunk_id, source_hash, model, vector, dimensions, created_at FROM repository_embeddings WHERE workspace_id = ? AND current = 1 AND model = ?", s.workspaceID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var embeddings []RepositoryEmbedding
	for rows.Next() {
		var embedding RepositoryEmbedding
		var createdAt int64
		if err := rows.Scan(&embedding.ID, &embedding.ChunkID, &embedding.SourceHash, &embedding.Model, &embedding.Vector, &embedding.Dimensions, &createdAt); err != nil {
			return nil, err
		}
		embedding.CreatedAt = fromUnix(createdAt)
		embeddings = append(embeddings, embedding)
	}
	return embeddings, rows.Err()
}

var _ RepositoryIndexStore = (*Store)(nil)
