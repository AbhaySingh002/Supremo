package agent

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/capabilities/observation"
	"github.com/AbhaySingh002/supremo/internal/capabilities/repeat"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// newTestAgent keeps fake context pipelines out of the production constructor.
func newTestAgent(provider providers.Provider, toolManager *tools.Manager, lifecycle contextLifecycle, transcript TranscriptStore, hooks *runtime.HookSet) *Agent {
	agent := NewAgent(provider, toolManager, nil, transcript, hooks)
	agent.contextLifecycle = lifecycle
	return agent
}

func observationHooks(workspace string) *runtime.HookSet {
	hooks := runtime.NewHookSet()
	capability := observation.New(workspace)
	hooks.AddBeforeTool(capability)
	hooks.AddAfterTool(capability)
	return hooks
}

func repeatHooks(cfg repeat.Config) (*runtime.HookSet, *repeat.Guard) {
	hooks := runtime.NewHookSet()
	guard := repeat.New(cfg)
	hooks.AddAfterTool(guard)
	hooks.AddUserInput(guard)
	return hooks, guard
}

type mockSequenceProvider struct {
	stepFunc func(*models.Prompt) (*providers.Completion, error)
}

func (p *mockSequenceProvider) Chat(_ context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	return p.stepFunc(prompt)
}
