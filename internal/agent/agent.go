package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

const (
	maxToolMessageChars    = 6000
	maxSummaryChars        = 1800
	recentTurnsToKeep      = 3
	maxConsecutiveToolErrs = 3
	repeatedToolWindow     = 4
	todoNagRoundThreshold  = 3
)

const syntheticSummaryPrefix = "[compacted summary]"
const todoNagPrefix = "[todo reminder]"
const systemReminderTag = "<system-reminder>"
const postToolAnswerReminder = "<system-reminder>tool-results-ready\nTool results are available now. Answer the user's latest request directly using the evidence already gathered. Only call another tool if the current evidence is clearly insufficient. Do not leave the response empty."
const emptyToolAnswerRetryReminder = "<system-reminder>answer-now\nAnswer the user's latest request directly using the tool results above. Do not leave the response empty."
const failedCommandRetryReminder = "<system-reminder>recover-command\nThe last shell command failed. If the task can still be completed by correcting or retrying the command, do that now. Otherwise answer directly using the failure output. Do not leave the response empty."
const fileEvidenceRetryReminder = "<system-reminder>need-direct-content\nThe user asked for actual file or content details. Use read_file or another direct content tool before answering."
const urlEvidenceRetryReminder = "<system-reminder>need-official-url\nThe user asked for a repository, official page, or exact URL. Prefer GitHub, README, official docs, or fetch_url before answering."

type turnAction int

const (
	turnActionFinish turnAction = iota
	turnActionRetry
	turnActionExecuteTools
)

type turnDecision struct {
	action   turnAction
	reminder string
	final    string
	logLine  string
}

func (a turnAction) String() string {
	switch a {
	case turnActionRetry:
		return "retry"
	case turnActionExecuteTools:
		return "execute_tools"
	default:
		return "finish"
	}
}

type chatClient interface {
	Complete(ctx context.Context, req model.Request) (model.Response, error)
}

type Result struct {
	Final string
	Steps int
}

type TurnSummary struct {
	Turn        int             `json:"turn"`
	Decision    string          `json:"decision"`
	Assistant   model.Message   `json:"assistant"`
	ToolResults []model.Message `json:"tool_results,omitempty"`
	Reminder    string          `json:"reminder,omitempty"`
	Final       string          `json:"final,omitempty"`
}

type StreamClient interface {
	CompleteStream(ctx context.Context, req model.Request) (<-chan model.StreamChunk, <-chan error)
}

type EventSink interface {
	RecordAgentEvent(ctx context.Context, event AgentEvent)
}

type AgentEvent struct {
	Time time.Time
	Type string
	Data map[string]any
}

type Agent struct {
	client           chatClient
	streamClient     StreamClient
	registry         *tools.Registry
	executor         toolExecutor
	permissionPolicy tools.ToolPermissionPolicy
	permission       tools.ToolPermissionDecider
	hookRunner       tools.HookRunner
	contextWindow    int
	log              io.Writer
	eventSink        EventSink
}

type Session struct {
	agent                *Agent
	messages             []model.Message
	turns                []TurnSummary
	roundsSinceTodoPatch int
}

func New(client chatClient, registry *tools.Registry, contextWindow int, log io.Writer) *Agent {
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	a := &Agent{
		client:        client,
		registry:      registry,
		contextWindow: contextWindow,
		log:           log,
	}
	a.executor = registryToolExecutor{agent: a}
	return a
}

func (a *Agent) Run(ctx context.Context, task string) (Result, error) {
	return a.NewSession().RunTask(ctx, task)
}

func (a *Agent) SetStreamClient(sc StreamClient) {
	a.streamClient = sc
}

func (a *Agent) SetEventSink(sink EventSink) {
	a.eventSink = sink
}

func (a *Agent) SetToolExecutor(exec toolExecutor) {
	if a == nil {
		return
	}
	if exec == nil {
		a.executor = registryToolExecutor{agent: a}
		return
	}
	a.executor = exec
}

func (a *Agent) SetToolPermissionDecider(decider tools.ToolPermissionDecider) {
	if a == nil {
		return
	}
	a.permissionPolicy = nil
	a.permission = decider
}

func (a *Agent) SetToolPermissionPolicy(policy tools.ToolPermissionPolicy) {
	if a == nil {
		return
	}
	a.permissionPolicy = policy
	a.permission = tools.NewPermissionDecider(policy)
}

func (a *Agent) SetToolHookRunner(runner tools.HookRunner) {
	if a == nil {
		return
	}
	a.hookRunner = runner
}

func (a *Agent) CanStream() bool {
	return a.streamClient != nil
}

func (a *Agent) NewSession() *Session {
	return a.NewSessionWithPrompt(PromptContext{})
}

func (a *Agent) NewSessionWithMemory(memoryText string) *Session {
	return a.NewSessionWithPrompt(PromptContext{
		MemoryText: memoryText,
		ToolNames:  a.ToolNames(),
	})
}

func (a *Agent) NewSessionWithPrompt(prompt PromptContext) *Session {
	if len(prompt.ToolNames) == 0 {
		prompt.ToolNames = a.ToolNames()
	}
	return &Session{
		agent: a,
		messages: []model.Message{
			{Role: "system", Content: BuildSystemPrompt(prompt)},
		},
	}
}

func (a *Agent) ToolNames() []string {
	if a == nil || a.registry == nil {
		return nil
	}
	defs := a.registry.Definitions()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Function.Name) != "" {
			names = append(names, def.Function.Name)
		}
	}
	sort.Strings(names)
	return names
}

func (s *Session) Messages() []model.Message {
	out := make([]model.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *Session) TurnSummaries() []TurnSummary {
	out := make([]TurnSummary, len(s.turns))
	for i, turn := range s.turns {
		out[i] = TurnSummary{
			Turn:      turn.Turn,
			Decision:  turn.Decision,
			Assistant: copyMessage(turn.Assistant),
			Reminder:  turn.Reminder,
			Final:     turn.Final,
		}
		out[i].ToolResults = copyMessages(turn.ToolResults)
	}
	return out
}

func (s *Session) ReplaceMessages(messages []model.Message) {
	s.messages = normalizeSystemMessages(messages)
}

func (s *Session) SetAgent(agent *Agent) {
	s.agent = agent
}

func (a *Agent) TodoItems() []tools.TodoItem {
	if a == nil || a.registry == nil {
		return nil
	}
	return a.registry.TodoItems()
}

func (a *Agent) ReplaceTodo(items []tools.TodoItem) error {
	if a == nil || a.registry == nil {
		return nil
	}
	return a.registry.ReplaceTodo(items)
}

func (a *Agent) AddTool(tool tools.Tool) {
	if a == nil || a.registry == nil {
		return
	}
	a.registry.AddTool(tool)
}

func (a *Agent) AddHook(hook tools.ToolHook) {
	if a == nil || a.registry == nil {
		return
	}
	a.registry.AddHook(hook)
}

func (s *Session) RunTask(ctx context.Context, task string) (Result, error) {
	return s.Runtime().RunTask(ctx, task)
}

func (s *Session) Compact() bool {
	if s == nil {
		return false
	}
	return s.compactForContext()
}

// RunTaskStreaming is like RunTask but streams assistant tokens via onToken.
func (s *Session) RunTaskStreaming(ctx context.Context, task string, onToken func(string)) (Result, error) {
	return s.Runtime().RunTaskStreaming(ctx, task, onToken)
}

func decideTurn(messages []model.Message, msg model.Message) turnDecision {
	if len(msg.ToolCalls) > 0 {
		return turnDecision{action: turnActionExecuteTools}
	}
	if reminder := evidenceRetryReminder(messages, msg); reminder != "" {
		return turnDecision{
			action:   turnActionRetry,
			reminder: reminder,
			logLine:  "tool evidence looked shallow, retrying with a more specific-tool reminder",
		}
	}
	if shouldRetryFailedCommand(messages, msg) {
		return turnDecision{
			action:   turnActionRetry,
			reminder: failedCommandRetryReminder,
			logLine:  "empty final after failed shell command, retrying with recovery reminder",
		}
	}
	if shouldRetryEmptyToolAnswer(messages, msg) {
		return turnDecision{
			action:   turnActionRetry,
			reminder: emptyToolAnswerRetryReminder,
			logLine:  "empty final after tool results, retrying with direct-answer reminder",
		}
	}
	if final := fallbackToolAnswer(messages, msg); final != "" {
		return turnDecision{
			action:  turnActionFinish,
			final:   final,
			logLine: "model stayed empty after tool reminders, using latest tool output as final answer",
		}
	}
	return turnDecision{
		action: turnActionFinish,
		final:  finalResponseText(msg),
	}
}

func (s *Session) maybeInjectTodoReminder() {
	if s == nil || s.roundsSinceTodoPatch < todoNagRoundThreshold {
		return
	}
	if !hasRecentToolActivity(s.messages, todoNagRoundThreshold) {
		return
	}
	if hasRecentTodoReminder(s.messages) {
		return
	}
	s.messages = append(s.messages, model.Message{
		Role: "user",
		Content: todoNagPrefix + "\n" +
			"You have been doing multi-step work for several rounds without updating the plan. " +
			"Call update_todo to reflect progress (completed/in_progress/pending) before continuing.",
	})
	s.roundsSinceTodoPatch = 0
}

func (s *Session) trackTodoRounds(calls []model.ToolCall) {
	if len(calls) == 0 {
		return
	}
	for _, call := range calls {
		if strings.EqualFold(strings.TrimSpace(call.Function.Name), "update_todo") {
			s.roundsSinceTodoPatch = 0
			return
		}
	}
	s.roundsSinceTodoPatch++
}

func hasRecentToolActivity(messages []model.Message, rounds int) bool {
	if rounds <= 0 {
		return false
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			seen++
			if seen >= rounds {
				return true
			}
		}
	}
	return false
}

func hasRecentTodoReminder(messages []model.Message) bool {
	for i := len(messages) - 1; i >= 0 && i >= len(messages)-4; i-- {
		msg := messages[i]
		if msg.Role != "system" && msg.Role != "user" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(model.ContentString(msg.Content)), todoNagPrefix) {
			return true
		}
	}
	return false
}

func shouldStopSession(messages []model.Message) (bool, string) {
	if hasRepeatedToolLoop(messages, repeatedToolWindow) {
		return true, "Stopped after detecting a repeated tool-call loop. Ask to continue with a narrower instruction or inspect the latest tool output."
	}
	if hasConsecutiveToolErrors(messages, maxConsecutiveToolErrs) {
		return true, "Stopped after repeated tool failures. Inspect the latest error and adjust the request or environment before continuing."
	}
	return false, ""
}

func hasConsecutiveToolErrors(messages []model.Message, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "tool" {
			// Only a trailing block of tool errors should stop the session.
			// Once any other message appears, the user can keep going.
			if count > 0 {
				break
			}
			return false
		}
		text := strings.ToLower(strings.TrimSpace(model.ContentString(msg.Content)))
		if strings.HasPrefix(text, "tool error:") {
			count++
			if count >= threshold {
				return true
			}
			continue
		}
		break
	}
	return false
}

func hasRepeatedToolLoop(messages []model.Message, window int) bool {
	if window < 2 {
		return false
	}
	var calls []string
	for _, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, call := range msg.ToolCalls {
			name := strings.TrimSpace(call.Function.Name)
			args := compactSummaryText(call.Function.Arguments, 200)
			if name == "" {
				continue
			}
			calls = append(calls, name+" "+args)
		}
	}
	if len(calls) < window {
		return false
	}
	tail := calls[len(calls)-window:]
	first := tail[0]
	if first == "" {
		return false
	}
	for _, item := range tail[1:] {
		if item != first {
			return false
		}
	}
	return true
}

func (s *Session) buildRequest() model.Request {
	req := model.Request{
		Messages: normalizeSystemMessages(s.messages),
		Tools:    s.agent.registry.Definitions(),
	}
	req.ToolChoice = "auto"
	return req
}

func normalizeSystemMessages(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(messages))
	seenNonSystem := false
	for _, msg := range messages {
		msg = copyMessage(msg)
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" {
			if !seenNonSystem && len(out) == 0 {
				msg.Role = "system"
				out = append(out, msg)
				continue
			}
			msg.Role = "user"
			msg.Content = normalizeSystemReminderContent(msg.Content)
			out = append(out, msg)
			seenNonSystem = true
			continue
		}
		if role != "" {
			msg.Role = role
		}
		out = append(out, msg)
		seenNonSystem = true
	}
	return out
}

func normalizeSystemReminderContent(content any) string {
	text := strings.TrimSpace(model.ContentString(content))
	if text == "" {
		return systemReminderTag
	}
	if strings.HasPrefix(text, systemReminderTag) {
		return text
	}
	return systemReminderTag + "\n" + text
}

func annotateToolResult(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return postToolAnswerReminder
	}
	return output + "\n\n" + postToolAnswerReminder
}

func latestToolOutput(messages []model.Message) string {
	if len(messages) == 0 || messages[len(messages)-1].Role != "tool" {
		return ""
	}
	return cleanToolOutput(messages[len(messages)-1].Content)
}

func latestToolLooksLikeCommandFailure(messages []model.Message) bool {
	if !hasAnyTool(latestTrailingToolNames(messages), "run_command") {
		return false
	}
	lower := strings.ToLower(latestToolOutput(messages))
	if lower == "" {
		return false
	}
	hints := []string{
		"not found",
		"command failed",
		"no such file",
		"is not recognized",
		"permission denied",
		"timed out",
		"cannot open",
	}
	for _, hint := range hints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func shouldRetryFailedCommand(messages []model.Message, msg model.Message) bool {
	if strings.TrimSpace(model.ContentString(msg.Content)) != "" {
		return false
	}
	if len(msg.ToolCalls) != 0 || len(messages) == 0 {
		return false
	}
	if messages[len(messages)-1].Role != "tool" {
		return false
	}
	if hasReminderSince(messages, failedCommandRetryReminder, latestTrailingToolBlockStart(messages)) {
		return false
	}
	return latestToolLooksLikeCommandFailure(messages)
}

func shouldRetryEmptyToolAnswer(messages []model.Message, msg model.Message) bool {
	if strings.TrimSpace(model.ContentString(msg.Content)) != "" {
		return false
	}
	if len(msg.ToolCalls) != 0 || len(messages) == 0 {
		return false
	}
	if messages[len(messages)-1].Role != "tool" {
		return false
	}
	if hasReminderSince(messages, emptyToolAnswerRetryReminder, latestTrailingToolBlockStart(messages)) {
		return false
	}
	return true
}

func fallbackToolAnswer(messages []model.Message, msg model.Message) string {
	if strings.TrimSpace(model.ContentString(msg.Content)) != "" || len(msg.ToolCalls) != 0 {
		return ""
	}
	start, _ := latestToolBlockRange(messages)
	if start < 0 || !hasReminderSince(messages, emptyToolAnswerRetryReminder, start) {
		return ""
	}
	outputs := latestTrailingToolOutputs(messages)
	if len(outputs) == 0 {
		return ""
	}
	if len(outputs) == 1 {
		return outputs[0]
	}
	return "Latest tool results:\n\n" + strings.Join(outputs, "\n\n")
}

func evidenceRetryReminder(messages []model.Message, msg model.Message) string {
	finalText := strings.TrimSpace(model.ContentString(msg.Content))
	userText := latestRealUserRequest(messages)
	if userText == "" {
		return ""
	}
	toolNames := latestTrailingToolNames(messages)
	if len(toolNames) == 0 {
		return ""
	}
	if wantsOfficialURL(userText) && !strings.Contains(finalText, "http://") && !strings.Contains(finalText, "https://") {
		if hasAnyTool(toolNames, "web_search", "list_files", "glob_search", "grep") && !hasAnyTool(toolNames, "fetch_url", "read_file") {
			if !hasRecentReminder(messages, urlEvidenceRetryReminder) {
				return urlEvidenceRetryReminder
			}
		}
	}
	if wantsDirectContent(userText) {
		if hasAnyTool(toolNames, "list_files", "glob_search", "grep", "web_search") && !hasAnyTool(toolNames, "read_file", "fetch_url") {
			if !hasRecentReminder(messages, fileEvidenceRetryReminder) {
				return fileEvidenceRetryReminder
			}
		}
	}
	return ""
}

func latestRealUserRequest(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		text := strings.TrimSpace(model.ContentString(msg.Content))
		if text == "" || strings.HasPrefix(text, "<system-reminder>") {
			continue
		}
		return text
	}
	return ""
}

func latestTrailingToolNames(messages []model.Message) []string {
	if len(messages) == 0 || messages[len(messages)-1].Role != "tool" {
		return nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			names := make([]string, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Function.Name)
				if name != "" {
					names = append(names, name)
				}
			}
			return names
		}
	}
	return nil
}

func wantsDirectContent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	keywords := []string{
		"contents", "content of", "show me the contents", "read ", "open ", "cat ", "file contents",
		"内容", "读取", "读一下", "查看内容", "打开文件",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func wantsOfficialURL(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	keywords := []string{
		"github", "official", "readme", "docs", "documentation", "url", "link",
		"仓库", "官网", "官方", "链接", "地址", "文档",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func hasAnyTool(names []string, wanted ...string) bool {
	for _, name := range names {
		for _, item := range wanted {
			if name == item {
				return true
			}
		}
	}
	return false
}

func hasRecentReminder(messages []model.Message, reminder string) bool {
	for i := len(messages) - 1; i >= 0 && i >= len(messages)-4; i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(model.ContentString(msg.Content)), reminder) {
			return true
		}
	}
	return false
}

func hasReminderSince(messages []model.Message, reminder string, start int) bool {
	if start < 0 {
		start = 0
	}
	for i := len(messages) - 1; i >= start && i < len(messages); i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(model.ContentString(msg.Content)), reminder) {
			return true
		}
	}
	return false
}

func latestTrailingToolBlockStart(messages []model.Message) int {
	start, end := latestToolBlockRange(messages)
	if end != len(messages)-1 {
		return -1
	}
	return start
}

func latestToolBlockRange(messages []model.Message) (int, int) {
	if len(messages) == 0 {
		return -1, -1
	}
	i := len(messages) - 1
	for i >= 0 {
		msg := messages[i]
		if msg.Role == "user" && strings.HasPrefix(strings.TrimSpace(model.ContentString(msg.Content)), systemReminderTag) {
			i--
			continue
		}
		break
	}
	if i < 0 || messages[i].Role != "tool" {
		return -1, -1
	}
	end := i
	for i >= 0 {
		msg := messages[i]
		if msg.Role == "tool" {
			i--
			continue
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			return i, end
		}
		break
	}
	return -1, -1
}

func latestTrailingToolOutputs(messages []model.Message) []string {
	start, end := latestToolBlockRange(messages)
	if start < 0 {
		return nil
	}
	var outputs []string
	for i := start + 1; i <= end; i++ {
		if messages[i].Role != "tool" {
			continue
		}
		if text := cleanToolOutput(messages[i].Content); text != "" {
			outputs = append(outputs, text)
		}
	}
	return outputs
}

func cleanToolOutput(content any) string {
	output := model.ContentString(content)
	if marker := strings.Index(output, "\n\n<system-reminder>"); marker >= 0 {
		output = output[:marker]
	}
	return strings.TrimSpace(output)
}

func finalResponseText(msg model.Message) string {
	final := strings.TrimSpace(model.ContentString(msg.Content))
	if final != "" {
		return final
	}
	return "(empty response)"
}

func copyMessage(msg model.Message) model.Message {
	out := msg
	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = make([]model.ToolCall, len(msg.ToolCalls))
		copy(out.ToolCalls, msg.ToolCalls)
	}
	return out
}

func copyMessages(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, len(messages))
	for i, msg := range messages {
		out[i] = copyMessage(msg)
	}
	return out
}

func (s *Session) compactForContext() bool {
	limit := contextSoftLimitChars(s.agent.contextWindow)
	if conversationSize(s.messages) <= limit {
		return false
	}
	trimmed := compactConversation(s.messages, contextTargetChars(s.agent.contextWindow))
	if conversationSize(trimmed) >= conversationSize(s.messages) {
		return false
	}
	s.messages = trimmed
	s.agent.logf("compacted conversation to stay within context budget\n")
	return true
}

func (s *Session) compactForRetry() bool {
	trimmed := compactConversation(s.messages, contextRetryChars(s.agent.contextWindow))
	if conversationSize(trimmed) >= conversationSize(s.messages) {
		return false
	}
	s.messages = trimmed
	return true
}

func contextSoftLimitChars(window int) int {
	window = normalizeContextWindow(window)
	return max(1024, window*85/100)
}

func contextTargetChars(window int) int {
	window = normalizeContextWindow(window)
	return max(768, window*65/100)
}

func contextRetryChars(window int) int {
	window = normalizeContextWindow(window)
	return max(512, window/2)
}

func normalizeContextWindow(window int) int {
	if window <= 0 {
		return 32768
	}
	return window
}

func truncateToolMessage(text string, limit int) string {
	text = strings.TrimSpace(text)
	if marker := strings.Index(text, "\n\n<system-reminder>"); marker >= 0 {
		text = strings.TrimSpace(text[:marker])
	}
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
	layer1 := preprocessConversation(stripSyntheticSummaries(messages))
	if conversationSize(layer1) <= limit {
		return layer1
	}

	layer2 := summarizeConversationHistory(layer1, limit)
	if len(layer2) > 0 && conversationSize(layer2) <= limit {
		return layer2
	}

	return compactConversationAggressive(layer1, limit)
}

func compactConversationAggressive(messages []model.Message, limit int) []model.Message {
	if len(messages) == 0 {
		return messages
	}
	aggressive := make([]model.Message, len(messages))
	copy(aggressive, messages)
	if aggressive[0].Role == "system" {
		aggressive[0].Content = compactSummaryText(model.ContentString(aggressive[0].Content), max(400, min(1200, limit/3)))
	}
	for i := 1; i < len(aggressive)-1; i++ {
		switch aggressive[i].Role {
		case "assistant":
			if len(aggressive[i].ToolCalls) > 0 {
				aggressive[i].Content = ""
				for j := range aggressive[i].ToolCalls {
					aggressive[i].ToolCalls[j].Function.Arguments = compactSummaryText(aggressive[i].ToolCalls[j].Function.Arguments, 100)
				}
			} else {
				aggressive[i].Content = compactSummaryText(model.ContentString(aggressive[i].Content), 320)
			}
		case "tool":
			aggressive[i].Content = truncateToolMessage(model.ContentString(aggressive[i].Content), 220)
		case "user":
			aggressive[i].Content = compactSummaryText(model.ContentString(aggressive[i].Content), 240)
		}
	}
	pruned := pruneConversation(aggressive, limit)
	if conversationSize(pruned) <= limit {
		return pruned
	}

	return preserveLatestTurnCompacted(pruned, limit)
}

func preserveLatestTurnCompacted(messages []model.Message, limit int) []model.Message {
	if len(messages) == 0 {
		return messages
	}
	if len(messages) == 1 {
		messages[0].Content = compactSummaryText(model.ContentString(messages[0].Content), max(80, limit/2))
		return messages
	}

	start := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			start = i
			break
		}
	}

	out := make([]model.Message, 0, len(messages))
	if messages[0].Role == "system" && start > 0 {
		out = append(out, messages[0])
	}
	out = append(out, messages[start:]...)
	if conversationSize(out) <= limit {
		return out
	}

	budget := max(80, limit/max(2, len(out)))
	for i := range out {
		if i == 0 && out[i].Role == "system" {
			out[i].Content = compactSummaryText(model.ContentString(out[i].Content), max(120, budget))
			continue
		}
		switch out[i].Role {
		case "tool":
			out[i].Content = truncateToolMessage(model.ContentString(out[i].Content), max(120, budget))
		default:
			out[i].Content = compactSummaryText(model.ContentString(out[i].Content), budget)
		}
	}
	return out
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
		if len(messages) == 1 && messages[0].Role == "system" {
			cleaned := messages[0]
			cleaned.Content = stripSyntheticSummaryBlock(model.ContentString(cleaned.Content))
			return []model.Message{cleaned}
		}
		return messages
	}
	out := make([]model.Message, 0, len(messages))
	first := messages[0]
	if first.Role == "system" {
		first.Content = stripSyntheticSummaryBlock(model.ContentString(first.Content))
	}
	out = append(out, first)
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

	// Group messages so tool messages stay with their preceding assistant message.
	type group struct {
		msgs []model.Message
		size int
	}
	var groups []group
	groups = append(groups, group{msgs: []model.Message{messages[0]}, size: messageSize(messages[0])})

	current := group{}
	for _, msg := range messages[1:] {
		if msg.Role == "tool" {
			current.msgs = append(current.msgs, msg)
			current.size += messageSize(msg)
		} else {
			if len(current.msgs) > 0 {
				groups = append(groups, current)
			}
			current = group{msgs: []model.Message{msg}, size: messageSize(msg)}
		}
	}
	if len(current.msgs) > 0 {
		groups = append(groups, current)
	}

	latestUserGroup := -1
	for i := len(groups) - 1; i >= 1; i-- {
		if len(groups[i].msgs) > 0 && groups[i].msgs[0].Role == "user" {
			latestUserGroup = i
			break
		}
	}
	if latestUserGroup == -1 {
		latestUserGroup = len(groups) - 1
	}

	kept := make([]model.Message, 0, len(messages))
	total := 0
	if len(groups[0].msgs) > 0 && groups[0].msgs[0].Role == "system" {
		kept = append(kept, groups[0].msgs...)
		total += groups[0].size
	}

	required := groups[latestUserGroup:]
	requiredSize := 0
	for _, grp := range required {
		requiredSize += grp.size
	}
	if total+requiredSize > limit && total > 0 {
		kept = kept[:0]
		total = 0
	}
	for _, grp := range required {
		kept = append(kept, grp.msgs...)
		total += grp.size
	}

	var middle []group
	for i := latestUserGroup - 1; i >= 1; i-- {
		if total+groups[i].size > limit {
			continue
		}
		middle = append(middle, groups[i])
		total += groups[i].size
	}
	for i := len(middle) - 1; i >= 0; i-- {
		insertAt := 0
		if len(kept) > 0 && kept[0].Role == "system" {
			insertAt = 1
		}
		kept = append(kept[:insertAt], append(middle[i].msgs, kept[insertAt:]...)...)
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

	maxKeep := min(recentTurnsToKeep, len(turns))
	for keep := maxKeep; keep >= 1; keep-- {
		older := turns[:len(turns)-keep]
		recent := turns[len(turns)-keep:]

		candidate := []model.Message{messages[0]}
		if len(older) > 0 {
			summary := summarizeTurns(older, min(maxSummaryChars, max(400, limit/3)))
			if strings.TrimSpace(summary) != "" {
				candidate[0].Content = appendSyntheticSummary(model.ContentString(candidate[0].Content), summary)
			}
		}
		for _, turn := range recent {
			candidate = append(candidate, turn...)
		}
		if conversationSize(candidate) <= limit {
			return candidate
		}
	}

	older := turns[:len(turns)-1]
	recent := turns[len(turns)-1:]
	summary := summarizeTurns(older, min(maxSummaryChars, max(300, limit/2)))
	if strings.TrimSpace(summary) == "" {
		return pruneConversation(messages, limit)
	}
	candidate := []model.Message{messages[0]}
	candidate[0].Content = appendSyntheticSummary(model.ContentString(candidate[0].Content), summary)
	for _, turn := range recent {
		candidate = append(candidate, turn...)
	}
	return pruneConversation(candidate, limit)
}

func appendSyntheticSummary(base, summary string) string {
	base = stripSyntheticSummaryBlock(model.ContentString(base))
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return strings.TrimSpace(base)
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return syntheticSummaryPrefix + "\n" + summary
	}
	return base + "\n\n" + syntheticSummaryPrefix + "\n" + summary
}

func stripSyntheticSummaryBlock(text string) string {
	text = model.ContentString(text)
	if idx := strings.Index(text, "\n\n"+syntheticSummaryPrefix); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	if strings.HasPrefix(strings.TrimSpace(text), syntheticSummaryPrefix) {
		return ""
	}
	return text
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
	case "tool":
		facts := extractStructuredToolFacts(model.ContentString(msg.Content), 2)
		if len(facts) > 0 {
			return "- tool facts: " + strings.Join(facts, "; ")
		}
		text := truncateToolMessage(model.ContentString(msg.Content), 180)
		if text == "" {
			return ""
		}
		text = strings.ReplaceAll(text, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		return "- tool: " + text
	default:
		return ""
	}
}

func extractStructuredToolFacts(text string, maxFacts int) []string {
	if maxFacts <= 0 {
		return nil
	}
	if marker := strings.Index(text, "\n\n<system-reminder>"); marker >= 0 {
		text = text[:marker]
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, maxFacts)
	seen := make(map[string]bool, maxFacts)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		if !looksLikeStructuredFact(line) {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) >= maxFacts {
			break
		}
	}
	return out
}

func looksLikeStructuredFact(line string) bool {
	var key, value string
	var ok bool
	if strings.Contains(line, "://") {
		return true
	}
	if key, value, ok = strings.Cut(line, "="); !ok {
		key, value, ok = strings.Cut(line, ":")
	}
	if !ok {
		return false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" || len(key) > 48 || len(value) > 160 {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
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
	size := 4 + approxTokenCount(model.ContentString(msg.Content)) + approxTokenCount(msg.Role) + approxTokenCount(msg.ToolCallID)
	for _, call := range msg.ToolCalls {
		size += 2 + approxTokenCount(call.ID) + approxTokenCount(call.Type) + approxTokenCount(call.Function.Name) + approxTokenCount(call.Function.Arguments)
	}
	return size
}

func approxTokenCount(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}

	asciiBytes := 0
	tokens := 0
	for _, r := range text {
		switch {
		case r <= unicode.MaxASCII:
			if !unicode.IsSpace(r) {
				asciiBytes++
			}
		case isCJKRune(r):
			tokens += 2
		default:
			tokens++
		}
	}
	if asciiBytes > 0 {
		tokens += (asciiBytes + 3) / 4
	}
	if tokens == 0 {
		return 1
	}
	return tokens
}

func isCJKRune(r rune) bool {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
		return true
	}
	if (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF) {
		return true
	}
	return utf8.RuneLen(r) >= 3
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

func (a *Agent) logModelRequest(turn int, req model.Request) {
	a.logf(
		"requesting model turn=%d messages=%d tools=%d approx_tokens=%d\n",
		turn,
		len(req.Messages),
		len(req.Tools),
		conversationSize(req.Messages),
	)
}

func (a *Agent) logModelResponse(turn int, msg model.Message) {
	contentChars := len(strings.TrimSpace(model.ContentString(msg.Content)))
	if len(msg.ToolCalls) == 0 {
		a.logf("model response turn=%d final chars=%d\n", turn, contentChars)
		return
	}
	names := make([]string, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Function.Name)
		if name != "" {
			names = append(names, name)
		}
		if len(names) >= 4 {
			break
		}
	}
	suffix := ""
	if len(msg.ToolCalls) > len(names) {
		suffix = fmt.Sprintf(" +%d more", len(msg.ToolCalls)-len(names))
	}
	a.logf(
		"model response turn=%d tool_calls=%d names=%s%s chars=%d\n",
		turn,
		len(msg.ToolCalls),
		strings.Join(names, ","),
		suffix,
		contentChars,
	)
}

func (a *Agent) logToolStart(index, total int, name, callID string, raw json.RawMessage) {
	callID = strings.TrimSpace(callID)
	idPart := ""
	if callID != "" {
		idPart = " id=" + callID
	}
	preview := strings.TrimSpace(a.registry.Preview(name, raw))
	if preview == "" {
		a.logf("  [%d/%d] %s%s\n", index, total, name, idPart)
		return
	}
	a.logf("  [%d/%d] %s%s %s\n", index, total, name, idPart, preview)
}

func (a *Agent) logToolFinish(elapsed time.Duration, output string, err error) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	a.logf("        %s in %s", status, roundDuration(elapsed))

	lines := summarizeOutput(output)
	if len(lines) > 0 {
		a.logf(" | %s\n", lines[0])
		for _, line := range lines[1:] {
			a.logf("        | %s\n", line)
		}
		return
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

func summarizeOutput(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	rawLines := strings.Split(text, "\n")
	limit := min(3, len(rawLines))
	out := make([]string, 0, limit+1)
	for _, line := range rawLines[:limit] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 160 {
			line = line[:160] + "..."
		}
		out = append(out, line)
	}
	if len(rawLines) > limit {
		out = append(out, fmt.Sprintf("... %d more lines", len(rawLines)-limit))
	}
	return out
}

func streamToolCallKey(choiceIndex int, tc model.ToolCall, fallback int) string {
	if tc.Index != nil {
		return fmt.Sprintf("%d:%d", choiceIndex, *tc.Index)
	}
	if strings.TrimSpace(tc.ID) != "" {
		return fmt.Sprintf("%d:id:%s", choiceIndex, tc.ID)
	}
	return fmt.Sprintf("%d:fallback:%d", choiceIndex, fallback)
}

func (a *Agent) emitEvent(ctx context.Context, typ string, data map[string]any) {
	if a == nil || a.eventSink == nil || strings.TrimSpace(typ) == "" {
		return
	}
	a.eventSink.RecordAgentEvent(ctx, AgentEvent{
		Time: time.Now(),
		Type: typ,
		Data: data,
	})
}

func (a *Agent) emitTurnSummaryEvent(ctx context.Context, summary TurnSummary) {
	if a == nil {
		return
	}
	a.emitEvent(ctx, "turn_summary", map[string]any{
		"turn":         summary.Turn,
		"decision":     summary.Decision,
		"assistant":    copyMessage(summary.Assistant),
		"tool_results": copyMessages(summary.ToolResults),
		"reminder":     summary.Reminder,
		"final":        summary.Final,
	})
}

func modelResponseEventType(msg model.Message) string {
	if len(msg.ToolCalls) == 0 {
		return "model_response_final"
	}
	return "model_response_tool_calls"
}

func toolCallNames(calls []model.ToolCall, limit int) []string {
	if len(calls) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = len(calls)
	}
	names := make([]string, 0, min(limit, len(calls)))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
		if len(names) >= limit {
			break
		}
	}
	return names
}

func toolEventStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func toolEventError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstOutputSummary(output string) string {
	lines := summarizeOutput(output)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
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
