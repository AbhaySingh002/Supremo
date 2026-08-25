package approval

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

// ApprovalActionMsg communicates the user's decision on a pending tool.
type ApprovalActionMsg struct {
	Action    string // "approve", "deny", "edit", "auto"
	Tool      string
	Arguments string
}

// KeyMap defines the keybindings for tool approval actions.
type KeyMap struct {
	Approve key.Binding
	Deny    key.Binding
	Edit    key.Binding
	Auto    key.Binding
}

// DefaultKeyMap returns the canonical approval key bindings.
var DefaultKeyMap = KeyMap{
	Approve: key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y/enter", "allow")),
	Deny:    key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "deny")),
	Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	Auto:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "auto-approve")),
}

// ApprovalModel is a standalone Bubble Tea sub-model managing tool authorization.
type ApprovalModel struct {
	tool      string
	arguments string
	deciding  bool
	editing   bool
	err       string
	input     textarea.Model
	body      viewport.Model
	styles    rendering.Styles
	keys      KeyMap
}

// NewApprovalModel creates a new Approval sub-model.
func NewApprovalModel(tool, arguments string, st rendering.Styles) *ApprovalModel {
	input := textarea.New()
	input.ShowLineNumbers = false
	input.Prompt = ""
	input.SetWidth(56)
	input.SetHeight(4)
	input.SetValue(arguments)
	styles := input.Styles()
	styles.Cursor.Blink = true
	input.SetStyles(styles)
	body := viewport.New(viewport.WithWidth(56), viewport.WithHeight(8))
	body.FillHeight = false
	body.MouseWheelEnabled = true
	body.MouseWheelDelta = 2
	return &ApprovalModel{
		tool:      tool,
		arguments: arguments,
		input:     input,
		body:      body,
		styles:    st,
		keys:      DefaultKeyMap,
	}
}

// Tool returns the pending tool name.
func (m *ApprovalModel) Tool() string { return m.tool }

// Arguments returns the tool arguments.
func (m *ApprovalModel) Arguments() string { return m.arguments }

// IsDeciding returns whether approval submission is in flight.
func (m *ApprovalModel) IsDeciding() bool { return m.deciding }

// SetDeciding sets the in-flight state.
func (m *ApprovalModel) SetDeciding(deciding bool) { m.deciding = deciding }

// IsEditing returns whether argument JSON editing is active.
func (m *ApprovalModel) IsEditing() bool { return m.editing }

// SetError sets the validation error message.
func (m *ApprovalModel) SetError(err string) { m.err = err }

// Update handles keyboard navigation and editing for the approval dialog.
func (m *ApprovalModel) Update(msg tea.Msg) (*ApprovalModel, tea.Cmd) {
	if m.deciding {
		return m, nil
	}

	if _, ok := msg.(tea.MouseWheelMsg); ok && !m.editing {
		var cmd tea.Cmd
		m.body, cmd = m.body.Update(msg)
		return m, cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	if m.editing {
		switch keyMsg.String() {
		case "esc":
			m.editing = false
			m.err = ""
			m.input.SetValue(m.arguments)
			m.input.Blur()
			return m, nil
		case "enter":
			edited := strings.TrimSpace(m.input.Value())
			if edited == "" {
				m.err = "Arguments cannot be empty."
				return m, nil
			}
			m.arguments = edited
			m.editing = false
			m.err = ""
			m.input.Blur()
			return m, func() tea.Msg {
				return ApprovalActionMsg{Action: "edit", Tool: m.tool, Arguments: edited}
			}
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}

	switch {
	case key.Matches(keyMsg, m.keys.Approve):
		return m, func() tea.Msg {
			return ApprovalActionMsg{Action: "approve", Tool: m.tool, Arguments: m.arguments}
		}
	case key.Matches(keyMsg, m.keys.Deny):
		return m, func() tea.Msg {
			return ApprovalActionMsg{Action: "deny", Tool: m.tool, Arguments: m.arguments}
		}
	case key.Matches(keyMsg, m.keys.Edit):
		m.editing = true
		m.err = ""
		m.input.SetValue(m.arguments)
		return m, m.input.Focus()
	case key.Matches(keyMsg, m.keys.Auto):
		return m, func() tea.Msg {
			return ApprovalActionMsg{Action: "auto", Tool: m.tool, Arguments: m.arguments}
		}
	}

	switch keyMsg.String() {
	case "home":
		m.body.GotoTop()
		return m, nil
	case "end":
		m.body.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.body, cmd = m.body.Update(msg)
	return m, cmd
}

// View renders the approval dialog card.
func (m *ApprovalModel) View(width, height int) string {
	boxWidth := min(max(40, width-8), 76)
	inner := max(20, boxWidth-6)

	badge := m.styles.ApprovalDanger.Render("◆") + " " + m.styles.Title.Render("Approval required")
	promptText := FormatPrompt(m.tool, m.arguments)
	prompt := m.styles.Text.Bold(true).Render(promptText)

	var actions string
	if m.deciding {
		actions = m.styles.Muted.Render("Submitting decision...")
	} else if !m.editing {
		allowLabel, denyLabel, editLabel, autoLabel := m.keys.Approve.Help().Key+" "+m.keys.Approve.Help().Desc, m.keys.Deny.Help().Key+" "+m.keys.Deny.Help().Desc, m.keys.Edit.Help().Key+" "+m.keys.Edit.Help().Desc, m.keys.Auto.Help().Key+" "+m.keys.Auto.Help().Desc
		if inner < 58 {
			allowLabel, denyLabel, editLabel, autoLabel = "y allow", "n deny", "e edit", "a auto"
		}
		allowBtn := zone.Mark("approval-allow", m.styles.Success.Render(allowLabel))
		denyBtn := zone.Mark("approval-deny", m.styles.Error.Render(denyLabel))
		editBtn := zone.Mark("approval-edit", m.styles.Accent.Render(editLabel))
		autoBtn := zone.Mark("approval-auto", m.styles.Warning.Render(autoLabel))
		actions = allowBtn + "  " + denyBtn + "  " + editBtn + "  " + autoBtn
	}

	headerHeight := lipgloss.Height(badge) + lipgloss.Height(prompt) + 1
	actionHeight := lipgloss.Height(actions)
	availableBody := max(1, height-m.styles.ApprovalModal.GetVerticalFrameSize()-headerHeight-actionHeight-2)

	var body string
	if m.editing {
		m.input.SetWidth(inner)
		m.input.SetHeight(max(2, min(6, availableBody-1)))
		body = m.styles.Muted.Render("Edit JSON arguments (Enter to confirm, Esc to cancel):") + "\n" + m.input.View()
		if m.err != "" {
			body += "\n" + m.styles.Error.Render(m.err)
		}
	} else {
		content := FormatArguments(m.tool, m.arguments)
		if content == "" {
			content = m.tool
		}
		m.body.SetWidth(inner)
		m.body.SetContent(content)
		m.body.SetHeight(min(max(1, m.body.TotalLineCount()), availableBody))
		body = m.body.View()
	}

	lines := []string{badge, "", prompt, "", body}
	if actions != "" {
		lines = append(lines, "", actions)
	}

	return components.Card(m.styles.ApprovalModal, boxWidth, "", strings.Join(lines, "\n"))
}

// FormatArguments presents the exact approval scope without exposing the
// transport JSON as a generic field table.
func FormatArguments(tool, arguments string) string {
	start := strings.Index(arguments, "{")
	if start < 0 {
		return strings.TrimSpace(arguments)
	}
	var values map[string]any
	if json.Unmarshal([]byte(arguments[start:]), &values) != nil {
		return strings.TrimSpace(arguments)
	}
	if tool == "execute_command" || tool == "local_shell" {
		command, _ := values["command"].(string)
		if rawArgs, ok := values["args"].([]any); ok {
			args := make([]string, 0, len(rawArgs))
			for _, value := range rawArgs {
				if text, ok := value.(string); ok {
					args = append(args, text)
				}
			}
			command = strings.TrimSpace(command + " " + strings.Join(args, " "))
		}
		if command != "" {
			lines := []string{"Shell", "$ " + command}
			if directory, _ := values["directory"].(string); directory != "" {
				lines = append(lines, "in  "+directory)
			}
			return strings.Join(lines, "\n")
		}
	}
	order := []string{"path", "directory", "old_path", "new_path", "pattern", "query", "symbol", "url", "label", "scope"}
	seen := make(map[string]bool, len(order))
	lines := make([]string, 0, len(values))
	appendValue := func(key string, value any) {
		seen[key] = true
		label := strings.ReplaceAll(key, "_", " ")
		switch value := value.(type) {
		case string:
			if key == "content" || key == "replacement" || key == "prompt" || key == "message" {
				lines = append(lines, fmt.Sprintf("%s  %d characters", label, len(value)))
			} else if strings.TrimSpace(value) != "" {
				lines = append(lines, label+"  "+value)
			}
		case bool, float64:
			lines = append(lines, fmt.Sprintf("%s  %v", label, value))
		case []any:
			lines = append(lines, fmt.Sprintf("%s  %d items", label, len(value)))
		}
	}
	for _, key := range order {
		if value, ok := values[key]; ok {
			appendValue(key, value)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] && key != "api_key" && key != "key" && key != "environment" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		appendValue(key, values[key])
	}
	if len(lines) == 0 {
		return strings.ReplaceAll(tool, "_", " ")
	}
	return strings.Join(lines, "\n")
}

// FormatPrompt returns a friendly explanation of the tool being executed.
func FormatPrompt(tool, arguments string) string {
	switch tool {
	case "write_file", "replace_in_file":
		return "Write / modify file on disk?"
	case "delete_file":
		return "Delete file from disk?"
	case "rename_file":
		return "Rename file on disk?"
	case "execute_command":
		return "Run shell command?"
	default:
		return "Execute tool: " + tool + "?"
	}
}
