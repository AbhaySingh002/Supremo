package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/storage"
)

type ApprovalRequest struct {
	InteractionID string
	SessionID     string
	RunID         string
	MessageID     string
	CallID        string
	Tool          string
	Arguments     string
	Input         any
}

type ApprovalResolution struct {
	Decision string
	Reason   string
	Input    any
}

type ApprovalRecorder interface {
	RecordApprovalRequested(context.Context, ApprovalRequest) error
	RecordApprovalResolved(context.Context, ApprovalRequest, ApprovalResolution) error
}

// Manager handles tool execution with validation and registry lookup.
type Manager struct {
	registry         *Registry
	mu               sync.Mutex
	pending          *approvalRequest
	report           func(Event)
	activity         []Activity
	approvalRecorder ApprovalRecorder
}

// SetReporter emits execution lifecycle events for interactive clients.
func (m *Manager) SetReporter(report func(Event)) { m.report = report }

func (m *Manager) SetApprovalRecorder(recorder ApprovalRecorder) { m.approvalRecorder = recorder }

// Approve releases the one pending mutating tool call with its original input.
func (m *Manager) Approve() bool {
	return m.ResolveApproval("", ApprovalResolution{Decision: "approved"}) == nil
}

// ApproveWithInput replaces a pending tool's arguments, validates the revised
// object, then executes that revised action only.
func (m *Manager) ApproveWithInput(input any) bool {
	return m.ResolveApproval("", ApprovalResolution{Decision: "edited", Input: input}) == nil
}

// Deny rejects the one pending mutating tool call.
func (m *Manager) Deny(reason string) bool {
	if reason == "" {
		reason = "tool execution denied"
	}
	return m.ResolveApproval("", ApprovalResolution{Decision: "denied", Reason: reason}) == nil
}

// HasPendingApproval reports whether this session tool manager is waiting for a decision.
func (m *Manager) HasPendingApproval() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending != nil
}

func (m *Manager) ResolveApproval(interactionID string, resolution ApprovalResolution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.pending
	if request == nil {
		return fmt.Errorf("no tool approval is pending")
	}
	if interactionID != "" && request.id != interactionID {
		return fmt.Errorf("pending interaction does not match %q", interactionID)
	}
	decision := approvalDecision{}
	switch resolution.Decision {
	case "", "approved":
		decision.input = request.input
	case "edited":
		decision.input, decision.revised = resolution.Input, true
	case "denied", "cancelled", "interrupted":
		if resolution.Reason == "" {
			resolution.Reason = "tool execution " + resolution.Decision
		}
		decision.err = fmt.Errorf("%s", resolution.Reason)
	default:
		return fmt.Errorf("invalid approval decision %q", resolution.Decision)
	}
	if m.approvalRecorder != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := m.approvalRecorder.RecordApprovalResolved(ctx, approvalRequestValue(request), resolution)
		cancel()
		if err != nil {
			return err
		}
	}
	m.pending = nil
	request.decision <- decision
	return nil
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
	start := time.Now()
	tool, err := m.registry.Get(name)
	if err != nil {
		logging.Error("Tool lookup failed (tool=%s): %v", name, err)
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "failed", Input: input, Error: err})
		return nil, err
	}
	desc, _ := m.registry.Descriptor(name)
	if !ToolIsActive(ctx, name) {
		logging.Warn("Tool execution denied (not active: tool=%s)", name)
		result := BuildToolResult(false, "Tool is unavailable for this request", nil)
		result.Status = ToolStatusDenied
		result.Error = &ToolError{Class: ErrorClassPermission, Message: result.Message}
		m.record(ctx, name, "denied", result.Message, renderToolCall(name, input), "")
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "denied", Input: input, Result: result, Arguments: renderToolCall(name, input)})
		return result, nil
	}
	if IsResearchOnly(ctx) {
		descriptor, err := m.registry.Descriptor(name)
		if err != nil {
			return nil, err
		}
		if !descriptor.PlanningSafe() {
			logging.Warn("Tool execution denied during plan research (not safe: tool=%s)", name)
			err := classify(ErrorClassPermission, fmt.Errorf("tool %q is not allowed during local plan research", name))
			recordLifecycle(ctx, Lifecycle{Tool: name, Status: "denied", Input: input, Error: err})
			return nil, err
		}
	}
	arguments := renderToolCall(name, input)
	if err := m.Validate(name, input); err != nil {
		logging.Warn("Tool validation failed (tool=%s): %v", name, err)
		result := BuildToolResult(false, "Invalid tool arguments: "+err.Error(), nil)
		result.Error = &ToolError{Class: ErrorClassToolArgument, Message: result.Message}
		m.record(ctx, name, "failed", result.Message, arguments, "")
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "failed", Input: input, Result: result, Arguments: arguments})
		return result, nil
	}
	if IsReadOnly(ctx) && !desc.PlanningSafe() {
		logging.Warn("Tool execution denied in read-only context (tool=%s)", name)
		err := classify(ErrorClassPermission, fmt.Errorf("tool %q is not allowed in a read-only execution context", name))
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "denied", Input: input, Error: err, Arguments: arguments})
		return nil, err
	}

	if IsDryRun(ctx) && desc.RequiresApproval {
		logging.Info("Tool dry-run (tool=%s args=%s)", name, arguments)
		result := BuildToolResult(true, "Dry run: would execute "+renderToolCall(name, input), nil)
		result.Status = ToolStatusDryRun
		m.record(ctx, name, "dry run", result.Message, renderToolCall(name, input), "")
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "dry_run", Input: input, Result: result, Arguments: arguments, Access: desc.Access, SideEffect: desc.SideEffect, Family: desc.Family})
		return result, nil
	}
	if RequiresApprovalFor(ctx, desc, input) {
		logging.Info("Tool approval requested (tool=%s args=%s)", name, arguments)
		approvedInput, revised, err := m.waitForApproval(ctx, name, input)
		if err != nil {
			logging.Warn("Tool approval rejected (tool=%s): %v", name, err)
			m.record(ctx, name, "denied", err.Error(), renderToolCall(name, input), "")
			recordLifecycle(ctx, Lifecycle{Tool: name, Status: "denied", Input: input, Error: err, Arguments: arguments})
			return nil, classify(ErrorClassPermission, err)
		}
		input = approvedInput
		if revised {
			if err := m.Validate(name, input); err != nil {
				logging.Warn("Tool revised arguments invalid (tool=%s): %v", name, err)
				m.record(ctx, name, "denied", "revised arguments are invalid: "+err.Error(), renderToolCall(name, input), "")
				recordLifecycle(ctx, Lifecycle{Tool: name, Status: "denied", Input: input, Error: err, Arguments: arguments})
				return nil, classify(ErrorClassToolArgument, fmt.Errorf("revised tool arguments: %w", err))
			}
		}
		logging.Info("Tool approval granted (tool=%s revised=%v)", name, revised)
		m.record(ctx, name, "approved", "", renderToolCall(name, input), "")
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "approved", Input: input, Arguments: renderToolCall(name, input)})
	}
	arguments = renderToolCall(name, input)
	change := captureFileChange(ctx, desc, input)
	checkpoint, err := beginCheckpoint(ctx, desc, input)
	if err != nil {
		logging.Error("Tool checkpoint failed (tool=%s): %v", name, err)
		m.record(ctx, name, "failed", err.Error(), arguments, "")
		recordLifecycle(ctx, Lifecycle{Tool: name, Status: "failed", Input: input, Error: err, Arguments: arguments})
		return nil, err
	}
	if checkpoint != nil {
		defer func() {
			if summary := checkpoint.finish(); summary != nil {
				logging.Info("Tool checkpoint created (tool=%s summary=%+v)", name, summary)
				recordLifecycle(ctx, Lifecycle{Tool: name, Status: "checkpoint", Checkpoint: summary})
				m.recordEvent(ctx, Event{Time: time.Now().UTC(), Tool: name, Status: "checkpoint", Checkpoint: summary})
			}
		}()
	}
	logging.Info("Tool execution starting (tool=%s args=%s)", name, arguments)
	m.recordEvent(ctx, Event{Time: time.Now().UTC(), Tool: name, Status: "running", Arguments: arguments})
	recordLifecycle(ctx, Lifecycle{Tool: name, Status: "called", Input: input, Arguments: arguments, Access: desc.Access, SideEffect: desc.SideEffect, Family: desc.Family})
	result, err := executeTool(ctx, tool, name, input)
	result, rawOutput := NormalizeToolResult(name, input, result, err)
	duration := time.Since(start)
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
		logging.Warn("Tool execution non-success (tool=%s status=%s duration=%v): %s", name, status, duration, message)
	} else {
		logging.Info("Tool execution completed (tool=%s duration=%v): %s", name, duration, message)
	}
	enrichment := recordLifecycle(ctx, Lifecycle{Tool: name, Status: status, Input: input, Result: result, Error: err, Arguments: arguments, RawOutput: rawOutput, Access: desc.Access, SideEffect: desc.SideEffect, Family: desc.Family})
	if enrichment.ArtifactID != "" {
		result.ArtifactID = enrichment.ArtifactID
	}
	if enrichment.WorldRevision != "" {
		result.WorldRevision = enrichment.WorldRevision
	}
	m.recordEvent(ctx, Event{
		Time: time.Now().UTC(), Tool: name, Status: status, Message: message, Arguments: arguments,
		Output: toolOutputPreview(result), Diff: change.diff(status == "completed"),
	})
	if err != nil {
		return nil, err
	}
	return result, err
}

func executeTool(ctx context.Context, tool Tool, name string, input any) (result *ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("tool %q panicked: %v", name, recovered)
		}
	}()
	return tool.Execute(ctx, input)
}

// Catalog exposes the registry's immutable routing view to workflow validation.
func (m *Manager) Descriptor(name string) (ToolDescriptor, error) {
	if m == nil || m.registry == nil {
		return ToolDescriptor{}, ErrToolNotFound
	}
	return m.registry.Descriptor(name)
}

func (m *Manager) waitForApproval(ctx context.Context, name string, input any) (any, bool, error) {
	id, err := storage.NewID()
	if err != nil {
		return nil, false, err
	}
	request := &approvalRequest{id: id, decision: make(chan approvalDecision, 1), tool: name, arguments: renderToolCall(name, input), input: input, scope: ProgressScopeFromContext(ctx)}
	m.mu.Lock()
	if m.pending != nil {
		m.mu.Unlock()
		return nil, false, fmt.Errorf("another tool call is already awaiting approval")
	}
	m.pending = request
	if m.approvalRecorder != nil {
		if err := m.approvalRecorder.RecordApprovalRequested(ctx, approvalRequestValue(request)); err != nil {
			m.pending = nil
			m.mu.Unlock()
			return nil, false, err
		}
	}
	m.mu.Unlock()
	m.recordEvent(ctx, Event{Time: time.Now().UTC(), Tool: name, Status: "waiting approval", Arguments: request.arguments, Message: "Approval required"})
	select {
	case decision := <-request.decision:
		return decision.input, decision.revised, decision.err
	case <-ctx.Done():
		m.mu.Lock()
		var persistErr error
		if m.pending == request {
			if m.approvalRecorder != nil {
				terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				persistErr = m.approvalRecorder.RecordApprovalResolved(terminalCtx, approvalRequestValue(request), ApprovalResolution{Decision: "cancelled", Reason: ctx.Err().Error()})
				cancel()
			}
			m.pending = nil
		}
		m.mu.Unlock()
		return nil, false, errors.Join(ctx.Err(), persistErr)
	}
}

func approvalRequestValue(request *approvalRequest) ApprovalRequest {
	return ApprovalRequest{InteractionID: request.id, SessionID: request.scope.SessionID, RunID: request.scope.RunID,
		MessageID: request.scope.MessageID, CallID: request.scope.CallID, Tool: request.tool, Arguments: request.arguments, Input: request.input}
}

// Validate checks the generic required-field portion of a registered tool's schema.
func (m *Manager) Validate(name string, input any) error {
	tool, err := m.registry.Get(name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("tool %q arguments must be an object", name)
	}
	schema, ok := tool.Schema().(map[string]any)
	if !ok {
		return nil
	}
	required, _ := schema["required"].([]string)
	for _, field := range required {
		value, exists := values[field]
		if !exists || value == nil {
			return fmt.Errorf("tool %q arguments are missing %q", name, field)
		}
		if value, ok := value.(string); ok && strings.TrimSpace(value) == "" {
			return fmt.Errorf("tool %q argument %q cannot be empty", name, field)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return nil
	}
	for field, value := range values {
		property, ok := properties[field].(map[string]any)
		if !ok {
			return fmt.Errorf("tool %q arguments contain unknown field %q", name, field)
		}
		if value == nil {
			continue
		}
		if expected, _ := property["type"].(string); expected != "" && !schemaValueMatches(expected, value) {
			return fmt.Errorf("tool %q argument %q must be a %s", name, field, expected)
		}
	}
	return nil
}

func schemaValueMatches(expected string, value any) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		_, ok := value.(float64)
		return ok
	default:
		return true
	}
}

// Recent returns the newest bounded set of local tool activity.
func (m *Manager) Recent() []Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	activity := make([]Activity, len(m.activity))
	copy(activity, m.activity)
	return activity
}

func (m *Manager) record(ctx context.Context, name, status, message, arguments, output string) {
	m.recordEvent(ctx, Event{Time: time.Now().UTC(), Tool: name, Status: status, Message: message, Arguments: arguments, Output: output})
}

func toolOutputPreview(result *ToolResult) string {
	if result == nil {
		return ""
	}
	return result.Preview
}

func (m *Manager) recordEvent(ctx context.Context, event Event) {
	scope := ProgressScopeFromContext(ctx)
	event.SessionID, event.TaskID = scope.SessionID, scope.TaskID
	m.mu.Lock()
	if event.Checkpoint == nil {
		m.activity = append(m.activity, Activity{Time: event.Time, Tool: event.Tool, Status: event.Status, Message: event.Message})
		if len(m.activity) > 50 {
			m.activity = m.activity[len(m.activity)-50:]
		}
	}
	report := m.report
	m.mu.Unlock()
	if report != nil {
		report(event)
	}
}
