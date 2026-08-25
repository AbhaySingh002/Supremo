package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// RealContextBuilder is the production Agent context pipeline backed by Compiler V2.
type RealContextBuilder struct {
	project      string
	registry     *tools.Registry
	contextLimit func() int
	compiler     *contextcompiler.Compiler
}

// NewRealContextBuilder creates the only workspace context pipeline.
func NewRealContextBuilder(registry *tools.Registry, compiler *contextcompiler.Compiler, contextLimit func() int) (*RealContextBuilder, error) {
	if compiler == nil {
		return nil, fmt.Errorf("context compiler is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("tool registry is required")
	}
	workspace, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &RealContextBuilder{project: loadProjectInstructions(workspace), registry: registry, contextLimit: contextLimit, compiler: compiler}, nil
}

func (cb *RealContextBuilder) Compile(ctx context.Context, request ContextRequest) (*models.Prompt, error) {
	prepared, err := cb.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := prepared.Commit(ctx); err != nil {
		return nil, err
	}
	return prepared.Prompt, nil
}

func (cb *RealContextBuilder) Prepare(ctx context.Context, request ContextRequest) (*PreparedContext, error) {
	if request.Session == nil {
		return nil, fmt.Errorf("context session is required")
	}
	compiled, err := prompts.Compile(request.Profile)
	if err != nil {
		return nil, err
	}
	limit := 0
	if cb.contextLimit != nil {
		limit = cb.contextLimit()
	}
	catalog, err := cb.registry.Catalog()
	if err != nil {
		return nil, err
	}
	history := []models.Message{}
	if request.Session != nil {
		history = request.Session.DeriveMessages()
	}
	mode := contextToolMode(request)
	researchOnly := tools.IsResearchOnly(ctx)
	readOnly := tools.IsReadOnly(ctx) || researchOnly || mode == tools.ToolModeAudit || mode == tools.ToolModeSide
	taskID := request.TaskID
	if taskID == "" {
		taskID = request.Session.ActiveTaskID
	}
	prepared, err := cb.compiler.Prepare(ctx, contextcompiler.Request{
		SessionID: request.Session.ID, TaskID: taskID, Turn: request.Turn, Step: request.Step, Objective: request.Objective,
		OverflowPressure: request.OverflowPressure, Provider: request.Session.Provider, Model: request.Session.Model,
		ContextLimit: limit, Control: compiled.Control, PromptMetadata: compiled.Metadata, ProjectInstructions: cb.project, ToolCatalog: catalog, ToolMode: mode, ToolReadOnly: readOnly, ToolResearchOnly: researchOnly, RequiredCapabilities: request.RequiredCapabilities, ToolApprovalMode: request.Session.ApprovalMode, ToolDryRun: request.Session.DryRun, History: history,
	})
	if err != nil {
		return nil, err
	}
	prompt := prepared.Prompt
	if request.Profile == protocol.SideAnswer {
		prompt.ActiveTools = nil
		prompt.ToolDefinitions = nil
		prompt.Metadata.SelectedTools = nil
	}
	if request.Session != nil && request.Session.PlanModeActive() {
		prompt.System = strings.TrimSpace(prompt.System + "\n\n# Plan Mode Policy\n" + prompts.PlanModePolicy)
	}
	return &PreparedContext{Prompt: prompt, commit: func(ctx context.Context) error { return cb.compiler.Commit(ctx, prepared) }}, nil
}

func (cb *RealContextBuilder) RecordObjective(ctx context.Context, sessionID, taskID, objective string) error {
	return cb.compiler.RecordObjective(ctx, sessionID, taskID, objective)
}

func (cb *RealContextBuilder) RecordUsage(ctx context.Context, prompt *models.Prompt, usage providers.Usage) error {
	if prompt == nil {
		return nil
	}
	return cb.compiler.RecordUsage(ctx, prompt.ManifestID, usage.InputTokens, usage.OutputTokens)
}

func (cb *RealContextBuilder) ObserveTool(ctx context.Context, sessionID, taskID string, observation ToolObservation) error {
	return cb.compiler.ObserveTool(ctx, sessionID, taskID, contextcompiler.ToolObservation{Name: observation.Name, Status: observation.Status, Success: observation.Success, ArtifactID: observation.ArtifactID})
}

func loadProjectInstructions(workspace string) string {
	for _, name := range []string{"SUPREMO.md", "AGENTS.md"} {
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if err == nil {
			return string(content)
		}
	}
	return ""
}

func contextToolMode(request ContextRequest) tools.ToolMode {
	if request.Session != nil && request.Session.PlanModeActive() {
		return tools.ToolModePlanning
	}
	if request.Mode != "" {
		return request.Mode
	}
	if request.Profile == protocol.Execution {
		return tools.ToolModeExecution
	}
	return tools.ToolModeNormal
}
