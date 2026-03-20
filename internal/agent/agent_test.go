package agent

import (
	"strings"
	"testing"

	"onek-agent/internal/model"
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

func TestTruncateToolMessageShrinksLargeOutput(t *testing.T) {
	got := truncateToolMessage(strings.Repeat("B", 5000), 900)
	if len(got) >= 5000 {
		t.Fatalf("expected truncated output, got length %d", len(got))
	}
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}
