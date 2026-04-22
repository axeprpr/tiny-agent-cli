package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type chatTUILayout struct {
	appWidth                int
	contentWidth            int
	viewportWidth           int
	viewportHeight          int
	inputWidth              int
	headerHeight            int
	todoHeight              int
	conversationTitleHeight int
	composerHeight          int
	statusHeight            int
	chatTop                 int
}

func (m chatTUIModel) computeLayout() chatTUILayout {
	layout := chatTUILayout{
		appWidth:      max(20, m.width-2),
		contentWidth:  max(20, m.width-4),
		viewportWidth: max(18, m.width-4),
		inputWidth:    max(20, m.width-8),
		statusHeight:  1,
	}

	if m.runtime != nil {
		layout.headerHeight = lipgloss.Height(m.renderHeader())
		if todo := strings.TrimSpace(m.renderTodoSummary()); todo != "" {
			layout.todoHeight = lipgloss.Height(todo)
		}
		layout.conversationTitleHeight = m.conversationTitleHeightForWidth(layout.viewportWidth)
	}

	layout.composerHeight = m.composerHeight(layout.appWidth)
	layout.chatTop = layout.headerHeight + layout.todoHeight + layout.conversationTitleHeight

	occupied := layout.headerHeight + layout.todoHeight + layout.conversationTitleHeight + layout.composerHeight + layout.statusHeight + appStyle.GetVerticalFrameSize()
	layout.viewportHeight = max(6, m.height-occupied)

	return layout
}

func (m *chatTUIModel) reconcileInputHeight(prevHeight int) bool {
	newHeight := m.desiredInputHeight()
	if newHeight != m.input.Height() {
		m.input.SetHeight(newHeight)
	}
	return m.input.Height() != prevHeight
}

func (m *chatTUIModel) resize(forceRefresh bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	layout := m.computeLayout()
	m.chatTop = layout.chatTop
	m.chatViewport.Height = layout.viewportHeight

	widthChanged := m.chatViewport.Width != layout.viewportWidth
	m.chatViewport.Width = layout.viewportWidth

	prevInputHeight := m.input.Height()
	m.input.SetWidth(layout.inputWidth)
	if m.reconcileInputHeight(prevInputHeight) {
		layout = m.computeLayout()
		m.chatTop = layout.chatTop
		m.chatViewport.Height = layout.viewportHeight
	}

	if widthChanged {
		m.entriesDirty = true
	}
	if widthChanged || forceRefresh {
		m.refreshViewports(forceRefresh)
	}
}

func (m *chatTUIModel) refreshViewports(forceReanchor bool) {
	if m.chatViewport.Width <= 0 {
		return
	}
	if m.entriesDirty || m.entriesWidth != m.chatViewport.Width {
		offset := m.chatViewport.YOffset
		m.renderedBody = m.renderEntries()
		m.chatViewport.SetContent(m.renderedBody)
		if m.stickToBottom {
			m.chatViewport.GotoBottom()
		} else {
			m.chatViewport.SetYOffset(offset)
		}
		m.entriesDirty = false
		m.entriesWidth = m.chatViewport.Width
		return
	}
	if forceReanchor && m.stickToBottom {
		m.chatViewport.GotoBottom()
	}
}

