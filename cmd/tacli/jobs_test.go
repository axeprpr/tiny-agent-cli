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
