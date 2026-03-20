package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"onek-agent/internal/model"
	"onek-agent/internal/tools"
)

const systemPrompt = `You are a small terminal agent.

Constraints:
- You are running one task only.
- Prefer using tools instead of guessing.
- Keep the final answer concise and actionable.
- Format the final answer for a plain terminal.
- Do not reveal chain-of-thought, hidden reasoning, or thinking process.
- Do not access files outside the workspace.
- Avoid dangerous shell commands.

Behavior:
- Inspect the workspace before editing when the task depends on local files.
- Use web_search for broad discovery and fetch_url for direct page inspection.
- When writing files, explain what you changed in the final answer.
- Return only the final answer to the user, not your reasoning.
- Do not use Markdown tables.
- Prefer short sections and simple bullet lists.
- Stop as soon as the task is complete.`

type chatClient interface {
	Complete(ctx context.Context, req model.Request) (model.Response, error)
}

type Result struct {
	Final string
	Steps int
}

type Agent struct {
	client   chatClient
	registry *tools.Registry
	maxSteps int
	log      io.Writer
}

func New(client chatClient, registry *tools.Registry, maxSteps int, log io.Writer) *Agent {
	return &Agent{
		client:   client,
		registry: registry,
		maxSteps: maxSteps,
		log:      log,
	}
}

func (a *Agent) Run(ctx context.Context, task string) (Result, error) {
	messages := []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}

	for step := 1; step <= a.maxSteps; step++ {
		a.logf("[step %d/%d] requesting model\n", step, a.maxSteps)

		resp, err := a.client.Complete(ctx, model.Request{
			Messages:   messages,
			Tools:      a.registry.Definitions(),
			ToolChoice: "auto",
		})
		if err != nil {
			return Result{}, err
		}
		if len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("model returned no choices")
		}

		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			final := SanitizeFinal(model.ContentString(msg.Content))
			if final == "" {
				final = "(empty response)"
			}
			return Result{Final: final, Steps: step}, nil
		}

		messages = append(messages, model.Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})

		a.logf("[step %d/%d] executing %d tool(s)\n", step, a.maxSteps, len(msg.ToolCalls))

		for i, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			a.logToolStart(i+1, len(msg.ToolCalls), call.Function.Name, args)

			started := time.Now()
			output, err := a.registry.Call(ctx, call.Function.Name, args)
			a.logToolFinish(time.Since(started), output, err)
			if err != nil {
				output = "tool error: " + err.Error()
			}

			messages = append(messages, model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    output,
			})
		}
	}

	return Result{}, fmt.Errorf("max steps reached without final response")
}

func (a *Agent) logf(format string, args ...any) {
	if a.log == nil {
		return
	}
	fmt.Fprintf(a.log, format, args...)
}

func (a *Agent) logToolStart(index, total int, name string, raw json.RawMessage) {
	preview := strings.TrimSpace(a.registry.Preview(name, raw))
	if preview == "" {
		a.logf("  [%d/%d] %s\n", index, total, name)
		return
	}
	a.logf("  [%d/%d] %s %s\n", index, total, name, preview)
}

func (a *Agent) logToolFinish(elapsed time.Duration, output string, err error) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	a.logf("        %s in %s", status, roundDuration(elapsed))

	summary := summarizeOutput(output)
	if summary != "" {
		a.logf(" | %s", summary)
	}
	a.logf("\n")
}

func roundDuration(d time.Duration) time.Duration {
	switch {
	case d >= time.Second:
		return d.Round(100 * time.Millisecond)
	case d >= time.Millisecond:
		return d.Round(time.Millisecond)
	default:
		return d
	}
}

func summarizeOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	first := strings.TrimSpace(lines[0])
	if len(first) > 72 {
		first = first[:72] + "..."
	}

	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%d lines, first: %s", len(lines), first)
}

func SanitizeFinal(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	for {
		start := strings.Index(text, "<think>")
		end := strings.Index(text, "</think>")
		if start == -1 || end == -1 || end < start {
			break
		}
		text = strings.TrimSpace(text[:start] + text[end+len("</think>"):])
	}

	for {
		start := strings.Index(text, "<thinking>")
		end := strings.Index(text, "</thinking>")
		if start == -1 || end == -1 || end < start {
			break
		}
		text = strings.TrimSpace(text[:start] + text[end+len("</thinking>"):])
	}

	replacements := []string{
		"<think>", "",
		"</think>", "",
		"<thinking>", "",
		"</thinking>", "",
	}
	for i := 0; i < len(replacements); i += 2 {
		text = strings.ReplaceAll(text, replacements[i], replacements[i+1])
	}

	text = stripAfterMarker(text, "Output:")
	text = stripAfterMarker(text, "Final Answer:")
	text = stripAfterMarker(text, "Answer:")
	text = stripAfterMarker(text, "Final:")

	lines := strings.Split(text, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "</think>" || trimmed == "</thinking>" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "让我") ||
			strings.HasPrefix(trimmed, "现在让我") ||
			strings.HasPrefix(lower, "let me") {
			continue
		}
		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}

	text = strings.TrimSpace(strings.Join(filtered, "\n"))
	text = trimToDirectAnswer(text)
	text = normalizeTerminalText(text)
	text = trimLeadIn(text)
	return strings.TrimSpace(text)
}

func stripAfterMarker(text, marker string) string {
	index := strings.LastIndex(text, marker)
	if index == -1 {
		return text
	}
	suffix := strings.TrimSpace(text[index+len(marker):])
	if suffix == "" {
		return text
	}
	return suffix
}

var directAnswerPattern = regexp.MustCompile(`^(there|here|summary:|result:|final answer:|the answer is|it has|it is)\b`)

func trimToDirectAnswer(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 3 {
		return text
	}

	hasReasoningPrefix := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "thinking process") ||
			strings.Contains(lower, "let me") ||
			strings.Contains(lower, "returned") ||
			strings.Contains(lower, "analyze") ||
			strings.Contains(lower, "that's ") ||
			regexp.MustCompile(`^\d+\.`).MatchString(trimmed) {
			hasReasoningPrefix = true
			break
		}
	}
	if !hasReasoningPrefix {
		return text
	}

	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if directAnswerPattern.MatchString(strings.ToLower(trimmed)) {
			return strings.Join(lines[i:], "\n")
		}
	}
	return text
}

func normalizeTerminalText(text string) string {
	text = normalizeMarkdownTables(text)

	replacer := strings.NewReplacer(
		"**", "",
		"__", "",
		"`", "",
		"### ", "",
		"## ", "",
		"# ", "",
	)
	text = replacer.Replace(text)

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func trimLeadIn(text string) string {
	text = trimToRestartHeading(text)

	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return text
	}

	first := strings.TrimSpace(lines[0])
	second := strings.TrimSpace(lines[1])
	if first == "" || second == "" {
		return text
	}

	leadPhrases := []string{
		"好的",
		"现在让我",
		"我已经",
		"下面是",
		"here is",
		"i have",
		"i've",
	}

	for _, phrase := range leadPhrases {
		if strings.Contains(strings.ToLower(first), strings.ToLower(phrase)) && looksLikeHeading(second) {
			return strings.Join(lines[1:], "\n")
		}
	}

	return text
}

func trimToRestartHeading(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 4 {
		return text
	}

	restartPhrases := []string{
		"环境信息统计",
		"总结",
		"summary",
		"environment summary",
	}

	for i := len(lines) - 1; i >= 1; i-- {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		for _, phrase := range restartPhrases {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				return strings.Join(lines[i:], "\n")
			}
		}
	}

	return text
}

func looksLikeHeading(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return false
	}
	if len([]rune(line)) > 24 {
		return false
	}
	return !strings.Contains(line, ":")
}

func normalizeMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	var out []string

	for i := 0; i < len(lines); {
		if !looksLikeTableLine(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}

		j := i
		for j < len(lines) && looksLikeTableLine(lines[j]) {
			j++
		}

		table := renderTableBlock(lines[i:j])
		if table == "" {
			out = append(out, lines[i:j]...)
		} else {
			out = append(out, table)
		}
		i = j
	}

	return strings.Join(out, "\n")
}

func looksLikeTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Count(trimmed, "|") >= 2
}

func renderTableBlock(lines []string) string {
	var rows [][]string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isTableSeparator(trimmed) {
			continue
		}

		parts := strings.Split(trimmed, "|")
		var cells []string
		for _, part := range parts {
			cell := strings.TrimSpace(part)
			if cell != "" {
				cells = append(cells, cell)
			}
		}
		if len(cells) >= 2 {
			rows = append(rows, cells)
		}
	}

	if len(rows) == 0 {
		return ""
	}

	if len(rows) >= 2 && len(rows[0]) == 2 {
		var out []string
		start := 0
		if len(rows[0]) == len(rows[1]) && isLikelyHeader(rows[0]) {
			start = 1
		}
		for _, row := range rows[start:] {
			out = append(out, fmt.Sprintf("- %s: %s", row[0], strings.Join(row[1:], " | ")))
		}
		return strings.Join(out, "\n")
	}

	var out []string
	for _, row := range rows {
		out = append(out, "- "+strings.Join(row, " | "))
	}
	return strings.Join(out, "\n")
}

func isTableSeparator(line string) bool {
	line = strings.ReplaceAll(line, "|", "")
	line = strings.ReplaceAll(line, "-", "")
	line = strings.ReplaceAll(line, ":", "")
	line = strings.TrimSpace(line)
	return line == ""
}

func isLikelyHeader(row []string) bool {
	if len(row) != 2 {
		return false
	}
	left := strings.ToLower(strings.TrimSpace(row[0]))
	right := strings.ToLower(strings.TrimSpace(row[1]))
	headers := map[string]bool{
		"item":    true,
		"value":   true,
		"项目":      true,
		"信息":      true,
		"type":    true,
		"entry":   true,
		"版本":      true,
		"version": true,
	}
	return headers[left] || headers[right]
}
