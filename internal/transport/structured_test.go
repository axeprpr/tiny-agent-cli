package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
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

func TestAgentEventSinkWritesStructuredAgentPayloads(t *testing.T) {
	var buf bytes.Buffer
	writer := NewStructuredWriter(&buf)
	sink := AgentEventSink{Writer: writer}

	sink.RecordAgentEvent(context.Background(), agent.AgentEvent{
		Type: "turn_summary",
		Data: map[string]any{
			"turn":     1,
			"decision": "finish",
			"assistant": model.Message{
				Role:    "assistant",
				Content: "done",
			},
			"tool_results": []model.Message{
				{
					Role:       "tool",
					ToolCallID: "call-1",
					Content:    "tool result",
				},
			},
			"permission": tools.PermissionDecision{
				Allowed: true,
				Mode:    tools.PermissionModeAllow,
			},
		},
	})

	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if event.Type != "turn_summary" {
		t.Fatalf("unexpected event type: %q", event.Type)
	}
	if got := event.Data["decision"]; got != "finish" {
		t.Fatalf("unexpected decision: %#v", got)
	}
	assistant, ok := event.Data["assistant"].(map[string]any)
	if !ok || assistant["role"] != "assistant" || assistant["content"] != "done" {
		t.Fatalf("unexpected assistant payload: %#v", event.Data["assistant"])
	}
	permission, ok := event.Data["permission"].(map[string]any)
	if !ok || permission["allowed"] != true || permission["mode"] != tools.PermissionModeAllow {
		t.Fatalf("unexpected permission payload: %#v", event.Data["permission"])
	}
}
