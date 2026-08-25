package terminal

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/hostexec"
)

// ShellResultMsg represents the result of a direct host shell command execution.
type ShellResultMsg struct {
	ID      int
	Command string
	Output  hostexec.Output
	Err     error
}

// RunShellCmd executes an explicit ! host shell command. It never enters agent
// memory or the approval queue; its bounded output and cancellation behavior
// come from the same process helper used by terminal tools.
func RunShellCmd(ctx context.Context, workspace, command string, id int) tea.Cmd {
	return func() tea.Msg {
		command = strings.TrimSpace(command)
		cmd := HostShellCommand(ctx, command)
		cmd.Dir = workspace
		output, err := hostexec.Run(ctx, cmd)
		return ShellResultMsg{ID: id, Command: command, Output: output, Err: err}
	}
}

// HostShellCommand constructs an *exec.Cmd using the appropriate host shell.
func HostShellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-lc", command)
}
