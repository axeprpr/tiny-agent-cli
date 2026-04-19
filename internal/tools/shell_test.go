package tools

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateCommandBlocksDangerousRMVariants(t *testing.T) {
	cases := []string{
		"rm -rf /",
		"rm -rf  /",
		"rm -fr /",
		"rm -r -f /",
		"FOO=bar rm -rf /",
		"rm -rf -- /",
		"echo ok; rm -rf /",
		`rm -rf "$EMPTY/"`,
	}
	for _, command := range cases {
		if err := validateCommand(command); err == nil {
			t.Fatalf("expected blocked command: %q", command)
		}
	}
}

func TestValidateCommandAllowsSafeRM(t *testing.T) {
	if err := validateCommand("rm -rf /tmp/tiny-agent-cli-test"); err != nil {
		t.Fatalf("unexpected block: %v", err)
	}
}

func TestRunCommandToolUsesDefaultTimeoutWhenUnset(t *testing.T) {
	tool, ok := newRunCommandTool(".", "bash", 0, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo ok"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRunCommandToolHonorsTimeoutOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command is shell-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	started := time.Now()
	_, err := tool.Call(context.Background(), json.RawMessage(`{"command":"sleep 2","timeout_seconds":1}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestConfigureCommandCancellationSetsCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows uses no-op cancellation setup")
	}
	tool, ok := newRunCommandTool(".", "bash", time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	_ = tool
	// Execute a short timed command to exercise CommandContext + custom cancel hook path.
	_, err := newRunCommandTool(".", "bash", time.Second, nil).Call(context.Background(), json.RawMessage(`{"command":"sleep 2","timeout_seconds":1}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout with cancellation, got %v", err)
	}
}

func TestRunCommandToolInjectsDefaultCacheEnvWhenMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("GOCACHE", "")

	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"printf '%s|%s|%s' \"$HOME\" \"$XDG_CACHE_HOME\" \"$GOCACHE\""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 3 {
		t.Fatalf("unexpected env output: %q", out)
	}
	for i, part := range parts {
		if strings.TrimSpace(part) == "" {
			t.Fatalf("expected part %d to be non-empty, got %q", i, out)
		}
	}
	if !strings.HasSuffix(parts[2], "/go-build") {
		t.Fatalf("expected GOCACHE to end with /go-build, got %q", parts[2])
	}
}

func TestRunCommandToolPreservesExplicitCacheEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	t.Setenv("HOME", "/tmp/tacli-home")
	t.Setenv("XDG_CACHE_HOME", "/tmp/tacli-cache")
	t.Setenv("GOCACHE", "/tmp/tacli-gocache")

	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"printf '%s|%s|%s' \"$HOME\" \"$XDG_CACHE_HOME\" \"$GOCACHE\""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "/tmp/tacli-home|/tmp/tacli-cache|/tmp/tacli-gocache" {
		t.Fatalf("unexpected env output: %q", out)
	}
}

func TestRunCommandToolRejectsForegroundLongRunningCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	_, err := tool.Call(context.Background(), json.RawMessage(`{"command":"tail -f /dev/null"}`))
	if err == nil || !strings.Contains(err.Error(), "long-running foreground process") {
		t.Fatalf("expected foreground-process rejection, got %v", err)
	}
}

func TestRunCommandToolRejectsChainedForegroundHttpServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	_, err := tool.Call(context.Background(), json.RawMessage(`{"command":"cd /tmp && python3 -m http.server 8000 --bind 0.0.0.0 >/tmp/http.log 2>&1"}`))
	if err == nil || !strings.Contains(err.Error(), "long-running foreground process") {
		t.Fatalf("expected chained foreground-process rejection, got %v", err)
	}
}

func TestRunCommandToolRejectsBareBackgroundingForLongRunningServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	_, err := tool.Call(context.Background(), json.RawMessage(`{"command":"cd /tmp && python3 -m http.server 8000 --bind 0.0.0.0 >/tmp/http.log 2>&1 & echo started"}`))
	if err == nil || !strings.Contains(err.Error(), "shell backgrounding") {
		t.Fatalf("expected bare background-service rejection, got %v", err)
	}
}

func TestRunCommandToolAllowsDetachedBackgroundingForLongRunningCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"nohup tail -f /dev/null >/tmp/tacli-tail.log 2>&1 < /dev/null & pid=$!; sleep 0.1; kill $pid >/dev/null 2>&1; echo ok"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRunCommandToolPersistsLargeOutputToLogFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	workDir := t.TempDir()
	tool, ok := newRunCommandTool(workDir, "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"python3 - <<'PY'\nprint('x'*40000)\nPY"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[output truncated; full log:") {
		t.Fatalf("expected truncated output hint, got %q", out)
	}
	marker := "[output truncated; full log:"
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatalf("missing output marker: %q", out)
	}
	tail := out[start+len(marker):]
	end := strings.Index(tail, "]")
	if end < 0 {
		t.Fatalf("expected closing bracket in output marker: %q", out)
	}
	path := strings.TrimSpace(tail[:end])
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected full log file to exist at %q: %v", path, statErr)
	}
}

func TestRunCommandToolCallStreamEmitsUpdates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	workDir := t.TempDir()
	tool, ok := newRunCommandTool(workDir, "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	var updates []string
	_, err := tool.CallStream(context.Background(), json.RawMessage(`{"command":"printf 'first\\nsecond\\n'"}`), func(update string) {
		if strings.TrimSpace(update) != "" {
			updates = append(updates, update)
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) == 0 {
		t.Fatalf("expected at least one streaming update")
	}
}
