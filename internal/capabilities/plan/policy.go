package plan

import (
	"context"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// IsActiveFunc reports whether Plan Mode is active for a given session.
type IsActiveFunc func(ctx context.Context, sessionID string) bool

// Guard enforces Plan Mode runtime non-mutation safety using descriptor metadata.
type Guard struct {
	isActive IsActiveFunc
}

func NewGuard(isActive IsActiveFunc) *Guard {
	return &Guard{isActive: isActive}
}

func (g *Guard) BeforeTool(event runtime.BeforeToolEvent) (runtime.BeforeToolDecision, error) {
	if g == nil || g.isActive == nil {
		return runtime.BeforeToolDecision{}, nil
	}
	if !g.isActive(event.Context, event.SessionID) {
		return runtime.BeforeToolDecision{}, nil
	}

	if isPlanModeAllowed(event.Descriptor) {
		return runtime.BeforeToolDecision{}, nil
	}

	name := event.Descriptor.Name
	if name == "" {
		name = event.Call.Name
	}

	return runtime.BeforeToolDecision{
		Result: &tools.ToolResult{
			Status:    tools.ToolStatusDenied,
			Success:   false,
			Retryable: true,
			Message: fmt.Sprintf(
				"Tool %q is denied in Plan Mode. Plan Mode is strictly read-only: explore the repository using inspection tools, use 'ask_user_question' for user-owned decisions, or submit the final plan with 'exit_plan_mode'. Do not mutate the workspace during planning.",
				name,
			),
		},
	}, nil
}

func isPlanModeAllowed(desc tools.ToolDescriptor) bool {
	if desc.Name == "ask_user_question" || desc.Name == "exit_plan_mode" {
		return true
	}
	if desc.Access == tools.ToolAccessRead && desc.SideEffect != tools.ToolSideEffectWorkspace {
		return true
	}
	if desc.Inspection && desc.Access != tools.ToolAccessWrite && desc.Access != tools.ToolAccessDestructive {
		return true
	}
	return false
}
