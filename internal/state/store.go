package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/storage"
	_ "modernc.org/sqlite"
)

const (
	stateDirectory   = ".supremo"
	databaseFilename = "state.db"
)

var repositories = struct {
	sync.Mutex
	byRoot map[string]*Store
}{byRoot: make(map[string]*Store)}

// Store is the workspace-local SQLite implementation of Repository.
type Store struct {
	root        string
	database    string
	objects     string
	workspaceID string
	db          *sql.DB
	mu          sync.Mutex
	pending     []Event
	subscribers map[uint64]*eventSubscriber
	nextSubID   uint64
}

var _ Repository = (*Store)(nil)

// Open returns the process-wide repository for one workspace. Keeping one
// connection per workspace lets SQLite serialize durable writes predictably.
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

// CloseWorkspace closes the cached database for root. Tests and app shutdown
// use it; callers never need to close a shared store themselves.
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
	return store.db.Close()
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

func open(ctx context.Context, root string) (*Store, error) {
	clean, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}

	workspaceID, err := ResolveWorkspaceIdentity(ctx, clean)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace identity: %w", err)
	}

	// Safely migrate legacy .supremo directory if present and not yet migrated
	if err := MigrateLegacyWorkspace(ctx, clean, workspaceID); err != nil {
		return nil, fmt.Errorf("migrate legacy workspace: %w", err)
	}

	workspaceDir := WorkspaceDir(workspaceID)
	objects := WorkspaceObjectsDir(workspaceID)
	if err := os.MkdirAll(workspaceDir, 0700); err != nil {
		return nil, fmt.Errorf("create workspace storage directory: %w", err)
	}
	if err := os.MkdirAll(objects, 0700); err != nil {
		return nil, fmt.Errorf("create workspace objects directory: %w", err)
	}

	database := WorkspaceDBPath(workspaceID)
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	// ponytail: one local agent currently writes each workspace; add read-pool
	// concurrency only after profiling proves this single durable writer hurts UI latency.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping state database: %w", err)
	}
	for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA synchronous = FULL"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure state database: %w", err)
		}
	}
	store := &Store{root: clean, database: database, objects: objects, workspaceID: workspaceID, db: db, subscribers: make(map[uint64]*eventSubscriber)}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.initializeWorkspace(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) WorkspaceID() string { return s.workspaceID }

// Root is the canonical workspace path this local store owns.
func (s *Store) Root() string { return s.root }

// RecordLegacyImport records the exact source hash only after a caller has
// completed its transactional migration work. Legacy files are never modified.
func (s *Store) RecordLegacyImport(ctx context.Context, source string, data []byte) error {
	hash := sha256.Sum256(data)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_imports(workspace_id, source, source_hash, imported_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(workspace_id, source) DO NOTHING`, s.workspaceID, source, hex.EncodeToString(hash[:]), nowUnix(time.Now())); err != nil {
			return err
		}
		_, err := s.appendEventTx(ctx, tx, EventInput{Type: "legacy.imported", IdempotencyKey: "legacy:" + source + ":" + hex.EncodeToString(hash[:]), Payload: map[string]any{"source": source, "hash": hex.EncodeToString(hash[:])}})
		return err
	})
}

func (s *Store) initializeWorkspace(ctx context.Context) error {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE id = ?", s.workspaceID).Scan(&id)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, "UPDATE workspaces SET root = ? WHERE id = ?", s.root, s.workspaceID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspaces(id, root, created_at) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET root = excluded.root`, s.workspaceID, s.root, nowUnix(time.Now()))
	return err
}

type migration struct {
	version int
	sql     string
}

var migrations = []migration{{version: 1, sql: `
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE workspaces (id TEXT PRIMARY KEY, root TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE TABLE events (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL UNIQUE, workspace_id TEXT NOT NULL,
  session_id TEXT, agent_id TEXT, type TEXT NOT NULL, correlation_id TEXT, causation_id TEXT,
  idempotency_key TEXT, payload_version INTEGER NOT NULL, payload BLOB NOT NULL, created_at INTEGER NOT NULL,
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE UNIQUE INDEX events_idempotency ON events(workspace_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != '';
CREATE INDEX events_session_sequence ON events(workspace_id, session_id, sequence);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, status TEXT NOT NULL, current_task_id TEXT, provider TEXT, model TEXT,
  metadata BLOB NOT NULL, data BLOB NOT NULL, version INTEGER NOT NULL,
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX sessions_visible ON sessions(workspace_id, status, updated_at DESC);
CREATE TABLE messages (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, session_id TEXT NOT NULL, sequence INTEGER NOT NULL,
  role TEXT NOT NULL, task_id TEXT, state TEXT NOT NULL, parent_id TEXT, created_at INTEGER NOT NULL,
  FOREIGN KEY(session_id) REFERENCES sessions(id), UNIQUE(session_id, sequence)
);
CREATE INDEX messages_session_sequence ON messages(workspace_id, session_id, sequence);
CREATE TABLE message_parts (
  message_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, text BLOB, artifact_id TEXT,
  metadata BLOB NOT NULL, PRIMARY KEY(message_id, ordinal), FOREIGN KEY(message_id) REFERENCES messages(id)
);
CREATE TABLE artifacts (
  hash TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, size INTEGER NOT NULL, content_type TEXT NOT NULL,
  origin TEXT NOT NULL, created_at INTEGER NOT NULL, FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE documents (
  id TEXT NOT NULL, kind TEXT NOT NULL, workspace_id TEXT NOT NULL, session_id TEXT, status TEXT NOT NULL,
  version INTEGER NOT NULL, payload BLOB NOT NULL, provenance BLOB NOT NULL, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, PRIMARY KEY(id, kind, version), FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE documents_current (
  id TEXT NOT NULL, kind TEXT NOT NULL, workspace_id TEXT NOT NULL, session_id TEXT, status TEXT NOT NULL,
  version INTEGER NOT NULL, payload BLOB NOT NULL, provenance BLOB NOT NULL, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, PRIMARY KEY(id, kind), FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX documents_current_session ON documents_current(workspace_id, kind, session_id, updated_at DESC);
CREATE TABLE claims (
  id TEXT PRIMARY KEY, lineage_id TEXT NOT NULL, workspace_id TEXT NOT NULL, kind TEXT NOT NULL,
  statement TEXT NOT NULL, scope TEXT, status TEXT NOT NULL, confidence REAL NOT NULL, provenance BLOB NOT NULL,
  supersedes_id TEXT, superseded_by_id TEXT, created_at INTEGER NOT NULL, FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX claims_current ON claims(workspace_id, kind, superseded_by_id, created_at DESC);
CREATE TABLE workspace_revisions (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, head TEXT, branch TEXT, dirty INTEGER NOT NULL,
  metadata BLOB NOT NULL, observed_at INTEGER NOT NULL, FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE files (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, current_path TEXT NOT NULL, deleted INTEGER NOT NULL,
  created_at INTEGER NOT NULL, FOREIGN KEY(workspace_id) REFERENCES workspaces(id), UNIQUE(workspace_id, current_path)
);
CREATE TABLE file_versions (
  id TEXT PRIMARY KEY, file_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT, size INTEGER NOT NULL,
  deleted INTEGER NOT NULL, modified_at INTEGER NOT NULL, workspace_revision_id TEXT, artifact_id TEXT,
  observed_at INTEGER NOT NULL, FOREIGN KEY(file_id) REFERENCES files(id), FOREIGN KEY(artifact_id) REFERENCES artifacts(hash)
);
CREATE INDEX file_versions_file ON file_versions(file_id, observed_at);
CREATE TABLE legacy_imports (
  workspace_id TEXT NOT NULL, source TEXT NOT NULL, source_hash TEXT NOT NULL, imported_at INTEGER NOT NULL,
  PRIMARY KEY(workspace_id, source), FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE workspace_memory (workspace_id TEXT PRIMARY KEY, content BLOB NOT NULL, updated_at INTEGER NOT NULL,
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id));
`}, {version: 2, sql: `
CREATE TABLE file_paths (
  file_id TEXT NOT NULL, path TEXT NOT NULL, observed_at INTEGER NOT NULL,
  PRIMARY KEY(file_id, path), FOREIGN KEY(file_id) REFERENCES files(id)
);
CREATE INDEX file_paths_path ON file_paths(path);
`}, {version: 3, sql: `
CREATE TABLE repository_revisions (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, workspace_revision_id TEXT,
  head TEXT, branch TEXT, dirty INTEGER NOT NULL, scanner_version TEXT NOT NULL,
  observed_at INTEGER NOT NULL, FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX repository_revisions_workspace ON repository_revisions(workspace_id, observed_at DESC);
CREATE TABLE repository_file_state (
  file_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, file_version_id TEXT, path TEXT NOT NULL,
  hash TEXT, size INTEGER NOT NULL, modified_at INTEGER NOT NULL, language TEXT NOT NULL,
  repository_revision_id TEXT, deleted INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  FOREIGN KEY(file_id) REFERENCES files(id), FOREIGN KEY(file_version_id) REFERENCES file_versions(id)
);
CREATE INDEX repository_file_state_path ON repository_file_state(workspace_id, path);
CREATE TABLE repository_observations (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, repository_revision_id TEXT NOT NULL,
  file_id TEXT NOT NULL, file_version_id TEXT, path TEXT NOT NULL, hash TEXT,
  size INTEGER NOT NULL, modified_at INTEGER NOT NULL, language TEXT NOT NULL, deleted INTEGER NOT NULL,
  observed_at INTEGER NOT NULL, FOREIGN KEY(file_id) REFERENCES files(id)
);
CREATE INDEX repository_observations_revision ON repository_observations(repository_revision_id, file_id);
CREATE TABLE repository_symbols (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, file_id TEXT NOT NULL, stable_key TEXT NOT NULL,
  name TEXT NOT NULL, qualified_name TEXT NOT NULL, kind TEXT NOT NULL, exported INTEGER NOT NULL,
  created_at INTEGER NOT NULL, UNIQUE(workspace_id, stable_key), FOREIGN KEY(file_id) REFERENCES files(id)
);
CREATE INDEX repository_symbols_lookup ON repository_symbols(workspace_id, name, qualified_name);
CREATE TABLE repository_symbol_versions (
  id TEXT PRIMARY KEY, symbol_id TEXT NOT NULL, file_version_id TEXT NOT NULL, source_hash TEXT NOT NULL,
  start_line INTEGER NOT NULL, start_column INTEGER NOT NULL, end_line INTEGER NOT NULL, end_column INTEGER NOT NULL,
  signature TEXT NOT NULL, doc_comment TEXT NOT NULL, current INTEGER NOT NULL, created_at INTEGER NOT NULL,
  stale_at INTEGER, FOREIGN KEY(symbol_id) REFERENCES repository_symbols(id), FOREIGN KEY(file_version_id) REFERENCES file_versions(id)
);
CREATE INDEX repository_symbol_versions_current ON repository_symbol_versions(symbol_id, current);
CREATE TABLE repository_chunks (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, file_id TEXT NOT NULL, file_version_id TEXT NOT NULL,
  symbol_id TEXT, stable_key TEXT NOT NULL, kind TEXT NOT NULL, source_hash TEXT NOT NULL,
  start_line INTEGER NOT NULL, start_column INTEGER NOT NULL, end_line INTEGER NOT NULL, end_column INTEGER NOT NULL,
  content TEXT NOT NULL, current INTEGER NOT NULL, created_at INTEGER NOT NULL, stale_at INTEGER,
  UNIQUE(workspace_id, file_version_id, stable_key), FOREIGN KEY(file_id) REFERENCES files(id)
);
CREATE INDEX repository_chunks_current ON repository_chunks(workspace_id, file_id, current);
CREATE TABLE repository_relations (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, source_file_id TEXT, source_symbol_id TEXT,
  target_file_id TEXT, target_symbol_id TEXT, target_name TEXT NOT NULL, type TEXT NOT NULL,
  evidence_file_version_id TEXT NOT NULL, confidence REAL NOT NULL, provenance BLOB NOT NULL,
  current INTEGER NOT NULL, created_at INTEGER NOT NULL, stale_at INTEGER
);
CREATE INDEX repository_relations_source ON repository_relations(workspace_id, source_symbol_id, current);
CREATE INDEX repository_relations_target ON repository_relations(workspace_id, target_symbol_id, current);
CREATE TABLE repository_summaries (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, scope TEXT NOT NULL, target_stable_key TEXT NOT NULL,
  file_version_id TEXT, source_hash TEXT NOT NULL, content TEXT NOT NULL, generation_method TEXT NOT NULL,
  generation_model TEXT NOT NULL, confidence REAL NOT NULL, current INTEGER NOT NULL,
  created_at INTEGER NOT NULL, stale_at INTEGER
);
CREATE INDEX repository_summaries_current ON repository_summaries(workspace_id, target_stable_key, current);
CREATE VIRTUAL TABLE repository_fts USING fts5(
  doc_id UNINDEXED, workspace_id UNINDEXED, file_id UNINDEXED, file_version_id UNINDEXED,
  path, identifier, content, tokenize='unicode61'
);
CREATE TABLE repository_fts_state (
  doc_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, file_id TEXT NOT NULL,
  file_version_id TEXT NOT NULL, current INTEGER NOT NULL, stale_at INTEGER
);
CREATE INDEX repository_fts_state_current ON repository_fts_state(workspace_id, file_id, current);
CREATE TABLE repository_semantic_settings (
  workspace_id TEXT PRIMARY KEY, enabled INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE repository_embeddings (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, chunk_id TEXT NOT NULL, source_hash TEXT NOT NULL,
  model TEXT NOT NULL, vector BLOB NOT NULL, dimensions INTEGER NOT NULL, current INTEGER NOT NULL,
  created_at INTEGER NOT NULL, stale_at INTEGER, UNIQUE(workspace_id, chunk_id, model, source_hash)
);
CREATE INDEX repository_embeddings_current ON repository_embeddings(workspace_id, model, current);
`}, {version: 4, sql: `
CREATE TABLE IF NOT EXISTS repository_fts_state (
  doc_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, file_id TEXT NOT NULL,
  file_version_id TEXT NOT NULL, current INTEGER NOT NULL, stale_at INTEGER
);
CREATE INDEX IF NOT EXISTS repository_fts_state_current ON repository_fts_state(workspace_id, file_id, current);
INSERT OR IGNORE INTO repository_fts_state(doc_id, workspace_id, file_id, file_version_id, current)
  SELECT doc_id, workspace_id, file_id, file_version_id, 1 FROM repository_fts;
`}}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)"); err != nil {
		return err
	}
	for _, migration := range migrations {
		var applied int
		err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = ?", migration.version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migration.sql); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)", migration.version, nowUnix(time.Now()))
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply state migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit state migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer func() {
		s.pending = nil
		s.mu.Unlock()
	}()
	s.pending = nil
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishCommittedLocked(s.pending)
	return nil
}

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

func nowUnix(value time.Time) int64 {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().UnixNano()
}

func fromUnix(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
