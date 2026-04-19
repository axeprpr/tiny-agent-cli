package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestReplaceMessagesConvertsMisplacedSystemMessages(t *testing.T) {
	a := New(stubChatClient{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()
	session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "base system"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "late system note"},
	})

	msgs := session.Messages()
	if len(msgs) != 3 {
		t.Fatalf("unexpected message count: %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected first message to remain system, got %#v", msgs[0])
	}
	if msgs[2].Role != "user" {
		t.Fatalf("expected misplaced system message to become user reminder, got %#v", msgs[2])
	}
	if !strings.HasPrefix(model.ContentString(msgs[2].Content), systemReminderTag) {
		t.Fatalf("expected system reminder tag, got %q", model.ContentString(msgs[2].Content))
	}
}

func TestBuildRequestDoesNotSendNonLeadingSystemMessages(t *testing.T) {
	a := New(stubChatClient{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()
	session.messages = []model.Message{
		{Role: "system", Content: "base system"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "legacy background note"},
		{Role: "assistant", Content: "ok"},
	}

	req := session.buildRequest()
	for i, msg := range req.Messages {
		if i > 0 && msg.Role == "system" {
			t.Fatalf("request contained non-leading system message at %d: %#v", i, req.Messages)
		}
	}
	if req.Messages[2].Role != "user" || !strings.Contains(model.ContentString(req.Messages[2].Content), "legacy background note") {
		t.Fatalf("expected legacy system note to be preserved as user reminder, got %#v", req.Messages[2])
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
	if len(got) < 3 {
		t.Fatalf("expected summarized history plus recent messages, got %#v", got)
	}
	if got[0].Role != "system" || !strings.Contains(model.ContentString(got[0].Content), syntheticSummaryPrefix) {
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

func TestPruneConversationKeepsLatestUserTurnWithToolResult(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "earlier user"},
		{Role: "assistant", Content: "earlier answer"},
		{Role: "user", Content: "read bigmemo.txt and remember the passphrase"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult(strings.Repeat("chunk ", 200) + "PASSPHRASE=cedar-omega")},
	}

	got := pruneConversation(messages, 1200)
	var sawLatestUser, sawTool bool
	for _, msg := range got {
		if msg.Role == "user" && strings.Contains(model.ContentString(msg.Content), "remember the passphrase") {
			sawLatestUser = true
		}
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "PASSPHRASE=cedar-omega") {
			sawTool = true
		}
	}
	if !sawLatestUser || !sawTool {
		t.Fatalf("expected latest user turn and tool result to remain, got %#v", got)
	}
}

func TestCompactConversationKeepsLatestTurnWhenLimitIsTight(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: strings.Repeat("system ", 200)},
		{Role: "user", Content: "read bigmemo.txt and remember the passphrase"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult(strings.Repeat("chunk ", 300) + "PASSPHRASE=cedar-omega")},
	}

	got := compactConversation(messages, 1200)
	var sawLatestUser, sawTool bool
	for _, msg := range got {
		if msg.Role == "user" && strings.Contains(model.ContentString(msg.Content), "remember the passphrase") {
			sawLatestUser = true
		}
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "PASSPHRASE=cedar-omega") {
			sawTool = true
		}
	}
	if !sawLatestUser || !sawTool {
		t.Fatalf("expected compacted conversation to preserve latest turn, got %#v", got)
	}
}

func TestCompactConversationAggressiveToolCompactionKeepsTailFacts(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: strings.Repeat("system ", 400)},
		{Role: "user", Content: "read bigmemo.txt and remember the passphrase"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult(strings.Repeat("memory chunk retains background detail\n", 200) + "PASSPHRASE=cedar-omega")},
		{Role: "user", Content: "what is the passphrase"},
	}

	got := compactConversation(messages, 240)
	var sawTailFact bool
	for _, msg := range got {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "PASSPHRASE=cedar-omega") {
			sawTailFact = true
		}
	}
	if !sawTailFact {
		t.Fatalf("expected compacted tool output to retain tail fact, got %#v", got)
	}
}

func TestCompactConversationSummaryCarriesOlderToolFacts(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "read bigmemo.txt and remember the passphrase"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult(strings.Repeat("memory chunk retains background detail\n", 60) + "PASSPHRASE=cedar-omega")},
		{Role: "assistant", Content: "noted."},
		{Role: "user", Content: "filler-one"},
		{Role: "assistant", Content: "filler-one"},
		{Role: "user", Content: "filler-two"},
		{Role: "assistant", Content: "filler-two"},
	}

	got := compactConversation(messages, 220)
	var summary string
	for _, msg := range got {
		if msg.Role == "system" && strings.Contains(model.ContentString(msg.Content), syntheticSummaryPrefix) {
			summary = model.ContentString(msg.Content)
			break
		}
	}
	if !strings.Contains(summary, "PASSPHRASE=cedar-omega") {
		t.Fatalf("expected synthetic summary to preserve older tool fact, got %#v", got)
	}
}

func TestExtractStructuredToolFactsFindsKeyValueFacts(t *testing.T) {
	got := extractStructuredToolFacts("noise line\nowner: regression-bot\nPASSPHRASE=cedar-omega\n\n<system-reminder>ignore</system-reminder>", 3)
	want := []string{"owner: regression-bot", "PASSPHRASE=cedar-omega"}
	if len(got) != len(want) {
		t.Fatalf("unexpected facts: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected fact %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCompactConversationFallbackSummaryStillKeepsLatestTurn(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "read bigmemo.txt and remember the passphrase"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult(strings.Repeat("memory chunk retains background detail\n", 80) + "PASSPHRASE=cedar-omega")},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is the passphrase?"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"bigmemo.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-2", Content: annotateToolResult(strings.Repeat("memory chunk retains background detail\n", 120) + "PASSPHRASE=cedar-omega")},
	}

	got := compactConversation(messages, 180)
	var sawLatestUser bool
	for _, msg := range got {
		if msg.Role == "user" && strings.Contains(strings.ToLower(model.ContentString(msg.Content)), "what is the passphrase") {
			sawLatestUser = true
		}
	}
	if !sawLatestUser {
		t.Fatalf("expected fallback summary path to keep latest user turn, got %#v", got)
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

func TestShouldRetryEmptyToolAnswerAgainForNewToolBlock(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "do the whole task"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "write_file",
				Arguments: `{"path":"sample.txt","content":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("wrote sample.txt")},
		{Role: "user", Content: emptyToolAnswerRetryReminder},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"cat sample.txt"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-2", Content: annotateToolResult("x")},
	}
	if !shouldRetryEmptyToolAnswer(messages, model.Message{}) {
		t.Fatalf("expected a new tool block to allow another retry")
	}
}

func TestShouldRetryFailedCommandAfterEmptyAnswer(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "run calc.py and tell me the result"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"python calc.py"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("bash: line 1: python: command not found")},
	}
	if !shouldRetryFailedCommand(messages, model.Message{}) {
		t.Fatalf("expected failed command to trigger retry")
	}
}

func TestShouldNotRetryFailedCommandTwice(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "run calc.py and tell me the result"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"python calc.py"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("bash: line 1: python: command not found")},
		{Role: "user", Content: failedCommandRetryReminder},
	}
	if shouldRetryFailedCommand(messages, model.Message{}) {
		t.Fatalf("expected failed-command reminder to suppress another retry")
	}
}

func TestShouldRetryFailedCommandAgainForNewToolBlock(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "keep trying"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"missing-a"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: command failed\nmissing-a: command not found")},
		{Role: "user", Content: failedCommandRetryReminder},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"missing-b"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-2", Content: annotateToolResult("tool error: command failed\nmissing-b: command not found")},
	}
	if !shouldRetryFailedCommand(messages, model.Message{}) {
		t.Fatalf("expected a new failed command block to allow another retry")
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

func TestDecideTurnStopsAfterExplicitUserDenial(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "update the file"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: file write rejected by user")},
	}
	decision := decideTurn(messages, model.Message{
		ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "edit_file",
				Arguments: `{"path":"README.md","search":"old","replace":"new"}`,
			},
		}},
	})
	if decision.action != turnActionFinish || decision.final != userDeniedActionFinal {
		t.Fatalf("expected explicit-denial finish decision, got %#v", decision)
	}
}

func TestDecideTurnOverridesAssistantWorkaroundAfterExplicitUserDenial(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "write the file"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: file write rejected by user")},
	}
	decision := decideTurn(messages, model.Message{Content: "写文件被拒了，我改用精确编辑现有文件。"})
	if decision.action != turnActionFinish || decision.final != userDeniedActionFinal {
		t.Fatalf("expected denial to return control to user, got %#v", decision)
	}
}

func TestDecideTurnKeepsSafeAssistantReplyAfterExplicitUserDenial(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "write the file"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: file write rejected by user")},
	}
	decision := decideTurn(messages, model.Message{Content: "Creating new files is currently not allowed in this workspace. Is there something else I can help you with?"})
	if decision.action != turnActionFinish || decision.final != "Creating new files is currently not allowed in this workspace. Is there something else I can help you with?" {
		t.Fatalf("expected safe denial reply to pass through, got %#v", decision)
	}
}

func TestDecideTurnKeepsSafeAssistantReplyWithLetMeKnowAfterExplicitUserDenial(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "write the file"},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: file write rejected by user")},
	}
	reply := "I am currently unable to create the file DENY_TEST_2.txt due to workspace restrictions. Let me know if you'd like me to try another approach or assist with something else."
	decision := decideTurn(messages, model.Message{Content: reply})
	if decision.action != turnActionFinish || decision.final != reply {
		t.Fatalf("expected safe denial reply to pass through, got %#v", decision)
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

func TestDecideTurnRetriesOnFailedCommandRecovery(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "run calc.py and tell me the result"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"python calc.py"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("bash: line 1: python: command not found")},
	}
	decision := decideTurn(messages, model.Message{})
	if decision.action != turnActionRetry || decision.reminder != failedCommandRetryReminder {
		t.Fatalf("expected failed-command retry decision, got %#v", decision)
	}
}

func TestDecideTurnUsesToolOutputAfterRepeatedEmptyAnswer(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "show the output"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"printf done"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("done")},
		{Role: "user", Content: emptyToolAnswerRetryReminder},
	}
	decision := decideTurn(messages, model.Message{})
	if decision.action != turnActionFinish || decision.final != "done" {
		t.Fatalf("expected fallback final from tool output, got %#v", decision)
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

func TestDecideTurnUsesSyntaxSpecificFailedCommandReminder(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "run this command"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"echo hi | | cat"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: command failed (syntax)\n[run_command_error kind=syntax]\nbash: syntax error near unexpected token `|'")},
	}
	decision := decideTurn(messages, model.Message{})
	if decision.action != turnActionRetry || decision.reminder != failedCommandSyntaxRetryReminder {
		t.Fatalf("expected syntax-specific retry reminder, got %#v", decision)
	}
}

func TestDecideTurnRetriesCompletionClaimWithoutEvidence(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "optimize the script"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "edit_file",
				Arguments: `{"path":"scripts/preflight.sh","old_text":"foo","new_text":"bar"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("edited /repo/scripts/preflight.sh")},
	}
	decision := decideTurn(messages, model.Message{Content: "优化完成，全部可以安全使用。"})
	if decision.action != turnActionRetry || decision.reminder != completionClaimRetryReminder {
		t.Fatalf("expected completion-claim retry reminder, got %#v", decision)
	}
}

func TestDecideTurnAllowsCompletionClaimWithEvidence(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "optimize the script"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "edit_file",
				Arguments: `{"path":"scripts/preflight.sh","old_text":"foo","new_text":"bar"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("edited /repo/scripts/preflight.sh")},
	}
	decision := decideTurn(messages, model.Message{Content: "优化完成。变更文件 scripts/preflight.sh，并用 run_command 验证语法通过。"})
	if decision.action != turnActionFinish {
		t.Fatalf("expected finish decision with evidence, got %#v", decision)
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

type timingToolExecutor struct {
	mu    sync.Mutex
	start map[string]time.Time
	delay time.Duration
}

func (e *timingToolExecutor) ExecuteToolCall(_ context.Context, _ int, _ int, _ int, call model.ToolCall) model.Message {
	if e.delay <= 0 {
		e.delay = 150 * time.Millisecond
	}
	e.mu.Lock()
	if e.start == nil {
		e.start = map[string]time.Time{}
	}
	e.start[call.Function.Name] = time.Now()
	e.mu.Unlock()
	time.Sleep(e.delay)
	return model.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Content:    annotateToolResult(call.Function.Name + " ok"),
	}
}

func (e *timingToolExecutor) started(name string) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.start[name]
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

type stubPermissionPolicy struct {
	calls    []tools.ToolInvocation
	decision tools.PermissionDecision
}

func (p *stubPermissionPolicy) Evaluate(_ context.Context, inv tools.ToolInvocation) tools.PermissionDecision {
	p.calls = append(p.calls, inv)
	return p.decision
}

type recordedEventSink struct {
	events []AgentEvent
}

func (s *recordedEventSink) RecordAgentEvent(_ context.Context, event AgentEvent) {
	s.events = append(s.events, event)
}

type scriptedChatClient struct {
	responses []model.Response
	errors    []error
	requests  []model.Request
}

func (s *scriptedChatClient) Complete(_ context.Context, req model.Request) (model.Response, error) {
	s.requests = append(s.requests, req)
	index := len(s.requests) - 1
	if index < len(s.errors) && s.errors[index] != nil {
		return model.Response{}, s.errors[index]
	}
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

func TestSessionQueueFollowUpContinuesAfterFirstFinish(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{Content: "first"}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "second"}}}},
		},
	}
	registry := tools.NewRegistry(".", "bash", time.Second, nil)
	a := New(client, registry, 32768, nil)
	session := a.NewSession()
	if !session.QueueFollowUpMessage("follow-up question") {
		t.Fatalf("expected follow-up message to be queued")
	}

	result, err := session.RunTask(context.Background(), "initial question")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "second" {
		t.Fatalf("expected second response after follow-up, got %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.requests))
	}
	var sawFollowUp bool
	for _, msg := range session.Messages() {
		if msg.Role == "user" && model.ContentString(msg.Content) == "follow-up question" {
			sawFollowUp = true
			break
		}
	}
	if !sawFollowUp {
		t.Fatalf("expected queued follow-up message in session history")
	}
}

func TestSessionQueueSteeringInjectsBeforeModelRequest(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{Content: "done"}}}},
		},
	}
	registry := tools.NewRegistry(".", "bash", time.Second, nil)
	a := New(client, registry, 32768, nil)
	session := a.NewSession()
	if !session.QueueSteeringMessage("extra steering context") {
		t.Fatalf("expected steering message to be queued")
	}

	result, err := session.RunTask(context.Background(), "initial question")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(client.requests))
	}
	req := client.requests[0]
	var sawSteering bool
	for _, msg := range req.Messages {
		if msg.Role == "user" && strings.Contains(model.ContentString(msg.Content), "extra steering context") {
			sawSteering = true
			break
		}
	}
	if !sawSteering {
		t.Fatalf("expected steering message in model request: %#v", req.Messages)
	}
}

func TestRunTaskExecutesReadOnlyToolBatchInParallel(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "read_file",
									Arguments: `{"path":"README.md"}`,
								},
							},
							{
								ID:   "call-2",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "list_files",
									Arguments: `{"path":"."}`,
								},
							},
						},
					},
				}},
			},
			{Choices: []model.Choice{{Message: model.Message{Content: "done"}}}},
		},
	}
	registry := tools.NewRegistry(".", "bash", time.Second, nil)
	a := New(client, registry, 32768, nil)
	exec := &timingToolExecutor{delay: 180 * time.Millisecond}
	a.SetToolExecutor(exec)

	started := time.Now()
	result, err := a.NewSession().RunTask(context.Background(), "inspect in parallel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	elapsed := time.Since(started)
	if elapsed >= 320*time.Millisecond {
		t.Fatalf("expected read-only tools to execute in parallel, elapsed=%s", elapsed)
	}
	t1 := exec.started("read_file")
	t2 := exec.started("list_files")
	if t1.IsZero() || t2.IsZero() {
		t.Fatalf("expected both tool calls to start: %#v", exec.start)
	}
	diff := t1.Sub(t2)
	if diff < 0 {
		diff = -diff
	}
	if diff > 80*time.Millisecond {
		t.Fatalf("expected near-concurrent start times, diff=%s", diff)
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

func TestRunTaskEmitsPermissionDecisionAndTurnSummaryEvents(t *testing.T) {
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
	policy := &stubPermissionPolicy{
		decision: tools.PermissionDecision{
			Allowed: false,
			Mode:    tools.PermissionModeReadOnly,
			Reason:  "denied by structured test policy",
		},
	}
	sink := &recordedEventSink{}
	a.SetToolPermissionPolicy(policy)
	a.SetEventSink(sink)

	session := a.NewSession()
	result, err := session.RunTask(context.Background(), "inspect repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final answer: %q", result.Final)
	}
	if len(policy.calls) != 1 || policy.calls[0].Name != "run_command" {
		t.Fatalf("expected policy to receive tool invocation, got %#v", policy.calls)
	}

	var permissionEvents []AgentEvent
	var turnEvents []AgentEvent
	for _, event := range sink.events {
		switch event.Type {
		case "permission_decision":
			permissionEvents = append(permissionEvents, event)
		case "turn_summary":
			turnEvents = append(turnEvents, event)
		}
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one permission_decision event, got %#v", sink.events)
	}
	if len(turnEvents) != 2 {
		t.Fatalf("expected two turn_summary events, got %#v", sink.events)
	}

	permission := permissionEvents[0]
	if permission.Data["tool"] != "run_command" {
		t.Fatalf("unexpected permission tool: %#v", permission.Data)
	}
	if permission.Data["required_mode"] != tools.PermissionModeDangerFullAccess {
		t.Fatalf("unexpected required mode: %#v", permission.Data)
	}
	decision, ok := permission.Data["decision"].(tools.PermissionDecision)
	if !ok {
		t.Fatalf("expected structured decision payload, got %#v", permission.Data["decision"])
	}
	if decision.Allowed || decision.Mode != tools.PermissionModeReadOnly || decision.Reason != "denied by structured test policy" {
		t.Fatalf("unexpected decision payload: %#v", decision)
	}

	firstTurn := turnEvents[0]
	if firstTurn.Data["decision"] != "execute_tools" {
		t.Fatalf("unexpected first turn decision payload: %#v", firstTurn.Data)
	}
	assistant, ok := firstTurn.Data["assistant"].(model.Message)
	if !ok {
		t.Fatalf("expected assistant message payload, got %#v", firstTurn.Data["assistant"])
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Name != "run_command" {
		t.Fatalf("unexpected assistant payload: %#v", assistant)
	}
	toolResults, ok := firstTurn.Data["tool_results"].([]model.Message)
	if !ok {
		t.Fatalf("expected tool results payload, got %#v", firstTurn.Data["tool_results"])
	}
	if len(toolResults) != 1 || !strings.Contains(model.ContentString(toolResults[0].Content), "denied by structured test policy") {
		t.Fatalf("unexpected tool results payload: %#v", toolResults)
	}

	secondTurn := turnEvents[1]
	if secondTurn.Data["decision"] != "finish" || secondTurn.Data["final"] != "done" {
		t.Fatalf("unexpected final turn event: %#v", secondTurn.Data)
	}
}

func TestVerifyRoleBlocksWriteFileToolCalls(t *testing.T) {
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
									Name:      "write_file",
									Arguments: `{"path":"VERIFY_DENY.txt","content":"x"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "verify role stayed read-only"},
					},
				},
			},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "verification",
		Objective: "Verify the change safely",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "read-only verification completed", Status: "completed", Evidence: "pre-seeded for role test"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	session := a.NewSessionWithPrompt(PromptContext{AgentRole: tools.AgentRoleVerify})
	result, err := session.RunTask(context.Background(), "verify the change")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "verify role stayed read-only" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if _, err := os.Stat(filepath.Join(dir, "VERIFY_DENY.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected verify role to block file creation, stat err=%v", err)
	}
	found := false
	for _, msg := range session.Messages() {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "verify role is read-only") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected read-only verify denial in tool output: %#v", session.Messages())
	}
}

func TestVerifyRoleAllowsReadOnlyRunCommand(t *testing.T) {
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
									Arguments: `{"command":"printf verify-ok"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "verify-ok"},
					},
				},
			},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "verification",
		Objective: "Verify the command path",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "read-only command ran", Status: "completed", Evidence: "pre-seeded for role test"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	session := a.NewSessionWithPrompt(PromptContext{AgentRole: tools.AgentRoleVerify})
	result, err := session.RunTask(context.Background(), "run a read-only verification command")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "verify-ok" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	found := false
	for _, msg := range session.Messages() {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "verify-ok") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected verify role to allow read-only command: %#v", session.Messages())
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
					Arguments: `{"path":"plan.md"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "done"}}}},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
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
		if msg.Role == "user" && strings.Contains(model.ContentString(msg.Content), todoNagPrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected todo reminder in request 4 messages: %#v", fourth.Messages)
	}
}

func TestFinishGateReminderBlocksPendingAcceptanceChecks(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		Deliverables: []tools.ContractItem{
			{Text: "single binary", Status: "completed", Evidence: "go build ./..."},
		},
		AcceptanceChecks: []tools.ContractItem{
			{Text: "GET / returns app html", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	got := a.NewSession().finishGateReminder()
	if !strings.Contains(got, "acceptance [pending]: GET / returns app html") {
		t.Fatalf("expected pending acceptance check in finish gate reminder, got %q", got)
	}
}

func TestVerifyFinishGateRequiresAcceptanceEvidence(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		Deliverables: []tools.ContractItem{
			{Text: "single binary", Status: "completed", Evidence: "go build ./..."},
		},
		AcceptanceChecks: []tools.ContractItem{
			{Text: "GET / returns app html", Status: "completed"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	session := a.NewSessionWithPrompt(PromptContext{AgentRole: tools.AgentRoleVerify})
	got := session.finishGateReminder()
	if !strings.Contains(got, "missing evidence") {
		t.Fatalf("expected verify finish gate to require evidence, got %q", got)
	}
}

func TestFinishGateAllowsTerminalBlockedAcceptanceChecks(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		AcceptanceChecks: []tools.ContractItem{
			{
				Text:         "GET / returns app html",
				Status:       "blocked",
				EvidenceKind: "http",
				Terminal:     true,
				Reason:       "current session mode cannot start long-running local services",
				Handoff:      "Run python3 -m http.server 4173 --bind 0.0.0.0 and retry the HTTP check.",
			},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	if got := a.NewSession().finishGateReminder(); got != "" {
		t.Fatalf("expected terminal blocked handoff to satisfy finish gate, got %q", got)
	}
}

func TestPlanGateReminderRequiresPlanBeforeMutatingShell(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()
	got := session.planGateReminder([]model.ToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: model.ToolFunction{
			Name:      "run_command",
			Arguments: `{"command":"npm create vite@latest web -- --template vue"}`,
		},
	}})
	if got != planGateRetryReminder {
		t.Fatalf("expected plan gate reminder, got %q", got)
	}
}

func TestPlanGateReminderAllowsMutatingShellWithContract(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "go build passes", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	session := a.NewSession()
	got := session.planGateReminder([]model.ToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: model.ToolFunction{
			Name:      "run_command",
			Arguments: `{"command":"npm install"}`,
		},
	}})
	if got != "" {
		t.Fatalf("expected plan gate to allow mutating shell with contract, got %q", got)
	}
}

func TestMutatingFailureCooldownBlocksAnotherMutatingAttempt(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()
	session.messages = []model.Message{
		{
			Role: "assistant",
			ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "run_command",
					Arguments: `{"command":"npm install codex"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "tool error: command failed\nnpm ERR! not found"},
	}
	got := session.mutatingFailureCooldownReminder([]model.ToolCall{{
		ID:   "call-2",
		Type: "function",
		Function: model.ToolFunction{
			Name:      "run_command",
			Arguments: `{"command":"pip install openai-codex"}`,
		},
	}})
	if got != mutatingFailureCooldownRetryReminder {
		t.Fatalf("expected mutating failure cooldown reminder, got %q", got)
	}
}

func TestMutatingFailureCooldownAllowsReadOnlyDiagnosis(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	session := a.NewSession()
	session.messages = []model.Message{
		{
			Role: "assistant",
			ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "run_command",
					Arguments: `{"command":"npm install codex"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "tool error: command failed\nnpm ERR! not found"},
	}
	got := session.mutatingFailureCooldownReminder([]model.ToolCall{{
		ID:   "call-2",
		Type: "function",
		Function: model.ToolFunction{
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	}})
	if got != "" {
		t.Fatalf("expected read-only diagnosis to be allowed, got %q", got)
	}
}

func TestRunTaskStopsAfterRepeatedFinishGateBlocks(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{Content: "done 1"}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "done 2"}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "done 3"}}}},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "GET / returns app html", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	result, err := a.NewSession().RunTask(context.Background(), "ship the embedded app")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if !strings.Contains(result.Final, "finish-gate doom loop") {
		t.Fatalf("expected finish-gate doom-loop stop, got %q", result.Final)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected three requests before doom-loop stop, got %d", len(client.requests))
	}
}

func TestRunTaskBypassesStaleFinishGateForUnrelatedRunTask(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "read_file",
					Arguments: `{"path":"note.txt"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "NOTE_OK"}}}},
		},
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("NOTE_OK\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded aquarium web app",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "GET / returns app html", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed stale task contract: %v", err)
	}

	result, err := a.NewSession().RunTask(context.Background(), "read note.txt and reply exactly NOTE_OK")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "NOTE_OK" {
		t.Fatalf("expected NOTE_OK final, got %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two requests, got %d", len(client.requests))
	}
}

func TestTryAutoCloseTaskContractFromRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTodo([]tools.TodoItem{{Text: "ship aquarium page", Status: "completed"}}); err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the aquarium page",
		Deliverables: []tools.ContractItem{
			{Text: "index.html exists", Status: "pending", EvidenceKind: "file"},
		},
		AcceptanceChecks: []tools.ContractItem{
			{Text: "page was verified after edit", Status: "pending", EvidenceKind: "file"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	session := a.NewSession()
	session.turns = []TurnSummary{
		{
			Assistant: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "write_file",
					Arguments: `{"path":"index.html","content":"<html></html>"}`,
				},
			}}},
			ToolResults: []model.Message{{
				Role:       "tool",
				ToolCallID: "call-1",
				Content:    annotateToolResult("wrote 12 bytes to index.html"),
			}},
		},
		{
			Assistant: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "read_file",
					Arguments: `{"path":"index.html"}`,
				},
			}}},
			ToolResults: []model.Message{{
				Role:       "tool",
				ToolCallID: "call-2",
				Content:    annotateToolResult("<html></html>"),
			}},
		},
	}

	if !session.tryAutoCloseTaskContract() {
		t.Fatalf("expected runtime evidence to auto-close contract")
	}
	contract := a.TaskContract()
	for _, item := range contract.Deliverables {
		if item.Status != "completed" {
			t.Fatalf("expected deliverable completed, got %#v", item)
		}
		if !strings.Contains(item.Evidence, "write_file:") {
			t.Fatalf("expected deliverable evidence to mention write_file, got %q", item.Evidence)
		}
	}
	for _, item := range contract.AcceptanceChecks {
		if item.Status != "completed" {
			t.Fatalf("expected acceptance completed, got %#v", item)
		}
		if !strings.Contains(item.Evidence, "read_file:") {
			t.Fatalf("expected acceptance evidence to mention read_file, got %q", item.Evidence)
		}
	}
}

func TestTryAutoCloseTaskContractRequiresVerificationAfterMutation(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the aquarium page",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "page was verified after edit", Status: "pending", EvidenceKind: "file"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	session := a.NewSession()
	session.turns = []TurnSummary{
		{
			Assistant: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "write_file",
					Arguments: `{"path":"index.html","content":"<html></html>"}`,
				},
			}}},
			ToolResults: []model.Message{{
				Role:       "tool",
				ToolCallID: "call-1",
				Content:    annotateToolResult("wrote 12 bytes to index.html"),
			}},
		},
	}

	if session.tryAutoCloseTaskContract() {
		t.Fatalf("expected contract to stay open without post-mutation verification")
	}
	contract := a.TaskContract()
	if got := contract.AcceptanceChecks[0].Status; got != "pending" {
		t.Fatalf("expected pending acceptance check, got %q", got)
	}
}

func TestRunTaskAutoClosesStaleContractAfterVerifiedMutation(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "update_task_contract",
					Arguments: `{"task_kind":"webapp_with_deploy","objective":"Ship the aquarium page","deliverables":[{"text":"index.html exists","status":"pending","evidence_kind":"file"}],"acceptance_checks":[{"text":"page was verified after edit","status":"pending","evidence_kind":"file"}]}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "update_todo",
					Arguments: `{"items":[{"text":"ship aquarium page","status":"in_progress"}]}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-3",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "write_file",
					Arguments: `{"path":"index.html","content":"<html></html>"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				{
					ID:   "call-4",
					Type: "function",
					Function: model.ToolFunction{
						Name:      "read_file",
						Arguments: `{"path":"index.html"}`,
					},
				},
				{
					ID:   "call-5",
					Type: "function",
					Function: model.ToolFunction{
						Name:      "update_todo",
						Arguments: `{"items":[{"text":"ship aquarium page","status":"completed"}]}`,
					},
				},
			}}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "done"}}}},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	result, err := a.NewSession().RunTask(context.Background(), "ship the aquarium page")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "done" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 5 {
		t.Fatalf("expected five requests including final closeout, got %d", len(client.requests))
	}
	contract := a.TaskContract()
	for _, item := range contract.Deliverables {
		if item.Status != "completed" || strings.TrimSpace(item.Evidence) == "" {
			t.Fatalf("expected completed deliverable with evidence, got %#v", item)
		}
	}
	for _, item := range contract.AcceptanceChecks {
		if item.Status != "completed" || strings.TrimSpace(item.Evidence) == "" {
			t.Fatalf("expected completed acceptance with evidence, got %#v", item)
		}
	}
}

func TestTryAutoCloseTaskContractDoesNotCompleteBrowserChecksFromStaticEvidence(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTodo([]tools.TodoItem{{Text: "ship aquarium page", Status: "completed"}}); err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the aquarium page",
		AcceptanceChecks: []tools.ContractItem{
			{Text: "page renders without console errors", Status: "pending", EvidenceKind: "browser"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	session := a.NewSession()
	session.turns = []TurnSummary{
		{
			Assistant: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "write_file",
					Arguments: `{"path":"index.html","content":"<html></html>"}`,
				},
			}}},
			ToolResults: []model.Message{{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("wrote 12 bytes to index.html")}},
		},
		{
			Assistant: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "run_command",
					Arguments: `{"command":"grep -n \"type=\\\"module\\\"\" index.html"}`,
				},
			}}},
			ToolResults: []model.Message{{Role: "tool", ToolCallID: "call-2", Content: annotateToolResult("12:<script type=\"module\">")}},
		},
	}

	if session.tryAutoCloseTaskContract() {
		t.Fatalf("expected browser evidence requirement to prevent auto-close")
	}
	if got := a.TaskContract().AcceptanceChecks[0].Status; got != "pending" {
		t.Fatalf("expected browser acceptance to remain pending, got %q", got)
	}
}

func TestTryAutoCloseTaskContractAutoBlocksTerminalHTTPCheckOnServiceConstraint(t *testing.T) {
	dir := t.TempDir()
	a := New(stubChatClient{}, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	if err := a.ReplaceTodo([]tools.TodoItem{{Text: "ship aquarium page", Status: "completed"}}); err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the aquarium page",
		AcceptanceChecks: []tools.ContractItem{
			{
				Text:         "GET / returns app html",
				Status:       "pending",
				EvidenceKind: "http",
				Terminal:     true,
			},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}

	session := a.NewSession()
	session.messages = []model.Message{
		{Role: "system", Content: "base system"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "run_command",
				Arguments: `{"command":"python3 -m http.server 8000 >/tmp/server.log 2>&1 & sleep 1; curl -I http://127.0.0.1:8000/"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: annotateToolResult("tool error: command appears to start a long-running service via shell backgrounding; use start_background_job or detach it with nohup/setsid")},
	}

	if !session.tryAutoCloseTaskContract() {
		t.Fatalf("expected blocked handoff downgrade to satisfy finish gate")
	}
	item := a.TaskContract().AcceptanceChecks[0]
	if item.Status != "blocked" || strings.TrimSpace(item.Reason) == "" || strings.TrimSpace(item.Handoff) == "" {
		t.Fatalf("expected blocked terminal handoff, got %#v", item)
	}
	if got := session.finishGateReminder(); got != "" {
		t.Fatalf("expected blocked terminal handoff to satisfy finish gate, got %q", got)
	}
}

func TestRunTaskRetriesForPlanGateBeforeMutatingShell(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "run_command",
					Arguments: `{"command":"npm create vite@latest web -- --template vue"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{Content: "planned"}}}},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	result, err := a.NewSession().RunTask(context.Background(), "create a vue app and deploy it")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "planned" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected plan-gate retry before execution, got %d requests", len(client.requests))
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if last.Role != "user" || model.ContentString(last.Content) != planGateRetryReminder {
		t.Fatalf("expected plan-gate reminder in retry request, got %#v", last)
	}
}

func TestRunTaskRetriesAgainForNewToolBlockAndFallsBackFromToolOutput(t *testing.T) {
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "write_file",
					Arguments: `{"path":"sample.txt","content":"hello"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{}}}},
			{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:   "call-2",
				Type: "function",
				Function: model.ToolFunction{
					Name:      "run_command",
					Arguments: `{"command":"printf second-output"}`,
				},
			}}}}}},
			{Choices: []model.Choice{{Message: model.Message{}}}},
			{Choices: []model.Choice{{Message: model.Message{}}}},
		},
	}

	dir := t.TempDir()
	a := New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	result, err := a.NewSession().RunTask(context.Background(), "do the multi-step task")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "second-output" {
		t.Fatalf("expected fallback final from latest tool output, got %q", result.Final)
	}
	if len(client.requests) != 5 {
		t.Fatalf("expected two retries plus fallback, got %d requests", len(client.requests))
	}
	secondRetry := client.requests[4].Messages[len(client.requests[4].Messages)-1]
	if secondRetry.Role != "user" || !strings.HasPrefix(model.ContentString(secondRetry.Content), emptyToolAnswerRetryReminder) {
		t.Fatalf("expected second answer-now reminder for new tool block, got %#v", secondRetry)
	}
}

func TestRunTaskHandlesLongContextDuringLocalPackageInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell commands are bash-specific")
	}
	goBin := "/usr/local/go/bin/go"
	if _, err := os.Stat(goBin); err != nil {
		if found, lookErr := exec.LookPath("go"); lookErr == nil {
			goBin = found
		} else {
			t.Skip("go toolchain not available")
		}
	}

	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "cmd", "demo-tool")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/installfixture\n\ngo 1.25.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello-from-demo-tool\") }\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
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
										Name:      "run_command",
										Arguments: fmt.Sprintf(`{"command":"HOME=/root GOBIN=$PWD/bin GOPATH=$PWD/.gopath GOMODCACHE=$PWD/.gopath/pkg/mod GOCACHE=$PWD/.gocache %s install ./cmd/demo-tool","timeout_seconds":60}`, goBin),
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
							ToolCalls: []model.ToolCall{
								{
									ID:   "call-2",
									Type: "function",
									Function: model.ToolFunction{
										Name:      "run_command",
										Arguments: `{"command":"./bin/demo-tool","timeout_seconds":30}`,
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
						Message: model.Message{Content: "Installed demo-tool and verified hello-from-demo-tool."},
					},
				},
			},
		},
	}

	a := New(client, tools.NewRegistry(dir, "bash", 90*time.Second, nil), 1200, nil)
	if err := a.ReplaceTaskContract(tools.TaskContract{
		TaskKind:  "local_package_install",
		Objective: "Install and verify the local demo tool",
		Deliverables: []tools.ContractItem{
			{Text: "demo tool installed into ./bin", Status: "completed", Evidence: "pre-seeded for compaction regression"},
		},
		AcceptanceChecks: []tools.ContractItem{
			{Text: "demo tool prints hello-from-demo-tool", Status: "completed", Evidence: "pre-seeded for compaction regression"},
		},
	}); err != nil {
		t.Fatalf("seed task contract: %v", err)
	}
	session := a.NewSession()
	history := session.Messages()
	for i := 0; i < 12; i++ {
		history = append(history,
			model.Message{Role: "user", Content: strings.Repeat(fmt.Sprintf("previous install requirement %d ", i), 32)},
			model.Message{Role: "assistant", Content: strings.Repeat(fmt.Sprintf("previous install update %d ", i), 28)},
		)
	}
	session.ReplaceMessages(history)

	result, err := session.RunTask(context.Background(), "Install the local demo-tool binary into ./bin, verify it runs, and summarize the result.")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "Installed demo-tool and verified hello-from-demo-tool." {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected three model requests, got %d", len(client.requests))
	}
	if got, wantMax := len(client.requests[0].Messages), len(history)+1; got >= wantMax {
		t.Fatalf("expected first request to compact long history, got %d messages from %d-history seed", got, len(history))
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "demo-tool")); err != nil {
		t.Fatalf("expected installed binary: %v", err)
	}
	foundVerification := false
	for _, msg := range session.Messages() {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "hello-from-demo-tool") {
			foundVerification = true
			break
		}
	}
	if !foundVerification {
		t.Fatalf("expected verification command output in session messages")
	}
}

func TestRunTaskCompactsOversizedToolOutputBeforeNextRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell commands are bash-specific")
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
										Name:      "run_command",
										Arguments: `{"command":"for i in $(seq 1 700); do printf 'row=%04d payload=abcdefghij\\n' \"$i\"; done; printf 'TAIL=omega\\n'","timeout_seconds":30}`,
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
						Message: model.Message{Content: "summarized"},
					},
				},
			},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", 30*time.Second, nil), 32768, nil)
	result, err := a.NewSession().RunTask(context.Background(), "Collect the command output and summarize it.")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "summarized" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.requests))
	}

	var toolText string
	for _, msg := range client.requests[1].Messages {
		if msg.Role == "tool" {
			toolText = model.ContentString(msg.Content)
		}
	}
	if toolText == "" {
		t.Fatalf("expected compacted tool output in second request")
	}
	if !strings.Contains(toolText, "[tool output truncated:") && !strings.Contains(toolText, "[output truncated; full log:") {
		t.Fatalf("expected truncation marker, got %q", toolText)
	}
	if !strings.Contains(toolText, "TAIL=omega") {
		t.Fatalf("expected tail fact to survive compaction, got %q", toolText)
	}
	if len(toolText) > maxToolMessageChars+len("\n\n"+postToolAnswerReminder)+200 {
		t.Fatalf("expected compacted tool text to stay bounded, got %d chars", len(toolText))
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

func TestRunTaskStopsAfterRepeatedRunCommandFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell error text is bash-specific")
	}
	client := &scriptedChatClient{
		responses: []model.Response{
			{Choices: []model.Choice{{Message: model.Message{
				ToolCalls: []model.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "run_command",
							Arguments: `{"command":"false"}`,
						},
					},
					{
						ID:   "call-2",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "run_command",
							Arguments: `{"command":"false"}`,
						},
					},
					{
						ID:   "call-3",
						Type: "function",
						Function: model.ToolFunction{
							Name:      "run_command",
							Arguments: `{"command":"false"}`,
						},
					},
				},
			}}}},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", 5*time.Second, nil), 32768, nil)
	session := a.NewSession()
	result, err := session.RunTask(context.Background(), "Try the install commands even if they keep failing.")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if !strings.Contains(result.Final, "repeated tool failures") {
		t.Fatalf("expected repeated tool failure stop, got %q", result.Final)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected stop before a second model request, got %d", len(client.requests))
	}
	var toolErrors int
	for _, msg := range session.Messages() {
		if msg.Role != "tool" {
			continue
		}
		text := strings.ToLower(model.ContentString(msg.Content))
		if strings.Contains(text, "tool error:") && strings.Contains(text, "command failed") {
			toolErrors++
		}
	}
	if toolErrors != 3 {
		t.Fatalf("expected three failing command tool messages, got %d", toolErrors)
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

func TestIsContextLengthErrorMatchesProviderSpecificLimits(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{err: fmt.Errorf("model API returned 413 Request Entity Too Large: {\"error\":{\"code\":\"tokens_limit_reached\"}}"), want: true},
		{err: fmt.Errorf("request body too large for gpt-4o model. max size: 8000 tokens"), want: true},
		{err: fmt.Errorf("context length exceeded"), want: true},
		{err: fmt.Errorf("model API returned 429 Too Many Requests"), want: false},
	}

	for _, tt := range cases {
		got := isContextLengthError(tt.err)
		if got != tt.want {
			t.Fatalf("isContextLengthError(%q) = %v, want %v", tt.err.Error(), got, tt.want)
		}
	}
}

func TestRunTaskCompactsAndRetriesOnProvider413(t *testing.T) {
	client := &scriptedChatClient{
		errors: []error{
			fmt.Errorf("model API returned 413 Request Entity Too Large: {\"error\":{\"code\":\"tokens_limit_reached\"}}"),
		},
		responses: []model.Response{
			{},
			{Choices: []model.Choice{{Message: model.Message{Content: "done after compaction"}}}},
		},
	}

	a := New(client, tools.NewRegistry(".", "bash", time.Second, nil), 900, nil)
	session := a.NewSession()
	history := session.Messages()
	for i := 0; i < 10; i++ {
		history = append(history,
			model.Message{Role: "user", Content: strings.Repeat(fmt.Sprintf("old user %d ", i), 40)},
			model.Message{Role: "assistant", Content: strings.Repeat(fmt.Sprintf("old answer %d ", i), 40)},
		)
	}
	session.ReplaceMessages(history)

	result, err := session.RunTask(context.Background(), "finish now")
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if result.Final != "done after compaction" {
		t.Fatalf("unexpected final: %q", result.Final)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected retry after context compaction, got %d requests", len(client.requests))
	}
	firstSize := conversationSize(client.requests[0].Messages)
	secondSize := conversationSize(client.requests[1].Messages)
	if secondSize >= firstSize {
		t.Fatalf("expected compacted retry request to be smaller: first=%d second=%d", firstSize, secondSize)
	}
}
