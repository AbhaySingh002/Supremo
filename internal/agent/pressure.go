package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/state"
)

// contextPressureManager manages the context pressure lifecycle: measurement,
// deterministic pruning, and LLM compaction. It is private to Agent so no
// runtime dependency can replace compaction orchestration.
type contextPressureManager interface {
	Measure(session *Session, prompt *models.Prompt, contextLimit int) Measurement
	ResolvePressure(ctx context.Context, store state.EventStore, session *Session, provider providers.Provider, prompt *models.Prompt, measurement Measurement) (PressureResult, error)
	RecoverOverflow(ctx context.Context, store state.EventStore, session *Session, provider providers.Provider, prompt *models.Prompt, contextLimit int) (PressureResult, error)
}

var ErrContextNotConverging = errors.New("context pressure is not converging")

type PressureResult struct {
	Changed           bool
	BeforeTokens      int
	AfterTokens       int
	SurfaceGeneration uint64
}

// RealContextPressureManager implements ContextPressureManager coordinating TokenMeter, ToolResultPruner, and CompactionEngine.
type RealContextPressureManager struct {
	meter      TokenMeter
	pruner     ToolResultPruner
	compaction CompactionEngine
}

// NewRealContextPressureManager constructs a new RealContextPressureManager.
func NewRealContextPressureManager(
	meter TokenMeter,
	pruner ToolResultPruner,
	compaction CompactionEngine,
) *RealContextPressureManager {
	if meter == nil {
		meter = NewDefaultTokenMeter()
	}
	if pruner == nil {
		pruner = NewDefaultToolResultPruner()
	}
	if compaction == nil {
		compaction = NewDefaultCompactionEngine()
	}
	return &RealContextPressureManager{
		meter:      meter,
		pruner:     pruner,
		compaction: compaction,
	}
}

func (m *RealContextPressureManager) Measure(session *Session, prompt *models.Prompt, contextLimit int) Measurement {
	return m.meter.Measure(session, prompt, contextLimit)
}

// BeforeStep is the direct pressure API retained for focused callers.
func (m *RealContextPressureManager) BeforeStep(
	ctx context.Context,
	store state.EventStore,
	session *Session,
	provider providers.Provider,
	prompt *models.Prompt,
	contextLimit int,
) (PressureResult, error) {
	measurement := m.Measure(session, prompt, contextLimit)
	if !measurement.IsPressured() {
		return PressureResult{BeforeTokens: measurement.TotalTokens, AfterTokens: measurement.TotalTokens, SurfaceGeneration: surfaceGeneration(session)}, nil
	}
	return m.ResolvePressure(ctx, store, session, provider, prompt, measurement)
}

func (m *RealContextPressureManager) ResolvePressure(
	ctx context.Context,
	store state.EventStore,
	session *Session,
	provider providers.Provider,
	prompt *models.Prompt,
	measurement Measurement,
) (PressureResult, error) {
	if session == nil {
		return PressureResult{}, nil
	}
	result := PressureResult{BeforeTokens: measurement.TotalTokens, AfterTokens: measurement.TotalTokens, SurfaceGeneration: surfaceGeneration(session)}
	beforeGeneration := result.SurfaceGeneration
	prunedCount, err := m.pruner.Prune(ctx, store, session)
	if err != nil {
		return result, err
	}
	if prunedCount == 0 {
		compacted, compactErr := m.compaction.Compact(ctx, store, session, provider, prompt, measurement)
		if compactErr != nil {
			return result, compactErr
		}
		if !compacted {
			return result, fmt.Errorf("%w: no safe surface reduction is available", ErrContextNotConverging)
		}
	}
	result.Changed = true
	result.SurfaceGeneration = surfaceGeneration(session)
	updatedPrompt := promptWithCurrentSurface(prompt, session)
	result.AfterTokens = m.meter.Measure(session, updatedPrompt, measurement.ContextLimit).TotalTokens
	if result.SurfaceGeneration <= beforeGeneration || result.AfterTokens >= result.BeforeTokens {
		return result, fmt.Errorf("%w: tokens %d -> %d, surface generation %d -> %d", ErrContextNotConverging, result.BeforeTokens, result.AfterTokens, beforeGeneration, result.SurfaceGeneration)
	}
	return result, nil
}

// RecoverOverflow forces one durable recovery pass after a provider overflow.
func (m *RealContextPressureManager) RecoverOverflow(
	ctx context.Context,
	store state.EventStore,
	session *Session,
	provider providers.Provider,
	prompt *models.Prompt,
	contextLimit int,
) (PressureResult, error) {
	measurement := m.Measure(session, prompt, contextLimit)
	return m.ResolvePressure(ctx, store, session, provider, prompt, measurement)
}

func promptWithCurrentSurface(prompt *models.Prompt, session *Session) *models.Prompt {
	if prompt == nil {
		return &models.Prompt{Messages: session.DeriveMessages()}
	}
	updated := *prompt
	updated.Messages = append([]models.Message(nil), session.DeriveMessages()...)
	return &updated
}

func surfaceGeneration(session *Session) uint64 {
	if session == nil {
		return 0
	}
	session.ensureSurface()
	return session.surface.Generation()
}
