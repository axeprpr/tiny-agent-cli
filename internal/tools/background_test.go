package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeJobControl struct {
	startedRole string
	startedTask string
	isolation   string
	startedID   string
}

func (f *fakeJobControl) Start(task string) (string, error) {
	f.startedTask = task
	f.startedID = "job-start"
	return f.startedID, nil
}

func (f *fakeJobControl) StartWithRole(role, task string) (string, error) {
	f.startedRole = role
	f.startedTask = task
	f.startedID = "job-role"
	return f.startedID, nil
}

func (f *fakeJobControl) StartWithRoleAndOptions(role, task string, opts BackgroundStartOptions) (string, error) {
	f.startedRole = role
	f.startedTask = task
	f.isolation = opts.Isolation
	f.startedID = "job-role"
	return f.startedID, nil
}

func (f *fakeJobControl) Send(_, _ string) error { return nil }

func (f *fakeJobControl) Cancel(_ string) error { return nil }

func (f *fakeJobControl) List() []BackgroundJobSnapshot { return nil }

func (f *fakeJobControl) Snapshot(_ string) (BackgroundJobSnapshot, bool) {
	return BackgroundJobSnapshot{}, false
}

func TestDelegateSubagentToolStartsRoleTask(t *testing.T) {
	jobs := &fakeJobControl{}
	tool := newDelegateSubagentTool(jobs)
	raw := json.RawMessage(`{"role":"verify","isolation":"read_only","task":"Run tests and summarize failures"}`)
	got, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("delegate_subagent call failed: %v", err)
	}
	if jobs.startedRole != "verify" {
		t.Fatalf("unexpected role: %q", jobs.startedRole)
	}
	if jobs.startedTask != "Run tests and summarize failures" {
		t.Fatalf("unexpected task: %q", jobs.startedTask)
	}
	if jobs.isolation != "read_only" {
		t.Fatalf("unexpected isolation: %q", jobs.isolation)
	}
	if !strings.Contains(got, "job-role") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestRegistryIncludesDelegateSubagentWhenJobsEnabled(t *testing.T) {
	jobs := &fakeJobControl{}
	r := NewRegistry(".", "bash", time.Second, nil, jobs)
	defs := r.Definitions()
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	if !names["delegate_subagent"] {
		t.Fatalf("delegate_subagent missing from registry")
	}

	preview := r.Preview("delegate_subagent", json.RawMessage(`{"role":"plan","task":"Create implementation breakdown"}`))
	if !strings.Contains(preview, "role=plan") || !strings.Contains(preview, "task=Create implementation breakdown") {
		t.Fatalf("unexpected preview: %q", preview)
	}
}
