package terminal_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/ui/terminal"
)

func TestShellCommandUsesHostShell(t *testing.T) {
	cmd := terminal.HostShellCommand(context.Background(), "echo supremo")
	if runtime.GOOS == "windows" {
		if cmd.Path == "" || len(cmd.Args) < 3 || cmd.Args[1] != "/C" {
			t.Fatalf("windows shell command = %#v", cmd.Args)
		}
		return
	}
	if cmd.Path == "" || len(cmd.Args) < 3 || cmd.Args[1] != "-lc" || cmd.Args[2] != "echo supremo" {
		t.Fatalf("unix shell command = %#v", cmd.Args)
	}
}
