package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// MigrateLegacyWorkspace checks if a legacy project/.supremo exists and safely
// migrates its state.db and CAS objects to the new global location.
// It never mutates or deletes the legacy files.
func MigrateLegacyWorkspace(ctx context.Context, root string, workspaceID string) error {
	clean, err := canonicalRoot(root)
	if err != nil {
		return err
	}

	legacyBase := filepath.Join(clean, stateDirectory)
	legacyDB := filepath.Join(legacyBase, "state", databaseFilename)
	legacyObjects := filepath.Join(legacyBase, "objects")

	if _, err := os.Stat(legacyDB); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // No legacy state to migrate
		}
		return err
	}

	destDir := WorkspaceDir(workspaceID)
	destDB := WorkspaceDBPath(workspaceID)
	destObjects := WorkspaceObjectsDir(workspaceID)

	// Check if already migrated
	if _, err := os.Stat(destDB); err == nil {
		return nil // Destination already exists and is ready
	}

	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("create destination workspace dir: %w", err)
	}
	if err := os.MkdirAll(destObjects, 0700); err != nil {
		return fmt.Errorf("create destination objects dir: %w", err)
	}

	// 1. Flush source SQLite WAL if accessible
	if srcDB, err := sql.Open("sqlite", legacyDB); err == nil {
		_, _ = srcDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		_ = srcDB.Close()
	}

	// 2. Safely copy database to temporary file first
	tmpDB := destDB + ".migrating"
	_ = os.Remove(tmpDB)

	if err := copyFile(legacyDB, tmpDB); err != nil {
		_ = os.Remove(tmpDB)
		return fmt.Errorf("copy legacy database: %w", err)
	}

	// Also copy WAL and SHM if they exist
	_ = copyFile(legacyDB+"-wal", tmpDB+"-wal")
	_ = copyFile(legacyDB+"-shm", tmpDB+"-shm")

	// 3. Verify SQLite integrity of copied database
	checkDB, err := sql.Open("sqlite", tmpDB)
	if err != nil {
		_ = os.Remove(tmpDB)
		return fmt.Errorf("open copied database for verification: %w", err)
	}
	var integrity string
	checkErr := checkDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	_ = checkDB.Close()
	if checkErr != nil || integrity != "ok" {
		_ = os.Remove(tmpDB)
		_ = os.Remove(tmpDB + "-wal")
		_ = os.Remove(tmpDB + "-shm")
		return fmt.Errorf("migrated database failed integrity check (%s): %w", integrity, checkErr)
	}

	// Atomic rename to final destination
	if err := os.Rename(tmpDB, destDB); err != nil {
		_ = os.Remove(tmpDB)
		return fmt.Errorf("finalize migrated database: %w", err)
	}
	_ = os.Rename(tmpDB+"-wal", destDB+"-wal")
	_ = os.Rename(tmpDB+"-shm", destDB+"-shm")

	// 4. Copy CAS objects
	if info, err := os.Stat(legacyObjects); err == nil && info.IsDir() {
		_ = filepath.Walk(legacyObjects, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(legacyObjects, path)
			if relErr != nil {
				return nil
			}
			targetPath := filepath.Join(destObjects, rel)
			_ = os.MkdirAll(filepath.Dir(targetPath), 0700)
			_ = copyFile(path, targetPath)
			return nil
		})
	}

	// Write marker in legacy directory
	_ = os.WriteFile(filepath.Join(legacyBase, ".migrated"), []byte(time.Now().UTC().Format(time.RFC3339)), 0600)

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
