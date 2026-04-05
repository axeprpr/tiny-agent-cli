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

type chatClient interface {
	Complete(ctx context.Context, req model.Request) (model.Response, error)
}

type Result struct {
	Final string
	Steps int
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
	client        chatClient
	streamClient  StreamClient
	registry      *tools.Registry
	contextWindow int
	log           io.Writer
	eventSink     EventSink
}

type Session struct {
	agent                *Agent
	messages             []model.Message
	roundsSinceTodoPatch int
}

func New(client chatClient, registry *tools.Registry, contextWindow int, log io.Writer) *Agent {
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	return &Agent{
		client:        client,
		registry:      registry,
		contextWindow: contextWindow,
		log:           log,
	}
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

func (s *Session) ReplaceMessages(messages []model.Message) {
	s.messages = make([]model.Message, len(messages))
	copy(s.messages, messages)
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
	s.messages = append(s.messages, model.Message{
		Role:    "user",
		Content: task,
	})

	turn := 0
	for {
		turn++
		s.maybeInjectTodoReminder()
		s.compactForContext()
		if stop, reason := shouldStopSession(s.messages); stop {
			s.agent.logf("stopping early: %s\n", reason)
			return Result{Final: reason}, nil
		}

		req := s.buildRequest()
		s.agent.logModelRequest(turn, req)
		s.agent.emitEvent(ctx, "model_request", map[string]any{
			"turn":          turn,
			"messages":      len(req.Messages),
			"tools":         len(req.Tools),
			"approx_tokens": conversationSize(req.Messages),
		})
		const maxContextRetries = 2
		var (
			resp model.Response
			err  error
		)
		for retry := 0; retry <= maxContextRetries; retry++ {
			resp, err = s.agent.client.Complete(ctx, req)
			if err == nil {
				break
			}
			if !isContextLengthError(err) || retry == maxContextRetries {
				break
			}
			if !s.compactForRetry() {
				break
			}
			s.agent.logf("context too long, retrying with shorter history\n")
			req = s.buildRequest()
			s.agent.logModelRequest(turn, req)
		}
		if err != nil {
			return Result{}, err
		}
		if len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("model returned no choices")
		}

		msg := resp.Choices[0].Message
		s.agent.logModelResponse(turn, msg)
		s.agent.emitEvent(ctx, modelResponseEventType(msg), map[string]any{
			"turn":       turn,
			"chars":      len(strings.TrimSpace(model.ContentString(msg.Content))),
			"tool_calls": len(msg.ToolCalls),
			"tool_names": toolCallNames(msg.ToolCalls, 8),
		})
		if len(msg.ToolCalls) == 0 {
			final := finalResponseText(msg)
			s.messages = append(s.messages, model.Message{
				Role:    "assistant",
				Content: msg.Content,
			})
			return Result{Final: final}, nil
		}

		s.messages = append(s.messages, model.Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})

		s.agent.logf("executing %d tool(s)\n", len(msg.ToolCalls))

		for i, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			s.agent.logToolStart(i+1, len(msg.ToolCalls), call.Function.Name, call.ID, args)
			s.agent.emitEvent(ctx, "tool_start", map[string]any{
				"turn":    turn,
				"index":   i + 1,
				"total":   len(msg.ToolCalls),
				"name":    call.Function.Name,
				"call_id": call.ID,
				"preview": strings.TrimSpace(s.agent.registry.Preview(call.Function.Name, args)),
			})

			started := time.Now()
			output, err := s.agent.registry.Call(ctx, call.Function.Name, args)
			output = truncateToolMessage(output, maxToolMessageChars)
			s.agent.logToolFinish(time.Since(started), output, err)
			s.agent.emitEvent(ctx, "tool_finish", map[string]any{
				"turn":        turn,
				"index":       i + 1,
				"total":       len(msg.ToolCalls),
				"name":        call.Function.Name,
				"call_id":     call.ID,
				"duration_ms": time.Since(started).Milliseconds(),
				"status":      toolEventStatus(err),
				"summary":     firstOutputSummary(output),
				"error":       toolEventError(err),
			})
			if err != nil {
				output = "tool error: " + err.Error()
			}

			s.messages = append(s.messages, model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    output,
			})
		}
		s.trackTodoRounds(msg.ToolCalls)
	}
}

func (s *Session) Compact() bool {
	if s == nil {
		return false
	}
	return s.compactForContext()
}

// RunTaskStreaming is like RunTask but streams assistant tokens via onToken.
func (s *Session) RunTaskStreaming(ctx context.Context, task string, onToken func(string)) (Result, error) {
	if s.agent.streamClient == nil {
		return s.RunTask(ctx, task)
	}

	s.messages = append(s.messages, model.Message{
		Role:    "user",
		Content: task,
	})

	turn := 0
	var streamFilter model.ThinkingTagFilter
	emitToken := onToken
	if onToken != nil {
		emitToken = func(token string) {
			visible := streamFilter.Strip(token)
			if visible != "" {
				onToken(visible)
			}
		}
	}

	for {
		turn++
		s.maybeInjectTodoReminder()
		s.compactForContext()
		if stop, reason := shouldStopSession(s.messages); stop {
			s.agent.logf("stopping early: %s\n", reason)
			return Result{Final: reason}, nil
		}

		req := s.buildRequest()
		s.agent.logModelRequest(turn, req)
		s.agent.emitEvent(ctx, "model_request", map[string]any{
			"turn":          turn,
			"messages":      len(req.Messages),
			"tools":         len(req.Tools),
			"approx_tokens": conversationSize(req.Messages),
		})

		chunks, errc := s.agent.streamClient.CompleteStream(ctx, req)

		var contentBuf strings.Builder
		var role string
		toolCallMap := map[string]*model.ToolCall{}
		var toolCallOrder []string

		for chunk := range chunks {
			for _, choice := range chunk.Choices {
				d := choice.Delta
				if d.Role != "" {
					role = d.Role
				}
				if d.Content != "" {
					contentBuf.WriteString(d.Content)
					if emitToken != nil {
						emitToken(d.Content)
					}
				}
				for _, tc := range d.ToolCalls {
					key := streamToolCallKey(choice.Index, tc, len(toolCallOrder))
					existing, ok := toolCallMap[key]
					if !ok {
						copy := tc
						existing = &copy
						toolCallMap[key] = existing
						toolCallOrder = append(toolCallOrder, key)
					} else {
						if tc.ID != "" {
							existing.ID = tc.ID
						}
						if tc.Function.Name != "" {
							if existing.Function.Name == "" {
								existing.Function.Name = tc.Function.Name
							} else {
								existing.Function.Name += tc.Function.Name
							}
						}
						if tc.Function.Arguments != "" {
							existing.Function.Arguments += tc.Function.Arguments
						}
					}
				}
			}
		}
		if emitToken != nil {
			if tail := streamFilter.Flush(); tail != "" {
				onToken(tail)
			}
		}
		if err := <-errc; err != nil {
			if isContextLengthError(err) {
				if s.compactForRetry() {
					s.agent.logf("context too long, retrying\n")
					continue
				}
			}
			return Result{}, err
		}

		if role == "" {
			role = "assistant"
		}

		var toolCalls []model.ToolCall
		for _, key := range toolCallOrder {
			tc := *toolCallMap[key]
			tc.Index = nil
			toolCalls = append(toolCalls, tc)
		}

		msg := model.Message{
			Role:      role,
			Content:   contentBuf.String(),
			ToolCalls: toolCalls,
		}
		s.agent.logModelResponse(turn, msg)
		s.agent.emitEvent(ctx, modelResponseEventType(msg), map[string]any{
			"turn":       turn,
			"chars":      len(strings.TrimSpace(model.ContentString(msg.Content))),
			"tool_calls": len(msg.ToolCalls),
			"tool_names": toolCallNames(msg.ToolCalls, 8),
		})

		if len(toolCalls) == 0 {
			final := finalResponseText(msg)
			s.messages = append(s.messages, model.Message{
				Role:    "assistant",
				Content: msg.Content,
			})
			return Result{Final: final}, nil
		}

		s.messages = append(s.messages, msg)
		s.agent.logf("executing %d tool(s)\n", len(toolCalls))

		for i, call := range toolCalls {
			args := json.RawMessage(call.Function.Arguments)
			s.agent.logToolStart(i+1, len(toolCalls), call.Function.Name, call.ID, args)
			s.agent.emitEvent(ctx, "tool_start", map[string]any{
				"turn":    turn,
				"index":   i + 1,
				"total":   len(toolCalls),
				"name":    call.Function.Name,
				"call_id": call.ID,
				"preview": strings.TrimSpace(s.agent.registry.Preview(call.Function.Name, args)),
			})
			started := time.Now()
			output, err := s.agent.registry.Call(ctx, call.Function.Name, args)
			output = truncateToolMessage(output, maxToolMessageChars)
			s.agent.logToolFinish(time.Since(started), output, err)
			s.agent.emitEvent(ctx, "tool_finish", map[string]any{
				"turn":        turn,
				"index":       i + 1,
				"total":       len(toolCalls),
				"name":        call.Function.Name,
				"call_id":     call.ID,
				"duration_ms": time.Since(started).Milliseconds(),
				"status":      toolEventStatus(err),
				"summary":     firstOutputSummary(output),
				"error":       toolEventError(err),
			})
			if err != nil {
				output = "tool error: " + err.Error()
			}
			s.messages = append(s.messages, model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    output,
			})
		}
		s.trackTodoRounds(toolCalls)
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
		Role: "system",
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
		if msg.Role != "system" {
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
		Messages: s.messages,
		Tools:    s.agent.registry.Definitions(),
	}
	req.ToolChoice = "auto"
	return req
}

func finalResponseText(msg model.Message) string {
	final := strings.TrimSpace(model.ContentString(msg.Content))
	if final != "" {
		return final
	}
	return "(empty response)"
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
			aggressive[i].Content = compactSummaryText(model.ContentString(aggressive[i].Content), 220)
		case "user":
			aggressive[i].Content = compactSummaryText(model.ContentString(aggressive[i].Content), 240)
		}
	}
	pruned := pruneConversation(aggressive, limit)
	if conversationSize(pruned) <= limit {
		return pruned
	}

	if len(pruned) == 0 {
		return pruned
	}
	if len(pruned) == 1 {
		pruned[0].Content = compactSummaryText(model.ContentString(pruned[0].Content), max(80, limit/2))
		return pruned
	}

	out := []model.Message{pruned[0], pruned[len(pruned)-1]}
	if conversationSize(out) <= limit {
		return out
	}
	budget := max(80, limit/3)
	out[0].Content = compactSummaryText(model.ContentString(out[0].Content), budget)
	out[1].Content = compactSummaryText(model.ContentString(out[1].Content), budget)
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

	kept := make([]model.Message, 0, len(messages))
	kept = append(kept, groups[0].msgs...)
	total := groups[0].size

	var tail []group
	for i := len(groups) - 1; i >= 1; i-- {
		if total+groups[i].size > limit {
			continue
		}
		tail = append(tail, groups[i])
		total += groups[i].size
	}
	for i := len(tail) - 1; i >= 0; i-- {
		kept = append(kept, tail[i].msgs...)
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

	summary := summarizeTurns(turns, min(maxSummaryChars, max(300, limit/2)))
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
