package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tiny-agent-cli/internal/model"
)

type stubTool struct {
	output string
	err    error
	onCall func()
}

func (t *stubTool) Definition() model.Tool {
	return model.Tool{}
}

func (t *stubTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	if t.onCall != nil {
		t.onCall()
	}
	return t.output, t.err
}

type recordHook struct {
	events *[]string
}

func (h recordHook) BeforeTool(_ context.Context, _ *ToolInvocation) error {
	*h.events = append(*h.events, "before")
	return nil
}

func (h recordHook) AfterTool(_ context.Context, _ *ToolInvocation, _ *ToolOutcome) error {
	*h.events = append(*h.events, "after")
	return nil
}

func (h recordHook) OnToolError(_ context.Context, _ *ToolInvocation, _ *ToolOutcome) error {
	*h.events = append(*h.events, "fail")
	return nil
}

type recordPermission struct {
	events *[]string
	err    error
}

func (p recordPermission) Decide(_ context.Context, _ ToolInvocation) error {
	if p.events != nil {
		*p.events = append(*p.events, "permission")
	}
	return p.err
}

type recordAudit struct {
	events []ToolAuditEvent
}

func (a *recordAudit) RecordToolEvent(_ context.Context, event ToolAuditEvent) {
	a.events = append(a.events, event)
}

func TestRegistryCallPipelineSuccess(t *testing.T) {
	var events []string
	tool := &stubTool{
		output: "ok",
		onCall: func() { events = append(events, "tool") },
	}
	r := &Registry{
		tools: map[string]Tool{
			"fake": tool,
		},
		permission: recordPermission{events: &events},
	}
	r.AddHook(recordHook{events: &events})

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
	want := []string{"before", "permission", "tool", "after"}
	if len(events) != len(want) {
		t.Fatalf("unexpected events: got=%v want=%v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("unexpected events: got=%v want=%v", events, want)
		}
	}
}

func TestRegistryCallPipelineAllowsHookMutation(t *testing.T) {
	var events []string
	tool := &stubTool{
		output: "initial",
		onCall: func() { events = append(events, "tool") },
	}
	hook := mutationHook{events: &events}
	r := &Registry{
		tools: map[string]Tool{
			"fake": tool,
		},
		permission: recordPermission{events: &events},
	}
	r.AddHook(hook)

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if out != "mutated" {
		t.Fatalf("unexpected output: %q", out)
	}
	want := []string{"before", "permission", "tool", "after"}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("unexpected events: got=%v want=%v", events, want)
		}
	}
}

func TestRegistryCallPipelineFailure(t *testing.T) {
	var events []string
	tool := &stubTool{
		output: "boom",
		err:    errors.New("tool failed"),
		onCall: func() { events = append(events, "tool") },
	}
	audit := &recordAudit{}
	r := &Registry{
		tools: map[string]Tool{
			"fake": tool,
		},
		permission: recordPermission{events: &events},
		audit:      audit,
	}
	r.AddHook(recordHook{events: &events})

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err == nil {
		t.Fatalf("expected error")
	}
	if out != "boom" {
		t.Fatalf("unexpected output: %q", out)
	}
	want := []string{"before", "permission", "tool", "fail"}
	if len(events) != len(want) {
		t.Fatalf("unexpected events: got=%v want=%v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("unexpected events: got=%v want=%v", events, want)
		}
	}
	if len(audit.events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audit.events))
	}
	if audit.events[0].Status != "error" {
		t.Fatalf("unexpected audit status: %s", audit.events[0].Status)
	}
}

func TestRegistryCallPermissionDeniedAudited(t *testing.T) {
	audit := &recordAudit{}
	r := &Registry{
		tools: map[string]Tool{
			"fake": &stubTool{output: "ok"},
		},
		permission: recordPermission{err: errors.New("denied")},
		audit:      audit,
	}
	_, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err == nil {
		t.Fatalf("expected permission error")
	}
	if len(audit.events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audit.events))
	}
	if audit.events[0].Status != "permission_denied" {
		t.Fatalf("unexpected audit status: %s", audit.events[0].Status)
	}
}

func TestRegistryCallPipelineCanHandleToolError(t *testing.T) {
	tool := &stubTool{
		output: "boom",
		err:    errors.New("tool failed"),
	}
	r := &Registry{
		tools: map[string]Tool{
			"fake": tool,
		},
	}
	r.AddHook(errorHandlingHook{})

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("expected hook to recover, got %v", err)
	}
	if out != "recovered" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRegistryCallPreToolHookDenialBlocksExecution(t *testing.T) {
	called := false
	r := &Registry{
		tools: map[string]Tool{
			"fake": &stubTool{
				output: "ok",
				onCall: func() { called = true },
			},
		},
		hookRunner: NewHookRunner(HookConfig{
			PreToolUse: []string{hookShellSnippet("printf 'blocked by hook'; exit 2")},
		}),
	}

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err == nil {
		t.Fatalf("expected hook denial error")
	}
	if called {
		t.Fatalf("tool should not execute when hook denies")
	}
	if !strings.Contains(out, "Hook feedback (denied):\nblocked by hook") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRegistryCallAppendsPreAndPostHookFeedback(t *testing.T) {
	r := &Registry{
		tools: map[string]Tool{
			"fake": &stubTool{output: "tool output"},
		},
		hookRunner: NewHookRunner(HookConfig{
			PreToolUse:  []string{hookShellSnippet("printf 'pre hook ran'")},
			PostToolUse: []string{hookShellSnippet("printf 'post hook ran'")},
		}),
	}

	out, err := r.Call(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !strings.Contains(out, "tool output") {
		t.Fatalf("missing tool output: %q", out)
	}
	if !strings.Contains(out, "pre hook ran") {
		t.Fatalf("missing pre hook feedback: %q", out)
	}
	if !strings.Contains(out, "post hook ran") {
		t.Fatalf("missing post hook feedback: %q", out)
	}
}

func TestRegistryCallStructuredForRuntimeSkipsPermissionAndHookRunner(t *testing.T) {
	called := false
	r := &Registry{
		tools: map[string]Tool{
			"fake": &stubTool{
				output: "tool output",
				onCall: func() { called = true },
			},
		},
		permission: recordPermission{err: errors.New("denied")},
		hookRunner: NewHookRunner(HookConfig{
			PreToolUse: []string{hookShellSnippet("printf 'blocked by hook'; exit 2")},
		}),
	}

	out, err := r.CallStructuredForRuntime(context.Background(), "fake", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !called {
		t.Fatalf("expected tool to execute without registry-side permission/hook gate")
	}
	if out.Status != "ok" || out.Output != "tool output" {
		t.Fatalf("unexpected runtime call result: %#v", out)
	}
}

type mutationHook struct {
	events *[]string
}

func (h mutationHook) BeforeTool(_ context.Context, _ *ToolInvocation) error {
	*h.events = append(*h.events, "before")
	return nil
}

func (h mutationHook) AfterTool(_ context.Context, _ *ToolInvocation, out *ToolOutcome) error {
	*h.events = append(*h.events, "after")
	out.Output = "mutated"
	return nil
}

func (h mutationHook) OnToolError(_ context.Context, _ *ToolInvocation, _ *ToolOutcome) error {
	return nil
}

type errorHandlingHook struct{}

func (errorHandlingHook) BeforeTool(_ context.Context, _ *ToolInvocation) error {
	return nil
}

func (errorHandlingHook) AfterTool(_ context.Context, _ *ToolInvocation, _ *ToolOutcome) error {
	return nil
}

func (errorHandlingHook) OnToolError(_ context.Context, _ *ToolInvocation, out *ToolOutcome) error {
	out.Output = "recovered"
	out.Err = nil
	return nil
}
