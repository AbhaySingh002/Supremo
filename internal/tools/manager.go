package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Manager handles tool execution with validation and registry lookup.
type Manager struct {
	registry *Registry
	mu       sync.Mutex
	pending  *approvalRequest
	report   func(Event)
	activity []Activity
}

// SetReporter emits execution lifecycle events for interactive clients.
func (m *Manager) SetReporter(report func(Event)) { m.report = report }

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

	if IsDryRun(ctx) && RequiresApproval(name) {
		result := BuildToolResult(true, "Dry run: would execute "+renderToolCall(name, input), nil)
		m.record(name, "dry run", result.Message, renderToolCall(name, input), "")
		return result, nil
	}
	if RequiresApprovalFor(ctx, name, input) {
		if err := m.waitForApproval(ctx, name, input); err != nil {
			m.record(name, "denied", err.Error(), renderToolCall(name, input), "")
			return nil, err
		}
		m.record(name, "approved", "", renderToolCall(name, input), "")
	}
	arguments := renderToolCall(name, input)
	m.recordEvent(Event{Time: time.Now().UTC(), Tool: name, Status: "running", Arguments: arguments})
	result, err := tool.Execute(ctx, input)
	status := "completed"
	message := ""
	if result != nil {
		message = result.Message
	}
	if err != nil || result == nil || !result.Success {
		status = "failed"
		if err != nil {
			message = err.Error()
		}
	}
	m.record(name, status, message, arguments, toolOutputPreview(result))
	return result, err
}

func (m *Manager) waitForApproval(ctx context.Context, name string, input any) error {
	request := &approvalRequest{decision: make(chan error, 1), tool: name, arguments: renderToolCall(name, input)}
	m.mu.Lock()
	if m.pending != nil {
		m.mu.Unlock()
		return fmt.Errorf("another tool call is already awaiting approval")
	}
	m.pending = request
	m.mu.Unlock()
	m.recordEvent(Event{Time: time.Now().UTC(), Tool: name, Status: "waiting approval", Arguments: request.arguments, Message: "Approval required"})
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

// Recent returns the newest bounded set of local tool activity.
func (m *Manager) Recent() []Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	activity := make([]Activity, len(m.activity))
	copy(activity, m.activity)
	return activity
}

func (m *Manager) record(name, status, message, arguments, output string) {
	m.recordEvent(Event{Time: time.Now().UTC(), Tool: name, Status: status, Message: message, Arguments: arguments, Output: output})
}

func toolOutputPreview(result *ToolResult) string {
	if result == nil || len(result.Data) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(result.Data, "", "  ")
	if err != nil {
		return ""
	}
	const limit = 12_000
	if len(data) > limit {
		return string(data[:limit]) + "\n… output truncated"
	}
	return string(data)
}

func (m *Manager) recordEvent(event Event) {
	m.mu.Lock()
	m.activity = append(m.activity, Activity{Time: event.Time, Tool: event.Tool, Status: event.Status, Message: event.Message})
	if len(m.activity) > 50 {
		m.activity = m.activity[len(m.activity)-50:]
	}
	report := m.report
	m.mu.Unlock()
	if report != nil {
		report(event)
	}
}
