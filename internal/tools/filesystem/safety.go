package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/storage"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// FileTarget represents a normalized, workspace-scoped file target identity.
type FileTarget struct {
	WorkspaceRoot string
	AbsPath       string
	RelPath       string
}

// ResolveTarget normalizes rawPath against the workspace root from context.
func ResolveTarget(ctx context.Context, rawPath string) (FileTarget, error) {
	root := tools.Workspace(ctx)
	absPath, err := tools.ValidateAndResolvePath(ctx, rawPath)
	if err != nil {
		return FileTarget{}, err
	}
	relPath := state.NormalizePath(absPath, root)
	return FileTarget{
		WorkspaceRoot: root,
		AbsPath:       absPath,
		RelPath:       relPath,
	}, nil
}

var (
	targetLocksMu sync.Mutex
	targetLocks   = make(map[string]*sync.Mutex)
	writerMu      sync.RWMutex
	// ponytail: writer identity is process-local; persist it only if conflict
	// diagnostics must survive a backend restart.
	lastWriter = make(map[string]string)

	// memoryObsFallback tracks in-memory observations for unit tests without a SQLite store.
	memoryObsMu       sync.RWMutex
	memoryObsFallback = make(map[string]map[string]state.Observation) // sessionID -> relPath -> Observation
)

// withTargetLock acquires a fine-grained mutex for the target path during mutation.
func withTargetLock[T any](targetKey string, fn func() (T, error)) (T, error) {
	targetLocksMu.Lock()
	mu, ok := targetLocks[targetKey]
	if !ok {
		mu = &sync.Mutex{}
		targetLocks[targetKey] = mu
	}
	targetLocksMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// GetTrustedObservation returns the latest trusted observation from durable state
// without performing disk I/O. The only authoritative disk freshness check happens
// at mutation time inside ValidateAndExecuteMutation.
func GetTrustedObservation(ctx context.Context, sessionID string, target FileTarget) (state.Observation, bool, error) {
	if target.RelPath == "" {
		return state.Observation{}, false, nil
	}
	if target.WorkspaceRoot != "" {
		if store, err := state.Open(target.WorkspaceRoot); err == nil && store != nil {
			if obs, found, err := store.LatestFileObservation(ctx, sessionID, target.RelPath); err == nil && found {
				return obs, true, nil
			}
		}
	}
	if sessionID != "" {
		memoryObsMu.RLock()
		if sessionMap, ok := memoryObsFallback[sessionID]; ok {
			if obs, found := sessionMap[target.RelPath]; found {
				memoryObsMu.RUnlock()
				return obs, true, nil
			}
		}
		memoryObsMu.RUnlock()
	}
	return state.Observation{}, false, nil
}

// RecordTrustedObservation updates the durable store and fallback memory with a fresh observation.
func RecordTrustedObservation(ctx context.Context, sessionID string, target FileTarget, toolName string, sourceHash string, negative bool) {
	if target.RelPath == "" {
		return
	}
	fp, cArgs, _, _ := state.ComputeCallFingerprint(toolName, map[string]any{"path": target.RelPath}, target.WorkspaceRoot)
	obs := state.Observation{
		SessionID:       sessionID,
		Tool:            toolName,
		CallFingerprint: fp,
		CanonicalArgs:   cArgs,
		Path:            target.RelPath,
		SourceHash:      sourceHash,
		Negative:        negative,
		Version:         state.CurrentObservationVersion,
		CreatedAt:       time.Now().UTC(),
	}
	if target.WorkspaceRoot != "" && sessionID != "" {
		if store, err := state.Open(target.WorkspaceRoot); err == nil && store != nil {
			_, _ = store.SaveObservation(ctx, obs)
		}
	}
	if sessionID != "" {
		memoryObsMu.Lock()
		if _, ok := memoryObsFallback[sessionID]; !ok {
			memoryObsFallback[sessionID] = make(map[string]state.Observation)
		}
		memoryObsFallback[sessionID][target.RelPath] = obs
		memoryObsMu.Unlock()
	}
}

// FileNotObservedError occurs when an edit/overwrite is attempted on a file not yet observed in the session.
type FileNotObservedError struct {
	Path    string
	Message string
}

func (e *FileNotObservedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("File %q must be read before modifying it. Read the file first and retry.", e.Path)
}

func requireTrustedPresent(ctx context.Context, sessionID string, target FileTarget, action string) (string, error) {
	obs, found, _ := GetTrustedObservation(ctx, sessionID, target)
	if !found || obs.Negative {
		return "", &FileNotObservedError{
			Path:    target.RelPath,
			Message: fmt.Sprintf("File %q must be read before %s. Read the file first and retry.", target.RelPath, action),
		}
	}
	return obs.SourceHash, nil
}

func directoryMutationUnsupported(relPath, action string) *tools.ToolResult {
	return recoverableResult(fmt.Sprintf("Directory %q cannot be %s; operate on individual files.", relPath, action), map[string]any{"path": relPath})
}

// FileStaleVersionError occurs when disk content differs from the version observed by the model.
type FileStaleVersionError struct {
	Path            string
	Expected        string
	Actual          string
	WriterSessionID string
}

func (e *FileStaleVersionError) Error() string {
	if e.WriterSessionID != "" {
		return fmt.Sprintf("File %q changed since it was read; the last in-process writer was session %q. Re-read the file and retry.", e.Path, e.WriterSessionID)
	}
	return fmt.Sprintf("File %q changed since it was read. Re-read the file and retry.", e.Path)
}

// FileCreationConflictError occurs when creating a file that already exists on disk.
type FileCreationConflictError struct {
	Path            string
	WriterSessionID string
}

func (e *FileCreationConflictError) Error() string {
	if e.WriterSessionID != "" {
		return fmt.Sprintf("File %q already exists after a write by session %q. Read the file before modifying it.", e.Path, e.WriterSessionID)
	}
	return fmt.Sprintf("File %q already exists. Read the file before modifying it.", e.Path)
}

func rememberWriter(targetKey, sessionID string) {
	if targetKey == "" || sessionID == "" {
		return
	}
	writerMu.Lock()
	lastWriter[targetKey] = sessionID
	writerMu.Unlock()
}

func annotateConflict(err error, targetKey string) error {
	writerMu.RLock()
	writer := lastWriter[targetKey]
	writerMu.RUnlock()
	switch conflict := err.(type) {
	case *FileStaleVersionError:
		conflict.WriterSessionID = writer
	case *FileCreationConflictError:
		conflict.WriterSessionID = writer
	}
	return err
}

// SafetyErrorResult formats a filesystem safety error into a recoverable Phase 3 ToolResult.
func SafetyErrorResult(err error) *tools.ToolResult {
	if err == nil {
		return nil
	}
	msg := err.Error()
	class := "recoverable"
	if _, ok := err.(*FileStaleVersionError); ok {
		class = "conflict"
	} else if _, ok := err.(*FileCreationConflictError); ok {
		class = "conflict"
	}
	return &tools.ToolResult{
		Success:   false,
		Status:    tools.ToolStatusFailed,
		Retryable: true,
		Message:   msg,
		Error:     &tools.ToolError{Class: class, Message: msg},
	}
}

// CurrentDiskHash reads the file from disk and computes its SHA-256 hash.
// If the file does not exist, it returns "", false, nil.
func CurrentDiskHash(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true, nil
}

// MutationIntent represents the pre-condition for a file mutation.
type MutationIntent struct {
	CreateIfAbsent bool
	ExpectedHash   string
	Delete         bool
}

func inspectDisk(target FileTarget, intent MutationIntent) (diskHash string, exists bool, before []byte, err error) {
	diskHash, exists, err = CurrentDiskHash(target.AbsPath)
	if err != nil {
		return "", false, nil, fmt.Errorf("failed to inspect disk state: %w", err)
	}
	if intent.CreateIfAbsent {
		if exists {
			return diskHash, true, nil, &FileCreationConflictError{Path: target.RelPath}
		}
		return diskHash, false, nil, nil
	}
	if !exists {
		return "", false, nil, fmt.Errorf("file %q does not exist", target.RelPath)
	}
	if intent.ExpectedHash != "" && diskHash != intent.ExpectedHash {
		return diskHash, true, nil, &FileStaleVersionError{Path: target.RelPath, Expected: intent.ExpectedHash, Actual: diskHash}
	}
	before, err = os.ReadFile(target.AbsPath)
	if err != nil {
		return diskHash, true, nil, fmt.Errorf("failed to read existing file: %w", err)
	}
	return diskHash, true, before, nil
}

func pathPresent(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func withOrderedTargetLocks[T any](a, b string, fn func() (T, error)) (T, error) {
	first, second := a, b
	if second < first {
		first, second = second, first
	}
	if first == "" {
		first = second
	}
	if first == second || second == "" {
		return withTargetLock(first, fn)
	}
	return withTargetLock(first, func() (T, error) {
		return withTargetLock(second, fn)
	})
}

// ValidateAndExecuteMutation performs atomic mutation with strict lock acquisition,
// disk freshness validation, atomic physical write, observation refresh, and post-state update.
// On any failure, the observation is never updated.
func ValidateAndExecuteMutation(
	ctx context.Context,
	sessionID string,
	target FileTarget,
	toolName string,
	intent MutationIntent,
	mutateFn func(before []byte) (after []byte, err error),
) (MutationOutput, error) {
	targetKey := target.AbsPath
	if targetKey == "" {
		targetKey = target.RelPath
	}

	return withTargetLock(targetKey, func() (MutationOutput, error) {
		_, _, before, err := inspectDisk(target, intent)
		if err != nil {
			return MutationOutput{}, annotateConflict(err, targetKey)
		}

		if intent.Delete {
			if err := os.Remove(target.AbsPath); err != nil {
				return MutationOutput{}, err
			}
			RecordTrustedObservation(ctx, sessionID, target, toolName, "", true)
			rememberWriter(targetKey, sessionID)
			return MutationOutput{Path: target.RelPath, OldHash: fileHash(before)}, nil
		}

		after, err := mutateFn(before)
		if err != nil {
			return MutationOutput{}, err
		}

		if err := EnsureParentDirectoryExists(target.AbsPath); err != nil {
			return MutationOutput{}, fmt.Errorf("parent directory does not exist: %w", err)
		}

		if intent.CreateIfAbsent && len(after) == 0 {
			f, err := os.OpenFile(target.AbsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err != nil {
				if os.IsExist(err) {
					return MutationOutput{}, annotateConflict(&FileCreationConflictError{Path: target.RelPath}, targetKey)
				}
				return MutationOutput{}, fmt.Errorf("failed to create file: %w", err)
			}
			_ = f.Close()
		} else {
			if err := storage.WriteFileAtomic(target.AbsPath, after, 0644); err != nil {
				return MutationOutput{}, fmt.Errorf("failed to write file atomically: %w", err)
			}
		}

		newHash := fileHash(after)
		RecordTrustedObservation(ctx, sessionID, target, toolName, newHash, false)
		rememberWriter(targetKey, sessionID)
		return mutationResult(target.RelPath, before, after), nil
	})
}

// ValidateAndExecuteRename moves src to dst after source freshness and destination absence checks.
func ValidateAndExecuteRename(ctx context.Context, sessionID string, src, dst FileTarget, expectedHash, toolName string) error {
	srcKey, dstKey := src.AbsPath, dst.AbsPath
	if srcKey == "" {
		srcKey = src.RelPath
	}
	if dstKey == "" {
		dstKey = dst.RelPath
	}
	_, err := withOrderedTargetLocks(srcKey, dstKey, func() (struct{}, error) {
		_, _, _, err := inspectDisk(src, MutationIntent{ExpectedHash: expectedHash})
		if err != nil {
			return struct{}{}, annotateConflict(err, srcKey)
		}
		exists, err := pathPresent(dst.AbsPath)
		if err != nil {
			return struct{}{}, err
		}
		if exists {
			return struct{}{}, annotateConflict(&FileCreationConflictError{Path: dst.RelPath}, dstKey)
		}
		if err := EnsureParentDirectoryExists(dst.AbsPath); err != nil {
			return struct{}{}, fmt.Errorf("parent directory does not exist: %w", err)
		}
		if err := os.Rename(src.AbsPath, dst.AbsPath); err != nil {
			return struct{}{}, err
		}
		hash, _, _ := CurrentDiskHash(dst.AbsPath)
		RecordTrustedObservation(ctx, sessionID, src, toolName, "", true)
		RecordTrustedObservation(ctx, sessionID, dst, toolName, hash, false)
		rememberWriter(srcKey, sessionID)
		rememberWriter(dstKey, sessionID)
		return struct{}{}, nil
	})
	return err
}
