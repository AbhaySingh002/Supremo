//go:build !windows

package hostexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 1 || cmd.Process.Pid == os.Getpid() || cmd.Process.Pid == syscall.Getpgrp() {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !killGone(err) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = commandWaitDelay
}

func cleanupCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil || cmd.Process.Pid <= 1 || cmd.Process.Pid == os.Getpid() || cmd.Process.Pid == syscall.Getpgrp() {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !killGone(err) {
		return err
	}
	return nil
}

func killGone(err error) bool {
	return errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM)
}
