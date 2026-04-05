package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"tiny-agent-cli/internal/model"
)

type globSearchTool struct {
	workDir string
}

func newGlobSearchTool(workDir string) Tool {
	return &globSearchTool{workDir: workDir}
}

func (t *globSearchTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "glob_search",
			Description: "Find workspace files matching a glob pattern such as **/*.go or cmd/*.go.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"pattern"},
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern relative to the workspace root. Supports ** for recursive matches.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional subdirectory relative to the workspace root.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum matches to return, default 100.",
					},
				},
			},
		},
	}
}

func (t *globSearchTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if args.Limit <= 0 {
		args.Limit = 100
	}

	root := t.workDir
	if strings.TrimSpace(args.Path) != "" {
		var err error
		root, err = securePath(t.workDir, args.Path)
		if err != nil {
			return "", err
		}
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel, d.IsDir()) {
			if d.IsDir() {
				rel += "/"
			}
			matches = append(matches, rel)
			if len(matches) >= args.Limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

func globMatch(pattern, rel string, isDir bool) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")
			return strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, suffix)
		}
	}
	base := filepath.Base(rel)
	if isDir {
		base = strings.TrimSuffix(base, "/")
	}
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	return false
}
