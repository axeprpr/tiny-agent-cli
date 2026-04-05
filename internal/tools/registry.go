package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	tools      map[string]Tool
	todo       *todoStore
	hooks      []ToolHook
	hookConfig HookConfig
	permission ToolPermissionDecider
	audit      ToolAuditSink
}

func NewRegistry(workDir, shell string, commandTimeout time.Duration, approver Approver, jobs ...JobControl) *Registry {
	return NewRegistryWithHooks(workDir, shell, commandTimeout, approver, DefaultHookConfig(), jobs...)
}

func NewRegistryWithHooks(workDir, shell string, commandTimeout time.Duration, approver Approver, hookCfg HookConfig, jobs ...JobControl) *Registry {
	r := &Registry{
		tools:      make(map[string]Tool),
		hookConfig: hookCfg.normalized(),
	}
	todos := newTodoStoreWithPath(filepath.Join(workDir, ".tacli", "tasks-v2.json"))
	r.todo = todos
	r.permission = newApprovalPermissionDecider(workDir, approver)
	r.hooks = append(r.hooks, NewDefaultHooks(r.hookConfig)...)

	toolset := []Tool{
		newUpdateTodoTool(todos),
		newShowTodoTool(todos),
		newListFilesTool(workDir),
		newReadFileTool(workDir),
		newEditFileTool(workDir, nil),
		newWriteFileTool(workDir, nil),
		newGrepTool(workDir),
		newRunCommandTool(workDir, shell, commandTimeout, nil),
		newFetchURLTool(),
		newWebSearchTool(),
	}
	if len(jobs) > 0 && jobs[0] != nil {
		toolset = append(toolset,
			newStartBackgroundJobTool(jobs[0]),
			newDelegateSubagentTool(jobs[0]),
			newListBackgroundJobsTool(jobs[0]),
			newInspectBackgroundJobTool(jobs[0]),
			newSendBackgroundJobTool(jobs[0]),
		)
	}

	for _, tool := range toolset {
		r.tools[tool.Definition().Function.Name] = tool
	}

	return r
}

func (r *Registry) TodoItems() []TodoItem {
	if r.todo == nil {
		return nil
	}
	return r.todo.Items()
}

func (r *Registry) ReplaceTodo(items []TodoItem) error {
	if r.todo == nil {
		return nil
	}
	return r.todo.Replace(items)
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
	inv := ToolInvocation{Name: name, Raw: raw}
	for _, hook := range r.hooks {
		if hook == nil {
			continue
		}
		if err := hook.BeforeTool(ctx, &inv); err != nil {
			r.recordAudit(ctx, inv, ToolOutcome{Err: err}, "pre_hook_error")
			return "", err
		}
	}
	if err := validateToolInput(inv.Raw, tool.Definition().Function.Parameters); err != nil {
		r.recordAudit(ctx, inv, ToolOutcome{Err: err}, "validation_error")
		return "", err
	}
	if r.permission != nil {
		if err := r.permission.Decide(ctx, inv); err != nil {
			r.recordAudit(ctx, inv, ToolOutcome{Err: err}, "permission_denied")
			return "", err
		}
	}

	started := time.Now()
	output, err := tool.Call(ctx, inv.Raw)
	out := ToolOutcome{
		Output:   output,
		Err:      err,
		Duration: time.Since(started),
	}
	if err != nil {
		for _, hook := range r.hooks {
			if hook == nil {
				continue
			}
			if hookErr := hook.OnToolError(ctx, &inv, &out); hookErr != nil {
				out.Err = hookErr
				r.recordAudit(ctx, inv, out, "error_hook_error")
				return out.Output, out.Err
			}
		}
		if out.Err != nil {
			r.recordAudit(ctx, inv, out, "error")
			return out.Output, out.Err
		}
		r.recordAudit(ctx, inv, out, "error_handled")
		return out.Output, nil
	}
	for _, hook := range r.hooks {
		if hook == nil {
			continue
		}
		if hookErr := hook.AfterTool(ctx, &inv, &out); hookErr != nil {
			out.Err = hookErr
			r.recordAudit(ctx, inv, out, "post_hook_error")
			return out.Output, out.Err
		}
	}
	r.recordAudit(ctx, inv, out, "ok")
	return out.Output, nil
}

func (r *Registry) AddHook(hook ToolHook) {
	if hook == nil {
		return
	}
	r.hooks = append(r.hooks, hook)
}

func (r *Registry) AddTool(tool Tool) {
	if r == nil || tool == nil {
		return
	}
	def := tool.Definition()
	name := strings.TrimSpace(def.Function.Name)
	if name == "" {
		return
	}
	r.tools[name] = tool
}

func (r *Registry) SetAuditSink(audit ToolAuditSink) {
	r.audit = audit
}

func (r *Registry) recordAudit(ctx context.Context, inv ToolInvocation, out ToolOutcome, status string) {
	if r == nil || r.audit == nil {
		return
	}
	event := ToolAuditEvent{
		Time:         time.Now(),
		Tool:         inv.Name,
		Status:       status,
		DurationMs:   out.Duration.Milliseconds(),
		ArgsPreview:  compactPreviewString(json.RawMessage(inv.Raw), 140),
		OutputSample: compactAuditSample(out.Output, 200),
	}
	if out.Err != nil {
		event.Error = out.Err.Error()
	}
	r.audit.RecordToolEvent(ctx, event)
}

func (r *Registry) Preview(name string, raw json.RawMessage) string {
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return ""
		}
	}

	switch name {
	case "update_todo":
		return compactPreviewString(args["items"], 120)
	case "run_command":
		return compactPreviewString(args["command"], 80)
	case "start_background_job":
		return compactPreviewString(args["task"], 80)
	case "delegate_subagent":
		return joinPreviewParts(
			compactKeyValue("role", args["role"], 24),
			compactKeyValue("task", args["task"], 80),
		)
	case "send_background_job":
		return joinPreviewParts(
			compactKeyValue("id", args["id"], 24),
			compactKeyValue("task", args["task"], 64),
		)
	case "inspect_background_job":
		return compactKeyValue("id", args["id"], 24)
	case "list_files", "read_file", "write_file", "fetch_url":
		return compactPreviewString(args["path"], 80) + compactKeyValue("url", args["url"], 80)
	case "edit_file":
		return joinPreviewParts(
			compactKeyValue("path", args["path"], 60),
			compactKeyValue("old_text", args["old_text"], 40),
		)
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
		if marshaled, err := json.Marshal(value); err == nil {
			text = string(marshaled)
		} else {
			return ""
		}
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
