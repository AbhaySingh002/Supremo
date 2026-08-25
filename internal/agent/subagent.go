package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

type SubagentScope string

const (
	SubagentScopeLocalRead SubagentScope = "local_read"
	SubagentScopeExecution SubagentScope = "execution"
	maxDelegationDepth                   = 3
)

type SubagentRequest struct {
	ParentSessionID string
	Label           string
	Prompt          string
	Scope           SubagentScope
	RunInBackground *bool
	IdempotencyKey  string
	RequestDigest   string
}

type SubagentDescriptor struct {
	AgentID         string        `json:"agent_id"`
	ParentSessionID string        `json:"parent_session_id"`
	Label           string        `json:"label"`
	Depth           int           `json:"depth"`
	Scope           SubagentScope `json:"scope"`
	Provider        string        `json:"provider,omitempty"`
	Model           string        `json:"model,omitempty"`
}

type SubagentRun struct {
	AgentID   string `json:"agent_id"`
	MessageID string `json:"message_id"`
	RunID     string `json:"run_id,omitempty"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type SubagentStatus struct {
	SubagentDescriptor
	Status string `json:"status"`
}

type SubagentManager struct {
	workspace string
	store     *state.Store
	runtimes  *RuntimeManager
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	workers   map[string]bool
	updates   map[string]chan struct{}
	failures  map[string]error
	cancelled map[string]bool
	closed    bool
	active    sync.WaitGroup
}

func NewSubagentManager(workspace string, store *state.Store, runtimes *RuntimeManager) (*SubagentManager, error) {
	if strings.TrimSpace(workspace) == "" || store == nil || runtimes == nil {
		return nil, errors.New("workspace, state store, and runtime manager are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &SubagentManager{
		workspace: workspace, store: store, runtimes: runtimes, ctx: ctx, cancel: cancel,
		workers: make(map[string]bool), updates: make(map[string]chan struct{}), failures: make(map[string]error), cancelled: make(map[string]bool),
	}, nil
}

func (m *SubagentManager) Start(ctx context.Context, request SubagentRequest) (SubagentRun, error) {
	if m == nil {
		return SubagentRun{}, errors.New("subagent manager is required")
	}
	request.ParentSessionID = strings.TrimSpace(request.ParentSessionID)
	request.Label = strings.TrimSpace(request.Label)
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.ParentSessionID == "" || request.Label == "" || request.Prompt == "" {
		return SubagentRun{}, errors.New("parent session, label, and prompt are required")
	}
	if request.Scope == "" {
		request.Scope = SubagentScopeLocalRead
	}
	if request.Scope != SubagentScopeLocalRead && request.Scope != SubagentScopeExecution {
		return SubagentRun{}, fmt.Errorf("invalid subagent scope %q", request.Scope)
	}
	if _, err := validatedSessionName(request.Label); err != nil {
		return SubagentRun{}, fmt.Errorf("invalid subagent label: %w", err)
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return SubagentRun{}, errors.New("subagent manager is closed")
	}
	parent, err := LoadSession(m.workspace, request.ParentSessionID)
	if err != nil {
		return SubagentRun{}, fmt.Errorf("load parent session: %w", err)
	}
	if err := parent.AttachSurface(ctx, m.store); err != nil {
		return SubagentRun{}, fmt.Errorf("load parent surface: %w", err)
	}
	if parent.DelegationDepth >= maxDelegationDepth {
		return SubagentRun{}, fmt.Errorf("delegation depth limit %d reached", maxDelegationDepth)
	}
	if parent.Origin == "subagent" && parent.DelegationScope == SubagentScopeLocalRead && request.Scope == SubagentScopeExecution {
		return SubagentRun{}, errors.New("local_read subagent cannot widen delegated scope to execution")
	}
	if parent.PlanModeActive() {
		request.Scope = SubagentScopeLocalRead
	}
	childID, err := newSessionID()
	if err != nil {
		return SubagentRun{}, err
	}
	messageID, err := newSessionID()
	if err != nil {
		return SubagentRun{}, err
	}
	now := time.Now().UTC()
	child := &Session{
		ID: childID, Name: request.Label, CreatedAt: now, Status: "active", NeedsName: false,
		Provider: parent.Provider, Model: parent.Model, DryRun: parent.DryRun, ApprovalMode: parent.ApprovalMode,
		ParentSessionID: parent.ID, Origin: "subagent", DelegationLabel: request.Label,
		DelegationDepth: parent.DelegationDepth + 1, DelegationScope: request.Scope,
	}
	descriptor := sessionlog.SubagentDescriptorPayload{
		Version: 1, ParentSessionID: parent.ID, Label: request.Label, Depth: child.DelegationDepth,
		Scope: string(request.Scope), Provider: child.Provider, Model: child.Model,
		ApprovalMode: string(child.ApprovalMode), DryRun: child.DryRun,
	}
	queued := sessionlog.SubagentQueuedPayload{MessageID: messageID, SenderSessionID: parent.ID, Content: request.Prompt, RequestDigest: request.RequestDigest}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SubagentRun{}, errors.New("subagent manager is closed")
	}
	if err := m.createChildIdempotent(ctx, child, descriptor, queued, request.IdempotencyKey); err != nil {
		m.mu.Unlock()
		return SubagentRun{}, err
	}
	m.ensureWorkerLocked(childID)
	m.mu.Unlock()
	run := SubagentRun{AgentID: childID, MessageID: messageID, Status: "queued"}
	if request.RunInBackground == nil || *request.RunInBackground {
		return run, nil
	}
	stop := context.AfterFunc(ctx, func() { m.cancelMessage(childID, messageID) })
	defer stop()
	return m.Wait(ctx, parent.ID, childID, messageID)
}

func (m *SubagentManager) createChild(ctx context.Context, child *Session, descriptor sessionlog.SubagentDescriptorPayload, queued sessionlog.SubagentQueuedPayload) error {
	return m.createChildIdempotent(ctx, child, descriptor, queued, "")
}

func (m *SubagentManager) createChildIdempotent(ctx context.Context, child *Session, descriptor sessionlog.SubagentDescriptorPayload, queued sessionlog.SubagentQueuedPayload, idempotencyKey string) error {
	data, err := json.MarshalIndent(child, "", "  ")
	if err != nil {
		return err
	}
	descriptorEvent, err := sessionlog.New(sessionlog.EventSubagentDescriptor, descriptor)
	if err != nil {
		return err
	}
	queuedEvent, err := sessionlog.New(sessionlog.EventSubagentQueued, queued)
	if err != nil {
		return err
	}
	related := make([]state.EventInput, 0, 2)
	for _, event := range []sessionlog.Record{descriptorEvent, queuedEvent} {
		input, err := sessionlog.ToEventInput(child.ID, event)
		if err != nil {
			return err
		}
		related = append(related, input)
	}
	if idempotencyKey != "" {
		related[len(related)-1].IdempotencyKey = idempotencyKey
	}
	saved, err := m.store.SaveSession(ctx, state.SessionInput{
		ID: child.ID, Name: child.Name, CreatedAt: child.CreatedAt, Status: child.Status,
		Provider: child.Provider, Model: child.Model, Data: data, RelatedEvents: related,
	})
	if err != nil {
		return fmt.Errorf("create subagent session: %w", err)
	}
	child.Version, child.UpdatedAt = saved.Version, saved.UpdatedAt
	return nil
}

func (m *SubagentManager) Send(ctx context.Context, parentID, childID, content string) (SubagentRun, error) {
	return m.SendIdempotent(ctx, parentID, childID, content, "", "")
}

func (m *SubagentManager) SendIdempotent(ctx context.Context, parentID, childID, content, idempotencyKey, requestDigest string) (SubagentRun, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return SubagentRun{}, errors.New("message is required")
	}
	if _, err := m.requireDirectChild(parentID, childID); err != nil {
		return SubagentRun{}, err
	}
	messageID, err := newSessionID()
	if err != nil {
		return SubagentRun{}, err
	}
	event, err := sessionlog.New(sessionlog.EventSubagentQueued, sessionlog.SubagentQueuedPayload{MessageID: messageID, SenderSessionID: parentID, Content: content, RequestDigest: requestDigest})
	if err != nil {
		return SubagentRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return SubagentRun{}, errors.New("subagent manager is closed")
	}
	if idempotencyKey != "" {
		ctx = sessionlog.WithEventMeta(ctx, sessionlog.EventMeta{IdempotencyKey: idempotencyKey})
	}
	if _, err := sessionlog.Append(ctx, m.store, childID, event); err != nil {
		return SubagentRun{}, fmt.Errorf("queue subagent message: %w", err)
	}
	m.ensureWorkerLocked(childID)
	return SubagentRun{AgentID: childID, MessageID: messageID, Status: "queued"}, nil
}

func (m *SubagentManager) Wait(ctx context.Context, parentID, childID, messageID string) (SubagentRun, error) {
	if _, err := m.requireDirectChild(parentID, childID); err != nil {
		return SubagentRun{}, err
	}
	for {
		records, err := sessionlog.Load(ctx, m.store, childID)
		if err != nil {
			return SubagentRun{}, err
		}
		if messageID == "" {
			messageID = latestQueuedMessage(records)
		}
		if messageID == "" {
			return SubagentRun{}, errors.New("subagent has no queued messages")
		}
		if end, ok := findSubagentEnd(records, messageID); ok {
			return SubagentRun{AgentID: childID, MessageID: messageID, RunID: end.RunID, Status: end.Status, Output: end.Output, Error: end.Error}, nil
		}
		m.mu.Lock()
		if failure := m.failures[messageID]; failure != nil {
			m.mu.Unlock()
			return SubagentRun{}, failure
		}
		if failure := m.failures[childID]; failure != nil {
			m.mu.Unlock()
			return SubagentRun{}, failure
		}
		updates := m.updateChannelLocked(childID)
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return SubagentRun{}, ctx.Err()
		case <-updates:
		}
	}
}

func (m *SubagentManager) List(ctx context.Context, parentID string, descendants bool) ([]SubagentStatus, error) {
	if _, err := LoadSession(m.workspace, parentID); err != nil {
		return nil, fmt.Errorf("load caller session: %w", err)
	}
	sessions, err := m.store.Sessions(ctx, false)
	if err != nil {
		return nil, err
	}
	all := make(map[string]Session)
	for _, saved := range sessions {
		var session Session
		if json.Unmarshal(saved.Data, &session) == nil && session.Origin == "subagent" {
			session.CreatedAt = saved.CreatedAt
			all[session.ID] = session
		}
	}
	var result []SubagentStatus
	for _, child := range all {
		include := child.ParentSessionID == parentID
		if descendants && !include {
			for ancestor := child.ParentSessionID; ancestor != ""; {
				if ancestor == parentID {
					include = true
					break
				}
				parent, ok := all[ancestor]
				if !ok {
					break
				}
				ancestor = parent.ParentSessionID
			}
		}
		if !include {
			continue
		}
		result = append(result, SubagentStatus{SubagentDescriptor: descriptorFromSession(child), Status: m.liveStatus(child.ID)})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth == result[j].Depth {
			return result[i].AgentID < result[j].AgentID
		}
		return result[i].Depth < result[j].Depth
	})
	return result, nil
}

func (m *SubagentManager) Interrupt(ctx context.Context, parentID, childID string) error {
	allowed, err := m.isAncestor(parentID, childID)
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("caller is not an ancestor of the subagent")
	}
	records, err := sessionlog.Load(ctx, m.store, childID)
	if err != nil {
		return fmt.Errorf("load subagent runs: %w", err)
	}
	if messageID := firstPendingMessage(records); messageID != "" {
		m.mu.Lock()
		m.cancelled[messageID] = true
		m.mu.Unlock()
	}
	m.runtimes.CancelSession(childID)
	return nil
}

func (m *SubagentManager) Recover(ctx context.Context) error {
	sessions, err := m.store.Sessions(ctx, false)
	if err != nil {
		return err
	}
	for _, saved := range sessions {
		var session Session
		if json.Unmarshal(saved.Data, &session) != nil || session.Origin != "subagent" {
			continue
		}
		records, err := sessionlog.Load(ctx, m.store, session.ID)
		if err != nil {
			return err
		}
		if err := m.repairRuns(ctx, session.ID, records); err != nil {
			return err
		}
		records, err = sessionlog.Load(ctx, m.store, session.ID)
		if err != nil {
			return err
		}
		if firstPendingMessage(records) != "" {
			m.mu.Lock()
			m.ensureWorkerLocked(session.ID)
			m.mu.Unlock()
		}
	}
	return nil
}

func (m *SubagentManager) repairRuns(ctx context.Context, childID string, records []sessionlog.Record) error {
	for index, record := range records {
		start, ok := record.Data.(sessionlog.SubagentRunStartPayload)
		if !ok {
			continue
		}
		if _, ended := findSubagentEnd(records, start.MessageID); ended {
			continue
		}
		status, output := "interrupted", ""
		for _, later := range records[index+1:] {
			if later.Type == sessionlog.EventSubagentRunStart {
				break
			}
			if later.Type == sessionlog.EventAssistantMessage && strings.TrimSpace(later.Message.Content) != "" {
				output = later.Message.Content
			}
			if ended, ok := later.Data.(sessionlog.TurnEndPayload); ok && ended.Reason == "completed" {
				status = "recovered-completed"
			}
		}
		end, err := sessionlog.New(sessionlog.EventSubagentRunEnd, sessionlog.SubagentRunEndPayload{RunID: start.RunID, MessageID: start.MessageID, Status: status, Output: output, Recovered: true})
		if err != nil {
			return err
		}
		if _, err := sessionlog.Append(ctx, m.store, childID, end); err != nil {
			return err
		}
	}
	return nil
}

func (m *SubagentManager) ensureWorkerLocked(childID string) {
	if m.closed || m.workers[childID] {
		return
	}
	delete(m.failures, childID)
	m.workers[childID] = true
	m.active.Add(1)
	go m.work(childID)
}

func (m *SubagentManager) work(childID string) {
	defer m.active.Done()
	for {
		records, err := sessionlog.Load(m.ctx, m.store, childID)
		if err != nil {
			m.failWorker(childID, "", err)
			return
		}
		messageID := firstPendingMessage(records)
		if messageID == "" {
			m.mu.Lock()
			records, err = sessionlog.Load(context.WithoutCancel(m.ctx), m.store, childID)
			if err == nil && firstPendingMessage(records) == "" {
				delete(m.workers, childID)
				m.mu.Unlock()
				m.runtimes.Release(childID)
				return
			}
			m.mu.Unlock()
			if err != nil {
				m.failWorker(childID, "", err)
				return
			}
			continue
		}
		queued, found := findQueued(records, messageID)
		if !found {
			m.failWorker(childID, messageID, fmt.Errorf("queued subagent message %s is missing", messageID))
			return
		}
		if err := m.runQueued(childID, queued); err != nil {
			m.failWorker(childID, messageID, err)
			return
		}
	}
}

func (m *SubagentManager) runQueued(childID string, queued sessionlog.SubagentQueuedPayload) error {
	runID, err := newSessionID()
	if err != nil {
		return err
	}
	start, err := sessionlog.New(sessionlog.EventSubagentRunStart, sessionlog.SubagentRunStartPayload{RunID: runID, MessageID: queued.MessageID})
	if err != nil {
		return err
	}
	if _, err := sessionlog.Append(m.ctx, m.store, childID, start); err != nil {
		return fmt.Errorf("persist subagent run start: %w", err)
	}
	m.notify(childID)
	m.mu.Lock()
	cancelled := m.cancelled[queued.MessageID]
	delete(m.cancelled, queued.MessageID)
	m.mu.Unlock()
	status, output, errorText := "cancelled", "", ""
	if !cancelled {
		child, loadErr := LoadSession(m.workspace, childID)
		if loadErr != nil {
			status, errorText = "failed", loadErr.Error()
		} else {
			output, err = m.runtimes.Run(m.ctx, child, queued.Content)
			switch {
			case err == nil:
				status = "completed"
			case errors.Is(err, context.Canceled):
				status, errorText = "cancelled", err.Error()
			default:
				status, errorText = "failed", err.Error()
			}
		}
	}
	m.mu.Lock()
	delete(m.cancelled, queued.MessageID)
	m.mu.Unlock()
	end, err := sessionlog.New(sessionlog.EventSubagentRunEnd, sessionlog.SubagentRunEndPayload{RunID: runID, MessageID: queued.MessageID, Status: status, Output: output, Error: errorText})
	if err != nil {
		return err
	}
	terminalCtx, cancel := terminalContext(m.ctx)
	defer cancel()
	if _, err := sessionlog.Append(terminalCtx, m.store, childID, end); err != nil {
		return fmt.Errorf("persist subagent run end: %w", err)
	}
	m.notify(childID)
	return nil
}

func (m *SubagentManager) cancelMessage(childID, messageID string) {
	m.mu.Lock()
	m.cancelled[messageID] = true
	m.mu.Unlock()
	m.runtimes.CancelSession(childID)
}

func (m *SubagentManager) failWorker(childID, messageID string, err error) {
	m.mu.Lock()
	delete(m.workers, childID)
	m.failures[childID] = err
	if messageID != "" {
		m.failures[messageID] = err
	}
	m.broadcastLocked(childID)
	m.mu.Unlock()
}

func (m *SubagentManager) notify(childID string) {
	m.mu.Lock()
	m.broadcastLocked(childID)
	m.mu.Unlock()
}

func (m *SubagentManager) updateChannelLocked(childID string) chan struct{} {
	if m.updates[childID] == nil {
		m.updates[childID] = make(chan struct{})
	}
	return m.updates[childID]
}

func (m *SubagentManager) broadcastLocked(childID string) {
	if updates := m.updates[childID]; updates != nil {
		close(updates)
	}
	m.updates[childID] = make(chan struct{})
}

func (m *SubagentManager) requireDirectChild(parentID, childID string) (*Session, error) {
	if parentID == "" || parentID == childID {
		return nil, errors.New("caller and child session IDs are required and must differ")
	}
	if _, err := LoadSession(m.workspace, parentID); err != nil {
		return nil, fmt.Errorf("load caller session: %w", err)
	}
	child, err := LoadSession(m.workspace, childID)
	if err != nil {
		return nil, fmt.Errorf("load subagent: %w", err)
	}
	if child.Origin != "subagent" || child.ParentSessionID != parentID {
		return nil, errors.New("subagent is not a direct child of the caller")
	}
	return child, nil
}

func (m *SubagentManager) isAncestor(parentID, childID string) (bool, error) {
	if parentID == "" || parentID == childID {
		return false, nil
	}
	if _, err := LoadSession(m.workspace, parentID); err != nil {
		return false, fmt.Errorf("load caller session: %w", err)
	}
	current, err := LoadSession(m.workspace, childID)
	if err != nil {
		return false, fmt.Errorf("load subagent: %w", err)
	}
	if current.Origin != "subagent" || current.ID == parentID {
		return false, nil
	}
	for current.ParentSessionID != "" {
		if current.ParentSessionID == parentID {
			return true, nil
		}
		current, err = LoadSession(m.workspace, current.ParentSessionID)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func (m *SubagentManager) liveStatus(childID string) string {
	m.mu.Lock()
	running := m.workers[childID]
	m.mu.Unlock()
	if running {
		return "running"
	}
	if m.runtimes.existing(childID) != nil {
		return "idle"
	}
	return "ready"
}

func descriptorFromSession(session Session) SubagentDescriptor {
	return SubagentDescriptor{AgentID: session.ID, ParentSessionID: session.ParentSessionID, Label: session.DelegationLabel, Depth: session.DelegationDepth, Scope: session.DelegationScope, Provider: session.Provider, Model: session.Model}
}

func findQueued(records []sessionlog.Record, messageID string) (sessionlog.SubagentQueuedPayload, bool) {
	for _, record := range records {
		if queued, ok := record.Data.(sessionlog.SubagentQueuedPayload); ok && queued.MessageID == messageID {
			return queued, true
		}
	}
	return sessionlog.SubagentQueuedPayload{}, false
}

func latestQueuedMessage(records []sessionlog.Record) string {
	latest := ""
	for _, record := range records {
		if queued, ok := record.Data.(sessionlog.SubagentQueuedPayload); ok {
			latest = queued.MessageID
		}
	}
	return latest
}

func firstPendingMessage(records []sessionlog.Record) string {
	started := make(map[string]bool)
	for _, record := range records {
		if start, ok := record.Data.(sessionlog.SubagentRunStartPayload); ok {
			started[start.MessageID] = true
		}
	}
	for _, record := range records {
		if queued, ok := record.Data.(sessionlog.SubagentQueuedPayload); ok && !started[queued.MessageID] {
			return queued.MessageID
		}
	}
	return ""
}

func findSubagentEnd(records []sessionlog.Record, messageID string) (sessionlog.SubagentRunEndPayload, bool) {
	for _, record := range records {
		if end, ok := record.Data.(sessionlog.SubagentRunEndPayload); ok && end.MessageID == messageID {
			return end, true
		}
	}
	return sessionlog.SubagentRunEndPayload{}, false
}

func (m *SubagentManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
	}
	children := make([]string, 0, len(m.workers))
	for childID := range m.workers {
		children = append(children, childID)
	}
	m.mu.Unlock()
	for _, childID := range children {
		m.runtimes.CancelSession(childID)
	}
	done := make(chan struct{})
	go func() { m.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("subagent workers did not stop within five seconds")
	}
}
