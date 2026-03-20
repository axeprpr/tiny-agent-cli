package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"onek-agent/internal/model"
	"onek-agent/internal/tools"
)

type tuiLogMsg struct {
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
			w.events <- tuiLogMsg{text: line}
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
	PageDown key.Binding
	PageUp   key.Binding
}

func (k chatKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Switch, k.Help, k.Quit}
}

func (k chatKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Switch, k.Help, k.Quit},
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
	logs         []string
	approval     *tuiApprovalMsg
	width        int
	height       int
	busy         bool
	stepText     string
	statusText   string
	showFullHelp bool
	activePane   string
}

var (
	appStyle = lipgloss.NewStyle().Padding(0, 1)
	panelGap = 1

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("25")).
			Bold(true).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			PaddingLeft(1)

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
			Foreground(lipgloss.Color("246")).
			Padding(0, 1)

	approvalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(1).
			Background(lipgloss.Color("236"))
)

func newChatTUIModel(runtime *chatRuntime, events chan tea.Msg) chatTUIModel {
	ta := textarea.New()
	ta.Placeholder = "Ask onek-agent..."
	ta.Focus()
	ta.Prompt = "│ "
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
		keys: chatKeyMap{
			Send:     key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "send")),
			Newline:  key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "newline")),
			Switch:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch pane")),
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
	m.statusText = "Ready"
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
			m.runtime.beforeExit()
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.showFullHelp = !m.showFullHelp
		case key.Matches(msg, m.keys.Switch):
			if m.activePane == "chat" {
				m.activePane = "log"
			} else {
				m.activePane = "chat"
			}
		case key.Matches(msg, m.keys.PageUp):
			if m.activePane == "log" {
				m.logViewport.HalfViewUp()
			} else {
				m.chatViewport.HalfViewUp()
			}
		case key.Matches(msg, m.keys.PageDown):
			if m.activePane == "log" {
				m.logViewport.HalfViewDown()
			} else {
				m.chatViewport.HalfViewDown()
			}
		case key.Matches(msg, m.keys.Newline):
			m.input.InsertString("\n")
		case key.Matches(msg, m.keys.Send):
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
		m.logs = append(m.logs, msg.text)
		m.refreshViewports()
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiApprovalMsg:
		m.approval = &msg
		m.statusText = "approval required"
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
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.chatViewport, cmd = m.chatViewport.Update(msg)
	cmds = append(cmds, cmd)
	m.logViewport, cmd = m.logViewport.Update(msg)
	cmds = append(cmds, cmd)
	m.resize()
	return m, tea.Batch(cmds...)
}

func (m chatTUIModel) View() string {
	title := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("onek-agent"),
		subtitleStyle.Render("lightweight codex-style terminal agent"),
	)

	statusParts := []string{
		"model=" + m.runtime.cfg.Model,
		"approval=" + m.runtime.approver.Mode(),
		"session=" + m.runtime.sessionName,
		approxContextStatus(m.runtime, m.input.Value()),
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

	mainPane := m.renderPane("Conversation", m.chatViewport.View(), m.activePane == "chat", m.chatViewport.Width)
	sidePane := m.renderPane("Activity", m.logViewport.View(), m.activePane == "log", m.logViewport.Width)
	contentRow := lipgloss.JoinHorizontal(lipgloss.Top, mainPane, strings.Repeat(" ", panelGap), sidePane)

	parts := []string{
		title,
		contentRow,
		m.input.View(),
		statusStyle.Width(max(0, m.width-2)).Render(strings.Join(statusParts, "  ")),
		helpView,
	}
	if m.approval != nil {
		parts = append(parts, approvalStyle.Width(max(30, m.width-4)).Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render(m.approval.title),
				m.approval.body,
				lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render("[y] yes   [n] no   [a] always dangerously"),
			),
		))
	}
	view := appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	if m.approval != nil && m.width > 0 && m.height > 0 {
		modal := approvalStyle.Width(min(84, max(40, m.width-8))).Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render(m.approval.title),
				m.approval.body,
				lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render("[y] yes   [n] no   [a] always dangerously"),
			),
		)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, view+"\n"+modal)
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
	extra := 5 + footerHeight + m.input.Height()
	if m.approval != nil {
		extra += 6
	}
	vpHeight := m.height - extra
	if vpHeight < 5 {
		vpHeight = 5
	}
	contentWidth := max(20, m.width-4)
	mainWidth, sideWidth := splitWidths(contentWidth)
	m.chatViewport.Width = mainWidth - 2
	m.chatViewport.Height = vpHeight
	m.logViewport.Width = sideWidth - 2
	m.logViewport.Height = vpHeight
	m.input.SetWidth(contentWidth)
	m.refreshViewports()
}

func (m *chatTUIModel) refreshViewports() {
	if m.chatViewport.Width > 0 {
		m.chatViewport.SetContent(m.renderEntries())
		m.chatViewport.GotoBottom()
	}
	if m.logViewport.Width > 0 {
		m.logViewport.SetContent(m.renderLogs())
		m.logViewport.GotoBottom()
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
			body = messageBodyStyle
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
	out := make([]string, 0, len(m.logs))
	start := 0
	if len(m.logs) > 80 {
		start = len(m.logs) - 80
	}
	for _, line := range m.logs[start:] {
		out = append(out, logBodyStyle.Width(width).Render(line))
	}
	return strings.Join(out, "\n")
}

func (m chatTUIModel) renderPane(title, content string, active bool, width int) string {
	style := paneStyle
	if active {
		style = activePaneStyle
	}
	if width <= 0 {
		width = 20
	}
	return style.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left,
		paneTitleStyle.Render(title),
		content,
	))
}

func approxContextStatus(runtime *chatRuntime, input string) string {
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
	return fmt.Sprintf("ctx≈%d%%", pct)
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

func splitWidths(total int) (int, int) {
	if total < 80 {
		return total, 24
	}
	main := total * 68 / 100
	side := total - main - panelGap
	if side < 24 {
		side = 24
		main = total - side - panelGap
	}
	return max(36, main), max(24, side)
}

var _ io.Writer = (*tuiLogWriter)(nil)
var _ tools.Approver = (*tuiApprover)(nil)
