package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusCanceled   = "canceled"
)

type Item struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Details   string    `json:"details,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Update struct {
	Title   *string
	Status  *string
	Details *string
}

type Store struct {
	mu     sync.Mutex
	path   string
	nextID int
	items  []Item
}

type fileState struct {
	Version int    `json:"version"`
	NextID  int    `json:"next_id"`
	Items   []Item `json:"items"`
}

func New(path string) *Store {
	store := &Store{
		path:   strings.TrimSpace(path),
		nextID: 1,
	}
	_ = store.load()
	return store
}

func (s *Store) List() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Item(nil), s.items...)
	slices.SortFunc(out, func(a, b Item) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(a.ID, b.ID)
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		return 1
	})
	return out
}

func (s *Store) Create(title, details string) (Item, error) {
	title = cleanText(title)
	details = strings.TrimSpace(details)
	if title == "" {
		return Item{}, fmt.Errorf("task title must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	item := Item{
		ID:        fmt.Sprintf("task-%03d", s.nextID),
		Title:     title,
		Status:    StatusPending,
		Details:   details,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.nextID++
	s.items = append(s.items, item)
	if err := s.saveLocked(); err != nil {
		return Item{}, err
	}
	return item, nil
}

func (s *Store) Update(id string, update Update) (Item, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Item{}, fmt.Errorf("task id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		if update.Title != nil {
			title := cleanText(*update.Title)
			if title == "" {
				return Item{}, fmt.Errorf("task title must not be empty")
			}
			s.items[i].Title = title
		}
		if update.Status != nil {
			status := NormalizeStatus(*update.Status)
			if status == "" {
				return Item{}, fmt.Errorf("invalid task status %q", *update.Status)
			}
			s.items[i].Status = status
		}
		if update.Details != nil {
			s.items[i].Details = strings.TrimSpace(*update.Details)
		}
		s.items[i].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(); err != nil {
			return Item{}, err
		}
		return s.items[i], nil
	}
	return Item{}, fmt.Errorf("unknown task %q", id)
}

func (s *Store) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("task id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		s.items = append(s.items[:i], s.items[i+1:]...)
		return s.saveLocked()
	}
	return fmt.Errorf("unknown task %q", id)
}

func NormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusPending:
		return StatusPending
	case StatusInProgress:
		return StatusInProgress
	case StatusCompleted:
		return StatusCompleted
	case StatusCanceled:
		return StatusCanceled
	default:
		return ""
	}
}

func Format(items []Item) string {
	if len(items) == 0 {
		return "no tasks"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := fmt.Sprintf("%s [%s] %s", item.ID, item.Status, item.Title)
		if strings.TrimSpace(item.Details) != "" {
			line += " :: " + cleanDetails(item.Details)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (s *Store) load() error {
	if s == nil || s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = max(1, state.NextID)
	s.items = nil
	for _, item := range state.Items {
		if strings.TrimSpace(item.ID) == "" || cleanText(item.Title) == "" || NormalizeStatus(item.Status) == "" {
			continue
		}
		item.Title = cleanText(item.Title)
		item.Status = NormalizeStatus(item.Status)
		item.Details = strings.TrimSpace(item.Details)
		s.items = append(s.items, item)
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s == nil || s.path == "" {
		return nil
	}
	state := fileState{
		Version: 1,
		NextID:  s.nextID,
		Items:   append([]Item(nil), s.items...),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cleanText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func cleanDetails(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	return strings.Join(strings.Fields(text), " ")
}
