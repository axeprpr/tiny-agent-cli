package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/model"
)

func TestExtractStableMemoryNotes(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "My preference is short Chinese answers.\nThis repo uses Go."},
		{Role: "assistant", Content: "I will keep that in mind."},
		{Role: "user", Content: "Please debug this failing test."},
	}

	got := extractStableMemoryNotes(messages)
	want := []string{
		"Prefer short Chinese answers",
		"This repo uses Go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected notes: %#v", got)
	}
}

func TestExtractStableMemoryNotesSkipsTransientRequests(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Please inspect this bug and show me the stack trace."},
		{Role: "user", Content: "Summarize the current logs in plain text."},
	}

	got := extractStableMemoryNotes(messages)
	if len(got) != 0 {
		t.Fatalf("expected no notes, got %#v", got)
	}
}

func TestBuildConversationSummaryInputKeepsRecentMessages(t *testing.T) {
	var messages []model.Message
	for i := 0; i < memorySummaryMaxMessages+6; i++ {
		messages = append(messages, model.Message{
			Role:    "user",
			Content: fmt.Sprintf("message-%02d", i),
		})
	}

	got := buildConversationSummaryInput(messages)
	if strings.Contains(got, "message-00") {
		t.Fatalf("expected oldest message to be trimmed: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("message-%02d", memorySummaryMaxMessages+5)) {
		t.Fatalf("expected newest message to be kept: %q", got)
	}
	if count := strings.Count(got, "user: message-"); count != memorySummaryMaxMessages {
		t.Fatalf("expected %d recent messages, got %d", memorySummaryMaxMessages, count)
	}
}

func TestBuildConversationSummaryInputTruncatesLargeEntries(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: strings.Repeat("alpha ", 120)},
		{Role: "assistant", Content: strings.Repeat("beta ", 120)},
	}

	got := buildConversationSummaryInput(messages)
	if len(got) >= len(model.ContentString(messages[0].Content))+len(model.ContentString(messages[1].Content)) {
		t.Fatalf("expected summary input to truncate large entries, got length %d", len(got))
	}
	if !strings.Contains(got, "user: alpha") {
		t.Fatalf("expected user entry to remain recognizable, got %q", got)
	}
	if !strings.Contains(got, "assistant: beta") {
		t.Fatalf("expected assistant entry to remain recognizable, got %q", got)
	}
}

func TestPeelGlobalDangerously(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		want   []string
		danger bool
	}{
		{name: "short flag", args: []string{"-d", "inspect repo"}, want: []string{"inspect repo"}, danger: true},
		{name: "long flag", args: []string{"--dangerously", "run", "go test"}, want: []string{"run", "go test"}, danger: true},
		{name: "single dash long flag", args: []string{"-dangerously", "chat"}, want: []string{"chat"}, danger: true},
		{name: "no flag", args: []string{"chat"}, want: []string{"chat"}, danger: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, gotDanger := peelGlobalDangerously(tt.args)
			if gotDanger != tt.danger {
				t.Fatalf("danger mismatch: got %v want %v", gotDanger, tt.danger)
			}
			if !reflect.DeepEqual(gotArgs, tt.want) {
				t.Fatalf("args mismatch: got %#v want %#v", gotArgs, tt.want)
			}
		})
	}
}

func TestWithDangerouslyFlag(t *testing.T) {
	got := withDangerouslyFlag([]string{"chat"}, true)
	want := []string{"--dangerously", "chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestResolveChatSessionName(t *testing.T) {
	now := time.Date(2026, time.March, 20, 14, 5, 6, 0, time.FixedZone("CST", 8*3600))

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "blank creates new session", input: "", want: "chat-20260320-140506"},
		{name: "new creates new session", input: "new", want: "chat-20260320-140506"},
		{name: "explicit session kept", input: "bugfix", want: "bugfix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveChatSessionName(tt.input, now); got != tt.want {
				t.Fatalf("unexpected session: got %q want %q", got, tt.want)
			}
		})
	}
}
