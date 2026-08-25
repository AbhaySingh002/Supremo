package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/commands"
	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/composer"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
	"github.com/AbhaySingh002/supremo/internal/ui/terminal"
	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

type entryKind int

const (
	entryUser entryKind = iota
	entryCommand
	entryStatus
	entryTool
	entryDiff
	entryAssistant
	entryStreaming
	entryError
	entryDebug
	entryTodos
)

type transcriptEntry struct {
	kind          entryKind
	content       string
	tool          string
	toolStatus    string
	rendered      string
	renderedWidth int
	renderedCache string
	dirty         bool
	details       string
	arguments     string
	expanded      bool
	detailOffset  int
	toolCallID    string
	toolBatchID   string
	artifactID    string
}

type taskKind int

const (
	taskAgent taskKind = iota
	taskCommand
	taskShell
)

type activeTask struct {
	id     int
	ctx    context.Context
	cancel context.CancelFunc
	kind   taskKind
	runID  string
}

// InterruptMsg is sent by the CLI signal bridge so terminal signals use the
// same orderly task-cancellation path as Ctrl+C.
type InterruptMsg struct{ Terminate bool }

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
	inputTop       int
	inputBottom    int
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

type surfaceKind int

const (
	surfaceNone surfaceKind = iota
	surfaceSessions
	surfaceDeleteSession
	surfaceRewind
	surfaceSideQuestion
	surfaceKrypton
	surfaceProvider
	surfaceModel
	surfaceCredential
	surfacePlanQuestion
	surfaceApproval
	surfaceDiff
	surfaceHelp
	surfaceActivity
)

type focusTarget int

const (
	focusComposer focusTarget = iota
	focusTranscript
	focusActivity
	focusOverlay
)

type activityEvent struct {
	Time       time.Time
	SessionID  string
	TaskID     string
	Tool       string
	Status     string
	Message    string
	Arguments  string
	Output     string
	Diff       string
	Checkpoint *api.Checkpoint
}

// Options contains frontend-host behavior that cannot travel over the backend
// API. Clipboard, shell execution, and export remain package-local.
type Options struct {
	Context  context.Context
	Shutdown context.CancelFunc
	Purge    func(context.Context) error
	Debug    bool
}

const (
	maxComposerHeight   = 6
	composerPlaceholder = "Message Supremo…"
	planPlaceholder     = "Describe what you want to plan"
	maxVisibleDiffBytes = 64 << 10
)

// chatModel owns the conversation surface and composer state. The root model
// coordinates it with backend messages but does not duplicate its state.
type chatModel struct {
	input                textarea.Model
	inputOffset          int
	inputHistory         []string
	historyIndex         int
	historyDraft         string
	historyQuery         string
	selection            *textSelection
	feed                 viewport.Model
	entries              []transcriptEntry
	streamingEntry       int
	pendingInput         string
	palette              selectors.CommandMenu
	mentionMenu          list.Model
	paletteOpen          bool
	mentionOpen          bool
	mentionQuery         string
	mentionCatalog       []composer.MentionItem
	liveEntry            int
	followTail           bool
	newOutput            int
	markdownRun          int
	historyPrefix        string
	historyPrefixCount   int
	historyPrefixWidth   int
	streamBuffer         strings.Builder
	streamTicking        bool
	streamLastTick       time.Time
	planDraft            bool
	collapsedToolBatches map[string]bool
}

// activityModel owns the derived run, tool, checklist, and subagent rail.
type activityModel struct {
	phase           string
	activity        []activityEvent
	agents          []api.Agent
	runs            []api.Run
	todos           []api.TodoItem
	showActivity    bool
	activityToggled bool
}

// surfaceState routes the single active overlay and restores its prior focus.
type surfaceState struct {
	diffViewport         viewport.Model
	diffEntry            int
	diffRun              int
	workspaceDiff        string
	workspaceDiffSummary string
	overlayList          list.Model
	overlayInput         textinput.Model
	approval             *approval.ApprovalModel
	providerSelector     *selectors.ProviderSelector
	modelSelector        *selectors.ProviderSelector
	credential           *credentialSetup
	surface              surfaceKind
	overlayTarget        *api.Session
	overlayCheckpoint    *api.Checkpoint
	overlayConfirm       bool
	overlayForce         bool
	overlayError         string
	sideAnswer           string
	sideLoading          bool
	focus                focusTarget
	priorFocus           focusTarget
	planQuestion         *plan.PlanQuestionModel
	pendingInteraction   string
}

// Model is Supremo's root Bubble Tea model.
type Model struct {
	chatModel
	activityModel
	surfaceState

	client       api.Client
	registry     *commands.Registry
	ctx          context.Context
	shutdown     context.CancelFunc
	purge        func(context.Context) error
	workspace    string
	subscription api.EventStream
	cursor       int64
	sessionEpoch int
	providers    []api.Provider
	modelCatalog []api.Provider
	catalogBusy  bool
	catalogNote  string

	session         api.Session
	provider        string
	modelName       string
	credentialReady bool
	inputTokens     int
	outputTokens    int
	contextLimit    int
	debug           bool

	width          int
	height         int
	composerTopRow int
	spinner        spinner.Model
	initialFocus   tea.Cmd
	keys           KeyMap
	help           help.Model
	styles         rendering.Styles

	active        *activeTask
	cancelling    bool
	quitWhenIdle  bool
	nextTaskID    int
	showDebug     bool
	workspaceInfo workspaceState
	tokenBar      progress.Model
}

func (m *Model) openProviderSelector() {
	m.paletteOpen = false
	m.modelSelector = nil
	m.surface = surfaceProvider
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	choices := []selectors.Provider{}
	activeType := strings.SplitN(m.provider, ":", 2)[0]
	for _, provider := range m.providers {
		choices = append(choices, selectors.Provider{ID: provider.ID, Name: provider.Name, Description: map[bool]string{true: "configured", false: "needs setup"}[provider.Configured], Active: activeType == provider.ID})
	}
	selector := selectors.NewProviderSelector(choices, theme.Default())
	width, height := m.selectorSize()
	selector.SetSize(width, height)
	m.providerSelector = &selector
	m.layout()
}

func (m Model) providerChoice(id string) (api.Provider, bool) {
	for _, provider := range m.providers {
		if provider.ID == id {
			return provider, true
		}
	}
	base := strings.SplitN(id, ":", 2)[0]
	for _, provider := range m.providers {
		if provider.ID == base {
			provider.ID = id
			provider.Configured = m.credentialReady && id == m.provider
			return provider, true
		}
	}
	return api.Provider{}, false
}

func (m *Model) openCredential(provider api.Provider) tea.Cmd {
	m.paletteOpen = false
	m.providerSelector, m.modelSelector = nil, nil
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.surface = surfaceCredential
	m.input.Blur()
	m.credential = newCredentialSetup(provider, m.styles)
	m.layout()
	if m.credential.step == credentialEndpoint {
		return m.credential.endpoint.Focus()
	}
	return m.credential.key.Focus()
}

func (m *Model) refreshModelCatalog() tea.Cmd {
	m.paletteOpen = false
	m.providerSelector, m.modelSelector = nil, nil
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.surface, m.catalogBusy, m.catalogNote = surfaceModel, true, ""
	m.input.Blur()
	m.layout()
	return tea.Batch(listModelsCmd(m.ctx, m.client, true), m.spinner.Tick)
}

func (m *Model) openModelSelector() bool {
	options := make([]selectors.Provider, 0)
	warnings := 0
	for _, provider := range m.modelCatalog {
		models := append([]api.Model(nil), provider.Models...)
		sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		if provider.MetadataWarning != "" {
			warnings++
		}
		for _, model := range models {
			description := provider.MetadataState
			if model.ContextLength > 0 {
				description += fmt.Sprintf(" · %dk context", model.ContextLength/1000)
			}
			if model.Name != "" && model.Name != model.ID {
				description += " · " + model.Name
			}
			if provider.MetadataWarning != "" {
				description += " · refresh failed; cached"
			}
			options = append(options, selectors.Provider{
				ID: model.ID, ProviderID: provider.ID, Name: provider.Name + "  ·  " + model.ID, Description: description,
				Active: provider.ID == m.provider && model.ID == m.modelName,
			})
		}
	}
	if len(options) == 0 {
		return false
	}
	m.catalogNote = ""
	if warnings > 0 {
		m.catalogNote = fmt.Sprintf("%d provider refresh warning(s); cached models remain available", warnings)
	}
	selector := selectors.NewModelSelector(options, theme.Default())
	width, height := m.selectorSize()
	selector.SetSize(width, height)
	m.paletteOpen = false
	m.providerSelector = nil
	m.modelSelector = &selector
	m.surface = surfaceModel
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	m.layout()
	return true
}

// selectorSize bounds list modals to a readable card instead of letting a
// short list consume the full terminal body.
func (m Model) selectorSize() (width, height int) {
	height = m.feed.Height()
	if height <= 0 {
		height = m.height - 4
	}
	return max(1, min(92, m.width-4)), max(1, min(24, height))
}

// New creates the root API-backed frontend model.
func New(client api.Client, workspace, sessionID string, options Options) Model {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = fmt.Sprintf("ephemeral-%d", time.Now().UnixNano())
	}
	session := api.Session{ID: sessionID, Name: sessionID, ApprovalMode: "batman", Checklist: true, Rewind: true, ProviderRetry: true}
	styles := rendering.NewStyles()
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = composerPlaceholder
	input.ShowLineNumbers = false
	input.SetWidth(78)
	input.SetHeight(1)
	input.SetVirtualCursor(false)
	input.MaxHeight = 0
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+enter", "cmd+enter", "super+enter", "meta+enter", "opt+enter", "option+enter", "esc+enter", "esc+return", "shift+return", "alt+return", "ctrl+return", "cmd+return", "super+return", "meta+return", "opt+return", "option+return", "ctrl+j", "ctrl+o"), key.WithHelp("shift+enter", "newline"))
	inStyles := input.Styles()
	inStyles.Focused.Base = styles.ComposerBase
	inStyles.Focused.Prompt = styles.Accent
	inStyles.Focused.Text = styles.Text
	inStyles.Focused.Placeholder = styles.Muted
	inStyles.Focused.CursorLine = styles.ComposerBase
	inStyles.Cursor.Blink = true
	if !styles.Ascii {
		inStyles.Cursor.Color = styles.Accent.GetForeground()
	} else {
		inStyles.Cursor.Color = nil
	}
	inStyles.Blurred.Base = styles.ComposerBase
	inStyles.Blurred.Text = styles.Text
	inStyles.Blurred.Placeholder = styles.Muted
	input.SetStyles(inStyles)

	registry := commands.NewRegistry()
	commands := componentCommands(registry)
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.NormalTitle = styles.CommandItem
	delegate.Styles.NormalDesc = styles.CommandDescription
	delegate.Styles.SelectedTitle = styles.CommandSelected
	delegate.Styles.DimmedTitle = styles.CommandItem
	delegate.Styles.DimmedDesc = styles.CommandDescription
	palette := selectors.NewCommandMenu(commands, theme.Default())
	mentionListDelegate := composer.NewMentionDelegate(styles.CommandItem, styles.Accent, styles.Accent, styles.Accent)
	mentionMenu := list.New(nil, mentionListDelegate, 64, 10)
	mentionMenu.Title = "Workspace references"
	mentionMenu.SetFilteringEnabled(true)
	mentionMenu.SetShowFilter(false)
	mentionMenu.SetShowStatusBar(false)
	mentionMenu.SetShowPagination(false)
	mentionMenu.SetShowHelp(false)
	mentionMenu.KeyMap.Quit.SetEnabled(false)
	mentionMenu.KeyMap.ForceQuit.SetEnabled(false)
	mentionMenu.Styles.TitleBar = styles.PaletteTitleBar
	mentionMenu.Styles.Title = styles.PaletteTitle
	mentionMenu.Styles.NoItems = styles.Muted
	overlayList := list.New(nil, delegate, 64, 10)
	overlayList.SetShowStatusBar(false)
	overlayList.SetShowPagination(false)
	overlayList.SetShowHelp(false)
	overlayList.Styles.TitleBar = styles.PaletteTitleBar
	overlayList.Styles.Title = styles.PaletteTitle
	overlayList.Styles.NoItems = styles.Muted
	activitySpinner := spinner.MiniDot
	if styles.Ascii {
		activitySpinner = spinner.Line
	}

	h := help.New()
	h.Styles.ShortKey = styles.Accent
	h.Styles.ShortDesc = styles.Muted
	h.Styles.ShortSeparator = styles.Muted
	h.Styles.FullKey = styles.Accent
	h.Styles.FullDesc = styles.Muted
	h.Styles.FullSeparator = styles.Muted

	overlayIn := textinput.New()
	overlayIn.Prompt = "> "
	overlayIn.SetWidth(64)
	overlayStyles := overlayIn.Styles()
	overlayStyles.Focused.Prompt = styles.Accent
	overlayStyles.Focused.Text = styles.Text
	overlayStyles.Focused.Placeholder = styles.Muted
	overlayStyles.Cursor.Blink = true
	if !styles.Ascii {
		overlayStyles.Cursor.Color = styles.Accent.GetForeground()
	} else {
		overlayStyles.Cursor.Color = nil
	}
	overlayIn.SetStyles(overlayStyles)

	tokenBar := progress.New(progress.WithWidth(10), progress.WithoutPercentage(), progress.WithDefaultBlend())
	m := Model{
		chatModel: chatModel{
			input:                input,
			feed:                 viewport.New(viewport.WithWidth(80), viewport.WithHeight(12)),
			palette:              palette,
			mentionMenu:          mentionMenu,
			streamingEntry:       -1,
			liveEntry:            -1,
			followTail:           true,
			collapsedToolBatches: make(map[string]bool),
		},
		activityModel: activityModel{},
		surfaceState: surfaceState{
			diffViewport: viewport.New(viewport.WithWidth(72), viewport.WithHeight(18)),
			diffEntry:    -1,
			overlayList:  overlayList,
			overlayInput: overlayIn,
			focus:        focusComposer,
			priorFocus:   focusComposer,
		},
		client:    client,
		registry:  registry,
		ctx:       ctx,
		shutdown:  options.Shutdown,
		purge:     options.Purge,
		workspace: workspace,
		session:   session,
		spinner:   spinner.New(spinner.WithSpinner(activitySpinner), spinner.WithStyle(styles.Accent)),
		keys:      newKeyMap(),
		help:      h,
		styles:    styles,
		tokenBar:  tokenBar,
		width:     80,
		height:    24,
	}
	m.initialFocus = m.input.Focus()
	m.debug = options.Debug
	m.layout()
	return m
}

func init() {
	zone.NewGlobal()
	zone.SetEnabled(true)
}

func componentCommands(registry *commands.Registry) []selectors.Command {
	available := registry.List()
	items := make([]selectors.Command, 0, len(available))
	for _, command := range available {
		items = append(items, selectors.Command{Name: command.Name, Description: command.Description})
	}
	return items
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.initialFocus, initializeClientCmd(m.ctx, m.client, m.session.ID, m.sessionEpoch), func() tea.Msg { return tea.RequestWindowSize() }}
	return tea.Batch(cmds...)
}

func (m *Model) refreshProvider() {
	if m.provider == "" {
		m.provider = "unconfigured"
	}
}

func (m Model) maxComposerRows() int {
	maxRows := maxComposerHeight
	if m.height > 0 {
		maxRows = min(10, max(3, m.height/4))
	}
	return maxRows
}

func (m *Model) layout() {
	m.width = max(1, m.width)
	m.height = max(1, m.height)
	contentWidth := m.contentWidth()
	m.input.SetWidth(max(1, contentWidth-4))
	m.input.SetHeight(min(m.maxComposerRows(), composerRows(m.input)))
	m.realignComposer()
	paletteHeight, mentionHeight := 0, 0
	paletteFrame := m.styles.Palette.GetVerticalFrameSize()
	if m.paletteOpen && m.surface == surfaceNone {
		paletteHeight = min(10, max(4, m.height/3))
		m.palette.SetSize(max(20, min(72, m.width-4)), max(1, paletteHeight-paletteFrame))
	}
	if m.mentionOpen && m.surface == surfaceNone {
		mentionHeight = min(14, max(5, m.height/2))
		m.mentionMenu.SetSize(max(20, min(72, m.width-4)), max(1, mentionHeight-paletteFrame))
	}
	headerHeight, footerHeight := lipgloss.Height(m.HeaderView()), lipgloss.Height(m.FooterView())
	inputHeight := 0
	if m.surface == surfaceNone {
		inputHeight = lipgloss.Height(m.inputView())
	}
	bodyHeight := max(1, m.height-headerHeight-footerHeight-paletteHeight-mentionHeight-inputHeight)
	m.feed.SetWidth(contentWidth)
	m.feed.SetHeight(bodyHeight)
	m.diffViewport.SetWidth(max(1, min(100, m.width-10)))
	m.diffViewport.SetHeight(max(1, bodyHeight-5))
	m.overlayList.SetSize(max(20, min(72, m.width-4)), min(12, max(4, m.height/2)))
	m.overlayInput.SetWidth(max(20, min(72, m.width-8)))
	m.help.SetWidth(max(1, m.width-2))
	if m.providerSelector != nil {
		width, height := m.selectorSize()
		m.providerSelector.SetSize(width, height)
	}
	if m.modelSelector != nil {
		width, height := m.selectorSize()
		m.modelSelector.SetSize(width, height)
	}
	m.composerTopRow = headerHeight + bodyHeight + paletteHeight + mentionHeight
	m.rebuildFeed()
}

func (m *Model) resetComposer() {
	m.input.Reset()
	m.input.Placeholder = composerPlaceholder
	if m.planDraft {
		m.input.Placeholder = planPlaceholder
	}
	m.closeMentionMenu()
	m.inputOffset = 0
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.historyQuery = ""
	m.selection = nil
	m.input.SetHeight(1)
	m.layout()
}

func (m *Model) setPlanDraft(enabled bool) {
	m.planDraft = enabled
	m.paletteOpen = false
	m.resetComposer()
	if enabled {
		m.setStatus("Plan Mode — describe the change you want to plan.")
		return
	}
	m.setStatus("Plan Mode cancelled.")
}

func (m *Model) resizeComposer() {
	rows, cursorRow := composerMetrics(m.input)
	targetHeight := min(m.maxComposerRows(), max(1, rows))
	if targetHeight != m.input.Height() {
		m.layout()
		return
	}
	m.syncComposerOffset(rows, cursorRow)
}

func (m *Model) scrollComposer(delta int) bool {
	rows, _ := composerMetrics(m.input)
	visible := max(1, m.input.Height())
	if rows <= visible {
		return false
	}
	maxOffset := max(0, rows-visible)
	next := min(maxOffset, max(0, m.inputOffset+delta))
	if next != m.inputOffset {
		m.inputOffset = next
		return true
	}
	return false
}

func composerRows(input textarea.Model) int {
	rows, _ := composerMetrics(input)
	return rows
}

type composerVisualRow = composer.VisualRow

type composerLayoutState struct {
	projection    composer.MentionProjection
	rows          []composerVisualRow
	cursorDisplay int
	scrollRow     int
	visibleRows   int
}

func composerMentionTokens(input textarea.Model) []composer.MentionToken {
	spans := composer.MentionTokens(input.Value())
	if active, ok := composer.ActiveMention(input.Value(), composerCursorOffset(input)); ok {
		kept := spans[:0]
		for _, span := range spans {
			if span.Start != active.Start || span.End != active.End {
				kept = append(kept, span)
			}
		}
		spans = kept
	}
	return spans
}

func composerLayout(input textarea.Model) composerLayoutState {
	projection := composer.ProjectMentions(input.Value(), composerMentionTokens(input))
	width := input.Width()
	if width <= 0 {
		width = 80
	}
	rows := composer.ComputeVisualRows(projection.Runes, width)
	return composerLayoutState{
		projection: projection,
		rows:       rows,
	}
}

func (m Model) composerLayout() composerLayoutState {
	state := composerLayout(m.input)
	cursor := min(composerCursorOffset(m.input), len(state.projection.RawToDisplay)-1)
	display := 0
	if cursor >= 0 && cursor < len(state.projection.RawToDisplay) {
		display = state.projection.RawToDisplay[cursor]
	}
	state.cursorDisplay = display
	state.scrollRow = m.inputOffset
	state.visibleRows = max(1, m.input.Height())
	return state
}

func composerMetrics(input textarea.Model) (rows, cursorRow int) {
	layout := composerLayout(input)
	cursor := min(composerCursorOffset(input), len(layout.projection.RawToDisplay)-1)
	display := 0
	if cursor >= 0 && cursor < len(layout.projection.RawToDisplay) {
		display = layout.projection.RawToDisplay[cursor]
	}
	for row, target := range layout.rows {
		if (display >= target.Start && display < target.End) ||
			(display == target.End && (row == len(layout.rows)-1 || target.Start == target.End || (target.End < len(layout.projection.Runes) && layout.projection.Runes[target.End] == '\n'))) {
			cursorRow = row
		}
	}
	return len(layout.rows), cursorRow
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
	rows, cursorRow := composerMetrics(m.input)
	m.syncComposerOffset(rows, cursorRow)
}

func (m *Model) syncComposerOffset(rows, cursorRow int) {
	m.inputOffset = min(m.inputOffset, max(0, rows-m.input.Height()))
	if cursorRow < m.inputOffset {
		m.inputOffset = cursorRow
	} else if cursorRow >= m.inputOffset+m.input.Height() {
		m.inputOffset = cursorRow - m.input.Height() + 1
	}
}

func (m *Model) moveComposerVisualCursor(delta int) bool {
	if len(composerMentionTokens(m.input)) == 0 {
		return false
	}
	layout := composerLayout(m.input)
	_, current := composerMetrics(m.input)
	target := current + delta
	if target < 0 || target >= len(layout.rows) {
		return false
	}
	cursor := min(composerCursorOffset(m.input), len(layout.projection.RawToDisplay)-1)
	display := layout.projection.RawToDisplay[cursor]
	column := max(0, display-layout.rows[current].Start)
	display = min(layout.rows[target].End, layout.rows[target].Start+column)
	setComposerCursorOffset(&m.input, layout.projection.DisplayToRaw[display])
	m.resizeComposer()
	return true
}

func (m *Model) appendEntry(kind entryKind, content string) {
	content = safeText(content)
	width := m.contentWidth()
	if width <= 0 {
		width = 80
	}
	n := len(m.entries)
	if n > 0 && m.historyPrefix != "" && m.historyPrefixCount == n-1 && m.historyPrefixWidth == width {
		if m.entries[n-1].renderedCache != "" {
			m.historyPrefix += "\n\n" + m.entries[n-1].renderedCache
			m.historyPrefixCount = n
		}
	} else if n == 1 && m.entries[0].renderedCache != "" && m.historyPrefixWidth == width {
		m.historyPrefix = m.entries[0].renderedCache
		m.historyPrefixCount = 1
	} else {
		m.historyPrefix = ""
		m.historyPrefixCount = 0
	}
	m.entries = append(m.entries, transcriptEntry{kind: kind, content: content, dirty: true})
	m.noteOutput()
	m.rebuildFeed()
}

func (m *Model) setStatus(content string) {
	content = safeText(content)
	if len(m.entries) > 0 && m.entries[len(m.entries)-1].kind == entryStatus {
		m.entries[len(m.entries)-1].content = content
		m.entries[len(m.entries)-1].dirty = true
		m.noteOutput()
		m.liveEntry = -1
		if m.active != nil {
			m.liveEntry = len(m.entries) - 1
		}
		m.rebuildFeed()
		return
	}
	m.appendEntry(entryStatus, content)
	if m.active != nil {
		m.liveEntry = len(m.entries) - 1
	}
}

func (m *Model) setTodos(items []api.TodoItem) {
	m.todos = items
	body := components.Todos(items)
	for i := range m.entries {
		if m.entries[i].kind == entryTodos {
			m.entries[i].content = body
			m.entries[i].dirty = true
			m.rebuildFeed()
			return
		}
	}
	if body == "" {
		return
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryTodos, content: body, dirty: true})
	m.noteOutput()
	m.rebuildFeed()
}

func (m *Model) waitForProvider() tea.Cmd {
	if m.planDraft || m.session.PlanModeActive() {
		m.setStatus("Planning…")
	} else {
		m.setStatus("Thinking…")
	}
	return m.spinner.Tick
}

func (m *Model) clearLiveStatus() {
	if m.liveEntry < 0 || m.liveEntry >= len(m.entries) || m.entries[m.liveEntry].kind != entryStatus {
		m.liveEntry = -1
		return
	}
	index := m.liveEntry
	m.entries = append(m.entries[:index], m.entries[index+1:]...)
	if m.streamingEntry > index {
		m.streamingEntry--
	}
	m.liveEntry = -1
	m.historyPrefix = ""
	m.historyPrefixCount = 0
	m.rebuildFeed()
}

func (m *Model) invalidateRenderCache() {
	rendering.ClearGlamourCache()
	m.historyPrefix = ""
	m.historyPrefixCount = 0
	m.historyPrefixWidth = 0
	for i := range m.entries {
		m.entries[i].dirty = true
		m.entries[i].renderedCache = ""
	}
}

func (m Model) contentWidth() int {
	rail := m.activityRailWidth()
	if rail > 0 {
		return max(1, m.width-rail-1)
	}
	return max(1, m.width)
}

func (m Model) transcriptFocused() bool { return m.focus == focusTranscript }

func (m *Model) restoreFocus() tea.Cmd {
	target := m.priorFocus
	if target == focusOverlay || target == focusActivity {
		target = focusComposer
	}
	m.focus = target
	m.layout()
	if target == focusTranscript {
		m.input.Blur()
		return nil
	}
	return m.input.Focus()
}

func (m *Model) restoreComposerAfterWork() tea.Cmd {
	if m.surface != surfaceNone || m.active != nil || m.focus == focusTranscript || m.focus == focusActivity {
		return nil
	}
	m.focus = focusComposer
	return m.input.Focus()
}

func (m *Model) feedWidth() int {
	width := m.contentWidth()
	if width <= 0 {
		return 80
	}
	return width
}

func (m *Model) historyPrefixValid(n, width int) bool {
	return n > 1 &&
		m.historyPrefix != "" &&
		m.historyPrefixCount == n-1 &&
		m.historyPrefixWidth == width
}

func (m *Model) applyLivePrefix(n, width int) {
	pinned := m.followTail && m.feed.AtBottom()
	var out strings.Builder
	out.Grow(len(m.historyPrefix) + 512)
	out.WriteString(m.historyPrefix)
	out.WriteString("\n\n")
	last := m.renderEntry(n-1, m.entries[n-1])
	m.entries[n-1].renderedCache = last
	m.entries[n-1].renderedWidth = width
	m.entries[n-1].dirty = false
	out.WriteString(last)
	m.feed.SetContent(strings.TrimSpace(out.String()))
	if pinned {
		m.feed.GotoBottom()
	}
}

func (m *Model) refreshLiveFeed() {
	n := len(m.entries)
	width := m.feedWidth()
	if m.historyPrefixValid(n, width) {
		m.applyLivePrefix(n, width)
		return
	}
	m.rebuildFeed()
}

func (m *Model) rebuildFeed() {
	pinned := m.followTail && m.feed.AtBottom()
	width := m.feedWidth()

	n := len(m.entries)
	if n == 0 {
		m.feed.SetContent("")
		return
	}

	if m.historyPrefixValid(n, width) {
		m.applyLivePrefix(n, width)
		return
	}

	type feedBlock struct {
		text       string
		start, end int
	}
	blocks := make([]feedBlock, 0, n)
	batchIndices := make(map[string][]int)
	for index, entry := range m.entries {
		if entry.kind == entryTool && entry.toolBatchID != "" {
			batchIndices[entry.toolBatchID] = append(batchIndices[entry.toolBatchID], index)
		}
	}

	for index := 0; index < n; index++ {
		entry := m.entries[index]
		if entry.kind == entryDebug && !m.showDebug {
			continue
		}
		if entry.kind == entryTool && entry.toolBatchID != "" {
			indices := batchIndices[entry.toolBatchID]
			if len(indices) > 1 {
				if indices[0] != index {
					continue
				}
				blocks = append(blocks, feedBlock{text: m.renderToolBatch(indices), start: index, end: indices[len(indices)-1]})
				continue
			}
		}
		rendered := m.renderEntry(index, entry)
		m.entries[index].renderedCache = rendered
		m.entries[index].renderedWidth = width
		m.entries[index].dirty = false
		blocks = append(blocks, feedBlock{text: rendered, start: index, end: index})
	}

	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, block.text)
	}
	content := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if len(blocks) > 1 && blocks[len(blocks)-1].start == n-1 {
		m.historyPrefix = strings.Join(parts[:len(parts)-1], "\n\n")
		m.historyPrefixCount = n - 1
		m.historyPrefixWidth = width
	} else if len(blocks) == 1 && blocks[0].start == 0 && blocks[0].end == 0 {
		m.historyPrefix = content
		m.historyPrefixCount = 1
		m.historyPrefixWidth = width
	} else {
		m.historyPrefix = ""
		m.historyPrefixCount = 0
		m.historyPrefixWidth = width
	}

	m.feed.SetContent(content)
	if pinned {
		m.feed.GotoBottom()
	}
}

func (m *Model) invalidateFeedPrefix() {
	m.historyPrefix = ""
	m.historyPrefixCount = 0
}

func (m *Model) collapseCompletedToolBatches() {
	if m.collapsedToolBatches == nil {
		m.collapsedToolBatches = make(map[string]bool)
	}
	seen := make(map[string]bool)
	for _, entry := range m.entries {
		if entry.kind != entryTool || entry.toolBatchID == "" || seen[entry.toolBatchID] {
			continue
		}
		seen[entry.toolBatchID] = true
		indices := m.toolBatchIndices(entry.toolBatchID)
		if len(indices) > 1 && toolBatchTerminal(m.entries, indices) {
			m.collapsedToolBatches[entry.toolBatchID] = true
		}
	}
	m.invalidateFeedPrefix()
}

func toolBatchTerminal(entries []transcriptEntry, indices []int) bool {
	for _, index := range indices {
		switch strings.ToLower(entries[index].toolStatus) {
		case "", "queued", "running", "waiting approval":
			return false
		}
	}
	return true
}

func (m *Model) noteOutput() {
	if !m.followTail || !m.feed.AtBottom() {
		m.followTail = false
		m.newOutput++
	}
}

func (m *Model) syncFollowTail() {
	if m.feed.AtBottom() {
		m.followTail = true
		m.newOutput = 0
		return
	}
	m.followTail = false
}

func (m Model) glyph(symbol, fallback string) string {
	if m.styles.Ascii {
		return fallback
	}
	return symbol
}

func (m *Model) renderEntry(index int, entry transcriptEntry) string {
	live := m.active != nil && index == m.liveEntry
	width := m.contentWidth()
	if width <= 0 {
		width = 80
	}
	if !live && !entry.dirty && entry.renderedCache != "" && entry.renderedWidth == width && entry.kind != entryStreaming {
		return entry.renderedCache
	}
	gutterWidth := max(1, width-4)
	switch entry.kind {
	case entryUser:
		cleanPrompt, attachedFiles := composer.CleanUserPrompt(entry.content)
		body := m.renderMentionText(cleanPrompt)
		if len(attachedFiles) > 0 {
			var pills []string
			for _, file := range attachedFiles {
				pills = append(pills, m.styles.Info.Render(composer.MentionMarker(false)+" "+file))
			}
			body += "\n" + strings.Join(pills, " ")
		}
		return m.styles.UserGutter.Width(gutterWidth).Render(body)
	case entryCommand:
		line := m.styles.User.Render("$ ") + m.styles.Text.Render(entry.content)
		return m.styles.UserGutter.Width(gutterWidth).Render(line)
	case entryStatus:
		marker := m.glyph("·", "-")
		if live {
			marker = m.spinner.View()
		}
		return m.styles.Muted.Width(width).Render(marker + " " + entry.content)
	case entryTool:
		return m.RenderToolEntry(index, entry, live)
	case entryDiff:
		return m.RenderDiffEntry(index, entry)
	case entryAssistant:
		label := m.styles.Assistant.Render(m.glyph("◆", "*") + " Supremo")
		var body string
		if entry.rendered != "" {
			body = entry.rendered
		} else {
			wrap := min(88, max(20, width-8))
			if rendered, err := rendering.RenderMarkdownContent(entry.content, width-4, wrap); err == nil && rendered != "" {
				if index < len(m.entries) {
					m.entries[index].rendered = rendered
				}
				body = rendered
			} else {
				body = m.styles.Text.Width(gutterWidth).Render(entry.content)
			}
		}
		return m.styles.AssistantGutter.Width(gutterWidth).Render(label + "\n" + body)
	case entryStreaming:
		label := m.styles.Assistant.Render(m.glyph("◆", "*") + " Supremo")
		return m.styles.AssistantGutter.Width(gutterWidth).Render(label + "\n" + m.styles.Text.Width(gutterWidth).Render(entry.content))
	case entryError:
		return m.formatUserError(entry.content, width)
	case entryDebug:
		return m.styles.Debug.Width(width).Render("debug · " + entry.content)
	case entryTodos:
		return entry.content
	default:
		return m.styles.Text.Width(width).Render(entry.content)
	}
}

func diffSummary(diff string) string {
	path := "file"
	added, removed := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed++
		}
	}
	return fmt.Sprintf("%s · +%d −%d", path, added, removed)
}

func visibleToolDetails(details string, offset int) (preview string, start, end, total int) {
	lines := strings.Split(details, "\n")
	offset = min(max(0, offset), max(0, len(lines)-maxVisibleToolLines))
	end = min(len(lines), offset+maxVisibleToolLines)
	return strings.Join(lines[offset:end], "\n"), offset, end, len(lines)
}

func (m *Model) appendStreamingChunk(content string) tea.Cmd {
	m.clearLiveStatus()
	m.streamBuffer.WriteString(content)
	now := time.Now()
	if m.streamLastTick.IsZero() || now.Sub(m.streamLastTick) >= 25*time.Millisecond || m.streamBuffer.Len() > 4096 {
		m.flushStreaming()
		return nil
	}
	if !m.streamTicking {
		m.streamTicking = true
		return streamFlushCmd()
	}
	return nil
}

func (m *Model) flushStreaming() {
	if m.streamBuffer.Len() == 0 {
		return
	}
	chunk := m.streamBuffer.String()
	m.streamBuffer.Reset()
	m.streamTicking = false
	m.streamLastTick = time.Now()

	m.updateStreaming(chunk)
}

func (m *Model) updateStreaming(content string) {
	content = safeText(content)
	if m.streamingEntry >= 0 && m.streamingEntry < len(m.entries) {
		m.entries[m.streamingEntry].content += content
		m.entries[m.streamingEntry].dirty = true
		m.noteOutput()
		m.rebuildFeed()
		return
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryStreaming, content: content, dirty: true})
	m.streamingEntry = len(m.entries) - 1
	m.noteOutput()
	m.rebuildFeed()
}

func (m *Model) finishStreaming(kind entryKind, content string) bool {
	m.flushStreaming()
	if m.streamingEntry < 0 || m.streamingEntry >= len(m.entries) {
		return false
	}
	if content == "" {
		m.entries = append(m.entries[:m.streamingEntry], m.entries[m.streamingEntry+1:]...)
	} else {
		m.entries[m.streamingEntry] = transcriptEntry{kind: kind, content: safeText(content), dirty: true}
	}
	m.streamingEntry = -1
	m.historyPrefix = ""
	m.historyPrefixCount = 0
	m.rebuildFeed()
	return true
}

func (m *Model) startTask(input string) tea.Cmd {
	if m.client != nil && !m.credentialReady {
		m.paletteOpen = false
		m.appendEntry(entryError, "A valid API key is required. Run /auth to enter one securely.")
		return nil
	}
	m.nextTaskID++
	id := m.nextTaskID
	attachments, warnings := composer.LoadMentionAttachments(m.workspace, composer.MentionPaths(input))
	prompt := composer.PromptWithAttachments(input, attachments)
	ctx, cancel := context.WithCancel(m.ctx)
	m.active = &activeTask{id: id, ctx: ctx, cancel: cancel, kind: taskAgent}
	m.approval = nil
	m.paletteOpen = false
	m.pendingInput = input
	m.input.Blur()
	for _, warning := range warnings {
		m.appendEntry(entryStatus, "@ "+warning)
	}
	m.setStatus("Submitting request")
	return tea.Batch(submitPromptCmd(ctx, m.client, m.session.ID, prompt, input, id), m.waitForProvider())
}

func (m *Model) startCommand(input string) tea.Cmd {
	m.nextTaskID++
	id := m.nextTaskID
	ctx, cancel := context.WithCancel(m.ctx)
	m.active = &activeTask{id: id, ctx: ctx, cancel: cancel, kind: taskCommand}
	m.paletteOpen = false
	m.resetComposer()
	m.appendEntry(entryCommand, displayCommand(input))
	if strings.HasPrefix(input, "/plan") {
		m.setStatus("Planning…")
	}
	return tea.Batch(executeCommandCmd(ctx, m.client, m.registry, m.session, input, id), m.spinner.Tick)
}

func (m *Model) startShell(command string) tea.Cmd {
	m.nextTaskID++
	id := m.nextTaskID
	ctx, cancel := context.WithCancel(m.ctx)
	m.active = &activeTask{id: id, ctx: ctx, cancel: cancel, kind: taskShell}
	m.paletteOpen = false
	m.resetComposer()
	m.entries = append(m.entries, newLocalShellEntry(command))
	m.liveEntry = len(m.entries) - 1
	m.noteOutput()
	m.rebuildFeed()
	m.setStatus("Running local shell command…")
	return tea.Batch(terminal.RunShellCmd(ctx, m.workspace, command, id), m.spinner.Tick)
}

func approvalModeCommand(input string) (string, bool) {
	parts := strings.Fields(input)
	if len(parts) != 1 && (len(parts) != 2 || parts[1] != "mode") {
		return "", false
	}
	switch parts[0] {
	case "/strict":
		return "strict", true
	case "/batman":
		return "batman", true
	case "/superman":
		return "superman", true
	default:
		return "", false
	}
}

func approvalModeStatus(mode string) string {
	switch mode {
	case "batman":
		return "Ask risky enabled — routine work runs automatically; risky actions ask first."
	case "superman":
		return "Auto-approve enabled — tools can run without confirmation."
	default:
		return "Ask changes enabled — changes and commands ask first."
	}
}

func workspaceStatusAPICmd(ctx context.Context, client api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return workspaceStatusMsg{err: errors.New("backend is unavailable")}
		}
		status, err := client.WorkspaceStatus(ctx)
		if err != nil {
			return workspaceStatusMsg{err: err}
		}
		return workspaceStatusMsg{info: workspaceState{branch: status.Branch, changed: status.Changed, ready: status.Ready, err: status.Error}}
	}
}

func markdownCmd(run, width int, entries []transcriptEntry) tea.Cmd {
	return func() tea.Msg {
		rendered := make(map[int]string)
		wrap := min(88, max(20, width-4))
		for i, entry := range entries {
			if entry.kind != entryAssistant {
				continue
			}
			value, err := rendering.RenderMarkdownContent(entry.content, width, wrap)
			if err == nil {
				rendered[i] = value
			}
		}
		return markdownRenderedMsg{run: run, rendered: rendered}
	}
}

func (m *Model) renderMarkdown() tea.Cmd {
	m.markdownRun++
	entries := append([]transcriptEntry(nil), m.entries...)
	return markdownCmd(m.markdownRun, m.feed.Width(), entries)
}

func streamFlushCmd() tea.Cmd {
	return tea.Tick(20*time.Millisecond, func(time.Time) tea.Msg { return streamFlushMsg{} })
}

func commandIs(input, name string) bool {
	parts := strings.Fields(input)
	return len(parts) > 0 && parts[0] == name
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
	if limit <= 0 {
		return value
	}
	return ansi.TruncateWc(value, limit, "…")
}

func (m Model) conciseToolLabel(tool, status, arguments string) string {
	path := m.toolPath(toolArgument(arguments, "path"))
	switch tool {
	case "read_file":
		return withTarget("Reading", path)
	case "write_file", "replace_in_file":
		return withTarget("Updating", path)
	case "delete_file":
		return withTarget("Removing", path)
	case "rename_file":
		return "Renaming files…"
	case "create_directory":
		return "Creating folder…"
	case "list_directory":
		return withTarget("Listing", path)
	case "search_file_name":
		return withTarget("Finding files matching", quotedToolTarget(firstToolArg(arguments, "pattern", "query")))
	case "search_text":
		return withTarget("Searching", quotedToolTarget(firstToolArg(arguments, "pattern", "query")))
	case "find_symbol":
		return withTarget("Finding symbol", quotedToolTarget(toolArgument(arguments, "symbol")))
	case "find_references":
		return withTarget("Finding references to", quotedToolTarget(toolArgument(arguments, "symbol")))
	case "repository_query":
		return withTarget("Querying repository for", quotedToolTarget(toolArgument(arguments, "query")))
	case "git_status", "git_diff", "git_log":
		return "Running " + tool + "…"
	case "web_fetch":
		return "Fetching reference…"
	case "execute_command":
		return conciseCommandLabel(toolArgument(arguments, "command"), toolArguments(arguments, "args"), status)
	default:
		return "Working…"
	}
}

func (m Model) toolPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return ""
	}
	if filepath.IsAbs(path) && m.workspace != "" {
		if relative, err := filepath.Rel(m.workspace, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = relative
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func conciseCommandLabel(command string, args []string, status string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		if status == "failed" {
			return "Command failed"
		}
		return "Running command…"
	}
	parts := append([]string{command}, args...)
	label := strings.Join(parts, " ")
	if status == "failed" {
		return "Command failed: " + truncate(label, 100)
	}
	return "Running " + truncate(label, 100) + "…"
}
