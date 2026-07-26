package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/tools"
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
	cancel   func() bool
	resume   func() error
}

// SetCancel connects /cancel to the interactive shell's active task.
func (r *Registry) SetCancel(cancel func() bool) { r.cancel = cancel }

// SetResume connects /plan resume to the interactive shell.
func (r *Registry) SetResume(resume func() error) { r.resume = resume }

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
			if err := app.Agent.ClearMemory(ctx, session.ID); err != nil {
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
			if err := app.Agent.ClearMemory(ctx, session.ID); err != nil {
				return "", err
			}
			session.CurrentPlanID = ""
			session.PlanMode = false
			session.DryRun = false
			if err := session.Save("."); err != nil {
				return "", err
			}
			return "Agent state and conversation history reset.", nil
		},
	})

	// 4. /init
	r.Register(Command{
		Name:        "/init",
		Description: "Create local workspace memory",
		Execute: func(_ context.Context, _ *app.App, _ *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /init")
			}
			return agent.InitializeWorkspace(".")
		},
	})

	// 5. /krypton
	r.Register(Command{
		Name:        "/krypton",
		Description: "Remove Supremo state from this workspace (keeps global credentials)",
		Execute: func(_ context.Context, _ *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /krypton")
			}
			root, err := os.Getwd()
			if err != nil {
				return "", err
			}
			for _, name := range []string{".memory", ".session", ".scratchpad"} {
				if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
					return "", fmt.Errorf("remove %s: %w", name, err)
				}
			}
			if session != nil {
				session.CurrentPlanID = ""
				session.PlanMode = false
				session.DryRun = false
			}
			return "Supremo workspace state removed. Global configuration and credentials were kept.", nil
		},
	})

	// 6. /plan
	r.Register(Command{
		Name:        "/plan",
		Description: "Toggle plan mode; status, show, or resume an active plan",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) == 1 {
				plan, err := session.ActivePlan(".")
				if err != nil {
					return "", err
				}
				switch args[0] {
				case "status":
					if plan == nil {
						return fmt.Sprintf("Plan mode: %t\nNo active plan.", session.PlanMode), nil
					}
					return fmt.Sprintf("Plan mode: %t\n%s", session.PlanMode, plan.Context()), nil
				case "show":
					if plan == nil {
						return "No active plan.", nil
					}
					return plan.Context(), nil
				case "resume":
					if plan == nil {
						return "", fmt.Errorf("no active plan")
					}
					if r.resume == nil {
						return "", fmt.Errorf("plan resume is unavailable")
					}
					return "", r.resume()
				}
			}
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /plan [status|show|resume]")
			}
			session.PlanMode = !session.PlanMode
			if err := session.Save("."); err != nil {
				return "", err
			}
			if session.PlanMode {
				return "Plan mode enabled.", nil
			}
			return "Plan mode disabled.", nil
		},
	})

	// 7. /approve
	r.Register(Command{
		Name:        "/approve",
		Description: "Approve the pending mutating tool call",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /approve")
			}
			if app == nil || !app.Agent.ApprovePendingTool() {
				return "No tool call is awaiting approval.", nil
			}
			return "Tool call approved.", nil
		},
	})

	// 8. /deny
	r.Register(Command{
		Name:        "/deny",
		Description: "Deny the pending mutating tool call",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if app == nil || !app.Agent.DenyPendingTool(strings.Join(args, " ")) {
				return "No tool call is awaiting approval.", nil
			}
			return "Tool call denied.", nil
		},
	})

	// 9. /dry-run
	r.Register(Command{
		Name:        "/dry-run",
		Description: "Toggle dry run for mutating tool calls",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /dry-run")
			}
			session.DryRun = !session.DryRun
			if err := session.Save("."); err != nil {
				return "", err
			}
			return fmt.Sprintf("Dry run %s.", map[bool]string{true: "enabled", false: "disabled"}[session.DryRun]), nil
		},
	})

	// 10. /cancel
	r.Register(Command{
		Name:        "/cancel",
		Description: "Cancel the active task",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /cancel")
			}
			if r.cancel == nil || !r.cancel() {
				return "No active task.", nil
			}
			return "Cancellation requested.", nil
		},
	})

	// 11. /tools
	r.Register(Command{
		Name:        "/tools",
		Description: "List tools and their approval policy",
		Execute: func(_ context.Context, application *app.App, _ *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /tools")
			}
			if application == nil || application.Registry == nil {
				return "", fmt.Errorf("tool registry is unavailable")
			}
			registered := application.Registry.All()
			sort.Slice(registered, func(i, j int) bool { return registered[i].Name() < registered[j].Name() })
			var output strings.Builder
			output.WriteString("Tools:\n")
			for _, tool := range registered {
				policy := "read-only"
				if tools.RequiresApproval(tool.Name()) {
					policy = "approval required"
				}
				fmt.Fprintf(&output, "  %-20s %-18s %s\n", tool.Name(), policy, tool.Description())
			}
			return output.String(), nil
		},
	})

	// 12. /activity
	r.Register(Command{
		Name:        "/activity",
		Description: "Show recent local tool activity",
		Execute: func(_ context.Context, application *app.App, _ *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /activity")
			}
			if application == nil || application.ToolManager == nil {
				return "", fmt.Errorf("tool manager is unavailable")
			}
			activity := application.ToolManager.Recent()
			if len(activity) == 0 {
				return "No tool activity in this Supremo session.", nil
			}
			var output strings.Builder
			output.WriteString("Recent tool activity:\n")
			for _, entry := range activity {
				fmt.Fprintf(&output, "- %s %s: %s", entry.Time.Format("15:04:05"), entry.Tool, entry.Status)
				if entry.Message != "" {
					fmt.Fprintf(&output, " (%s)", entry.Message)
				}
				output.WriteByte('\n')
			}
			return output.String(), nil
		},
	})

	// 13. /doctor
	r.Register(Command{
		Name:        "/doctor",
		Description: "Check local setup without calling the provider",
		Execute: func(_ context.Context, application *app.App, _ *agent.Session, args []string) (string, error) {
			if len(args) != 0 {
				return "", fmt.Errorf("usage: /doctor")
			}
			return doctor(application)
		},
	})

	// 14. /exit
	r.Register(Command{
		Name:        "/exit",
		Description: "Exit the application",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			return "", ErrExit
		},
	})

	// 15. /auth
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

	// 16. /model
	r.Register(Command{
		Name:        "/model",
		Description: "Change model (e.g. gemini-2.5-flash)",
		Execute: func(ctx context.Context, app *app.App, session *agent.Session, args []string) (string, error) {
			if len(args) == 0 {
				return "", fmt.Errorf("usage: /model <model_name>")
			}
			if err := app.ProviderManager.UpdateModel(ctx, args[0]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Model updated to %s.", args[0]), nil
		},
	})

	// 17. /config
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
	names := []string{"/help", "/init", "/clear", "/reset", "/krypton", "/plan", "/approve", "/deny", "/dry-run", "/cancel", "/tools", "/activity", "/doctor", "/auth", "/model", "/config", "/exit"}
	var list []Command
	for _, name := range names {
		if cmd, ok := r.commands[name]; ok {
			list = append(list, cmd)
		}
	}
	return list
}

func doctor(application *app.App) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Supremo doctor\n- Workspace: %s\n", root)
	probe, err := os.CreateTemp(root, ".supremo-doctor-*")
	if err != nil {
		fmt.Fprintf(&output, "- Workspace write: failed (%v)\n", err)
	} else {
		name := probe.Name()
		err = probe.Close()
		if removeErr := os.Remove(name); err == nil {
			err = removeErr
		}
		if err != nil {
			fmt.Fprintf(&output, "- Workspace write: failed (%v)\n", err)
		} else {
			output.WriteString("- Workspace write: ok\n")
		}
	}
	for _, binary := range []string{"git", "go"} {
		if _, err := exec.LookPath(binary); err != nil {
			fmt.Fprintf(&output, "- %s: not found\n", binary)
		} else {
			fmt.Fprintf(&output, "- %s: found\n", binary)
		}
	}
	if application == nil || application.ProviderManager == nil || application.ProviderManager.GetRuntimeConfig() == nil {
		output.WriteString("- Provider: unavailable\n")
	} else {
		provider, model, _, apiKey, client := application.ProviderManager.GetRuntimeConfig().Get()
		if apiKey == "" || apiKey == "YOUR_GEMINI_API_KEY" || client == nil {
			fmt.Fprintf(&output, "- Provider: %s / %s needs an API key\n", provider, model)
		} else {
			fmt.Fprintf(&output, "- Provider: %s / %s configured\n", provider, model)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".githooks", "pre-commit")); err == nil {
		output.WriteString("- Pre-commit hook: present\n")
	} else {
		output.WriteString("- Pre-commit hook: missing (run: git config core.hooksPath .githooks)\n")
	}
	output.WriteString("- Provider network access: not checked\n")
	return output.String(), nil
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
