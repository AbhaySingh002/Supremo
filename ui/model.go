package ui

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/commands"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/git_tools"
)

type entryKind int

const (
	entryUser entryKind = iota
	entryCommand
	entryStatus
	entryTool
	entryAssistant
	entryStreaming
	entryError
	entryDebug
)

type transcriptEntry struct {
	kind     entryKind
	content  string
	rendered string
	details  string
	expanded bool
}

type taskKind int

const (
	taskAgent taskKind = iota
	taskCommand
)

type activeTask struct {
	id     int
	cancel context.CancelFunc
	kind   taskKind
}

type approvalState struct {
	tool      string
	arguments string
	deciding  bool
}

type workspaceState struct {
	branch  string
	changed int
	ready   bool
	err     string
}

type textSelection struct {
	startX, startY int
	endX, endY     int
	anchor, head   int
	inputLeft      int
	input          bool
	dragging       bool
	copied         bool
}

func (s *textSelection) active() bool {
	if s == nil {
		return false
	}
	if s.input {
		return s.anchor != s.head
	}
	return s.startX != s.endX || s.startY != s.endY
}

type keyMap struct {
	submit          key.Binding
	newline         key.Binding
	complete        key.Binding
	togglePanel     key.Binding
	toggleDebug     key.Binding
	focusTranscript key.Binding
	focusInput      key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		submit:          key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		newline:         key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "newline")),
		complete:        key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete command")),
		togglePanel:     key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "plan panel")),
		toggleDebug:     key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "debug")),
		focusTranscript: key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "transcript")),
		focusInput:      key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "input")),
	}
}

type commandItem struct{ command commands.Command }

func (i commandItem) Title() string       { return i.command.Name }
func (i commandItem) Description() string { return i.command.Description }
func (i commandItem) FilterValue() string { return i.command.Name + " " + i.command.Description }

// Model is Supremo's root Bubble Tea model.
type Model struct {
	application *app.App
	registry    *commands.Registry
	ctx         context.Context
	shutdown    context.CancelFunc
	bridge      *eventBridge
	workspace   string

	session         agent.Session
	provider        string
	modelName       string
	credentialReady bool
	debug           bool

	width        int
	height       int
	input        textarea.Model
	inputOffset  int
	inputHistory []string
	historyIndex int
	historyDraft string
	selection    *textSelection
	feed         viewport.Model
	feedPadding  int
	palette      list.Model
	spinner      spinner.Model
	initialFocus tea.Cmd
	keys         keyMap
	styles       styles

	entries        []transcriptEntry
	streamingEntry int
	plan           *agent.Plan
	phase          string
	activity       []tools.Event
	approval       *approvalState
	active         *activeTask
	nextTaskID     int
	paletteOpen    bool
	showSidebar    bool
	showHelp       bool
	showDebug      bool
	focusFeed      bool
	workspaceInfo  workspaceState
	liveEntry      int
	heroAction     int
	heroStatus     bool

	pulse         float64
	pulseVelocity float64
	pulseTicks    int
	pulseEnabled  bool
	spring        harmonica.Spring
	markdownRun   int
}

// New creates the root model. The supplied session is copied before worker use.
func New(application *app.App, session *agent.Session, ctx context.Context, shutdown context.CancelFunc) Model {
	workspace, _ := os.Getwd()
	if session == nil {
		session = &agent.Session{ID: "cli-session"}
	}
	if session.ApprovalMode == "" {
		session.ApprovalMode = tools.ApprovalStrict
	}
	styles := newStyles()
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = "Try \"explain this workspace\" or type / for commands"
	input.ShowLineNumbers = false
	input.SetWidth(78)
	input.SetHeight(1)
	input.MaxHeight = 0
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "newline"))
	input.FocusedStyle.Base = styles.composerBase
	input.FocusedStyle.Prompt = styles.accent
	input.FocusedStyle.Text = styles.text
	input.FocusedStyle.Placeholder = styles.muted
	input.BlurredStyle.Base = styles.composerBase
	input.BlurredStyle.Text = styles.text
	input.BlurredStyle.Placeholder = styles.muted

	registry := commands.NewRegistry()
	items := commandItems(registry)
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.NormalTitle = styles.commandItem
	delegate.Styles.NormalDesc = styles.commandDescription
	delegate.Styles.SelectedTitle = styles.commandSelected
	delegate.Styles.SelectedDesc = styles.commandSelectedDescription
	delegate.Styles.DimmedTitle = styles.commandItem
	delegate.Styles.DimmedDesc = styles.commandDescription
	palette := list.New(items, delegate, 64, 8)
	palette.Title = "Commands"
	palette.SetFilteringEnabled(false)
	palette.SetShowStatusBar(false)
	palette.SetShowPagination(false)
	palette.SetShowHelp(false)
	palette.Styles.TitleBar = styles.paletteTitleBar
	palette.Styles.Title = styles.paletteTitle
	palette.Styles.NoItems = styles.muted

	m := Model{
		application:    application,
		registry:       registry,
		ctx:            ctx,
		shutdown:       shutdown,
		workspace:      workspace,
		session:        *session,
		input:          input,
		feed:           viewport.New(80, 12),
		palette:        palette,
		spinner:        spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(styles.accent)),
		keys:           newKeyMap(),
		styles:         styles,
		showSidebar:    false,
		streamingEntry: -1,
		liveEntry:      -1,
		pulseEnabled:   os.Getenv("NO_COLOR") == "" && lipgloss.ColorProfile() != termenv.Ascii,
		spring:         harmonica.NewSpring(harmonica.FPS(30), 8, 1),
	}
	m.initialFocus = m.input.Focus()
	m.bridge = newEventBridge(ctx, progressQueueCapacity)
	if application != nil && application.Agent != nil {
		application.Agent.SetProgress(m.bridge.publish)
		m.debug = application.Agent.Debug()
	}
	m.refreshProvider()
	m.layout()
	return m
}

func commandItems(registry *commands.Registry) []list.Item {
	available := registry.List()
	items := make([]list.Item, 0, len(available))
	for _, command := range available {
		items = append(items, commandItem{command: command})
	}
	return items
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.initialFocus, m.bridge.wait(), workspaceStatusCmd(m.ctx, m.workspace), loadPlanCmd(m.session, m.workspace))
}

func (m *Model) refreshProvider() {
	if m.application == nil || m.application.ProviderManager == nil || m.application.ProviderManager.GetRuntimeConfig() == nil {
		m.provider, m.modelName, m.credentialReady = "unconfigured", "", false
		return
	}
	runtime := m.application.ProviderManager.GetRuntimeConfig()
	m.provider, m.modelName, _, _, _ = runtime.Get()
	m.credentialReady = runtime.CredentialConfigured()
}

func (m *Model) layout() {
	m.width = max(1, m.width)
	m.height = max(1, m.height)
	m.input.SetWidth(max(1, m.width-3))
	m.input.SetHeight(min(4, composerRows(m.input)))
	m.realignComposer()
	paletteHeight := 0
	if m.paletteOpen {
		paletteHeight = min(8, max(3, m.height/3))
	}
	inputHeight := m.input.Height() + 3
	bodyHeight := max(1, m.height-3-inputHeight-paletteHeight)
	m.feed.Width = m.width
	m.feed.Height = bodyHeight
	m.palette.SetSize(max(20, min(72, m.width-4)), paletteHeight)
	m.rebuildFeed()
}

func (m *Model) resetComposer() {
	m.input.Reset()
	m.inputOffset = 0
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.selection = nil
	m.input.SetHeight(1)
	m.layout()
}

func (m *Model) resizeComposer() {
	rows, cursorRow := composerMetrics(m.input)
	if height := min(4, rows); height != m.input.Height() {
		m.layout()
		return
	}
	needsRealign := m.inputOffset > max(0, rows-m.input.Height())
	m.syncComposerOffset(rows, cursorRow)
	if needsRealign {
		m.realignComposer()
	}
}

func composerRows(input textarea.Model) int {
	rows, _ := composerMetrics(input)
	return rows
}

func composerMetrics(input textarea.Model) (rows, cursorRow int) {
	targetLine, targetWrap := input.Line(), input.LineInfo().RowOffset
	probe := textarea.New()
	probe.Prompt = ""
	probe.ShowLineNumbers = false
	probe.SetWidth(input.Width())
	for line, value := range strings.Split(input.Value(), "\n") {
		probe.SetValue(value)
		if line == targetLine {
			cursorRow = rows + targetWrap
		}
		rows += probe.LineInfo().Height
	}
	return rows, cursorRow
}

func moveComposerCursor(input *textarea.Model, line int) {
	for input.Line() > line {
		input.CursorStart()
		input.CursorUp()
	}
	for input.Line() < line {
		input.CursorEnd()
		input.CursorDown()
	}
}

func (m *Model) realignComposer() {
	focused := m.input.Focused()
	if !focused {
		m.input.Focus()
	}
	line := m.input.Line()
	_, cursorRow := composerMetrics(m.input)
	cursorColumn := m.input.LineInfo().StartColumn + m.input.LineInfo().ColumnOffset
	m.input.View()
	moveComposerCursor(&m.input, 0)
	m.input.CursorStart()
	m.input, _ = m.input.Update(nil)
	moveComposerCursor(&m.input, line)
	m.input.SetCursor(cursorColumn)
	m.input, _ = m.input.Update(nil)
	m.inputOffset = max(0, cursorRow-m.input.Height()+1)
	if !focused {
		m.input.Blur()
	}
}

func (m *Model) syncComposerOffset(rows, cursorRow int) {
	m.inputOffset = min(m.inputOffset, max(0, rows-m.input.Height()))
	if cursorRow < m.inputOffset {
		m.inputOffset = cursorRow
	} else if cursorRow >= m.inputOffset+m.input.Height() {
		m.inputOffset = cursorRow - m.input.Height() + 1
	}
}

func (m *Model) appendEntry(kind entryKind, content string) {
	content = safeText(content)
	m.entries = append(m.entries, transcriptEntry{kind: kind, content: content})
	m.rebuildFeed()
}

func (m *Model) setStatus(content string) {
	m.heroStatus = false
	content = safeText(content)
	if len(m.entries) > 0 && m.entries[len(m.entries)-1].kind == entryStatus {
		m.entries[len(m.entries)-1].content = content
		m.liveEntry = -1
		if m.active != nil {
			m.liveEntry = len(m.entries) - 1
		}
		m.rebuildFeed()
		return
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryStatus, content: content})
	m.liveEntry = -1
	if m.active != nil {
		m.liveEntry = len(m.entries) - 1
	}
	m.rebuildFeed()
}

func (m *Model) setHeroStatus() tea.Cmd {
	m.setStatus(heroActions[m.heroAction%len(heroActions)] + "…")
	m.heroStatus = true
	m.spinner = spinner.New(
		spinner.WithSpinner(heroSpinners[m.heroAction%len(heroSpinners)]),
		spinner.WithStyle(m.styles.accent),
	)
	return m.spinner.Tick
}

func randomHeroAction(current int) int {
	for attempts := 0; attempts < len(heroActions)*2; attempts++ {
		next := rand.IntN(len(heroActions))
		if next != current && next%len(heroSpinners) != current%len(heroSpinners) {
			return next
		}
	}
	return (current + 1) % len(heroActions)
}

var heroActions = []string{
	"Lasering", "Flying", "Helping", "Saving", "Punching", "Leaping", "Lifting", "Racing", "Shielding", "Rescuing", "Defending", "Protecting", "Smashing", "Dodging", "Striking", "Carrying", "Hovering", "Scanning", "Landing", "Soaring", "Diving", "Bursting", "Charging", "Grappling", "Overpowering", "Outrunning", "Withstanding", "Sacrificing", "Inspiring", "Leading", "Overcoming", "Crushing", "Hurling", "Tossing", "Navigating", "Traversing", "Pursuing", "Intercepting", "Fortifying", "Restoring", "Healing", "Rebuilding", "Championing", "Safeguarding",
}

// heroSpinners rotate with the current action so active work remains visibly live.
var heroSpinners = []spinner.Spinner{
	spinner.MiniDot,
	spinner.Line,
	spinner.Dot,
	spinner.Jump,
	spinner.Pulse,
	spinner.Points,
	spinner.Meter,
	spinner.Hamburger,
}

func (m *Model) clearHeroStatus() {
	if !m.heroStatus || m.liveEntry < 0 || m.liveEntry >= len(m.entries) || m.entries[m.liveEntry].kind != entryStatus {
		m.liveEntry = -1
		m.heroStatus = false
		return
	}
	index := m.liveEntry
	m.entries = append(m.entries[:index], m.entries[index+1:]...)
	if m.streamingEntry > index {
		m.streamingEntry--
	}
	m.liveEntry = -1
	m.heroStatus = false
	m.rebuildFeed()
}

func (m *Model) rebuildFeed() {
	atBottom := m.feed.AtBottom()
	var out strings.Builder
	for index, entry := range m.entries {
		if entry.kind == entryDebug && !m.showDebug {
			continue
		}
		out.WriteString(m.renderEntry(index, entry))
		out.WriteString("\n\n")
	}
	content := strings.TrimSpace(out.String())
	m.feedPadding = max(0, m.feed.Height-lipgloss.Height(content))
	m.feed.SetContent(strings.Repeat("\n", m.feedPadding) + content)
	if atBottom {
		m.feed.GotoBottom()
	}
}

func (m Model) renderEntry(index int, entry transcriptEntry) string {
	live := m.active != nil && index == m.liveEntry
	switch entry.kind {
	case entryUser:
		return m.styles.user.Render("› You") + "\n" + entry.content
	case entryCommand:
		return m.styles.command.Render("› " + entry.content)
	case entryStatus:
		marker := "•"
		if live {
			marker = m.spinner.View()
		}
		return m.styles.status.Render(marker + " " + entry.content)
	case entryTool:
		marker := "Tool ·"
		if live {
			marker = m.spinner.View() + " Tool ·"
		}
		line := m.styles.tool.Render(marker + " " + entry.content)
		if entry.expanded && entry.details != "" {
			return line + "\n" + m.styles.muted.Render("  "+strings.ReplaceAll(entry.details, "\n", "\n  "))
		}
		return line
	case entryAssistant:
		label := m.styles.accent.Render("● Supremo")
		if entry.rendered != "" {
			return label + "\n" + entry.rendered
		}
		return label + "\n" + m.styles.text.Render(entry.content)
	case entryStreaming:
		return m.styles.accent.Render("● Supremo") + "\n" + m.styles.text.Render(entry.content)
	case entryError:
		return m.styles.error.Render("✕ " + entry.content)
	case entryDebug:
		return m.styles.debug.Render("debug · " + entry.content)
	default:
		return entry.content
	}
}

func (m *Model) updateStreaming(content string) {
	content = safeText(content)
	if m.streamingEntry >= 0 && m.streamingEntry < len(m.entries) {
		m.entries[m.streamingEntry].content = content
		m.rebuildFeed()
		return
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryStreaming, content: content})
	m.streamingEntry = len(m.entries) - 1
	m.rebuildFeed()
}

func (m *Model) finishStreaming(kind entryKind, content string) bool {
	if m.streamingEntry < 0 || m.streamingEntry >= len(m.entries) {
		return false
	}
	if content == "" {
		m.entries = append(m.entries[:m.streamingEntry], m.entries[m.streamingEntry+1:]...)
	} else {
		m.entries[m.streamingEntry] = transcriptEntry{kind: kind, content: safeText(content)}
	}
	m.streamingEntry = -1
	m.rebuildFeed()
	return true
}

func (m *Model) startTask(input string, resume bool) tea.Cmd {
	if m.application != nil && !m.credentialReady {
		m.paletteOpen = false
		m.resetComposer()
		if resume {
			m.appendEntry(entryCommand, "/plan resume")
		} else {
			m.appendEntry(entryUser, input)
		}
		m.appendEntry(entryError, "A valid API key is required. Run /auth <key> to set one.")
		return nil
	}
	m.nextTaskID++
	id := m.nextTaskID
	ctx, cancel := context.WithCancel(m.ctx)
	m.active = &activeTask{id: id, cancel: cancel, kind: taskAgent}
	m.approval = nil
	m.heroAction, m.heroStatus = rand.IntN(len(heroActions)), false
	m.paletteOpen = false
	m.resetComposer()
	if resume {
		m.appendEntry(entryCommand, "/plan resume")
	} else {
		m.appendEntry(entryUser, input)
	}
	snapshot := m.session
	return tea.Batch(runAgentCmd(ctx, m.application, snapshot, input, resume, id), heroStatusCmd(id))
}

func (m *Model) startCommand(input string) tea.Cmd {
	m.nextTaskID++
	id := m.nextTaskID
	ctx, cancel := context.WithCancel(m.ctx)
	m.active = &activeTask{id: id, cancel: cancel, kind: taskCommand}
	m.paletteOpen = false
	m.resetComposer()
	m.appendEntry(entryCommand, displayCommand(input))
	return runCommandCmd(ctx, m.registry, m.application, m.session, m.workspace, input, id)
}

func runAgentCmd(ctx context.Context, application *app.App, session agent.Session, input string, resume bool, id int) tea.Cmd {
	return func() tea.Msg {
		if application == nil || application.Agent == nil {
			return taskResultMsg{id: id, session: session, err: errors.New("agent is unavailable")}
		}
		var response string
		var err error
		if resume {
			response, err = application.Agent.ResumePlan(ctx, &session)
		} else {
			response, err = application.Agent.Run(ctx, &session, input)
		}
		return taskResultMsg{id: id, session: session, response: response, err: err}
	}
}

func runCommandCmd(ctx context.Context, registry *commands.Registry, application *app.App, session agent.Session, workspace, input string, id int) tea.Cmd {
	return func() tea.Msg {
		output, _, err := registry.Handle(ctx, application, &session, input)
		var plan *agent.Plan
		if session.CurrentPlanID != "" {
			plan, _ = session.ActivePlan(workspace)
		}
		return commandResultMsg{id: id, input: input, session: session, output: output, plan: plan, err: err}
	}
}

func approvalCmd(application *app.App, deny bool, reason string) tea.Cmd {
	return func() tea.Msg {
		if application == nil || application.Agent == nil {
			return approvalResultMsg{err: errors.New("agent is unavailable")}
		}
		if deny {
			return approvalResultMsg{resolved: application.Agent.DenyPendingTool(reason)}
		}
		return approvalResultMsg{resolved: application.Agent.ApprovePendingTool()}
	}
}

func loadPlanCmd(session agent.Session, workspace string) tea.Cmd {
	return func() tea.Msg {
		plan, err := session.ActivePlan(workspace)
		return planLoadedMsg{plan: plan, err: err}
	}
}

func workspaceStatusCmd(ctx context.Context, workspace string) tea.Cmd {
	return func() tea.Msg {
		result, err := (&git_tools.GitStatus{}).Execute(tools.WithWorkspace(ctx, workspace), git_tools.GitStatusInput{Directory: "."})
		if err != nil {
			return workspaceStatusMsg{err: err}
		}
		if result == nil || !result.Success {
			return workspaceStatusMsg{err: errors.New("not a git workspace")}
		}
		data, err := json.Marshal(result.Data)
		if err != nil {
			return workspaceStatusMsg{err: err}
		}
		var status git_tools.GitStatusOutput
		if err := json.Unmarshal(data, &status); err != nil {
			return workspaceStatusMsg{err: err}
		}
		return workspaceStatusMsg{info: workspaceState{branch: status.Branch, changed: len(status.Staged) + len(status.Modified) + len(status.Untracked), ready: true}}
	}
}

func markdownCmd(run, width int, entries []transcriptEntry) tea.Cmd {
	return func() tea.Msg {
		rendered := make(map[int]string)
		renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(glamourstyles.DarkStyle), glamour.WithColorProfile(lipgloss.ColorProfile()), glamour.WithWordWrap(min(88, max(20, width-4))))
		if err != nil {
			return markdownRenderedMsg{run: run, rendered: rendered}
		}
		for i, entry := range entries {
			if entry.kind != entryAssistant {
				continue
			}
			value, err := renderer.Render(entry.content)
			if err == nil {
				rendered[i] = strings.TrimSpace(value)
			}
		}
		return markdownRenderedMsg{run: run, rendered: rendered}
	}
}

func (m *Model) renderMarkdown() tea.Cmd {
	m.markdownRun++
	entries := append([]transcriptEntry(nil), m.entries...)
	return markdownCmd(m.markdownRun, m.feed.Width, entries)
}

func (m *Model) startPulse() tea.Cmd {
	if !m.pulseEnabled {
		return nil
	}
	m.pulse, m.pulseVelocity, m.pulseTicks = 0, 0, 0
	return pulseCmd()
}

func pulseCmd() tea.Cmd {
	return tea.Tick(time.Second/30, func(time.Time) tea.Msg { return pulseMsg{} })
}

func heroStatusCmd(taskID int) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return heroStatusMsg{taskID: taskID} })
}

func commandIs(input, name string) bool {
	parts := strings.Fields(input)
	return len(parts) > 0 && parts[0] == name
}

func planResume(input string) bool {
	parts := strings.Fields(input)
	return len(parts) == 2 && parts[0] == "/plan" && parts[1] == "resume"
}

func displayCommand(input string) string {
	if commandIs(input, "/auth") {
		return "/auth ••••••••"
	}
	return input
}

func safeText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, value)
	return truncate(value, 12_000)
}

func truncate(value string, limit int) string {
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

func conciseToolLabel(tool, status, arguments string) string {
	path := filepath.Base(toolArgument(arguments, "path"))
	if path == "." {
		path = ""
	}
	switch tool {
	case "read_file":
		return withTarget("Reading", path)
	case "write_file":
		return withTarget("Updating", path)
	case "create_file":
		return withTarget("Creating", path)
	case "delete_file":
		return withTarget("Removing", path)
	case "rename_file":
		return "Renaming files…"
	case "create_directory":
		return "Creating folder…"
	case "list_directory", "search_file_name":
		return "Scanning files…"
	case "search_text", "find_references", "find_symbol":
		return "Searching code…"
	case "git_status", "git_diff", "git_log":
		return "Checking changes…"
	case "run_tests":
		return "Testing…"
	case "run_build":
		return "Building…"
	case "run_formatter":
		return "Formatting…"
	case "web_fetch":
		return "Fetching reference…"
	case "execute_command":
		return conciseCommandLabel(toolArgument(arguments, "command"), toolArguments(arguments, "args"), status)
	default:
		return "Working…"
	}
}

func withTarget(action, target string) string {
	if target == "" {
		return action + "…"
	}
	return action + " " + target + "…"
}

func conciseCommandLabel(command string, args []string, status string) string {
	command = strings.ToLower(command)
	joined := strings.ToLower(strings.Join(args, " "))
	switch {
	case command == "pwd":
		return "Checking workspace…"
	case command == "go" && strings.HasPrefix(joined, "test"):
		return "Testing…"
	case command == "go" && strings.HasPrefix(joined, "build"):
		return "Building…"
	case command == "git":
		return "Checking Git…"
	case command == "ls" || command == "find":
		return "Scanning files…"
	case command == "rg" || command == "grep":
		return "Searching code…"
	case status == "failed":
		return "Command failed"
	default:
		return "Running command…"
	}
}

func toolArgument(arguments, name string) string {
	values := toolArguments(arguments, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func toolArguments(arguments, name string) []string {
	start := strings.Index(arguments, "{")
	if start < 0 {
		return nil
	}
	var data map[string]any
	if json.Unmarshal([]byte(arguments[start:]), &data) != nil {
		return nil
	}
	value, ok := data[name]
	if !ok {
		return nil
	}
	if text, ok := value.(string); ok {
		return []string{text}
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
