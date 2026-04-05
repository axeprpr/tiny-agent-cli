package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

func TestBuildPromptContextUsesSortedToolNamesAndRole(t *testing.T) {
	cfg := config.Config{
		WorkDir:      "/tmp/work",
		Shell:        "bash",
		ApprovalMode: tools.ApprovalDangerously,
		Model:        "test-model",
	}
	loop := agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	ctx := BuildPromptContext(cfg, loop, "background:verify", "memory")

	if ctx.AgentRole != "verify" {
		t.Fatalf("unexpected role: %q", ctx.AgentRole)
	}
	if ctx.MemoryText != "memory" {
		t.Fatalf("unexpected memory text: %q", ctx.MemoryText)
	}
	expectedToolNames := []string{
		"edit_file",
		"fetch_url",
		"glob_search",
		"grep",
		"list_files",
		"list_mcp_servers",
		"read_file",
		"run_command",
		"show_todo",
		"update_todo",
		"web_search",
		"write_file",
	}
	if !reflect.DeepEqual(ctx.ToolNames, expectedToolNames) {
		t.Fatalf("unexpected tool names: %#v", ctx.ToolNames)
	}
}

func TestRoleFromSessionMode(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{mode: "", want: ""},
		{mode: "chat", want: ""},
		{mode: "background:plan", want: "plan"},
		{mode: " BACKGROUND:Explore ", want: "explore"},
	}
	for _, tt := range tests {
		if got := roleFromSessionMode(tt.mode); got != tt.want {
			t.Fatalf("mode %q: got %q want %q", tt.mode, got, tt.want)
		}
	}
}

func TestGitPromptContextCleanAndDirty(t *testing.T) {
	workDir := t.TempDir()
	runGit(t, workDir, "init")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, workDir, "add", "README.md")
	runGit(t, workDir, "commit", "-m", "init")

	branch, status := gitPromptContext(workDir)
	if strings.TrimSpace(branch) == "" {
		t.Fatalf("expected branch, got empty")
	}
	if status != "clean" {
		t.Fatalf("expected clean status, got %q", status)
	}

	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, dirty := gitPromptContext(workDir)
	if !strings.HasPrefix(dirty, "dirty(") {
		t.Fatalf("expected dirty status, got %q", dirty)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

type chatClientStub struct{}

func (chatClientStub) Complete(_ context.Context, _ model.Request) (model.Response, error) {
	return model.Response{}, nil
}
