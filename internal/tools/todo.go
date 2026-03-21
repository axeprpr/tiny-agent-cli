package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"tiny-agent-cli/internal/model"
)

type todoStore struct {
	mu    sync.Mutex
	items []TodoItem
}

type TodoItem struct {
	Text   string
	Status string
}

type updateTodoTool struct {
	store *todoStore
}

type showTodoTool struct {
	store *todoStore
}

func newTodoStore() *todoStore {
	return &todoStore{}
}

func newUpdateTodoTool(store *todoStore) Tool {
	return &updateTodoTool{store: store}
}

func newShowTodoTool(store *todoStore) Tool {
	return &showTodoTool{store: store}
}

func (t *updateTodoTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "update_todo",
			Description: "Create or replace the current task checklist for this conversation. Use it for multi-step work, repository exploration, or edits that need explicit progress tracking.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"items"},
				"properties": map[string]any{
					"items": map[string]any{
						"type":        "array",
						"description": "Ordered task list. Keep it short and concrete.",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"text", "status"},
							"properties": map[string]any{
								"text": map[string]any{
									"type":        "string",
									"description": "Concrete task step",
								},
								"status": map[string]any{
									"type":        "string",
									"description": "One of pending, in_progress, completed",
									"enum":        []string{"pending", "in_progress", "completed"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (t *updateTodoTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Items []TodoItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if len(args.Items) == 0 {
		return "", fmt.Errorf("items must not be empty")
	}

	if err := t.store.Replace(args.Items); err != nil {
		return "", err
	}
	return formatTodoItems(t.store.Items()), nil
}

func (t *showTodoTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "show_todo",
			Description: "Show the current task checklist for this conversation.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *showTodoTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	t.store.mu.Lock()
	items := append([]TodoItem(nil), t.store.items...)
	t.store.mu.Unlock()
	if len(items) == 0 {
		return "(no todo items)", nil
	}
	return formatTodoItems(items), nil
}

func normalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "pending"
	case "in_progress":
		return "in_progress"
	case "completed":
		return "completed"
	default:
		return ""
	}
}

func formatTodoItems(items []TodoItem) string {
	lines := make([]string, 0, len(items))
	for i, item := range items {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, todoStatusLabel(item.Status), item.Text))
	}
	return strings.Join(lines, "\n")
}

func (s *todoStore) Items() []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TodoItem(nil), s.items...)
}

func (s *todoStore) Replace(items []TodoItem) error {
	normalized := make([]TodoItem, 0, len(items))
	inProgress := 0
	for _, item := range items {
		text := strings.Join(strings.Fields(strings.TrimSpace(item.Text)), " ")
		status := normalizeTodoStatus(item.Status)
		if text == "" {
			return fmt.Errorf("todo item text must not be empty")
		}
		if status == "" {
			return fmt.Errorf("invalid todo status %q", item.Status)
		}
		if status == "in_progress" {
			inProgress++
		}
		normalized = append(normalized, TodoItem{Text: text, Status: status})
	}
	if inProgress > 1 {
		return fmt.Errorf("only one todo item can be in_progress")
	}
	s.mu.Lock()
	s.items = normalized
	s.mu.Unlock()
	return nil
}

func todoStatusLabel(status string) string {
	switch status {
	case "completed":
		return "done"
	case "in_progress":
		return "doing"
	default:
		return "todo"
	}
}
