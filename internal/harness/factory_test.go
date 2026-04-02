package harness

import (
	"context"
	"reflect"
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
		"grep",
		"list_files",
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

type chatClientStub struct{}

func (chatClientStub) Complete(_ context.Context, _ model.Request) (model.Response, error) {
	return model.Response{}, nil
}
