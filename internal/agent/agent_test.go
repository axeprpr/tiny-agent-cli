package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

func TestFormatTerminalOutputRemovesThinkTags(t *testing.T) {
	input := "<think>\nprivate reasoning\n</think>\n\nHello.\n"
	got := FormatTerminalOutput(input)
	if got != "Hello." {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFormatTerminalOutputRemovesDanglingTag(t *testing.T) {
	input := "Answer first\n</think>\nsecond line"
	got := FormatTerminalOutput(input)
	if got != "Answer first\nsecond line" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFormatTerminalOutputPrefersOutputMarker(t *testing.T) {
	input := "Thinking Process:\n1. do stuff\nOutput: pong"
	got := FormatTerminalOutput(input)
	if got != "pong" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFormatTerminalOutputNormalizesMarkdownTable(t *testing.T) {
	input := "环境信息统计\n| 项目 | 信息 |\n|------|------|\n| 操作系统 | Ubuntu |\n| 架构 | x86_64 |"
	got := FormatTerminalOutput(input)
	want := "环境信息统计\n- 操作系统: Ubuntu\n- 架构: x86_64"
	if got != want {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestCompactConversationPrefersKeepingRecentContext(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "old user"},
		{Role: "assistant", Content: "old assistant", ToolCalls: []model.ToolCall{{ID: "1", Type: "function", Function: model.ToolFunction{Name: "run_command", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: "1", Content: strings.Repeat("A", 5000)},
		{Role: "user", Content: "recent user"},
		{Role: "assistant", Content: "recent answer"},
	}

	got := compactConversation(messages, 1600)
	if len(got) < 3 {
		t.Fatalf("expected recent context to remain, got %#v", got)
	}
	if model.ContentString(got[len(got)-2].Content) != "recent user" {
		t.Fatalf("expected recent user message to remain, got %#v", got)
	}
	if model.ContentString(got[len(got)-1].Content) != "recent answer" {
		t.Fatalf("expected recent assistant message to remain, got %#v", got)
	}
}

func TestCompactConversationAddsSyntheticSummaryForOlderTurns(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: strings.Repeat("old user ", 60)},
		{Role: "assistant", Content: strings.Repeat("old assistant ", 60)},
		{Role: "user", Content: "recent user"},
		{Role: "assistant", Content: "recent answer"},
	}

	got := compactConversation(messages, 220)
	if len(got) < 4 {
		t.Fatalf("expected summarized history plus recent messages, got %#v", got)
	}
	if got[1].Role != "system" || !strings.Contains(model.ContentString(got[1].Content), syntheticSummaryPrefix) {
		t.Fatalf("expected synthetic summary message, got %#v", got)
	}
	if model.ContentString(got[len(got)-2].Content) != "recent user" {
		t.Fatalf("expected recent user to remain, got %#v", got)
	}
	if model.ContentString(got[len(got)-1].Content) != "recent answer" {
		t.Fatalf("expected recent assistant to remain, got %#v", got)
	}
}

func TestCompactConversationAggressiveLayerStillKeepsLatestTurn(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: strings.Repeat("system ", 1200)},
		{Role: "user", Content: strings.Repeat("old user ", 800)},
		{Role: "assistant", Content: strings.Repeat("old assistant ", 800), ToolCalls: []model.ToolCall{{
			ID:   "1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: strings.Repeat("{\"path\":\"README.md\"}", 200),
			},
		}}},
		{Role: "tool", ToolCallID: "1", Content: strings.Repeat("huge output ", 2000)},
		{Role: "user", Content: "latest user request"},
		{Role: "assistant", Content: "latest answer"},
	}

	got := compactConversation(messages, 700)
	if conversationSize(got) > 700 {
		t.Fatalf("expected compacted size <= 700, got %d", conversationSize(got))
	}
	if len(got) == 0 {
		t.Fatalf("expected compacted conversation")
	}
	if model.ContentString(got[len(got)-1].Content) != "latest answer" {
		t.Fatalf("expected latest assistant answer to remain, got %#v", got)
	}
}

func TestTruncateToolMessageShrinksLargeOutput(t *testing.T) {
	got := truncateToolMessage(strings.Repeat("B", 5000), 900)
	if len(got) >= 5000 {
		t.Fatalf("expected truncated output, got length %d", len(got))
	}
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestSummarizeOutputKeepsSeveralLines(t *testing.T) {
	got := summarizeOutput("line one\nline two\nline three\nline four")
	want := []string{"line one", "line two", "line three", "... 1 more lines"}
	if len(got) != len(want) {
		t.Fatalf("unexpected summary length: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected summary at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

type stubChatClient struct {
	resp model.Response
	err  error
}

func (s stubChatClient) Complete(context.Context, model.Request) (model.Response, error) {
	return s.resp, s.err
}

func TestRunTaskStopsRepeatedToolLoopWithoutStepBudget(t *testing.T) {
	agent := New(stubChatClient{
		resp: model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "list_files",
									Arguments: `{"path":"."}`,
								},
							},
						},
					},
				},
			},
		},
	}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)

	result, err := agent.NewSession().RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Final, "repeated tool-call loop") {
		t.Fatalf("expected loop stop, got %q", result.Final)
	}
}

func TestCompactForContextUsesConfiguredWindow(t *testing.T) {
	session := New(stubChatClient{}, tools.NewRegistry(".", "bash", time.Second, nil), 64, nil).NewSession()
	session.messages = append(session.messages,
		model.Message{Role: "user", Content: strings.Repeat("older context ", 900)},
		model.Message{Role: "assistant", Content: strings.Repeat("assistant reply ", 900)},
		model.Message{Role: "user", Content: "recent user"},
		model.Message{Role: "assistant", Content: "recent answer"},
	)

	before := conversationSize(session.messages)
	if !session.compactForContext() {
		t.Fatalf("expected context compaction to trigger")
	}
	after := conversationSize(session.messages)
	if after >= before {
		t.Fatalf("expected compacted conversation to shrink: before=%d after=%d", before, after)
	}
	if model.ContentString(session.messages[len(session.messages)-2].Content) != "recent user" {
		t.Fatalf("expected recent user message to remain, got %#v", session.messages)
	}
}

type scriptedChatClient struct {
	responses []model.Response
	requests  []model.Request
}

func (s *scriptedChatClient) Complete(_ context.Context, req model.Request) (model.Response, error) {
	s.requests = append(s.requests, req)
	index := len(s.requests) - 1
	if index >= len(s.responses) {
		return model.Response{}, nil
	}
	return s.responses[index], nil
}

type scriptedStreamClient struct {
	sequences [][]model.StreamChunk
	requests  int
	err       error
}

func (s *scriptedStreamClient) CompleteStream(_ context.Context, _ model.Request) (<-chan model.StreamChunk, <-chan error) {
	var chunks []model.StreamChunk
	if s.requests < len(s.sequences) {
		chunks = s.sequences[s.requests]
	}
	s.requests++

	ch := make(chan model.StreamChunk, len(chunks))
	errc := make(chan error, 1)
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	errc <- s.err
	close(errc)
	return ch, errc
}

func TestRunTaskStreamingSeparatesMultipleToolCalls(t *testing.T) {
	addIndex := 0
	mulIndex := 1
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "done"},
					},
				},
			},
		},
	}
	registry := tools.NewRegistry(".", "bash", time.Second, nil)
	agent := New(client, registry, 32768, nil)
	streamClient := &scriptedStreamClient{
		sequences: [][]model.StreamChunk{
			{
				{
					Choices: []model.StreamChoice{
						{
							Index: 0,
							Delta: model.StreamDelta{
								Role: "assistant",
								ToolCalls: []model.ToolCall{
									{
										Index: &addIndex,
										ID:    "add:0",
										Type:  "function",
										Function: model.ToolFunction{
											Name:      "run_command",
											Arguments: "",
										},
									},
									{
										Index: &mulIndex,
										ID:    "mul:1",
										Type:  "function",
										Function: model.ToolFunction{
											Name:      "run_command",
											Arguments: "",
										},
									},
								},
							},
						},
					},
				},
				{
					Choices: []model.StreamChoice{
						{
							Index: 0,
							Delta: model.StreamDelta{
								ToolCalls: []model.ToolCall{
									{
										Index:    &addIndex,
										Function: model.ToolFunction{Arguments: `{"command":"printf add"}`},
									},
									{
										Index:    &mulIndex,
										Function: model.ToolFunction{Arguments: `{"command":"printf mul"}`},
									},
								},
							},
						},
					},
				},
			},
			{
				{
					Choices: []model.StreamChoice{
						{
							Index: 0,
							Delta: model.StreamDelta{
								Role:    "assistant",
								Content: "done",
							},
						},
					},
				},
			},
		},
	}
	agent.SetStreamClient(streamClient)

	session := agent.NewSession()
	result, err := session.RunTaskStreaming(context.Background(), "run tools", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	msgs := session.Messages()
	if len(msgs) < 5 {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	var assistant model.Message
	found := false
	for _, msg := range msgs {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			assistant = msg
			found = true
		}
	}
	if !found {
		t.Fatalf("expected assistant tool-call message, got %#v", msgs)
	}
	if len(assistant.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %#v", assistant.ToolCalls)
	}
	if assistant.ToolCalls[0].Function.Name != "run_command" || assistant.ToolCalls[1].Function.Name != "run_command" {
		t.Fatalf("unexpected tool names: %#v", assistant.ToolCalls)
	}
	if assistant.ToolCalls[0].Function.Arguments != `{"command":"printf add"}` {
		t.Fatalf("unexpected first args: %q", assistant.ToolCalls[0].Function.Arguments)
	}
	if assistant.ToolCalls[1].Function.Arguments != `{"command":"printf mul"}` {
		t.Fatalf("unexpected second args: %q", assistant.ToolCalls[1].Function.Arguments)
	}
	if assistant.ToolCalls[0].Index != nil || assistant.ToolCalls[1].Index != nil {
		t.Fatalf("stream-only tool indexes should not leak into stored messages: %#v", assistant.ToolCalls)
	}
}

func TestRunTaskStreamingFiltersThinkingTagsInTokens(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "ok"},
					},
				},
			},
		},
	}
	registry := tools.NewRegistry(".", "bash", time.Second, nil)
	agent := New(client, registry, 32768, nil)
	streamClient := &scriptedStreamClient{
		sequences: [][]model.StreamChunk{
			{
				{
					Choices: []model.StreamChoice{
						{
							Index: 0,
							Delta: model.StreamDelta{
								Role:    "assistant",
								Content: "Visible <thi",
							},
						},
					},
				},
				{
					Choices: []model.StreamChoice{
						{
							Index: 0,
							Delta: model.StreamDelta{
								Content: "nk>hidden</thinking </thinking> done",
							},
						},
					},
				},
			},
		},
	}
	agent.SetStreamClient(streamClient)

	session := agent.NewSession()
	var streamed strings.Builder
	_, err := session.RunTaskStreaming(context.Background(), "run", func(token string) {
		streamed.WriteString(token)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := streamed.String(); got != "Visible  done" {
		t.Fatalf("unexpected streamed content: %q", got)
	}
}

func TestRunTaskUsesFinalStepToForceAnswer(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: model.ToolFunction{
										Name:      "list_files",
										Arguments: `{"path":"."}`,
									},
								},
							},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Content: "final answer",
						},
					},
				},
			},
		},
	}

	agent := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	result, err := agent.NewSession().RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "final answer" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("unexpected request count: %d", len(client.requests))
	}
	if len(client.requests[0].Tools) == 0 || client.requests[0].ToolChoice != "auto" {
		t.Fatalf("expected first request to allow tools, got %#v", client.requests[0])
	}
	if len(client.requests[1].Tools) == 0 || client.requests[1].ToolChoice != "auto" {
		t.Fatalf("expected follow-up request to keep tools enabled, got %#v", client.requests[1])
	}
}

func TestHasConsecutiveToolErrors(t *testing.T) {
	msgs := []model.Message{
		{Role: "tool", Content: "tool error: failed one"},
		{Role: "tool", Content: "tool error: failed two"},
		{Role: "tool", Content: "tool error: failed three"},
	}
	if !hasConsecutiveToolErrors(msgs, 3) {
		t.Fatalf("expected consecutive tool errors to be detected")
	}
}

func TestHasConsecutiveToolErrorsStopsAtNewUserMessage(t *testing.T) {
	msgs := []model.Message{
		{Role: "tool", Content: "tool error: failed one"},
		{Role: "tool", Content: "tool error: failed two"},
		{Role: "tool", Content: "tool error: failed three"},
		{Role: "user", Content: "try again with a different approach"},
	}
	if hasConsecutiveToolErrors(msgs, 3) {
		t.Fatalf("expected new user message to reset recoverable tool error stop")
	}
}

func TestHasConsecutiveToolErrorsStopsAtSystemMessage(t *testing.T) {
	msgs := []model.Message{
		{Role: "tool", Content: "tool error: failed one"},
		{Role: "tool", Content: "tool error: failed two"},
		{Role: "tool", Content: "tool error: failed three"},
		{Role: "system", Content: "resume after fixing the environment"},
	}
	if hasConsecutiveToolErrors(msgs, 3) {
		t.Fatalf("expected non-tool boundary to reset recoverable tool error stop")
	}
}

func TestHasRepeatedToolLoop(t *testing.T) {
	call := model.ToolCall{
		ID:   "1",
		Type: "function",
		Function: model.ToolFunction{
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	}
	msgs := []model.Message{
		{Role: "assistant", ToolCalls: []model.ToolCall{call}},
		{Role: "tool", Content: "ok"},
		{Role: "assistant", ToolCalls: []model.ToolCall{call}},
		{Role: "tool", Content: "ok"},
		{Role: "assistant", ToolCalls: []model.ToolCall{call}},
		{Role: "tool", Content: "ok"},
		{Role: "assistant", ToolCalls: []model.ToolCall{call}},
	}
	if !hasRepeatedToolLoop(msgs, 4) {
		t.Fatalf("expected repeated tool loop to be detected")
	}
}

func TestRunTaskInjectsTodoReminderAfterSeveralToolRounds(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "list_files",
					Arguments: `{"path":"."}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "grep",
					Arguments: `{"pattern":"Goal","path":"docs"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-3",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "read_file",
					Arguments: `{"path":"docs/plan.md"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "done"}}}},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	result, err := a.NewSession().RunTask(context.Background(), "inspect repo and summarize")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 4 {
		t.Fatalf("unexpected request count: %d", len(client.requests))
	}
	fourth := client.requests[3]
	found := false
	for _, msg := range fourth.Messages {
		if msg.Role == "system" && strings.Contains(model.ContentString(msg.Content), todoNagPrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected todo reminder in request 4 messages: %#v", fourth.Messages)
	}
}

func TestSessionCanRecoverAfterRepeatedToolFailures(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{
				ToolCalls: []model.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "missing_tool_a",
							Arguments: `{}`,
						},
					},
					{
						ID:   "call-2",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "missing_tool_b",
							Arguments: `{}`,
						},
					},
					{
						ID:   "call-3",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "missing_tool_c",
							Arguments: `{}`,
						},
					},
				},
			}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "recovered"}}}},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()

	first, err := session.RunTask(context.Background(), "try the broken tools")
	if err != nil {
		t.Fatalf("first run task: %v", err)
	}
	if !strings.Contains(first.Final, "repeated tool failures") {
		t.Fatalf("expected recoverable tool failure stop, got %q", first.Final)
	}

	second, err := session.RunTask(context.Background(), "continue without those tools")
	if err != nil {
		t.Fatalf("second run task: %v", err)
	}
	if second.Final != "recovered" {
		t.Fatalf("expected recovery response, got %q", second.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected second request after recovery, got %d", len(client.requests))
	}
}

func TestApproxTokenCountTreatsCJKAsHeavier(t *testing.T) {
	ascii := approxTokenCount("hello world")
	cjk := approxTokenCount("你好世界")
	if cjk <= ascii {
		t.Fatalf("expected CJK to cost more tokens: ascii=%d cjk=%d", ascii, cjk)
	}
}

func TestConversationSizeUsesTokenApproximation(t *testing.T) {
	ascii := conversationSize([]model.Message{{Role: "user", Content: strings.Repeat("a", 40)}})
	cjk := conversationSize([]model.Message{{Role: "user", Content: "你你你你你你你你你你"}})
	if cjk <= 0 || ascii <= 0 {
		t.Fatalf("unexpected sizes: ascii=%d cjk=%d", ascii, cjk)
	}
	if cjk <= ascii/2 {
		t.Fatalf("expected CJK size not to be heavily undercounted: ascii=%d cjk=%d", ascii, cjk)
	}
}
