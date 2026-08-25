package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/storage"
)

const (
	checkpointVersion       = 1
	checkpointFileLimit     = int64(50 << 20)
	checkpointActionLimit   = int64(50 << 20)
	checkpointSessionLimit  = int64(250 << 20)
	checkpointSessionCount  = 25
	checkpointManifestName  = "manifest.json"
	checkpointPreimageName  = "preimage.json"
	checkpointDirectoryName = "rewind"
)

var checkpointMu sync.Mutex // ponytail: one mutating agent task exists today; shard by workspace if that changes.
var renameCheckpointDirectory = os.Rename
var errCheckpointPathUnavailable = errors.New("checkpoint path is unavailable through a symlink outside the workspace")

type checkpointContext struct {
	sessionID string
	enabled   bool
}

// CheckpointWarning describes one path or effect that rewind cannot restore.
type CheckpointWarning struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

// CheckpointSummary is the safe, UI-facing description of one saved checkpoint.
type CheckpointSummary struct {
	ID        string              `json:"id"`
	CreatedAt time.Time           `json:"created_at"`
	Action    string              `json:"action"`
	Files     int                 `json:"files"`
	Partial   bool                `json:"partial,omitempty"`
	Warnings  []CheckpointWarning `json:"warnings,omitempty"`
}

// RewindResult reports the covered changes restored by one rewind.
type RewindResult struct {
	Restored int
	Partial  bool
	Warnings []CheckpointWarning
	Backup   *CheckpointSummary
}

// CheckpointConflictError protects workspace changes made after a checkpoint.
type CheckpointConflictError struct{ Paths []string }

func (e *CheckpointConflictError) Error() string {
	return fmt.Sprintf("%d path(s) changed after the selected checkpoint", len(e.Paths))
}

type checkpointManifest struct {
	Version int               `json:"version"`
	Summary CheckpointSummary `json:"summary"`
	Entries []checkpointEntry `json:"entries"`
}

type checkpointEntry struct {
	Path   string    `json:"path"`
	Before fileState `json:"before"`
	After  fileState `json:"after"`
	Blob   string    `json:"blob,omitempty"`
}

type fileState struct {
	Kind string `json:"kind"`
	Mode uint32 `json:"mode,omitempty"`
	Size int64  `json:"size,omitempty"`
	Hash string `json:"hash,omitempty"`
	Link string `json:"link,omitempty"`
}

type snapshotScope struct {
	path             string
	broad            bool
	allowLeafSymlink bool
}

type capturedPath struct {
	state      fileState
	blob       string
	restorable bool
	reason     string
}

type checkpointHandle struct {
	ctx                context.Context
	root               string
	sessionID          string
	id                 string
	action             string
	tempDir            string
	scopes             []snapshotScope
	before             map[string]capturedPath
	warnings           []CheckpointWarning
	bytes              int64
	unlock             bool
	uncovered          bool
	preScanIncomplete  bool
	postScanIncomplete bool
	preflightErr       error
}

// WithCheckpointSession enables session-scoped snapshots for mutating tools.
func WithCheckpointSession(ctx context.Context, sessionID string, enabled bool) context.Context {
	return context.WithValue(ctx, checkpointKey, checkpointContext{sessionID: sessionID, enabled: enabled})
}

func checkpointContextFrom(ctx context.Context) checkpointContext {
	config, _ := ctx.Value(checkpointKey).(checkpointContext)
	return config
}

func beginCheckpoint(ctx context.Context, desc ToolDescriptor, input any) (*checkpointHandle, error) {
	config := checkpointContextFrom(ctx)
	name := desc.Name
	if !config.enabled || !desc.RequiresApproval {
		return nil, nil
	}
	if config.sessionID == "" {
		return nil, classify(ErrorClassCheckpoint, errors.New("checkpoint preflight failed: missing session ID"))
	}
	if Workspace(ctx) == "" {
		return nil, classify(ErrorClassCheckpoint, errors.New("checkpoint preflight failed: missing workspace"))
	}
	checkpointMu.Lock()
	handle := newCheckpointHandle(ctx, Workspace(ctx), config.sessionID, checkpointAction(name, input), checkpointScopes(ctx, name, input))
	handle.unlock = true
	if desc.SideEffect == ToolSideEffectProcess {
		handle.warn("", "commands may affect excluded paths or external state")
	}
	if handle.preflightErr != nil {
		logging.Error("Checkpoint preflight error for action %s: %v", handle.action, handle.preflightErr)
		err := classify(ErrorClassCheckpoint, fmt.Errorf("checkpoint preflight failed: %w", handle.preflightErr))
		handle.discard()
		return nil, err
	}
	return handle, nil
}

func newCheckpointHandle(ctx context.Context, root, sessionID, action string, scopes []snapshotScope) *checkpointHandle {
	handle := &checkpointHandle{
		ctx: ctx, root: filepath.Clean(root), sessionID: sessionID, action: action, scopes: scopes,
		before: make(map[string]capturedPath),
	}
	if !safeCheckpointID(sessionID) {
		handle.failPreflight("", errors.New("invalid session ID"))
		return handle
	}
	if _, err := loadCheckpointManifests(handle.root, sessionID); err != nil {
		handle.failPreflight("", fmt.Errorf("validate checkpoint store: %w", err))
		return handle
	}
	handle.id = newCheckpointID()
	base := checkpointSessionDir(handle.root, sessionID)
	if err := os.MkdirAll(base, 0700); err != nil {
		handle.failPreflight("", fmt.Errorf("create checkpoint store: %w", err))
		return handle
	}
	handle.tempDir = filepath.Join(base, ".tmp-"+handle.id)
	if err := os.Mkdir(handle.tempDir, 0700); err != nil {
		handle.failPreflight("", fmt.Errorf("create checkpoint: %w", err))
		handle.tempDir = ""
		return handle
	}
	handle.before = handle.capture(true)
	if handle.preflightErr == nil {
		if err := handle.writePreimageManifest(); err != nil {
			handle.failPreflight("", fmt.Errorf("save checkpoint preimage: %w", err))
		}
	}
	return handle
}

func (h *checkpointHandle) writePreimageManifest() error {
	paths := make([]string, 0, len(h.before))
	for path := range h.before {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	manifest := checkpointManifest{
		Version: checkpointVersion,
		Summary: CheckpointSummary{
			ID: h.id, CreatedAt: time.Now().UTC(), Action: h.action, Files: len(paths), Partial: true,
			Warnings: append(append([]CheckpointWarning(nil), h.warnings...), CheckpointWarning{Reason: "action outcome may be incomplete"}),
		},
	}
	for _, path := range paths {
		before := h.before[path]
		manifest.Entries = append(manifest.Entries, checkpointEntry{Path: path, Before: before.state, Blob: before.blob})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(filepath.Join(h.tempDir, checkpointPreimageName), data, 0600)
}

func (h *checkpointHandle) finish() *CheckpointSummary {
	if h == nil {
		return nil
	}
	if h.unlock {
		h.unlock = false
		defer checkpointMu.Unlock()
	}
	return h.finishUnlocked()
}

func (h *checkpointHandle) discard() {
	if h.tempDir != "" {
		_ = os.RemoveAll(h.tempDir)
	}
	if h.unlock {
		h.unlock = false
		checkpointMu.Unlock()
	}
}

func (h *checkpointHandle) finishUnlocked() *CheckpointSummary {
	if h.tempDir == "" {
		if len(h.warnings) == 0 {
			return nil
		}
		return &CheckpointSummary{Action: h.action, Partial: true, Warnings: h.warnings}
	}
	taskContext := h.ctx
	h.ctx = context.Background()
	after := h.capture(false)
	h.ctx = taskContext
	paths := make(map[string]struct{}, len(h.before)+len(after))
	for path := range h.before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	manifest := checkpointManifest{Version: checkpointVersion}
	for _, path := range ordered {
		before, beforeOK := h.before[path]
		afterPath, afterOK := after[path]
		if !beforeOK && h.preScanIncomplete {
			h.warn(path, "pre-action scan was incomplete")
			h.uncovered = true
			continue
		}
		if !afterOK && h.postScanIncomplete {
			h.warn(path, "post-action scan was incomplete")
			h.uncovered = true
			if beforeOK && before.restorable {
				afterPath = capturedPath{state: fileState{Kind: "unknown"}}
				afterOK = true
			} else {
				continue
			}
		}
		if !beforeOK {
			before = capturedPath{state: fileState{Kind: "absent"}, restorable: true}
		}
		if !afterOK {
			afterPath = capturedPath{state: fileState{Kind: "absent"}, restorable: true}
		}
		if sameFileState(before.state, afterPath.state) {
			if before.blob != "" {
				_ = os.Remove(filepath.Join(h.tempDir, before.blob))
			}
			continue
		}
		if !before.restorable {
			h.warn(path, before.reason)
			h.uncovered = true
			continue
		}
		if before.state.Kind != "directory" && afterPath.state.Kind == "directory" {
			afterPath.state.Hash, _ = directoryFingerprint(h.root, path)
		}
		manifest.Entries = append(manifest.Entries, checkpointEntry{
			Path: path, Before: before.state, After: afterPath.state, Blob: before.blob,
		})
	}
	if len(manifest.Entries) == 0 && !h.uncovered && len(h.warnings) == 0 {
		_ = os.RemoveAll(h.tempDir)
		return nil
	}
	manifest.Summary = CheckpointSummary{
		ID: h.id, CreatedAt: time.Now().UTC(), Action: h.action, Files: len(manifest.Entries),
		Partial: len(h.warnings) > 0, Warnings: h.warnings,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return h.incompleteSummary(err)
	}
	if err := storage.WriteFileAtomic(filepath.Join(h.tempDir, checkpointManifestName), data, 0600); err != nil {
		return h.incompleteSummary(err)
	}
	if !h.postScanIncomplete {
		_ = os.Remove(filepath.Join(h.tempDir, checkpointPreimageName))
	}
	finalDir := filepath.Join(checkpointSessionDir(h.root, h.sessionID), h.id)
	if err := renameCheckpointDirectory(h.tempDir, finalDir); err != nil {
		return h.incompleteSummary(err)
	}
	_ = pruneCheckpoints(h.root, h.sessionID)
	return &manifest.Summary
}

func (h *checkpointHandle) incompleteSummary(err error) *CheckpointSummary {
	h.warn("", "checkpoint finalization failed: "+err.Error())
	return &CheckpointSummary{Action: h.action, Partial: true, Warnings: append([]CheckpointWarning(nil), h.warnings...)}
}

func (h *checkpointHandle) capture(before bool) map[string]capturedPath {
	captured := make(map[string]capturedPath)
	for _, scope := range h.scopes {
		if err := h.captureScope(scope, before, captured); err != nil {
			h.warn(scope.path, err.Error())
			h.markScanIncomplete(before)
			if before {
				h.failPreflight(scope.path, err)
			}
		}
	}
	return captured
}

func (h *checkpointHandle) captureScope(scope snapshotScope, before bool, captured map[string]capturedPath) error {
	abs, err := resolveCheckpointPath(h.root, scope.path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(h.root, abs)
	if err != nil || !safeRelativePath(rel) {
		return fmt.Errorf("path is outside the workspace")
	}
	rel = filepath.ToSlash(rel)
	if reason := excludedCheckpointPath(rel); reason != "" {
		h.warn(rel, reason)
		if before && !scope.broad {
			h.failPreflight(rel, errors.New(reason))
		}
		h.uncovered = h.uncovered || !scope.broad
		return nil
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		captured[rel] = capturedPath{state: fileState{Kind: "absent"}, restorable: true}
		return nil
	}
	if err != nil {
		return err
	}
	if before && info.Mode()&os.ModeSymlink != 0 && !scope.allowLeafSymlink {
		return errors.New("checkpoint target is a symlink")
	}
	if !info.IsDir() {
		captured[rel] = h.captureFile(abs, rel, info, before, scope.broad)
		return nil
	}
	return filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			pathRel, _ := filepath.Rel(h.root, path)
			h.warn(filepath.ToSlash(pathRel), walkErr.Error())
			h.markScanIncomplete(before)
			if before {
				return walkErr
			}
			return nil
		}
		if err := h.ctx.Err(); err != nil {
			return err
		}
		pathRel, err := filepath.Rel(h.root, path)
		if err != nil || !safeRelativePath(pathRel) {
			return nil
		}
		pathRel = filepath.ToSlash(pathRel)
		if path != abs {
			if reason := excludedCheckpointPath(pathRel); reason != "" {
				if before && !scope.broad {
					h.failPreflight(pathRel, errors.New(reason))
				}
				if entry.IsDir() {
					h.warn(pathRel, reason)
					return filepath.SkipDir
				}
				h.warn(pathRel, reason)
				return nil
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			h.warn(pathRel, err.Error())
			h.markScanIncomplete(before)
			if before {
				return err
			}
			return nil
		}
		captured[pathRel] = h.captureFile(path, pathRel, info, before, scope.broad)
		return nil
	})
}

func (h *checkpointHandle) markScanIncomplete(before bool) {
	if before {
		h.preScanIncomplete = true
	} else {
		h.postScanIncomplete = true
	}
}

func (h *checkpointHandle) captureFile(abs, rel string, info os.FileInfo, before bool, broad bool) capturedPath {
	mode := info.Mode()
	switch {
	case mode.IsDir():
		return capturedPath{state: fileState{Kind: "directory", Mode: uint32(mode.Perm())}, restorable: true}
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			if before && !broad {
				h.failPreflight(rel, err)
			}
			h.warn(rel, err.Error())
			return capturedPath{state: fileState{Kind: "symlink"}, reason: err.Error()}
		}
		return capturedPath{state: fileState{Kind: "symlink", Link: target}, restorable: true}
	case !mode.IsRegular():
		reason := "special files are not rewindable"
		if before && !broad {
			h.failPreflight(rel, errors.New(reason))
		}
		h.warn(rel, reason)
		return capturedPath{state: fileState{Kind: "special"}, reason: reason}
	}
	state := fileState{Kind: "file", Mode: uint32(mode.Perm()), Size: info.Size()}
	if broad && (info.Size() > checkpointFileLimit || isBinaryFile(abs)) {
		reason := "per-file snapshot limit exceeded"
		if isBinaryFile(abs) {
			reason = "binary or compiled artifact excluded from snapshot"
		}
		h.warn(rel, reason)
		return capturedPath{state: state, reason: reason}
	}
	if before && info.Size() <= checkpointFileLimit && h.bytes+info.Size() <= checkpointActionLimit {
		data, err := os.ReadFile(abs)
		if err != nil {
			if before && !broad {
				h.failPreflight(rel, err)
			}
			h.warn(rel, err.Error())
			return capturedPath{state: state, reason: err.Error()}
		}
		sum := sha256.Sum256(data)
		state.Hash = hex.EncodeToString(sum[:])
		blob := checkpointBlobName(rel)
		if err := storage.WriteFileAtomic(filepath.Join(h.tempDir, blob), data, 0600); err != nil {
			if before && !broad {
				h.failPreflight(rel, err)
			}
			h.warn(rel, err.Error())
			return capturedPath{state: state, reason: err.Error()}
		}
		h.bytes += int64(len(data))
		return capturedPath{state: state, blob: blob, restorable: true}
	}
	hash, err := hashFile(abs)
	if err != nil {
		if before && !broad {
			h.failPreflight(rel, err)
		}
		h.warn(rel, err.Error())
		return capturedPath{state: state, reason: err.Error()}
	}
	state.Hash = hash
	if before {
		reason := "per-file snapshot limit exceeded"
		if info.Size() <= checkpointFileLimit {
			reason = "per-action snapshot limit exceeded"
		}
		if !broad {
			h.failPreflight(rel, errors.New(reason))
		} else {
			h.warn(rel, reason)
		}
		return capturedPath{state: state, reason: reason}
	}
	return capturedPath{state: state, restorable: true}
}

func (h *checkpointHandle) warn(path, reason string) {
	if reason == "" {
		reason = "not rewindable"
	}
	for _, warning := range h.warnings {
		if warning.Path == path && warning.Reason == reason {
			return
		}
	}
	h.warnings = append(h.warnings, CheckpointWarning{Path: path, Reason: reason})
}

func (h *checkpointHandle) failPreflight(path string, err error) {
	if err == nil {
		return
	}
	h.warn(path, err.Error())
	if path != "" {
		err = fmt.Errorf("%s: %w", path, err)
	}
	h.preflightErr = errors.Join(h.preflightErr, err)
}

func checkpointScopes(ctx context.Context, name string, input any) []snapshotScope {
	switch name {
	case "create_directory", "delete_file", "write_file", "replace_in_file":
		return []snapshotScope{{path: inputValue(input, "path")}}
	case "rename_file":
		return []snapshotScope{{path: inputValue(input, "old_path")}, {path: inputValue(input, "new_path")}}
	case "execute_command":
		directory := inputValue(input, "directory")
		if directory == "" {
			directory = "."
		}
		return []snapshotScope{{path: directory, broad: true}}
	default:
		return nil
	}
}

func checkpointAction(name string, input any) string {
	switch name {
	case "create_directory", "delete_file", "write_file", "replace_in_file":
		return name + " " + filepath.Base(inputValue(input, "path"))
	case "rename_file":
		return "rename_file " + filepath.Base(inputValue(input, "old_path")) + " → " + filepath.Base(inputValue(input, "new_path"))
	case "execute_command":
		return "execute_command " + filepath.Base(inputValue(input, "command"))
	default:
		return name
	}
}

func checkpointBlobName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]) + ".bin"
}

func newCheckpointID() string {
	id, err := storage.NewID()
	if err == nil && len(id) >= 8 {
		return fmt.Sprintf("%d-%s", time.Now().UnixNano(), id[:8])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func resolveCheckpointPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", err
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absoluteRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(absoluteRoot, candidate)
	if err != nil || !safeRelativePath(relative) {
		return "", fmt.Errorf("path is outside the workspace")
	}
	if candidate == absoluteRoot {
		return candidate, nil
	}
	// The final path component is operated on with Lstat/remove/rename semantics.
	// Resolve only its parent so a leaf symlink can be captured without following it.
	existing := filepath.Dir(candidate)
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			inside, relErr := filepath.Rel(resolvedRoot, resolved)
			if relErr != nil || !safeRelativePath(inside) {
				return "", errCheckpointPathUnavailable
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", statErr
			}
			if !info.IsDir() {
				return "", errCheckpointPathUnavailable
			}
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		existing = parent
	}
}

func safeRelativePath(path string) bool {
	return path != ".." && !filepath.IsAbs(path) && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func safeCheckpointID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return false
	}
	for _, r := range id {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [512]byte
	n, err := f.Read(buf[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

func excludedCheckpointPath(path string) string {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		switch part {
		case ".git", ".session", ".sessions", ".scratchpad", ".supremo":
			return "Supremo and Git state is excluded"
		case "node_modules", "vendor", ".venv", "venv", "dist", "build", "target",
			".next", "out", "bin", "obj", ".turbo", ".cache", "coverage",
			".gradle", ".pytest_cache", "__pycache__":
			return "generated, cache, or dependency directory is excluded"
		}
	}
	return ""
}

func sameFileState(left, right fileState) bool {
	return left.Kind == right.Kind && left.Mode == right.Mode && left.Size == right.Size && left.Hash == right.Hash && left.Link == right.Link
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func directoryFingerprint(root, relative string) (string, error) {
	abs, err := resolveCheckpointPath(root, relative)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if path != abs {
			if excludedCheckpointPath(rel) != "" && entry.IsDir() {
				return filepath.SkipDir
			}
			if excludedCheckpointPath(rel) != "" {
				return nil
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", rel, info.Mode().String(), info.Size())
		if info.Mode().IsRegular() {
			value, err := hashFile(path)
			if err != nil {
				return err
			}
			io.WriteString(hash, value)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			io.WriteString(hash, target)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checkpointRoot(root string) string {
	workspaceID, err := state.ResolveWorkspaceIdentity(context.Background(), root)
	if err == nil && workspaceID != "" {
		dir := state.WorkspaceCheckpointsDir(workspaceID)
		_ = os.MkdirAll(dir, 0700)
		return dir
	}
	return legacyCheckpointRoot(root)
}

func legacyCheckpointRoot(root string) string {
	return filepath.Join(root, ".session", checkpointDirectoryName)
}

func checkpointSessionDir(root, sessionID string) string {
	return filepath.Join(checkpointRoot(root), sessionID)
}

// ListCheckpoints returns the current session's checkpoints newest first.
func ListCheckpoints(root, sessionID string) ([]CheckpointSummary, error) {
	checkpointMu.Lock()
	defer checkpointMu.Unlock()
	manifests, err := loadCheckpointManifests(root, sessionID)
	if err != nil {
		return nil, err
	}
	summaries := make([]CheckpointSummary, len(manifests))
	for index := range manifests {
		summaries[len(manifests)-1-index] = manifests[index].Summary
	}
	return summaries, nil
}

func loadCheckpointManifests(root, sessionID string) ([]checkpointManifest, error) {
	if !safeCheckpointID(sessionID) {
		return nil, fmt.Errorf("invalid session ID")
	}
	dir := checkpointSessionDir(root, sessionID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) || len(entries) == 0 {
		legacyDir := filepath.Join(legacyCheckpointRoot(root), sessionID)
		legacyEntries, legErr := os.ReadDir(legacyDir)
		if legErr == nil && len(legacyEntries) > 0 {
			dir = legacyDir
			entries = legacyEntries
		} else if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var manifests []checkpointManifest
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".tmp-") {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), checkpointManifestName))
		if err != nil {
			return nil, err
		}
		var manifest checkpointManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("load checkpoint %s: %w", entry.Name(), err)
		}
		if manifest.Version != checkpointVersion || manifest.Summary.ID != entry.Name() {
			return nil, fmt.Errorf("invalid checkpoint %s", entry.Name())
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Summary.CreatedAt.Before(manifests[j].Summary.CreatedAt) })
	return manifests, nil
}

// ClearCheckpoints removes only one chat session's rewind history.
func ClearCheckpoints(root, sessionID string) error {
	if !safeCheckpointID(sessionID) {
		return fmt.Errorf("invalid session ID")
	}
	checkpointMu.Lock()
	defer checkpointMu.Unlock()
	_ = os.RemoveAll(filepath.Join(legacyCheckpointRoot(root), sessionID))
	return os.RemoveAll(checkpointSessionDir(root, sessionID))
}

// Rewind restores the workspace to immediately before checkpointID.
func Rewind(ctx context.Context, root, sessionID, checkpointID string, force bool) (RewindResult, error) {
	checkpointMu.Lock()
	defer checkpointMu.Unlock()
	manifests, err := loadCheckpointManifests(root, sessionID)
	if err != nil {
		return RewindResult{}, err
	}
	selected := -1
	for index := range manifests {
		if manifests[index].Summary.ID == checkpointID {
			selected = index
			break
		}
	}
	if selected < 0 {
		return RewindResult{}, fmt.Errorf("checkpoint %q not found", checkpointID)
	}
	affected := manifests[selected:]
	conflicts, err := rewindConflicts(root, affected)
	if err != nil {
		return RewindResult{}, err
	}
	if len(conflicts) > 0 && !force {
		return RewindResult{}, &CheckpointConflictError{Paths: conflicts}
	}

	scopes := rewindScopes(affected)
	backup := newCheckpointHandle(ctx, root, sessionID, "rewind "+affected[0].Summary.Action, scopes)
	if backup.preflightErr != nil {
		err := fmt.Errorf("rewind backup preflight failed: %w", backup.preflightErr)
		backup.discard()
		return RewindResult{}, err
	}
	result := RewindResult{}
	for index := len(affected) - 1; index >= 0; index-- {
		if err := restoreCheckpoint(root, sessionID, affected[index], force); err != nil {
			if summary := backup.finishUnlocked(); summary != nil {
				result.Backup = summary
			}
			return result, err
		}
		result.Restored += len(affected[index].Entries)
		result.Partial = result.Partial || affected[index].Summary.Partial
		result.Warnings = append(result.Warnings, affected[index].Summary.Warnings...)
	}
	if summary := backup.finishUnlocked(); summary != nil {
		result.Partial = result.Partial || summary.Partial
		result.Warnings = append(result.Warnings, summary.Warnings...)
		if summary.ID == "" {
			return result, fmt.Errorf("rewind completed, but its protective backup could not be finalized; original checkpoints were retained")
		}
		result.Backup = summary
	}
	for _, manifest := range affected {
		if err := os.RemoveAll(filepath.Join(checkpointSessionDir(root, sessionID), manifest.Summary.ID)); err != nil {
			return result, err
		}
	}
	return result, pruneCheckpoints(root, sessionID)
}

func rewindConflicts(root string, manifests []checkpointManifest) ([]string, error) {
	expected := make(map[string]fileState)
	for _, manifest := range manifests {
		for _, entry := range manifest.Entries {
			expected[entry.Path] = entry.After
		}
	}
	var conflicts []string
	for path, state := range expected {
		current, err := stateForPath(root, path, state.Hash != "" && state.Kind == "directory")
		if errors.Is(err, errCheckpointPathUnavailable) {
			conflicts = append(conflicts, path)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !sameFileState(current, state) {
			conflicts = append(conflicts, path)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func stateForPath(root, relative string, treeHash bool) (fileState, error) {
	abs, err := resolveCheckpointPath(root, relative)
	if err != nil {
		return fileState{}, err
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return fileState{Kind: "absent"}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	mode := info.Mode()
	switch {
	case mode.IsDir():
		state := fileState{Kind: "directory", Mode: uint32(mode.Perm())}
		if treeHash {
			state.Hash, err = directoryFingerprint(root, relative)
		}
		return state, err
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		return fileState{Kind: "symlink", Link: target}, err
	case mode.IsRegular():
		hash, err := hashFile(abs)
		return fileState{Kind: "file", Mode: uint32(mode.Perm()), Size: info.Size(), Hash: hash}, err
	default:
		return fileState{Kind: "special"}, nil
	}
}

func rewindScopes(manifests []checkpointManifest) []snapshotScope {
	paths := make(map[string]struct{})
	for _, manifest := range manifests {
		for _, entry := range manifest.Entries {
			paths[entry.Path] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := strings.Count(ordered[i], "/"), strings.Count(ordered[j], "/")
		if left == right {
			return ordered[i] < ordered[j]
		}
		return left < right
	})
	var scopes []snapshotScope
	for _, path := range ordered {
		covered := false
		for _, scope := range scopes {
			if path == scope.path || strings.HasPrefix(path, strings.TrimSuffix(scope.path, "/")+"/") {
				covered = true
				break
			}
		}
		if !covered {
			scopes = append(scopes, snapshotScope{path: path, allowLeafSymlink: true})
		}
	}
	return scopes
}

func restoreCheckpoint(root, sessionID string, manifest checkpointManifest, force bool) error {
	entries := append([]checkpointEntry(nil), manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool { return strings.Count(entries[i].Path, "/") < strings.Count(entries[j].Path, "/") })
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Before.Kind != "absent" {
			continue
		}
		path, err := resolveCheckpointPath(root, entry.Path)
		if force && errors.Is(err, errCheckpointPathUnavailable) {
			continue
		}
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() && force {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.Before.Kind != "directory" {
			continue
		}
		path, err := resolveCheckpointPath(root, entry.Path)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(path, os.FileMode(entry.Before.Mode)); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.Before.Kind != "file" && entry.Before.Kind != "symlink" {
			continue
		}
		path, err := resolveCheckpointPath(root, entry.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if entry.Before.Kind == "symlink" {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if err := os.Symlink(entry.Before.Link, path); err != nil {
				return err
			}
			continue
		}
		if entry.Blob != checkpointBlobName(entry.Path) {
			return fmt.Errorf("invalid checkpoint blob for %s", entry.Path)
		}
		blobPath := filepath.Join(checkpointSessionDir(root, sessionID), manifest.Summary.ID, entry.Blob)
		if _, statErr := os.Lstat(blobPath); errors.Is(statErr, os.ErrNotExist) {
			legacyBlobPath := filepath.Join(legacyCheckpointRoot(root), sessionID, manifest.Summary.ID, entry.Blob)
			if _, legErr := os.Lstat(legacyBlobPath); legErr == nil {
				blobPath = legacyBlobPath
			}
		}
		if info, err := os.Lstat(blobPath); err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("checkpoint blob is not a regular file")
			}
			return err
		}
		data, err := os.ReadFile(blobPath)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		if err := storage.WriteFileAtomic(path, data, os.FileMode(entry.Before.Mode)); err != nil {
			return err
		}
		if err := os.Chmod(path, os.FileMode(entry.Before.Mode)); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.Before.Kind == "directory" {
			path, err := resolveCheckpointPath(root, entry.Path)
			if err != nil {
				return err
			}
			if err := os.Chmod(path, os.FileMode(entry.Before.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

func pruneCheckpoints(root, sessionID string) error {
	manifests, err := loadCheckpointManifests(root, sessionID)
	if err != nil {
		return err
	}
	var total int64
	sizes := make(map[string]int64, len(manifests))
	for _, manifest := range manifests {
		dir := filepath.Join(checkpointSessionDir(root, sessionID), manifest.Summary.ID)
		size, err := directorySize(dir)
		if err != nil {
			return err
		}
		sizes[manifest.Summary.ID] = size
		total += size
	}
	for len(manifests) > checkpointSessionCount || total > checkpointSessionLimit {
		oldest := manifests[0]
		total -= sizes[oldest.Summary.ID]
		if err := os.RemoveAll(filepath.Join(checkpointSessionDir(root, sessionID), oldest.Summary.ID)); err != nil {
			return err
		}
		manifests = manifests[1:]
	}
	return nil
}

func directorySize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}
