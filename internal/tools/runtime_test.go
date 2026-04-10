package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tiny-agent-cli/internal/model"
)

func TestValidateInputSchemaRequired(t *testing.T) {
	spec := model.FunctionSpec{
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"path", "content"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
		},
	}
	err := validateInputSchema(json.RawMessage(`{"path":"a.txt"}`), spec)
	if err == nil {
		t.Fatalf("expected missing required error")
	}
}

func TestValidateInputSchemaTypeMismatch(t *testing.T) {
	spec := model.FunctionSpec{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timeout_seconds": map[string]any{"type": "integer"},
			},
		},
	}
	err := validateInputSchema(json.RawMessage(`{"timeout_seconds":"10"}`), spec)
	if err == nil {
		t.Fatalf("expected type mismatch error")
	}
}

func TestValidateToolInputWithSchema(t *testing.T) {
	params := map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	if err := validateToolInput(json.RawMessage(`{"query":"hello"}`), params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHookRunnerAllowsExitCodeZeroAndCapturesStdout(t *testing.T) {
	runner := NewHookRunner(HookConfig{
		PreToolUse: []string{hookShellSnippet("printf 'pre ok'")},
	})

	result := runner.RunPreToolUse("Read", `{"path":"README.md"}`)

	if result.IsDenied() {
		t.Fatalf("expected hook to allow")
	}
	if got := result.Messages(); len(got) != 1 || got[0] != "pre ok" {
		t.Fatalf("unexpected hook messages: %#v", got)
	}
}

func TestHookRunnerDeniesExitCodeTwo(t *testing.T) {
	runner := NewHookRunner(HookConfig{
		PreToolUse: []string{hookShellSnippet("printf 'blocked by hook'; exit 2")},
	})

	result := runner.RunPreToolUse("Bash", `{"command":"pwd"}`)

	if !result.IsDenied() {
		t.Fatalf("expected hook denial")
	}
	if got := result.Messages(); len(got) != 1 || got[0] != "blocked by hook" {
		t.Fatalf("unexpected hook messages: %#v", got)
	}
}

func TestHookRunnerWarnsForOtherNonZeroStatuses(t *testing.T) {
	runner := NewHookRunner(HookConfig{
		PreToolUse: []string{hookShellSnippet("printf 'warning hook'; exit 1")},
	})

	result := runner.RunPreToolUse("Edit", `{"file":"src/lib.rs"}`)

	if result.IsDenied() {
		t.Fatalf("expected warning, not denial")
	}
	if got := strings.Join(result.Messages(), "\n"); !strings.Contains(got, "allowing tool execution to continue") {
		t.Fatalf("unexpected warning output: %q", got)
	}
}

func TestApprovalPermissionDeciderChecksEditFileWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	decider := newApprovalPermissionDecider(dir, mockApprover{writeAllowed: false}, nil)
	err := decider.Decide(context.Background(), ToolInvocation{
		Name: "edit_file",
		Raw:  json.RawMessage(`{"path":"note.txt","old_text":"beta","new_text":"BETA"}`),
	})
	if err == nil || err.Error() != "file write rejected by user" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApprovalPermissionPolicyReturnsStructuredDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	policy := NewApprovalPermissionPolicy(dir, mockApprover{writeAllowed: false}, nil)
	decision := policy.Evaluate(context.Background(), ToolInvocation{
		Name: "edit_file",
		Raw:  json.RawMessage(`{"path":"note.txt","old_text":"beta","new_text":"BETA"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected structured policy denial")
	}
	if decision.Reason != "file write rejected by user" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
	if decision.Mode != PermissionModePrompt {
		t.Fatalf("unexpected mode: %q", decision.Mode)
	}
}

func TestApprovalPermissionPolicyReadOnlyDeniesWrites(t *testing.T) {
	dir := t.TempDir()
	policy := NewApprovalPermissionPolicy(dir, modeApprover{mode: PermissionModeReadOnly, writeAllowed: true, commandAllowed: true}, nil)
	decision := policy.Evaluate(context.Background(), ToolInvocation{
		Name: "write_file",
		Raw:  json.RawMessage(`{"path":"note.txt","content":"hello"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected read-only mode to deny writes")
	}
	if !strings.Contains(decision.Reason, "requires workspace-write permission") {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestApprovalPermissionPolicyWorkspaceWritePromptsCommands(t *testing.T) {
	dir := t.TempDir()
	policy := NewApprovalPermissionPolicy(dir, modeApprover{mode: PermissionModeWorkspaceWrite, commandAllowed: false, writeAllowed: true}, nil)
	decision := policy.Evaluate(context.Background(), ToolInvocation{
		Name: "run_command",
		Raw:  json.RawMessage(`{"command":"pwd"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected workspace-write mode to require command approval")
	}
	if decision.Reason != "command rejected by user" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestApprovalPermissionPolicyAllowsCommandRuleOverride(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadPermissionStore(filepath.Join(dir, "permissions.json"))
	if err != nil {
		t.Fatalf("load permission store: %v", err)
	}
	store.SetCommandMode("git status *", PermissionModeAllow)

	policy := NewApprovalPermissionPolicy(dir, modeApprover{mode: PermissionModePrompt, commandAllowed: false, writeAllowed: true}, store)
	decision := policy.Evaluate(context.Background(), ToolInvocation{
		Name: "run_command",
		Raw:  json.RawMessage(`{"command":"git status --short"}`),
	})
	if !decision.Allowed {
		t.Fatalf("expected allow override, got %#v", decision)
	}
	if decision.Mode != PermissionModeAllow {
		t.Fatalf("unexpected mode: %q", decision.Mode)
	}
}

func TestApprovalPermissionPolicyDeniesCommandRuleOverride(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadPermissionStore(filepath.Join(dir, "permissions.json"))
	if err != nil {
		t.Fatalf("load permission store: %v", err)
	}
	store.SetCommandMode("git push *", PermissionModeDeny)

	policy := NewApprovalPermissionPolicy(dir, modeApprover{mode: PermissionModeDangerFullAccess, commandAllowed: true, writeAllowed: true}, store)
	decision := policy.Evaluate(context.Background(), ToolInvocation{
		Name: "run_command",
		Raw:  json.RawMessage(`{"command":"git push origin main"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected deny override")
	}
	if !strings.Contains(decision.Reason, `policy pattern "git push *"`) {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

type modeApprover struct {
	mode           string
	writeAllowed   bool
	commandAllowed bool
}

func (m modeApprover) ApproveCommand(context.Context, string) (bool, error) {
	return m.commandAllowed, nil
}

func (m modeApprover) ApproveWrite(context.Context, string, string) (bool, error) {
	return m.writeAllowed, nil
}

func (m modeApprover) Mode() string {
	return m.mode
}

func (m modeApprover) SetMode(string) error {
	return nil
}

func hookShellSnippet(script string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(script, "'", "\"")
	}
	return script
}
