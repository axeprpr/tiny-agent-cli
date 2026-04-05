package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type SubagentSessionState struct {
	MessageCount int       `json:"message_count,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type SubagentSnapshot struct {
	ID         string               `json:"id"`
	Status     string               `json:"status"`
	Role       string               `json:"role,omitempty"`
	Model      string               `json:"model,omitempty"`
	TaskCount  int                  `json:"task_count,omitempty"`
	Queued     int                  `json:"queued,omitempty"`
	LastPrompt string               `json:"last_prompt,omitempty"`
	LastOutput string               `json:"last_output,omitempty"`
	LastError  string               `json:"last_error,omitempty"`
	LogTail    string               `json:"log_tail,omitempty"`
	Session    SubagentSessionState `json:"session,omitempty"`
	CreatedAt  time.Time            `json:"created_at,omitempty"`
	StartedAt  time.Time            `json:"started_at,omitempty"`
	UpdatedAt  time.Time            `json:"updated_at,omitempty"`
	FinishedAt time.Time            `json:"finished_at,omitempty"`
}

type OrchestrationRegistry struct {
	mu        sync.RWMutex
	snapshots map[string]SubagentSnapshot
	cancels   map[string]context.CancelFunc
}

func NewOrchestrationRegistry() *OrchestrationRegistry {
	return &OrchestrationRegistry{
		snapshots: make(map[string]SubagentSnapshot),
		cancels:   make(map[string]context.CancelFunc),
	}
}

func (r *OrchestrationRegistry) Register(snapshot SubagentSnapshot, cancel context.CancelFunc) {
	if r == nil {
		return
	}
	id := strings.TrimSpace(snapshot.ID)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots[id] = snapshot
	if cancel != nil {
		r.cancels[id] = cancel
	}
}

func (r *OrchestrationRegistry) Update(snapshot SubagentSnapshot) {
	if r == nil {
		return
	}
	id := strings.TrimSpace(snapshot.ID)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.snapshots[id]; !ok {
		return
	}
	r.snapshots[id] = snapshot
}

func (r *OrchestrationRegistry) Cancel(id string) bool {
	if r == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	r.mu.Lock()
	cancel := r.cancels[id]
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *OrchestrationRegistry) Snapshot(id string) (SubagentSnapshot, bool) {
	if r == nil {
		return SubagentSnapshot{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot, ok := r.snapshots[strings.TrimSpace(id)]
	return snapshot, ok
}

func (r *OrchestrationRegistry) List() []SubagentSnapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SubagentSnapshot, 0, len(r.snapshots))
	for _, snapshot := range r.snapshots {
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (r *OrchestrationRegistry) Restore(snapshots []SubagentSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots = make(map[string]SubagentSnapshot, len(snapshots))
	r.cancels = make(map[string]context.CancelFunc)
	for _, snapshot := range snapshots {
		id := strings.TrimSpace(snapshot.ID)
		if id == "" {
			continue
		}
		r.snapshots[id] = snapshot
	}
}

func SnapshotSession(session *Session) SubagentSessionState {
	if session == nil {
		return SubagentSessionState{}
	}
	return SubagentSessionState{
		MessageCount: len(session.Messages()),
		UpdatedAt:    time.Now(),
	}
}
