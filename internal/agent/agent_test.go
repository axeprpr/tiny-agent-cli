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

	got := compactConversation(messages, 600)
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

func TestTruncateToolMessageShrinksLargeOutput(t *testing.T) {
	got := truncateToolMessage(strings.Repeat("B", 5000), 900)
	if len(got) >= 5000 {
		t.Fatalf("expected truncated output, got length %d", len(got))
	}
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatalf("expected truncation marker, got %q", got)
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
