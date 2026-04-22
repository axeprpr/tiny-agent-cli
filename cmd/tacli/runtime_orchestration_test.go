package main

import (
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
)

func TestRuntimeOrchestratorSummariesAndReadyApply(t *testing.T) {
	o := newRuntimeOrchestrator(config.Config{}, "")
	now := time.Now()
	o.manager.jobs["job-001"] = &backgroundJob{id: "job-001", status: jobReady, updatedAt: now}
	o.manager.jobs["job-002"] = &backgroundJob{id: "job-002", status: jobRunning, updatedAt: now}

	if got := o.Summary(); got != "jobs=2 running=1 queued=0" {
		t.Fatalf("unexpected summary: %q", got)
	}

	ready := o.CollectReadyForApply()
	if len(ready) != 1 || ready[0].ID != "job-001" {
		t.Fatalf("unexpected ready jobs: %#v", ready)
	}

	o.MarkApplied("job-001")
	if next := o.CollectReadyForApply(); len(next) != 0 {
		t.Fatalf("expected job to be marked applied, got %#v", next)
	}
}

func TestRuntimeOrchestratorSubagentViews(t *testing.T) {
	o := newRuntimeOrchestrator(config.Config{}, "")
	o.manager.orchestration.Register(agent.SubagentSnapshot{
		ID:        "job-007",
		Status:    "ready",
		Role:      "explore",
		Model:     "test-model",
		TaskCount: 1,
	}, nil)

	list := o.ListSubagents()
	if len(list) != 1 || list[0].ID != "job-007" {
		t.Fatalf("unexpected subagent list: %#v", list)
	}

	snap, ok := o.SubagentSnapshot("job-007")
	if !ok {
		t.Fatalf("expected subagent snapshot")
	}
	if snap.Role != "explore" || snap.Status != "ready" {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}
}
