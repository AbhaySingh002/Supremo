//go:build windows

package hostexec

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func prepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		if err == nil {
			return nil
		}
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return errors.Join(err, killErr)
	}
	cmd.WaitDelay = commandWaitDelay
}

func cleanupCommand(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	return nil
}
