package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	interactionbroker "github.com/AbhaySingh002/supremo/internal/interaction"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// API remains as a compatibility alias for embedders; the contract is owned
// by internal/api so frontends never import the backend implementation.
type API = api.Client

var _ api.Client = (*Service)(nil)

type Service struct {
	workspace    string
	version      string
	store        *state.Store
	runtimes     *agent.RuntimeManager
	subagents    *agent.SubagentManager
	providers    *providers.Manager
	registry     *tools.Registry
	repository   *repository.Service
	compiler     *contextcompiler.Compiler
	questions    *questions.Service
	interactions *interactionbroker.Broker

	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	admission sync.Mutex
	workers   map[string]bool
	active    map[string]string
	started   bool
	closed    bool
	wg        sync.WaitGroup
}

func New(workspace, version string, store *state.Store, runtimes *agent.RuntimeManager, subagents *agent.SubagentManager, providerManager *providers.Manager, registry *tools.Registry, repositoryService *repository.Service, compiler *contextcompiler.Compiler, questionService *questions.Service, broker *interactionbroker.Broker) (*Service, error) {
	if workspace == "" || store == nil || runtimes == nil || subagents == nil || providerManager == nil || registry == nil || repositoryService == nil || compiler == nil || questionService == nil || broker == nil {
		return nil, errors.New("workspace, state, runtimes, subagents, providers, tools, repository, context, questions, and interactions are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{workspace: workspace, version: version, store: store, runtimes: runtimes, subagents: subagents, providers: providerManager, registry: registry, repository: repositoryService, compiler: compiler, questions: questionService, interactions: broker,
		ctx: ctx, cancel: cancel, workers: make(map[string]bool), active: make(map[string]string)}, nil
}

func (s *Service) SetVersion(version string) {
	if s != nil && version != "" {
		s.version = version
	}
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("backend is closed")
	}
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()
	s.questions.SetProvider(s.interactions)
	if err := s.recoverRuns(ctx); err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Service) ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("backend is closed")
	}
	if !s.started {
		return errors.New("backend is not started")
	}
	return nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("backend workers did not stop within five seconds")
	}
}

func apiError(code api.ErrorCode, message string, retryable bool) error {
	return &api.Error{Code: code, Message: message, Retryable: retryable}
}
