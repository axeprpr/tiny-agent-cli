package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"tiny-agent-cli/internal/i18n"
	"tiny-agent-cli/internal/tools"
)

func (m chatTUIModel) View() string {
	parts := []string{
		m.renderHeader(),
		m.renderTodoSummary(),
		m.renderConversation(),
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

func (m chatTUIModel) composerHeight(width int) int {
	lines := 1 + m.input.Height()
	if hint := strings.TrimSpace(m.composerHint()); hint != "" {
		lines++
	}
	return lines + inputPaneStyle.GetVerticalFrameSize()
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
	m.input.Prompt = "│ "
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
	layout := m.computeLayout()
	maxVisible := m.height - (layout.headerHeight + layout.todoHeight + layout.conversationTitleHeight + layout.statusHeight + inputPaneStyle.GetVerticalFrameSize() + 7)
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
