package tools

import (
	"context"
	"encoding/json"
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

func TestRunCommandToolAllowsExplicitBackgroundingForLongRunningCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions are bash-specific")
	}
	tool, ok := newRunCommandTool(".", "bash", 5*time.Second, nil).(*runCommandTool)
	if !ok {
		t.Fatalf("unexpected tool type")
	}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"command":"tail -f /dev/null >/dev/null 2>&1 & pid=$!; sleep 0.1; kill $pid >/dev/null 2>&1; wait $pid 2>/dev/null; echo ok"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}
