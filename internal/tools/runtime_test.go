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

func hookShellSnippet(script string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(script, "'", "\"")
	}
	return script
}
