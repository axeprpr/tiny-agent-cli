package agent

import "testing"

func TestSanitizeFinalRemovesThinkTags(t *testing.T) {
	input := "<think>\nprivate reasoning\n</think>\n\nHello.\n"
	got := SanitizeFinal(input)
	if got != "Hello." {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalRemovesDanglingTag(t *testing.T) {
	input := "Answer first\n</think>\nsecond line"
	got := SanitizeFinal(input)
	if got != "Answer first\nsecond line" {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalPrefersOutputMarker(t *testing.T) {
	input := "Thinking Process:\n1. do stuff\nOutput: pong"
	got := SanitizeFinal(input)
	if got != "pong" {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalTrimsReasoningPrefixBeforeDirectAnswer(t *testing.T) {
	input := "The list_files function returned entries.\nLet me count them:\n1. a\n2. b\nThere are 2 entries.\n- a\n- b"
	got := SanitizeFinal(input)
	if got != "There are 2 entries.\n- a\n- b" {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalNormalizesMarkdownTable(t *testing.T) {
	input := "环境信息统计\n| 项目 | 信息 |\n|------|------|\n| 操作系统 | Ubuntu |\n| 架构 | x86_64 |"
	got := SanitizeFinal(input)
	want := "环境信息统计\n- 操作系统: Ubuntu\n- 架构: x86_64"
	if got != want {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalTrimsLeadIn(t *testing.T) {
	input := "好的，我已经整理好了。\n环境信息统计\n- 操作系统: Ubuntu"
	got := SanitizeFinal(input)
	want := "环境信息统计\n- 操作系统: Ubuntu"
	if got != want {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeFinalTrimsRestartHeading(t *testing.T) {
	input := "系统信息：\n- 操作系统：Ubuntu\n- 架构：x86_64\n让我输出一个简洁的总结。\n环境信息统计\n- 操作系统: Ubuntu"
	got := SanitizeFinal(input)
	want := "环境信息统计\n- 操作系统: Ubuntu"
	if got != want {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}
