package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type stateRecorder struct {
	store      *state.Store
	repository *repository.Service
	root       string
	sessionID  string
}

func (r *stateRecorder) RecordToolLifecycle(ctx context.Context, lifecycle tools.Lifecycle) tools.LifecycleEnrichment {
	// Observability must never change the result of a user-approved tool call.
	// Use a live context so cancellation still leaves a durable explanation.
	ctx = context.WithoutCancel(ctx)
	payload := map[string]any{"tool": lifecycle.Tool, "status": lifecycle.Status, "arguments": lifecycle.Arguments}
	if lifecycle.Checkpoint != nil {
		payload["checkpoint"] = lifecycle.Checkpoint
	}
	if lifecycle.Error != nil {
		payload["error"] = lifecycle.Error.Error()
	}
	enrichment := tools.LifecycleEnrichment{}
	if lifecycle.Result != nil {
		raw := lifecycle.RawOutput
		if len(raw) > 0 {
			if artifact, err := r.store.PutArtifact(ctx, state.ArtifactInput{Data: raw, ContentType: "application/json", Origin: "tool:" + lifecycle.Tool}); err == nil {
				payload["artifact"] = artifact.Hash
				enrichment.ArtifactID = artifact.Hash
			}
		}
	}
	eventType := "tool." + lifecycle.Status
	if lifecycle.Status == "checkpoint" {
		eventType = "checkpoint.available"
	}
	if lifecycle.Status == "called" {
		eventType = "tool.called"
	}
	if lifecycle.Status == "completed" {
		eventType = "tool.completed"
	}
	if lifecycle.Status == "failed" {
		eventType = "tool.failed"
	}
	input := sessionlog.ApplyEventMeta(state.EventInput{SessionID: r.sessionID, Type: eventType, Payload: payload}, sessionlog.EventMetaFromContext(ctx))
	event, err := r.store.AppendEvent(ctx, input)
	if err != nil {
		return enrichment
	}
	if lifecycle.Status == "called" {
		_, _ = r.store.SaveDocument(ctx, state.DocumentInput{
			ID:         "tool-exec-" + r.sessionID,
			Kind:       "tool_execution",
			SessionID:  r.sessionID,
			Status:     "started",
			Payload:    mustJSON(payload),
			Provenance: state.Provenance{SourceEventID: event.ID, Authority: state.AuthorityRuntime, ObservedAt: time.Now().UTC()},
		})
	}
	if lifecycle.Status == "called" && lifecycle.Family == "filesystem" && inputPath(lifecycle.Input, "old_path") != "" && inputPath(lifecycle.Input, "new_path") != "" {
		revision, _ := r.store.ObserveWorkspace(ctx, workspaceSnapshot(ctx, r.root, lifecycle.Tool))
		if oldPath := inputPath(lifecycle.Input, "old_path"); oldPath != "" {
			r.observePath(ctx, oldPath, lifecycle.Access, lifecycle.Status, revision.ID, event.ID)
		}
		enrichment.WorldRevision = revision.ID
		return enrichment
	}
	if lifecycle.Status != "completed" && lifecycle.Status != "failed" {
		return enrichment
	}
	revision, _ := r.store.ObserveWorkspace(ctx, workspaceSnapshot(ctx, r.root, lifecycle.Tool))
	enrichment.WorldRevision = revision.ID
	_, _ = r.store.SaveDocument(ctx, state.DocumentInput{
		ID:         "tool-exec-" + r.sessionID,
		Kind:       "tool_execution",
		SessionID:  r.sessionID,
		Status:     lifecycle.Status,
		Payload:    mustJSON(payload),
		Provenance: state.Provenance{SourceEventID: event.ID, Authority: state.AuthorityRuntime, WorkspaceRevisionID: revision.ID, ObservedAt: time.Now().UTC()},
	})
	if lifecycle.Status == "completed" && lifecycle.Family == "filesystem" && inputPath(lifecycle.Input, "old_path") != "" {
		_ = r.store.RenameFile(ctx, state.FileRename{OldPath: r.relativePath(inputPath(lifecycle.Input, "old_path")), NewPath: r.relativePath(inputPath(lifecycle.Input, "new_path")), WorkspaceRevisionID: revision.ID, Event: state.EventInput{SessionID: r.sessionID, CausationID: event.ID}})
	}
	if lifecycle.SideEffect != tools.ToolSideEffectProcess {
		for _, path := range observedPaths(lifecycle.Input, lifecycle.Result) {
			r.observePath(ctx, path, lifecycle.Access, lifecycle.Status, revision.ID, event.ID)
		}
	}
	if lifecycle.Family == "shell" && r.repository != nil {
		r.repository.MarkDirty()
		_, _ = r.repository.Scan(ctx)
	}
	if lifecycle.Family == "terminal" && strings.Contains(lifecycle.Tool, "test") {
		status := lifecycle.Status
		_, _ = r.store.SaveDocument(ctx, state.DocumentInput{ID: "test-" + event.ID, Kind: "test", SessionID: r.sessionID, Status: status, Payload: mustJSON(payload), Provenance: state.Provenance{SourceEventID: event.ID, Authority: state.AuthorityRuntime, WorkspaceRevisionID: revision.ID, ObservedAt: time.Now().UTC()}})
	}
	if lifecycle.Status == "failed" {
		_, _ = r.store.SaveDocument(ctx, state.DocumentInput{ID: "error-" + event.ID, Kind: "error", SessionID: r.sessionID, Status: "active", Payload: mustJSON(payload), Provenance: state.Provenance{SourceEventID: event.ID, Authority: state.AuthorityRuntime, WorkspaceRevisionID: revision.ID, ObservedAt: time.Now().UTC()}})
	}
	return enrichment
}

func (r *stateRecorder) observePath(ctx context.Context, path string, access tools.ToolAccess, status, revisionID, eventID string) {
	path = r.relativePath(path)
	if path == "" {
		return
	}
	abs := filepath.Join(r.root, path)
	data, err := os.ReadFile(abs)
	deleted := os.IsNotExist(err)
	if err != nil && !deleted {
		return
	}
	eventType := "file.read"
	if deleted {
		eventType = "file.deleted"
	} else if status == "completed" && (access == tools.ToolAccessWrite || access == tools.ToolAccessDestructive) {
		eventType = "file.modified"
	}
	event := state.EventInput{SessionID: r.sessionID, CausationID: eventID, Type: eventType}
	if r.repository != nil {
		if err := r.repository.IndexPath(ctx, path, revisionID, event); err == nil {
			return
		}
	}
	_, _ = r.store.ObserveFile(ctx, state.FileObservation{Path: path, Data: data, Deleted: deleted, WorkspaceRevisionID: revisionID, Event: event})
}

func (r *stateRecorder) relativePath(path string) string {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(r.root, path)
		if err != nil {
			return ""
		}
		path = relative
	}
	if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(path)
}

func inputPath(input any, key string) string {
	values, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	path, _ := values[key].(string)
	return path
}

func observedPaths(input any, result *tools.ToolResult) []string {
	seen := map[string]bool{}
	var paths []string
	if result != nil {
		for _, entity := range result.AffectedEntities {
			if entity.Path != "" && !seen[entity.Path] {
				seen[entity.Path], paths = true, append(paths, entity.Path)
			}
		}
	}
	var visit func(any, string)
	visit = func(value any, key string) {
		switch value := value.(type) {
		case map[string]any:
			for childKey, child := range value {
				visit(child, strings.ToLower(childKey))
			}
		case []any:
			for _, child := range value {
				visit(child, key)
			}
		case string:
			if strings.Contains(key, "path") || key == "file" || key == "directory" {
				if !seen[value] {
					seen[value], paths = true, append(paths, value)
				}
			}
		}
	}
	visit(jsonValue(input), "")
	return paths
}

func jsonValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized any
	if json.Unmarshal(data, &normalized) != nil {
		return value
	}
	return normalized
}

func workspaceSnapshot(ctx context.Context, root, toolName string) state.WorkspaceSnapshot {
	// ponytail: arbitrary shell commands record a world revision rather than an
	// expensive whole-tree diff; add incremental watcher hashes when shell file
	// provenance needs to be exact.
	head := gitValue(ctx, root, "rev-parse", "HEAD")
	branch := gitValue(ctx, root, "branch", "--show-current")
	dirty := gitValue(ctx, root, "status", "--porcelain") != ""
	return state.WorkspaceSnapshot{Head: head, Branch: branch, Dirty: dirty, Metadata: mustJSON(map[string]string{"tool": toolName, "root": root}), ObservedAt: time.Now().UTC()}
}

func gitValue(ctx context.Context, root string, args ...string) string {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"serialization_error":%q}`, err.Error()))
	}
	return data
}
