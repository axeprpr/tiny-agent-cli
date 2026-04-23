//go:build !windows

package platform

import (
	"errors"
	"os/exec"
	"syscall"
)

func IsWindows() bool {
	return false
}

func DefaultShell() string {
	return "bash"
}

func ConfigureCommandCancellation(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
