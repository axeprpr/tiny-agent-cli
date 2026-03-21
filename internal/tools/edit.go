package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tiny-agent-cli/internal/model"
)

type editFileTool struct {
	workDir  string
	approver Approver
}

func newEditFileTool(workDir string, approver Approver) Tool {
	return &editFileTool{workDir: workDir, approver: approver}
}

func (t *editFileTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "edit_file",
			Description: "Edit an existing text file by replacing one exact text block with another. Use this for precise changes instead of rewriting the full file.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"path", "old_text", "new_text"},
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to the workspace root",
					},
					"old_text": map[string]any{
						"type":        "string",
						"description": "Exact existing text to replace. Must match exactly once.",
					},
					"new_text": map[string]any{
						"type":        "string",
						"description": "Replacement text",
					},
				},
			},
		},
	}
}

func (t *editFileTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.OldText == "" {
		return "", fmt.Errorf("old_text must not be empty")
	}

	path, err := securePath(t.workDir, args.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if looksBinary(data) {
		return "", fmt.Errorf("edit_file only supports text files")
	}

	original := string(data)
	matches := strings.Count(original, args.OldText)
	switch {
	case matches == 0:
		return "", fmt.Errorf("old_text not found in %s", args.Path)
	case matches > 1:
		return "", fmt.Errorf("old_text matched %d times in %s; provide a more specific block", matches, args.Path)
	}

	updated := strings.Replace(original, args.OldText, args.NewText, 1)
	if updated == original {
		return "no changes applied", nil
	}
	if t.approver != nil {
		approved, err := t.approver.ApproveWrite(ctx, path, updated)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("file write rejected by user")
		}
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}
