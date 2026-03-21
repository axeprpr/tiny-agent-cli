package main

import (
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/tools"
)

func TestAppendActivityEntryMergesToolResultLines(t *testing.T) {
	m := chatTUIModel{}

	m.appendActivityEntry("tools", `[1/1] run_command echo "hi"`)
	m.appendActivityEntry("steps", "ok in 12ms")
	m.appendActivityEntry("steps", "| 1 line, first: hi")

	if len(m.entries) != 1 {
		t.Fatalf("unexpected entry count: %d", len(m.entries))
	}
	got := m.entries[0]
	if got.role != "activity" {
		t.Fatalf("unexpected role: %q", got.role)
	}
	want := "tool  [1/1] run_command echo \"hi\"\nok in 12ms\n1 line, first: hi"
	if got.text != want {
		t.Fatalf("unexpected merged activity:\n%s", got.text)
	}
}

func TestNextStepStatusSkipsSummaryLines(t *testing.T) {
	current := "run_command echo hi"
	got := nextStepStatus(current, "steps", "| 1 line, first: hi")
	if got != current {
		t.Fatalf("summary line should not replace status: got %q want %q", got, current)
	}
}

func TestTodoLine(t *testing.T) {
	if got := todoLine(tools.TodoItem{Text: "inspect auth flow", Status: "in_progress"}); got != "[doing] inspect auth flow" {
		t.Fatalf("unexpected todo line: %q", got)
	}
}

func TestRenderTodoSummary(t *testing.T) {
	r := &chatRuntime{
		cfg: config.Config{Model: "test-model"},
	}
	r.loop = agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 1, nil)
	if err := r.loop.ReplaceTodo([]tools.TodoItem{
		{Text: "inspect auth flow", Status: "completed"},
		{Text: "patch refresh logic", Status: "pending"},
	}); err != nil {
		t.Fatalf("replace todo: %v", err)
	}
	m := chatTUIModel{runtime: r, width: 100}
	got := m.renderTodoSummary()
	if !strings.Contains(got, "plan") || !strings.Contains(got, "[done] inspect auth flow") {
		t.Fatalf("unexpected todo summary: %q", got)
	}
}
