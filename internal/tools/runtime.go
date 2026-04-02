package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

type ToolHook interface {
	BeforeTool(ctx context.Context, inv ToolInvocation) error
	AfterTool(ctx context.Context, inv ToolInvocation, out ToolOutcome)
	OnToolError(ctx context.Context, inv ToolInvocation, out ToolOutcome)
}

type ToolPermissionDecider interface {
	Decide(ctx context.Context, inv ToolInvocation) error
}

type ToolAuditEvent struct {
	Time         time.Time `json:"time"`
	Tool         string    `json:"tool"`
	Status       string    `json:"status"`
	DurationMs   int64     `json:"duration_ms"`
	ArgsPreview  string    `json:"args_preview,omitempty"`
	OutputSample string    `json:"output_sample,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type ToolAuditSink interface {
	RecordToolEvent(ctx context.Context, event ToolAuditEvent)
}

type runCommandSafetyHook struct{}

func NewDefaultHooks() []ToolHook {
	return []ToolHook{
		runCommandSafetyHook{},
	}
}

func (runCommandSafetyHook) BeforeTool(_ context.Context, inv ToolInvocation) error {
	if inv.Name != "run_command" {
		return nil
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(inv.Raw, &args); err != nil {
		return fmt.Errorf("decode args: %w", err)
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	return validateCommand(command)
}

func (runCommandSafetyHook) AfterTool(_ context.Context, _ ToolInvocation, _ ToolOutcome) {}

func (runCommandSafetyHook) OnToolError(_ context.Context, _ ToolInvocation, _ ToolOutcome) {}

type approvalPermissionDecider struct {
	workDir  string
	approver Approver
}

func newApprovalPermissionDecider(workDir string, approver Approver) ToolPermissionDecider {
	if approver == nil {
		return nil
	}
	return &approvalPermissionDecider{
		workDir:  workDir,
		approver: approver,
	}
}

func (d *approvalPermissionDecider) Decide(ctx context.Context, inv ToolInvocation) error {
	if d == nil || d.approver == nil {
		return nil
	}
	switch inv.Name {
	case "run_command":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return fmt.Errorf("decode args: %w", err)
		}
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return fmt.Errorf("command is required")
		}
		approved, err := d.approver.ApproveCommand(ctx, command)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("command rejected by user")
		}
	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return fmt.Errorf("decode args: %w", err)
		}
		path, err := securePath(d.workDir, args.Path)
		if err != nil {
			return err
		}
		approved, err := d.approver.ApproveWrite(ctx, path, args.Content)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("file write rejected by user")
		}
	case "start_background_job":
		// Background jobs enforce internal command limits; allow and rely on audit trail.
	}
	return nil
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
