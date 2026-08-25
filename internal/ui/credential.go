package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

type credentialStep int

const (
	credentialEndpoint credentialStep = iota
	credentialKey
)

type credentialSetup struct {
	provider api.Provider
	endpoint textinput.Model
	key      textinput.Model
	step     credentialStep
	loading  bool
	err      string
	styles   rendering.Styles
}

type credentialCancelledMsg struct{}

type credentialSubmittedMsg struct {
	provider string
	endpoint string
	key      string
}

func newCredentialSetup(provider api.Provider, styles rendering.Styles) *credentialSetup {
	endpoint := textinput.New()
	endpoint.Prompt = "endpoint  "
	endpoint.Placeholder = "https://api.example.com/v1"
	endpoint.SetValue(provider.Endpoint)
	endpoint.SetWidth(60)
	key := textinput.New()
	key.Prompt = "api key   "
	key.Placeholder = "paste credential"
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	key.SetWidth(60)
	for _, input := range []*textinput.Model{&endpoint, &key} {
		inputStyles := input.Styles()
		inputStyles.Focused.Prompt = styles.Accent
		inputStyles.Focused.Text = styles.Text
		inputStyles.Focused.Placeholder = styles.Muted
		inputStyles.Blurred.Prompt = styles.Muted
		inputStyles.Blurred.Text = styles.Text
		inputStyles.Cursor.Blink = true
		if !styles.Ascii {
			inputStyles.Cursor.Color = styles.Accent.GetForeground()
		} else {
			inputStyles.Cursor.Color = nil
		}
		input.SetStyles(inputStyles)
	}
	step := credentialKey
	if provider.RequiresEndpoint {
		step = credentialEndpoint
		endpoint.Focus()
	} else {
		key.Focus()
	}
	return &credentialSetup{provider: provider, endpoint: endpoint, key: key, step: step, styles: styles}
}

func (m *credentialSetup) Update(msg tea.KeyPressMsg) tea.Cmd {
	if m == nil || m.loading {
		return nil
	}
	if msg.String() == "esc" || msg.Code == tea.KeyEsc {
		return func() tea.Msg { return credentialCancelledMsg{} }
	}
	if msg.String() == "enter" || msg.Code == tea.KeyEnter {
		if m.step == credentialEndpoint {
			if strings.TrimSpace(m.endpoint.Value()) == "" {
				m.err = "Endpoint is required."
				return nil
			}
			m.err, m.step = "", credentialKey
			m.endpoint.Blur()
			return m.key.Focus()
		}
		if strings.TrimSpace(m.key.Value()) == "" {
			m.err = "API key is required."
			return nil
		}
		m.loading, m.err = true, ""
		provider, endpoint, key := m.provider.ID, strings.TrimSpace(m.endpoint.Value()), m.key.Value()
		return func() tea.Msg { return credentialSubmittedMsg{provider: provider, endpoint: endpoint, key: key} }
	}
	var cmd tea.Cmd
	if m.step == credentialEndpoint {
		m.endpoint, cmd = m.endpoint.Update(msg)
	} else {
		m.key, cmd = m.key.Update(msg)
	}
	return cmd
}

func (m *credentialSetup) View(width, height int, spinner string) string {
	if m == nil {
		return ""
	}
	status := "Enter saves · Esc cancels"
	if m.step == credentialEndpoint {
		status = "Enter continues to API key · Esc cancels"
	}
	if m.loading {
		status = spinner + " verifying credential and model access…"
	}
	lines := []string{
		m.styles.Title.Render("Connect " + m.provider.Name),
		m.styles.Muted.Render("The key is stored locally and never added to chat history."),
		"",
	}
	if height < 9 {
		lines = lines[:1]
	}
	if m.provider.RequiresEndpoint {
		lines = append(lines, m.endpoint.View())
	}
	lines = append(lines, m.key.View())
	if m.err != "" {
		lines = append(lines, "", m.styles.Error.Render("× "+m.err))
	}
	lines = append(lines, "", m.styles.Muted.Render(status))
	return components.Card(m.styles.Overlay, max(28, min(width-4, 76)), "", strings.Join(lines, "\n"))
}

func (m *credentialSetup) clear() {
	if m != nil {
		m.key.Reset()
	}
}
