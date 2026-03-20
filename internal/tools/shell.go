package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"onek-agent/internal/model"
)

type runCommandTool struct {
	workDir        string
	shell          string
	commandTimeout time.Duration
	approver       Approver
}

func newRunCommandTool(workDir, shell string, commandTimeout time.Duration, approver Approver) Tool {
	return &runCommandTool{
		workDir:        workDir,
		shell:          shell,
		commandTimeout: commandTimeout,
		approver:       approver,
	}
}

func (t *runCommandTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "run_command",
			Description: "Run a shell command in the workspace. Use for inspection, tests, and simple automation.",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"command",
				},
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Optional timeout override in seconds",
					},
				},
			},
		},
	}
}

func (t *runCommandTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	command := strings.TrimSpace(args.Command)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if err := validateCommand(command); err != nil {
		return "", err
	}
	if t.approver != nil {
		approved, err := t.approver.ApproveCommand(ctx, command)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("command rejected by user")
		}
	}

	timeout := t.commandTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, shellArgs := shellInvocation(t.shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = t.workDir

	data, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(data))
	if len(text) > 8192 {
		text = text[:8192] + "\n...[truncated]"
	}

	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("command failed")
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func shellInvocation(shell, command string) (string, []string) {
	if runtime.GOOS == "windows" || strings.Contains(strings.ToLower(shell), "powershell") {
		return shell, []string{"-NoProfile", "-Command", command}
	}
	return shell, []string{"-lc", command}
}

func validateCommand(command string) error {
	lower := strings.ToLower(command)
	blocked := []string{
		"rm -rf /",
		"shutdown",
		"reboot",
		"poweroff",
		"halt",
		"mkfs",
		"fdisk",
		"parted",
		"dd if=",
		":(){:|:&};:",
	}

	for _, token := range blocked {
		if strings.Contains(lower, token) {
			return fmt.Errorf("blocked command pattern: %s", token)
		}
	}
	return nil
}
