package transport

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeOutputMode(t *testing.T) {
	tests := map[string]string{
		"":           OutputRaw,
		"raw":        OutputRaw,
		"terminal":   OutputTerminal,
		"json":       OutputJSON,
		"jsonl":      OutputJSONL,
		"structured": OutputStructured,
		"weird":      "",
	}
	for input, want := range tests {
		if got := NormalizeOutputMode(input); got != want {
			t.Fatalf("NormalizeOutputMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWriterEmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	writer := NewStructuredWriter(&buf)
	if err := writer.EmitToken("hello"); err != nil {
		t.Fatalf("EmitToken: %v", err)
	}
	if err := writer.EmitResult("done", 3); err != nil {
		t.Fatalf("EmitResult: %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var event Event
	if err := json.Unmarshal(lines[0], &event); err != nil {
		t.Fatalf("unmarshal first event: %v", err)
	}
	if event.Type != "token" {
		t.Fatalf("unexpected event type: %q", event.Type)
	}
	if got := event.Data["text"]; got != "hello" {
		t.Fatalf("unexpected token text: %#v", got)
	}
}
