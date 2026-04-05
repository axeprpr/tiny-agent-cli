package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tasks"
)

type createTaskTool struct {
	store *tasks.Store
}

type listTasksTool struct {
	store *tasks.Store
}

type updateTaskTool struct {
	store *tasks.Store
}

type deleteTaskTool struct {
	store *tasks.Store
}

func newCreateTaskTool(store *tasks.Store) Tool {
	return &createTaskTool{store: store}
}

func newListTasksTool(store *tasks.Store) Tool {
	return &listTasksTool{store: store}
}

func newUpdateTaskTool(store *tasks.Store) Tool {
	return &updateTaskTool{store: store}
}

func newDeleteTaskTool(store *tasks.Store) Tool {
	return &deleteTaskTool{store: store}
}

func (t *createTaskTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "create_task",
			Description: "Create a persistent project task for work that should survive beyond the current conversation.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"title"},
				"properties": map[string]any{
					"title": map[string]any{"type": "string", "description": "Short task title"},
					"details": map[string]any{"type": "string", "description": "Optional extra task details"},
				},
			},
		},
	}
}

func (t *createTaskTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Title   string `json:"title"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	item, err := t.store.Create(args.Title, args.Details)
	if err != nil {
		return "", err
	}
	return tasks.Format([]tasks.Item{item}), nil
}

func (t *listTasksTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "list_tasks",
			Description: "List persistent project tasks.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *listTasksTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	return tasks.Format(t.store.List()), nil
}

func (t *updateTaskTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "update_task",
			Description: "Update a persistent project task.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task id"},
					"title": map[string]any{"type": "string", "description": "Optional replacement title"},
					"status": map[string]any{
						"type":        "string",
						"description": "Optional replacement status",
						"enum":        []string{tasks.StatusPending, tasks.StatusInProgress, tasks.StatusCompleted, tasks.StatusCanceled},
					},
					"details": map[string]any{"type": "string", "description": "Optional replacement details"},
				},
			},
		},
	}
}

func (t *updateTaskTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		ID      string  `json:"id"`
		Title   *string `json:"title"`
		Status  *string `json:"status"`
		Details *string `json:"details"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	item, err := t.store.Update(args.ID, tasks.Update{
		Title:   args.Title,
		Status:  args.Status,
		Details: args.Details,
	})
	if err != nil {
		return "", err
	}
	return tasks.Format([]tasks.Item{item}), nil
}

func (t *deleteTaskTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "delete_task",
			Description: "Delete a persistent project task.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task id"},
				},
			},
		},
	}
}

func (t *deleteTaskTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if err := t.store.Delete(args.ID); err != nil {
		return "", err
	}
	return "deleted " + args.ID, nil
}
