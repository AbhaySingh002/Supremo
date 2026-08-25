package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRepositoryRemainsCleanOnOpen(t *testing.T) {
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseWorkspace(root)

	// Save session, message, artifact
	ctx := context.Background()
	_, err = store.SaveSession(ctx, SessionInput{ID: "clean-session", Name: "Clean"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutArtifact(ctx, ArtifactInput{Data: []byte("test-data"), ContentType: "text/plain", Origin: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the workspace root directory remains 100% clean (no .supremo folder)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 files in workspace root, found %d: %#v", len(entries), entries)
	}

	// Verify global storage exists
	if _, err := os.Stat(WorkspaceDBPath(store.WorkspaceID())); err != nil {
		t.Fatalf("expected global workspace DB to exist: %v", err)
	}
}

func TestLegacySupremoMigration(t *testing.T) {
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	root := t.TempDir()
	legacyDir := filepath.Join(root, ".supremo")
	legacyStateDir := filepath.Join(legacyDir, "state")
	legacyObjectsDir := filepath.Join(legacyDir, "objects", "ab")
	if err := os.MkdirAll(legacyStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyObjectsDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Create a mock legacy database with workspaces, sessions, and messages
	legacyDBPath := filepath.Join(legacyStateDir, "state.db")
	db, err := sql.Open("sqlite", legacyDBPath)
	if err != nil {
		t.Fatal(err)
	}
	createSchema := `
	CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
	CREATE TABLE workspaces (id TEXT PRIMARY KEY, root TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL, created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL, status TEXT NOT NULL, current_task_id TEXT, provider TEXT, model TEXT,
		metadata BLOB NOT NULL, data BLOB NOT NULL, version INTEGER NOT NULL
	);
	CREATE TABLE messages (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, session_id TEXT NOT NULL, sequence INTEGER NOT NULL,
		role TEXT NOT NULL, task_id TEXT, state TEXT NOT NULL, parent_id TEXT, created_at INTEGER NOT NULL
	);
	CREATE TABLE message_parts (
		message_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, text BLOB, artifact_id TEXT,
		metadata BLOB NOT NULL, PRIMARY KEY(message_id, ordinal)
	);
	CREATE TABLE artifacts (
		hash TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, size INTEGER NOT NULL, content_type TEXT NOT NULL,
		origin TEXT NOT NULL, created_at INTEGER NOT NULL
	);
	CREATE TABLE documents (
		id TEXT NOT NULL, kind TEXT NOT NULL, workspace_id TEXT NOT NULL, session_id TEXT, status TEXT NOT NULL,
		version INTEGER NOT NULL, payload BLOB NOT NULL, provenance BLOB NOT NULL, created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL, PRIMARY KEY(id, kind, version)
	);
	CREATE TABLE documents_current (
		id TEXT NOT NULL, kind TEXT NOT NULL, workspace_id TEXT NOT NULL, session_id TEXT, status TEXT NOT NULL,
		version INTEGER NOT NULL, payload BLOB NOT NULL, provenance BLOB NOT NULL, created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL, PRIMARY KEY(id, kind)
	);
	CREATE TABLE claims (
		id TEXT PRIMARY KEY, lineage_id TEXT NOT NULL, workspace_id TEXT NOT NULL, kind TEXT NOT NULL,
		statement TEXT NOT NULL, scope TEXT, status TEXT NOT NULL, confidence REAL NOT NULL, provenance BLOB NOT NULL,
		supersedes_id TEXT, superseded_by_id TEXT, created_at INTEGER NOT NULL
	);
	CREATE TABLE workspace_revisions (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, head TEXT, branch TEXT, dirty INTEGER NOT NULL,
		metadata BLOB NOT NULL, observed_at INTEGER NOT NULL
	);
	CREATE TABLE files (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, current_path TEXT NOT NULL, deleted INTEGER NOT NULL,
		created_at INTEGER NOT NULL, UNIQUE(workspace_id, current_path)
	);
	CREATE TABLE file_versions (
		id TEXT PRIMARY KEY, file_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT, size INTEGER NOT NULL,
		deleted INTEGER NOT NULL, modified_at INTEGER NOT NULL, workspace_revision_id TEXT, artifact_id TEXT,
		observed_at INTEGER NOT NULL
	);
	CREATE TABLE legacy_imports (
		workspace_id TEXT NOT NULL, source TEXT NOT NULL, source_hash TEXT NOT NULL, imported_at INTEGER NOT NULL,
		PRIMARY KEY(workspace_id, source)
	);
	CREATE TABLE workspace_memory (workspace_id TEXT PRIMARY KEY, content BLOB NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE events (
		sequence INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL UNIQUE, workspace_id TEXT NOT NULL,
		session_id TEXT, agent_id TEXT, type TEXT NOT NULL, correlation_id TEXT, causation_id TEXT,
		idempotency_key TEXT, payload_version INTEGER NOT NULL, payload BLOB NOT NULL, created_at INTEGER NOT NULL
	);
	`
	if _, err := db.Exec(createSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES(1, 1000), (2, 1000), (3, 1000), (4, 1000)"); err != nil {
		t.Fatal(err)
	}

	legacyID := "ws-legacy-12345"
	if _, err := db.Exec("INSERT INTO workspaces(id, root, created_at) VALUES(?, ?, ?)", legacyID, root, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sessions(id, workspace_id, name, created_at, updated_at, status, metadata, data, version) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"sess-legacy", legacyID, "Legacy Session", 1000, 1000, "active", []byte("{}"), []byte("{}"), 1); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Write mock CAS object in legacy location
	legacyArtifactFile := filepath.Join(legacyObjectsDir, "abcdef123456")
	if err := os.WriteFile(legacyArtifactFile, []byte("legacy-cas-object-content"), 0600); err != nil {
		t.Fatal(err)
	}

	// Now Open the store through the new global architecture
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseWorkspace(root)

	if store.WorkspaceID() != legacyID {
		t.Fatalf("expected workspace ID %s, got %s", legacyID, store.WorkspaceID())
	}

	// Verify session was migrated
	sess, err := store.Session(context.Background(), "sess-legacy")
	if err != nil || sess.Name != "Legacy Session" {
		t.Fatalf("failed to retrieve migrated session: %v, %#v", err, sess)
	}

	// Verify CAS object was migrated
	destArtifactFile := filepath.Join(WorkspaceObjectsDir(legacyID), "ab", "abcdef123456")
	data, err := os.ReadFile(destArtifactFile)
	if err != nil || string(data) != "legacy-cas-object-content" {
		t.Fatalf("failed to read migrated CAS object: %v, %q", err, string(data))
	}

	// Verify migration marker was created in legacy directory
	if _, err := os.Stat(filepath.Join(legacyDir, ".migrated")); err != nil {
		t.Fatalf("expected .migrated marker file: %v", err)
	}
}

func TestMovedWorkspacePreservesIdentity(t *testing.T) {
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	origRoot := t.TempDir()
	gitDir := filepath.Join(origRoot, ".git")
	if err := os.MkdirAll(gitDir, 0700); err != nil {
		t.Fatal(err)
	}
	gitConfig := "[remote \"origin\"]\n\turl = git@github.com:example/repo.git\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
		t.Fatal(err)
	}

	store1, err := Open(origRoot)
	if err != nil {
		t.Fatal(err)
	}
	wsID1 := store1.WorkspaceID()
	ctx := context.Background()
	if _, err := store1.SaveSession(ctx, SessionInput{ID: "moved-session", Name: "Moved Repo Session", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	_ = CloseWorkspace(origRoot)

	// Simulate moving the project to a new directory
	newRoot := t.TempDir()
	newGitDir := filepath.Join(newRoot, ".git")
	if err := os.MkdirAll(newGitDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newGitDir, "config"), []byte(gitConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
		t.Fatal(err)
	}

	store2, err := Open(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseWorkspace(newRoot)

	if store2.WorkspaceID() != wsID1 {
		t.Fatalf("expected moved workspace to retain workspace ID %s, got %s", wsID1, store2.WorkspaceID())
	}

	sess, err := store2.Session(ctx, "moved-session")
	if err != nil || sess.Name != "Moved Repo Session" {
		t.Fatalf("failed to retrieve session from moved workspace: %v, %#v", err, sess)
	}
}
