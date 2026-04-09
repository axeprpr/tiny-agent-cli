package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestAnnotateToolResultAppendsReminder(t *testing.T) {
	got := annotateToolResult("file-a\nfile-b")
	if !strings.Contains(got, "file-a") {
		t.Fatalf("expected original tool output, got %q", got)
	}
	if !strings.Contains(got, postToolAnswerReminder) {
		t.Fatalf("expected post-tool reminder, got %q", got)
	}
}

func TestShouldRetryEmptyToolAnswerAfterToolResult(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "show the file contents"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("calc.py")},
	}
	if !shouldRetryEmptyToolAnswer(messages, model.Message{}) {
		t.Fatalf("expected empty post-tool answer to retry")
	}
}

func TestShouldNotRetryEmptyToolAnswerTwice(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "show the file contents"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("calc.py")},
		{Role: "user", Content: emptyToolAnswerRetryReminder},
	}
	if shouldRetryEmptyToolAnswer(messages, model.Message{}) {
		t.Fatalf("expected retry reminder to suppress another retry")
	}
}

func TestDecideTurnExecutesToolCalls(t *testing.T) {
	decision := decideTurn(nil, model.Message{
		ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}},
	})
	if decision.action != turnActionExecuteTools {
		t.Fatalf("expected execute-tools decision, got %#v", decision)
	}
}

func TestDecideTurnRetriesOnEmptyPostToolAnswer(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "show the file contents"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("calc.py")},
	}
	decision := decideTurn(messages, model.Message{})
	if decision.action != turnActionRetry || decision.reminder != emptyToolAnswerRetryReminder {
		t.Fatalf("expected empty-answer retry decision, got %#v", decision)
	}
}

func TestEvidenceRetryReminderRequestsDirectFileRead(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Show me the contents of chat_note.txt."},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("chat_note.txt")},
	}
	got := evidenceRetryReminder(messages, model.Message{Content: "I found chat_note.txt in the directory."})
	if got != fileEvidenceRetryReminder {
		t.Fatalf("expected direct-content reminder, got %q", got)
	}
}

func TestEvidenceRetryReminderRequestsOfficialURL(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Find the GitHub repository URL for tiny-agent-cli."},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "web_search",
				Arguments: `{"query":"tiny-agent-cli"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("query: tiny-agent-cli")},
	}
	got := evidenceRetryReminder(messages, model.Message{Content: "I found some search results."})
	if got != urlEvidenceRetryReminder {
		t.Fatalf("expected official-url reminder, got %q", got)
	}
}

func TestEvidenceRetryReminderSkipsWhenDirectReadAlreadyUsed(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Show me the contents of chat_note.txt."},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"chat_note.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("hello-chat")},
	}
	got := evidenceRetryReminder(messages, model.Message{Content: "The file contains hello-chat."})
	if got != "" {
		t.Fatalf("expected no reminder, got %q", got)
	}
}

func TestDecideTurnRetriesOnShallowEvidence(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Show me the contents of chat_note.txt."},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("chat_note.txt")},
	}
	decision := decideTurn(messages, model.Message{Content: "I found chat_note.txt in the directory."})
	if decision.action != turnActionRetry || decision.reminder != fileEvidenceRetryReminder {
		t.Fatalf("expected shallow-evidence retry decision, got %#v", decision)
	}
}

func TestDecideTurnFinishesWithFinalText(t *testing.T) {
	decision := decideTurn(nil, model.Message{Content: "final answer"})
	if decision.action != turnActionFinish || decision.final != "final answer" {
		t.Fatalf("expected finish decision, got %#v", decision)
	}
}

type scriptedToolExecutor struct {
	messages []model.Message
	calls    []model.ToolCall
}

func (e *scriptedToolExecutor) ExecuteToolCall(_ context.Context, _ int, _ int, _ int, call model.ToolCall) model.Message {
	e.calls = append(e.calls, call)
	if len(e.messages) == 0 {
		return model.Message{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    annotateToolResult(fmt.Sprintf("tool error: no scripted tool result for %s", call.Function.Name)),
		}
	}
	msg := e.messages[0]
	e.messages = e.messages[1:]
	if strings.TrimSpace(msg.ToolCallID) == "" {
		msg.ToolCallID = call.ID
	}
	if strings.TrimSpace(model.ContentString(msg.Content)) == "" {
		msg.Content = annotateToolResult("(empty tool result)")
	}
	return msg
}

func testHookShellSnippet(script string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(script, "'", "\"")
	}
	return script
}

type stubPermissionDecider struct {
	calls []tools.ToolInvocation
	err   error
}

func (d *stubPermissionDecider) Decide(_ context.Context, inv tools.ToolInvocation) error {
	d.calls = append(d.calls, inv)
	return d.err
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

func TestConversationRuntimeRunsAgainstSessionState(t *testing.T) {
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

	session := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession()
	runtime := session.Runtime()
	if runtime.Session() != session {
		t.Fatalf("expected runtime to expose the original session")
	}

	result, err := runtime.RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(session.Messages()) < 3 {
		t.Fatalf("expected runtime to mutate session state, got %#v", session.Messages())
	}
}

func TestRunTaskRetriesWhenToolEvidenceIsTooShallow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chat_note.txt"), []byte("hello-chat"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
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
							Content: "I found chat_note.txt in the directory.",
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Content: "The contents are hello-chat.",
						},
					},
				},
			},
		},
	}

	agent := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	result, err := agent.NewSession().RunTask(context.Background(), "Show me the contents of chat_note.txt.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "The contents are hello-chat." {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected a retry request, got %d requests", len(client.requests))
	}
	lastReq := client.requests[2]
	if len(lastReq.Messages) == 0 {
		t.Fatalf("expected retry request messages")
	}
	lastMsg := lastReq.Messages[len(lastReq.Messages)-1]
	if lastMsg.Role != "user" || !strings.HasPrefix(model.ContentString(lastMsg.Content), fileEvidenceRetryReminder) {
		t.Fatalf("expected direct-content reminder, got %#v", lastMsg)
	}
}

func TestRunTaskUsesConfiguredToolExecutor(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "list_files",
									Arguments: `{"path":"."}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "done"},
					},
				},
			},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	exec := &scriptedToolExecutor{
		messages: []model.Message{
			{Role: "tool", Content: annotateToolResult("custom executor output")},
		},
	}
	a.SetToolExecutor(exec)

	session := a.NewSession()
	result, err := session.RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(exec.calls) != 1 || exec.calls[0].Function.Name != "list_files" {
		t.Fatalf("expected configured tool executor to receive the tool call, got %#v", exec.calls)
	}
	found := false
	for _, msg := range session.Messages() {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "custom executor output") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected session to keep tool message from configured executor")
	}
}

func TestRunTaskUsesAgentPermissionDeciderAndRecordsTurnSummaries(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "run_command",
									Arguments: `{"command":"printf denied"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "done"},
					},
				},
			},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	perm := &stubPermissionDecider{err: fmt.Errorf("denied by test policy")}
	a.SetToolPermissionDecider(perm)

	session := a.NewSession()
	result, err := session.RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(perm.calls) != 1 || perm.calls[0].Name != "run_command" {
		t.Fatalf("expected permission decider to receive tool invocation, got %#v", perm.calls)
	}
	turns := session.TurnSummaries()
	if len(turns) != 2 {
		t.Fatalf("expected two turn summaries, got %#v", turns)
	}
	if turns[0].Decision != "execute_tools" {
		t.Fatalf("unexpected first turn decision: %#v", turns[0])
	}
	if len(turns[0].ToolResults) != 1 || !strings.Contains(model.ContentString(turns[0].ToolResults[0].Content), "denied by test policy") {
		t.Fatalf("expected denied tool result in first turn summary, got %#v", turns[0])
	}
	if turns[1].Decision != "finish" || turns[1].Final != "done" {
		t.Fatalf("unexpected final turn summary: %#v", turns[1])
	}
	if result.Steps != 2 {
		t.Fatalf("expected result steps to track turn summaries, got %d", result.Steps)
	}
}

func TestRunTaskUsesAgentHookRunnerAroundToolExecution(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "read_file",
									Arguments: `{"path":"note.txt"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "done"},
					},
				},
			},
		},
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	a.SetToolHookRunner(tools.NewHookRunner(tools.HookConfig{
		PreToolUse:  []string{testHookShellSnippet("printf 'pre hook ran'")},
		PostToolUse: []string{testHookShellSnippet("printf 'post hook ran'")},
	}))

	session := a.NewSession()
	result, err := session.RunTask(context.Background(), "read note.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	turns := session.TurnSummaries()
	if len(turns) == 0 || len(turns[0].ToolResults) != 1 {
		t.Fatalf("expected tool results in turn summary, got %#v", turns)
	}
	output := model.ContentString(turns[0].ToolResults[0].Content)
	if !strings.Contains(output, "pre hook ran") || !strings.Contains(output, "post hook ran") {
		t.Fatalf("expected hook feedback in tool result, got %q", output)
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
