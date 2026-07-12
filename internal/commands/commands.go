package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
)

// ErrExit signals the application shell loop to terminate.
var ErrExit = errors.New("exit requested")

// Command represents an interactive user command.
type Command struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error)
}

// Registry holds the list of registered commands.
type Registry struct {
	commands map[string]Command
}

// NewRegistry constructs a Registry and registers all standard commands.
func NewRegistry() *Registry {
	r := &Registry{
		commands: make(map[string]Command),
	}

	// 1. /help
	r.Register(Command{
		Name:        "/help",
		Description: "Show available commands",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			var sb strings.Builder
			sb.WriteString("Available Commands:\n")
			for _, cmd := range r.List() {
				sb.WriteString(fmt.Sprintf("  %-8s %s\n", cmd.Name, cmd.Description))
			}
			return sb.String(), nil
		},
	})

	// 2. /clear
	r.Register(Command{
		Name:        "/clear",
		Description: "Clear current conversation",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if err := app.Agent.Memory().Clear(ctx, session.ID); err != nil {
				return "", err
			}
			return "Conversation cleared.", nil
		},
	})

	// 3. /reset
	r.Register(Command{
		Name:        "/reset",
		Description: "Reset agent state",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if err := app.Agent.Memory().Clear(ctx, session.ID); err != nil {
				return "", err
			}
			session.OpenFiles = nil
			session.CurrentPlanID = ""
			session.Metadata = make(map[string]interface{})
			return "Agent state and conversation history reset.", nil
		},
	})

	// 4. /exit
	r.Register(Command{
		Name:        "/exit",
		Description: "Exit the application",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			return "", ErrExit
		},
	})

	// 5. /auth
	r.Register(Command{
		Name:        "/auth",
		Description: "Update API key",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) == 0 {
				return "", fmt.Errorf("usage: /auth <api_key>")
			}
			apiKey := args[0]
			if err := app.ProviderManager.UpdateAPIKey(ctx, apiKey); err != nil {
				return "", err
			}
			return "API key updated successfully.", nil
		},
	})

	// 6. /config
	r.Register(Command{
		Name:        "/config",
		Description: "View or reload configuration",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) > 0 && args[0] == "reload" {
				if err := app.ProviderManager.Initialize(ctx); err != nil {
					return "", err
				}
				providerName, model, endpoint, _, _ := app.ProviderManager.GetRuntimeConfig().Get()
				return fmt.Sprintf("Configuration reloaded successfully.\nActive Configuration:\n  Provider: %s\n  Model:    %s\n  Endpoint: %s", providerName, model, endpoint), nil
			}
			providerName, model, endpoint, _, _ := app.ProviderManager.GetRuntimeConfig().Get()
			return fmt.Sprintf("Active Configuration:\n  Provider: %s\n  Model:    %s\n  Endpoint: %s", providerName, model, endpoint), nil
		},
	})

	return r
}

// Register adds a command to the registry.
func (r *Registry) Register(cmd Command) {
	r.commands[cmd.Name] = cmd
}

// List returns all registered commands sorted by name.
func (r *Registry) List() []Command {
	names := []string{"/help", "/clear", "/reset", "/auth", "/config", "/exit"}
	var list []Command
	for _, name := range names {
		if cmd, ok := r.commands[name]; ok {
			list = append(list, cmd)
		}
	}
	return list
}

// Handle processes the user input. If it is a command (starts with '/'), it executes it
// and returns (output, handled, error).
func (r *Registry) Handle(ctx context.Context, app *app.App, session *agent.Session, input string) (string, bool, error) {
	if !strings.HasPrefix(input, "/") {
		return "", false, nil
	}

	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "", false, nil
	}

	cmdName := parts[0]
	cmd, exists := r.commands[cmdName]
	if !exists {
		return fmt.Sprintf("Unknown command: %s. Type /help to see all available commands.", cmdName), true, nil
	}

	out, err := cmd.Execute(ctx, app, session, parts[1:])
	return out, true, err
}
