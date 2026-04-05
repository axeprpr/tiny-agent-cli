package transport

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"tiny-agent-cli/internal/agent"
)

const (
	OutputRaw        = "raw"
	OutputTerminal   = "terminal"
	OutputJSON       = "json"
	OutputJSONL      = "jsonl"
	OutputStructured = "structured"
)

type Event struct {
	Type string         `json:"type"`
	Time time.Time      `json:"time"`
	Data map[string]any `json:"data,omitempty"`
}

type Writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewStructuredWriter(out io.Writer) *Writer {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return &Writer{enc: enc}
}

func NormalizeOutputMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", OutputRaw:
		return OutputRaw
	case OutputTerminal:
		return OutputTerminal
	case OutputJSON:
		return OutputJSON
	case OutputJSONL:
		return OutputJSONL
	case OutputStructured:
		return OutputStructured
	default:
		return ""
	}
}

func IsStructuredMode(mode string) bool {
	switch NormalizeOutputMode(mode) {
	case OutputJSON, OutputJSONL, OutputStructured:
		return true
	default:
		return false
	}
}

func IsStreamingMode(mode string) bool {
	switch NormalizeOutputMode(mode) {
	case OutputJSONL, OutputStructured:
		return true
	default:
		return false
	}
}

func (w *Writer) Emit(eventType string, data map[string]any) error {
	if w == nil || w.enc == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(Event{
		Type: strings.TrimSpace(eventType),
		Time: time.Now().UTC(),
		Data: cloneMap(data),
	})
}

func (w *Writer) EmitToken(token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return w.Emit("token", map[string]any{"text": token})
}

func (w *Writer) EmitResult(final string, steps int) error {
	return w.Emit("result", map[string]any{
		"final": final,
		"steps": steps,
	})
}

func (w *Writer) EmitError(err error) error {
	if err == nil {
		return nil
	}
	return w.Emit("error", map[string]any{"message": err.Error()})
}

type AgentEventSink struct {
	Writer *Writer
}

func (s AgentEventSink) RecordAgentEvent(_ context.Context, event agent.AgentEvent) {
	if s.Writer == nil {
		return
	}
	_ = s.Writer.Emit(event.Type, event.Data)
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
