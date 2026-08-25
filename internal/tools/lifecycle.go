package tools

import "context"

// Lifecycle is the durable-observability payload for one tool transition.
// The tools package stays storage-agnostic; the agent attaches a recorder at
// its task boundary.
type Lifecycle struct {
	Tool       string
	Status     string
	Input      any
	Result     *ToolResult
	Error      error
	Arguments  string
	RawOutput  []byte
	Access     ToolAccess
	SideEffect ToolSideEffect
	Family     string
	Checkpoint *CheckpointSummary
}

type LifecycleEnrichment struct {
	ArtifactID    string
	WorldRevision string
}

type LifecycleRecorder interface {
	RecordToolLifecycle(context.Context, Lifecycle) LifecycleEnrichment
}

type lifecycleRecorderKey struct{}

func WithLifecycleRecorder(ctx context.Context, recorder LifecycleRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, lifecycleRecorderKey{}, recorder)
}

func recordLifecycle(ctx context.Context, event Lifecycle) LifecycleEnrichment {
	if recorder, _ := ctx.Value(lifecycleRecorderKey{}).(LifecycleRecorder); recorder != nil {
		return recorder.RecordToolLifecycle(ctx, event)
	}
	return LifecycleEnrichment{}
}
