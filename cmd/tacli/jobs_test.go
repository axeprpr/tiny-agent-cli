package main

import (
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/config"
)

func TestSummarizeBackgroundResultStructured(t *testing.T) {
	text := `Key findings:
- auth flow has two entry points
Relevant files:
- internal/auth/service.go
- internal/auth/handler.go
Risks or unknowns:
- token refresh path is untested
Recommended next steps:
- add tests around refresh`

	got := summarizeBackgroundResult(text)
	if len(got.Findings) != 1 || got.Findings[0] != "auth flow has two entry points" {
		t.Fatalf("unexpected findings: %#v", got.Findings)
	}
	if len(got.Files) != 2 {
		t.Fatalf("unexpected files: %#v", got.Files)
	}
	if len(got.Risks) != 1 || len(got.NextSteps) != 1 {
		t.Fatalf("unexpected summary: %#v", got)
	}
}

func TestSummarizeBackgroundResultFallback(t *testing.T) {
	got := summarizeBackgroundResult("first line\nsecond line\nthird line\nfourth line")
	if len(got.Findings) != 3 {
		t.Fatalf("unexpected fallback findings: %#v", got.Findings)
	}
}

func TestRenderJobSummary(t *testing.T) {
	text := renderJobSummary(jobSummary{
		Findings: []string{"found one issue"},
		Files:    []string{"a.go"},
	})
	if !strings.Contains(text, "findings:") || !strings.Contains(text, "files:") {
		t.Fatalf("unexpected rendered summary: %q", text)
	}
}

func TestCollectReadyForApply(t *testing.T) {
	m := newJobManager(config.Config{}, "")
	now := time.Now()
	m.jobs["job-001"] = &backgroundJob{id: "job-001", status: jobReady, updatedAt: now}
	m.jobs["job-002"] = &backgroundJob{id: "job-002", status: jobRunning, updatedAt: now}
	m.jobs["job-003"] = &backgroundJob{id: "job-003", status: jobReady, applied: true, updatedAt: now}

	got := m.CollectReadyForApply()
	if len(got) != 1 || got[0].ID != "job-001" {
		t.Fatalf("unexpected ready jobs: %#v", got)
	}

	m.MarkApplied("job-001")
	if next := m.CollectReadyForApply(); len(next) != 0 {
		t.Fatalf("expected no remaining jobs, got %#v", next)
	}
}

func TestValidateBackgroundRole(t *testing.T) {
	for _, role := range []string{"", "general", "explore", "plan", "implement", "verify"} {
		if err := validateBackgroundRole(role); err != nil {
			t.Fatalf("expected role %q to be valid: %v", role, err)
		}
	}
	if err := validateBackgroundRole("weird"); err == nil {
		t.Fatalf("expected invalid role error")
	}
}

func TestFormatJobListIncludesRole(t *testing.T) {
	text := formatJobList([]jobSnapshot{{
		ID:        "job-007",
		Status:    jobReady,
		Role:      backgroundRoleVerify,
		Model:     "test-model",
		TaskCount: 1,
	}})
	if !strings.Contains(text, "role=verify") {
		t.Fatalf("expected role in list: %q", text)
	}
}

func TestRouteBackgroundRole(t *testing.T) {
	tests := []struct {
		task string
		want string
	}{
		{task: "Run build and tests, then provide a verdict", want: backgroundRoleVerify},
		{task: "Give me a concrete plan and step breakdown", want: backgroundRolePlan},
		{task: "Implement a patch for auth token refresh", want: backgroundRoleImplement},
		{task: "Explore this repo in read-only mode and find risks", want: backgroundRoleExplore},
		{task: "continue working", want: backgroundRoleGeneral},
	}
	for _, tt := range tests {
		if got := routeBackgroundRole(tt.task); got != tt.want {
			t.Fatalf("task %q: got %q want %q", tt.task, got, tt.want)
		}
	}
}

func TestShouldPairBackgroundVerify(t *testing.T) {
	if !shouldPairBackgroundVerify(backgroundRoleImplement, false) {
		t.Fatalf("expected default implement role to pair with verify")
	}
	if shouldPairBackgroundVerify(backgroundRoleImplement, true) {
		t.Fatalf("expected explicit implement role to skip automatic verify pairing")
	}
	if shouldPairBackgroundVerify(backgroundRoleVerify, false) {
		t.Fatalf("did not expect verify role to pair again")
	}
}

func TestBuildVerifyFollowupTask(t *testing.T) {
	text := buildVerifyFollowupTask("Implement the auth refresh fix")
	for _, want := range []string{
		"read-only mode",
		"update_task_contract",
		"Original subtask: Implement the auth refresh fix",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("follow-up task missing %q: %q", want, text)
		}
	}
}

func TestBuildBackgroundTaskContract(t *testing.T) {
	contract := buildBackgroundTaskContract("Implement the auth refresh fix")
	if contract.TaskKind != "background_implement" {
		t.Fatalf("unexpected task kind: %#v", contract)
	}
	if !strings.Contains(contract.Objective, "Implement the auth refresh fix") {
		t.Fatalf("unexpected objective: %#v", contract)
	}
	if len(contract.AcceptanceChecks) < 2 {
		t.Fatalf("expected bootstrap acceptance checks, got %#v", contract)
	}
}
