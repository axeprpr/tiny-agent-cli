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
		"check_webapp",
		"create_task",
		"delete_task",
		"edit_file",
		"fetch_url",
		"glob_search",
		"grep",
		"inspect_docx",
		"inspect_pdf",
		"list_files",
		"list_mcp_resources",
		"list_mcp_servers",
		"list_tasks",
		"read_file",
		"read_mcp_resource",
		"review_diff",
		"run_command",
		"show_task_contract",
		"show_todo",
		"update_task",
		"update_task_contract",
		"update_todo",
		"web_search",
		"write_file",
	}
	if !reflect.DeepEqual(ctx.ToolNames, expectedToolNames) {
		t.Fatalf("unexpected tool names: %#v", ctx.ToolNames)
	}
	if len(ctx.Capabilities) == 0 {
		t.Fatalf("expected bundled capabilities in prompt context")
	}
	joinedSkills := strings.ToLower(strings.Join(func() []string {
		names := make([]string, 0, len(ctx.Skills))
		for _, item := range ctx.Skills {
			names = append(names, item.Name)
		}
		return names
	}(), " "))
	for _, want := range []string{"grill-me", "tdd", "to-prd", "git-guardrails"} {
		if !strings.Contains(joinedSkills, want) {
			t.Fatalf("expected bundled skill %q in prompt context, got %#v", want, ctx.Skills)
		}
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
	if !strings.Contains(dirty, "README.md") {
		t.Fatalf("expected dirty status, got %q", dirty)
	}
}

func TestDiscoverInstructionFilesWalksFromRootToWorkdir(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "projects", "demo")
	if err := os.MkdirAll(filepath.Join(nested, ".claw"), 0o755); err != nil {
		t.Fatalf("mkdir nested dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAW.md"), []byte("root rules\n"), 0o644); err != nil {
		t.Fatalf("write root instruction: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".claw", "instructions.md"), []byte("local rules\n"), 0o644); err != nil {
		t.Fatalf("write local instruction: %v", err)
	}

	files, err := discoverInstructionFiles(nested)
	if err != nil {
		t.Fatalf("discoverInstructionFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("unexpected instruction files: %#v", files)
	}
	if files[0].Path != filepath.Join(root, "CLAW.md") {
		t.Fatalf("unexpected first path: %#v", files)
	}
	if files[1].Path != filepath.Join(nested, ".claw", "instructions.md") {
		t.Fatalf("unexpected second path: %#v", files)
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
