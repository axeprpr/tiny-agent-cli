package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/session"
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

func TestRenderHeaderShowsVersionExplicitly(t *testing.T) {
	r := &chatRuntime{
		cfg:         config.Config{Model: "test-model"},
		sessionName: "chat-test",
		approver:    newTUIApprover(tools.ApprovalConfirm, make(chan tea.Msg, 1)),
	}
	m := chatTUIModel{runtime: r, width: 100}
	got := m.renderHeader()
	if !strings.Contains(got, "tacli") || !strings.Contains(got, "version "+version) {
		t.Fatalf("expected header to show explicit version, got %q", got)
	}
}

func TestRenderStatusLineShowsVersionBeforeContext(t *testing.T) {
	r := &chatRuntime{
		cfg:     config.Config{ContextWindow: 32768},
		session: agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}
	m := chatTUIModel{runtime: r, width: 100}
	got := m.renderStatusLine()

	versionIndex := strings.Index(got, version)
	ctxIndex := strings.Index(got, "ctx ")
	if versionIndex < 0 || ctxIndex < 0 {
		t.Fatalf("expected status line to show version before context, got %q", got)
	}
	if versionIndex > ctxIndex {
		t.Fatalf("expected version to appear before context, got %q", got)
	}
}

func TestRefreshViewportsOnlyRerendersDirtyContent(t *testing.T) {
	m := chatTUIModel{
		chatViewport:  viewport.New(80, 20),
		entries:       []tuiEntry{{role: "assistant", text: "hello"}},
		entriesDirty:  true,
		stickToBottom: true,
	}

	m.refreshViewports(false)
	if m.entriesDirty {
		t.Fatalf("expected refresh to clear dirty flag")
	}

	entriesWidth := m.entriesWidth
	content := m.chatViewport.View()

	m.refreshViewports(false)
	if m.entriesWidth != entriesWidth {
		t.Fatalf("expected cached widths to remain unchanged")
	}
	if m.chatViewport.View() != content {
		t.Fatalf("expected viewport content to remain stable without rerender")
	}
}

func TestResizeKeepsViewportPinnedToBottomWhenComposerHeightChanges(t *testing.T) {
	m := chatTUIModel{
		width:         80,
		height:        18,
		chatViewport:  viewport.New(76, 8),
		input:         textarea.New(),
		entries:       []tuiEntry{{role: "system", text: strings.Repeat("line\n", 24)}},
		entriesDirty:  true,
		stickToBottom: true,
	}

	m.input.SetHeight(1)
	m.resize(true)
	if !m.chatViewport.AtBottom() {
		t.Fatalf("expected initial viewport to be pinned to bottom")
	}

	m.input.SetHeight(5)
	m.resize(true)
	if !m.chatViewport.AtBottom() {
		t.Fatalf("expected viewport to stay pinned to bottom after composer resize")
	}
}

func TestRefreshViewportsPreservesOffsetWhenUserScrolledUp(t *testing.T) {
	m := chatTUIModel{
		width:         80,
		height:        18,
		chatViewport:  viewport.New(76, 6),
		input:         textarea.New(),
		entries:       []tuiEntry{{role: "system", text: strings.Repeat("line\n", 300)}},
		entriesDirty:  true,
		stickToBottom: false,
	}

	m.resize(true)
	m.chatViewport.SetYOffset(2)
	m.entries = append(m.entries, tuiEntry{role: "system", text: "new line"})
	m.entriesDirty = true
	m.refreshViewports(false)

	if m.chatViewport.YOffset != 2 {
		t.Fatalf("expected user scroll offset to be preserved, got %d", m.chatViewport.YOffset)
	}
}

func TestArrowKeyEditingDoesNotScrollChatViewport(t *testing.T) {
	m := chatTUIModel{
		chatViewport:  viewport.New(80, 3),
		input:         textarea.New(),
		stickToBottom: false,
	}
	m.chatViewport.SetContent(strings.Join([]string{
		"1", "2", "3", "4", "5", "6",
	}, "\n"))
	m.chatViewport.SetYOffset(2)
	m.input.Focus()
	m.input.SetWidth(20)
	m.input.SetHeight(2)
	m.input.SetValue("first line\nsecond line")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(chatTUIModel)

	if m.chatViewport.YOffset != 2 {
		t.Fatalf("expected up-arrow editing to leave chat viewport offset unchanged, got %d", m.chatViewport.YOffset)
	}
}

func TestRenderComposerKeepsMultilineInputCompact(t *testing.T) {
	m := chatTUIModel{
		width: 80,
		input: textarea.New(),
	}
	m.input.SetWidth(20)
	m.input.SetHeight(2)
	m.input.SetValue("first line\nsecond line")
	m.input.Blur()

	lines := strings.Split(m.renderComposer(), "\n")
	firstIndex := -1
	secondIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "first line") {
			firstIndex = i
		}
		if strings.Contains(line, "second line") {
			secondIndex = i
		}
	}

	if firstIndex < 0 || secondIndex < 0 {
		t.Fatalf("expected multiline composer output, got %q", m.renderComposer())
	}
	if secondIndex-firstIndex != 1 {
		t.Fatalf("expected multiline input lines to stay adjacent, got indices %d and %d in %q", firstIndex, secondIndex, m.renderComposer())
	}
}

func TestDesiredInputHeightGrowsBeyondFiveLinesWhenSpaceAllows(t *testing.T) {
	m := chatTUIModel{
		height: 30,
		input:  textarea.New(),
	}
	m.input.SetValue(strings.Repeat("line\n", 7) + "tail")

	if got := m.desiredInputHeight(); got != 8 {
		t.Fatalf("expected input height to grow with content, got %d", got)
	}
}

func TestRenderEntriesPreservesStyleOnClippedMultilineBody(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	oldDark := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
		lipgloss.SetHasDarkBackground(oldDark)
	})

	m := chatTUIModel{
		chatViewport: viewport.New(40, 1),
		entries: []tuiEntry{{
			role: "system",
			text: "first line\n192.168.23.35",
		}},
		entriesDirty: true,
	}

	m.refreshViewports(false)
	m.chatViewport.SetYOffset(2)
	view := m.chatViewport.View()

	if !strings.Contains(view, "192.168.23.35") {
		t.Fatalf("expected clipped viewport to show second body line, got %q", view)
	}
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected clipped body line to retain ANSI styling, got %q", view)
	}
}

func TestResizeSkipsDirtyRefreshUntilForced(t *testing.T) {
	m := chatTUIModel{
		width:        100,
		height:       30,
		chatViewport: viewport.New(96, 18),
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

func TestRenderEntriesRendersAssistantMarkdown(t *testing.T) {
	m := chatTUIModel{
		chatViewport: viewport.New(80, 20),
		entries:      []tuiEntry{{role: "assistant", text: "# heading\n\n- item"}},
	}

	got := m.renderEntries()
	if !strings.Contains(got, "heading") || !strings.Contains(got, "item") {
		t.Fatalf("expected assistant markdown to render content, got %q", got)
	}
	if strings.Contains(got, "- item") {
		t.Fatalf("expected assistant markdown list marker to be rendered, got %q", got)
	}
}

func TestTUILogWriterSanitizesToolCallID(t *testing.T) {
	events := make(chan tea.Msg, 1)
	writer := tuiLogWriter{events: events}

	if _, err := writer.Write([]byte("[1/1] read_file id=call_123 path=README.md\n")); err != nil {
		t.Fatalf("write log: %v", err)
	}

	msg := (<-events).(tuiLogMsg)
	if strings.Contains(msg.text, "id=") {
		t.Fatalf("expected tool call id to be hidden from TUI logs, got %q", msg.text)
	}
	if !strings.Contains(msg.text, "read_file") {
		t.Fatalf("expected tool name to remain visible, got %q", msg.text)
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

func TestMouseWheelScrollAppliesImmediately(t *testing.T) {
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
	if cmd != nil {
		t.Fatalf("expected immediate wheel handling without scheduling")
	}
	if m.chatViewport.YOffset == 0 {
		t.Fatalf("expected wheel to move viewport immediately")
	}
}

func TestMouseScrollFlushKeepsComposerStable(t *testing.T) {
	m := chatTUIModel{
		width:        80,
		height:       24,
		chatViewport: viewport.New(80, 3),
		input:        textarea.New(),
	}
	m.chatViewport.SetContent(strings.Join([]string{
		"1", "2", "3", "4", "5", "6", "7", "8",
	}, "\n"))
	m.input.SetWidth(20)
	m.input.SetHeight(2)
	m.input.SetValue("first line\nsecond line")

	before := m.renderComposer()
	updated, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = updated.(chatTUIModel)
	after := m.renderComposer()

	if strings.Count(after, "first line") != 1 || strings.Count(after, "second line") != 1 {
		t.Fatalf("expected composer lines to remain unique after mouse scroll flush, got %q", after)
	}
	if !strings.Contains(after, "─") {
		t.Fatalf("expected composer separator line to remain visible, got %q", after)
	}
	if strings.TrimSpace(before) == "" || strings.TrimSpace(after) == "" {
		t.Fatalf("unexpected empty composer output: before=%q after=%q", before, after)
	}
}

func TestMouseWheelEventsAreThrottled(t *testing.T) {
	m := chatTUIModel{
		chatViewport:  viewport.New(80, 3),
		input:         textarea.New(),
		lastMouseTime: time.Now(),
	}

	updated, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = updated.(chatTUIModel)
	if cmd != nil {
		t.Fatalf("expected throttled wheel to skip scheduling")
	}
	if m.chatViewport.YOffset != 0 {
		t.Fatalf("expected no viewport movement when throttled, got %d", m.chatViewport.YOffset)
	}
}

func TestMouseClickCopiesEntry(t *testing.T) {
	r := &chatRuntime{
		cfg:         config.Config{Model: "test-model"},
		sessionName: "chat-test",
		approver:    newTUIApprover(tools.ApprovalConfirm, make(chan tea.Msg, 1)),
	}
	r.loop = agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	r.session = r.loop.NewSession()
	m := newChatTUIModel(r, make(chan tea.Msg, 1))
	m.chatViewport.Width = 80
	m.chatViewport.Height = 4
	m.entries = []tuiEntry{
		{role: "assistant", text: "first"},
		{role: "assistant", text: "second"},
	}
	m.entriesDirty = true
	m.refreshViewports(true)

	press := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      1,
		Y:      m.chatViewportTop() + 1,
	}
	updated, cmd := m.Update(press)
	m = updated.(chatTUIModel)
	if cmd != nil {
		t.Fatalf("expected no copy command on mouse press")
	}

	release := tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      1,
		Y:      m.chatViewportTop() + 1,
	}
	updated, cmd = m.Update(release)
	m = updated.(chatTUIModel)
	if cmd == nil {
		t.Fatalf("expected copy command from mouse release")
	}
	copyMsg := cmd()
	updated, _ = m.Update(copyMsg)
	m = updated.(chatTUIModel)
	if m.statusText != "copied" {
		t.Fatalf("expected copied status, got %q", m.statusText)
	}
}

func TestBuildOSC52SequenceProducesControlSequence(t *testing.T) {
	seq := buildOSC52Sequence("copy me")
	if !strings.Contains(seq, "]52;") {
		t.Fatalf("expected OSC52 sequence, got %q", seq)
	}
}

func TestSelectedContentTextUsesDisplayCoordinates(t *testing.T) {
	m := chatTUIModel{
		chatViewport: viewport.New(80, 20),
		entries:      []tuiEntry{{role: "assistant", text: "abcdef"}},
	}
	m.entriesDirty = true
	m.refreshViewports(true)
	all := strings.Split(m.renderEntries(), "\n")
	if len(all) == 0 {
		t.Fatalf("expected rendered content")
	}
	targetLine := -1
	for i, line := range all {
		if strings.Contains(line, "abcdef") {
			targetLine = i
			break
		}
	}
	if targetLine < 0 {
		t.Fatalf("expected body line in rendered entries")
	}
	plain := strings.Split(m.renderEntries(), "\n")[targetLine]
	start := strings.Index(plain, "abcdef")
	if start < 0 {
		t.Fatalf("expected abcdef in target line")
	}
	m.selStartLine = targetLine
	m.selEndLine = targetLine
	m.selStartCol = start + 1
	m.selEndCol = start + 4

	got := m.selectedContentText()
	if got != "bcd" {
		t.Fatalf("unexpected selected substring: got %q want %q", got, "bcd")
	}
}

func TestRenderConversationHighlightsSelectionDuringDrag(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	oldDark := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
		lipgloss.SetHasDarkBackground(oldDark)
	})

	m := chatTUIModel{
		chatViewport: viewport.New(80, 4),
		entries:      []tuiEntry{{role: "assistant", text: "one\ntwo\nthree"}},
		selActive:    true,
	}
	m.entriesDirty = true
	m.refreshViewports(true)

	m.selStartLine = 1
	m.selEndLine = 2
	m.selStartCol = 0
	m.selEndCol = 10

	selected := m.renderConversation()
	m.selActive = false
	plain := m.renderConversation()
	if selected == plain {
		t.Fatalf("expected selected conversation rendering to differ from plain rendering")
	}
}

func TestBusySendQueuesPrompt(t *testing.T) {
	r := &chatRuntime{
		cfg:     config.Config{Model: "test-model"},
		session: agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}
	m := newChatTUIModel(r, make(chan tea.Msg, 1))
	m.busy = true
	m.input.SetValue("follow-up question")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(chatTUIModel)

	if len(m.queuedTasks) != 0 {
		t.Fatalf("expected local task queue to remain empty, got %#v", m.queuedTasks)
	}
	if m.input.Value() != "" {
		t.Fatalf("expected input to reset after queueing, got %q", m.input.Value())
	}
	if len(m.entries) != 2 {
		t.Fatalf("expected user entry plus queue note, got %d entries", len(m.entries))
	}
	if m.entries[1].role != "system" || !strings.Contains(strings.ToLower(m.entries[1].text), "steering") {
		t.Fatalf("expected steering queue note, got %#v", m.entries[1])
	}
}

func TestInterruptForegroundCancelsRunningTask(t *testing.T) {
	canceled := false
	r := &chatRuntime{
		cfg:     config.Config{Model: "test-model"},
		session: agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}
	m := newChatTUIModel(r, make(chan tea.Msg, 1))
	m.busy = true
	m.runningCancel = func() {
		canceled = true
	}
	m.runtime.setForegroundCancel(m.runningCancel)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(chatTUIModel)

	if !canceled {
		t.Fatalf("expected running task to be canceled")
	}
	if !strings.Contains(m.statusText, "interrupt") {
		t.Fatalf("expected interrupt status, got %q", m.statusText)
	}
}

func TestTaskDoneStartsQueuedTask(t *testing.T) {
	r := &chatRuntime{
		cfg: config.Config{Model: "test-model"},
	}
	r.loop = agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	m := chatTUIModel{
		runtime:       r,
		input:         textarea.New(),
		queuedTasks:   []string{"second task"},
		busy:          true,
		runningTask:   "first task",
		runningCancel: func() {},
	}

	updated, cmd := m.Update(tuiTaskDoneMsg{task: "first task", err: context.Canceled})
	m = updated.(chatTUIModel)

	if !m.busy {
		t.Fatalf("expected queued task to start immediately")
	}
	if m.runningTask != "second task" {
		t.Fatalf("expected second task to start, got %q", m.runningTask)
	}
	if len(m.queuedTasks) != 0 {
		t.Fatalf("expected queue to drain, got %#v", m.queuedTasks)
	}
	if cmd == nil {
		t.Fatalf("expected queued task command to be scheduled")
	}
}

func TestTaskDoneNormalizesStreamedTerminalOutputWithoutDuplicateAssistant(t *testing.T) {
	r := &chatRuntime{
		cfg:        config.Config{Model: "test-model"},
		outputMode: "terminal",
	}
	m := chatTUIModel{
		runtime:       r,
		input:         textarea.New(),
		entries:       []tuiEntry{{role: "streaming", text: "我看到了当前环境里有这些关键信息：\n- 工作目录下有多个项目：`deer-flow/`、`clashctl/`"}},
		busy:          true,
		runningTask:   "inspect env",
		runningCancel: func() {},
	}

	updated, _ := m.Update(tuiTaskDoneMsg{
		task:   "inspect env",
		output: "我看到了当前环境里有这些关键信息：\n- 工作目录下有多个项目：deer-flow/、clashctl/",
	})
	m = updated.(chatTUIModel)

	if len(m.entries) != 1 {
		t.Fatalf("expected streamed answer to be reused, got %#v", m.entries)
	}
	if m.entries[0].role != "assistant" {
		t.Fatalf("expected streamed entry to become assistant, got %#v", m.entries[0])
	}
	if strings.Contains(m.entries[0].text, "`") {
		t.Fatalf("expected terminal output normalization to strip markdown markers, got %q", m.entries[0].text)
	}
	if m.entries[0].text != "我看到了当前环境里有这些关键信息：\n- 工作目录下有多个项目：deer-flow/、clashctl/" {
		t.Fatalf("unexpected normalized assistant text: %q", m.entries[0].text)
	}
}

func TestSessionLoadReloadsVisibleConversation(t *testing.T) {
	stateDir := t.TempDir()
	if err := session.Save(session.SessionPath(stateDir, "source"), session.State{
		SessionName: "source",
		Messages: []model.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "old question"},
			{Role: "assistant", Content: "old answer"},
		},
	}); err != nil {
		t.Fatalf("save source session: %v", err)
	}

	r := &chatRuntime{
		cfg:         config.Config{Model: "test-model", StateDir: stateDir},
		session:     agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
		sessionName: "current",
		approver:    newTUIApprover(tools.ApprovalConfirm, make(chan tea.Msg, 1)),
	}
	r.session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "current question"},
		{Role: "assistant", Content: "current answer"},
	})

	m := newChatTUIModel(r, make(chan tea.Msg, 1))
	m.width = 100
	m.height = 30
	m.input.SetValue("/resume source")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(chatTUIModel)
	m.resize(true)

	content := m.renderEntries()
	if strings.Contains(content, "current question") {
		t.Fatalf("expected previous session content to be replaced, got %q", content)
	}
	if !strings.Contains(content, "old question") || !strings.Contains(content, "old answer") {
		t.Fatalf("expected restored session content, got %q", content)
	}
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "system" || !strings.Contains(m.entries[len(m.entries)-1].text, "conversation resumed: source") {
		t.Fatalf("expected restore confirmation entry, got %#v", m.entries)
	}
}
