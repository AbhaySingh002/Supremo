package observation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// Capability reuses and persists fingerprint observations for inspection tools.
type Capability struct {
	workspace string
}

func New(workspace string) *Capability {
	return &Capability{workspace: workspace}
}

func (c *Capability) BeforeTool(event runtime.BeforeToolEvent) (runtime.BeforeToolDecision, error) {
	if c == nil || !event.Descriptor.Inspection {
		return runtime.BeforeToolDecision{}, nil
	}
	root := tools.Workspace(event.Context)
	if root == "" {
		root = c.workspace
	}
	if root == "" || event.SessionID == "" {
		return runtime.BeforeToolDecision{}, nil
	}
	store, err := state.Open(root)
	if err != nil || store == nil {
		return runtime.BeforeToolDecision{}, nil
	}
	fp, cArgs, _, _ := state.ComputeCallFingerprint(event.Call.Name, event.Call.Arguments, root)
	existing, found, _ := store.ObservationByFingerprint(event.Context, event.SessionID, fp)
	if !found || !state.IsObservationValid(event.Context, existing, store, root) || existing.ArtifactID == "" {
		return runtime.BeforeToolDecision{}, nil
	}
	artifactBytes, err := store.ReadArtifact(event.Context, existing.ArtifactID)
	if err != nil || len(artifactBytes) == 0 {
		return runtime.BeforeToolDecision{}, nil
	}
	var parsedData map[string]any
	if json.Unmarshal(artifactBytes, &parsedData) != nil {
		return runtime.BeforeToolDecision{}, nil
	}
	parsedData["reused"] = true
	parsedData["observation_id"] = existing.ID
	logging.Info("Tool execution reused from durable memory: %s (id=%s, obs=%s)", event.Call.Name, event.Call.ID, existing.ID)
	_ = cArgs
	return runtime.BeforeToolDecision{
		Reused: true,
		Result: &tools.ToolResult{
			Success:    true,
			Status:     tools.ToolStatusCompleted,
			ArtifactID: existing.ArtifactID,
			Data:       parsedData,
			Preview:    string(artifactBytes),
			Message:    fmt.Sprintf("Unchanged observation reused from durable memory (id=%s)", existing.ID),
		},
	}, nil
}

func (c *Capability) AfterTool(event runtime.AfterToolEvent) (runtime.AfterToolDecision, error) {
	if c == nil || event.Reused || !event.Descriptor.PersistCallObservation || event.Result == nil || !event.Result.Success {
		return runtime.AfterToolDecision{}, nil
	}
	root := tools.Workspace(event.Context)
	if root == "" {
		root = c.workspace
	}
	if root == "" || event.SessionID == "" {
		return runtime.AfterToolDecision{}, nil
	}
	store, err := state.Open(root)
	if err != nil || store == nil {
		return runtime.AfterToolDecision{}, nil
	}
	fp, cArgs, path, scope := state.ComputeCallFingerprint(event.Call.Name, event.Call.Arguments, root)
	raw := ""
	if event.Result.Preview != "" {
		raw = event.Result.Preview
	}
	artifactID := event.Result.ArtifactID
	if artifactID == "" {
		rawBytes := []byte(raw)
		if len(rawBytes) == 0 && event.Result.Data != nil {
			rawBytes, _ = json.Marshal(event.Result.Data)
			raw = string(rawBytes)
		}
		if len(rawBytes) > 0 {
			if artifact, err := store.PutArtifact(event.Context, state.ArtifactInput{Data: rawBytes, ContentType: "application/json", Origin: "tool:" + event.Call.Name}); err == nil {
				artifactID = artifact.Hash
			}
		}
	}
	if artifactID == "" {
		return runtime.AfterToolDecision{}, nil
	}
	sum, neg, sHash := state.ExtractObservationSummary(event.Call.Name, path, event.Result.Data, event.Result.Success, event.Result.Message, raw, root)
	_, _ = store.SaveObservation(event.Context, state.Observation{
		SessionID:       event.SessionID,
		TaskID:          event.TaskID,
		Tool:            event.Call.Name,
		CallFingerprint: fp,
		CanonicalArgs:   cArgs,
		Scope:           scope,
		Path:            path,
		Summary:         sum,
		ArtifactID:      artifactID,
		SourceHash:      sHash,
		Negative:        neg,
		CreatedAt:       time.Now().UTC(),
	})
	return runtime.AfterToolDecision{}, nil
}
