package platform

import (
	"os/exec"
	"strings"
)

func ShellInvocation(shell, command string) (string, []string) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = DefaultShell()
	}
	if IsWindows() || strings.Contains(strings.ToLower(shell), "powershell") {
		return shell, []string{"-NoProfile", "-Command", command}
	}
	return shell, []string{"-c", command}
}

func HookShellCommand(command string) *exec.Cmd {
	if IsWindows() {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-lc", command)
}
