package terminal

import (
	"testing"
)

func TestClipboardCopyCmdNilOnEmpty(t *testing.T) {
	if cmd := CopyTextCmd(""); cmd != nil {
		t.Fatal("expected nil cmd on empty copy")
	}
}

func TestClipboardCopyCmdNonNil(t *testing.T) {
	cmd := CopyTextCmd("hello")
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd for valid text")
	}
}

func TestReadClipboardCmdReturnsMessage(t *testing.T) {
	cmd := ReadClipboardCmd()
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd for ReadClipboardCmd")
	}
	msg := cmd()
	if _, ok := msg.(ClipboardPasteMsg); !ok {
		t.Fatalf("expected ClipboardPasteMsg, got %T: %#v", msg, msg)
	}
}
