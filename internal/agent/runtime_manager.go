package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

var ErrRuntimeShutdownTimeout = errors.New("session runtimes did not stop within five seconds")

// AgentFactory builds one isolated execution runtime for a session.
type AgentFactory func(sessionID string) (*Agent, error)

// RuntimeManager owns the mutable execution state for every live session.
// Provider, registry, transcript, compiler, repository, and state dependencies
// are shared by the agents returned by the factory.
type RuntimeManager struct {
	mu              sync.Mutex
	factory         AgentFactory
	runtimes        map[string]*Agent
	progress        func(ProgressEvent)
	progressSubs    map[uint64]chan ProgressEvent
	nextProgressSub uint64
	debug           bool
	closed          bool
	active          sync.WaitGroup
	ctx             context.Context
	cancel          context.CancelFunc
}

func NewRuntimeManager(factory AgentFactory) *RuntimeManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &RuntimeManager{factory: factory, runtimes: make(map[string]*Agent), progressSubs: make(map[uint64]chan ProgressEvent), ctx: ctx, cancel: cancel}
}

// For returns the stable in-process runtime assigned to sessionID.
func (m *RuntimeManager) For(sessionID string) (*Agent, error) {
	if m == nil {
		return nil, errors.New("runtime manager is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("runtime manager is closed")
	}
	if runtime := m.runtimes[sessionID]; runtime != nil {
		return runtime, nil
	}
	if m.factory == nil {
		return nil, errors.New("agent factory is required")
	}
	runtime, err := m.factory(sessionID)
	if err != nil {
		return nil, fmt.Errorf("create runtime for session %s: %w", sessionID, err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("create runtime for session %s: factory returned nil", sessionID)
	}
	runtime.SetDebug(m.debug)
	runtime.SetProgress(routeProgress(sessionID, m.emitProgress))
	m.runtimes[sessionID] = runtime
	return runtime, nil
}

func (m *RuntimeManager) begin(sessionID string) (*Agent, context.Context, func(), error) {
	runtime, err := m.For(sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, nil, errors.New("runtime manager is closed")
	}
	m.active.Add(1)
	shutdown := m.ctx
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(shutdown, cancel)
	done := func() {
		stop()
		cancel()
		m.active.Done()
	}
	return runtime, ctx, done, nil
}

func combineRuntimeContext(caller, runtime context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(runtime, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (m *RuntimeManager) Run(ctx context.Context, session *Session, userInput string) (string, error) {
	return m.RunAccepted(ctx, session, RunRequest{Content: userInput})
}

func (m *RuntimeManager) RunAccepted(ctx context.Context, session *Session, request RunRequest) (string, error) {
	if session == nil {
		return "", errors.New("session is required")
	}
	runtime, shutdown, done, err := m.begin(session.ID)
	if err != nil {
		return "", err
	}
	defer done()
	runCtx, cancel := combineRuntimeContext(ctx, shutdown)
	defer cancel()
	return runtime.RunAccepted(runCtx, session, request)
}

func (m *RuntimeManager) AnswerSideQuestion(ctx context.Context, sessionID, question string) (string, error) {
	runtime, shutdown, done, err := m.begin(sessionID)
	if err != nil {
		return "", err
	}
	defer done()
	runCtx, cancel := combineRuntimeContext(ctx, shutdown)
	defer cancel()
	return runtime.AnswerSideQuestion(runCtx, sessionID, question)
}

func (m *RuntimeManager) SetPlanMode(ctx context.Context, session *Session, active bool) error {
	if session == nil {
		return errors.New("session is required")
	}
	runtime, err := m.For(session.ID)
	if err != nil {
		return err
	}
	return runtime.SetPlanMode(ctx, session, active)
}

func (m *RuntimeManager) ClearMemory(ctx context.Context, sessionID string) error {
	runtime, err := m.For(sessionID)
	if err != nil {
		return err
	}
	return runtime.ClearMemory(ctx, sessionID)
}

func (m *RuntimeManager) ReadAllTranscript(ctx context.Context, sessionID string) ([]models.Message, error) {
	runtime, err := m.For(sessionID)
	if err != nil {
		return nil, err
	}
	return runtime.ReadAllTranscript(ctx, sessionID)
}

func (m *RuntimeManager) Checkpoints(root, sessionID string) ([]tools.CheckpointSummary, error) {
	runtime, err := m.For(sessionID)
	if err != nil {
		return nil, err
	}
	return runtime.Checkpoints(root, sessionID)
}

func (m *RuntimeManager) Rewind(ctx context.Context, root, sessionID, checkpointID string, force bool) (tools.RewindResult, error) {
	runtime, err := m.For(sessionID)
	if err != nil {
		return tools.RewindResult{}, err
	}
	return runtime.Rewind(ctx, root, sessionID, checkpointID, force)
}

func (m *RuntimeManager) DeleteSession(ctx context.Context, root, sessionID string) error {
	runtime, err := m.For(sessionID)
	if err != nil {
		return err
	}
	if err := runtime.DeleteSession(ctx, root, sessionID); err != nil {
		return err
	}
	m.Release(sessionID)
	return nil
}

// Release drops one session runtime after cancelling its current turn.
func (m *RuntimeManager) Release(sessionID string) {
	if m == nil || sessionID == "" {
		return
	}
	m.mu.Lock()
	runtime := m.runtimes[sessionID]
	delete(m.runtimes, sessionID)
	m.mu.Unlock()
	if runtime != nil {
		runtime.Cancel()
	}
}

func (m *RuntimeManager) SteerSession(sessionID string, message models.Message) bool {
	if runtime := m.existing(sessionID); runtime != nil {
		runtime.Steer(message)
		return true
	}
	return false
}

func (m *RuntimeManager) FollowupSession(sessionID string, session *Session, message models.Message) error {
	if session == nil || session.ID != sessionID {
		return errors.New("session does not match runtime")
	}
	runtime, err := m.For(sessionID)
	if err != nil {
		return err
	}
	runtime.Followup(session, message)
	return nil
}

func (m *RuntimeManager) CancelSession(sessionID string) bool {
	if runtime := m.existing(sessionID); runtime != nil {
		runtime.Cancel()
		return true
	}
	return false
}

func (m *RuntimeManager) ApproveSession(sessionID string) bool {
	return m.resolveSessionApproval(sessionID, func(runtime *Agent) bool { return runtime.ApprovePendingTool() })
}

func (m *RuntimeManager) ApproveSessionWithInput(sessionID string, input any) bool {
	return m.resolveSessionApproval(sessionID, func(runtime *Agent) bool { return runtime.ApprovePendingToolWithInput(input) })
}

func (m *RuntimeManager) DenySession(sessionID, reason string) bool {
	return m.resolveSessionApproval(sessionID, func(runtime *Agent) bool { return runtime.DenyPendingTool(reason) })
}

func (m *RuntimeManager) ResolveApprovalSession(sessionID, interactionID string, resolution tools.ApprovalResolution) error {
	runtime := m.existing(sessionID)
	if runtime == nil {
		return fmt.Errorf("session runtime is not active")
	}
	return runtime.ResolvePendingTool(interactionID, resolution)
}

func (m *RuntimeManager) resolveSessionApproval(sessionID string, resolve func(*Agent) bool) bool {
	runtime := m.existing(sessionID)
	return runtime != nil && runtime.hasPendingApproval() && resolve(runtime)
}

func (m *RuntimeManager) ApprovePendingTool() bool {
	return m.resolveOnlyPending(func(runtime *Agent) bool { return runtime.ApprovePendingTool() })
}

func (m *RuntimeManager) ApprovePendingToolWithInput(input any) bool {
	return m.resolveOnlyPending(func(runtime *Agent) bool { return runtime.ApprovePendingToolWithInput(input) })
}

func (m *RuntimeManager) DenyPendingTool(reason string) bool {
	return m.resolveOnlyPending(func(runtime *Agent) bool { return runtime.DenyPendingTool(reason) })
}

func (m *RuntimeManager) resolveOnlyPending(resolve func(*Agent) bool) bool {
	m.mu.Lock()
	pending := make([]*Agent, 0, 1)
	for _, runtime := range m.runtimes {
		if runtime.hasPendingApproval() {
			pending = append(pending, runtime)
		}
	}
	m.mu.Unlock()
	return len(pending) == 1 && resolve(pending[0])
}

func (m *RuntimeManager) existing(sessionID string) *Agent {
	if m == nil || sessionID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimes[sessionID]
}

func (m *RuntimeManager) SetProgress(report func(ProgressEvent)) {
	m.mu.Lock()
	m.progress = report
	m.mu.Unlock()
}

type ProgressSubscription struct {
	Events  <-chan ProgressEvent
	manager *RuntimeManager
	id      uint64
}

func (s *ProgressSubscription) Close() {
	if s == nil || s.manager == nil {
		return
	}
	s.manager.mu.Lock()
	if ch := s.manager.progressSubs[s.id]; ch != nil {
		delete(s.manager.progressSubs, s.id)
		close(ch)
	}
	s.manager.mu.Unlock()
}

func (m *RuntimeManager) SubscribeProgress(buffer int) *ProgressSubscription {
	if buffer <= 0 {
		buffer = 256
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextProgressSub++
	ch := make(chan ProgressEvent, buffer)
	m.progressSubs[m.nextProgressSub] = ch
	return &ProgressSubscription{Events: ch, manager: m, id: m.nextProgressSub}
}

func (m *RuntimeManager) emitProgress(event ProgressEvent) {
	m.mu.Lock()
	report := m.progress
	for _, listener := range m.progressSubs {
		select {
		case listener <- event:
		default:
		}
	}
	m.mu.Unlock()
	if report != nil {
		report(event)
	}
}

func routeProgress(sessionID string, report func(ProgressEvent)) func(ProgressEvent) {
	if report == nil {
		return nil
	}
	return func(event ProgressEvent) {
		if event.SessionID == "" {
			event.SessionID = sessionID
		}
		report(event)
	}
}

func (m *RuntimeManager) SetDebug(enabled bool) {
	m.mu.Lock()
	m.debug = enabled
	runtimes := m.snapshotLocked()
	m.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.SetDebug(enabled)
	}
}

func (m *RuntimeManager) Debug() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.debug
}

// Recent merges session-local tool activity into timestamp order.
func (m *RuntimeManager) Recent() []tools.Activity {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtimes := m.snapshotLocked()
	m.mu.Unlock()
	var activity []tools.Activity
	for _, runtime := range runtimes {
		if runtime.toolManager != nil {
			activity = append(activity, runtime.toolManager.Recent()...)
		}
	}
	sort.SliceStable(activity, func(i, j int) bool { return activity[i].Time.Before(activity[j].Time) })
	if len(activity) > 50 {
		activity = activity[len(activity)-50:]
	}
	return activity
}

// RecentSession returns activity only for an already-loaded session runtime.
// Reading frontend status must not create an otherwise idle runtime.
func (m *RuntimeManager) RecentSession(sessionID string) []tools.Activity {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	m.mu.Lock()
	runtime := m.runtimes[sessionID]
	m.mu.Unlock()
	if runtime == nil || runtime.toolManager == nil {
		return nil
	}
	return runtime.toolManager.Recent()
}

func (m *RuntimeManager) snapshotLocked() []*Agent {
	runtimes := make([]*Agent, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	return runtimes
}

// Close cancels every active runtime and waits for durable turn endings.
func (m *RuntimeManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
		for id, ch := range m.progressSubs {
			delete(m.progressSubs, id)
			close(ch)
		}
	}
	runtimes := m.snapshotLocked()
	m.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.Cancel()
	}

	done := make(chan struct{})
	go func() {
		m.active.Wait()
		for _, runtime := range runtimes {
			runtime.waitIdle()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return ErrRuntimeShutdownTimeout
	}
}
