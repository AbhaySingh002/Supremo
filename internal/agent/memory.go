package agent

import (
	"context"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// InMemoryMemory implements the Memory interface using an in-memory store.
type InMemoryMemory struct {
	mu       sync.RWMutex
	messages map[string][]models.Message // sessionID -> messages
}

// NewInMemoryMemory creates a new InMemoryMemory instance.
func NewInMemoryMemory() *InMemoryMemory {
	return &InMemoryMemory{
		messages: make(map[string][]models.Message),
	}
}

// Append implements agent.Memory by storing a message in the session's history.
func (m *InMemoryMemory) Append(ctx context.Context, sessionID string, msg models.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[sessionID] = append(m.messages[sessionID], msg)
	return nil
}

// Get retrieves the conversation history for a session.
func (m *InMemoryMemory) Get(ctx context.Context, sessionID string) ([]models.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs, exists := m.messages[sessionID]
	if !exists {
		return []models.Message{}, nil
	}

	// Return a copy to avoid race conditions
	copied := make([]models.Message, len(msgs))
	copy(copied, msgs)
	return copied, nil
}

// Clear removes all messages for a session.
func (m *InMemoryMemory) Clear(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.messages, sessionID)
	return nil
}
