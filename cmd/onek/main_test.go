package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"onek-agent/internal/model"
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
