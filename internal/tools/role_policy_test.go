package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvaluateRolePermissionVerifyDeniesWriteFile(t *testing.T) {
	decision := EvaluateRolePermission(AgentRoleVerify, ToolInvocation{
		Name: "write_file",
		Raw:  json.RawMessage(`{"path":"README.md","content":"x"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected verify role to deny write_file")
	}
	if !strings.Contains(decision.Reason, "read-only") {
		t.Fatalf("unexpected denial reason: %q", decision.Reason)
	}
}

func TestEvaluateRolePermissionVerifyAllowsReadOnlyRunCommand(t *testing.T) {
	decision := EvaluateRolePermission(AgentRoleVerify, ToolInvocation{
		Name: "run_command",
		Raw:  json.RawMessage(`{"command":"go test ./..."}`),
	})
	if !decision.Allowed {
		t.Fatalf("expected verify role to allow go test, got %#v", decision)
	}
}

func TestEvaluateRolePermissionVerifyDeniesMutatingRunCommand(t *testing.T) {
	decision := EvaluateRolePermission(AgentRoleVerify, ToolInvocation{
		Name: "run_command",
		Raw:  json.RawMessage(`{"command":"npm install"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected verify role to deny mutating command")
	}
	if !strings.Contains(decision.Reason, "read-only shell commands") {
		t.Fatalf("unexpected denial reason: %q", decision.Reason)
	}
}

func TestEvaluateRolePermissionPlanDeniesEditFile(t *testing.T) {
	decision := EvaluateRolePermission(AgentRolePlan, ToolInvocation{
		Name: "edit_file",
		Raw:  json.RawMessage(`{"path":"README.md","old_text":"a","new_text":"b"}`),
	})
	if decision.Allowed {
		t.Fatalf("expected plan role to deny edit_file")
	}
}
