package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type inlineInputSubmitMsg struct{}

type inlineInputModel struct {
	input     textarea.Model
	width     int
	height    int
	submitted bool
	canceled  bool
	value     string
}

var (
	inlineInputFrameStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 1)
	inlineTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Bold(true)
	inlineHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))
	inlineUserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)
	inlineAssistantStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("81")).
				Bold(true)
)

func newInlineInputModel() inlineInputModel {
	ta := textarea.New()
	ta.Focus()
	ta.Prompt = ""
	ta.Placeholder = "输入消息，Enter 发送，Ctrl+J 换行，Ctrl+C 退出"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.SetWidth(72)
	return inlineInputModel{
		input: ta,
		width: 80,
	}
}

func (m inlineInputModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m inlineInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(24, msg.Width-6))
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.canceled = true
			return m, tea.Quit
		case tea.KeyEnter:
			task := sanitizeSubmittedInput(m.input.Value())
			if task == "" {
				return m, nil
			}
			m.value = task
			m.submitted = true
			return m, tea.Quit
		}
		if msg.Type == tea.KeyCtrlJ {
			m.input.InsertString("\n")
			m.reconcileHeight()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.reconcileHeight()
	return m, cmd
}

func (m inlineInputModel) View() string {
	width := max(24, m.width-2)
	title := inlineTitleStyle.Render("Message")
	hint := inlineHintStyle.Render("Enter 发送  Ctrl+J 换行  Ctrl+C 退出")
	body := lipgloss.JoinVertical(lipgloss.Left, title, hint, m.input.View())
	return inlineInputFrameStyle.Width(width).Render(body)
}

func (m *inlineInputModel) reconcileHeight() {
	lines := max(1, strings.Count(m.input.Value(), "\n")+1)
	m.input.SetHeight(min(max(3, lines), 12))
}

func runChatInline(runtime *chatRuntime) int {
	if runtime == nil {
		return 1
	}

	printNativeChatBanner(runtime)
	for {
		task, interrupted, err := promptInlineInput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "chat input error: %v\n", err)
			runtime.beforeExit(true)
			return 1
		}
		if interrupted {
			runtime.beforeExit(true)
			return 0
		}
		if task == "" {
			continue
		}

		printInlineUserTurn(task)

		if strings.HasPrefix(task, "/") {
			result := runtime.executeCommand(task)
			if result.handled {
				if strings.TrimSpace(result.output) != "" {
					printInlineSystemTurn(result.output)
				}
				if result.exitCode >= 0 {
					runtime.beforeExit(true)
					return result.exitCode
				}
				continue
			}
		}

		output, runErr := runtime.executeTask(context.Background(), task)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", runErr)
			continue
		}
		printInlineAssistantTurn(output)
	}
}

func promptInlineInput() (task string, interrupted bool, err error) {
	model := newInlineInputModel()
	p := tea.NewProgram(
		model,
		tea.WithEnvironment(os.Environ()),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)

	finalModel, runErr := p.Run()
	if runErr != nil {
		return "", false, runErr
	}
	result := finalModel.(inlineInputModel)
	clearInlineInputView(result.View())
	if result.canceled {
		return "", true, nil
	}
	return strings.TrimSpace(result.value), false, nil
}

func clearInlineInputView(view string) {
	lineCount := strings.Count(strings.ReplaceAll(view, "\r\n", "\n"), "\n") + 1
	for i := 0; i < lineCount; i++ {
		fmt.Fprint(os.Stdout, "\r\033[2K")
		if i < lineCount-1 {
			fmt.Fprint(os.Stdout, "\033[1A")
		}
	}
	fmt.Fprint(os.Stdout, "\r")
}

func printInlineUserTurn(text string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, inlineUserStyle.Render("> "+lines[0]))
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
	fmt.Fprintln(os.Stdout, inlineAssistantStyle.Render("Assistant"))
	rendered := renderInlineMarkdown(text)
	if strings.TrimSpace(rendered) == "" {
		rendered = text
	}
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

