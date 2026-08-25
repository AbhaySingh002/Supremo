package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

// DurableMemory keeps the complete transcript in the State Engine. Windows
// are pure reads; model context never decides what the transcript retains.
type DurableMemory struct{ store *state.Store }

func NewDurableMemory(root string) (*DurableMemory, error) {
	store, err := state.Open(root)
	if err != nil {
		return nil, err
	}
	return &DurableMemory{store: store}, nil
}

func newDurableMemory(store *state.Store) *DurableMemory { return &DurableMemory{store: store} }

func (m *DurableMemory) Append(ctx context.Context, sessionID string, msg models.Message) error {
	return m.append(ctx, sessionID, msg, nil)
}

// AppendAssistantMessage records a completed assistant message and its
// diagnostic stream provenance. The source IDs are audit-only and never enter
// provider-visible message content.
func (m *DurableMemory) AppendAssistantMessage(ctx context.Context, sessionID string, msg models.Message, sourceEventSeqs []int64) error {
	return m.append(ctx, sessionID, msg, sourceEventSeqs)
}

func (m *DurableMemory) append(ctx context.Context, sessionID string, msg models.Message, sourceEventSeqs []int64) error {
	parts := make([]state.MessagePartInput, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" {
		parts = append(parts, state.MessagePartInput{Kind: "text", Text: msg.Content})
	}
	if msg.TurnProgress != nil {
		if progMeta, err := json.Marshal(msg.TurnProgress); err == nil {
			parts = append(parts, state.MessagePartInput{Kind: "turn_progress", Metadata: progMeta})
		}
	}
	for _, call := range msg.ToolCalls {
		metadata, err := json.Marshal(call)
		if err != nil {
			return err
		}
		parts = append(parts, state.MessagePartInput{Kind: "assistant_tool_call", Metadata: metadata})
	}
	if msg.Role == models.RoleTool {
		artifact, err := m.store.PutArtifact(ctx, state.ArtifactInput{Data: []byte(msg.Content), ContentType: "text/plain; charset=utf-8", Origin: "tool-output", Event: state.EventInput{SessionID: sessionID, Type: "artifact.created"}})
		if err != nil {
			return err
		}
		metadata, err := json.Marshal(map[string]string{"tool_call_id": msg.ToolCallID, "tool_name": msg.ToolName})
		if err != nil {
			return err
		}
		boundedText := truncateObservationBounded(msg.Content, toolSnippetTokens, artifact.Hash)
		if boundedText != msg.Content {
			boundedText = fmt.Sprintf("[Tool observation archived as transcript artifact %s; cite result.artifact_id below as evidence]\n%s", artifact.Hash, boundedText)
		}
		part := state.MessagePartInput{Kind: "tool_result", ArtifactID: artifact.Hash, Text: boundedText, Metadata: metadata}
		parts = []state.MessagePartInput{part}
	}
	if len(parts) == 0 {
		parts = append(parts, state.MessagePartInput{Kind: "text", Text: ""})
	}
	kind, ok := eventTypeForRole(msg.Role)
	if !ok {
		_, err := m.store.AppendMessage(ctx, state.MessageInput{SessionID: sessionID, Role: string(msg.Role), TaskID: msg.TaskID, Parts: parts})
		return err
	}
	event, err := sessionlog.New(kind, nil)
	if err != nil {
		return err
	}
	event.Time = time.Now().UTC()
	event.Message = msg
	event.SurfaceOp = &SurfaceOp{Kind: surfaceOpAppend}
	event.SourceEventSeqs = append([]int64(nil), sourceEventSeqs...)
	related, err := sessionlog.ToEventInput(sessionID, event)
	if err != nil {
		return err
	}
	meta := sessionlog.EventMetaFromContext(ctx)
	related = sessionlog.ApplyEventMeta(related, meta)
	_, err = m.store.AppendMessage(ctx, state.MessageInput{
		ID: msg.LocalID, SessionID: sessionID, Role: string(msg.Role), TaskID: msg.TaskID, Parts: parts,
		Event:         state.EventInput{CorrelationID: meta.CorrelationID, CausationID: meta.CausationID},
		RelatedEvents: []state.EventInput{related},
	})
	return err
}

func (m *DurableMemory) AppendHarnessEvent(ctx context.Context, sessionID string, kind EventType, data any) error {
	event, err := sessionlog.New(kind, data)
	if err != nil {
		return err
	}
	_, err = m.AppendSessionEvent(ctx, sessionID, event)
	return err
}

// AppendSessionEvent is the efficient v2 diagnostic path. It persists without
// rebuilding the model surface because diagnostic events are never context.
func (m *DurableMemory) AppendSessionEvent(ctx context.Context, sessionID string, event SessionEvent) (SessionEvent, error) {
	return appendSessionEvent(ctx, m.store, sessionID, event)
}

func (m *DurableMemory) ReadAllTranscript(ctx context.Context, sessionID string) ([]models.Message, error) {
	messages, err := m.store.Messages(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	return state.ToModelMessages(messages), nil
}

func (m *DurableMemory) Clear(ctx context.Context, sessionID string) error {
	return m.store.ArchiveMessages(ctx, sessionID)
}

func (m *DurableMemory) SyncSurface(ctx context.Context, session *Session) error {
	if m == nil || m.store == nil || session == nil {
		return nil
	}
	if !session.surfaceReady {
		return session.AttachSurface(ctx, m.store)
	}
	return session.PullSurface(ctx, m.store)
}

type legacySessionCheckpoint struct {
	SessionID     string            `json:"session_id"`
	Messages      []models.Message  `json:"messages"`
	Summary       string            `json:"summary,omitempty"`
	TaskSummaries map[string]string `json:"task_summaries,omitempty"`
}

// importLegacyMemory preserves old compacted checkpoints as history without
// rewriting their source files. It runs only while a legacy session is first
// promoted to SQLite.
func importLegacyMemory(root, sessionID string) error {
	store, err := state.Open(root)
	if err != nil {
		return err
	}
	existing, err := store.Messages(context.Background(), sessionID, true)
	if err != nil {
		return err
	}
	checkpoint, data, source, err := legacyCheckpoint(root, sessionID)
	if err != nil {
		return err
	}
	if len(existing) == 0 && len(data) > 0 {
		memory := newDurableMemory(store)
		for _, message := range checkpoint.Messages {
			if err := memory.Append(context.Background(), sessionID, message); err != nil {
				return err
			}
		}
		summaries := checkpoint.TaskSummaries
		if checkpoint.Summary != "" {
			if summaries == nil {
				summaries = map[string]string{}
			}
			summaries[""] = checkpoint.Summary
		}
		for taskID, summary := range summaries {
			if strings.TrimSpace(summary) == "" {
				continue
			}
			if _, err := store.AppendMessage(context.Background(), state.MessageInput{SessionID: sessionID, Role: string(models.RoleSystem), TaskID: taskID, Parts: []state.MessagePartInput{{Kind: "legacy_summary", Text: summary}}}); err != nil {
				return err
			}
		}
	}
	if err := importLegacyScratchpads(root, store, sessionID); err != nil {
		return err
	}
	if len(data) > 0 {
		return store.RecordLegacyImport(context.Background(), source, data)
	}
	return nil
}

func validateLegacyImport(root, sessionID string) error {
	if _, _, _, err := legacyCheckpoint(root, sessionID); err != nil {
		return err
	}
	directory := filepath.Join(root, ".scratchpad", safeSessionID(sessionID))
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, err := os.ReadFile(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func legacyCheckpoint(root, sessionID string) (legacySessionCheckpoint, []byte, string, error) {
	for _, path := range []string{filepath.Join(root, ".sessions", safeSessionID(sessionID)+".json"), filepath.Join(root, ".session", safeSessionID(sessionID)+".json")} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return legacySessionCheckpoint{}, nil, "", err
		}
		var checkpoint legacySessionCheckpoint
		if err := json.Unmarshal(data, &checkpoint); err != nil {
			return legacySessionCheckpoint{}, nil, "", fmt.Errorf("import legacy memory: %w", err)
		}
		return checkpoint, data, path, nil
	}
	return legacySessionCheckpoint{}, nil, "", nil
}

func importLegacyScratchpads(root string, store *state.Store, sessionID string) error {
	directory := filepath.Join(root, ".scratchpad", safeSessionID(sessionID))
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := store.PutArtifact(context.Background(), state.ArtifactInput{Data: data, ContentType: "text/plain; charset=utf-8", Origin: "legacy-scratchpad:" + entry.Name()}); err != nil {
			return err
		}
		if err := store.RecordLegacyImport(context.Background(), path, data); err != nil {
			return err
		}
	}
	return nil
}
