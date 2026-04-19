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
		return "", fmt.Errorf("old_text not found in %s. %s", args.Path, buildEditNotFoundDiagnostic(original, args.OldText))
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
	mode := fileModeOrDefault(path, 0o644)
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

func buildEditNotFoundDiagnostic(original, oldText string) string {
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return "Use read_file first and provide an exact non-empty old_text block."
	}
	anchor := ""
	for _, line := range strings.Split(oldText, "\n") {
		candidate := strings.TrimSpace(line)
		if len(candidate) >= 4 {
			anchor = candidate
			break
		}
	}
	if anchor == "" {
		anchor = oldText
	}
	if len(anchor) > 64 {
		anchor = anchor[:64]
	}

	type hit struct {
		line int
		text string
	}
	var hits []hit
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	lowerAnchor := strings.ToLower(anchor)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lowerLine := strings.ToLower(trimmed)
		if strings.Contains(trimmed, anchor) || strings.Contains(lowerLine, lowerAnchor) {
			if len(trimmed) > 100 {
				trimmed = trimmed[:100] + "..."
			}
			hits = append(hits, hit{line: i + 1, text: trimmed})
			if len(hits) >= 3 {
				break
			}
		}
	}
	if len(hits) == 0 {
		for _, token := range strings.Fields(anchor) {
			if len(token) < 4 {
				continue
			}
			lowerToken := strings.ToLower(token)
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" {
					continue
				}
				lowerLine := strings.ToLower(trimmed)
				if strings.Contains(lowerLine, lowerToken) {
					if len(trimmed) > 100 {
						trimmed = trimmed[:100] + "..."
					}
					hits = append(hits, hit{line: i + 1, text: trimmed})
					if len(hits) >= 3 {
						break
					}
				}
			}
			if len(hits) > 0 {
				break
			}
		}
	}
	if len(hits) == 0 {
		return "Use read_file to copy the exact block (including whitespace/newlines), then retry with a smaller unique old_text."
	}
	var b strings.Builder
	b.WriteString("Closest anchor matches: ")
	for i, item := range hits {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(fmt.Sprintf("line %d: %q", item.line, item.text))
	}
	b.WriteString(". Copy an exact unique block from read_file output and retry.")
	return b.String()
}
