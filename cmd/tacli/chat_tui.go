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
	showDrawer   bool
	logFilter    string
}

var (
	appStyle    = lipgloss.NewStyle().Padding(0, 1)
	headerStyle = lipgloss.NewStyle().
			Padding(0, 0, 1, 0)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
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
				Foreground(lipgloss.Color("246")).
				Bold(true)

	userLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42")).
				Bold(true)

	systemLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Bold(true)

	userCardStyle      = lipgloss.NewStyle()
	assistantCardStyle = lipgloss.NewStyle()
	systemCardStyle    = lipgloss.NewStyle()
	errorCardStyle     = lipgloss.NewStyle()

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
			Foreground(lipgloss.Color("244")).
			Padding(1, 0, 1, 0)

	paneTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Bold(true).
			Padding(0, 0, 1, 0)

	approvalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(1).
			Background(lipgloss.Color("236"))

	inputPaneStyle = lipgloss.NewStyle().
			Padding(0, 0, 1, 0)

	inputTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
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
	ta.Placeholder = "输入消息或 /help..."
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
	m.refreshInputState()
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

func runTaskCmd(runtime *chatRuntime, events chan tea.Msg, task string) tea.Cmd {
	return func() tea.Msg {
		output, err := runtime.executeTask(context.Background(), task)
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

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.InterruptMsg:
		m.runtime.beforeExit()
		return m, tea.Quit
	case tea.KeyMsg:
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
			m.showDrawer = !m.showDrawer
			if m.showDrawer {
				m.statusText = "activity drawer shown"
			} else {
				m.statusText = "activity drawer hidden"
			}
		case key.Matches(msg, m.keys.Filter):
			keyHandled = true
			m.logFilter = nextLogFilter(m.logFilter)
			if m.showDrawer {
				m.statusText = "log filter: " + m.logFilter
			} else {
				m.statusText = "activity filter updated"
			}
			m.refreshViewports()
		case key.Matches(msg, m.keys.PageUp):
			keyHandled = true
			m.chatViewport.HalfViewUp()
		case key.Matches(msg, m.keys.PageDown):
			keyHandled = true
			m.chatViewport.HalfViewDown()
		case key.Matches(msg, m.keys.Newline):
			keyHandled = true
			m.input.InsertString("\n")
		case key.Matches(msg, m.keys.Send):
			keyHandled = true
			task := strings.TrimSpace(m.input.Value())
			if task == "" {
				break
			}
			if m.approval == nil && m.busy {
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
					m.entries = append(m.entries, tuiEntry{role: "system", text: "approval pending: reply with y, n, or a"})
					m.statusText = "approval required"
				}
				m.refreshInputState()
				m.refreshViewports()
				break
			}
			if strings.HasPrefix(task, "/") {
				result := m.runtime.executeCommand(task)
				if result.handled {
					if strings.TrimSpace(result.output) != "" {
						m.entries = append(m.entries, tuiEntry{role: "system", text: result.output})
						m.refreshViewports()
					}
					m.refreshInputState()
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
			m.refreshInputState()
			cmds = append(cmds, runTaskCmd(m.runtime, m.events, task))
		}
	case tuiLogMsg:
		m.stepText = nextStepStatus(m.stepText, msg.kind, msg.text)
		m.logs = append(m.logs, tuiLogEntry{kind: msg.kind, text: msg.text})
		m.appendActivityEntry(msg.kind, msg.text)
		m.refreshViewports()
		cmds = append(cmds, waitForTUIEvent(m.events))
	case tuiApprovalMsg:
		m.approval = &msg
		m.logs = append(m.logs, tuiLogEntry{kind: "approval", text: msg.title + ": " + strings.ReplaceAll(msg.body, "\n", " | ")})
		m.entries = append(m.entries, tuiEntry{role: "system", text: renderApprovalInlineText(&msg)})
		m.statusText = "approval required"
		m.refreshInputState()
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
		m.refreshInputState()
		m.refreshViewports()
		cmds = append(cmds, waitForTUIEvent(m.events))
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
		"activity=" + drawerStateLabel(m.showDrawer),
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

	content := m.renderConversation()
	if m.showDrawer {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", m.renderActivityDrawer())
	}

	parts := []string{
		header,
		content,
		m.renderComposer(),
		statusStyle.Width(max(0, m.width-2)).Render(strings.Join(statusParts, "  ")),
		helpView,
	}
	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
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
	availableHeight := m.height - extra
	drawerHeight := 0
	if m.showDrawer {
		drawerHeight = activityDrawerHeight(availableHeight)
	}
	m.chatViewport.Height = max(6, availableHeight-drawerHeight)
	m.logViewport.Height = drawerHeight
	m.chatViewport.Width = max(18, contentWidth)
	m.logViewport.Width = max(18, contentWidth)
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
	bodyWidth := max(20, m.chatViewport.Width-3)
	for _, entry := range m.entries {
		var label lipgloss.Style
		var body lipgloss.Style
		switch entry.role {
		case "user":
			label = userLabelStyle
			body = messageBodyStyle
		case "assistant":
			label = assistantLabelStyle
			body = codeBodyStyle
		case "activity":
			label = activityLabelStyle
			body = logBodyStyle
		case "system":
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
			continue
		}
		if entry.role == "assistant" {
			text = renderMarkdown(text, bodyWidth)
		}
		block := lipgloss.JoinVertical(
			lipgloss.Left,
			label.Render(strings.ToUpper(entry.role)),
			body.Width(bodyWidth).Render(text),
		)
		rendered = append(rendered, block)
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
		titleStyle.Render("tiny-agent-cli"),
		" ",
		subtitleStyle.Render("tiny terminal agent for local shells"),
		"  ",
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
		lipgloss.JoinVertical(
			lipgloss.Left,
			titleRow,
			chipMutedStyle.Render(strings.Join(chips, "  ·  ")),
		),
	)
}

func (m chatTUIModel) renderConversation() string {
	title := paneTitleStyle.Width(max(20, m.chatViewport.Width)).Render(
		fmt.Sprintf("Conversation  %d msg", len(m.entries)),
	)
	return lipgloss.JoinVertical(lipgloss.Left, title, m.chatViewport.View())
}

func (m chatTUIModel) renderActivityDrawer() string {
	title := paneTitleStyle.Width(max(20, m.logViewport.Width)).Render(
		fmt.Sprintf("Activity drawer  %s  %d evt  Ctrl+O hide", strings.ToUpper(m.logFilter), m.filteredLogCount()),
	)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", max(20, m.logViewport.Width))),
		title,
		m.logViewport.View(),
	)
}

func drawerStateLabel(show bool) string {
	if show {
		return "drawer"
	}
	return "hidden"
}

func (m chatTUIModel) renderComposer() string {
	hints := inputHintStyle.Render(m.composerHint())
	return inputPaneStyle.Width(max(20, m.width-2)).Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", max(20, m.width-2))),
			hints,
			m.input.View(),
		),
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

func (m *chatTUIModel) refreshInputState() {
	m.input.Prompt = ""
	m.input.SetHeight(1)
	switch {
	case m.approval != nil:
		m.input.Placeholder = "输入 y / n / a ..."
	case m.busy:
		m.input.Placeholder = "正在执行，等待下一步..."
	default:
		m.input.Placeholder = "输入消息或 /help..."
	}
}

func (m chatTUIModel) composerHint() string {
	switch {
	case m.approval != nil:
		return "审批等待中，输入 y 同意，n 拒绝，a 设为 dangerously"
	case m.busy:
		return "执行过程已并入上方对话，当前任务运行中"
	default:
		return fmt.Sprintf("当前会话 %s，Enter 发送，Ctrl+J 换行，Ctrl+O 活动，/help 命令", m.runtime.sessionName)
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
		m.entries = append(m.entries, tuiEntry{role: "activity", text: "[tool] " + text})
	case isToolResultLine(text):
		if m.mergeLastActivityLine(text) {
			return
		}
		m.entries = append(m.entries, tuiEntry{role: "activity", text: "[tool]\n" + normalizeActivityContinuation(text)})
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

	lines := []string{strings.TrimSpace(msg.title)}
	titleLower := strings.ToLower(strings.TrimSpace(msg.title))
	switch {
	case strings.Contains(titleLower, "command"):
		lines = append(lines, strings.TrimSpace(msg.body))
	case strings.Contains(titleLower, "file write"):
		fields := parseApprovalFields(msg.body)
		if path := fields["path"]; path != "" {
			lines = append(lines, "path: "+path)
		}
		if bytes := fields["bytes"]; bytes != "" {
			lines = append(lines, "bytes: "+bytes)
		}
		if preview := fields["preview"]; preview != "" {
			lines = append(lines, "preview:")
			lines = append(lines, strings.ReplaceAll(preview, "\\n", "\n"))
		}
	default:
		lines = append(lines, strings.TrimSpace(msg.body))
	}
	lines = append(lines, "reply with y, n, or a")
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
		return "approval granted"
	case "always":
		return "approval mode switched to dangerously"
	default:
		return "approval denied"
	}
}

func approvalResponseText(answer string) string {
	switch answer {
	case "yes":
		return "approval granted"
	case "always":
		return "approval granted and mode switched to dangerously"
	default:
		return "approval denied"
	}
}

var _ io.Writer = (*tuiLogWriter)(nil)
var _ tools.Approver = (*tuiApprover)(nil)
