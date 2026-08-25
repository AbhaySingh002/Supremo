package app

import (
	"context"
	"encoding/json"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/capabilities/observation"
	"github.com/AbhaySingh002/supremo/internal/capabilities/plan"
	"github.com/AbhaySingh002/supremo/internal/capabilities/repeat"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func newRuntimeHooks(workspace string, store *state.Store) *runtime.HookSet {
	hooks := runtime.NewHookSet()
	repeatCapability := repeat.New(repeat.Config{})
	hooks.AddAfterTool(repeatCapability)
	hooks.AddUserInput(repeatCapability)
	observationCapability := observation.New(workspace)
	hooks.AddBeforeTool(observationCapability)
	hooks.AddAfterTool(observationCapability)
	planGuard := plan.NewGuard(func(ctx context.Context, sessionID string) bool {
		if store == nil || sessionID == "" {
			return false
		}
		saved, err := store.Session(ctx, sessionID)
		if err != nil {
			return false
		}
		var s agent.Session
		if err := json.Unmarshal(saved.Data, &s); err != nil {
			return false
		}
		return s.PlanModeActive()
	})
	hooks.AddBeforeTool(planGuard)
	return hooks
}
