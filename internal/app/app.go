package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/file_system"
	"github.com/AbhaySingh002/supremo/internal/tools/git_tools"
	"github.com/AbhaySingh002/supremo/internal/tools/search"
	"github.com/AbhaySingh002/supremo/internal/tools/terminal"
	"github.com/AbhaySingh002/supremo/internal/tools/web_search"
)

// App is the composition root that holds the high-level initialized dependencies.
type App struct {
	Agent           *agent.Agent
	ProviderManager *providers.Manager
	Registry        *tools.Registry
	ToolManager     *tools.Manager
}

// New constructs the application, initializing and wiring together all subsystems.
func New() (*App, error) {
	ctx := context.Background()

	// 1. Load/Create configuration using Provider Manager
	fmt.Println("Initializing Provider...")
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find user home directory: %w", err)
	}
	configDir := filepath.Join(home, ".supremo")

	credStore := providers.NewFileCredentialStore(configDir)
	providerManager := providers.NewManager(configDir, credStore)

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
		&search.SearchFileName{},
		&search.SearchText{},
		&web_search.WebFetch{},
	}

	for _, t := range allTools {
		if err := registry.Register(t); err != nil {
			return nil, fmt.Errorf("failed to register tool %s: %w", t.Name(), err)
		}
	}

	// 4. Create Tool Manager
	toolManager := tools.NewManager(registry)

	// 5. Initialize Parser
	defaultParser := parser.NewParser()

	// 6. Initialize Memory
	memory := agent.NewInMemoryMemory()

	// 7. Load the fixed prompt templates once.
	fmt.Println("Initializing Prompt System...")
	contextBuilder, err := agent.NewRealContextBuilder(registry, memory, providerManager.ContextLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to load prompts: %w", err)
	}

	// 8. Get the active provider & construct the Agent.
	fmt.Println("Initializing Agent...")
	appAgent := agent.NewAgent(
		providerManager,
		toolManager,
		defaultParser,
		contextBuilder,
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
