package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileAuditSinkWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit", "tool-events.jsonl")
	sink := NewFileAuditSink(path)
	if sink == nil {
		t.Fatalf("expected sink")
	}
	sink.RecordToolEvent(context.Background(), ToolAuditEvent{
		Time:         time.Now(),
		Tool:         "read_file",
		Status:       "ok",
		DurationMs:   12,
		ArgsPreview:  `{"path":"README.md"}`,
		OutputSample: "hello",
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"tool":"read_file"`) {
		t.Fatalf("unexpected audit payload: %s", text)
	}
}

func TestReadAuditTailAndStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit", "tool-events.jsonl")
	sink := NewFileAuditSink(path)
	now := time.Now()
	sink.RecordToolEvent(context.Background(), ToolAuditEvent{
		Time:   now.Add(-2 * time.Second),
		Tool:   "read_file",
		Status: "ok",
	})
	sink.RecordToolEvent(context.Background(), ToolAuditEvent{
		Time:   now.Add(-1 * time.Second),
		Tool:   "run_command",
		Status: "error",
		Error:  "command failed",
	})
	sink.RecordToolEvent(context.Background(), ToolAuditEvent{
		Time:   now,
		Tool:   "read_file",
		Status: "ok",
	})

	events, err := ReadAuditTail(path, 2)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Tool != "run_command" || events[1].Tool != "read_file" {
		t.Fatalf("unexpected tail order: %#v", events)
	}

	stats := ComputeAuditStats(events)
	if stats.Total != 2 {
		t.Fatalf("unexpected total: %d", stats.Total)
	}
	text := FormatAuditStats(stats, 2)
	if !strings.Contains(text, "audit=2") {
		t.Fatalf("unexpected summary: %q", text)
	}
	if !strings.Contains(text, "errors=1") {
		t.Fatalf("unexpected summary: %q", text)
	}
}
