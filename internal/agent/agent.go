package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

const (
	maxToolMessageChars    = 6000
	maxConversationChars   = 24000
	retryConversationChars = 12000
	maxSummaryChars        = 1800
	recentTurnsToKeep      = 3
)

const syntheticSummaryPrefix = "[compacted summary]"

const systemPrompt = `You are a small terminal agent.

Constraints:
- You are running one task only.
- Use a private PDCA loop for each task: Plan, Do, Check, Act.
- Use a private ReAct loop: observe the task, choose the next action, use a tool if needed, check the result.
- Prefer using tools instead of guessing.
- The terminal prints your assistant message directly to the user.
- Keep the final answer concise, actionable, and terminal-friendly.
- Keep planning private.
- Do not reveal chain-of-thought, hidden reasoning, thinking process, or self-talk.
- Your visible reply must contain only tool calls or the user-facing answer.
- Never emit <think>, </think>, <thinking>, analysis tags, or hidden-reasoning markers.
- Do not access files outside the workspace.
- Avoid dangerous shell commands.

Behavior:
- Start with a short internal plan before acting, but keep it silent.
- Choose the smallest useful next step, then verify the result before continuing.
- Do not narrate intent with phrases like "let me", "I will", "I am going to", or "first I will".
- Do not describe the user or their request in the third person.
- Do not say that you will remember, confirm, summarize, or prepare an answer.
- Do not repeat or summarize the user's request unless it is necessary for the final answer.
- For simple requests, answer directly in 1 to 3 short lines.
- If no tool is needed, reply immediately with the answer and no preamble.
- Inspect the workspace before editing when the task depends on local files.
- When a tool is needed, call it instead of merely describing it.
- Use web_search for broad discovery and fetch_url for direct page inspection.
- Prefer plain text over Markdown tables unless the user explicitly asks for tables.
- When writing files, explain what you changed in the final answer.
- Return only the final answer to the user, not your reasoning.
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

type Session struct {
	agent    *Agent
	messages []model.Message
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
	return a.NewSession().RunTask(ctx, task)
}

func (a *Agent) NewSession() *Session {
	return a.NewSessionWithMemory("")
}

func (a *Agent) NewSessionWithMemory(memoryText string) *Session {
	return &Session{
		agent: a,
		messages: []model.Message{
			{Role: "system", Content: SystemPromptWithMemory(memoryText)},
		},
	}
}

func SystemPromptWithMemory(memoryText string) string {
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + memoryText
}

func (s *Session) Messages() []model.Message {
	out := make([]model.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *Session) ReplaceMessages(messages []model.Message) {
	s.messages = make([]model.Message, len(messages))
	copy(s.messages, messages)
}

func (s *Session) SetAgent(agent *Agent) {
	s.agent = agent
}

func (s *Session) RunTask(ctx context.Context, task string) (Result, error) {
	s.messages = append(s.messages, model.Message{
		Role:    "user",
		Content: task,
	})

	for step := 1; step <= s.agent.maxSteps; step++ {
		finalStep := step == s.agent.maxSteps
		s.messages = compactConversation(s.messages, maxConversationChars)
		if finalStep {
			s.agent.logf("[step %d/%d] finalizing answer with current context\n", step, s.agent.maxSteps)
		} else {
			s.agent.logf("[step %d/%d] requesting model\n", step, s.agent.maxSteps)
		}

		req := s.buildRequest(finalStep)
		resp, err := s.agent.client.Complete(ctx, req)
		if err != nil {
			if isContextLengthError(err) {
				trimmed := compactConversation(s.messages, retryConversationChars)
				if len(trimmed) < len(s.messages) {
					s.messages = trimmed
					s.agent.logf("[step %d/%d] context too long, retrying with shorter history\n", step, s.agent.maxSteps)
					req = s.buildRequest(finalStep)
					resp, err = s.agent.client.Complete(ctx, req)
				}
			}
		}
		if err != nil {
			return Result{}, err
		}
		if len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("model returned no choices")
		}

		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 || finalStep {
			final := finalResponseText(msg, finalStep, s.agent.maxSteps)
			s.messages = append(s.messages, model.Message{
				Role:    "assistant",
				Content: msg.Content,
			})
			return Result{Final: final, Steps: step}, nil
		}

		s.messages = append(s.messages, model.Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})

		s.agent.logf("[step %d/%d] executing %d tool(s)\n", step, s.agent.maxSteps, len(msg.ToolCalls))

		for i, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			s.agent.logToolStart(i+1, len(msg.ToolCalls), call.Function.Name, args)

			started := time.Now()
			output, err := s.agent.registry.Call(ctx, call.Function.Name, args)
			output = truncateToolMessage(output, maxToolMessageChars)
			s.agent.logToolFinish(time.Since(started), output, err)
			if err != nil {
				output = "tool error: " + err.Error()
			}

			s.messages = append(s.messages, model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    output,
			})
		}
	}

	return Result{}, fmt.Errorf("%s", stepBudgetReachedText(s.agent.maxSteps))
}

func (s *Session) buildRequest(finalStep bool) model.Request {
	req := model.Request{
		Messages: s.messages,
	}
	if finalStep {
		req.ToolChoice = "none"
		return req
	}
	req.Tools = s.agent.registry.Definitions()
	req.ToolChoice = "auto"
	return req
}

func finalResponseText(msg model.Message, finalStep bool, maxSteps int) string {
	final := strings.TrimSpace(model.ContentString(msg.Content))
	if final != "" {
		return final
	}
	if finalStep {
		return stepBudgetReachedText(maxSteps)
	}
	return "(empty response)"
}

func stepBudgetReachedText(maxSteps int) string {
	return fmt.Sprintf("Step budget reached (%d). The session is still active. Send `continue` to keep going, or ask a narrower follow-up. You can usually continue until the context window fills up", maxSteps)
}

func truncateToolMessage(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 || len(text) <= limit {
		return text
	}

	head := limit * 3 / 4
	tail := limit / 4
	if head <= 0 || tail <= 0 {
		return text[:limit] + "\n...[truncated]"
	}
	return text[:head] + "\n...[truncated]...\n" + text[len(text)-tail:]
}

func compactConversation(messages []model.Message, limit int) []model.Message {
	compacted := stripSyntheticSummaries(messages)
	compacted = preprocessConversation(compacted)
	if conversationSize(compacted) <= limit {
		return compacted
	}

	if summarized := summarizeConversationHistory(compacted, limit); len(summarized) > 0 {
		return summarized
	}

	return pruneConversation(compacted, limit)
}

func preprocessConversation(messages []model.Message) []model.Message {
	compacted := make([]model.Message, len(messages))
	copy(compacted, messages)

	for i := 1; i < len(compacted)-2; i++ {
		switch compacted[i].Role {
		case "assistant":
			if len(compacted[i].ToolCalls) > 0 {
				compacted[i].Content = ""
			} else {
				compacted[i].Content = truncateToolMessage(model.ContentString(compacted[i].Content), 1200)
			}
		case "tool":
			compacted[i].Content = truncateToolMessage(model.ContentString(compacted[i].Content), 900)
		}
	}
	return compacted
}

func stripSyntheticSummaries(messages []model.Message) []model.Message {
	if len(messages) <= 1 {
		return messages
	}
	out := make([]model.Message, 0, len(messages))
	out = append(out, messages[0])
	for _, msg := range messages[1:] {
		if msg.Role == "system" && strings.HasPrefix(model.ContentString(msg.Content), syntheticSummaryPrefix) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func pruneConversation(messages []model.Message, limit int) []model.Message {
	if len(messages) <= 2 || limit <= 0 {
		return messages
	}

	kept := make([]model.Message, 0, len(messages))
	kept = append(kept, messages[0])
	total := messageSize(messages[0])

	var tail []model.Message
	for i := len(messages) - 1; i >= 1; i-- {
		size := messageSize(messages[i])
		if total+size > limit {
			continue
		}
		tail = append(tail, messages[i])
		total += size
	}

	for i := len(tail) - 1; i >= 0; i-- {
		kept = append(kept, tail[i])
	}
	return kept
}

func summarizeConversationHistory(messages []model.Message, limit int) []model.Message {
	if len(messages) <= 2 || limit <= 0 {
		return messages
	}

	turns := splitConversationTurns(messages[1:])
	if len(turns) <= 1 {
		return pruneConversation(messages, limit)
	}

	maxKeep := minInt(recentTurnsToKeep, len(turns))
	for keep := maxKeep; keep >= 1; keep-- {
		older := turns[:len(turns)-keep]
		recent := turns[len(turns)-keep:]

		candidate := []model.Message{messages[0]}
		if len(older) > 0 {
			summary := summarizeTurns(older, minInt(maxSummaryChars, maxInt(400, limit/3)))
			if strings.TrimSpace(summary) != "" {
				candidate = append(candidate, model.Message{
					Role:    "system",
					Content: syntheticSummaryPrefix + "\n" + summary,
				})
			}
		}
		for _, turn := range recent {
			candidate = append(candidate, turn...)
		}
		if conversationSize(candidate) <= limit {
			return candidate
		}
	}

	summary := summarizeTurns(turns, minInt(maxSummaryChars, maxInt(300, limit/2)))
	if strings.TrimSpace(summary) == "" {
		return pruneConversation(messages, limit)
	}
	candidate := []model.Message{
		messages[0],
		{Role: "system", Content: syntheticSummaryPrefix + "\n" + summary},
	}
	return pruneConversation(candidate, limit)
}

func splitConversationTurns(messages []model.Message) [][]model.Message {
	var turns [][]model.Message
	var current []model.Message

	flush := func() {
		if len(current) == 0 {
			return
		}
		cp := make([]model.Message, len(current))
		copy(cp, current)
		turns = append(turns, cp)
		current = nil
	}

	for _, msg := range messages {
		if msg.Role == "user" && len(current) > 0 {
			flush()
		}
		current = append(current, msg)
	}
	flush()
	return turns
}

func summarizeTurns(turns [][]model.Message, limit int) string {
	if len(turns) == 0 || limit <= 0 {
		return ""
	}
	lines := []string{"Earlier conversation:"}
	total := len(lines[0])

	appendLine := func(line string) bool {
		line = strings.TrimSpace(line)
		if line == "" {
			return true
		}
		next := total + len(line) + 1
		if next > limit {
			return false
		}
		lines = append(lines, line)
		total = next
		return true
	}

	for _, turn := range turns {
		for _, msg := range turn {
			line := summarizeContextMessage(msg)
			if !appendLine(line) {
				return strings.Join(lines, "\n")
			}
		}
	}
	return strings.Join(lines, "\n")
}

func summarizeContextMessage(msg model.Message) string {
	switch msg.Role {
	case "system":
		text := strings.TrimSpace(model.ContentString(msg.Content))
		if strings.HasPrefix(text, syntheticSummaryPrefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, syntheticSummaryPrefix))
		}
		if text == "" {
			return ""
		}
		return "- prior summary: " + compactSummaryText(text, 180)
	case "user":
		text := compactSummaryText(model.ContentString(msg.Content), 180)
		if text == "" {
			return ""
		}
		return "- user: " + text
	case "assistant":
		if len(msg.ToolCalls) > 0 {
			var names []string
			seen := map[string]bool{}
			for _, call := range msg.ToolCalls {
				if call.Function.Name == "" || seen[call.Function.Name] {
					continue
				}
				seen[call.Function.Name] = true
				names = append(names, call.Function.Name)
			}
			if len(names) == 0 {
				return "- assistant used tools"
			}
			return "- assistant used tools: " + strings.Join(names, ", ")
		}
		text := compactSummaryText(model.ContentString(msg.Content), 160)
		if text == "" {
			return ""
		}
		return "- assistant: " + text
	default:
		return ""
	}
}

func compactSummaryText(text string, limit int) string {
	text = strings.TrimSpace(FormatTerminalOutput(text))
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func conversationSize(messages []model.Message) int {
	total := 0
	for _, msg := range messages {
		total += messageSize(msg)
	}
	return total
}

func messageSize(msg model.Message) int {
	size := len(model.ContentString(msg.Content)) + len(msg.Role) + len(msg.ToolCallID)
	for _, call := range msg.ToolCalls {
		size += len(call.ID) + len(call.Type) + len(call.Function.Name) + len(call.Function.Arguments)
	}
	return size
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context length") ||
		strings.Contains(text, "input tokens") ||
		strings.Contains(text, "maximum input length")
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

func FormatTerminalOutput(text string) string {
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
		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}

	text = strings.TrimSpace(strings.Join(filtered, "\n"))
	text = normalizeTerminalText(text)
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
