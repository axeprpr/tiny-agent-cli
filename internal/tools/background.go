package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiny-agent-cli/internal/model"
)

type JobControl interface {
	Start(task string) (string, error)
	StartWithRole(role, task string) (string, error)
	Send(id, task string) error
	Cancel(id string) error
	List() []BackgroundJobSnapshot
	Snapshot(id string) (BackgroundJobSnapshot, bool)
}

type BackgroundJobSnapshot struct {
	ID         string
	Status     string
	Role       string
	Model      string
	TaskCount  int
	Queued     int
	LastPrompt string
	LastOutput string
	LastError  string
	LogTail    string
}

type startBackgroundJobTool struct {
	jobs JobControl
}

type listBackgroundJobsTool struct {
	jobs JobControl
}

type inspectBackgroundJobTool struct {
	jobs JobControl
}

type sendBackgroundJobTool struct {
	jobs JobControl
}

type delegateSubagentTool struct {
	jobs JobControl
}

func newStartBackgroundJobTool(jobs JobControl) Tool {
	return &startBackgroundJobTool{jobs: jobs}
}

func newListBackgroundJobsTool(jobs JobControl) Tool {
	return &listBackgroundJobsTool{jobs: jobs}
}

func newInspectBackgroundJobTool(jobs JobControl) Tool {
	return &inspectBackgroundJobTool{jobs: jobs}
}

func newSendBackgroundJobTool(jobs JobControl) Tool {
	return &sendBackgroundJobTool{jobs: jobs}
}

func newDelegateSubagentTool(jobs JobControl) Tool {
	return &delegateSubagentTool{jobs: jobs}
}

func (t *startBackgroundJobTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "start_background_job",
			Description: "Start a background subtask in a separate agent session. Use this for longer exploration or verification work you want to continue in parallel.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"task"},
				"properties": map[string]any{
					"role": map[string]any{
						"type":        "string",
						"description": "Optional role: general|explore|plan|implement|verify",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Subtask instructions for the background agent",
					},
				},
			},
		},
	}
}

func (t *startBackgroundJobTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Role string `json:"role"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	id, err := t.jobs.StartWithRole(args.Role, args.Task)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("started background job %s", id), nil
}

func (t *listBackgroundJobsTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "list_background_jobs",
			Description: "List current background jobs and their status summaries.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *listBackgroundJobsTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	snaps := t.jobs.List()
	if len(snaps) == 0 {
		return "no background jobs", nil
	}
	lines := make([]string, 0, len(snaps))
	for _, snap := range snaps {
		line := fmt.Sprintf("%s status=%s tasks=%d", snap.ID, snap.Status, snap.TaskCount)
		if strings.TrimSpace(snap.Role) != "" {
			line += " role=" + snap.Role
		}
		if snap.Queued > 0 {
			line += fmt.Sprintf(" queued=%d", snap.Queued)
		}
		if strings.TrimSpace(snap.LastOutput) != "" {
			line += " result=" + compactPreviewString(SingleLineText(snap.LastOutput), 120)
		} else if strings.TrimSpace(snap.LastError) != "" {
			line += " error=" + compactPreviewString(SingleLineText(snap.LastError), 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (t *inspectBackgroundJobTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "inspect_background_job",
			Description: "Inspect one background job in detail, including latest result and recent activity.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Background job ID",
					},
				},
			},
		},
	}
}

func (t *inspectBackgroundJobTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	snap, ok := t.jobs.Snapshot(args.ID)
	if !ok {
		return "", fmt.Errorf("unknown background job %q", args.ID)
	}
	lines := []string{
		"id=" + snap.ID,
		"status=" + snap.Status,
		"role=" + snap.Role,
		fmt.Sprintf("tasks=%d", snap.TaskCount),
		fmt.Sprintf("queued=%d", snap.Queued),
	}
	if strings.TrimSpace(snap.LastPrompt) != "" {
		lines = append(lines, "last_prompt="+SingleLineText(snap.LastPrompt))
	}
	if strings.TrimSpace(snap.LastOutput) != "" {
		lines = append(lines, "last_output="+SingleLineText(snap.LastOutput))
	}
	if strings.TrimSpace(snap.LastError) != "" {
		lines = append(lines, "last_error="+SingleLineText(snap.LastError))
	}
	if strings.TrimSpace(snap.LogTail) != "" {
		lines = append(lines, "log_tail="+SingleLineText(snap.LogTail))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *sendBackgroundJobTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "send_background_job",
			Description: "Send a follow-up message to an existing background job so it can continue with updated instructions.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"id", "task"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Background job ID",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Follow-up instructions for that background job",
					},
				},
			},
		},
	}
}

func (t *sendBackgroundJobTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		ID   string `json:"id"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if err := t.jobs.Send(args.ID, args.Task); err != nil {
		return "", err
	}
	return fmt.Sprintf("queued follow-up for %s", args.ID), nil
}

func (t *delegateSubagentTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "delegate_subagent",
			Description: "Delegate a bounded subtask to a background subagent and continue foreground work.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"task"},
				"properties": map[string]any{
					"role": map[string]any{
						"type":        "string",
						"description": "Optional role: general|explore|plan|implement|verify",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Concrete subtask instructions for the delegated subagent",
					},
				},
			},
		},
	}
}

func (t *delegateSubagentTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Role string `json:"role"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	id, err := t.jobs.StartWithRole(args.Role, args.Task)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Role) == "" {
		return fmt.Sprintf("delegated subagent job %s", id), nil
	}
	return fmt.Sprintf("delegated subagent job %s role=%s", id, strings.TrimSpace(args.Role)), nil
}

func SingleLineText(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
