package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"tiny-agent-cli/internal/model"
)

type reviewDiffTool struct {
	workDir string
}

func newReviewDiffTool(workDir string) Tool {
	return &reviewDiffTool{workDir: workDir}
}

func (t *reviewDiffTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "review_diff",
			Description: "Read a git diff for code review. Use this before reviewing changes so findings are grounded in the actual patch.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"base": map[string]any{
						"type":        "string",
						"description": "Optional base ref, default HEAD",
					},
					"target": map[string]any{
						"type":        "string",
						"description": "Optional target ref to compare against base",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional workspace-relative path filter",
					},
					"staged": map[string]any{
						"type":        "boolean",
						"description": "Review staged changes instead of working tree changes",
					},
				},
			},
		},
	}
}

func (t *reviewDiffTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Base   string `json:"base"`
		Target string `json:"target"`
		Path   string `json:"path"`
		Staged bool   `json:"staged"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	return ReviewDiff(ctx, t.workDir, args.Base, args.Target, args.Path, args.Staged)
}

func ReviewDiff(ctx context.Context, workDir, base, target, path string, staged bool) (string, error) {
	base = strings.TrimSpace(base)
	target = strings.TrimSpace(target)
	path = strings.TrimSpace(path)
	if base == "" {
		base = "HEAD"
	}

	args := []string{"-C", workDir, "diff", "--no-ext-diff", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	if target != "" {
		args = append(args, base, target)
	} else if !staged {
		args = append(args, base)
	}
	if path != "" {
		args = append(args, "--", path)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	data, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(data))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("git diff failed: %s", text)
	}
	if text == "" {
		return "no diff found", nil
	}
	if len(text) > 48000 {
		text = text[:48000] + "\n...[truncated]"
	}
	return text, nil
}
