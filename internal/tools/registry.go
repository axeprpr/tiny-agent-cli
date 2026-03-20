package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"onek-agent/internal/model"
)

type Tool interface {
	Definition() model.Tool
	Call(ctx context.Context, raw json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(workDir, shell string, commandTimeout time.Duration) *Registry {
	r := &Registry{tools: make(map[string]Tool)}

	for _, tool := range []Tool{
		newListFilesTool(workDir),
		newReadFileTool(workDir),
		newWriteFileTool(workDir),
		newGrepTool(workDir),
		newRunCommandTool(workDir, shell, commandTimeout),
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
