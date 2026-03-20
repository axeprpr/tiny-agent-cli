package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
)

type Tool interface {
	Definition() model.Tool
	Call(ctx context.Context, raw json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(workDir, shell string, commandTimeout time.Duration, approver Approver) *Registry {
	r := &Registry{tools: make(map[string]Tool)}

	for _, tool := range []Tool{
		newListFilesTool(workDir),
		newReadFileTool(workDir),
		newWriteFileTool(workDir, approver),
		newGrepTool(workDir),
		newRunCommandTool(workDir, shell, commandTimeout, approver),
		newFetchURLTool(),
		newWebSearchTool(),
	} {
		r.tools[tool.Definition().Function.Name] = tool
	}

	return r
}

func (r *Registry) Definitions() []model.Tool {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]model.Tool, 0, len(names))
	for _, name := range names {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

func (r *Registry) Call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return tool.Call(ctx, raw)
}

func (r *Registry) Preview(name string, raw json.RawMessage) string {
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return ""
		}
	}

	switch name {
	case "run_command":
		return compactPreviewString(args["command"], 80)
	case "list_files", "read_file", "write_file", "fetch_url":
		return compactPreviewString(args["path"], 80) + compactKeyValue("url", args["url"], 80)
	case "grep":
		return joinPreviewParts(
			compactKeyValue("pattern", args["pattern"], 40),
			compactKeyValue("path", args["path"], 40),
		)
	case "web_search":
		return compactPreviewString(args["query"], 80)
	default:
		return ""
	}
}

func compactKeyValue(key string, value any, limit int) string {
	text := compactPreviewString(value, limit)
	if text == "" {
		return ""
	}
	return key + "=" + text
}

func compactPreviewString(value any, limit int) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > limit {
		text = text[:limit] + "..."
	}
	return text
}

func joinPreviewParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}
