package questions

import (
	"context"
	"sync"
)

// Service provides deterministic access to the active registered human question provider.
type Service struct {
	mu       sync.RWMutex
	provider Provider
}

func NewService(provider Provider) *Service {
	return &Service{provider: provider}
}

func (s *Service) SetProvider(provider Provider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
}

func (s *Service) Provider() Provider {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provider
}

func (s *Service) Ask(ctx context.Context, req Request) (AnswerSet, error) {
	if s == nil {
		return AnswerSet{}, ErrNoQuestionProvider
	}
	s.mu.RLock()
	p := s.provider
	s.mu.RUnlock()
	if p == nil {
		return AnswerSet{}, ErrNoQuestionProvider
	}
	return p.Ask(ctx, req)
}
