package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/platform"
)

type runCommandTool struct {
	workDir        string
	shell          string
	commandTimeout time.Duration
	approver       Approver
}

const defaultCommandTimeout = 30 * time.Second
const maxInlineCommandOutputChars = 32768
const maxTailCaptureBytes = maxInlineCommandOutputChars * 4

const (
	runCommandFailureOther      = "other"
	runCommandFailureTimeout    = "timeout"
	runCommandFailureSyntax     = "syntax"
	runCommandFailureNotFound   = "not_found"
	runCommandFailurePermission = "permission"
)

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
	return t.call(ctx, raw, nil)
}

func (t *runCommandTool) CallStream(ctx context.Context, raw json.RawMessage, onUpdate func(string)) (string, error) {
	return t.call(ctx, raw, onUpdate)
}

func (t *runCommandTool) call(ctx context.Context, raw json.RawMessage, onUpdate func(string)) (string, error) {
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
	if err := validateForegroundCommand(command); err != nil {
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

	name, shellArgs := platform.ShellInvocation(t.shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = t.workDir
	cmd.Env = commandEnv(t.workDir)
	platform.ConfigureCommandCancellation(cmd)

	if onUpdate == nil {
		out, waitErr := cmd.CombinedOutput()
		text := finalizeCommandText(t.workDir, strings.TrimSpace(string(out)))
		if runCtx.Err() == context.DeadlineExceeded {
			kind := runCommandFailureTimeout
			return annotateRunCommandFailure(text, kind), fmt.Errorf("command timed out after %s (%s)", timeout, kind)
		}
		if waitErr != nil {
			if text == "" {
				text = waitErr.Error()
			}
			kind := classifyRunCommandFailureKind(text, waitErr)
			return annotateRunCommandFailure(text, kind), fmt.Errorf("command failed (%s)", kind)
		}
		if text == "" {
			return "(no output)", nil
		}
		return text, nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	chunks := make(chan []byte, 64)
	readErrCh := make(chan error, 2)
	var readers sync.WaitGroup
	streamReader := func(r io.Reader) {
		defer readers.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				part := append([]byte(nil), buf[:n]...)
				select {
				case chunks <- part:
				case <-runCtx.Done():
					return
				}
			}
			if err == nil {
				continue
			}
			if err != io.EOF && !ignorablePipeReadError(err) {
				readErrCh <- err
			}
			return
		}
	}
	readers.Add(2)
	go streamReader(stdout)
	go streamReader(stderr)
	go func() {
		readers.Wait()
		close(chunks)
	}()

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()

	var full bytes.Buffer
	tail := make([]byte, 0, maxTailCaptureBytes)
	lastEmit := time.Time{}
	emitted := false
	for chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		full.Write(chunk)
		tail = append(tail, chunk...)
		if len(tail) > maxTailCaptureBytes {
			tail = tail[len(tail)-maxTailCaptureBytes:]
		}
		if onUpdate != nil {
			now := time.Now()
			if lastEmit.IsZero() || now.Sub(lastEmit) >= 250*time.Millisecond {
				onUpdate(compactCommandPreview(string(tail)))
				lastEmit = now
				emitted = true
			}
		}
	}
	if onUpdate != nil && len(tail) > 0 {
		preview := compactCommandPreview(string(tail))
		if strings.TrimSpace(preview) != "" && !emitted {
			onUpdate(preview)
		}
	}
	close(readErrCh)
	for readErr := range readErrCh {
		if readErr != nil && err == nil {
			err = readErr
		}
	}
	waitErr := <-waitErrCh
	if err == nil {
		err = waitErr
	}

	text := finalizeCommandText(t.workDir, strings.TrimSpace(full.String()))
	if onUpdate != nil && !emitted && strings.TrimSpace(text) != "" {
		onUpdate(text)
		emitted = true
	}

	if runCtx.Err() == context.DeadlineExceeded {
		kind := runCommandFailureTimeout
		return annotateRunCommandFailure(text, kind), fmt.Errorf("command timed out after %s (%s)", timeout, kind)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		kind := classifyRunCommandFailureKind(text, err)
		return annotateRunCommandFailure(text, kind), fmt.Errorf("command failed (%s)", kind)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func compactCommandPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 120 {
		lines = append(lines[len(lines)-120:], "...[truncated]")
		text = strings.Join(lines, "\n")
	}
	if len(text) > maxInlineCommandOutputChars {
		text = text[len(text)-maxInlineCommandOutputChars:]
		text = "...[truncated]\n" + text
	}
	return strings.TrimSpace(text)
}

func annotateRunCommandFailure(text, kind string) string {
	text = strings.TrimSpace(text)
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		kind = runCommandFailureOther
	}
	marker := fmt.Sprintf("[run_command_error kind=%s]", kind)
	if text == "" {
		return marker
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(marker)) {
		return text
	}
	return marker + "\n" + text
}

func classifyRunCommandFailureKind(output string, err error) string {
	lower := strings.ToLower(strings.TrimSpace(output))
	if err != nil {
		errText := strings.ToLower(strings.TrimSpace(err.Error()))
		if errText != "" {
			lower += "\n" + errText
		}
	}
	switch {
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		return runCommandFailureTimeout
	case strings.Contains(lower, "syntax error"),
		strings.Contains(lower, "unexpected token"),
		strings.Contains(lower, "unexpected eof"),
		strings.Contains(lower, "parse error"),
		strings.Contains(lower, "bad substitution"):
		return runCommandFailureSyntax
	case strings.Contains(lower, "command not found"),
		strings.Contains(lower, "is not recognized as an internal or external command"),
		strings.Contains(lower, "no such file or directory"),
		strings.Contains(lower, "not found"):
		return runCommandFailureNotFound
	case strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "operation not permitted"),
		strings.Contains(lower, "access is denied"):
		return runCommandFailurePermission
	default:
		return runCommandFailureOther
	}
}

func finalizeCommandText(workDir, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.Count(text, "\n") > 250 || len(text) > maxInlineCommandOutputChars {
		path, persistErr := persistCommandOutputLog(workDir, []byte(text))
		text = compactCommandPreview(text)
		if path != "" {
			return text + fmt.Sprintf("\n\n[output truncated; full log: %s]", path)
		}
		if persistErr != nil {
			return text + fmt.Sprintf("\n\n[output truncated; failed to persist full log: %v]", persistErr)
		}
		return text + "\n\n[output truncated]"
	}
	if len(text) > maxInlineCommandOutputChars {
		return compactCommandPreview(text)
	}
	return text
}

func persistCommandOutputLog(workDir string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	dir := filepath.Join(workDir, ".tacli", "command-logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cmd-"+time.Now().UTC().Format("20060102-150405.000")+".log")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func ignorablePipeReadError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "file already closed") || strings.Contains(text, "closed pipe")
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

func validateForegroundCommand(command string) error {
	for _, part := range splitShellCommands(command) {
		normalized := normalizeShellCommand(part)
		if normalized == "" {
			continue
		}
		if !looksLikeLongRunningCommand(normalized) {
			continue
		}
		if !containsBackgroundOperator(part) {
			return fmt.Errorf("command appears to start a long-running foreground process; use start_background_job or detach it with nohup/setsid")
		}
		if !usesDetachedBackgrounding(normalized) {
			return fmt.Errorf("command appears to start a long-running service via shell backgrounding; use start_background_job or detach it with nohup/setsid")
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

	for i := 0; i < len(command); i++ {
		r := rune(command[i])
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
		case '&', '|':
			if i+1 < len(command) && rune(command[i+1]) == r {
				flush()
				i++
				continue
			}
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

func normalizeShellCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(command)), " "))
}

func looksLikeLongRunningCommand(normalized string) bool {
	switch {
	case strings.HasPrefix(normalized, "tail -f "),
		strings.HasPrefix(normalized, "tail -f"),
		strings.HasPrefix(normalized, "tail -F "),
		strings.HasPrefix(normalized, "watch "),
		strings.HasPrefix(normalized, "top"),
		strings.HasPrefix(normalized, "htop"),
		strings.HasPrefix(normalized, "less "),
		strings.HasPrefix(normalized, "more "),
		strings.HasPrefix(normalized, "python -m http.server"),
		strings.HasPrefix(normalized, "python3 -m http.server"),
		strings.HasPrefix(normalized, "npm run dev"),
		strings.HasPrefix(normalized, "npm start"),
		strings.HasPrefix(normalized, "pnpm dev"),
		strings.HasPrefix(normalized, "pnpm start"),
		strings.HasPrefix(normalized, "yarn dev"),
		strings.HasPrefix(normalized, "yarn start"),
		strings.HasPrefix(normalized, "vite"),
		strings.HasPrefix(normalized, "next dev"),
		strings.HasPrefix(normalized, "next start"),
		strings.HasPrefix(normalized, "uvicorn "),
		strings.HasPrefix(normalized, "gunicorn "),
		strings.HasPrefix(normalized, "docker compose up"),
		strings.HasPrefix(normalized, "docker-compose up"):
		return true
	default:
		return false
	}
}

func usesDetachedBackgrounding(normalized string) bool {
	if strings.Contains(normalized, " nohup ") || strings.HasPrefix(normalized, "nohup ") {
		return true
	}
	if strings.Contains(normalized, " setsid ") || strings.HasPrefix(normalized, "setsid ") {
		return true
	}
	return false
}

func containsBackgroundOperator(command string) bool {
	var quote rune
	escape := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escape {
			escape = false
			continue
		}
		if quote != 0 {
			if ch == '\\' && quote == '"' {
				escape = true
				continue
			}
			if rune(ch) == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = rune(ch)
		case '&':
			prev := byte(0)
			next := byte(0)
			if i > 0 {
				prev = command[i-1]
			}
			if i+1 < len(command) {
				next = command[i+1]
			}
			if prev == '&' || next == '&' || prev == '>' || prev == '<' {
				continue
			}
			return true
		}
	}
	return false
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
