package terminal

import (
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// CopyTextCmd returns a Bubble Tea command that copies text to the clipboard.
// It uses tea.SetClipboard (OSC52 via Bubble Tea's output pipeline) and
// atotto/clipboard (OS-native pbcopy/xclip/wl-copy) in parallel.
func CopyTextCmd(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return tea.Batch(
		tea.SetClipboard(text),
		func() tea.Msg {
			// ponytail: OS clipboard as best-effort fallback; ignore errors
			// since tea.SetClipboard covers OSC52-capable terminals.
			_ = clipboard.WriteAll(text)
			return clipboardDoneMsg{}
		},
	)
}

// ReadClipboardCmd returns a Bubble Tea command that reads from the OS clipboard.
// Falls back to atotto/clipboard since tea.ReadClipboard relies on terminal
// OSC52 response which many terminals silently ignore.
func ReadClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.ReadAll()
		return ClipboardPasteMsg{Text: text, Err: err}
	}
}

// ClipboardPasteMsg is emitted when an asynchronous clipboard read finishes.
type ClipboardPasteMsg struct {
	Text string
	Err  error
}

// clipboardDoneMsg is a no-op signal that the OS clipboard write completed.
type clipboardDoneMsg struct{}
