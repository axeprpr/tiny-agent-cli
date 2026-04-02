package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Time    time.Time      `json:"time"`
	Session string         `json:"session,omitempty"`
	Scope   string         `json:"scope,omitempty"`
	Source  string         `json:"source,omitempty"`
	Type    string         `json:"type"`
	Data    map[string]any `json:"data,omitempty"`
}

type FileSink struct {
	path string
	mu   sync.Mutex
}

func NewFileSink(path string) *FileSink {
	return &FileSink{path: strings.TrimSpace(path)}
}

func (s *FileSink) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *FileSink) Record(_ context.Context, event Event) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if strings.TrimSpace(event.Type) == "" {
		return nil
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func Path(stateDir, sessionName string) string {
	name := sanitizeSessionName(sessionName)
	if name == "" {
		name = "default"
	}
	return filepath.Join(stateDir, "trace-"+name+".jsonl")
}

func ReadTail(path string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	buf := make([]Event, 0, limit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if len(buf) == limit {
			copy(buf, buf[1:])
			buf[len(buf)-1] = event
		} else {
			buf = append(buf, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return buf, nil
}

func CountByType(events []Event) map[string]int {
	out := map[string]int{}
	for _, event := range events {
		key := strings.TrimSpace(event.Type)
		if key == "" {
			key = "unknown"
		}
		out[key]++
	}
	return out
}

func sanitizeSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
