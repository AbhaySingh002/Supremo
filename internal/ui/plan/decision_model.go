package plan

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

// PlanDecisionCompletedMsg is sent when all plan questions have been answered.
type PlanDecisionCompletedMsg struct {
	Answers map[string]string
}

// PlanDecisionCancelledMsg is sent when plan questioning is cancelled.
type PlanDecisionCancelledMsg struct{}

type lineRange struct{ start, end int }

// PlanQuestionModel is a focused, height-aware Plan Mode decision surface.
// One viewport owns long question and option content; action hints stay pinned.
type PlanQuestionModel struct {
	request      api.QuestionRequest
	question     int
	option       int
	answers      map[string]string
	customAnswer bool
	input        textinput.Model
	body         viewport.Model
	optionLines  []lineRange
	styles       rendering.Styles
	width        int
	height       int
	boxWidth     int
}

// NewPlanQuestionModel creates a new PlanQuestion sub-model.
func NewPlanQuestionModel(request api.QuestionRequest, st rendering.Styles, width int) *PlanQuestionModel {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Type custom answer…"
	input.SetWidth(56)
	styles := input.Styles()
	styles.Focused.Prompt = st.Accent
	styles.Focused.Text = st.Text
	styles.Focused.Placeholder = st.Muted
	styles.Cursor.Blink = true
	if !st.Ascii {
		styles.Cursor.Color = st.Accent.GetForeground()
	} else {
		styles.Cursor.Color = nil
	}
	input.SetStyles(styles)

	body := viewport.New(viewport.WithWidth(max(20, width-12)), viewport.WithHeight(8))
	body.FillHeight = false
	body.SoftWrap = false
	body.MouseWheelEnabled = true
	body.MouseWheelDelta = 2

	m := &PlanQuestionModel{
		request: request,
		answers: make(map[string]string),
		input:   input,
		body:    body,
		styles:  st,
	}
	m.resize(width, 24)
	return m
}

// Request returns the underlying question request.
func (m *PlanQuestionModel) Request() *api.QuestionRequest { return &m.request }

// Question returns the active question index.
func (m *PlanQuestionModel) Question() int { return m.question }

// SetQuestion sets the active question index and resets its reading position.
func (m *PlanQuestionModel) SetQuestion(idx int) {
	if idx < 0 || idx >= len(m.request.Questions) {
		return
	}
	m.question, m.option = idx, 0
	m.body.GotoTop()
	m.refreshBody()
	m.ensureOptionVisible()
}

// Option returns the currently focused option index.
func (m *PlanQuestionModel) Option() int { return m.option }

// SetOption sets the focused option index and reveals it in the viewport.
func (m *PlanQuestionModel) SetOption(idx int) {
	if len(m.request.Questions) == 0 {
		return
	}
	options := m.request.Questions[m.question].Options
	if len(options) == 0 {
		m.option = 0
		return
	}
	m.option = min(max(0, idx), len(options)-1)
	m.refreshBody()
	m.ensureOptionVisible()
}

// Answers returns the recorded answers map.
func (m *PlanQuestionModel) Answers() map[string]string { return m.answers }

// SetAnswer records an answer for a specific question ID.
func (m *PlanQuestionModel) SetAnswer(questionID, answer string) {
	if m.answers == nil {
		m.answers = make(map[string]string)
	}
	m.answers[questionID] = answer
}

// IsComplete returns true if all questions in the request have been answered.
func (m *PlanQuestionModel) IsComplete() bool {
	if len(m.request.Questions) == 0 {
		return true
	}
	for _, question := range m.request.Questions {
		if m.answers[question.ID] == "" {
			return false
		}
	}
	return true
}

// CustomAnswer returns whether custom answer input is active.
func (m *PlanQuestionModel) CustomAnswer() bool { return m.customAnswer }

func (m *PlanQuestionModel) advanceQuestion() tea.Cmd {
	if m.IsComplete() {
		answers := m.answers
		return func() tea.Msg { return PlanDecisionCompletedMsg{Answers: answers} }
	}
	if m.question < len(m.request.Questions)-1 {
		m.SetQuestion(m.question + 1)
	}
	return nil
}

// Update handles keyboard and mouse navigation for Plan Mode decisions.
func (m *PlanQuestionModel) Update(msg tea.Msg) (*PlanQuestionModel, tea.Cmd) {
	if len(m.request.Questions) == 0 {
		return m, nil
	}
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		var cmd tea.Cmd
		m.body, cmd = m.body.Update(msg)
		return m, cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	question := m.request.Questions[m.question]
	keyStr := strings.ToLower(keyMsg.String())

	if m.customAnswer {
		switch keyStr {
		case "esc":
			m.customAnswer = false
			m.input.Reset()
			m.input.Blur()
			return m, nil
		case "enter":
			answer := strings.TrimSpace(m.input.Value())
			if answer == "" {
				return m, nil
			}
			m.answers[question.ID] = answer
			m.customAnswer = false
			m.input.Reset()
			m.input.Blur()
			return m, m.advanceQuestion()
		case "pgup":
			m.body.PageUp()
			return m, nil
		case "pgdown":
			m.body.PageDown()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		idx := int(keyStr[0] - '1')
		if idx < len(question.Options) {
			m.SetOption(idx)
			m.answers[question.ID] = question.Options[idx].Label
			return m, m.advanceQuestion()
		}
	}

	switch keyStr {
	case "up", "left", "k":
		m.SetOption(m.option - 1)
		return m, nil
	case "down", "right", "j":
		m.SetOption(m.option + 1)
		return m, nil
	case "pgup":
		m.body.PageUp()
		return m, nil
	case "pgdown":
		m.body.PageDown()
		return m, nil
	case "home":
		m.SetOption(0)
		return m, nil
	case "end":
		m.SetOption(len(question.Options) - 1)
		return m, nil
	case "r":
		for i, option := range question.Options {
			if strings.Contains(strings.ToLower(option.Description), "recommended") {
				m.SetOption(i)
				m.answers[question.ID] = option.Label
				return m, m.advanceQuestion()
			}
		}
	case "y":
		for i, option := range question.Options {
			label := strings.ToLower(option.Label)
			if strings.HasPrefix(label, "yes") || strings.HasPrefix(label, "keep") || strings.HasPrefix(label, "use") {
				m.SetOption(i)
				m.answers[question.ID] = option.Label
				return m, m.advanceQuestion()
			}
		}
	case "n":
		for i, option := range question.Options {
			label := strings.ToLower(option.Label)
			if strings.HasPrefix(label, "no") || strings.HasPrefix(label, "skip") || strings.HasPrefix(label, "switch") {
				m.SetOption(i)
				m.answers[question.ID] = option.Label
				return m, m.advanceQuestion()
			}
		}
	case "c":
		m.customAnswer = true
		m.input.Reset()
		return m, m.input.Focus()
	case "space":
		if m.option >= 0 && m.option < len(question.Options) {
			m.answers[question.ID] = question.Options[m.option].Label
			m.refreshBody()
		}
		return m, nil
	case "enter":
		if m.option >= 0 && m.option < len(question.Options) {
			m.answers[question.ID] = question.Options[m.option].Label
		}
		return m, m.advanceQuestion()
	case "esc":
		return m, func() tea.Msg { return PlanDecisionCancelledMsg{} }
	}
	return m, nil
}

// View renders a centered question card bounded by the available body size.
func (m *PlanQuestionModel) View(width, height int) string {
	if len(m.request.Questions) == 0 {
		return ""
	}
	m.resize(width, height)
	total := len(m.request.Questions)
	badge := m.styles.PlanQuestion.Render(fmt.Sprintf("PLAN DECISION [%d/%d]", m.question+1, total))
	footer := m.footerView()
	content := strings.Join([]string{badge, m.body.View(), footer}, "\n\n")
	return components.Card(m.styles.PlanModal, m.boxWidth, "", content)
}

func (m *PlanQuestionModel) resize(width, height int) {
	boxWidth := min(max(36, width-8), 76)
	innerWidth := max(16, boxWidth-m.styles.PlanModal.GetHorizontalFrameSize())
	availableHeight := max(1, height)
	sizeChanged := boxWidth != m.boxWidth || innerWidth != m.width || availableHeight != m.height
	m.boxWidth, m.width, m.height = boxWidth, innerWidth, availableHeight
	m.body.SetWidth(m.width)
	m.input.SetWidth(m.width)
	m.refreshBody()

	footerHeight := lipgloss.Height(m.footerView())
	available := max(1, m.height-m.styles.PlanModal.GetVerticalFrameSize()-1-footerHeight-2)
	m.body.SetHeight(min(max(1, m.body.TotalLineCount()), available))
	if sizeChanged {
		m.ensureOptionVisible()
	}
}

func (m *PlanQuestionModel) footerView() string {
	if m.customAnswer {
		m.input.SetWidth(max(12, m.width))
		return m.styles.Muted.Width(m.width).Render("Custom answer: · Enter submit · Esc cancel") + "\n" + m.input.View()
	}
	return m.styles.Muted.Width(m.width).Render("c custom · ↑↓ select · PgUp/PgDn scroll · Enter confirm · Esc cancel")
}

func (m *PlanQuestionModel) refreshBody() {
	if len(m.request.Questions) == 0 || m.width <= 0 {
		return
	}
	question := m.request.Questions[m.question]
	lines := make([]string, 0, 8+len(question.Options)*3)
	appendBlock := func(block string) lineRange {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		start := len(lines)
		lines = append(lines, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
		return lineRange{start: start, end: max(start, len(lines)-1)}
	}

	appendBlock(m.styles.PlanTitle.Width(m.width).Render(question.Question))
	if question.Detail != "" {
		appendBlock(m.styles.PlanSubtitle.Width(m.width).Render(question.Detail))
	}
	m.optionLines = make([]lineRange, 0, len(question.Options))
	for index, option := range question.Options {
		indicator := "  "
		if index == m.option {
			indicator = "● "
			if m.styles.Ascii {
				indicator = "> "
			}
		} else if m.answers[question.ID] == option.Label {
			indicator = "✓ "
			if m.styles.Ascii {
				indicator = "* "
			}
		}
		label := fmt.Sprintf("%s%d. %s", indicator, index+1, option.Label)
		if strings.Contains(strings.ToLower(option.Description), "recommended") {
			label += "  " + m.styles.PlanOptionRecommended.Render("RECOMMENDED")
		}
		var row string
		if index == m.option {
			row = m.styles.PlanOptionSelected.Width(m.width).Render(label)
		} else {
			row = m.styles.PlanOptionKey.Render(fmt.Sprintf("%s%d. ", indicator, index+1)) + m.styles.Text.Render(option.Label)
		}
		if option.Description != "" {
			description := m.styles.PlanTradeoff.Width(max(8, m.width-4)).Render(option.Description)
			row += "\n    " + strings.ReplaceAll(description, "\n", "\n    ")
		}
		row = zone.Mark(fmt.Sprintf("plan-option-%d", index), row)
		m.optionLines = append(m.optionLines, appendBlock(row))
	}
	m.body.SetContent(strings.Join(lines, "\n"))
}

func (m *PlanQuestionModel) ensureOptionVisible() {
	if m.option < 0 || m.option >= len(m.optionLines) || m.body.Height() <= 0 {
		return
	}
	visible := m.optionLines[m.option]
	offset := m.body.YOffset()
	if visible.end-visible.start+1 >= m.body.Height() {
		m.body.SetYOffset(visible.start)
	} else if visible.start < offset {
		m.body.SetYOffset(visible.start)
	} else if visible.end >= offset+m.body.Height() {
		m.body.SetYOffset(max(0, visible.end-m.body.Height()+1))
	}
}
