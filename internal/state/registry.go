package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	registryMu sync.Mutex
	globalDB   *sql.DB
)

type WorkspaceInfo struct {
	ID                 string
	CanonicalPath      string
	GitRoot            string
	GitRemote          string
	RepositoryIdentity string
	LastSessionID      string
	StorageVersion     int
	CreatedAt          int64
	LastSeenAt         int64
}

// openGlobalDB returns a singleton handle to the global discovery SQLite database.
func openGlobalDB() (*sql.DB, error) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if globalDB != nil {
		if err := globalDB.Ping(); err == nil {
			return globalDB, nil
		}
		_ = globalDB.Close()
		globalDB = nil
	}

	dbPath := GlobalDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("create global data directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open global database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure global database: %w", err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		canonical_path TEXT NOT NULL UNIQUE,
		git_root TEXT,
		git_remote TEXT,
		repository_identity TEXT,
		last_session_id TEXT,
		storage_version INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workspaces_git ON workspaces(git_root, git_remote);
	CREATE INDEX IF NOT EXISTS idx_workspaces_identity ON workspaces(repository_identity);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate global database: %w", err)
	}

	globalDB = db
	return globalDB, nil
}

// ResolveWorkspaceIdentity resolves or registers the durable WorkspaceID for a given directory root.
// It handles:
// 1. Canonical path matching.
// 2. Moved workspace reconciliation via Git repository identity.
// 3. Legacy project/.supremo discovery to preserve existing workspace IDs during migration.
// 4. Fresh workspace registration.
func ResolveWorkspaceIdentity(ctx context.Context, root string) (string, error) {
	clean, err := canonicalRoot(root)
	if err != nil {
		return "", err
	}

	gdb, err := openGlobalDB()
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()

	// 1. Check exact canonical path match
	var existingID string
	err = gdb.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE canonical_path = ?", clean).Scan(&existingID)
	if err == nil {
		_, _ = gdb.ExecContext(ctx, "UPDATE workspaces SET last_seen_at = ? WHERE id = ?", now, existingID)
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	// 2. Inspect Git repository identity for moved projects
	gitRoot, gitRemote, repoID := inspectGitMetadata(clean)
	if repoID != "" {
		var movedID string
		err = gdb.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE repository_identity = ?", repoID).Scan(&movedID)
		if err == nil {
			// Reconcile moved project path
			_, _ = gdb.ExecContext(ctx, "UPDATE workspaces SET canonical_path = ?, git_root = ?, last_seen_at = ? WHERE id = ?", clean, gitRoot, now, movedID)
			return movedID, nil
		}
	} else if gitRemote != "" {
		var movedID string
		err = gdb.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE git_remote = ?", gitRemote).Scan(&movedID)
		if err == nil {
			_, _ = gdb.ExecContext(ctx, "UPDATE workspaces SET canonical_path = ?, git_root = ?, last_seen_at = ? WHERE id = ?", clean, gitRoot, now, movedID)
			return movedID, nil
		}
	}

	// 3. Check legacy project/.supremo/state/state.db for existing workspace ID
	legacyDB := filepath.Join(clean, stateDirectory, "state", databaseFilename)
	if _, statErr := os.Stat(legacyDB); statErr == nil {
		if legacyID, readErr := readLegacyWorkspaceID(legacyDB); readErr == nil && legacyID != "" {
			_, _ = gdb.ExecContext(ctx, `INSERT INTO workspaces(id, canonical_path, git_root, git_remote, repository_identity, created_at, last_seen_at)
				VALUES(?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET canonical_path = excluded.canonical_path, last_seen_at = excluded.last_seen_at`,
				legacyID, clean, gitRoot, gitRemote, repoID, now, now)
			return legacyID, nil
		}
	}

	// 4. Generate new durable Workspace ID
	newID, err := newWorkspaceID()
	if err != nil {
		return "", err
	}

	_, err = gdb.ExecContext(ctx, `INSERT INTO workspaces(id, canonical_path, git_root, git_remote, repository_identity, created_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, newID, clean, gitRoot, gitRemote, repoID, now, now)
	if err != nil {
		// Handle potential race condition on canonical_path
		var raceID string
		if qErr := gdb.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE canonical_path = ?", clean).Scan(&raceID); qErr == nil {
			return raceID, nil
		}
		return "", err
	}

	return newID, nil
}

func inspectGitMetadata(root string) (gitRoot string, gitRemote string, repoIdentity string) {
	curr := root
	for {
		gitDir := filepath.Join(curr, ".git")
		info, err := os.Stat(gitDir)
		if err == nil {
			if info.IsDir() {
				gitRoot = curr
				// Read config
				configFile := filepath.Join(gitDir, "config")
				if data, err := os.ReadFile(configFile); err == nil {
					gitRemote = extractGitRemote(string(data))
				}
				if gitRemote != "" {
					repoIdentity = "remote:" + gitRemote
				}
			}
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" {
			break
		}
		curr = parent
	}
	return gitRoot, gitRemote, repoIdentity
}

func extractGitRemote(config string) string {
	lines := strings.Split(config, "\n")
	inRemote := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[remote \"origin\"]") {
			inRemote = true
			continue
		}
		if inRemote {
			if strings.HasPrefix(trimmed, "[") {
				inRemote = false
				continue
			}
			if strings.HasPrefix(trimmed, "url =") {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, "url ="))
			}
		}
	}
	return ""
}

func readLegacyWorkspaceID(dbPath string) (string, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var id string
	err = db.QueryRow("SELECT id FROM workspaces LIMIT 1").Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func newWorkspaceID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "ws-" + hex.EncodeToString(buf[:8]), nil
}
