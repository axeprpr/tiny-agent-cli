//go:build windows

package platform

import "os/exec"

func IsWindows() bool {
	return true
}

func DefaultShell() string {
	return "powershell.exe"
}

func ConfigureCommandCancellation(cmd *exec.Cmd) {
	_ = cmd
}
