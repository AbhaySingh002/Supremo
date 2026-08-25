// Package commands owns the stable slash-command vocabulary. It deliberately
// parses only: frontends decide whether an intent is local or sent to the
// transport-neutral backend API.
package commands

import (
	"fmt"
	"sort"
	"strings"
)

type Kind string

const (
	Help                Kind = "help"
	Clear               Kind = "clear"
	Reset               Kind = "reset"
	InitializeWorkspace Kind = "initialize_workspace"
	Krypton             Kind = "krypton"
	Session             Kind = "session"
	NewSession          Kind = "new_session"
	DeleteSession       Kind = "delete_session"
	RenameSession       Kind = "rename_session"
	Plan                Kind = "plan"
	Tasks               Kind = "tasks"
	UX                  Kind = "ux"
	SideQuestion        Kind = "side_question"
	Rewind              Kind = "rewind"
	Approve             Kind = "approve"
	Deny                Kind = "deny"
	DryRun              Kind = "dry_run"
	ApprovalMode        Kind = "approval_mode"
	Copy                Kind = "copy"
	Export              Kind = "export"
	Diff                Kind = "diff"
	Cancel              Kind = "cancel"
	Tools               Kind = "tools"
	Activity            Kind = "activity"
	Doctor              Kind = "doctor"
	Exit                Kind = "exit"
	Auth                Kind = "auth"
	Provider            Kind = "provider"
	Providers           Kind = "providers"
	Endpoint            Kind = "endpoint"
	Models              Kind = "models"
	Usage               Kind = "usage"
	Model               Kind = "model"
	Config              Kind = "config"
	Context             Kind = "context"
	Index               Kind = "index"
)

// Intent is a validated command request. Value carries a canonical alias
// value such as an approval mode; Args retains user-authored arguments.
type Intent struct {
	Kind    Kind
	Command string
	Value   string
	Args    []string
}

type Command struct {
	Name        string
	Description string
	kind        Kind
	value       string
	validate    func([]string) error
}

type Registry struct{ commands map[string]Command }

func NewRegistry() *Registry {
	r := &Registry{commands: make(map[string]Command)}
	for _, command := range standardCommands() {
		r.commands[command.Name] = command
	}
	return r
}

func (r *Registry) List() []Command {
	items := make([]Command, 0, len(r.commands))
	for _, command := range r.commands {
		items = append(items, command)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (r *Registry) Parse(input string) (Intent, bool, error) {
	if !strings.HasPrefix(strings.TrimSpace(input), "/") {
		return Intent{}, false, nil
	}
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return Intent{}, false, nil
	}
	command, ok := r.commands[parts[0]]
	if !ok {
		return Intent{}, true, fmt.Errorf("unknown command %s; type /help to see available commands", parts[0])
	}
	args := append([]string(nil), parts[1:]...)
	if command.validate != nil {
		if err := command.validate(args); err != nil {
			return Intent{}, true, err
		}
	}
	return Intent{Kind: command.kind, Command: command.Name, Value: command.value, Args: args}, true, nil
}

func standardCommands() []Command {
	return []Command{
		cmd("/help", "Show available commands", Help, exact(0)),
		cmd("/clear", "Clear current conversation", Clear, exact(0)),
		cmd("/reset", "Reset agent state", Reset, exact(0)),
		cmd("/init", "Create local workspace memory", InitializeWorkspace, exact(0)),
		cmd("/krypton", "Remove Supremo state from this workspace", Krypton, exact(0)),
		cmd("/session", "Open or manage chat sessions", Session, sessionArgs),
		cmd("/new", "Start a new blank chat session", NewSession, exact(0)),
		cmd("/delete-session", "Open the session deletion menu", DeleteSession, exact(0)),
		cmd("/rename-session", "Rename the current chat session", RenameSession, atLeast(1)),
		cmd("/plan", "Toggle Plan Mode or start a planning turn", Plan, nil),
		cmd("/tasks", "Show task and plan status", Tasks, exact(0)),
		cmd("/ux", "Show or toggle agent UX features", UX, uxArgs),
		cmd("/side", "Open a tool-free side question panel", SideQuestion, exact(0)),
		cmd("/rewind", "Restore files from a checkpoint", Rewind, exact(0)),
		cmd("/approve", "Approve the pending tool call", Approve, exact(0)),
		cmd("/deny", "Deny the pending tool call", Deny, nil),
		cmd("/dry-run", "Toggle dry run for mutating tools", DryRun, exact(0)),
		cmd("/mode", "View, cycle, or set approval mode", ApprovalMode, modeArgs),
		alias("/strict", "Ask before changes and commands run", ApprovalMode, "strict"),
		alias("/batman", "Approve routine work; ask for risky changes", ApprovalMode, "batman"),
		alias("/superman", "Approve every tool automatically", ApprovalMode, "superman"),
		cmd("/copy", "Copy the last assistant response", Copy, exact(0)),
		cmd("/export", "Export the chat to Markdown", Export, between(0, 1)),
		cmd("/diff", "Inspect workspace uncommitted changes", Diff, exact(0)),
		cmd("/cancel", "Cancel the active run or Plan Mode", Cancel, exact(0)),
		cmd("/tools", "List tools and approval policies", Tools, exact(0)),
		cmd("/activity", "Show recent tool activity", Activity, exact(0)),
		cmd("/doctor", "Check local setup without provider calls", Doctor, exact(0)),
		cmd("/exit", "Exit Supremo", Exit, exact(0)),
		cmd("/auth", "Enter the active provider API key securely", Auth, exact(0)),
		cmd("/provider", "Switch provider and optional endpoint", Provider, between(1, 2)),
		cmd("/providers", "List available providers", Providers, exact(0)),
		cmd("/endpoint", "Set the active provider endpoint", Endpoint, exact(1)),
		cmd("/models", "Alias for the unified model picker", Models, optionalRefresh),
		cmd("/usage", "Show runtime usage and account credits", Usage, optionalRefresh),
		cmd("/model", "Refresh and choose across configured providers", Model, between(0, 1)),
		cmd("/config", "View, reload, or update embedding configuration", Config, configArgs),
		cmd("/context", "Inspect compiled request context", Context, contextArgs),
		cmd("/index", "Manage semantic repository indexing", Index, indexArgs),
	}
}

func cmd(name, description string, kind Kind, validate func([]string) error) Command {
	return Command{Name: name, Description: description, kind: kind, validate: validate}
}

func alias(name, description string, kind Kind, value string) Command {
	return Command{Name: name, Description: description, kind: kind, value: value, validate: exact(0)}
}

func exact(count int) func([]string) error { return between(count, count) }

func atLeast(count int) func([]string) error {
	return func(args []string) error {
		if len(args) < count {
			return fmt.Errorf("usage: command requires at least %d argument(s)", count)
		}
		return nil
	}
}

func between(minimum, maximum int) func([]string) error {
	return func(args []string) error {
		if len(args) < minimum || len(args) > maximum {
			return fmt.Errorf("usage: command accepts %d to %d argument(s)", minimum, maximum)
		}
		return nil
	}
}

func sessionArgs(args []string) error {
	if len(args) == 0 || len(args) == 1 && (args[0] == "list" || args[0] == "current" || args[0] == "new") || len(args) == 2 && (args[0] == "new" || args[0] == "switch") {
		return nil
	}
	return fmt.Errorf("usage: /session [list|current|new [id]|switch <id>]")
}

func uxArgs(args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "status" {
		return nil
	}
	if len(args) == 2 && (args[0] == "checklist" || args[0] == "rewind" || args[0] == "retry") && (args[1] == "on" || args[1] == "off") {
		return nil
	}
	return fmt.Errorf("usage: /ux [status|checklist on|off|rewind on|off|retry on|off]")
}

func modeArgs(args []string) error {
	if len(args) == 0 || len(args) == 1 && (args[0] == "strict" || args[0] == "changes" || args[0] == "batman" || args[0] == "risky" || args[0] == "superman" || args[0] == "auto") {
		return nil
	}
	return fmt.Errorf("usage: /mode [strict|batman|superman]")
}

func optionalRefresh(args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "refresh" {
		return nil
	}
	return fmt.Errorf("usage: command accepts only optional refresh")
}

func configArgs(args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "reload" || len(args) == 4 && args[0] == "embeddings" {
		return nil
	}
	return fmt.Errorf("usage: /config [reload|embeddings <credential-provider> <endpoint> <model>]")
}

func contextArgs(args []string) error {
	if len(args) == 1 && (args[0] == "status" || args[0] == "show") {
		return nil
	}
	return fmt.Errorf("usage: /context <status|show>")
}

func indexArgs(args []string) error {
	if len(args) == 2 && args[0] == "semantic" && (args[1] == "on" || args[1] == "off" || args[1] == "status") {
		return nil
	}
	return fmt.Errorf("usage: /index semantic <on|off|status>")
}
