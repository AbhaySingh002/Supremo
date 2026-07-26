package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/AbhaySingh002/supremo/internal/models"
)

const (
	toolSnippetTokens = 1_000
	summaryTokens     = 1_000
)

// InMemoryMemory keeps the hot conversation window and checkpoints it locally.
type InMemoryMemory struct {
	mu        sync.Mutex
	root      string
	messages  map[string][]models.Message
	summaries map[string]string
	loaded    map[string]bool
}

type sessionCheckpoint struct {
	SessionID string           `json:"session_id"`
	Messages  []models.Message `json:"messages"`
	Summary   string           `json:"summary,omitempty"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// NewInMemoryMemory creates a memory store rooted at the current workspace.
func NewInMemoryMemory() *InMemoryMemory {
	root, _ := os.Getwd()
	return newInMemoryMemory(root)
}

func newInMemoryMemory(root string) *InMemoryMemory {
	return &InMemoryMemory{
		root:      root,
		messages:  make(map[string][]models.Message),
		summaries: make(map[string]string),
		loaded:    make(map[string]bool),
	}
}

// Append stores a message, pruning oversized tool output to a disk-backed snippet.
func (m *InMemoryMemory) Append(_ context.Context, sessionID string, msg models.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadLocked(sessionID); err != nil {
		return err
	}
	if msg.Role == models.RoleTool && estimateTokens(msg.Content) > toolSnippetTokens {
		if err := m.pruneToolLocked(sessionID, &msg); err != nil {
			return err
		}
	}
	m.messages[sessionID] = append(m.messages[sessionID], msg)
	if msg.Role == models.RoleTool {
		if err := m.appendProgressLocked(sessionID, "recorded tool output"); err != nil {
			return err
		}
	}
	return m.checkpointLocked(sessionID)
}

// Get retrieves the current hot history for a session.
func (m *InMemoryMemory) Get(_ context.Context, sessionID string) ([]models.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(sessionID); err != nil {
		return nil, err
	}
	return m.messages[sessionID], nil
}

// GetWindow returns the newest history that fits the message and tool budgets.
// Older messages are condensed into a durable structured summary.
func (m *InMemoryMemory) GetWindow(_ context.Context, sessionID string, messageBudget, toolBudget int) ([]models.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(sessionID); err != nil {
		return nil, err
	}

	messages := m.messages[sessionID]
	start, messageTokens, toolTokens := len(messages), 0, 0
	for start > 0 {
		candidate := messages[start-1]
		tokens := estimateTokens(candidate.Content)
		if candidate.Role == models.RoleTool {
			if toolTokens+tokens > toolBudget {
				break
			}
			toolTokens += tokens
		} else {
			if messageTokens+tokens > messageBudget {
				break
			}
			messageTokens += tokens
		}
		start--
	}
	if start > 0 {
		m.summaries[sessionID] = summarize(m.summaries[sessionID], messages[:start])
		m.messages[sessionID] = messages[start:]
		if err := m.checkpointLocked(sessionID); err != nil {
			return nil, err
		}
	}
	return m.messages[sessionID], nil
}

// GetSummary returns the durable summary for injection into the system context.
func (m *InMemoryMemory) GetSummary(_ context.Context, sessionID string, budget int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(sessionID); err != nil {
		return "", err
	}
	return truncateTokens(m.summaries[sessionID], budget), nil
}

// PersistentContext reads the workspace memory that is shared across sessions.
func (m *InMemoryMemory) PersistentContext(budget int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureStorageLocked(); err != nil {
		return "", err
	}
	memory, err := os.ReadFile(filepath.Join(m.root, ".memory", "MEMORY.md"))
	if err != nil {
		return "", err
	}
	progress, err := os.ReadFile(filepath.Join(m.root, ".memory", "progress.md"))
	if err != nil {
		return "", err
	}
	return truncateTokens("# Workspace Memory\n"+string(memory)+"\n\n# Recent Progress\n"+string(progress), budget), nil
}

// Clear removes a session's hot state and durable checkpoint.
func (m *InMemoryMemory) Clear(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureStorageLocked(); err != nil {
		return err
	}
	delete(m.messages, sessionID)
	delete(m.summaries, sessionID)
	delete(m.loaded, sessionID)
	if err := os.Remove(m.sessionPath(sessionID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return m.appendProgressLocked(sessionID, "cleared session memory")
}

func (m *InMemoryMemory) loadLocked(sessionID string) error {
	if err := m.ensureStorageLocked(); err != nil || m.loaded[sessionID] {
		return err
	}
	m.loaded[sessionID] = true
	data, err := os.ReadFile(m.sessionPath(sessionID))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var checkpoint sessionCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return fmt.Errorf("load session checkpoint: %w", err)
	}
	m.messages[sessionID] = checkpoint.Messages
	m.summaries[sessionID] = checkpoint.Summary
	return nil
}

func (m *InMemoryMemory) checkpointLocked(sessionID string) error {
	data, err := json.MarshalIndent(sessionCheckpoint{
		SessionID: sessionID,
		Messages:  m.messages[sessionID],
		Summary:   m.summaries[sessionID],
		UpdatedAt: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.sessionPath(sessionID), data, 0600)
}

func (m *InMemoryMemory) pruneToolLocked(sessionID string, msg *models.Message) error {
	name := fmt.Sprintf("%d.txt", time.Now().UnixNano())
	dir := filepath.Join(m.root, ".scratchpad", safeSessionID(sessionID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(msg.Content), 0600); err != nil {
		return err
	}
	msg.Content = fmt.Sprintf("[Tool output truncated; full output: %s]\n%s", filepath.ToSlash(filepath.Join(".scratchpad", safeSessionID(sessionID), name)), truncateTokens(msg.Content, toolSnippetTokens))
	return nil
}

func (m *InMemoryMemory) appendProgressLocked(sessionID, observation string) error {
	entry := fmt.Sprintf("- %s session %s: %s\n", time.Now().UTC().Format(time.RFC3339), safeSessionID(sessionID), observation)
	file, err := os.OpenFile(filepath.Join(m.root, ".memory", "progress.md"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(entry)
	return err
}

func (m *InMemoryMemory) ensureStorageLocked() error {
	for _, dir := range []string{".memory", ".session", ".scratchpad"} {
		if err := os.MkdirAll(filepath.Join(m.root, dir), 0700); err != nil {
			return err
		}
	}
	defaults := map[string]string{
		"MEMORY.md": "# Codebase Memory\n\n## Architecture\n- Go CLI coding agent with local tools and Gemini provider support.\n\n## Dependencies\n- Standard library, Google GenAI SDK, and yaml.v3.\n\n## Decisions\n- Keep durable agent state in local Markdown and JSON files.\n",
		"progress.md": "# Progress\n\n",
	}
	for name, content := range defaults {
		path := filepath.Join(m.root, ".memory", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *InMemoryMemory) sessionPath(sessionID string) string {
	return filepath.Join(m.root, ".session", safeSessionID(sessionID)+".json")
}

func summarize(previous string, messages []models.Message) string {
	var summary strings.Builder
	if previous != "" {
		summary.WriteString(previous)
		if !strings.HasSuffix(previous, "\n") {
			summary.WriteByte('\n')
		}
	}
	for _, msg := range messages {
		content := strings.Join(strings.Fields(msg.Content), " ")
		fmt.Fprintf(&summary, "- %s: %s\n", msg.Role, truncateTokens(content, 120))
	}
	return truncateTokens(summary.String(), summaryTokens)
}

func estimateTokens(text string) int {
	return (len([]rune(text)) + 3) / 4
}

func truncateTokens(text string, budget int) string {
	if budget <= 0 || estimateTokens(text) <= budget {
		return text
	}
	runes := []rune(text)
	limit := budget * 4
	if limit > len(runes) {
		limit = len(runes)
	}
	return string(runes[:limit]) + "\n[truncated]"
}

func safeSessionID(id string) string {
	var safe strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('_')
		}
	}
	if safe.Len() == 0 {
		return "session"
	}
	return safe.String()
}
