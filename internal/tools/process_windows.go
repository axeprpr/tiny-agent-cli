//go:build windows

package tools

import "os/exec"

func configureCommandCancellation(cmd *exec.Cmd) {
	_ = cmd
}
