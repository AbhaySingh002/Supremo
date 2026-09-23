package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

type credentialStep int

const (
	credentialName credentialStep = iota
	credentialEndpoint
	credentialKey
	credentialModel
)

const customProviderID = "custom"

type credentialSetup struct {
	provider api.Provider
	name     textinput.Model
	endpoint textinput.Model
	key      textinput.Model
	model    textinput.Model
	custom   bool
	step     credentialStep
	loading  bool
	err      string
	styles   rendering.Styles
}

type credentialCancelledMsg struct{}

type credentialSubmittedMsg struct {
	provider   string
	endpoint   string
	key        string
	model      string
	openModels bool
}

func newCredentialSetup(provider api.Provider, styles rendering.Styles) *credentialSetup {
	return newCredentialSetupWithMode(provider, styles, false)
}

func newCustomCredentialSetup(styles rendering.Styles) *credentialSetup {
	provider := api.Provider{ID: "openai-compatible", Name: "Custom OpenAI-compatible", RequiresEndpoint: true}
	return newCredentialSetupWithMode(provider, styles, true)
}

func newCredentialSetupWithMode(provider api.Provider, styles rendering.Styles, custom bool) *credentialSetup {
	name := textinput.New()
	name.Prompt = "name      "
	name.Placeholder = "ollama"
	name.SetWidth(60)
	endpoint := textinput.New()
	endpoint.Prompt = "endpoint  "
	endpoint.Placeholder = "http://localhost:11434/v1"
	endpoint.SetValue(provider.Endpoint)
	endpoint.SetWidth(60)
	key := textinput.New()
	key.Prompt = "api key   "
	key.Placeholder = map[bool]string{true: "optional for local servers", false: "paste credential"}[custom]
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	key.SetWidth(60)
	model := textinput.New()
	model.Prompt = "model     "
	model.Placeholder = "llama3.2"
	model.SetWidth(60)
	for _, input := range []*textinput.Model{&name, &endpoint, &key, &model} {
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
	if custom {
		step = credentialName
		name.Focus()
	} else if provider.RequiresEndpoint {
		step = credentialEndpoint
		endpoint.Focus()
	} else {
		key.Focus()
	}
	return &credentialSetup{provider: provider, name: name, endpoint: endpoint, key: key, model: model, custom: custom, step: step, styles: styles}
}

func (m *credentialSetup) Update(msg tea.Msg) tea.Cmd {
	if m == nil || m.loading {
		return nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "esc" || msg.Code == tea.KeyEsc {
			return func() tea.Msg { return credentialCancelledMsg{} }
		}
		if msg.String() == "enter" || msg.Code == tea.KeyEnter {
			switch m.step {
			case credentialName:
				if _, err := customProviderRoute(m.name.Value()); err != nil {
					m.err = err.Error()
					return nil
				}
				m.err, m.step = "", credentialEndpoint
				m.name.Blur()
				return m.endpoint.Focus()
			case credentialEndpoint:
				if strings.TrimSpace(m.endpoint.Value()) == "" {
					m.err = "Endpoint is required."
					return nil
				}
				m.err, m.step = "", credentialKey
				m.endpoint.Blur()
				return m.key.Focus()
			case credentialKey:
				if !m.custom && strings.TrimSpace(m.key.Value()) == "" {
					m.err = "API key is required."
					return nil
				}
				if m.custom {
					m.err, m.step = "", credentialModel
					m.key.Blur()
					return m.model.Focus()
				}
				return m.submit()
			case credentialModel:
				if strings.TrimSpace(m.model.Value()) == "" {
					m.err = "Model is required."
					return nil
				}
				return m.submit()
			}
		}
	}
	var cmd tea.Cmd
	switch m.step {
	case credentialName:
		m.name, cmd = m.name.Update(msg)
	case credentialEndpoint:
		m.endpoint, cmd = m.endpoint.Update(msg)
	case credentialKey:
		m.key, cmd = m.key.Update(msg)
	case credentialModel:
		m.model, cmd = m.model.Update(msg)
	}
	return cmd
}

func (m *credentialSetup) submit() tea.Cmd {
	m.loading, m.err = true, ""
	provider := m.provider.ID
	if m.custom {
		var err error
		provider, err = customProviderRoute(m.name.Value())
		if err != nil {
			m.loading, m.err = false, err.Error()
			return nil
		}
	}
	endpoint, key, model := strings.TrimSpace(m.endpoint.Value()), m.key.Value(), strings.TrimSpace(m.model.Value())
	return func() tea.Msg {
		return credentialSubmittedMsg{provider: provider, endpoint: endpoint, key: key, model: model, openModels: !m.custom}
	}
}

func (m *credentialSetup) focus() tea.Cmd {
	if m == nil {
		return nil
	}
	switch m.step {
	case credentialName:
		return m.name.Focus()
	case credentialEndpoint:
		return m.endpoint.Focus()
	case credentialModel:
		return m.model.Focus()
	default:
		return m.key.Focus()
	}
}

func customProviderRoute(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("A name is required.")
	}
	if len(name) > 48 {
		return "", fmt.Errorf("Use a name of 48 characters or fewer.")
	}
	for _, r := range name {
		if !(unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return "", fmt.Errorf("Use lowercase letters, numbers, hyphens, underscores, or dots.")
		}
	}
	return "openai-compatible:" + name, nil
}

func (m *credentialSetup) View(width, height int, spinner string) string {
	if m == nil {
		return ""
	}
	status := "Enter saves · Esc cancels"
	switch m.step {
	case credentialName:
		status = "Enter continues to endpoint · Esc cancels"
	case credentialEndpoint:
		status = "Enter continues to API key · Esc cancels"
	case credentialKey:
		if m.custom {
			status = "Enter continues to model · Esc cancels"
		}
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
	if m.custom {
		lines = append(lines, m.name.View())
	}
	if m.provider.RequiresEndpoint {
		lines = append(lines, m.endpoint.View())
	}
	lines = append(lines, m.key.View())
	if m.custom {
		lines = append(lines, m.model.View())
	}
	if m.err != "" {
		lines = append(lines, "", m.styles.Error.Render("× "+m.err))
	}
	lines = append(lines, "", m.styles.Muted.Render(status))
	return components.Card(m.styles.Overlay, max(28, min(width-4, 76)), "", strings.Join(lines, "\n"))
}

func (m *credentialSetup) clear() {
	if m != nil {
		m.name.Reset()
		m.key.Reset()
		m.model.Reset()
	}
}
