package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type inlineShellTaskStartedMsg struct{}

type inlineShellTaskFinishedMsg struct{}

type inlineShellModel struct {
	input    textarea.Model
	submitCh chan string
	width    int
	height   int
	busy     bool
	status   string
}

type inlineShellRunResult struct {
	model inlineShellModel
	err   error
}

var (
	inlineComposerStyle = lipgloss.NewStyle().
			Padding(0, 0, 0, 1)
	inlineRuleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))
	inlineHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))
	inlineStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Bold(true)
	inlinePromptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)
	inlineUserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Bold(true)
	inlineAssistantStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
)

func newInlineShellModel(submitCh chan string) inlineShellModel {
	ta := textarea.New()
	ta.Focus()
	ta.Prompt = inlinePromptStyle.Render("› ")
	ta.Placeholder = "输入消息"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.SetWidth(72)
	return inlineShellModel{
		input:    ta,
		submitCh: submitCh,
		width:    80,
		status:   "ready",
	}
}

func (m inlineShellModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m inlineShellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(24, msg.Width-6))
		return m, nil
	case inlineShellTaskStartedMsg:
		m.busy = true
		m.status = "running"
		m.input.Blur()
		m.input.Placeholder = "生成中..."
		return m, nil
	case inlineShellTaskFinishedMsg:
		m.busy = false
		m.status = "ready"
		m.input.Placeholder = "输入消息"
		return m, m.input.Focus()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.busy {
				return m, nil
			}
			task := sanitizeSubmittedInput(m.input.Value())
			if task == "" {
				return m, nil
			}
			m.input.Reset()
			m.reconcileHeight()
			return m, submitInlineTaskCmd(m.submitCh, task)
		}
		if m.busy {
			return m, nil
		}
		if msg.Type == tea.KeyCtrlJ {
			m.input.InsertString("\n")
			m.reconcileHeight()
			return m, nil
		}
	}

	if m.busy {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.reconcileHeight()
	return m, cmd
}

func (m inlineShellModel) View() string {
	width := min(96, max(28, m.width-2))
	rule := inlineRuleStyle.Render(strings.Repeat("─", width))
	meta := inlineStatusStyle.Render("enter 发送") + "  " + inlineHintStyle.Render("ctrl+j 换行  ctrl+c 退出") + "  " + inlineHintStyle.Render(m.status)
	body := lipgloss.JoinVertical(lipgloss.Left, rule, inlineComposerStyle.Width(width).Render(m.input.View()), meta)
	return body
}

func (m *inlineShellModel) reconcileHeight() {
	lines := max(1, strings.Count(m.input.Value(), "\n")+1)
	m.input.SetHeight(min(max(3, lines), 12))
}

func submitInlineTaskCmd(ch chan string, task string) tea.Cmd {
	return func() tea.Msg {
		ch <- task
		return inlineShellTaskStartedMsg{}
	}
}

func runChatInline(runtime *chatRuntime) int {
	if runtime == nil {
		return 1
	}

	printNativeChatBanner(runtime)

	submitCh := make(chan string, 1)
	model := newInlineShellModel(submitCh)
	program := tea.NewProgram(
		model,
		tea.WithEnvironment(os.Environ()),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)

	resultCh := make(chan inlineShellRunResult, 1)
	go func() {
		finalModel, err := program.Run()
		if finalModel == nil {
			resultCh <- inlineShellRunResult{err: err}
			return
		}
		resultCh <- inlineShellRunResult{model: finalModel.(inlineShellModel), err: err}
	}()

	for {
		select {
		case res := <-resultCh:
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "chat input error: %v\n", res.err)
				runtime.beforeExit(true)
				return 1
			}
			runtime.beforeExit(true)
			return 0
		case task := <-submitCh:
			exitCode, quit := handleInlineTask(program, runtime, task)
			if quit {
				program.Send(tea.Quit())
				<-resultCh
				runtime.beforeExit(true)
				return exitCode
			}
			program.Send(inlineShellTaskFinishedMsg{})
		}
	}
}

func handleInlineTask(program *tea.Program, runtime *chatRuntime, task string) (exitCode int, quit bool) {
	if err := program.ReleaseTerminal(); err != nil {
		fmt.Fprintf(os.Stderr, "terminal error: %v\n", err)
	}
	defer func() {
		if err := program.RestoreTerminal(); err != nil {
			fmt.Fprintf(os.Stderr, "terminal restore error: %v\n", err)
		}
	}()

	printInlineUserTurn(task)

	if strings.HasPrefix(task, "/") {
		result := runtime.executeCommand(task)
		if strings.TrimSpace(result.output) != "" {
			printInlineSystemTurn(result.output)
		}
		if result.exitCode >= 0 {
			return result.exitCode, true
		}
		return 0, false
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-done:
		}
	}()

	output, err := runtime.executeTask(ctx, task)
	close(done)
	signal.Stop(sigCh)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		return 0, false
	}
	printInlineAssistantTurn(output)
	return 0, false
}

func printInlineUserTurn(text string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, inlineUserStyle.Render("› "+lines[0]))
	for _, line := range lines[1:] {
		fmt.Fprintln(os.Stdout, "  "+line)
	}
}

func printInlineSystemTurn(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	fmt.Fprintln(os.Stdout, text)
}

func printInlineAssistantTurn(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		fmt.Fprintln(os.Stdout)
		return
	}
	rendered := renderInlineMarkdown(text)
	if strings.TrimSpace(rendered) == "" {
		rendered = text
	}
	fmt.Fprintln(os.Stdout, inlineAssistantStyle.Render("•"))
	fmt.Fprintln(os.Stdout, rendered)
}

func renderInlineMarkdown(text string) string {
	width := inlineMarkdownWidth()
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("notty"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return strings.TrimSpace(text)
	}
	out, err := renderer.Render(strings.TrimSpace(text))
	if err != nil {
		return strings.TrimSpace(text)
	}
	return strings.TrimRight(out, "\n")
}

func inlineMarkdownWidth() int {
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		if width, _, err := term.GetSize(fd); err == nil {
			return max(40, width-2)
		}
	}
	return 100
}
