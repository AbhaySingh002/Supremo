package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/commands"
)

func executeCommandCmd(ctx context.Context, client api.Client, registry *commands.Registry, session api.Session, input string, id int) tea.Cmd {
	return func() tea.Msg {
		intent, handled, err := registry.Parse(input)
		result := commandResultMsg{id: id, input: input, session: session, intent: intent, err: err}
		if !handled || err != nil {
			return result
		}
		if client == nil && backendIntent(intent.Kind) {
			result.err = fmt.Errorf("backend is unavailable")
			return result
		}
		sessionRequest := api.SessionRequest{SessionID: session.ID}
		switch intent.Kind {
		case commands.Help:
			var out strings.Builder
			out.WriteString("Available commands:\n")
			for _, command := range registry.List() {
				fmt.Fprintf(&out, "  %-18s %s\n", command.Name, command.Description)
			}
			result.output = strings.TrimSpace(out.String())
		case commands.Clear:
			snapshot, callErr := client.ClearSession(ctx, sessionRequest)
			result.snapshot, result.err, result.output = &snapshot, callErr, "Conversation cleared."
		case commands.Reset:
			snapshot, callErr := client.ResetSession(ctx, sessionRequest)
			result.snapshot, result.err, result.output = &snapshot, callErr, "Agent state and conversation history reset."
		case commands.InitializeWorkspace:
			_, result.err = client.InitializeWorkspace(ctx)
			result.output = "Workspace memory initialized."
		case commands.Session:
			result = executeSessionIntent(ctx, client, result)
		case commands.NewSession:
			created, callErr := client.CreateSession(ctx, api.CreateSessionRequest{})
			result.session, result.err, result.switchSession = created, callErr, callErr == nil
			result.output = "Created chat session " + created.Name + "."
		case commands.RenameSession:
			name := strings.Join(intent.Args, " ")
			updated, callErr := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: session.ID, ExpectedRevision: session.Revision, Name: &name})
			result.session, result.err, result.output = updated, callErr, "Renamed chat session to "+name+"."
		case commands.Plan:
			result = executePlanIntent(ctx, client, result)
		case commands.Tasks:
			result.output = fmt.Sprintf("Active session: %s\nPlan Mode: %s", session.ID, onOff(session.PlanMode))
		case commands.UX:
			result = executeUXIntent(ctx, client, result)
		case commands.DryRun:
			value := !session.DryRun
			updated, callErr := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: session.ID, ExpectedRevision: session.Revision, DryRun: &value})
			result.session, result.err, result.output = updated, callErr, "Dry run "+onOff(value)+"."
		case commands.ApprovalMode:
			result = executeModeIntent(ctx, client, result)
		case commands.Export:
			snapshot, callErr := client.GetSession(ctx, session.ID)
			result.err = callErr
			if callErr == nil {
				filename := ""
				if len(intent.Args) > 0 {
					filename = intent.Args[0]
				}
				result.output, result.err = exportSnapshot(snapshot, filename)
			}
		case commands.Tools:
			items, callErr := client.ListTools(ctx, sessionRequest)
			result.err, result.output = callErr, formatTools(items)
		case commands.Activity:
			items, callErr := client.ToolActivity(ctx, sessionRequest)
			result.err, result.output = callErr, formatActivity(items)
		case commands.Doctor:
			report, callErr := client.Health(ctx)
			result.err, result.output = callErr, formatHealth(report)
		case commands.Auth:
			result.output = "Open the secure credential prompt with /auth."
		case commands.Provider:
			provider := intent.Args[0]
			request := api.ConfigureProviderRequest{Provider: &provider}
			if len(intent.Args) == 2 {
				request.Endpoint = &intent.Args[1]
			}
			initialized, callErr := client.ConfigureProvider(ctx, request)
			result.initialize, result.err, result.output = &initialized, callErr, "Provider updated to "+provider+"."
		case commands.Providers:
			initialized, callErr := client.Initialize(ctx)
			result.initialize, result.err, result.output = &initialized, callErr, formatProviders(initialized)
		case commands.Endpoint:
			endpoint := intent.Args[0]
			initialized, callErr := client.ConfigureProvider(ctx, api.ConfigureProviderRequest{Endpoint: &endpoint})
			result.initialize, result.err, result.output = &initialized, callErr, "Endpoint updated."
		case commands.Models:
			catalog, callErr := client.ListModels(ctx, api.ListModelsRequest{Refresh: true})
			result.err, result.output = callErr, formatModelCatalog(catalog)
		case commands.Usage:
			if len(intent.Args) == 1 {
				_, result.err = client.RefreshProviderMetadata(ctx)
			}
			if result.err == nil {
				usage, callErr := client.ProviderUsage(ctx)
				result.err, result.output = callErr, formatUsage(usage)
			}
		case commands.Model:
			if len(intent.Args) == 0 {
				catalog, callErr := client.ListModels(ctx, api.ListModelsRequest{Refresh: true})
				result.err, result.output = callErr, formatModelCatalog(catalog)
				break
			}
			model := strings.Join(intent.Args, " ")
			initialized, callErr := client.ConfigureProvider(ctx, api.ConfigureProviderRequest{Model: &model})
			result.initialize, result.err, result.output = &initialized, callErr, "Model updated to "+model+"."
		case commands.Config:
			result = executeConfigIntent(ctx, client, result)
		case commands.Context:
			status, callErr := client.ContextStatus(ctx, api.ContextStatusRequest{SessionID: session.ID, Detailed: intent.Args[0] == "show"})
			result.err, result.output = callErr, formatContext(status)
		case commands.Index:
			var status api.IndexStatus
			var callErr error
			if intent.Args[1] == "status" {
				status, callErr = client.IndexStatus(ctx)
			} else {
				status, callErr = client.UpdateIndex(ctx, api.UpdateIndexRequest{Semantic: intent.Args[1] == "on"})
			}
			result.err, result.output = callErr, formatIndex(status)
		case commands.Cancel:
			if !session.PlanModeActive() {
				result.output = "No run or Plan Mode is active."
				break
			}
			active := false
			updated, callErr := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: session.ID, ExpectedRevision: session.Revision, PlanMode: &active})
			result.session, result.err, result.output = updated, callErr, "Plan Mode cancelled."
		case commands.Approve, commands.Deny:
			result.output = "No interaction is awaiting a response."
		}
		return result
	}
}

func setPlanModeCmd(ctx context.Context, client api.Client, session api.Session, active bool) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return commandResultMsg{session: session, input: "/plan", err: fmt.Errorf("backend is unavailable")}
		}
		updated, err := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: session.ID, ExpectedRevision: session.Revision, PlanMode: &active})
		status := "Plan mode disabled."
		if active {
			status = "Plan mode enabled. Explore the repository and prepare a durable plan."
		}
		return commandResultMsg{session: updated, input: "/plan", intent: commands.Intent{Kind: commands.Plan}, output: status, err: err}
	}
}

func backendIntent(kind commands.Kind) bool {
	switch kind {
	case commands.Help, commands.Copy, commands.Exit, commands.Krypton, commands.Diff, commands.SideQuestion, commands.Rewind, commands.Approve, commands.Deny:
		return false
	default:
		return true
	}
}

func executePlanIntent(ctx context.Context, client api.Client, result commandResultMsg) commandResultMsg {
	args := result.intent.Args
	if len(args) == 1 && (args[0] == "status" || args[0] == "show") {
		result.output = "Plan Mode is " + onOff(result.session.PlanModeActive()) + "."
		if args[0] == "show" {
			snapshot, err := client.GetSession(ctx, result.session.ID)
			result.err = err
			if err == nil {
				result.output = latestPlanResponse(snapshot.Messages)
				if result.output == "" {
					result.output = "No durable plan response is available in this session."
				}
			}
		}
		return result
	}
	active := !result.session.PlanModeActive()
	output := "Plan Mode " + onOff(active) + "."
	if len(args) > 0 {
		active = true
		output = "Plan Mode enabled."
		switch args[0] {
		case "cancel":
			active, output = false, "Plan Mode cancelled."
		case "execute":
			active, output, result.followupPrompt = false, "Executing the most recent plan.", "Execute the most recent durable plan in this session."
		case "resume":
			result.followupPrompt = "Resume the current plan from the durable session context."
		default:
			result.followupPrompt = strings.Join(args, " ")
		}
	}
	updated, err := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: result.session.ID, ExpectedRevision: result.session.Revision, PlanMode: &active})
	result.session, result.err, result.output = updated, err, output
	if err != nil {
		result.followupPrompt = ""
	}
	return result
}

func latestPlanResponse(messages []api.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "assistant" {
			continue
		}
		var content strings.Builder
		for _, part := range messages[index].Parts {
			content.WriteString(part.Text)
		}
		if value := strings.TrimSpace(content.String()); planWorkflowOutput(value) {
			return value
		}
	}
	return ""
}

func executeSessionIntent(ctx context.Context, client api.Client, result commandResultMsg) commandResultMsg {
	args := result.intent.Args
	if len(args) == 0 || args[0] == "list" {
		items, err := client.ListSessions(ctx)
		result.err = err
		var out strings.Builder
		out.WriteString("Chat sessions:\n")
		for _, item := range items {
			marker := "  "
			if item.ID == result.session.ID {
				marker = "* "
			}
			fmt.Fprintf(&out, "%s%s (%s)\n", marker, item.Name, item.ID)
		}
		result.output = strings.TrimSpace(out.String())
		return result
	}
	if args[0] == "current" {
		result.output = fmt.Sprintf("Current chat session: %s (%s)", result.session.Name, result.session.ID)
		return result
	}
	if args[0] == "new" {
		request := api.CreateSessionRequest{}
		if len(args) == 2 {
			request.ID = args[1]
		}
		created, err := client.CreateSession(ctx, request)
		result.session, result.err, result.switchSession = created, err, err == nil
		result.output = "Created chat session " + created.Name + "."
		return result
	}
	snapshot, err := client.GetSession(ctx, args[1])
	result.session, result.snapshot, result.err, result.switchSession = snapshot.Session, &snapshot, err, err == nil
	result.output = "Switched to chat session " + snapshot.Session.ID + "."
	return result
}

func executeUXIntent(ctx context.Context, client api.Client, result commandResultMsg) commandResultMsg {
	if len(result.intent.Args) < 2 {
		result.output = fmt.Sprintf("Agent UX:\n  checklist: %s\n  rewind: %s\n  provider retry: %s", onOff(result.session.Checklist), onOff(result.session.Rewind), onOff(result.session.ProviderRetry))
		return result
	}
	enabled := result.intent.Args[1] == "on"
	request := api.UpdateSessionRequest{SessionID: result.session.ID, ExpectedRevision: result.session.Revision}
	switch result.intent.Args[0] {
	case "checklist":
		request.Checklist = &enabled
	case "rewind":
		request.Rewind = &enabled
	case "retry":
		request.ProviderRetry = &enabled
	}
	updated, err := client.UpdateSession(ctx, request)
	result.session, result.err, result.output = updated, err, result.intent.Args[0]+" "+onOff(enabled)+"."
	return result
}

func executeModeIntent(ctx context.Context, client api.Client, result commandResultMsg) commandResultMsg {
	mode := result.intent.Value
	if mode == "" && len(result.intent.Args) > 0 {
		mode = map[string]string{"changes": "strict", "risky": "batman", "auto": "superman"}[result.intent.Args[0]]
		if mode == "" {
			mode = result.intent.Args[0]
		}
	}
	if mode == "" {
		mode = map[string]string{"strict": "batman", "batman": "superman", "superman": "strict"}[result.session.ApprovalMode]
		if mode == "" {
			mode = "batman"
		}
	}
	updated, err := client.UpdateSession(ctx, api.UpdateSessionRequest{SessionID: result.session.ID, ExpectedRevision: result.session.Revision, ApprovalMode: &mode})
	result.session, result.err, result.output = updated, err, approvalModeStatus(mode)
	return result
}

func executeConfigIntent(ctx context.Context, client api.Client, result commandResultMsg) commandResultMsg {
	if len(result.intent.Args) == 4 {
		result.err = client.ConfigureEmbeddings(ctx, api.ConfigureEmbeddingsRequest{CredentialProvider: result.intent.Args[1], Endpoint: result.intent.Args[2], Model: result.intent.Args[3]})
		result.output = "Embedding configuration updated."
		return result
	}
	var initialized api.InitializeResult
	if len(result.intent.Args) == 1 {
		initialized, result.err = client.ReloadConfiguration(ctx)
	} else {
		initialized, result.err = client.Initialize(ctx)
	}
	result.initialize = &initialized
	result.output = fmt.Sprintf("Active configuration:\n  Provider: %s\n  Model: %s\n  Endpoint: %s\n  Credential: %s", initialized.Provider, initialized.Model, initialized.Endpoint, map[bool]string{true: "configured", false: "needs setup"}[initialized.CredentialReady])
	return result
}

func exportSnapshot(snapshot api.SessionSnapshot, filename string) (string, error) {
	if filename == "" {
		filename = "supremo-chat-" + strings.ReplaceAll(snapshot.Session.ID, "/", "-") + ".md"
	}
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}
	if len(snapshot.Messages) == 0 {
		return "Chat session is empty; nothing to export.", nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# Supremo Chat Session: %s\n\n- **Session ID**: `%s`\n- **Exported**: %s\n\n---\n\n", snapshot.Session.Name, snapshot.Session.ID, time.Now().Format("2006-01-02 15:04:05"))
	for _, message := range snapshot.Messages {
		text := messageText(message)
		if text == "" {
			continue
		}
		label := strings.Title(message.Role) //nolint:staticcheck
		if message.Role == "user" {
			label = "You"
		} else if message.Role == "assistant" {
			label = "Supremo"
		}
		fmt.Fprintf(&out, "### %s\n\n%s\n\n", label, text)
	}
	if err := os.WriteFile(filename, []byte(out.String()), 0o644); err != nil {
		return "", err
	}
	return "Chat exported successfully to " + filename, nil
}

func messageText(message api.Message) string {
	var out strings.Builder
	for _, part := range message.Parts {
		out.WriteString(part.Text)
	}
	return out.String()
}

func formatTools(items []api.Tool) string {
	var out strings.Builder
	for _, item := range items {
		fmt.Fprintf(&out, "%-20s %-18s %s\n", item.Name, item.Approval, item.Description)
	}
	return strings.TrimSpace(out.String())
}

func formatActivity(items []api.ToolActivity) string {
	if len(items) == 0 {
		return "No recent tool activity."
	}
	var out strings.Builder
	for _, item := range items {
		fmt.Fprintf(&out, "- %s %s: %s", item.Time.Local().Format("15:04:05"), item.Tool, item.Status)
		if item.Message != "" {
			fmt.Fprintf(&out, " (%s)", item.Message)
		}
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

func formatHealth(report api.HealthReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Supremo doctor\n- Workspace: %s\n", report.Workspace)
	for _, check := range report.Checks {
		fmt.Fprintf(&out, "- %s: %s", check.Name, check.Status)
		if check.Message != "" {
			fmt.Fprintf(&out, " (%s)", check.Message)
		}
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

func formatProviders(value api.InitializeResult) string {
	var out strings.Builder
	for _, provider := range value.Providers {
		marker := "  "
		if provider.ID == strings.SplitN(value.Provider, ":", 2)[0] {
			marker = "* "
		}
		fmt.Fprintf(&out, "%s%s", marker, provider.Name)
		if !provider.Configured {
			out.WriteString(" · needs setup")
		}
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

func formatModels(value api.InitializeResult) string {
	var models []api.Model
	for _, provider := range value.Providers {
		if provider.ID == strings.SplitN(value.Provider, ":", 2)[0] {
			models = provider.Models
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if len(models) == 0 {
		return "No cached models. Run /models refresh after configuring a key."
	}
	var out strings.Builder
	for _, model := range models {
		fmt.Fprintf(&out, "- %s", model.ID)
		if model.ContextLength > 0 {
			fmt.Fprintf(&out, " (context %d)", model.ContextLength)
		}
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

func formatModelCatalog(value api.ModelCatalog) string {
	var out strings.Builder
	for _, provider := range value.Providers {
		fmt.Fprintf(&out, "%s:\n", provider.Name)
		for _, model := range provider.Models {
			fmt.Fprintf(&out, "  - %s\n", model.ID)
		}
		if provider.MetadataWarning != "" {
			fmt.Fprintf(&out, "  ! %s\n", provider.MetadataWarning)
		}
	}
	if out.Len() == 0 {
		return "No text-generation models are available for configured providers."
	}
	return strings.TrimSpace(out.String())
}

func formatUsage(value api.Usage) string {
	out := fmt.Sprintf("Runtime usage: input %d, output %d", value.InputTokens, value.OutputTokens)
	if value.CostUSD != nil {
		out += fmt.Sprintf(", cost $%.6f", *value.CostUSD)
	}
	if value.CreditsRemain != nil {
		out += fmt.Sprintf("\nAccount credits: $%.6f remaining", *value.CreditsRemain)
	}
	if value.ContextLimit > 0 {
		out += fmt.Sprintf("\nSelected model context: %d tokens", value.ContextLimit)
	}
	return out
}

func formatContext(value api.ContextStatus) string {
	if value.RequestID == "" {
		return "No context has been compiled for this chat yet."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Context %s\nInput capacity: %d/%d tokens; output reserve %d\nWorking set: %d items (generation %d)", value.RequestID, value.EstimatedUsed, value.InputBudget, value.OutputReserve, value.WorkingSetItems, value.Generation)
	for _, item := range value.Items {
		fmt.Fprintf(&out, "\n- %s %s: %s (%d tokens, %s)", item.Layer, item.Kind, item.ID, item.Tokens, item.Reason)
	}
	return out.String()
}

func formatIndex(value api.IndexStatus) string {
	result := fmt.Sprintf("Index: %s%s\nSemantic: %s (embedding provider configured: %t)", map[bool]string{true: "ready", false: "starting"}[value.Ready], map[bool]string{true: ", dirty", false: ""}[value.Dirty], onOff(value.Semantic), value.Configured)
	if value.Error != "" {
		result += "\nLast scan error: " + value.Error
	}
	return result
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
