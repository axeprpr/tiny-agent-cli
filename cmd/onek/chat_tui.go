package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"onek-agent/internal/model"
	"onek-agent/internal/tools"
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
}

func newTUIApprover(mode string, events chan tea.Msg) *tuiApprover {
	return &tuiApprover{
		mode:   strings.TrimSpace(mode),
		events: events,
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
	if a.Mode() == tools.ApprovalDangerously {
		return true, nil
	}
	response := make(chan string, 1)
	a.events <- tuiApprovalMsg{
		title:    "Command approval",
		body:     strings.TrimSpace(command),
		response: response,
	}
	answer := <-response
	switch answer {
	case "yes":
		return true, nil
	case "always":
		_ = a.SetMode(tools.ApprovalDangerously)
		return true, nil
	default:
		return false, nil
	}
}

func (a *tuiApprover) ApproveWrite(_ context.Context, path, content string) (bool, error) {
	if a.Mode() == tools.ApprovalDangerously {
		return true, nil
	}

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
}

func (k chatKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Switch, k.Filter, k.Quit}
}

func (k chatKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Switch, k.Filter, k.Help, k.Quit},
		{k.PageUp, k.PageDown},
	}
}

type chatTUIModel struct {
	runtime      *chatRuntime
	events       chan tea.Msg
	chatViewport viewport.Model
	logViewport  viewport.Model
	input        textarea.Model
	spinner      spinner.Model
	help         help.Model
	keys         chatKeyMap
	entries      []tuiEntry
	logs         []tuiLogEntry
	approval     *tuiApprovalMsg
	width        int
	height       int
	busy         bool
	stepText     string
	statusText   string
	showFullHelp bool
	activePane   string
	logFilter    string
}

var (
	appStyle = lipgloss.NewStyle().Padding(0, 1)
	panelGap = 1

	headerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("25")).
			Bold(true).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	tagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248"))

	chipMutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("251")).
			Background(lipgloss.Color("238")).
			Padding(0, 1)

	chipAccentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("31")).
			Padding(0, 1)

	chipWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("235")).
			Background(lipgloss.Color("214")).
			Padding(0, 1)

	userLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42")).
				Bold(true)

	systemLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Bold(true)

	userCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("81")).
			Padding(0, 1)

	assistantCardStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("42")).
				Padding(0, 1)

	systemCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(0, 1)

	errorCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("203")).
			Padding(0, 1)

	logLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Bold(true)

	errorLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	messageBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	logBodyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errorBodyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	codeBodyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("238")).
			Padding(0, 1)

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	activePaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("81"))

	paneTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true).
			Padding(0, 1)

	approvalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(1).
			Background(lipgloss.Color("236"))

	inputPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("81")).
			Padding(0, 1)

	inputTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	inputHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	codeBlockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)
)

func newChatTUIModel(runtime *chatRuntime, events chan tea.Msg) chatTUIModel {
	ta := textarea.New()
	ta.Placeholder = "Ask onek-agent, inspect code, or use /help..."
	ta.Focus()
	ta.Prompt = "> "
	ta.ShowLineNumbers = false
	ta.SetHeight(3)

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
		activePane:   "chat",
		logFilter:    "all",
		keys: chatKeyMap{
			Send:     key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "send")),
			Newline:  key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "newline")),
			Switch:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch pane")),
			Filter:   key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter logs")),
			Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
			Quit:     key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
			PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
			PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
		},
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
	m.statusText = "ready"
	m.refreshViewports()
	return m
}

func runChatTUI(runtime *chatRuntime) int {
	events := make(chan tea.Msg, 256)
	approver := newTUIApprover(runtime.cfg.ApprovalMode, events)
	runtime.approver = approver
	runtime.loop = buildAgentWith(runtime.cfg, approver, &tuiLogWriter{events: events})
	runtime.session.SetAgent(runtime.loop)

	p := tea.NewProgram(newChatTUIModel(runtime, events), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "chat ui error: %v\n", err)
		runtime.beforeExit()
		return 1
	}
	return 0
}

func waitForTUIEvent(events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

func runTaskCmd(runtime *chatRuntime, task string) tea.Cmd {
	return func() tea.Msg {
		output, err := runtime.executeTask(context.Background(), task)
		return tuiTaskDoneMsg{output: output, err: err}
	}
}

func (m chatTUIModel) Init() tea.Cmd {
	return tea.Batch(waitForTUIEvent(m.events), m.spinner.Tick)
}

func (m chatTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	keyHandled := false

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyMsg:
		if m.approval != nil {
			switch msg.String() {
			case "y":
				m.approval.response <- "yes"
				m.statusText = "approval granted"
				m.approval = nil
			case "n", "esc":
				m.approval.response <- "no"
				m.statusText = "approval denied"
				m.approval = nil
			case "a":
				m.approval.response <- "always"
				m.statusText = "approval mode switched to dangerously"
				m.approval = nil
			}
			cmds = append(cmds, waitForTUIEvent(m.events))
			return m, tea.Batch(cmds...)
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			keyHandled = true
			m.runtime.beforeExit()
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			keyHandled = true
			m.showFullHelp = !m.showFullHelp
		case key.Matches(msg, m.keys.Switch):
			keyHandled = true
			if m.activePane == "chat" {
				m.activePane = "log"
			} else {
				m.activePane = "chat"
			}
		case key.Matches(msg, m.keys.Filter):
			keyHandled = true
			m.logFilter = nextLogFilter(m.logFilter)
			m.statusText = "log filter: " + m.logFilter
			m.refreshViewports()
		case key.Matches(msg, m.keys.PageUp):
			keyHandled = true
			if m.activePane == "log" {
				m.logViewport.HalfViewUp()
			} else {
				m.chatViewport.HalfViewUp()
			}
		case key.Matches(msg, m.keys.PageDown):
			keyHandled = true
			if m.activePane == "log" {
				m.logViewport.HalfViewDown()
			} else {
				m.chatViewport.HalfViewDown()
			}
		case key.Matches(msg, m.keys.Newline):
			keyHandled = true
			m.input.InsertString("\n")
		case key.Matches(msg, m.keys.Send):
			keyHandled = true
			task := strings.TrimSpace(m.input.Value())
			if task == "" || m.busy {
				break
			}
			m.input.Reset()
			if strings.HasPrefix(task, "/") {
				result := m.runtime.executeCommand(task)
				if result.handled {
					if strings.TrimSpace(result.output) != "" {
						m.entries = append(m.entries, tuiEntry{role: "system", text: result.output})
						m.refreshViewports()
					}
					if result.exitCode >= 0 {
						m.runtime.beforeExit()
						return m, tea.Quit
					}
					break
				}
			}

			m.entries = append(m.entries, tuiEntry{role: "user", text: task})
			m.refreshViewports()
			m.busy = true
			m.statusText = "running"
			cmds = append(cmds, runTaskCmd(m.runtime, task))
		}
	case tuiLogMsg:
		m.stepText = msg.text
		m.logs = append(m.logs, tuiLogEntry{kind: msg.kind, text: msg.text})
		m.refreshViewports()
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiApprovalMsg:
		m.approval = &msg
		m.logs = append(m.logs, tuiLogEntry{kind: "approval", text: msg.title + ": " + strings.ReplaceAll(msg.body, "\n", " | ")})
		m.statusText = "approval required"
		m.refreshViewports()
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiTaskDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.entries = append(m.entries, tuiEntry{role: "error", text: msg.err.Error()})
			m.statusText = "error"
		} else {
			m.entries = append(m.entries, tuiEntry{role: "assistant", text: msg.output})
			m.statusText = "ready"
		}
		m.refreshViewports()
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var cmd tea.Cmd
	if !keyHandled {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		m.chatViewport, cmd = m.chatViewport.Update(msg)
		cmds = append(cmds, cmd)
		m.logViewport, cmd = m.logViewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.resize()
	return m, tea.Batch(cmds...)
}

func (m chatTUIModel) View() string {
	header := m.renderHeader()

	statusParts := []string{
		"model=" + m.runtime.cfg.Model,
		"approval=" + m.runtime.approver.Mode(),
		"session=" + m.runtime.sessionName,
		contextStatus(m.runtime, m.input.Value()),
	}
	if m.busy {
		statusParts = append(statusParts, m.spinner.View()+" "+m.statusText)
	} else {
		statusParts = append(statusParts, m.statusText)
	}
	if strings.TrimSpace(m.stepText) != "" {
		statusParts = append(statusParts, m.stepText)
	}

	helpView := m.help.ShortHelpView(m.keys.ShortHelp())
	if m.showFullHelp {
		helpView = m.help.FullHelpView(m.keys.FullHelp())
	}

	mainPane := m.renderPane(
		fmt.Sprintf("Conversation  %d msg", len(m.entries)),
		m.chatViewport.View(),
		m.activePane == "chat",
		m.chatViewport.Width,
	)
	sidePane := m.renderPane(
		fmt.Sprintf("Activity  %s  %d evt", strings.ToUpper(m.logFilter), m.filteredLogCount()),
		m.logViewport.View(),
		m.activePane == "log",
		m.logViewport.Width,
	)
	contentRow := m.renderContent(mainPane, sidePane)

	parts := []string{
		header,
		contentRow,
		m.renderComposer(),
		statusStyle.Width(max(0, m.width-2)).Render(strings.Join(statusParts, "  ")),
		helpView,
	}
	view := appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	if m.approval != nil && m.width > 0 && m.height > 0 {
		modalWidth := min(96, max(44, m.width-10))
		modal := approvalStyle.Width(modalWidth).Render(renderApprovalModal(m.approval, modalWidth-6))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}
	return view
}

func (m *chatTUIModel) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	footerHeight := 2
	if m.showFullHelp {
		footerHeight = 4
	}
	extra := 7 + footerHeight + m.input.Height()
	contentWidth := max(20, m.width-4)
	stacked := shouldStackPanes(contentWidth)
	mainWidth, sideWidth := splitWidths(contentWidth, stacked)
	availableHeight := m.height - extra
	if stacked {
		chatHeight, logHeight := splitHeights(availableHeight)
		m.chatViewport.Height = chatHeight
		m.logViewport.Height = logHeight
	} else {
		vpHeight := max(6, availableHeight)
		m.chatViewport.Height = vpHeight
		m.logViewport.Height = vpHeight
	}
	m.chatViewport.Width = max(18, mainWidth-2)
	m.logViewport.Width = max(18, sideWidth-2)
	m.input.SetWidth(max(20, contentWidth-4))
	m.refreshViewports()
}

func (m *chatTUIModel) refreshViewports() {
	if m.chatViewport.Width > 0 {
		atBottom := m.chatViewport.AtBottom()
		offset := m.chatViewport.YOffset
		m.chatViewport.SetContent(m.renderEntries())
		if atBottom {
			m.chatViewport.GotoBottom()
		} else {
			m.chatViewport.SetYOffset(offset)
		}
	}
	if m.logViewport.Width > 0 {
		atBottom := m.logViewport.AtBottom()
		offset := m.logViewport.YOffset
		m.logViewport.SetContent(m.renderLogs())
		if atBottom {
			m.logViewport.GotoBottom()
		} else {
			m.logViewport.SetYOffset(offset)
		}
	}
}

func (m *chatTUIModel) renderEntries() string {
	if m.chatViewport.Width <= 0 {
		return ""
	}
	var rendered []string
	bodyWidth := max(20, m.chatViewport.Width-4)
	for _, entry := range m.entries {
		var label lipgloss.Style
		var body lipgloss.Style
		var card lipgloss.Style
		switch entry.role {
		case "user":
			label = userLabelStyle
			body = messageBodyStyle
			card = userCardStyle
		case "assistant":
			label = assistantLabelStyle
			body = codeBodyStyle
			card = assistantCardStyle
		case "system":
			label = systemLabelStyle
			body = messageBodyStyle
			card = systemCardStyle
		case "error":
			label = errorLabelStyle
			body = errorBodyStyle
			card = errorCardStyle
		default:
			label = logLabelStyle
			body = logBodyStyle
			card = systemCardStyle
		}
		text := strings.TrimSpace(entry.text)
		if text == "" {
			continue
		}
		if entry.role == "assistant" {
			text = renderMarkdown(text, bodyWidth)
		}
		rendered = append(rendered, card.Width(max(24, m.chatViewport.Width)).Render(lipgloss.JoinVertical(lipgloss.Left,
			label.Render(strings.ToUpper(entry.role)),
			body.Width(bodyWidth).Render(text),
		)))
	}
	return strings.Join(rendered, "\n\n")
}

func (m *chatTUIModel) renderLogs() string {
	if len(m.logs) == 0 {
		return logBodyStyle.Render("No tool activity yet.")
	}
	width := max(16, m.logViewport.Width-2)
	filtered := make([]tuiLogEntry, 0, len(m.logs))
	for _, entry := range m.logs {
		if m.logFilter == "all" || m.logFilter == entry.kind {
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == 0 {
		return logBodyStyle.Render("No matching activity.")
	}
	out := make([]string, 0, len(filtered))
	start := 0
	if len(filtered) > 80 {
		start = len(filtered) - 80
	}
	for _, entry := range filtered[start:] {
		style := logBodyStyle
		prefix := "[step]"
		switch entry.kind {
		case "tools":
			prefix = "[tool]"
		case "approval":
			prefix = "[ask ]"
			style = systemLabelStyle
		case "error":
			prefix = "[err ]"
			style = errorBodyStyle
		}
		out = append(out, style.Width(width).Render(prefix+" "+entry.text))
	}
	return strings.Join(out, "\n")
}

func (m chatTUIModel) renderPane(title, content string, active bool, width int) string {
	style := paneStyle
	if active {
		style = activePaneStyle
		title = "● " + title
	} else {
		title = "○ " + title
	}
	if width <= 0 {
		width = 20
	}
	return style.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left,
		paneTitleStyle.Render(title),
		content,
	))
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
	pct := remaining * 100 / window
	return fmt.Sprintf("ctx≈%d%% free", pct)
}

func estimateTokenUsage(messages []model.Message, draft string) int {
	chars := 0
	for _, msg := range messages {
		chars += len(model.ContentString(msg.Content)) + len(msg.Role) + len(msg.ToolCallID)
		for _, call := range msg.ToolCalls {
			chars += len(call.ID) + len(call.Type) + len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	chars += len(draft)
	return chars / 4
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func splitWidths(total int, stacked bool) (int, int) {
	if stacked {
		return total, total
	}
	main := total * 68 / 100
	side := total - main - panelGap
	if side < 24 {
		side = 24
		main = total - side - panelGap
	}
	return max(36, main), max(24, side)
}

func splitHeights(total int) (int, int) {
	total = max(12, total)
	logHeight := max(5, total*34/100)
	chatHeight := total - logHeight - panelGap
	if chatHeight < 6 {
		chatHeight = 6
		logHeight = max(5, total-chatHeight-panelGap)
	}
	return chatHeight, logHeight
}

func shouldStackPanes(total int) bool {
	return total < 110
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

	parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render("[y] yes   [n] no   [a] always dangerously"))
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

var markdownRenderers sync.Map

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	if renderer, ok := markdownRenderers.Load(width); ok {
		return renderer.(*glamour.TermRenderer), nil
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	markdownRenderers.Store(width, renderer)
	return renderer, nil
}

func (m chatTUIModel) renderHeader() string {
	titleRow := lipgloss.JoinHorizontal(
		lipgloss.Left,
		titleStyle.Render("onek-agent"),
		" ",
		subtitleStyle.Render("cheap codex for offline shells"),
		" ",
		tagStyle.Render("single binary"),
	)

	chips := []string{
		chipMutedStyle.Render("workdir " + filepath.Base(m.runtime.cfg.WorkDir)),
		chipMutedStyle.Render("shell " + filepath.Base(m.runtime.cfg.Shell)),
		chipAccentStyle.Render("model " + m.runtime.cfg.Model),
	}
	if m.runtime.approver.Mode() == tools.ApprovalDangerously {
		chips = append(chips, chipWarnStyle.Render("dangerously"))
	} else {
		chips = append(chips, chipMutedStyle.Render("confirm"))
	}
	return headerStyle.Width(max(20, m.width-2)).Render(
		lipgloss.JoinVertical(lipgloss.Left, titleRow, strings.Join(chips, " ")),
	)
}

func (m chatTUIModel) renderContent(mainPane, sidePane string) string {
	if shouldStackPanes(max(20, m.width-4)) {
		return lipgloss.JoinVertical(lipgloss.Left, mainPane, sidePane)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPane, strings.Repeat(" ", panelGap), sidePane)
}

func (m chatTUIModel) renderComposer() string {
	title := inputTitleStyle.Render("Composer")
	hints := inputHintStyle.Render("Enter send  Ctrl+J newline  /help commands")
	return inputPaneStyle.Width(max(20, m.width-2)).Render(
		lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", hints), m.input.View()),
	)
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

var _ io.Writer = (*tuiLogWriter)(nil)
var _ tools.Approver = (*tuiApprover)(nil)
