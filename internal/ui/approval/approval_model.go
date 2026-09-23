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
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

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
	Approve: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "allow")),
	Deny:    key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "deny")),
	Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	Auto:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "auto-approve")),
}

const (
	approvalAllow = iota
	approvalAuto
	approvalEdit
	approvalDeny
)

// ApprovalModel is a standalone Bubble Tea sub-model managing tool authorization.
type ApprovalModel struct {
	tool      string
	arguments string
	deciding  bool
	editing   bool
	choice    int
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
		choice:    approvalDeny,
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

// Select sets the highlighted decision for keyboard and mouse activation.
func (m *ApprovalModel) Select(choice int) {
	m.choice = min(approvalDeny, max(approvalAllow, choice))
}

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

	switch keyMsg.String() {
	case "up", "k":
		m.Select(m.choice - 1)
		return m, nil
	case "down", "j":
		m.Select(m.choice + 1)
		return m, nil
	case "1", "2", "3", "4":
		m.Select(int(keyMsg.String()[0] - '1'))
		return m, nil
	case "enter":
		return m, m.choiceAction()
	case "home":
		m.body.GotoTop()
		return m, nil
	case "end":
		m.body.GotoBottom()
		return m, nil
	}
	switch {
	case key.Matches(keyMsg, m.keys.Approve):
		m.Select(approvalAllow)
		return m, m.choiceAction()
	case key.Matches(keyMsg, m.keys.Deny):
		m.Select(approvalDeny)
		return m, m.choiceAction()
	case key.Matches(keyMsg, m.keys.Edit):
		m.editing = true
		m.err = ""
		m.input.SetValue(m.arguments)
		return m, m.input.Focus()
	case key.Matches(keyMsg, m.keys.Auto):
		m.Select(approvalAuto)
		return m, m.choiceAction()
	}
	var cmd tea.Cmd
	m.body, cmd = m.body.Update(msg)
	return m, cmd
}

func (m *ApprovalModel) choiceAction() tea.Cmd {
	action := "deny"
	switch m.choice {
	case approvalAllow:
		action = "approve"
	case approvalAuto:
		action = "auto"
	case approvalEdit:
		action = "edit"
		m.editing = true
		m.err = ""
		m.input.SetValue(m.arguments)
		return m.input.Focus()
	}
	return func() tea.Msg {
		return ApprovalActionMsg{Action: action, Tool: m.tool, Arguments: m.arguments}
	}
}

// View renders a compact, keyboard-first approval decision sheet.
func (m *ApprovalModel) View(width, height int) string {
	width = max(20, width)
	inner := max(16, width-2)

	rule := strings.Repeat("─", width)
	if m.styles.Ascii {
		rule = strings.Repeat("-", width)
	}
	badge := m.styles.ApprovalDanger.Render("Approval required")
	promptText := FormatPrompt(m.tool, m.arguments)
	prompt := m.styles.Text.Bold(true).Render(ansi.Hardwrap(promptText, inner, true))
	choices := m.choiceView()
	footer := m.styles.Muted.Render("↑↓ or 1–4 choose · Enter decide · Esc deny")
	compact := height < 16
	preamble := []string{m.styles.Muted.Render(rule), badge, prompt, ""}
	trailer := []string{m.styles.Text.Render("Do you want to proceed?"), choices, "", footer}
	if compact {
		preamble = []string{m.styles.Muted.Render(rule), m.styles.Text.Bold(true).Render(ansi.Truncate(promptText, max(8, inner-lipgloss.Width(badge)-3), "…"))}
		preamble[1] = badge + m.styles.Muted.Render(" · ") + preamble[1]
		trailer = []string{m.styles.Text.Render("Choose:"), choices, footer}
	}
	availableBody := max(1, height-lipgloss.Height(strings.Join(preamble, "\n"))-lipgloss.Height(strings.Join(trailer, "\n")))

	var body string
	if m.editing {
		m.input.SetWidth(inner)
		m.input.SetHeight(max(2, min(6, availableBody-1)))
		body = m.styles.Muted.Render("Edit JSON arguments (Enter to confirm, Esc to cancel):") + "\n" + m.input.View()
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
	if m.err != "" {
		body += "\n" + m.styles.Error.Render("× "+m.err)
	}

	lines := append(append([]string{}, preamble...), body)
	if m.deciding {
		lines = append(lines, "", m.styles.Muted.Render("Submitting decision..."))
	} else if !m.editing {
		lines = append(lines, trailer...)
	}
	return strings.Join(lines, "\n")
}

func (m *ApprovalModel) choiceView() string {
	labels := []string{
		"Yes, allow once",
		"Yes, switch this session to auto mode",
		"Edit command or arguments",
		"No",
	}
	lines := make([]string, 0, len(labels))
	for index, label := range labels {
		prefix, style := "  ", m.styles.Muted
		if index == m.choice {
			prefix, style = "> ", m.styles.Accent
		}
		line := fmt.Sprintf("%s%d. %s", prefix, index+1, label)
		lines = append(lines, zone.Mark(fmt.Sprintf("approval-choice-%d", index), style.Render(line)))
	}
	return strings.Join(lines, "\n")
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
