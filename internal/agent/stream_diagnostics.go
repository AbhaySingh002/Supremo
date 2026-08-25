package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

type sessionEventAppender interface {
	AppendSessionEvent(context.Context, string, SessionEvent) (SessionEvent, error)
}

// streamDiagnosticRecorder is the agent-owned bridge between canonical stream
// events, durable diagnostics, UI text, and the one completion assembler.
type streamDiagnosticRecorder struct {
	agent     *Agent
	ctx       context.Context
	session   *Session
	prompt    *models.Prompt
	turn      int
	step      int
	attempt   int
	assembler *providers.AssistantAssembler
	sources   []int64
	finishSet bool
	errorSet  bool
}

func newStreamDiagnosticRecorder(a *Agent, ctx context.Context, session *Session, prompt *models.Prompt, attempt int) *streamDiagnosticRecorder {
	turn, step := 0, 0
	if a != nil {
		a.mu.Lock()
		turn, step = a.phase.Turn, a.phase.Step
		a.mu.Unlock()
	}
	activeTools := []string(nil)
	if prompt != nil {
		activeTools = prompt.ActiveTools
	}
	return &streamDiagnosticRecorder{
		agent: a, ctx: ctx, session: session, prompt: prompt, turn: turn, step: step, attempt: attempt,
		assembler: providers.NewAssistantAssembler(activeTools, func(delta string) {
			if a != nil {
				a.emit(ProgressEvent{Kind: ProgressStream, Message: delta})
			}
		}),
	}
}

func (r *streamDiagnosticRecorder) Begin() error {
	if r.session == nil || r.prompt == nil {
		return nil
	}
	if err := r.persistHeader(); err != nil {
		return err
	}
	payload := sessionlog.RequestContextPayload{
		Turn: r.turn, Step: r.step, Attempt: r.attempt,
		Provider: r.session.Provider, Model: r.session.Model, Profile: r.prompt.Metadata.Profile,
		MessageEventSeqs: r.session.Nodes(), SystemDigest: r.prompt.SystemDigest,
		ToolSchemaDigest: r.prompt.ToolSchemaDigest, RequestDigest: r.prompt.RequestDigest, HeaderDigest: r.prompt.HeaderDigest,
		SelectedToolNames: append([]string(nil), r.prompt.ActiveTools...),
	}
	if r.agent != nil && !r.agent.ephemeral && r.agent.workspace != "" {
		store, err := state.Open(r.agent.workspace)
		if err != nil {
			return err
		}
		artifactData := append([]byte(nil), r.prompt.FrozenEnvelope...)
		artifact, err := store.PutArtifact(r.ctx, state.ArtifactInput{Data: artifactData, ContentType: "application/json", Origin: "compiled-prompt"})
		if err != nil {
			return fmt.Errorf("store compiled prompt artifact: %w", err)
		}
		payload.PromptArtifactID = artifact.Hash
	}
	event, err := sessionlog.New(sessionlog.EventRequestContext, payload)
	if err != nil {
		return err
	}
	_, _, err = r.append(event)
	return err
}

func (r *streamDiagnosticRecorder) persistHeader() error {
	if r.agent == nil || r.session == nil || r.prompt == nil {
		return nil
	}
	reason, emit := r.agent.requestHeaderReason(r.session, r.prompt.HeaderDigest)
	if !emit {
		return nil
	}
	event, err := sessionlog.New(sessionlog.EventRequestHeader, sessionlog.RequestHeaderPayload{
		Turn: r.turn, Step: r.step, Provider: r.session.Provider, Model: r.session.Model, Profile: r.prompt.Metadata.Profile,
		HeaderDigest: r.prompt.HeaderDigest, SystemDigest: r.prompt.SystemDigest, ToolSchemaDigest: r.prompt.ToolSchemaDigest, Reason: reason,
	})
	if err != nil {
		return err
	}
	if _, _, err := r.append(event); err != nil {
		return err
	}
	r.agent.markRequestHeader(r.prompt.HeaderDigest)
	return nil
}

func (a *Agent) requestHeaderReason(session *Session, headerDigest string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.requestHeaderSeen {
		return "change", a.requestHeaderDigest != headerDigest
	}
	reason := "initial"
	if session != nil {
		for _, event := range session.events {
			if event.Type == EventRequestHeader || event.Type == EventRequestContext {
				reason = "resume"
				break
			}
		}
	}
	return reason, true
}

func (a *Agent) markRequestHeader(headerDigest string) {
	a.mu.Lock()
	a.requestHeaderSeen, a.requestHeaderDigest = true, headerDigest
	a.mu.Unlock()
}

func (r *streamDiagnosticRecorder) Receive(event providers.StreamEvent) error {
	if err := r.persist(event); err != nil {
		return err
	}
	return r.assembler.Feed(event)
}

func (r *streamDiagnosticRecorder) persist(event providers.StreamEvent) error {
	if r.session == nil {
		return nil
	}
	switch event.Type {
	case providers.StreamEventTextDelta, providers.StreamEventToolCallDelta:
		record, err := sessionlog.New(sessionlog.EventAssistantChunk, sessionlog.AssistantChunkPayload{
			Turn: r.turn, Step: r.step, Attempt: r.attempt, Event: event,
		})
		if err != nil {
			return err
		}
		stored, persisted, err := r.append(record)
		if err != nil {
			return err
		}
		if persisted {
			r.sources = append(r.sources, stored.Seq)
		}
	case providers.StreamEventUsage:
		if event.Usage == nil {
			return nil
		}
		record, err := sessionlog.New(sessionlog.EventUsage, sessionlog.UsagePayload{
			Turn: r.turn, Step: r.step, Attempt: r.attempt, Usage: *event.Usage,
		})
		if err != nil {
			return err
		}
		_, _, err = r.append(record)
		return err
	case providers.StreamEventFinish:
		record, err := sessionlog.New(sessionlog.EventFinish, sessionlog.FinishPayload{
			Turn: r.turn, Step: r.step, Attempt: r.attempt, FinishReason: string(event.FinishReason),
		})
		if err != nil {
			return err
		}
		_, _, err = r.append(record)
		r.finishSet = err == nil
		return err
	case providers.StreamEventError:
		return r.RecordError(event.Err)
	case providers.StreamEventReasoningDelta:
		// Reasoning remains available to assembly but intentionally is not durable.
		return nil
	}
	return nil
}

func (r *streamDiagnosticRecorder) RecordError(err error) error {
	if r.session == nil || err == nil || r.errorSet {
		return nil
	}
	record, buildErr := sessionlog.New(sessionlog.EventError, sessionlog.ErrorPayload{
		Turn: r.turn, Step: r.step, Attempt: r.attempt, Code: providerErrorCode(err), Message: err.Error(),
	})
	if buildErr != nil {
		return buildErr
	}
	_, _, appendErr := r.append(record)
	if appendErr == nil {
		r.errorSet = true
	}
	return appendErr
}

func (r *streamDiagnosticRecorder) RecordRetry(delayMillis int64, err error) error {
	if r.session == nil {
		return nil
	}
	record, buildErr := sessionlog.New(sessionlog.EventRetry, sessionlog.RetryPayload{
		Turn: r.turn, Step: r.step, Attempt: r.attempt, DelayMillis: delayMillis, Code: providerErrorCode(err),
	})
	if buildErr != nil {
		return buildErr
	}
	_, _, appendErr := r.append(record)
	return appendErr
}

func (r *streamDiagnosticRecorder) Assemble() (*providers.Completion, error) {
	completion, err := r.assembler.Assemble()
	if err != nil {
		return nil, err
	}
	if !r.finishSet {
		if err := r.persist(providers.StreamEvent{Type: providers.StreamEventFinish, FinishReason: providers.NormalizeFinishReason(completion.FinishReason)}); err != nil {
			return nil, err
		}
	}
	completion.SourceEventSeqs = append([]int64(nil), r.sources...)
	return completion, nil
}

func (r *streamDiagnosticRecorder) append(event SessionEvent) (SessionEvent, bool, error) {
	if r.agent == nil || r.agent.ephemeral || r.agent.transcript == nil {
		return event, false, nil
	}
	if appender, ok := r.agent.transcript.(sessionEventAppender); ok {
		stored, err := appender.AppendSessionEvent(r.ctx, r.session.ID, event)
		return stored, err == nil, err
	}
	if err := r.agent.transcript.AppendHarnessEvent(r.ctx, r.session.ID, event.Type, event.Data); err != nil {
		return event, false, err
	}
	return event, false, nil
}

func providerErrorCode(err error) string {
	var failure *providers.ProviderFailure
	if errors.As(err, &failure) && failure != nil && failure.Code != "" {
		return string(failure.Code)
	}
	return "PROVIDER_ERROR"
}
