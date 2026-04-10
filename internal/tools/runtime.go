package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
)

type ToolInvocation struct {
	Name string
	Raw  json.RawMessage
}

type ToolOutcome struct {
	Output   string
	Err      error
	Duration time.Duration
}

type ToolResult struct {
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type ToolHook interface {
	BeforeTool(ctx context.Context, inv *ToolInvocation) error
	AfterTool(ctx context.Context, inv *ToolInvocation, out *ToolOutcome) error
	OnToolError(ctx context.Context, inv *ToolInvocation, out *ToolOutcome) error
}

type HookConfig struct {
	PreToolUse  []string `json:"PreToolUse,omitempty"`
	PostToolUse []string `json:"PostToolUse,omitempty"`
}

func DefaultHookConfig() HookConfig {
	return HookConfig{}
}

type PermissionDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type ToolPermissionPolicy interface {
	Evaluate(ctx context.Context, inv ToolInvocation) PermissionDecision
}

type ToolPermissionDecider interface {
	Decide(ctx context.Context, inv ToolInvocation) error
}

type ToolAuditEvent struct {
	Time         time.Time `json:"time"`
	Tool         string    `json:"tool"`
	Status       string    `json:"status"`
	DurationMs   int64     `json:"duration_ms"`
	InputJSON    string    `json:"input_json,omitempty"`
	ArgsPreview  string    `json:"args_preview,omitempty"`
	OutputSample string    `json:"output_sample,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type ToolAuditSink interface {
	RecordToolEvent(ctx context.Context, event ToolAuditEvent)
}

type HookEvent string

const (
	HookEventPreToolUse  HookEvent = "PreToolUse"
	HookEventPostToolUse HookEvent = "PostToolUse"
)

type HookRunResult struct {
	denied   bool
	messages []string
}

func AllowHook(messages []string) HookRunResult {
	return HookRunResult{
		denied:   false,
		messages: append([]string(nil), messages...),
	}
}

func (r HookRunResult) IsDenied() bool {
	return r.denied
}

func (r HookRunResult) Messages() []string {
	return append([]string(nil), r.messages...)
}

type HookRunner struct {
	config HookConfig
}

type hookCommandRequest struct {
	event      HookEvent
	toolName   string
	toolInput  string
	toolOutput *string
	isError    bool
	payload    []byte
}

func NewDefaultHooks(HookConfig) []ToolHook {
	return nil
}

func NewHookRunner(config HookConfig) HookRunner {
	return HookRunner{config: config.normalized()}
}

func (r HookRunner) RunPreToolUse(toolName, toolInput string) HookRunResult {
	return r.runCommands(HookEventPreToolUse, r.config.PreToolUse, toolName, toolInput, nil, false)
}

func (r HookRunner) RunPostToolUse(toolName, toolInput, toolOutput string, isError bool) HookRunResult {
	return r.runCommands(HookEventPostToolUse, r.config.PostToolUse, toolName, toolInput, &toolOutput, isError)
}

func (r HookRunner) runCommands(event HookEvent, commands []string, toolName, toolInput string, toolOutput *string, isError bool) HookRunResult {
	if len(commands) == 0 {
		return AllowHook(nil)
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name":      string(event),
		"tool_name":            toolName,
		"tool_input":           parseHookToolInput(toolInput),
		"tool_input_json":      toolInput,
		"tool_output":          toolOutput,
		"tool_result_is_error": isError,
	})
	if err != nil {
		return AllowHook([]string{fmt.Sprintf("failed to encode hook payload for %q: %v", toolName, err)})
	}

	messages := make([]string, 0)
	for _, command := range commands {
		outcome := runHookCommand(command, hookCommandRequest{
			event:      event,
			toolName:   toolName,
			toolInput:  toolInput,
			toolOutput: toolOutput,
			isError:    isError,
			payload:    payload,
		})
		switch outcome.kind {
		case hookOutcomeAllow:
			if outcome.message != "" {
				messages = append(messages, outcome.message)
			}
		case hookOutcomeDeny:
			message := outcome.message
			if strings.TrimSpace(message) == "" {
				message = fmt.Sprintf("%s hook denied tool `%s`", event, toolName)
			}
			messages = append(messages, message)
			return HookRunResult{denied: true, messages: messages}
		case hookOutcomeWarn:
			messages = append(messages, outcome.message)
		}
	}
	return AllowHook(messages)
}

func (c HookConfig) normalized() HookConfig {
	out := HookConfig{
		PreToolUse:  normalizeHookCommands(c.PreToolUse),
		PostToolUse: normalizeHookCommands(c.PostToolUse),
	}
	return out
}

func normalizeHookCommands(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		out = append(out, command)
	}
	return out
}

type hookCommandOutcomeKind int

const (
	hookOutcomeAllow hookCommandOutcomeKind = iota
	hookOutcomeDeny
	hookOutcomeWarn
)

type hookCommandOutcome struct {
	kind    hookCommandOutcomeKind
	message string
}

func parseHookToolInput(toolInput string) any {
	var parsed any
	if err := json.Unmarshal([]byte(toolInput), &parsed); err == nil {
		return parsed
	}
	return map[string]string{"raw": toolInput}
}

func runHookCommand(command string, req hookCommandRequest) hookCommandOutcome {
	cmd := hookShellCommand(command)
	cmd.Stdin = bytes.NewReader(req.payload)
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	cmd.Env = append(os.Environ(),
		"HOOK_EVENT="+string(req.event),
		"HOOK_TOOL_NAME="+req.toolName,
		"HOOK_TOOL_INPUT="+req.toolInput,
		"HOOK_TOOL_IS_ERROR="+hookErrorEnv(req.isError),
	)
	if req.toolOutput != nil {
		cmd.Env = append(cmd.Env, "HOOK_TOOL_OUTPUT="+*req.toolOutput)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	stdoutText := strings.TrimSpace(stdout.String())
	stderrText := strings.TrimSpace(stderr.String())
	if err == nil {
		return hookCommandOutcome{kind: hookOutcomeAllow, message: stdoutText}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code == 2 {
			return hookCommandOutcome{kind: hookOutcomeDeny, message: stdoutText}
		} else if code >= 0 {
			return hookCommandOutcome{
				kind:    hookOutcomeWarn,
				message: formatHookWarning(command, code, stdoutText, stderrText),
			}
		}
		return hookCommandOutcome{
			kind: hookOutcomeWarn,
			message: fmt.Sprintf(
				"%s hook `%s` terminated by signal while handling `%s`",
				req.event,
				command,
				req.toolName,
			),
		}
	}

	return hookCommandOutcome{
		kind: hookOutcomeWarn,
		message: fmt.Sprintf(
			"%s hook `%s` failed to start for `%s`: %v",
			req.event,
			command,
			req.toolName,
			err,
		),
	}
}

func hookShellCommand(command string) *exec.Cmd {
	if isWindowsShell() {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-lc", command)
}

func isWindowsShell() bool {
	return runtime.GOOS == "windows"
}

func hookErrorEnv(isError bool) string {
	if isError {
		return "1"
	}
	return "0"
}

func formatHookWarning(command string, code int, stdout, stderr string) string {
	message := fmt.Sprintf(
		"Hook `%s` exited with status %d; allowing tool execution to continue",
		command,
		code,
	)
	if stdout != "" {
		return message + ": " + stdout
	}
	if stderr != "" {
		return message + ": " + stderr
	}
	return message
}

func formatHookMessage(result HookRunResult, fallback string) string {
	if len(result.messages) == 0 {
		return fallback
	}
	return strings.Join(result.messages, "\n")
}

func mergeHookFeedback(messages []string, output string, denied bool) string {
	if len(messages) == 0 {
		return output
	}
	sections := make([]string, 0, 2)
	if strings.TrimSpace(output) != "" {
		sections = append(sections, output)
	}
	label := "Hook feedback"
	if denied {
		label = "Hook feedback (denied)"
	}
	sections = append(sections, label+":\n"+strings.Join(messages, "\n"))
	return strings.Join(sections, "\n\n")
}

func FormatHookMessage(result HookRunResult, fallback string) string {
	return formatHookMessage(result, fallback)
}

func MergeHookFeedback(messages []string, output string, denied bool) string {
	return mergeHookFeedback(messages, output, denied)
}

type approvalPermissionDecider struct {
	policy ToolPermissionPolicy
}

type approvalPermissionPolicy struct {
	workDir  string
	approver Approver
	policy   *PermissionStore
}

type permissionPolicyDecider struct {
	policy ToolPermissionPolicy
}

func NewApprovalPermissionPolicy(workDir string, approver Approver, policy *PermissionStore) ToolPermissionPolicy {
	if approver == nil && policy == nil {
		return nil
	}
	return &approvalPermissionPolicy{
		workDir:  workDir,
		approver: approver,
		policy:   policy,
	}
}

func NewPermissionDecider(policy ToolPermissionPolicy) ToolPermissionDecider {
	if policy == nil {
		return nil
	}
	return &permissionPolicyDecider{policy: policy}
}

func newApprovalPermissionDecider(workDir string, approver Approver, policy *PermissionStore) ToolPermissionDecider {
	return NewPermissionDecider(NewApprovalPermissionPolicy(workDir, approver, policy))
}

func (d *permissionPolicyDecider) Decide(ctx context.Context, inv ToolInvocation) error {
	if d == nil {
		return nil
	}
	decision := d.policy.Evaluate(ctx, inv)
	if decision.Allowed {
		return nil
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = fmt.Sprintf("tool %q is denied by permission policy", inv.Name)
	}
	return fmt.Errorf("%s", reason)
}

func (d *approvalPermissionPolicy) Evaluate(ctx context.Context, inv ToolInvocation) PermissionDecision {
	if d == nil {
		return PermissionDecision{Allowed: true}
	}
	mode := PermissionModePrompt
	if d.approver != nil {
		mode = NormalizePermissionMode(d.approver.Mode())
	}
	if d.policy != nil {
		if override := d.policy.ToolMode(inv.Name); override != "" {
			mode = override
		} else if strings.TrimSpace(mode) == "" {
			mode = d.policy.DefaultMode()
		}
	}
	var matchedCommandRule CommandPermissionRule
	var matchedCommand bool
	var runCommandText string
	if inv.Name == "run_command" && d.policy != nil {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: fmt.Sprintf("decode args: %v", err)}
		}
		runCommandText = strings.TrimSpace(args.Command)
		if runCommandText == "" {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "command is required"}
		}
		if rule, ok := d.policy.MatchCommandRule(runCommandText); ok {
			matchedCommandRule = rule
			matchedCommand = true
			mode = rule.Mode
		}
	}
	if mode == PermissionModeDeny {
		reason := fmt.Sprintf("tool %q is denied by permission policy", inv.Name)
		if matchedCommand {
			reason = fmt.Sprintf("command %q is denied by policy pattern %q", runCommandText, matchedCommandRule.Pattern)
		}
		return PermissionDecision{
			Allowed: false,
			Mode:    mode,
			Reason:  reason,
		}
	}
	if mode == PermissionModeAllow {
		return PermissionDecision{Allowed: true, Mode: mode}
	}
	requiredMode := requiredPermissionMode(inv.Name)
	if PermissionModeAllows(mode, requiredMode) {
		return PermissionDecision{Allowed: true, Mode: mode}
	}
	if d.approver == nil {
		return PermissionDecision{
			Allowed: false,
			Mode:    mode,
			Reason:  fmt.Sprintf("tool %q requires %s permission; current mode is %s", inv.Name, requiredMode, mode),
		}
	}
	if !shouldPromptForPermission(mode, requiredMode) {
		return PermissionDecision{
			Allowed: false,
			Mode:    mode,
			Reason:  fmt.Sprintf("tool %q requires %s permission; current mode is %s", inv.Name, requiredMode, mode),
		}
	}
	switch inv.Name {
	case "run_command":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: fmt.Sprintf("decode args: %v", err)}
		}
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "command is required"}
		}
		approved, err := d.approver.ApproveCommand(ctx, command)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		if !approved {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "command rejected by user"}
		}
	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: fmt.Sprintf("decode args: %v", err)}
		}
		path, err := securePath(d.workDir, args.Path)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		approved, err := d.approver.ApproveWrite(ctx, path, args.Content)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		if !approved {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "file write rejected by user"}
		}
	case "edit_file":
		var args struct {
			Path    string `json:"path"`
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: fmt.Sprintf("decode args: %v", err)}
		}
		path, err := securePath(d.workDir, args.Path)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		if looksBinary(data) {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "edit_file only supports text files"}
		}
		updated := strings.Replace(string(data), args.OldText, args.NewText, 1)
		approved, err := d.approver.ApproveWrite(ctx, path, updated)
		if err != nil {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: err.Error()}
		}
		if !approved {
			return PermissionDecision{Allowed: false, Mode: mode, Reason: "file write rejected by user"}
		}
	case "start_background_job", "delegate_subagent":
		return PermissionDecision{Allowed: false, Mode: mode, Reason: fmt.Sprintf("tool %q requires %s permission; current mode is %s", inv.Name, requiredMode, mode)}
	}
	return PermissionDecision{Allowed: true, Mode: mode}
}

func requiredPermissionMode(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "write_file", "edit_file", "update_todo", "create_task", "update_task", "delete_task":
		return PermissionModeWorkspaceWrite
	case "run_command", "start_background_job", "delegate_subagent", "send_background_job":
		return PermissionModeDangerFullAccess
	default:
		return PermissionModeReadOnly
	}
}

func RequiredPermissionMode(toolName string) string {
	return requiredPermissionMode(toolName)
}

func shouldPromptForPermission(activeMode, requiredMode string) bool {
	activeMode = NormalizePermissionMode(activeMode)
	requiredMode = NormalizePermissionMode(requiredMode)
	switch activeMode {
	case PermissionModePrompt:
		return requiredMode != PermissionModeReadOnly
	case PermissionModeWorkspaceWrite:
		return requiredMode == PermissionModeDangerFullAccess
	default:
		return false
	}
}

func extractToolOutputFormat(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		return raw, "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, "", nil
	}
	value, ok := payload["_output"]
	if !ok {
		return raw, "", nil
	}
	delete(payload, "_output")
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	format, _ := value.(string)
	return out, strings.ToLower(strings.TrimSpace(format)), nil
}

func validateToolInput(raw json.RawMessage, params map[string]any) error {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid JSON arguments")
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if params == nil || len(params) == 0 {
			return nil
		}
		return validateInputSchema(raw, model.FunctionSpec{Parameters: params})
	}
	return fmt.Errorf("tool arguments must be a JSON object or array")
}

func validateInputSchema(raw json.RawMessage, spec model.FunctionSpec) error {
	if spec.Parameters == nil {
		return nil
	}
	expectedType, _ := spec.Parameters["type"].(string)
	if expectedType != "" && expectedType != "object" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode args: %w", err)
	}
	required := requiredFields(spec.Parameters)
	for _, key := range required {
		value, ok := payload[key]
		if !ok {
			return fmt.Errorf("missing required argument %q", key)
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
			return fmt.Errorf("required argument %q must not be empty", key)
		}
	}
	props, _ := spec.Parameters["properties"].(map[string]any)
	for key, value := range payload {
		propDef, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		propType, _ := propDef["type"].(string)
		if propType == "" {
			continue
		}
		if !valueMatchesType(value, propType) {
			return fmt.Errorf("argument %q has invalid type: expected %s", key, propType)
		}
	}
	return nil
}

func requiredFields(params map[string]any) []string {
	raw, ok := params["required"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func valueMatchesType(value any, t string) bool {
	switch t {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		f, ok := value.(float64)
		if !ok {
			return false
		}
		return f == float64(int64(f))
	case "number":
		_, ok := value.(float64)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		// Unknown schema type: do not block execution.
		return true
	}
}

func compactAuditSample(text string, limit int) string {
	text = strings.TrimSpace(SingleLineText(text))
	if text == "" {
		return ""
	}
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func FormatDurationMs(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return strconv.FormatInt(d.Milliseconds(), 10)
}
