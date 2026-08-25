// Package repository builds deterministic, workspace-local discovery data on
// top of the durable state store. It deliberately has no provider dependency.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/state"
)

const (
	maxIndexedFileSize = 1 << 20
	scannerVersion     = "go-v1"
)

// Parser is intentionally small so a future Tree-sitter or LSP adapter can
// enrich the same durable schema without changing scanner or query callers.
type Parser interface {
	Parse(path string, data []byte) (ParsedFile, error)
}

// Indexer and QueryService are the narrow public seams used by runtime tools.
// Service is the workspace implementation; storage remains behind state.RepositoryIndexStore.
type Indexer interface {
	Scan(context.Context) (ScanStats, error)
	IndexPath(context.Context, string, string, state.EventInput) error
	MarkDirty()
}

type QueryService interface {
	Query(context.Context, Query) (QueryResult, error)
}

type ParsedFile struct {
	Language  string
	Symbols   []state.RepositorySymbolInput
	Chunks    []state.RepositoryChunkInput
	Relations []state.RepositoryRelationInput
	Summaries []state.RepositorySummaryInput
}

type ScanStats struct {
	Indexed  int
	Touched  int
	Deleted  int
	Skipped  int
	Revision string
}

// Service owns the background scanner and exposes only read-only queries to
// tools. Store remains the sole SQL owner.
type Service struct {
	root       string
	store      *state.Store
	parser     Parser
	ready      chan struct{}
	readyOnce  sync.Once
	scanMu     sync.Mutex
	mu         sync.Mutex
	started    bool
	dirty      bool
	lastErr    error
	embeddings EmbeddingProvider
}

var _ Indexer = (*Service)(nil)
var _ QueryService = (*Service)(nil)

func New(root string, store *state.Store, embeddings EmbeddingProvider) (*Service, error) {
	if store == nil {
		return nil, errors.New("repository state store is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Service{root: filepath.Clean(abs), store: store, parser: GoParser{}, ready: make(chan struct{}), embeddings: embeddings}, nil
}

func (s *Service) Root() string { return s.root }

func (s *Service) SetEmbeddingProvider(provider EmbeddingProvider) {
	s.mu.Lock()
	s.embeddings = provider
	s.mu.Unlock()
}

func (s *Service) embeddingProvider() EmbeddingProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.embeddings
}

// Start begins the first scan without delaying terminal startup.
func (s *Service) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go func() {
		_, err := s.Scan(context.Background())
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
		s.readyOnce.Do(func() { close(s.ready) })
	}()
}

func (s *Service) Wait(ctx context.Context) error {
	s.Start()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		s.mu.Lock()
		err := s.lastErr
		s.mu.Unlock()
		return err
	}
}

func (s *Service) MarkDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

func (s *Service) Status() (ready bool, dirty bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.ready:
		ready = true
	default:
	}
	return ready, s.dirty, s.lastErr
}

func (s *Service) Scan(ctx context.Context) (ScanStats, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	paths, gitWorkspace, err := s.workspacePaths(ctx)
	if err != nil {
		return ScanStats{}, err
	}
	previous, err := s.store.RepositoryFiles(ctx)
	if err != nil {
		return ScanStats{}, err
	}
	known := make(map[string]state.RepositoryFileState, len(previous))
	for _, file := range previous {
		known[file.Path] = file
	}

	plan := make([]scanFile, 0)
	stats := ScanStats{}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		info, safe := s.candidateInfo(path)
		if !safe {
			stats.Skipped++
			continue
		}
		old, exists := known[path]
		if exists && !old.Deleted && old.Size == info.Size() && old.ModifiedAt.Equal(info.ModTime().UTC()) {
			seen[path] = true
			stats.Skipped++
			continue
		}
		_, data, hash, ok := s.readCandidate(path)
		if !ok {
			stats.Skipped++
			continue
		}
		seen[path] = true
		if exists && !old.Deleted && old.Hash == hash {
			if err := s.store.TouchRepositoryFile(ctx, state.RepositoryFileState{FileID: old.FileID, Path: path, Size: info.Size(), ModifiedAt: info.ModTime().UTC(), Language: old.Language}); err != nil {
				return stats, err
			}
			stats.Touched++
			continue
		}
		plan = append(plan, scanFile{path: path, data: data, info: info, hash: hash})
	}
	for path, old := range known {
		if !seen[path] && !old.Deleted {
			plan = append(plan, scanFile{path: path, deleted: true})
		}
	}

	head, branch, dirty := s.gitState(ctx, gitWorkspace)
	last, err := s.store.LatestRepositoryRevision(ctx)
	if err != nil {
		return stats, err
	}
	if len(plan) == 0 && last.ID != "" && last.Head == head && last.Branch == branch && last.Dirty == dirty {
		s.mu.Lock()
		s.dirty = false
		s.mu.Unlock()
		return stats, nil
	}
	world, err := s.store.ObserveWorkspace(ctx, state.WorkspaceSnapshot{Head: head, Branch: branch, Dirty: dirty, ObservedAt: time.Now().UTC()})
	if err != nil {
		return stats, err
	}
	revision, err := s.store.BeginRepositoryRevision(ctx, state.RepositoryRevisionInput{WorkspaceRevisionID: world.ID, Head: head, Branch: branch, Dirty: dirty, ScannerVersion: scannerVersion, ObservedAt: time.Now().UTC()})
	if err != nil {
		return stats, err
	}
	stats.Revision = revision.ID
	renamed := map[string]bool{}
	if gitWorkspace {
		for oldPath, newPath := range s.gitRenames(ctx) {
			if old, ok := known[oldPath]; ok && !old.Deleted && seen[newPath] {
				if err := s.store.RenameFile(ctx, state.FileRename{OldPath: oldPath, NewPath: newPath, WorkspaceRevisionID: world.ID, Event: state.EventInput{Type: "repository.rename"}}); err != nil {
					return stats, err
				}
				renamed[oldPath] = true
			}
		}
	}
	for _, item := range plan {
		if item.deleted && renamed[item.path] {
			continue
		}
		if item.deleted {
			if err := s.store.MarkRepositoryFileDeleted(ctx, state.RepositoryDeleteInput{Path: item.path, RepositoryRevisionID: revision.ID}); err != nil {
				return stats, err
			}
			stats.Deleted++
			continue
		}
		if err := s.apply(ctx, item, revision.ID, world.ID, state.EventInput{Type: "repository.scan"}); err != nil {
			return stats, err
		}
		stats.Indexed++
	}
	s.mu.Lock()
	s.dirty = false
	s.lastErr = nil
	s.mu.Unlock()
	return stats, nil
}

func (s *Service) gitRenames(ctx context.Context) map[string]string {
	output, err := exec.CommandContext(ctx, "git", "-C", s.root, "status", "--porcelain=v1", "-z", "-M").Output()
	if err != nil {
		return nil
	}
	parts := strings.Split(string(output), "\x00")
	renames := map[string]string{}
	for index := 0; index+1 < len(parts); index++ {
		entry := parts[index]
		if len(entry) < 4 || entry[0] != 'R' && entry[1] != 'R' {
			continue
		}
		newPath, oldPath := filepath.ToSlash(entry[3:]), filepath.ToSlash(parts[index+1])
		if newPath != "" && oldPath != "" {
			renames[oldPath] = newPath
		}
		index++
	}
	return renames
}

// IndexPath is the small targeted path used after successful filesystem
// tools. Shell commands cannot safely name changes and use MarkDirty instead.
func (s *Service) IndexPath(ctx context.Context, path, workspaceRevisionID string, event state.EventInput) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	path, err := s.relativePath(path)
	if err != nil {
		return err
	}
	abs := filepath.Join(s.root, filepath.FromSlash(path))
	if info, statErr := os.Lstat(abs); statErr == nil && info.IsDir() {
		return nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	info, data, hash, ok := s.readCandidate(path)
	files, err := s.store.RepositoryFiles(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, file := range files {
		if file.Path != path || file.Deleted {
			continue
		}
		found = true
		if ok && file.Size == info.Size() && file.ModifiedAt.Equal(info.ModTime().UTC()) {
			return nil
		}
		if ok && file.Hash == hash {
			return s.store.TouchRepositoryFile(ctx, state.RepositoryFileState{FileID: file.FileID, Path: path, Size: info.Size(), ModifiedAt: info.ModTime().UTC(), Language: file.Language})
		}
		break
	}
	if !ok && !found {
		return nil
	}
	head, branch, dirty := s.gitState(ctx, s.isGitWorkspace(ctx))
	revision, err := s.store.BeginRepositoryRevision(ctx, state.RepositoryRevisionInput{WorkspaceRevisionID: workspaceRevisionID, Head: head, Branch: branch, Dirty: dirty, ScannerVersion: scannerVersion, ObservedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if !ok {
		return s.store.MarkRepositoryFileDeleted(ctx, state.RepositoryDeleteInput{Path: path, RepositoryRevisionID: revision.ID, Event: event})
	}
	return s.apply(ctx, scanFile{path: path, data: data, info: info}, revision.ID, workspaceRevisionID, event)
}

func (s *Service) apply(ctx context.Context, item scanFile, revisionID, workspaceRevisionID string, event state.EventInput) error {
	parsed, err := s.parser.Parse(item.path, item.data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", item.path, err)
	}
	_, err = s.store.ApplyRepositoryFile(ctx, state.RepositoryFileInput{
		RepositoryRevisionID: revisionID,
		Language:             parsed.Language,
		Symbols:              parsed.Symbols,
		Chunks:               parsed.Chunks,
		Relations:            parsed.Relations,
		Summaries:            parsed.Summaries,
		Observation:          state.FileObservation{Path: item.path, Data: item.data, ModifiedAt: item.info.ModTime().UTC(), WorkspaceRevisionID: workspaceRevisionID, Event: event},
	})
	return err
}

type scanFile struct {
	path    string
	data    []byte
	info    fs.FileInfo
	hash    string
	deleted bool
}

func (s *Service) workspacePaths(ctx context.Context) ([]string, bool, error) {
	if s.isGitWorkspace(ctx) {
		command := exec.CommandContext(ctx, "git", "-C", s.root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		output, err := command.Output()
		if err == nil {
			return nulPaths(output), true, nil
		}
	}
	var paths []string
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == s.root {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() && ignoredDirectory(rel) {
			return filepath.SkipDir
		}
		if entry.Type()&fs.ModeSymlink != 0 && entry.IsDir() {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			paths = append(paths, rel)
		}
		return nil
	})
	return paths, false, err
}

func (s *Service) isGitWorkspace(ctx context.Context) bool {
	command := exec.CommandContext(ctx, "git", "-C", s.root, "rev-parse", "--is-inside-work-tree")
	output, err := command.Output()
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func (s *Service) gitState(ctx context.Context, gitWorkspace bool) (head, branch string, dirty bool) {
	if !gitWorkspace {
		return "", "", false
	}
	git := func(args ...string) string {
		output, err := exec.CommandContext(ctx, "git", append([]string{"-C", s.root}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(output))
	}
	return git("rev-parse", "HEAD"), git("branch", "--show-current"), git("status", "--porcelain") != ""
}

func (s *Service) readCandidate(path string) (fs.FileInfo, []byte, string, bool) {
	clean, err := s.relativePath(path)
	if err != nil {
		return nil, nil, "", false
	}
	info, ok := s.candidateInfo(clean)
	if !ok {
		return nil, nil, "", false
	}
	abs := filepath.Join(s.root, filepath.FromSlash(clean))
	data, err := os.ReadFile(abs)
	if err != nil || isBinary(data) {
		return nil, nil, "", false
	}
	hash := sha256.Sum256(data)
	return info, data, hex.EncodeToString(hash[:]), true
}

func (s *Service) candidateInfo(path string) (fs.FileInfo, bool) {
	clean, err := s.relativePath(path)
	if err != nil || ignoredPath(clean) {
		return nil, false
	}
	abs := filepath.Join(s.root, filepath.FromSlash(clean))
	info, err := os.Lstat(abs)
	if err != nil || info.Size() > maxIndexedFileSize {
		return nil, false
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil || !withinRoot(s.root, resolved) {
			return nil, false
		}
		info, err = os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxIndexedFileSize {
			return nil, false
		}
		return info, true
	}
	return info, info.Mode().IsRegular()
}

func (s *Service) relativePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(s.root, path)
		if err != nil {
			return "", err
		}
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return path, nil
	}
	if path == ".." || strings.HasPrefix(path, "../") {
		return "", errors.New("path is outside workspace")
	}
	return path, nil
}

func nulPaths(data []byte) []string {
	var paths []string
	for _, path := range strings.Split(string(data), "\x00") {
		if path != "" && !ignoredPath(path) {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	sort.Strings(paths)
	return paths
}

func ignoredDirectory(path string) bool {
	base := filepath.Base(path)
	switch base {
	case ".git", ".supremo", "vendor", "node_modules", "build", "dist", "target", ".cache", ".session", ".sessions", ".scratchpad":
		return true
	}
	return false
}

func ignoredPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if ignoredDirectory(part) {
			return true
		}
	}
	return false
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isBinary(data []byte) bool {
	return len(data) > 0 && strings.IndexByte(string(data[:min(len(data), 8192)]), 0) >= 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
