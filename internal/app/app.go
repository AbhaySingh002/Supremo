package app

import (
	"context"
	"fmt"
	"os"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/file_system"
	"github.com/AbhaySingh002/supremo/internal/tools/git_tools"
	"github.com/AbhaySingh002/supremo/internal/tools/search"
	"github.com/AbhaySingh002/supremo/internal/tools/terminal"
)

// App is the composition root that holds the high-level initialized dependencies.
type App struct {
	Agent           *agent.Agent
	ProviderManager *providers.Manager
	Registry        *tools.Registry
	ToolManager     *tools.Manager
}

// RuntimeManager is a simple implementation of agent.RuntimeManager.
type RuntimeManager struct{}

// GetWorkingDirectory implements agent.RuntimeManager
func (r *RuntimeManager) GetWorkingDirectory() string {
	dir, _ := os.Getwd()
	return dir
}

// New constructs the application, initializing and wiring together all subsystems.
func New() (*App, error) {
	ctx := context.Background()

	// 1. Load/Create configuration using Provider Manager
	fmt.Println("Initializing Provider...")
	configDir := ".supremo"

	credStore := providers.NewFileCredentialStore(configDir)
	factory := providers.NewFactory()
	providerManager := providers.NewManager(configDir, credStore, factory)

	if err := providerManager.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize provider manager: %w", err)
	}

	// 2. Create Tool Registry
	fmt.Println("Initializing Tools...")
	registry := tools.NewRegistry()

	// 3. Register every existing tool
	allTools := []tools.Tool{
		&file_system.CreateDirectory{},
		&file_system.CreateFile{},
		&file_system.DeleteFile{},
		&file_system.FileInfo{},
		&file_system.ListDirectory{},
		&file_system.ReadFile{},
		&file_system.RenameFile{},
		&file_system.WriteFile{},
		&terminal.ExecuteCommand{},
		&terminal.RunBuild{},
		&terminal.RunFormatter{},
		&terminal.RunTests{},
		&git_tools.GitDiff{},
		&git_tools.GitLog{},
		&git_tools.GitStatus{},
		&search.FindReferences{},
		&search.FindSymbol{},
		&search.ListOpenFiles{},
		&search.SearchFileName{},
		&search.SearchText{},
	}

	for _, t := range allTools {
		if err := registry.Register(t); err != nil {
			return nil, fmt.Errorf("failed to register tool %s: %w", t.Name(), err)
		}
	}

	// 4. Create Tool Manager
	toolManager := tools.NewManager(registry)

	// 5. Initialize Prompt System
	fmt.Println("Initializing Prompt System...")
	promptLoader := prompts.NewLoader("internal/prompts/templates")
	promptRegistry := prompts.NewRegistry()
	promptBuilder := prompts.NewBuilder(promptLoader, promptRegistry, registry)

	// 6. Initialize Parser
	defaultParser := parser.NewParser()

	// 7. Initialize Memory
	memory := agent.NewInMemoryMemory()

	// 8. Initialize Context Builder with real implementation
	runtimeManager := &RuntimeManager{}
	contextBuilder := agent.NewRealContextBuilder(promptBuilder, runtimeManager, memory)

	// 9. Get the active provider & Construct Agent using dependency injection.
	fmt.Println("Initializing Agent...")
	appAgent := agent.NewAgent(
		providerManager, // Manager implements agent.Provider interface via Chat()
		toolManager,
		defaultParser,
		nil, // PromptBuilder not needed with RealContextBuilder
		contextBuilder,
		runtimeManager,
		memory,
	)

	fmt.Println("Ready.")

	return &App{
		Agent:           appAgent,
		ProviderManager: providerManager,
		Registry:        registry,
		ToolManager:     toolManager,
	}, nil
}
