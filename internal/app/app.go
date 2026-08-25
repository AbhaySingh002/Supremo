package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/backend"
	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	interactionbroker "github.com/AbhaySingh002/supremo/internal/interaction"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// AgentAPI preserves the legacy one-shot CLI and focused agent tests. Stateful
// frontends use api.Client through App.Backend instead.
type AgentAPI interface {
	Run(context.Context, *agent.Session, string) (string, error)
	AnswerSideQuestion(context.Context, string, string) (string, error)
	SetProgress(func(agent.ProgressEvent))
	ApprovePendingTool() bool
	ApprovePendingToolWithInput(any) bool
	DenyPendingTool(string) bool
	SetPlanMode(context.Context, *agent.Session, bool) error
	ClearMemory(context.Context, string) error
	ReadAllTranscript(context.Context, string) ([]models.Message, error)
	Checkpoints(string, string) ([]tools.CheckpointSummary, error)
	Rewind(context.Context, string, string, string, bool) (tools.RewindResult, error)
	DeleteSession(context.Context, string, string) error
	SetDebug(bool)
	Debug() bool
}

type ToolActivityAPI interface {
	Recent() []tools.Activity
}

var _ AgentAPI = (*agent.Agent)(nil)
var _ AgentAPI = (*agent.RuntimeManager)(nil)

// App is the composition root that holds the high-level initialized dependencies.
type App struct {
	Agent           AgentAPI
	Runtimes        *agent.RuntimeManager
	Subagents       *agent.SubagentManager
	ProviderManager *providers.Manager
	Registry        *tools.Registry
	ToolManager     ToolActivityAPI
	QuestionService *questions.Service
	State           *state.Store
	Repository      *repository.Service
	Context         *contextcompiler.Compiler
	Backend         *backend.Service
	Workspace       string
}

// New constructs the application, initializing and wiring together all subsystems.
func New() (*App, error) {
	return NewWithRuntimeOverrides(providers.RuntimeOverrides{})
}

// NewWithRuntimeOverrides constructs the application with process-local provider settings.
func NewWithRuntimeOverrides(overrides providers.RuntimeOverrides) (*App, error) {
	ctx := context.Background()
	workspace, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("find workspace: %w", err)
	}
	store, err := state.OpenContext(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace state: %w", err)
	}

	// 1. Load/Create configuration using Provider Manager.
	home, _ := os.UserHomeDir()
	configDir := state.DataDir()
	if home != "" {
		if _, err := os.Stat(filepath.Join(configDir, "credentials.json")); errors.Is(err, os.ErrNotExist) {
			legacyConfigDir := filepath.Join(home, ".supremo")
			if _, legErr := os.Stat(filepath.Join(legacyConfigDir, "credentials.json")); legErr == nil {
				configDir = legacyConfigDir
			}
		}
	}

	credStore := providers.NewFileCredentialStore(configDir)
	providerManager, err := buildProviderManager(ctx, configDir, credStore, overrides)
	if err != nil {
		return nil, fmt.Errorf("initialize provider manager: %w", err)
	}
	embeddings, err := providerManager.EmbeddingSettings()
	if err != nil {
		return nil, fmt.Errorf("load embedding settings: %w", err)
	}
	var embeddingProvider repository.EmbeddingProvider
	if embeddings.Endpoint != "" && embeddings.Model != "" && embeddings.APIKey != "" {
		embeddingProvider = repository.OpenAICompatibleEmbeddings{Endpoint: embeddings.Endpoint, ModelName: embeddings.Model, APIKey: embeddings.APIKey}
	}
	index, err := repository.New(workspace, store, embeddingProvider)
	if err != nil {
		return nil, fmt.Errorf("open repository index: %w", err)
	}
	index.Start()

	// 2. Create Tool Registry and Question Service.
	registry := tools.NewRegistry()
	if err := registerBuiltinTools(registry); err != nil {
		return nil, err
	}

	questionService := questions.NewService(nil)
	interactionBroker := interactionbroker.NewBroker(store)
	if err := registerPlanAndInteractionTools(registry, questionService); err != nil {
		return nil, err
	}

	// 5. Initialize the durable transcript.
	transcript, err := agent.NewDurableMemory(workspace)
	if err != nil {
		return nil, fmt.Errorf("open durable transcript: %w", err)
	}

	// 6. Load the fixed prompt templates once.
	compiler := contextcompiler.New(store, index)
	contextBuilder, err := agent.NewRealContextBuilder(registry, compiler, providerManager.ContextLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to load prompts: %w", err)
	}

	// 7. Create one mutable runtime per session around shared workspace services.
	runtimes := agent.NewRuntimeManager(func(string) (*agent.Agent, error) {
		toolManager := tools.NewManager(registry)
		toolManager.SetApprovalRecorder(interactionBroker)
		appAgent := agent.NewAgent(
			providerManager,
			toolManager,
			contextBuilder,
			transcript,
			newRuntimeHooks(workspace, store),
		)
		appAgent.SetRepository(index)
		return appAgent, nil
	})
	subagents, err := agent.NewSubagentManager(workspace, store, runtimes)
	if err != nil {
		return nil, fmt.Errorf("initialize subagent manager: %w", err)
	}
	if err := registerSubagentTools(registry, subagents); err != nil {
		return nil, err
	}
	if err := subagents.Recover(ctx); err != nil {
		return nil, fmt.Errorf("recover subagents: %w", err)
	}
	backendService, err := backend.New(workspace, "dev", store, runtimes, subagents, providerManager, registry, index, compiler, questionService, interactionBroker)
	if err != nil {
		return nil, fmt.Errorf("initialize backend service: %w", err)
	}

	return &App{
		Agent:           runtimes,
		Runtimes:        runtimes,
		Subagents:       subagents,
		ProviderManager: providerManager,
		Registry:        registry,
		ToolManager:     runtimes,
		QuestionService: questionService,
		State:           store,
		Repository:      index,
		Context:         compiler,
		Backend:         backendService,
		Workspace:       workspace,
	}, nil
}

// Close drains session runtimes before releasing workspace state.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	var backendErr, subagentErr, runtimeErr, stateErr error
	if a.Backend != nil {
		backendErr = a.Backend.Close()
	}
	if a.Subagents != nil {
		subagentErr = a.Subagents.Close()
	}
	if a.Runtimes != nil {
		runtimeErr = a.Runtimes.Close()
	}
	if a.Workspace != "" {
		stateErr = state.CloseWorkspace(a.Workspace)
	}
	return errors.Join(backendErr, subagentErr, runtimeErr, stateErr)
}
