package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"

	"tiny-agent-cli/internal/model"
)

type runCommandTool struct {
	workDir        string
	shell          string
	commandTimeout time.Duration
	approver       Approver
}

const defaultCommandTimeout = 30 * time.Second

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
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, shellArgs := shellInvocation(t.shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = t.workDir
	cmd.Env = commandEnv(t.workDir)
	configureCommandCancellation(cmd)

	data, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(data))
	if len(text) > 32768 {
		text = text[:32768] + "\n...[truncated]"
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

func commandEnv(workDir string) []string {
	env := append([]string(nil), os.Environ()...)
	home := envValue(env, "HOME")
	if home == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			home = strings.TrimSpace(detected)
		}
		if home == "" {
			home = strings.TrimSpace(workDir)
		}
		if home != "" {
			env = append(env, "HOME="+home)
		}
	}

	cacheHome := envValue(env, "XDG_CACHE_HOME")
	if cacheHome == "" && home != "" {
		cacheHome = filepath.Join(home, ".cache")
		env = append(env, "XDG_CACHE_HOME="+cacheHome)
	}
	if envValue(env, "GOCACHE") == "" && cacheHome != "" {
		env = append(env, "GOCACHE="+filepath.Join(cacheHome, "go-build"))
	}
	return env
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(env[i], prefix))
		}
	}
	return ""
}

func shellInvocation(shell, command string) (string, []string) {
	if runtime.GOOS == "windows" || strings.Contains(strings.ToLower(shell), "powershell") {
		return shell, []string{"-NoProfile", "-Command", command}
	}
	return shell, []string{"-c", command}
}

func validateCommand(command string) error {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	blocked := []string{
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
		if strings.Contains(normalized, token) {
			return fmt.Errorf("blocked command pattern: %s", token)
		}
	}

	commands := splitShellCommands(command)
	for _, part := range commands {
		if isDangerousRMInvocation(part) {
			return fmt.Errorf("blocked command pattern: rm -rf /")
		}
	}
	return nil
}

func splitShellCommands(command string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	escape := false

	flush := func() {
		part := strings.TrimSpace(b.String())
		if part != "" {
			out = append(out, part)
		}
		b.Reset()
	}

	for _, r := range command {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		if quote != 0 {
			if r == '\\' && quote == '"' {
				escape = true
				b.WriteRune(r)
				continue
			}
			if r == quote {
				quote = 0
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			b.WriteRune(r)
		case ';', '\n':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func isDangerousRMInvocation(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}

	cmdIndex := 0
	for cmdIndex < len(fields) && isEnvAssignment(fields[cmdIndex]) {
		cmdIndex++
	}
	if cmdIndex >= len(fields) {
		return false
	}
	if trimShellPunctuation(fields[cmdIndex]) != "rm" {
		return false
	}

	recursive := false
	force := false
	afterTerminator := false
	for _, rawArg := range fields[cmdIndex+1:] {
		arg := trimShellPunctuation(rawArg)
		if arg == "" {
			continue
		}
		if !afterTerminator && arg == "--" {
			afterTerminator = true
			continue
		}
		if !afterTerminator && strings.HasPrefix(arg, "-") {
			switch arg {
			case "-r", "-R", "--recursive":
				recursive = true
			case "-f", "--force":
				force = true
			default:
				if strings.HasPrefix(arg, "--") {
					continue
				}
				for _, ch := range arg[1:] {
					switch ch {
					case 'r', 'R':
						recursive = true
					case 'f':
						force = true
					}
				}
			}
			continue
		}
		if recursive && force && isDangerousRMTarget(arg) {
			return true
		}
	}
	return false
}

func trimShellPunctuation(token string) string {
	return strings.Trim(token, " \t\r\n;&|")
}

func isEnvAssignment(token string) bool {
	if token == "" {
		return false
	}
	parts := strings.SplitN(token, "=", 2)
	if len(parts) != 2 {
		return false
	}
	key := parts[0]
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

var shellVarPattern = regexp.MustCompile(`\$(\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*)`)

func isDangerousRMTarget(arg string) bool {
	candidate := strings.TrimSpace(arg)
	candidate = strings.Trim(candidate, `"'`)
	if candidate == "" {
		return false
	}
	if strings.HasPrefix(candidate, "-") {
		return false
	}

	collapsed := shellVarPattern.ReplaceAllString(candidate, "")
	collapsed = strings.TrimSpace(collapsed)
	if collapsed == "" {
		return false
	}

	if strings.HasPrefix(collapsed, "/") {
		cleaned := path.Clean(collapsed)
		if cleaned == "/" {
			return true
		}
		rest := strings.TrimLeft(collapsed, "/")
		if rest == "" {
			return true
		}
		if strings.Trim(rest, "*?.") == "" {
			return true
		}
	}
	return false
}
