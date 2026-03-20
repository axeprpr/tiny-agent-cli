package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"onek-agent/internal/model"
)

type listFilesTool struct {
	workDir string
}

func newListFilesTool(workDir string) Tool {
	return &listFilesTool{workDir: workDir}
}

func (t *listFilesTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "list_files",
			Description: "List files and directories inside the workspace or a subdirectory",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory path relative to the workspace root",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "Maximum recursion depth, default 2",
					},
				},
			},
		},
	}
}

func (t *listFilesTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("decode args: %w", err)
		}
	}

	if args.Depth <= 0 {
		args.Depth = 2
	}

	root := t.workDir
	if strings.TrimSpace(args.Path) != "" {
		var err error
		root, err = securePath(t.workDir, args.Path)
		if err != nil {
			return "", err
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return info.Name(), nil
	}

	var entries []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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
		depth := len(strings.Split(rel, string(os.PathSeparator)))
		if depth > args.Depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		label := rel
		if d.IsDir() {
			label += "/"
		}
		entries = append(entries, label)
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Strings(entries)
	if len(entries) == 0 {
		return "(empty)", nil
	}
	return strings.Join(entries, "\n"), nil
}

type readFileTool struct {
	workDir string
}

func newReadFileTool(workDir string) Tool {
	return &readFileTool{workDir: workDir}
}

func (t *readFileTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "read_file",
			Description: "Read a text file inside the workspace",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"path",
				},
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to the workspace root",
					},
				},
			},
		},
	}
}

func (t *readFileTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	path, err := securePath(t.workDir, args.Path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > 128*1024 {
		data = data[:128*1024]
	}
	return string(data), nil
}

type writeFileTool struct {
	workDir  string
	approver Approver
}

func newWriteFileTool(workDir string, approver Approver) Tool {
	return &writeFileTool{workDir: workDir, approver: approver}
}

func (t *writeFileTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "write_file",
			Description: "Write a text file inside the workspace. Creates parent directories when needed.",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"path",
					"content",
				},
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to the workspace root",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full file content to write",
					},
				},
			},
		},
	}
}

func (t *writeFileTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	path, err := securePath(t.workDir, args.Path)
	if err != nil {
		return "", err
	}
	if t.approver != nil {
		approved, err := t.approver.ApproveWrite(ctx, path, args.Content)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("file write rejected by user")
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", err
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), path), nil
}

type grepTool struct {
	workDir string
}

func newGrepTool(workDir string) Tool {
	return &grepTool{workDir: workDir}
}

func (t *grepTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "grep",
			Description: "Search for a regular expression across text files in the workspace",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"pattern",
				},
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regular expression pattern",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional file or directory path relative to the workspace root",
					},
				},
			},
		},
	}
}

func (t *grepTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", err
	}

	root := t.workDir
	if strings.TrimSpace(args.Path) != "" {
		root, err = securePath(t.workDir, args.Path)
		if err != nil {
			return "", err
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}

	var matches []string
	if !info.IsDir() {
		return grepFile(root, t.workDir, re)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 256*1024 {
			return nil
		}

		out, err := grepFile(path, t.workDir, re)
		if err == nil && out != "" {
			matches = append(matches, out)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

func grepFile(path, workDir string, re *regexp.Regexp) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	rel, err := filepath.Rel(workDir, path)
	if err != nil {
		rel = path
	}

	for i, line := range lines {
		if re.MatchString(line) {
			out = append(out, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
		}
		if len(out) >= 20 {
			break
		}
	}

	return strings.Join(out, "\n"), nil
}
