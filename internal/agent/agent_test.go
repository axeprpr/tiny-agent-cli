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

func TestRunTaskStepBudgetFinalizesWithContinueHintWhenModelStillWantsTools(t *testing.T) {
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
	}, tools.NewRegistry(".", "bash", time.Second, nil), 1, nil)

	result, err := agent.NewSession().RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Final, "Send `continue` to keep going") {
		t.Fatalf("expected continue hint, got %q", result.Final)
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

	agent := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 2, nil)
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
	if len(client.requests[1].Tools) != 0 || client.requests[1].ToolChoice != "none" {
		t.Fatalf("expected final request to disable tools, got %#v", client.requests[1])
	}
}
