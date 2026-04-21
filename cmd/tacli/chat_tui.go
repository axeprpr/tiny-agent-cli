package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"tiny-agent-cli/internal/i18n"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

type tuiLogMsg struct {
	kind string
	text string
}

type tuiApprovalMsg struct {
	title    string
	body     string
	response chan string
}

type tuiTaskDoneMsg struct {
	task   string
	output string
	err    error
}

type tuiJobUpdateMsg struct {
	text string
}

type tuiStreamMsg struct {
	token string
}

type tuiRefreshMsg struct{}

type tuiCopyResultMsg struct {
	ok     bool
	detail string
}

type tuiEntry struct {
	role string
	text string
}

type tuiLogWriter struct {
	events chan tea.Msg
}

type tuiAuditSink struct {
	events chan tea.Msg
}

func (w *tuiLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text == "" {
		return len(p), nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = sanitizeUserVisibleLogLine(line)
		if line != "" && !isHiddenActivityLogLine(line) {
			w.events <- tuiLogMsg{kind: classifyLogKind(line), text: line}
		}
	}
	return len(p), nil
}

func (s *tuiAuditSink) RecordToolEvent(_ context.Context, event tools.ToolAuditEvent) {
	if s == nil || s.events == nil {
		return
	}
	line := strings.TrimSpace(event.Tool + " " + event.Status)
	if strings.TrimSpace(event.Error) != "" {
		line += " err=" + compactJobText(event.Error, 140)
	}
	s.events <- tuiLogMsg{kind: "audit", text: line}
}

type tuiApprover struct {
	mu     sync.Mutex
	mode   string
	events chan tea.Msg
	cmds   map[string]bool
	writes map[string]bool
}

func newTUIApprover(mode string, events chan tea.Msg) *tuiApprover {
	return &tuiApprover{
		mode:   strings.TrimSpace(mode),
		events: events,
		cmds:   make(map[string]bool),
		writes: make(map[string]bool),
	}
}

func (a *tuiApprover) Mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *tuiApprover) SetMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", tools.ApprovalConfirm:
		a.mode = tools.ApprovalConfirm
	case tools.ApprovalDangerously:
		a.mode = tools.ApprovalDangerously
	default:
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	return nil
}

func (a *tuiApprover) ApproveCommand(_ context.Context, command string) (bool, error) {
	command = strings.TrimSpace(command)
	a.mu.Lock()
	if a.mode == tools.ApprovalDangerously || a.cmds[command] {
		a.mu.Unlock()
		return true, nil
	}
	a.mu.Unlock()
	response := make(chan string, 1)
	a.events <- tuiApprovalMsg{
		title:    "Command approval",
		body:     command,
		response: response,
	}
	answer := <-response
	switch answer {
	case "yes":
		a.mu.Lock()
		a.cmds[command] = true
		a.mu.Unlock()
		return true, nil
	case "always":
		_ = a.SetMode(tools.ApprovalDangerously)
		return true, nil
	default:
		return false, nil
	}
}

func (a *tuiApprover) ApproveWrite(_ context.Context, path, content string) (bool, error) {
	path = strings.TrimSpace(path)
	key := tools.WriteApprovalKey(path, content)
	a.mu.Lock()
	if a.mode == tools.ApprovalDangerously || a.writes[key] {
		a.mu.Unlock()
		return true, nil
	}
	a.mu.Unlock()

	preview := strings.TrimSpace(content)
	if preview == "" {
		preview = "(empty file)"
	}
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	preview = strings.ReplaceAll(preview, "\n", "\\n")

	response := make(chan string, 1)
	a.events <- tuiApprovalMsg{
		title:    "File write approval",
		body:     fmt.Sprintf("path: %s\nbytes: %d\npreview: %s", strings.TrimSpace(path), len(content), preview),
		response: response,
	}
	answer := <-response
	switch answer {
	case "yes":
		a.mu.Lock()
		a.writes[key] = true
		a.mu.Unlock()
		return true, nil
	case "always":
		_ = a.SetMode(tools.ApprovalDangerously)
		return true, nil
	default:
		return false, nil
	}
}

type chatKeyMap struct {
	Send      key.Binding
	Newline   key.Binding
	Interrupt key.Binding
	Quit      key.Binding
}

func (k chatKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Interrupt, k.Quit}
}

func (k chatKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Interrupt, k.Quit},
	}
}

type chatTUIModel struct {
	runtime       *chatRuntime
	events        chan tea.Msg
	chatViewport  viewport.Model
	input         textarea.Model
	spinner       spinner.Model
	keys          chatKeyMap
	entries       []tuiEntry
	approval      *tuiApprovalMsg
	width         int
	height        int
	busy          bool
	stepText      string
	statusText    string
	entriesDirty  bool
	entriesWidth  int
	refreshQueued bool
	entryKeys     []string
	entryBlocks   []string
	entryLines    []int
	mouseDragOn   bool
	mouseDragFrom int
	mouseDragTo   int
	chatTop       int
	selActive     bool
	selStartLine  int
	selStartCol   int
	selEndLine    int
	selEndCol     int
	mdWidth       int
	mdRenderer    *glamour.TermRenderer
	stickToBottom bool
	lastMouseTime time.Time
	queuedTasks   []string
	runningTask   string
	runningCancel context.CancelFunc
}

var (
	appStyle    = lipgloss.NewStyle().Padding(0, 1)
	headerStyle = lipgloss.NewStyle().
			Padding(0, 0, 1, 0)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246"))

	tagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	chipMutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	chipAccentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)

	chipWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	activityLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244"))

	userLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244"))

	systemLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))

	userCardStyle      = lipgloss.NewStyle()
	assistantCardStyle = lipgloss.NewStyle()
	systemCardStyle    = lipgloss.NewStyle()
	errorCardStyle     = lipgloss.NewStyle()

	logLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Bold(true)

	errorLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	messageBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	logBodyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	errorBodyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	codeBodyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectBodyStyle  = lipgloss.NewStyle().Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 0, 1, 0)

	statusVersionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "252"})

	paneTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242")).
			Padding(0, 0, 1, 0)

	todoLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	todoBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246"))

	approvalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(1).
			Background(lipgloss.Color("236"))

	inputPaneStyle = lipgloss.NewStyle().
			Padding(1, 0, 1, 0)

	inputTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Bold(true)

	inputHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	codeBlockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	conversationStyle = lipgloss.NewStyle().
				Padding(0, 0, 1, 0)
)

func newChatTUIModel(runtime *chatRuntime, events chan tea.Msg) chatTUIModel {
	ta := textarea.New()
	ta.Placeholder = i18n.T("tui.placeholder.default")
	ta.Focus()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))

	chatVP := viewport.New(0, 0)
	chatVP.MouseWheelEnabled = true

	m := chatTUIModel{
		runtime:      runtime,
		events:       events,
		input:        ta,
		spinner:      sp,
		chatViewport: chatVP,
		keys: chatKeyMap{
			Send:      key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "send")),
			Newline:   key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "newline")),
			Interrupt: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "interrupt")),
			Quit:      key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
		entriesDirty:  true,
		stickToBottom: true,
	}

	for _, msg := range runtime.session.Messages() {
		switch msg.Role {
		case "user":
			m.entries = append(m.entries, tuiEntry{role: "user", text: model.ContentString(msg.Content)})
		case "assistant":
			text := strings.TrimSpace(model.ContentString(msg.Content))
			if text != "" {
				m.entries = append(m.entries, tuiEntry{role: "assistant", text: formatRunOutput(text, runtime.outputMode)})
			}
		}
	}
	m.statusText = i18n.T("tui.status.ready")
	m.refreshInputState()
	m.refreshViewports(false)
	return m
}

func (m *chatTUIModel) reloadConversationFromSession() {
	m.entries = m.entries[:0]
	for _, msg := range m.runtime.session.Messages() {
		switch msg.Role {
		case "user":
			m.entries = append(m.entries, tuiEntry{role: "user", text: model.ContentString(msg.Content)})
		case "assistant":
			text := strings.TrimSpace(model.ContentString(msg.Content))
			if text != "" {
				m.entries = append(m.entries, tuiEntry{role: "assistant", text: formatRunOutput(text, m.runtime.outputMode)})
			}
		}
	}
	m.entriesDirty = true
	m.entriesWidth = 0
	m.chatViewport.SetContent("")
	m.chatViewport.SetYOffset(0)
	m.stickToBottom = true
}

func runChatTUI(runtime *chatRuntime) int {
	events := make(chan tea.Msg, 256)
	approver := newTUIApprover(runtime.cfg.ApprovalMode, events)
	runtime.approver = approver
	var jobs tools.JobControl
	if runtime.jobs != nil {
		jobs = jobToolAdapter{manager: runtime.jobs}
	}
	runtime.loop = buildAgentWith(runtime.cfg, approver, &tuiLogWriter{events: events}, jobs, runtime.permissions, &tuiAuditSink{events: events})
	runtime.attachAgentEventSink()
	runtime.session.SetAgent(runtime.loop)
	if runtime.jobs != nil {
		runtime.jobs.SetNotifier(func(text string) {
			events <- tuiJobUpdateMsg{text: text}
		})
	}

	p := tea.NewProgram(
		newChatTUIModel(runtime, events),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithFilter(chatMouseEventFilter),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("usage.error.ui"), err)
		runtime.beforeExit(false)
		return 1
	}
	return 0
}

func waitForTUIEvent(events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

func scheduleViewportRefresh() tea.Cmd {
	return tea.Tick(time.Second/30, func(time.Time) tea.Msg {
		return tuiRefreshMsg{}
	})
}

func runTaskCmd(runtime *chatRuntime, events chan tea.Msg, ctx context.Context, task string) tea.Cmd {
	return func() tea.Msg {
		output, err := runtime.executeTaskStreaming(ctx, task, func(token string) {
			events <- tuiStreamMsg{token: token}
		})
		events <- tuiTaskDoneMsg{task: task, output: output, err: err}
		return nil
	}
}

func (m chatTUIModel) Init() tea.Cmd {
	return tea.Batch(waitForTUIEvent(m.events), m.spinner.Tick)
}

func (m chatTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	keyHandled := false
	mouseHandled := false
	immediateRefresh := false
	forwardInput := shouldForwardToInput(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		immediateRefresh = true
	case tea.InterruptMsg:
		m.runtime.beforeExit(false)
		return m, tea.Quit
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			keyHandled = true
			m.runtime.beforeExit(false)
			return m, tea.Quit
		case key.Matches(msg, m.keys.Interrupt):
			keyHandled = true
			if m.interruptForeground() {
				m.entries = append(m.entries, tuiEntry{role: "system", text: i18n.T("cmd.interrupt.ok")})
				m.entriesDirty = true
				immediateRefresh = true
			}
		case key.Matches(msg, m.keys.Newline):
			keyHandled = true
			m.input.InsertString("\n")
		case key.Matches(msg, m.keys.Send):
			keyHandled = true
			task := strings.TrimSpace(m.input.Value())
			if task == "" {
				break
			}
			if m.approval != nil {
				m.input.Reset()
				m.entries = append(m.entries, tuiEntry{role: "user", text: task})
				if answer, ok := parseApprovalAnswer(task); ok {
					m.approval.response <- answer
					m.entries = append(m.entries, tuiEntry{role: "system", text: approvalResponseText(answer)})
					m.statusText = approvalStatusText(answer)
					m.approval = nil
				} else {
					m.entries = append(m.entries, tuiEntry{role: "approval", text: i18n.T("tui.approval.waiting")})
					m.statusText = i18n.T("tui.status.approval")
				}
				m.entriesDirty = true
				m.refreshInputState()
				immediateRefresh = true
				break
			}
			if m.busy && !strings.HasPrefix(task, "/") {
				m.input.Reset()
				m.entries = append(m.entries, tuiEntry{role: "user", text: task})
				if err := m.runtime.enqueueSteeringMessage(task); err != nil {
					m.entries = append(m.entries, tuiEntry{role: "error", text: err.Error()})
					m.statusText = i18n.T("tui.status.error")
				} else {
					m.entries = append(m.entries, tuiEntry{role: "system", text: "queued steering message"})
					m.statusText = i18n.T("tui.status.running")
				}
				m.entriesDirty = true
				m.refreshInputState()
				immediateRefresh = true
				break
			}
			m.input.Reset()
			if strings.HasPrefix(task, "/") {
				result := m.runtime.executeCommand(task)
				if result.handled {
					if result.reloadSessionView {
						m.reloadConversationFromSession()
					}
					if strings.TrimSpace(result.output) != "" {
						m.entries = append(m.entries, tuiEntry{role: "system", text: result.output})
						m.entriesDirty = true
						immediateRefresh = true
					}
					m.refreshInputState()
					if result.exitCode >= 0 {
						m.runtime.beforeExit(true)
						return m, tea.Quit
					}
					break
				}
			}

			m.entries = append(m.entries, tuiEntry{role: "user", text: task})
			m.entriesDirty = true
			immediateRefresh = true
			cmds = append(cmds, m.startTask(task))
		}
	case tea.MouseMsg:
		mouseHandled = true
		if shouldThrottleMouse(msg, m.lastMouseTime) {
			break
		}
		if isBurstMouseMsg(msg) {
			m.lastMouseTime = time.Now()
		}
		if delta, ok := mouseScrollDelta(msg, m.chatViewport.MouseWheelDelta); ok {
			if delta < 0 {
				m.stickToBottom = false
			}
			applyMouseScroll(&m.chatViewport, delta)
			if delta > 0 {
				m.stickToBottom = m.chatViewport.AtBottom()
			}
			break
		}
		if isLeftMousePress(msg) {
			if line, col, ok := m.contentPosAtMouse(msg); ok {
				m.selActive = true
				m.selStartLine = line
				m.selStartCol = col
				m.selEndLine = line
				m.selEndCol = col
				immediateRefresh = true
			}
			break
		}
		if m.selActive && msg.Action == tea.MouseActionMotion {
			// Dragging near viewport boundaries auto-scrolls, matching crush behavior.
			if m.chatViewport.Height > 0 {
				if msg.Y <= m.chatTop {
					m.stickToBottom = false
					m.chatViewport.ScrollUp(1)
				} else if msg.Y >= m.chatTop+m.chatViewport.Height-1 {
					m.chatViewport.ScrollDown(1)
					m.stickToBottom = m.chatViewport.AtBottom()
				}
			}
			if line, col, ok := m.contentPosAtMouse(msg); ok {
				if line != m.selEndLine || col != m.selEndCol {
					m.selEndLine = line
					m.selEndCol = col
					immediateRefresh = true
				}
			}
			break
		}
		if m.selActive && isLeftMouseRelease(msg) {
			if line, col, ok := m.contentPosAtMouse(msg); ok {
				m.selEndLine = line
				m.selEndCol = col
			}
			text := m.selectedContentText()
			if cmd := copyTextCmd(text); cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.selActive = false
			immediateRefresh = true
		}
	case tuiCopyResultMsg:
		if msg.ok {
			m.statusText = "copied"
		} else {
			m.statusText = "copy failed"
			if strings.TrimSpace(msg.detail) != "" {
				m.entries = append(m.entries, tuiEntry{role: "system", text: "copy failed: " + msg.detail})
				m.entriesDirty = true
			}
		}
		immediateRefresh = true
	case tuiLogMsg:
		m.stepText = nextStepStatus(m.stepText, msg.kind, msg.text)
		m.appendActivityEntry(msg.kind, msg.text)
		m.entriesDirty = true
		if !m.refreshQueued {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiApprovalMsg:
		m.approval = &msg
		m.entries = append(m.entries, tuiEntry{role: "approval", text: renderApprovalInlineText(&msg)})
		m.statusText = i18n.T("tui.status.approval")
		m.entriesDirty = true
		m.refreshInputState()
		immediateRefresh = true
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiTaskDoneMsg:
		m.busy = false
		m.runningTask = ""
		m.runtime.clearForegroundCancel(m.runningCancel)
		m.runningCancel = nil
		m.refreshQueued = false
		// Finalize any streamed assistant text using the active output mode so
		// dedupe compares against the same normalized content as the final result.
		for i := range m.entries {
			if m.entries[i].role == "streaming" {
				m.entries[i].text = formatRunOutput(m.entries[i].text, m.runtime.outputMode)
				m.entries[i].role = "assistant"
			}
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.entries = append(m.entries, tuiEntry{role: "system", text: i18n.T("cmd.interrupt.done")})
				m.statusText = i18n.T("tui.status.ready")
			} else {
				m.entries = append(m.entries, tuiEntry{role: "error", text: msg.err.Error()})
				m.statusText = i18n.T("tui.status.error")
			}
		} else {
			// Only add if no streaming entry captured the output
			hasStreamed := false
			for _, e := range m.entries {
				if e.role == "assistant" && strings.TrimSpace(e.text) == strings.TrimSpace(msg.output) {
					hasStreamed = true
					break
				}
			}
			if !hasStreamed {
				m.entries = append(m.entries, tuiEntry{role: "assistant", text: msg.output})
			}
			m.statusText = i18n.T("tui.status.ready")
		}
		m.entriesDirty = true
		m.refreshInputState()
		immediateRefresh = true
		if next, ok := m.dequeueTask(); ok {
			m.entries = append(m.entries, tuiEntry{role: "system", text: queuedTaskStartingText(next, len(m.queuedTasks))})
			m.entriesDirty = true
			cmds = append(cmds, m.startTask(next))
		}
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiJobUpdateMsg:
		m.entries = append(m.entries, tuiEntry{role: "system", text: msg.text})
		m.statusText = i18n.T("tui.status.bg.update")
		m.entriesDirty = true
		if !m.refreshQueued {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiStreamMsg:
		if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "streaming" {
			m.entries = append(m.entries, tuiEntry{role: "streaming", text: msg.token})
		} else {
			m.entries[len(m.entries)-1].text += msg.token
		}
		m.entriesDirty = true
		if !m.refreshQueued {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiRefreshMsg:
		m.refreshQueued = false
		immediateRefresh = true
		if m.entriesDirty {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var cmd tea.Cmd
	if forwardInput && !keyHandled && !mouseHandled {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		// Dynamic input height
		newHeight := m.desiredInputHeight()
		if newHeight != m.input.Height() {
			m.input.SetHeight(newHeight)
		}
	}
	m.resize(immediateRefresh)
	return m, tea.Batch(cmds...)
}

func (m chatTUIModel) View() string {
	header := m.renderHeader()

	content := m.renderConversation()

	parts := []string{
		header,
		m.renderTodoSummary(),
		content,
		m.renderComposer(),
		m.renderStatusLine(),
	}
	view := appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	lines := strings.Split(strings.ReplaceAll(view, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func (m chatTUIModel) renderStatusLine() string {
	statusParts := []string{
		statusVersionStyle.Render(strings.TrimSpace(version)),
		contextStatus(m.runtime, m.input.Value()),
	}
	if m.busy {
		statusParts = append(statusParts, m.spinner.View()+" "+m.statusText)
	} else {
		statusParts = append(statusParts, m.statusText)
	}
	if strings.TrimSpace(m.stepText) != "" {
		statusParts = append(statusParts, chipAccentStyle.Render(m.stepText))
	}
	return statusStyle.Width(max(0, m.width-2)).Render(strings.Join(statusParts, "  "))
}

func (m *chatTUIModel) resize(forceRefresh bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	footerHeight := 1
	extra := 7 + footerHeight + m.input.Height()
	contentWidth := max(20, m.width-4)
	availableHeight := m.height - extra
	m.chatViewport.Height = max(6, availableHeight)
	if m.runtime != nil {
		m.chatTop = lipgloss.Height(m.renderHeader())
		if todo := strings.TrimSpace(m.renderTodoSummary()); todo != "" {
			m.chatTop += lipgloss.Height(todo)
		}
	} else {
		m.chatTop = 0
	}
	newChatWidth := max(18, contentWidth)
	widthChanged := m.chatViewport.Width != newChatWidth
	m.chatViewport.Width = newChatWidth
	m.input.SetWidth(max(20, contentWidth-4))
	if widthChanged {
		m.entriesDirty = true
	}
	if widthChanged || forceRefresh {
		m.refreshViewports(forceRefresh)
	}
}

func (m *chatTUIModel) refreshViewports(forceReanchor bool) {
	if m.chatViewport.Width > 0 {
		if m.entriesDirty || m.entriesWidth != m.chatViewport.Width {
			offset := m.chatViewport.YOffset
			m.chatViewport.SetContent(m.renderEntries())
			if m.stickToBottom {
				m.chatViewport.GotoBottom()
			} else {
				m.chatViewport.SetYOffset(offset)
			}
			m.entriesDirty = false
			m.entriesWidth = m.chatViewport.Width
		} else if forceReanchor && m.stickToBottom {
			m.chatViewport.GotoBottom()
		}
	}
}

func (m *chatTUIModel) renderEntries() string {
	if m.chatViewport.Width <= 0 {
		return ""
	}
	bodyWidth := max(20, m.chatViewport.Width-3)
	visibleEntries := m.visibleConversationEntries()
	if len(m.entryKeys) != len(visibleEntries) {
		m.entryKeys = make([]string, len(visibleEntries))
		m.entryBlocks = make([]string, len(visibleEntries))
	}
	for i, entry := range visibleEntries {
		selected := m.isEntrySelected(i)
		key := fmt.Sprintf("%d|%s|%t|%s", bodyWidth, entry.role, selected, entry.text)
		if m.entryKeys[i] == key && m.entryBlocks[i] != "" {
			continue
		}

		var label lipgloss.Style
		var body lipgloss.Style
		switch entry.role {
		case "user":
			label = userLabelStyle
			body = messageBodyStyle
		case "assistant", "streaming":
			label = assistantLabelStyle
			body = codeBodyStyle
		case "activity":
			label = activityLabelStyle
			body = logBodyStyle
		case "system":
			label = systemLabelStyle
			body = messageBodyStyle
		case "approval":
			label = systemLabelStyle
			body = messageBodyStyle
		case "error":
			label = errorLabelStyle
			body = errorBodyStyle
		default:
			label = logLabelStyle
			body = logBodyStyle
		}
		text := strings.TrimSpace(entry.text)
		if text == "" {
			m.entryKeys[i] = key
			m.entryBlocks[i] = ""
			continue
		}
		if entry.role == "approval" {
			block := lipgloss.JoinVertical(
				lipgloss.Left,
				label.Render("approval"),
				approvalStyle.Width(bodyWidth).Render(text),
			)
			m.entryKeys[i] = key
			m.entryBlocks[i] = block
			continue
		}
		block := lipgloss.JoinVertical(
			lipgloss.Left,
			label.Render(entry.role),
			m.renderEntryBody(entry, body, bodyWidth, selected),
		)
		m.entryKeys[i] = key
		m.entryBlocks[i] = block
	}

	rendered := make([]string, 0, len(m.entryBlocks))
	lines := make([]int, 0, len(m.entryBlocks)*4)
	for idx, block := range m.entryBlocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		rendered = append(rendered, block)
		height := max(1, lipgloss.Height(block))
		for i := 0; i < height; i++ {
			lines = append(lines, idx)
		}
		lines = append(lines, -1, -1)
	}
	if len(lines) >= 2 {
		lines = lines[:len(lines)-2]
	}
	m.entryLines = lines
	return strings.Join(rendered, "\n\n")
}

func (m *chatTUIModel) renderEntryBody(entry tuiEntry, style lipgloss.Style, width int, selected bool) string {
	text := strings.TrimSpace(entry.text)
	if text == "" {
		return ""
	}
	var body string
	if entry.role == "assistant" {
		body = m.renderMarkdownBody(text, width)
	} else {
		body = renderStyledBody(style, width, text)
	}
	if selected {
		// Keep selected content plain to avoid markdown ANSI resetting selection
		// background, which can make highlighting invisible.
		body = renderStyledBody(selectBodyStyle, width, text)
	}
	return body
}

func (m *chatTUIModel) renderMarkdownBody(text string, width int) string {
	renderer, err := m.markdownRenderer(width)
	if err != nil {
		return renderStyledBody(codeBodyStyle, width, text)
	}
	out, err := renderer.Render(text)
	if err != nil {
		return renderStyledBody(codeBodyStyle, width, text)
	}
	return strings.TrimRight(out, "\n")
}

func (m *chatTUIModel) markdownRenderer(width int) (*glamour.TermRenderer, error) {
	if m.mdRenderer != nil && m.mdWidth == width {
		return m.mdRenderer, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	m.mdRenderer = r
	m.mdWidth = width
	return r, nil
}

func highlightRenderedBody(width int, text string) string {
	lines := strings.Split(text, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, selectBodyStyle.Width(width).Render(line))
	}
	return strings.Join(rendered, "\n")
}

func renderStyledBody(style lipgloss.Style, width int, text string) string {
	lines := strings.Split(text, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, style.Width(width).Render(line))
	}
	return strings.Join(rendered, "\n")
}

func contextStatus(runtime *chatRuntime, input string) string {
	window := runtime.cfg.ContextWindow
	if runtime.modelContextWindow > 0 {
		window = runtime.modelContextWindow
	}
	if window <= 0 {
		window = 32768
	}
	used := estimateTokenUsage(runtime.session.Messages(), input)
	if used < 0 {
		used = 0
	}
	remaining := window - used
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("ctx %s/%s tok  left %s", compactTokenCount(used), compactTokenCount(window), compactTokenCount(remaining))
}

func compactTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.2fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func estimateTokenUsage(messages []model.Message, draft string) int {
	count := 0
	for _, msg := range messages {
		text := model.ContentString(msg.Content) + msg.Role + msg.ToolCallID
		for _, call := range msg.ToolCalls {
			text += call.ID + call.Type + call.Function.Name + call.Function.Arguments
		}
		count += estimateStringTokens(text)
	}
	count += estimateStringTokens(draft)
	return count
}

func estimateStringTokens(s string) int {
	count := 0
	for _, r := range s {
		if isCJK(r) {
			count += 6
		} else {
			count++
		}
	}
	return count / 4
}

func isCJK(r rune) bool {
	return (r >= 0x2E80 && r <= 0x9FFF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0x20000 && r <= 0x2FA1F)
}

func mouseScrollDelta(msg tea.MouseMsg, step int) (int, bool) {
	if msg.Action != tea.MouseActionPress {
		return 0, false
	}
	step = max(1, step)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return -step, true
	case tea.MouseButtonWheelDown:
		return step, true
	default:
		return 0, false
	}
}

func applyMouseScroll(vp *viewport.Model, delta int) {
	switch {
	case delta < 0:
		vp.ScrollUp(-delta)
	case delta > 0:
		vp.ScrollDown(delta)
	}
}

func isBurstMouseMsg(msg tea.MouseMsg) bool {
	if msg.Action == tea.MouseActionMotion {
		return true
	}
	_, ok := mouseScrollDelta(msg, 1)
	return ok
}

func shouldThrottleMouse(msg tea.MouseMsg, last time.Time) bool {
	if !isBurstMouseMsg(msg) {
		return false
	}
	if last.IsZero() {
		return false
	}
	return time.Since(last) < 15*time.Millisecond
}

func isLeftMousePress(msg tea.MouseMsg) bool {
	return msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
}

func isLeftMouseRelease(msg tea.MouseMsg) bool {
	return msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft
}

func copyTextCmd(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return func() tea.Msg {
		oscErr := writeOSC52(text)
		sysErr := clipboard.WriteAll(text)
		if oscErr == nil || sysErr == nil {
			return tuiCopyResultMsg{ok: true}
		}
		return tuiCopyResultMsg{
			ok:     false,
			detail: fmt.Sprintf("osc52=%v; system=%v", oscErr, sysErr),
		}
	}
}

func buildOSC52Sequence(text string) string {
	seq := osc52.New(text)
	if strings.TrimSpace(os.Getenv("TMUX")) != "" {
		seq = seq.Tmux()
	} else if strings.TrimSpace(os.Getenv("STY")) != "" {
		seq = seq.Screen()
	}
	return seq.String()
}

func writeOSC52(text string) error {
	_, err := io.WriteString(os.Stdout, buildOSC52Sequence(text))
	return err
}

func selectedEntryText(entries []tuiEntry, from, to int) string {
	if len(entries) == 0 {
		return ""
	}
	if from > to {
		from, to = to, from
	}
	if from < 0 {
		from = 0
	}
	if to >= len(entries) {
		to = len(entries) - 1
	}
	if from > to {
		return ""
	}
	parts := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		text := strings.TrimSpace(entries[i].text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func selectedEntryRange(from, to, total int) (int, int, bool) {
	if total <= 0 {
		return 0, 0, false
	}
	if from > to {
		from, to = to, from
	}
	if from < 0 {
		from = 0
	}
	if to >= total {
		to = total - 1
	}
	if from > to {
		return 0, 0, false
	}
	return from, to, true
}

func (m chatTUIModel) isEntrySelected(idx int) bool {
	if !m.mouseDragOn {
		return false
	}
	start, end, ok := selectedEntryRange(m.mouseDragFrom, m.mouseDragTo, len(m.entries))
	if !ok {
		return false
	}
	return idx >= start && idx <= end
}

func (m chatTUIModel) contentPosAtMouse(msg tea.MouseMsg) (int, int, bool) {
	if m.chatViewport.Height <= 0 {
		return 0, 0, false
	}
	relY := msg.Y - m.chatViewportTop()
	if relY < 0 || relY >= m.chatViewport.Height {
		return 0, 0, false
	}
	line := m.chatViewport.YOffset + relY
	col := max(0, msg.X-1)
	return line, col, true
}

func normalizeSelection(startLine, startCol, endLine, endCol int) (int, int, int, int) {
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		return endLine, endCol, startLine, startCol
	}
	return startLine, startCol, endLine, endCol
}

func sliceByDisplayCols(s string, fromCol, toCol int) string {
	if toCol <= fromCol {
		return ""
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w <= 0 {
			w = 1
		}
		next := col + w
		if next > fromCol && col < toCol {
			b.WriteRune(r)
		}
		col = next
		if col >= toCol {
			break
		}
	}
	return b.String()
}

func (m chatTUIModel) selectedContentText() string {
	content := m.renderEntries()
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}
	startLine, startCol, endLine, endCol := normalizeSelection(m.selStartLine, m.selStartCol, m.selEndLine, m.selEndCol)
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}
	if startLine > endLine {
		return ""
	}
	out := make([]string, 0, endLine-startLine+1)
	for i := startLine; i <= endLine; i++ {
		plain := ansi.Strip(lines[i])
		width := runewidth.StringWidth(plain)
		from := 0
		to := width
		if i == startLine {
			from = min(max(0, startCol), width)
		}
		if i == endLine {
			to = min(max(0, endCol), width)
		}
		if i == startLine && i == endLine && to <= from {
			from = 0
			to = width
		}
		out = append(out, sliceByDisplayCols(plain, from, to))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (m chatTUIModel) highlightViewportSelection(view string) string {
	lines := strings.Split(view, "\n")
	startLine, _, endLine, _ := normalizeSelection(m.selStartLine, m.selStartCol, m.selEndLine, m.selEndCol)
	for i := range lines {
		globalLine := m.chatViewport.YOffset + i
		if globalLine < startLine || globalLine > endLine {
			continue
		}
		plain := ansi.Strip(lines[i])
		lines[i] = selectBodyStyle.Render(plain)
	}
	return strings.Join(lines, "\n")
}

func (m chatTUIModel) chatViewportTop() int {
	top := m.chatTop
	if top > 0 {
		return top
	}
	top = lipgloss.Height(m.renderHeader())
	if todo := strings.TrimSpace(m.renderTodoSummary()); todo != "" {
		top += lipgloss.Height(todo)
	}
	return top
}

func (m chatTUIModel) entryIndexAtMouse(msg tea.MouseMsg) (int, bool) {
	if m.chatViewport.Height <= 0 {
		return -1, false
	}
	relY := msg.Y - m.chatViewportTop()
	if relY < 0 || relY >= m.chatViewport.Height {
		return -1, false
	}
	line := m.chatViewport.YOffset + relY
	if line < 0 || line >= len(m.entryLines) {
		return -1, false
	}
	idx := m.entryLines[line]
	if idx < 0 || idx >= len(m.entries) {
		return -1, false
	}
	return idx, true
}

func shouldForwardToInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, tea.WindowSizeMsg:
		return true
	default:
		return false
	}
}

var lastFilteredMouseEvent time.Time

func chatMouseEventFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	mouseMsg, ok := msg.(tea.MouseMsg)
	if !ok {
		return msg
	}
	if !isBurstMouseMsg(mouseMsg) {
		return msg
	}
	now := time.Now()
	if !lastFilteredMouseEvent.IsZero() && now.Sub(lastFilteredMouseEvent) < 12*time.Millisecond {
		return nil
	}
	lastFilteredMouseEvent = now
	return msg
}

func classifyLogKind(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.Contains(lower, "error"):
		return "error"
	case strings.Contains(lower, "run_command") || strings.Contains(lower, "read_file") || strings.Contains(lower, "write_file") || strings.Contains(lower, "grep") || strings.Contains(lower, "web_search") || strings.Contains(lower, "fetch_url") || strings.Contains(lower, "list_files"):
		return "tools"
	case strings.Contains(lower, "requesting model") || strings.Contains(lower, "model response") || strings.Contains(lower, "executing "):
		return "steps"
	default:
		return "steps"
	}
}

func renderApprovalBody(text string, width int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ": ") {
			parts := strings.SplitN(line, ": ", 2)
			rendered = append(rendered,
				lipgloss.JoinHorizontal(lipgloss.Top,
					systemLabelStyle.Render(parts[0]+": "),
					messageBodyStyle.Width(max(10, width-len(parts[0])-2)).Render(parts[1]),
				),
			)
			continue
		}
		rendered = append(rendered, messageBodyStyle.Width(width).Render(line))
	}
	return strings.Join(rendered, "\n")
}

func renderApprovalModal(msg *tuiApprovalMsg, width int) string {
	parts := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render(msg.title),
	}

	titleLower := strings.ToLower(strings.TrimSpace(msg.title))
	switch {
	case strings.Contains(titleLower, "command"):
		parts = append(parts,
			inputHintStyle.Render("The agent wants to execute this command:"),
			codeBlockStyle.Width(width).Render(strings.TrimSpace(msg.body)),
		)
	case strings.Contains(titleLower, "file write"):
		fields := parseApprovalFields(msg.body)
		if path := fields["path"]; path != "" {
			parts = append(parts, renderApprovalField("path", path, width))
		}
		if bytes := fields["bytes"]; bytes != "" {
			parts = append(parts, renderApprovalField("bytes", bytes, width))
		}
		if preview := fields["preview"]; preview != "" {
			preview = strings.ReplaceAll(preview, "\\n", "\n")
			parts = append(parts,
				renderApprovalField("preview", "", width),
				codeBlockStyle.Width(width).Render(preview),
			)
		}
	default:
		parts = append(parts, renderApprovalBody(msg.body, width))
	}

	parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(i18n.T("tui.approval.bar")))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func renderApprovalField(name, value string, width int) string {
	if strings.TrimSpace(value) == "" {
		return systemLabelStyle.Render(name)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		systemLabelStyle.Render(name+": "),
		messageBodyStyle.Width(max(10, width-len(name)-2)).Render(value),
	)
}

func parseApprovalFields(body string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ": ") {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		fields[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
	}
	return fields
}

func (m chatTUIModel) renderHeader() string {
	chips := []string{
		titleStyle.Render("tacli"),
		chipAccentStyle.Render("version " + strings.TrimSpace(version)),
		chipAccentStyle.Render(m.runtime.cfg.Model),
		chipMutedStyle.Render(m.runtime.sessionName),
	}
	if m.runtime.approver.Mode() == tools.ApprovalDangerously {
		chips = append(chips, chipWarnStyle.Render("dangerously"))
	} else {
		chips = append(chips, chipMutedStyle.Render("confirm"))
	}
	return headerStyle.Width(max(20, m.width-2)).Render(strings.Join(chips, "  ·  "))
}

func (m chatTUIModel) renderConversation() string {
	visibleEntries := m.visibleConversationEntries()
	title := paneTitleStyle.Width(max(20, m.chatViewport.Width)).Render(
		fmt.Sprintf(i18n.T("tui.label.messages"), len(visibleEntries)),
	)
	view := m.chatViewport.View()
	if m.selActive {
		view = m.highlightViewportSelection(view)
	}
	return conversationStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, view))
}

func (m chatTUIModel) renderTodoSummary() string {
	items := m.runtime.loop.TodoItems()
	if len(items) == 0 {
		return ""
	}
	width := max(20, m.width-2)
	lines := []string{todoLabelStyle.Render(i18n.T("tui.label.plan"))}
	for _, item := range items {
		lines = append(lines, todoBodyStyle.Width(width).Render(todoLine(item)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m chatTUIModel) renderComposer() string {
	lines := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", max(20, m.width-2))),
	}
	if hint := strings.TrimSpace(m.composerHint()); hint != "" {
		lines = append(lines, inputHintStyle.Render(hint))
	}
	lines = append(lines, m.input.View())
	return inputPaneStyle.Width(max(20, m.width-2)).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}

func todoLine(item tools.TodoItem) string {
	switch item.Status {
	case "completed":
		return "[done] " + item.Text
	case "in_progress":
		return "[doing] " + item.Text
	default:
		return "[todo] " + item.Text
	}
}

func (m *chatTUIModel) refreshInputState() {
	m.input.Prompt = ""
	m.input.SetHeight(m.desiredInputHeight())
	switch {
	case m.approval != nil:
		m.input.Placeholder = i18n.T("tui.placeholder.approval")
	case m.busy:
		m.input.Placeholder = busyPlaceholder(len(m.queuedTasks))
	default:
		m.input.Placeholder = i18n.T("tui.placeholder.default")
	}
}

func (m chatTUIModel) desiredInputHeight() int {
	lines := max(1, strings.Count(m.input.Value(), "\n")+1)
	if m.height <= 0 {
		return lines
	}
	footerHeight := 1
	maxVisible := m.height - (7 + footerHeight + 6)
	if maxVisible < 1 {
		maxVisible = 1
	}
	return min(lines, maxVisible)
}

func (m chatTUIModel) composerHint() string {
	switch {
	case m.approval != nil:
		return i18n.T("tui.hint.approval")
	case m.busy:
		return busyHint(len(m.queuedTasks))
	default:
		return i18n.T("tui.hint.send")
	}
}

func (m *chatTUIModel) startTask(task string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.busy = true
	m.runningTask = task
	m.runningCancel = cancel
	m.runtime.setForegroundCancel(cancel)
	m.statusText = i18n.T("tui.status.running")
	m.refreshInputState()
	return runTaskCmd(m.runtime, m.events, ctx, task)
}

func (m *chatTUIModel) enqueueTask(task string) {
	m.queuedTasks = append(m.queuedTasks, task)
	m.statusText = queuedTaskAddedText(len(m.queuedTasks))
	m.entries = append(m.entries, tuiEntry{role: "system", text: queuedTaskAddedText(len(m.queuedTasks))})
}

func (m *chatTUIModel) dequeueTask() (string, bool) {
	if len(m.queuedTasks) == 0 {
		return "", false
	}
	task := m.queuedTasks[0]
	m.queuedTasks = m.queuedTasks[1:]
	return task, true
}

func (m *chatTUIModel) interruptForeground() bool {
	if !m.busy || m.runningCancel == nil {
		return false
	}
	m.runtime.interruptForegroundTask()
	m.statusText = i18n.T("cmd.interrupt.pending")
	m.refreshInputState()
	return true
}

func busyPlaceholder(queued int) string {
	base := strings.TrimSpace(i18n.T("tui.placeholder.busy"))
	if queued <= 0 {
		return base
	}
	return fmt.Sprintf("%s queued=%d", base, queued)
}

func busyHint(queued int) string {
	base := strings.TrimSpace(i18n.T("tui.hint.busy"))
	if queued <= 0 {
		return base
	}
	return fmt.Sprintf("%s queued=%d", base, queued)
}

func queuedTaskAddedText(count int) string {
	if count <= 1 {
		return i18n.T("cmd.queue.added.one")
	}
	return fmt.Sprintf(i18n.T("cmd.queue.added.many"), count)
}

func queuedTaskStartingText(task string, remaining int) string {
	text := fmt.Sprintf(i18n.T("cmd.queue.starting"), task)
	if remaining > 0 {
		text += fmt.Sprintf(" (%s)", fmt.Sprintf(i18n.T("cmd.queue.remaining"), remaining))
	}
	return text
}

func (m *chatTUIModel) appendActivityEntry(kind, text string) {
	text = sanitizeUserVisibleLogLine(text)
	if text == "" {
		return
	}

	switch {
	case shouldSkipInlineActivity(kind, text):
		return
	case kind == "error":
		m.entries = append(m.entries, tuiEntry{role: "error", text: text})
	case isToolStartLine(kind, text):
		m.entries = append(m.entries, tuiEntry{role: "activity", text: "tool  " + text})
	case isToolResultLine(text):
		if m.mergeLastActivityLine(text) {
			return
		}
		m.entries = append(m.entries, tuiEntry{role: "activity", text: "tool\n" + normalizeActivityContinuation(text)})
	case kind == "audit":
		m.entries = append(m.entries, tuiEntry{role: "activity", text: "[audit] " + strings.TrimSpace(text)})
	default:
		m.entries = append(m.entries, tuiEntry{role: "activity", text: formatInlineLogEntry(kind, text)})
	}
}

func shouldSkipInlineActivity(kind, text string) bool {
	if kind != "steps" {
		return false
	}
	if isHiddenActivityLogLine(text) {
		return true
	}
	return strings.Contains(strings.ToLower(text), "executing ")
}

func isToolStartLine(kind, text string) bool {
	return kind == "tools"
}

func isToolResultLine(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "ok in ") ||
		strings.HasPrefix(text, "error in ") ||
		strings.HasPrefix(text, "| ")
}

func normalizeActivityContinuation(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "| ") {
		return strings.TrimSpace(strings.TrimPrefix(text, "| "))
	}
	return text
}

func (m *chatTUIModel) mergeLastActivityLine(text string) bool {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].role == "activity" {
			m.entries[i].text = strings.TrimSpace(m.entries[i].text) + "\n" + normalizeActivityContinuation(text)
			return true
		}
		if m.entries[i].role != "system" {
			break
		}
	}
	return false
}

func formatInlineLogEntry(kind, text string) string {
	label := "step"
	switch kind {
	case "tools":
		label = "tool"
	case "error":
		label = "error"
	case "approval":
		label = "approval"
	case "audit":
		label = "audit"
	}
	return "[" + label + "] " + strings.TrimSpace(text)
}

func nextStepStatus(current, kind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return current
	}
	if strings.HasPrefix(text, "| ") {
		return current
	}
	if kind == "tools" || isToolResultLine(text) {
		return text
	}
	if isHiddenActivityLogLine(text) {
		return current
	}
	if strings.Contains(strings.ToLower(text), "executing ") {
		return text
	}
	return current
}

func isHiddenActivityLogLine(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "requesting model") ||
		strings.Contains(lower, "model response")
}

func (m chatTUIModel) visibleConversationEntries() []tuiEntry {
	return m.entries
}

func sanitizeUserVisibleLogLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	sanitized := fields[:0]
	for _, field := range fields {
		if strings.HasPrefix(field, "id=") {
			continue
		}
		sanitized = append(sanitized, field)
	}
	return strings.Join(sanitized, " ")
}

func renderApprovalInlineText(msg *tuiApprovalMsg) string {
	if msg == nil {
		return ""
	}

	lines := []string{}
	titleLower := strings.ToLower(strings.TrimSpace(msg.title))
	switch {
	case strings.Contains(titleLower, "command"):
		lines = append(lines, i18n.T("tui.approval.command"))
	case strings.Contains(titleLower, "file write"):
		lines = append(lines, i18n.T("tui.approval.write"))
		fields := parseApprovalFields(msg.body)
		if path := fields["path"]; path != "" {
			lines = append(lines, "path: "+path)
		}
		if bytes := fields["bytes"]; bytes != "" {
			lines = append(lines, "bytes: "+bytes)
		}
	default:
		lines = append(lines, i18n.T("tui.approval.general"))
	}
	lines = append(lines, i18n.T("tui.approval.hint"))
	return strings.Join(lines, "\n")
}

func parseApprovalAnswer(text string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "y", "yes":
		return "yes", true
	case "n", "no":
		return "no", true
	case "a", "always", "dangerously":
		return "always", true
	default:
		return "", false
	}
}

func approvalStatusText(answer string) string {
	switch answer {
	case "yes":
		return i18n.T("tui.approval.granted")
	case "always":
		return i18n.T("tui.approval.mode")
	default:
		return i18n.T("tui.approval.denied")
	}
}

func approvalResponseText(answer string) string {
	switch answer {
	case "yes":
		return i18n.T("tui.approval.granted")
	case "always":
		return i18n.T("tui.approval.always")
	default:
		return i18n.T("tui.approval.denied")
	}
}

var _ io.Writer = (*tuiLogWriter)(nil)
var _ tools.Approver = (*tuiApprover)(nil)
var _ tools.ToolAuditSink = (*tuiAuditSink)(nil)
