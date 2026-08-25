package ui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
)

// ComposerKeyMap defines keybindings for the composer input view.
type ComposerKeyMap struct {
	Submit      key.Binding
	Newline     key.Binding
	Complete    key.Binding
	Plans       key.Binding
	ToggleMode  key.Binding
	ToggleDebug key.Binding
	Activity    key.Binding
	Clear       key.Binding
	Help        key.Binding
	Cancel      key.Binding
}

func (k ComposerKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Complete, k.Plans, k.Activity, k.Help}
}

func (k ComposerKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Submit, k.Newline, k.Complete},
		{k.Plans, k.ToggleMode, k.Activity, k.ToggleDebug},
		{k.Clear, k.Help, k.Cancel},
	}
}

// FeedKeyMap defines keybindings for the conversation transcript.
type FeedKeyMap struct {
	ScrollUp   key.Binding
	ScrollDown key.Binding
	PgUp       key.Binding
	PgDown     key.Binding
	Top        key.Binding
	Bottom     key.Binding
	Copy       key.Binding
	Evidence   key.Binding
	Clear      key.Binding
	FocusInput key.Binding
}

func (k FeedKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.ScrollUp, k.ScrollDown, k.PgUp, k.PgDown, k.Bottom}
}

func (k FeedKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.ScrollUp, k.ScrollDown, k.PgUp, k.PgDown},
		{k.Top, k.Bottom, k.Copy, k.Evidence, k.Clear, k.FocusInput},
	}
}

// PlanDraftKeyMap defines keybindings when drafting a new plan.
type PlanDraftKeyMap struct {
	Submit key.Binding
	Exit   key.Binding
	Plans  key.Binding
	Help   key.Binding
}

func (k PlanDraftKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Exit, k.Plans, k.Help}
}

func (k PlanDraftKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Submit, k.Exit},
		{k.Plans, k.Help},
	}
}

// PlanReadyKeyMap defines keybindings when a plan is approved and ready.
type PlanReadyKeyMap struct {
	Execute key.Binding
	Plans   key.Binding
	Help    key.Binding
}

func (k PlanReadyKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Execute, k.Plans, k.Help}
}

func (k PlanReadyKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Execute, k.Plans, k.Help}}
}

// PlanQuestionKeyMap defines keybindings when answering interactive questions.
type PlanQuestionKeyMap struct {
	PickNumber  key.Binding
	Up          key.Binding
	Down        key.Binding
	Select      key.Binding
	Recommended key.Binding
	Custom      key.Binding
	Back        key.Binding
	Cancel      key.Binding
}

func (k PlanQuestionKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.PickNumber, k.Up, k.Down, k.Select, k.Recommended, k.Custom}
}

func (k PlanQuestionKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.PickNumber, k.Up, k.Down, k.Select},
		{k.Recommended, k.Custom, k.Back, k.Cancel},
	}
}

// ApprovalKeyMap defines keybindings for pending tool authorizations.
type ApprovalKeyMap struct {
	Approve key.Binding
	Deny    key.Binding
	Edit    key.Binding
	Auto    key.Binding
}

func (k ApprovalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Approve, k.Deny, k.Edit, k.Auto}
}

func (k ApprovalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Approve, k.Deny, k.Edit, k.Auto}}
}

// SelectorKeyMap defines keybindings for palettes, menus, and item lists.
type SelectorKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Select   key.Binding
	Complete key.Binding
	Close    key.Binding
}

func (k SelectorKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Select, k.Complete, k.Close}
}

func (k SelectorKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Select, k.Complete, k.Close}}
}

// OverlayKeyMap defines keybindings for modal dialogs and overlays.
type OverlayKeyMap struct {
	Confirm key.Binding
	Cancel  key.Binding
	Up      key.Binding
	Down    key.Binding
}

func (k OverlayKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Confirm, k.Cancel}
}

func (k OverlayKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Confirm, k.Cancel, k.Up, k.Down}}
}

// ViewerKeyMap defines keybindings for diff and artifact inspectors.
type ViewerKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	PgUp   key.Binding
	PgDown key.Binding
	Top    key.Binding
	Bottom key.Binding
	Close  key.Binding
}

func (k ViewerKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.PgUp, k.PgDown, k.Close}
}

func (k ViewerKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PgUp, k.PgDown},
		{k.Top, k.Bottom, k.Close},
	}
}

// StreamingKeyMap defines keybindings while agent tasks are running.
type StreamingKeyMap struct {
	Stop key.Binding
	Help key.Binding
}

func (k StreamingKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Stop, k.Help}
}

func (k StreamingKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Stop, k.Help}}
}

// KeyMap groups all contextual keymaps for the root model.
type KeyMap struct {
	Composer     ComposerKeyMap
	Feed         FeedKeyMap
	PlanDraft    PlanDraftKeyMap
	PlanReady    PlanReadyKeyMap
	PlanQuestion PlanQuestionKeyMap
	Approval     ApprovalKeyMap
	Selector     SelectorKeyMap
	Overlay      OverlayKeyMap
	Viewer       ViewerKeyMap
	Streaming    StreamingKeyMap
}

var _ help.KeyMap = ComposerKeyMap{}
var _ help.KeyMap = FeedKeyMap{}
var _ help.KeyMap = PlanDraftKeyMap{}
var _ help.KeyMap = PlanReadyKeyMap{}
var _ help.KeyMap = PlanQuestionKeyMap{}
var _ help.KeyMap = ApprovalKeyMap{}
var _ help.KeyMap = SelectorKeyMap{}
var _ help.KeyMap = OverlayKeyMap{}
var _ help.KeyMap = ViewerKeyMap{}
var _ help.KeyMap = StreamingKeyMap{}

// newKeyMap initializes the default global keybindings.
func newKeyMap() KeyMap {
	return KeyMap{
		Composer: ComposerKeyMap{
			Submit:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "send")),
			Newline:     key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+enter", "cmd+enter", "super+enter", "meta+enter", "opt+enter", "option+enter", "esc+enter", "esc+return", "shift+return", "alt+return", "ctrl+return", "cmd+return", "super+return", "meta+return", "opt+return", "option+return", "ctrl+j", "ctrl+o"), key.WithHelp("ctrl+j / \\+↵", "line")),
			Complete:    key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
			Plans:       key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "plans")),
			ToggleMode:  key.NewBinding(key.WithKeys("ctrl+m"), key.WithHelp("ctrl+m", "mode")),
			ToggleDebug: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "debug")),
			Activity:    key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "activity")),
			Clear:       key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "clear")),
			Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
			Cancel:      key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "cancel")),
		},
		Feed: FeedKeyMap{
			ScrollUp:   key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "scroll up")),
			ScrollDown: key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "scroll down")),
			PgUp:       key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
			PgDown:     key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "page down")),
			Top:        key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "top")),
			Bottom:     key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "bottom")),
			Copy:       key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "copy")),
			Evidence:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "evidence")),
			Clear:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
			FocusInput: key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "input")),
		},
		PlanDraft: PlanDraftKeyMap{
			Submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "create plan")),
			Exit:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "exit plan")),
			Plans:  key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "saved plans")),
			Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		},
		PlanReady: PlanReadyKeyMap{
			Execute: key.NewBinding(key.WithKeys("ctrl+x"), key.WithHelp("ctrl+x", "execute")),
			Plans:   key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "saved plans")),
			Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		},
		PlanQuestion: PlanQuestionKeyMap{
			PickNumber:  key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), key.WithHelp("1-9", "pick")),
			Up:          key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
			Down:        key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
			Select:      key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("↵/space", "choose")),
			Recommended: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "recommended")),
			Custom:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "custom")),
			Back:        key.NewBinding(key.WithKeys("left", "b"), key.WithHelp("b/←", "back")),
			Cancel:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		},
		Approval: ApprovalKeyMap{
			Approve: key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y/↵", "allow")),
			Deny:    key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "deny")),
			Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
			Auto:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "auto")),
		},
		Selector: SelectorKeyMap{
			Up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
			Down:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
			Select:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "select")),
			Complete: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
			Close:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		},
		Overlay: OverlayKeyMap{
			Confirm: key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("↵", "confirm")),
			Cancel:  key.NewBinding(key.WithKeys("esc", "n"), key.WithHelp("esc", "cancel")),
			Up:      key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
			Down:    key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		},
		Viewer: ViewerKeyMap{
			Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
			Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
			PgUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
			PgDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
			Top:    key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "top")),
			Bottom: key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "bottom")),
			Close:  key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc", "close")),
		},
		Streaming: StreamingKeyMap{
			Stop: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "stop task")),
			Help: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		},
	}
}
