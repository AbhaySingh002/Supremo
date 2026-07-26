package tools

import (
	"context"
	"fmt"
	"sync"
)

// Manager handles tool execution with validation and registry lookup.
type Manager struct {
	registry *Registry
	mu       sync.Mutex
	pending  *approvalRequest
	report   func(string)
}

// SetReporter emits compact execution status for interactive clients.
func (m *Manager) SetReporter(report func(string)) { m.report = report }

// Approve releases the one pending mutating tool call.
func (m *Manager) Approve() bool { return m.resolveApproval(nil) }

// Deny rejects the one pending mutating tool call.
func (m *Manager) Deny(reason string) bool {
	if reason == "" {
		reason = "tool execution denied"
	}
	return m.resolveApproval(fmt.Errorf("%s", reason))
}

func (m *Manager) resolveApproval(err error) bool {
	m.mu.Lock()
	request := m.pending
	m.pending = nil
	m.mu.Unlock()
	if request == nil {
		return false
	}
	request.decision <- err
	return true
}

// NewManager creates a new Manager with the given tool registry.
func NewManager(r *Registry) *Manager {
	return &Manager{
		registry: r,
	}
}

// Execute retrieves a tool by name, validates the input against its schema,
// and executes the tool with the provided context and input.
func (m *Manager) Execute(
	ctx context.Context,
	name string,
	input any,
) (*ToolResult, error) {

	if err := consumeToolBudget(ctx); err != nil {
		return nil, err
	}
	tool, err := m.registry.Get(name)

	if err != nil {
		return nil, err
	}

	if isMutating(name) {
		if IsDryRun(ctx) {
			return BuildToolResult(true, "Dry run: would execute "+renderToolCall(name, input), nil), nil
		}
		if err := m.waitForApproval(ctx, name, input); err != nil {
			return nil, err
		}
	}
	return tool.Execute(ctx, input)
}

func (m *Manager) waitForApproval(ctx context.Context, name string, input any) error {
	request := &approvalRequest{decision: make(chan error, 1)}
	m.mu.Lock()
	if m.pending != nil {
		m.mu.Unlock()
		return fmt.Errorf("another tool call is already awaiting approval")
	}
	m.pending = request
	report := m.report
	m.mu.Unlock()
	if report != nil {
		report("Approval required: " + renderToolCall(name, input) + " (use /approve or /deny)")
	}
	select {
	case err := <-request.decision:
		return err
	case <-ctx.Done():
		m.mu.Lock()
		if m.pending == request {
			m.pending = nil
		}
		m.mu.Unlock()
		return ctx.Err()
	}
}

// Has reports whether a registered tool can be executed by name.
func (m *Manager) Has(name string) bool {
	_, err := m.registry.Get(name)
	return err == nil
}
