package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Store) SaveDocument(_ context.Context, input DocumentInput) (Document, error) {
	if input.ID == "" || input.Kind == "" {
		return Document{}, errors.New("document ID and kind are required")
	}
	payload, err := jsonBytes(input.Payload)
	if err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.documents == nil {
		return Document{}, errors.New("store closed")
	}
	key := input.Kind + ":" + input.ID
	existing, exists := s.documents[key]
	var version int64
	createdAt := time.Now().UTC()
	if exists {
		if input.ExpectedVersion != 0 && input.ExpectedVersion != existing.Version {
			return Document{}, ErrConflict
		}
		version, createdAt = existing.Version, existing.CreatedAt
	} else if input.ExpectedVersion != 0 {
		return Document{}, ErrConflict
	}
	version++
	status := input.Status
	if status == "" {
		status = "active"
	}
	doc := Document{
		ID: input.ID, Kind: input.Kind, SessionID: input.SessionID, Status: status,
		Payload: payload, Provenance: input.Provenance, Version: version,
		CreatedAt: createdAt, UpdatedAt: time.Now().UTC(),
	}
	s.documents[key] = doc
	return doc, nil
}

func (s *Store) Document(_ context.Context, kind, id string) (Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.documents[kind+":"+id]
	if !ok {
		return Document{}, ErrNotFound
	}
	return doc, nil
}

func (s *Store) Documents(_ context.Context, kind, sessionID string) ([]Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var docs []Document
	for _, doc := range s.documents {
		if doc.Kind != kind {
			continue
		}
		if sessionID != "" && doc.SessionID != sessionID {
			continue
		}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].UpdatedAt.Equal(docs[j].UpdatedAt) {
			return docs[i].ID < docs[j].ID
		}
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})
	return docs, nil
}

func (s *Store) PutArtifact(_ context.Context, input ArtifactInput) (Artifact, error) {
	hashValue := sha256.Sum256(input.Data)
	hash := hex.EncodeToString(hashValue[:])
	contentType := input.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	origin := input.Origin
	if origin == "" {
		origin = "unknown"
	}
	artifact := Artifact{Hash: hash, Size: int64(len(input.Data)), ContentType: contentType, Origin: origin, CreatedAt: time.Now().UTC()}
	if _, err := s.writeObject(hash, input.Data); err != nil {
		return Artifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifacts == nil {
		return Artifact{}, errors.New("store closed")
	}
	s.artifacts[hash] = artifact
	if input.Event.Type != "" {
		event := s.createEventLocked(input.Event)
		s.events = append(s.events, event)
		s.publishCommittedLocked([]Event{event})
	}
	return artifact, nil
}

func (s *Store) Artifact(_ context.Context, hash string) (Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.artifacts[hash]
	if !ok {
		return Artifact{}, errors.New("artifact not found")
	}
	return a, nil
}

func (s *Store) ReadArtifact(_ context.Context, hash string) ([]byte, error) {
	s.mu.RLock()
	_, ok := s.artifacts[hash]
	s.mu.RUnlock()
	if !ok {
		return nil, errors.New("artifact not found")
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

func (s *Store) ObserveFile(_ context.Context, input FileObservation) (FileVersion, error) {
	path, err := cleanWorkspacePath(input.Path)
	if err != nil {
		return FileVersion{}, err
	}
	if input.ModifiedAt.IsZero() {
		input.ModifiedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fileID := s.files[path]
	if fileID == "" {
		fileID, err = newID()
		if err != nil {
			return FileVersion{}, err
		}
		s.files[path] = fileID
	}
	id, err := newID()
	if err != nil {
		return FileVersion{}, err
	}
	version := FileVersion{
		ID: id, FileID: fileID, Path: path, Deleted: input.Deleted,
		ModifiedAt: input.ModifiedAt.UTC(), WorkspaceRevisionID: input.WorkspaceRevisionID,
		ObservedAt: time.Now().UTC(),
	}
	if !input.Deleted {
		hash := sha256.Sum256(input.Data)
		version.Hash = hex.EncodeToString(hash[:])
		version.Size = int64(len(input.Data))
		if _, err := s.writeObject(version.Hash, input.Data); err != nil {
			return FileVersion{}, err
		}
		s.artifacts[version.Hash] = Artifact{
			Hash: version.Hash, Size: version.Size,
			ContentType: "application/octet-stream", Origin: "file:" + path,
			CreatedAt: time.Now().UTC(),
		}
		version.ArtifactID = version.Hash
	}
	s.fileVersions[path] = append(s.fileVersions[path], version)
	return version, nil
}

func (s *Store) RenameFile(_ context.Context, input FileRename) error {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	fileID := s.files[oldPath]
	if fileID == "" {
		return nil // first-seen rename is harmless
	}
	if existingID := s.files[newPath]; existingID != "" && existingID != fileID {
		return ErrConflict
	}
	s.files[newPath] = fileID
	delete(s.files, oldPath)
	// Move version history to new path key
	if versions := s.fileVersions[oldPath]; len(versions) > 0 {
		s.fileVersions[newPath] = append(s.fileVersions[newPath], versions...)
		delete(s.fileVersions, oldPath)
	}
	return nil
}

func (s *Store) FileVersions(_ context.Context, path string) ([]FileVersion, error) {
	path, err := cleanWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.fileVersions[path]
	if len(versions) == 0 {
		return nil, nil
	}
	result := make([]FileVersion, len(versions))
	copy(result, versions)
	return result, nil
}

func cleanWorkspacePath(path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return "", errors.New("file path must be workspace-relative")
	}
	return path, nil
}
