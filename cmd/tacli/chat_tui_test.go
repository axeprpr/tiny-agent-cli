package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

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
	r.loop = agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
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

func TestRefreshViewportsOnlyRerendersDirtyContent(t *testing.T) {
	m := chatTUIModel{
		chatViewport: viewport.New(80, 20),
		logViewport:  viewport.New(80, 10),
		entries:      []tuiEntry{{role: "assistant", text: "hello"}},
		logs:         []tuiLogEntry{{kind: "steps", text: "executing 1 tool(s)"}},
		entriesDirty: true,
		logsDirty:    true,
	}

	m.refreshViewports()
	if m.entriesDirty || m.logsDirty {
		t.Fatalf("expected refresh to clear dirty flags")
	}

	entriesWidth := m.entriesWidth
	logsWidth := m.logsWidth
	content := m.chatViewport.View()
	logContent := m.logViewport.View()

	m.refreshViewports()
	if m.entriesWidth != entriesWidth || m.logsWidth != logsWidth {
		t.Fatalf("expected cached widths to remain unchanged")
	}
	if m.chatViewport.View() != content || m.logViewport.View() != logContent {
		t.Fatalf("expected viewport content to remain stable without rerender")
	}
}

func TestResizeSkipsDirtyRefreshUntilForced(t *testing.T) {
	m := chatTUIModel{
		width:        100,
		height:       30,
		chatViewport: viewport.New(96, 18),
		logViewport:  viewport.New(96, 7),
		input:        textarea.New(),
		entries:      []tuiEntry{{role: "assistant", text: "hello"}},
		entriesDirty: true,
	}

	m.resize(false)
	if !m.entriesDirty {
		t.Fatalf("expected resize without force to leave dirty flag set")
	}

	m.resize(true)
	if m.entriesDirty {
		t.Fatalf("expected forced resize to refresh dirty content")
	}
}

func TestRenderEntriesKeepsStreamingTextPlain(t *testing.T) {
	m := chatTUIModel{
		chatViewport: viewport.New(80, 20),
		entries:      []tuiEntry{{role: "streaming", text: "# heading"}},
	}

	got := m.renderEntries()
	if !strings.Contains(got, "# heading") {
		t.Fatalf("expected streaming text to remain plain, got %q", got)
	}
}

func TestComposerHintHiddenByDefault(t *testing.T) {
	m := chatTUIModel{}
	if got := m.composerHint(); got != "" {
		t.Fatalf("expected default composer hint to be hidden, got %q", got)
	}
}

func TestContextStatusShowsTokenCounts(t *testing.T) {
	r := &chatRuntime{
		cfg:     config.Config{ContextWindow: 32768},
		session: agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}
	got := contextStatus(r, "hello world")
	if !strings.Contains(got, "ctx ") || !strings.Contains(got, "tok") || !strings.Contains(got, "left") {
		t.Fatalf("unexpected context status: %q", got)
	}
}

func TestMouseWheelScrollIsBatched(t *testing.T) {
	m := chatTUIModel{
		chatViewport: viewport.New(80, 3),
		input:        textarea.New(),
	}
	m.chatViewport.SetContent(strings.Join([]string{
		"1", "2", "3", "4", "5", "6", "7", "8",
	}, "\n"))

	msg := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}

	updated, cmd := m.Update(msg)
	m = updated.(chatTUIModel)
	if cmd == nil {
		t.Fatalf("expected scroll tick to be scheduled")
	}
	if m.chatViewport.YOffset != 0 {
		t.Fatalf("expected wheel event to defer viewport movement, got offset %d", m.chatViewport.YOffset)
	}

	updated, _ = m.Update(msg)
	m = updated.(chatTUIModel)
	if m.pendingScroll != 6 {
		t.Fatalf("expected pending scroll to accumulate, got %d", m.pendingScroll)
	}

	updated, _ = m.Update(tuiMouseScrollMsg{})
	m = updated.(chatTUIModel)
	if m.pendingScroll != 0 {
		t.Fatalf("expected pending scroll to flush, got %d", m.pendingScroll)
	}
	if m.chatViewport.YOffset != 5 {
		t.Fatalf("expected batched scroll to apply once, got offset %d", m.chatViewport.YOffset)
	}
}
