package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type fileAuditSink struct {
	path string
	mu   sync.Mutex
}

type ToolAuditStats struct {
	Total      int
	ByStatus   map[string]int
	ByTool     map[string]int
	LastTool   string
	LastStatus string
	LastError  string
	LastAt     string
}

type fanoutAuditSink struct {
	sinks []ToolAuditSink
}

func AuditPath(stateDir string) string {
	return filepath.Join(stateDir, "audit", "tool-events.jsonl")
}

func NewFileAuditSink(path string) ToolAuditSink {
	path = filepath.Clean(path)
	if path == "" {
		return nil
	}
	return &fileAuditSink{path: path}
}

func NewFanoutAuditSink(sinks ...ToolAuditSink) ToolAuditSink {
	out := make([]ToolAuditSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			out = append(out, sink)
		}
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return out[0]
	}
	return &fanoutAuditSink{sinks: out}
}

func (s *fileAuditSink) RecordToolEvent(_ context.Context, event ToolAuditEvent) {
	if s == nil || s.path == "" {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func (s *fanoutAuditSink) RecordToolEvent(ctx context.Context, event ToolAuditEvent) {
	if s == nil {
		return
	}
	for _, sink := range s.sinks {
		if sink != nil {
			sink.RecordToolEvent(ctx, event)
		}
	}
}

func ReadAuditTail(path string, limit int) ([]ToolAuditEvent, error) {
	if limit <= 0 {
		limit = 10
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var events []ToolAuditEvent
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event ToolAuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func ComputeAuditStats(events []ToolAuditEvent) ToolAuditStats {
	stats := ToolAuditStats{
		ByStatus: make(map[string]int),
		ByTool:   make(map[string]int),
	}
	if len(events) == 0 {
		return stats
	}
	stats.Total = len(events)
	for _, event := range events {
		stats.ByStatus[event.Status]++
		stats.ByTool[event.Tool]++
	}
	last := events[len(events)-1]
	stats.LastTool = last.Tool
	stats.LastStatus = last.Status
	stats.LastError = last.Error
	if !last.Time.IsZero() {
		stats.LastAt = last.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return stats
}

func FormatAuditStats(stats ToolAuditStats, topTools int) string {
	if stats.Total == 0 {
		return "audit=0"
	}
	if topTools <= 0 {
		topTools = 3
	}
	parts := []string{
		fmt.Sprintf("audit=%d", stats.Total),
		fmt.Sprintf("ok=%d", stats.ByStatus["ok"]),
		fmt.Sprintf("errors=%d", stats.Total-stats.ByStatus["ok"]),
	}
	if stats.LastTool != "" {
		parts = append(parts, "last="+stats.LastTool+":"+stats.LastStatus)
	}
	if stats.LastAt != "" {
		parts = append(parts, "at="+stats.LastAt)
	}
	top := topToolPairs(stats.ByTool, topTools)
	if len(top) > 0 {
		parts = append(parts, "top="+strings.Join(top, ","))
	}
	return strings.Join(parts, " ")
}

func topToolPairs(counts map[string]int, limit int) []string {
	type pair struct {
		name  string
		count int
	}
	var pairs []pair
	for name, count := range counts {
		if strings.TrimSpace(name) == "" || count <= 0 {
			continue
		}
		pairs = append(pairs, pair{name: name, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, fmt.Sprintf("%s:%d", p.name, p.count))
	}
	return out
}
