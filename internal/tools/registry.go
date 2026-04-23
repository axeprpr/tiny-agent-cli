package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tasks"
)

type Tool interface {
	Definition() model.Tool
	Call(ctx context.Context, raw json.RawMessage) (string, error)
}

type StreamTool interface {
	CallStream(ctx context.Context, raw json.RawMessage, onUpdate func(string)) (string, error)
}

type Registry struct {
	tools      map[string]Tool
	todo       *todoStore
	contract   *contractStore
	hooks      []ToolHook
	hookConfig HookConfig
	hookRunner HookRunner
	permission ToolPermissionDecider
	policy     *PermissionStore
	audit      ToolAuditSink
}

type callInternalOptions struct {
	decidePermission bool
	runHookRunner    bool
	recordAudit      bool
	onUpdate         func(string)
}

func NewRegistry(workDir, shell string, commandTimeout time.Duration, approver Approver, jobs ...JobControl) *Registry {
	return NewRegistryWithOptions(workDir, shell, commandTimeout, approver, DefaultHookConfig(), nil, jobs...)
}

func NewRegistryWithHooks(workDir, shell string, commandTimeout time.Duration, approver Approver, hookCfg HookConfig, jobs ...JobControl) *Registry {
	return NewRegistryWithOptions(workDir, shell, commandTimeout, approver, hookCfg, nil, jobs...)
}

func NewRegistryWithOptions(workDir, shell string, commandTimeout time.Duration, approver Approver, hookCfg HookConfig, policy *PermissionStore, jobs ...JobControl) *Registry {
	r := &Registry{
		tools:      make(map[string]Tool),
		hookConfig: hookCfg.normalized(),
		policy:     policy,
	}
	todos := newTodoStoreWithPath(filepath.Join(workDir, ".tacli", "tasks-v2.json"))
	contract := newContractStoreWithPath(ContractPath(workDir))
	taskStore := tasks.New(filepath.Join(workDir, ".tacli", "tasks.json"))
	r.todo = todos
	r.contract = contract
	r.permission = newApprovalPermissionDecider(workDir, approver, policy)
	r.hookRunner = NewHookRunner(r.hookConfig)
	r.hooks = append(r.hooks, NewDefaultHooks(r.hookConfig)...)

	toolset := []Tool{
		newUpdateTodoTool(todos),
		newShowTodoTool(todos),
		newUpdateTaskContractTool(contract),
		newShowTaskContractTool(contract),
		newCreateTaskTool(taskStore),
		newListTasksTool(taskStore),
		newUpdateTaskTool(taskStore),
		newDeleteTaskTool(taskStore),
		newReviewDiffTool(workDir),
		newListFilesTool(workDir),
		newGlobSearchTool(workDir),
		newReadFileTool(workDir),
		newEditFileTool(workDir, nil),
		newWriteFileTool(workDir, nil),
		newGrepTool(workDir),
		newRunCommandTool(workDir, shell, commandTimeout, nil),
		newFetchURLTool(),
		newWebSearchTool(),
		newInspectDOCXTool(workDir),
		newInspectPDFTool(workDir),
		newCheckWebappTool(),
		newListMCPServersTool(filepath.Join(workDir, ".tacli")),
		newListMCPResourcesTool(workDir, filepath.Join(workDir, ".tacli")),
		newReadMCPResourceTool(workDir, filepath.Join(workDir, ".tacli")),
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

func (r *Registry) TaskContract() TaskContract {
	if r.contract == nil {
		return TaskContract{}
	}
	return r.contract.Current()
}

func (r *Registry) ReplaceTaskContract(contract TaskContract) error {
	if r.contract == nil {
		return nil
	}
	return r.contract.Replace(contract)
}

func (r *Registry) ClearTaskContract() error {
	if r.contract == nil {
		return nil
	}
	return r.contract.Clear()
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
	result, err := r.callInternal(ctx, name, raw, callInternalOptions{
		decidePermission: true,
		runHookRunner:    true,
		recordAudit:      true,
	})
	if err != nil {
		return result.Output, err
	}
	if result.Status == "ok" {
		return result.Output, nil
	}
	return result.Output, fmt.Errorf("%s", result.Error)
}

func (r *Registry) CallStructured(ctx context.Context, name string, raw json.RawMessage) (ToolResult, error) {
	return r.callInternal(ctx, name, raw, callInternalOptions{
		decidePermission: true,
		runHookRunner:    true,
		recordAudit:      true,
	})
}

func (r *Registry) CallStructuredWithoutPermission(ctx context.Context, name string, raw json.RawMessage) (ToolResult, error) {
	return r.callInternal(ctx, name, raw, callInternalOptions{
		decidePermission: false,
		runHookRunner:    true,
		recordAudit:      true,
	})
}

func (r *Registry) CallStructuredForRuntime(ctx context.Context, name string, raw json.RawMessage) (ToolResult, error) {
	return r.CallStructuredForRuntimeWithUpdates(ctx, name, raw, nil)
}

func (r *Registry) CallStructuredForRuntimeWithUpdates(ctx context.Context, name string, raw json.RawMessage, onUpdate func(string)) (ToolResult, error) {
	return r.callInternal(ctx, name, raw, callInternalOptions{
		decidePermission: false,
		runHookRunner:    false,
		recordAudit:      false,
		onUpdate:         onUpdate,
	})
}

func (r *Registry) callInternal(ctx context.Context, name string, raw json.RawMessage, opts callInternalOptions) (ToolResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		err := fmt.Errorf("unknown tool %q", name)
		return ToolResult{Tool: name, Status: "error", Error: err.Error()}, err
	}
	record := func(inv ToolInvocation, out ToolOutcome, status string) {
		if opts.recordAudit {
			r.recordAudit(ctx, inv, out, status)
		}
	}
	cleanRaw, outputFormat, err := extractToolOutputFormat(raw)
	if err != nil {
		return ToolResult{Tool: name, Status: "error", Error: err.Error()}, err
	}
	inv := ToolInvocation{Name: name, Raw: cleanRaw}
	for _, hook := range r.hooks {
		if hook == nil {
			continue
		}
		if err := hook.BeforeTool(ctx, &inv); err != nil {
			record(inv, ToolOutcome{Err: err}, "pre_hook_error")
			return r.structuredResult(name, ToolOutcome{Err: err}, outputFormat), err
		}
	}
	if err := validateToolInput(inv.Raw, tool.Definition().Function.Parameters); err != nil {
		record(inv, ToolOutcome{Err: err}, "validation_error")
		return r.structuredResult(name, ToolOutcome{Err: err}, outputFormat), err
	}
	if opts.decidePermission && r.permission != nil {
		if err := r.permission.Decide(ctx, inv); err != nil {
			record(inv, ToolOutcome{Err: err}, "permission_denied")
			return r.structuredResult(name, ToolOutcome{Err: err}, outputFormat), err
		}
	}
	preHookResult := AllowHook(nil)
	if opts.runHookRunner {
		preHookResult = r.hookRunner.RunPreToolUse(name, string(inv.Raw))
	}
	if preHookResult.IsDenied() {
		err := errors.New(formatHookMessage(preHookResult, fmt.Sprintf("PreToolUse hook denied tool `%s`", name)))
		out := ToolOutcome{
			Output: mergeHookFeedback(preHookResult.Messages(), "", true),
			Err:    err,
		}
		record(inv, out, "pre_hook_denied")
		return r.structuredResult(name, out, outputFormat), err
	}

	started := time.Now()
	var output string
	if streamTool, ok := tool.(StreamTool); ok && opts.onUpdate != nil {
		output, err = streamTool.CallStream(ctx, inv.Raw, opts.onUpdate)
	} else {
		output, err = tool.Call(ctx, inv.Raw)
	}
	out := ToolOutcome{
		Output:   mergeHookFeedback(preHookResult.Messages(), output, false),
		Err:      err,
		Duration: time.Since(started),
	}
	postHookResult := AllowHook(nil)
	if opts.runHookRunner {
		postHookResult = r.hookRunner.RunPostToolUse(name, string(inv.Raw), out.Output, out.Err != nil)
	}
	if postHookResult.IsDenied() {
		out.Err = errors.New(formatHookMessage(postHookResult, fmt.Sprintf("PostToolUse hook denied tool `%s`", name)))
	}
	out.Output = mergeHookFeedback(postHookResult.Messages(), out.Output, postHookResult.IsDenied())
	if err != nil {
		for _, hook := range r.hooks {
			if hook == nil {
				continue
			}
			if hookErr := hook.OnToolError(ctx, &inv, &out); hookErr != nil {
				out.Err = hookErr
				record(inv, out, "error_hook_error")
				return r.structuredResult(name, out, outputFormat), out.Err
			}
		}
		if out.Err != nil {
			record(inv, out, "error")
			return r.structuredResult(name, out, outputFormat), out.Err
		}
		record(inv, out, "error_handled")
		return r.structuredResult(name, out, outputFormat), nil
	}
	for _, hook := range r.hooks {
		if hook == nil {
			continue
		}
		if hookErr := hook.AfterTool(ctx, &inv, &out); hookErr != nil {
			out.Err = hookErr
			record(inv, out, "post_hook_error")
			return r.structuredResult(name, out, outputFormat), out.Err
		}
	}
	record(inv, out, "ok")
	return r.structuredResult(name, out, outputFormat), nil
}

func (r *Registry) AddHook(hook ToolHook) {
	if hook == nil {
		return
	}
	r.hooks = append(r.hooks, hook)
}

func (r *Registry) PermissionDecider() ToolPermissionDecider {
	if r == nil {
		return nil
	}
	return r.permission
}

func (r *Registry) SetPermissionDecider(decider ToolPermissionDecider) {
	if r == nil {
		return
	}
	r.permission = decider
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

func (r *Registry) RecordToolAudit(ctx context.Context, inv ToolInvocation, out ToolOutcome, status string) {
	r.recordAudit(ctx, inv, out, status)
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
		InputJSON:    string(inv.Raw),
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
	case "update_task_contract":
		return joinPreviewParts(
			compactKeyValue("task_kind", args["task_kind"], 32),
			compactKeyValue("objective", args["objective"], 80),
		)
	case "run_command":
		return compactPreviewString(args["command"], 80)
	case "create_task":
		return compactPreviewString(args["title"], 80)
	case "update_task":
		return joinPreviewParts(
			compactKeyValue("id", args["id"], 24),
			compactKeyValue("status", args["status"], 20),
			compactKeyValue("title", args["title"], 40),
		)
	case "delete_task":
		return compactKeyValue("id", args["id"], 24)
	case "list_tasks":
		return ""
	case "review_diff":
		return joinPreviewParts(
			compactKeyValue("base", args["base"], 24),
			compactKeyValue("target", args["target"], 24),
			compactKeyValue("path", args["path"], 40),
		)
	case "start_background_job":
		return joinPreviewParts(
			compactKeyValue("role", args["role"], 20),
			compactKeyValue("isolation", args["isolation"], 16),
			compactKeyValue("task", args["task"], 80),
		)
	case "delegate_subagent":
		return joinPreviewParts(
			compactKeyValue("role", args["role"], 24),
			compactKeyValue("isolation", args["isolation"], 16),
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
	case "inspect_docx", "inspect_pdf":
		return compactPreviewString(args["path"], 80)
	case "check_webapp":
		return joinPreviewParts(
			compactKeyValue("url", args["url"], 80),
			compactKeyValue("title_contains", args["title_contains"], 40),
		)
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

func (r *Registry) structuredResult(name string, out ToolOutcome, format string) ToolResult {
	result := ToolResult{
		Tool:       name,
		Status:     "ok",
		Output:     out.Output,
		DurationMs: out.Duration.Milliseconds(),
	}
	if out.Err != nil {
		result.Status = "error"
		result.Error = out.Err.Error()
	}
	if format == "json" {
		data, err := json.Marshal(result)
		if err == nil {
			result.Output = string(data)
			result.Error = ""
			if out.Err != nil {
				result.Error = out.Err.Error()
			}
		}
	}
	return result
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
