package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

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

type tuiMouseScrollMsg struct{}

type tuiEntry struct {
	role string
	text string
}

type tuiLogEntry struct {
	kind string
	text string
}

type tuiLogWriter struct {
	events chan tea.Msg
}

func (w *tuiLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text == "" {
		return len(p), nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			w.events <- tuiLogMsg{kind: classifyLogKind(line), text: line}
		}
	}
	return len(p), nil
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
	Send     key.Binding
	Newline  key.Binding
	Quit     key.Binding
	Help     key.Binding
	Switch   key.Binding
	Filter   key.Binding
	PageDown key.Binding
	PageUp   key.Binding
	Home     key.Binding
	End      key.Binding
}

func (k chatKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Quit}
}

func (k chatKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Switch, k.Filter, k.Help, k.Quit},
		{k.PageUp, k.PageDown, k.Home, k.End},
	}
}

type chatTUIModel struct {
	runtime       *chatRuntime
	events        chan tea.Msg
	chatViewport  viewport.Model
	logViewport   viewport.Model
	input         textarea.Model
	spinner       spinner.Model
	help          help.Model
	keys          chatKeyMap
	entries       []tuiEntry
	logs          []tuiLogEntry
	approval      *tuiApprovalMsg
	width         int
	height        int
	busy          bool
	stepText      string
	statusText    string
	showFullHelp  bool
	showDrawer    bool
	logFilter     string
	entriesDirty  bool
	logsDirty     bool
	entriesWidth  int
	logsWidth     int
	refreshQueued bool
	entryKeys     []string
	entryBlocks   []string
	pendingScroll int
	scrollQueued  bool
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

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 0, 1, 0)

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

	activityDrawerStyle = lipgloss.NewStyle().
				Padding(0, 0, 0, 2)
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

	logVP := viewport.New(0, 0)
	logVP.MouseWheelEnabled = true

	m := chatTUIModel{
		runtime:      runtime,
		events:       events,
		input:        ta,
		spinner:      sp,
		chatViewport: chatVP,
		logViewport:  logVP,
		help:         help.New(),
		showDrawer:   false,
		logFilter:    "all",
		keys: chatKeyMap{
			Send:     key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "send")),
			Newline:  key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "newline")),
			Switch:   key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "toggle activity")),
			Filter:   key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "filter logs")),
			Help:     key.NewBinding(key.WithKeys("f1"), key.WithHelp("f1", "help")),
			Quit:     key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
			PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
			PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
			Home:     key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "top")),
			End:      key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "bottom")),
		},
		entriesDirty: true,
		logsDirty:    true,
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
	m.refreshViewports()
	return m
}

func runChatTUI(runtime *chatRuntime) int {
	events := make(chan tea.Msg, 256)
	approver := newTUIApprover(runtime.cfg.ApprovalMode, events)
	runtime.approver = approver
	var jobs tools.JobControl
	if runtime.jobs != nil {
		jobs = jobToolAdapter{manager: runtime.jobs}
	}
	runtime.loop = buildAgentWith(runtime.cfg, approver, &tuiLogWriter{events: events}, jobs)
	runtime.session.SetAgent(runtime.loop)
	if runtime.jobs != nil {
		runtime.jobs.SetNotifier(func(text string) {
			events <- tuiJobUpdateMsg{text: text}
		})
	}

	p := tea.NewProgram(newChatTUIModel(runtime, events), tea.WithAltScreen())
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

func scheduleMouseScroll() tea.Cmd {
	return tea.Tick(time.Second/60, func(time.Time) tea.Msg {
		return tuiMouseScrollMsg{}
	})
}

func runTaskCmd(runtime *chatRuntime, events chan tea.Msg, task string) tea.Cmd {
	return func() tea.Msg {
		output, err := runtime.executeTaskStreaming(context.Background(), task, func(token string) {
			events <- tuiStreamMsg{token: token}
		})
		events <- tuiTaskDoneMsg{output: output, err: err}
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
		case key.Matches(msg, m.keys.Help):
			keyHandled = true
			m.showFullHelp = !m.showFullHelp
		case key.Matches(msg, m.keys.Switch):
			keyHandled = true
			m.showDrawer = !m.showDrawer
			if m.showDrawer {
				m.statusText = i18n.T("tui.status.drawer.shown")
			} else {
				m.statusText = i18n.T("tui.status.drawer.hidden")
			}
		case key.Matches(msg, m.keys.Filter):
			keyHandled = true
			m.logFilter = nextLogFilter(m.logFilter)
			if m.showDrawer {
				m.statusText = "log filter: " + m.logFilter
			} else {
				m.statusText = i18n.T("tui.status.filter.updated")
			}
			m.logsDirty = true
			immediateRefresh = true
		case key.Matches(msg, m.keys.PageUp):
			keyHandled = true
			m.chatViewport.HalfViewUp()
		case key.Matches(msg, m.keys.PageDown):
			keyHandled = true
			m.chatViewport.HalfViewDown()
		case key.Matches(msg, m.keys.Home):
			keyHandled = true
			m.chatViewport.GotoTop()
		case key.Matches(msg, m.keys.End):
			keyHandled = true
			m.chatViewport.GotoBottom()
		case key.Matches(msg, m.keys.Newline):
			keyHandled = true
			m.input.InsertString("\n")
		case key.Matches(msg, m.keys.Send):
			keyHandled = true
			task := strings.TrimSpace(m.input.Value())
			if task == "" {
				break
			}
			if m.approval == nil && m.busy && !strings.HasPrefix(task, "/") {
				break
			}
			m.input.Reset()
			if m.approval != nil {
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
			if strings.HasPrefix(task, "/") {
				result := m.runtime.executeCommand(task)
				if result.handled {
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
			m.busy = true
			m.statusText = i18n.T("tui.status.running")
			m.refreshInputState()
			cmds = append(cmds, runTaskCmd(m.runtime, m.events, task))
		}
	case tea.MouseMsg:
		mouseHandled = true
		if delta, ok := mouseScrollDelta(msg, m.chatViewport.MouseWheelDelta); ok {
			m.pendingScroll += delta
			if !m.scrollQueued {
				m.scrollQueued = true
				cmds = append(cmds, scheduleMouseScroll())
			}
			break
		}
	case tuiLogMsg:
		m.stepText = nextStepStatus(m.stepText, msg.kind, msg.text)
		m.logs = append(m.logs, tuiLogEntry{kind: msg.kind, text: msg.text})
		capLogs(&m.logs, 500)
		m.appendActivityEntry(msg.kind, msg.text)
		m.logsDirty = true
		m.entriesDirty = true
		if !m.refreshQueued {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiApprovalMsg:
		m.approval = &msg
		m.logs = append(m.logs, tuiLogEntry{kind: "approval", text: msg.title + ": " + strings.ReplaceAll(msg.body, "\n", " | ")})
		capLogs(&m.logs, 500)
		m.entries = append(m.entries, tuiEntry{role: "approval", text: renderApprovalInlineText(&msg)})
		m.statusText = i18n.T("tui.status.approval")
		m.logsDirty = true
		m.entriesDirty = true
		m.refreshInputState()
		immediateRefresh = true
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiTaskDoneMsg:
		m.busy = false
		m.refreshQueued = false
		// Convert any streaming entry to assistant
		for i := range m.entries {
			if m.entries[i].role == "streaming" {
				m.entries[i].role = "assistant"
			}
		}
		if msg.err != nil {
			m.entries = append(m.entries, tuiEntry{role: "error", text: msg.err.Error()})
			m.statusText = i18n.T("tui.status.error")
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
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiJobUpdateMsg:
		m.logs = append(m.logs, tuiLogEntry{kind: "steps", text: msg.text})
		capLogs(&m.logs, 500)
		m.entries = append(m.entries, tuiEntry{role: "system", text: msg.text})
		m.statusText = i18n.T("tui.status.bg.update")
		m.logsDirty = true
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
		if m.entriesDirty || m.logsDirty {
			m.refreshQueued = true
			cmds = append(cmds, scheduleViewportRefresh())
		}
	case tuiMouseScrollMsg:
		m.scrollQueued = false
		if m.pendingScroll != 0 {
			applyMouseScroll(&m.chatViewport, m.pendingScroll)
			if m.showDrawer {
				applyMouseScroll(&m.logViewport, m.pendingScroll)
			}
			m.pendingScroll = 0
		}
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var cmd tea.Cmd
	if !keyHandled && !mouseHandled {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		m.chatViewport, cmd = m.chatViewport.Update(msg)
		cmds = append(cmds, cmd)
		m.logViewport, cmd = m.logViewport.Update(msg)
		cmds = append(cmds, cmd)
		// Dynamic input height
		lines := strings.Count(m.input.Value(), "\n") + 1
		newHeight := max(1, min(5, lines))
		if newHeight != m.input.Height() {
			m.input.SetHeight(newHeight)
		}
	}
	m.resize(immediateRefresh)
	return m, tea.Batch(cmds...)
}

func (m chatTUIModel) View() string {
	header := m.renderHeader()

	statusParts := []string{
		contextStatus(m.runtime, m.input.Value()),
	}
	if m.showDrawer {
		statusParts = append(statusParts, "activity")
	}
	if m.busy {
		statusParts = append(statusParts, m.spinner.View()+" "+m.statusText)
	} else {
		statusParts = append(statusParts, m.statusText)
	}
	if strings.TrimSpace(m.stepText) != "" {
		statusParts = append(statusParts, chipAccentStyle.Render(m.stepText))
	}

	content := m.renderConversation()
	if m.showDrawer {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", m.renderActivityDrawer())
	}

	parts := []string{
		header,
		m.renderTodoSummary(),
		content,
		m.renderComposer(),
		statusStyle.Width(max(0, m.width-2)).Render(strings.Join(statusParts, "  ")),
	}
	if m.showFullHelp {
		parts = append(parts, m.help.FullHelpView(m.keys.FullHelp()))
	}
	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *chatTUIModel) resize(forceRefresh bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	footerHeight := 1
	if m.showFullHelp {
		footerHeight = 4
	}
	extra := 7 + footerHeight + m.input.Height()
	contentWidth := max(20, m.width-4)
	availableHeight := m.height - extra
	drawerHeight := 0
	if m.showDrawer {
		drawerHeight = activityDrawerHeight(availableHeight)
	}
	m.chatViewport.Height = max(6, availableHeight-drawerHeight)
	m.logViewport.Height = drawerHeight
	newChatWidth := max(18, contentWidth)
	newLogWidth := max(18, contentWidth)
	widthChanged := m.chatViewport.Width != newChatWidth || m.logViewport.Width != newLogWidth
	m.chatViewport.Width = newChatWidth
	m.logViewport.Width = newLogWidth
	m.input.SetWidth(max(20, contentWidth-4))
	if widthChanged {
		m.entriesDirty = true
		m.logsDirty = true
	}
	if widthChanged || forceRefresh {
		m.refreshViewports()
	}
}

func (m *chatTUIModel) refreshViewports() {
	if m.chatViewport.Width > 0 {
		if m.entriesDirty || m.entriesWidth != m.chatViewport.Width {
			atBottom := m.chatViewport.AtBottom()
			offset := m.chatViewport.YOffset
			m.chatViewport.SetContent(m.renderEntries())
			if atBottom {
				m.chatViewport.GotoBottom()
			} else {
				m.chatViewport.SetYOffset(offset)
			}
			m.entriesDirty = false
			m.entriesWidth = m.chatViewport.Width
		}
	}
	if m.logViewport.Width > 0 {
		if m.logsDirty || m.logsWidth != m.logViewport.Width {
			atBottom := m.logViewport.AtBottom()
			offset := m.logViewport.YOffset
			m.logViewport.SetContent(m.renderLogs())
			if atBottom {
				m.logViewport.GotoBottom()
			} else {
				m.logViewport.SetYOffset(offset)
			}
			m.logsDirty = false
			m.logsWidth = m.logViewport.Width
		}
	}
}

func (m *chatTUIModel) renderEntries() string {
	if m.chatViewport.Width <= 0 {
		return ""
	}
	bodyWidth := max(20, m.chatViewport.Width-3)
	if len(m.entryKeys) != len(m.entries) {
		m.entryKeys = make([]string, len(m.entries))
		m.entryBlocks = make([]string, len(m.entries))
	}
	for i, entry := range m.entries {
		key := fmt.Sprintf("%d|%s|%s", bodyWidth, entry.role, entry.text)
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
		if entry.role == "assistant" {
			text = renderMarkdown(text, bodyWidth)
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
			body.Width(bodyWidth).Render(text),
		)
		m.entryKeys[i] = key
		m.entryBlocks[i] = block
	}

	rendered := make([]string, 0, len(m.entryBlocks))
	for _, block := range m.entryBlocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		rendered = append(rendered, block)
	}
	return strings.Join(rendered, "\n\n")
}

func (m *chatTUIModel) renderLogs() string {
	if len(m.logs) == 0 {
		return logBodyStyle.Render(i18n.T("tui.label.no.activity"))
	}
	width := max(16, m.logViewport.Width-2)
	filtered := make([]tuiLogEntry, 0, len(m.logs))
	for _, entry := range m.logs {
		if m.logFilter == "all" || m.logFilter == entry.kind {
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == 0 {
		return logBodyStyle.Render(i18n.T("tui.label.no.match"))
	}
	out := make([]string, 0, len(filtered))
	start := 0
	if len(filtered) > 80 {
		start = len(filtered) - 80
	}
	for _, entry := range filtered[start:] {
		style := logBodyStyle
		prefix := "step"
		switch entry.kind {
		case "tools":
			prefix = "tool"
		case "approval":
			prefix = "ask"
			style = systemLabelStyle
		case "error":
			prefix = "error"
			style = errorBodyStyle
		}
		out = append(out, style.Width(width).Render(prefix+"  "+entry.text))
	}
	return strings.Join(out, "\n")
}

func contextStatus(runtime *chatRuntime, input string) string {
	window := runtime.cfg.ContextWindow
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

func capLogs(logs *[]tuiLogEntry, limit int) {
	if len(*logs) > limit {
		*logs = (*logs)[len(*logs)-limit:]
	}
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

func activityDrawerHeight(total int) int {
	total = max(10, total)
	height := total / 3
	if height < 7 {
		height = 7
	}
	if height > 12 {
		height = 12
	}
	if total-height < 6 {
		height = max(0, total-6)
	}
	return height
}

func classifyLogKind(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.Contains(lower, "error"):
		return "error"
	case strings.Contains(lower, "run_command") || strings.Contains(lower, "read_file") || strings.Contains(lower, "write_file") || strings.Contains(lower, "grep") || strings.Contains(lower, "web_search") || strings.Contains(lower, "fetch_url") || strings.Contains(lower, "list_files"):
		return "tools"
	case strings.Contains(lower, "requesting model") || strings.Contains(lower, "executing "):
		return "steps"
	default:
		return "steps"
	}
}

func nextLogFilter(current string) string {
	switch current {
	case "all":
		return "steps"
	case "steps":
		return "tools"
	case "tools":
		return "error"
	case "error":
		return "approval"
	default:
		return "all"
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

func renderMarkdown(text string, width int) string {
	width = max(20, width)
	renderer, err := markdownRenderer(width)
	if err != nil {
		return text
	}
	out, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSpace(out)
}

var (
	mdCacheMu   sync.Mutex
	mdCache     = make(map[int]*glamour.TermRenderer)
	mdCacheKeys []int
)

const mdCacheLimit = 8

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()

	if r, ok := mdCache[width]; ok {
		return r, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	if len(mdCache) >= mdCacheLimit {
		oldest := mdCacheKeys[0]
		mdCacheKeys = mdCacheKeys[1:]
		delete(mdCache, oldest)
	}
	mdCache[width] = r
	mdCacheKeys = append(mdCacheKeys, width)
	return r, nil
}

func (m chatTUIModel) renderHeader() string {
	chips := []string{
		titleStyle.Render("tacli"),
		chipMutedStyle.Render(version),
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
	title := paneTitleStyle.Width(max(20, m.chatViewport.Width)).Render(
		fmt.Sprintf(i18n.T("tui.label.messages"), len(m.entries)),
	)
	return conversationStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, m.chatViewport.View()))
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

func (m chatTUIModel) renderActivityDrawer() string {
	title := paneTitleStyle.Width(max(20, m.logViewport.Width)).Render(
		fmt.Sprintf(i18n.T("tui.label.activity"), strings.ToUpper(m.logFilter), m.filteredLogCount()),
	)
	return activityDrawerStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		m.logViewport.View(),
	))
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

func (m chatTUIModel) filteredLogCount() int {
	if m.logFilter == "all" {
		return len(m.logs)
	}
	count := 0
	for _, entry := range m.logs {
		if entry.kind == m.logFilter {
			count++
		}
	}
	return count
}

func (m *chatTUIModel) refreshInputState() {
	m.input.Prompt = ""
	lines := strings.Count(m.input.Value(), "\n") + 1
	m.input.SetHeight(max(1, min(5, lines)))
	switch {
	case m.approval != nil:
		m.input.Placeholder = i18n.T("tui.placeholder.approval")
	case m.busy:
		m.input.Placeholder = i18n.T("tui.placeholder.busy")
	default:
		m.input.Placeholder = i18n.T("tui.placeholder.default")
	}
}

func (m chatTUIModel) composerHint() string {
	switch {
	case m.approval != nil:
		return i18n.T("tui.hint.approval")
	case m.busy:
		return i18n.T("tui.hint.busy")
	default:
		return i18n.T("tui.hint.send")
	}
}

func (m *chatTUIModel) appendActivityEntry(kind, text string) {
	text = strings.TrimSpace(text)
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
	default:
		m.entries = append(m.entries, tuiEntry{role: "activity", text: formatInlineLogEntry(kind, text)})
	}
}

func shouldSkipInlineActivity(kind, text string) bool {
	if kind != "steps" {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "requesting model") || strings.Contains(lower, "executing ")
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
	lower := strings.ToLower(text)
	if strings.Contains(lower, "requesting model") || strings.Contains(lower, "executing ") {
		return text
	}
	return current
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
