package session

import (
	"path/filepath"
	"testing"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "alpha.json")
	want := State{
		SessionName:   "alpha",
		Model:         "test-model",
		OutputMode:    "terminal",
		ApprovalMode:  "dangerously",
		TeamKey:       "eng",
		ScopeKey:      "/tmp/project",
		GlobalMemory:  []string{"Prefer concise answers"},
		TeamMemory:    []string{"Run reviews before merge"},
		ProjectMemory: []string{"Repo uses Go"},
		TodoItems:     []tools.TodoItem{{Text: "inspect repo", Status: "in_progress"}},
		TaskContract: tools.TaskContract{
			Objective: "Inspect the repository",
			Deliverables: []tools.ContractItem{
				{Text: "Summarize key architecture points", Status: "pending"},
			},
		},
		Messages: []model.Message{
			{Role: "user", Content: "inspect repo"},
			{Role: "assistant", Content: "done"},
		},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got.Model != want.Model || got.OutputMode != want.OutputMode || got.ApprovalMode != want.ApprovalMode {
		t.Fatalf("unexpected config fields: %#v", got)
	}
	if got.ScopeKey != want.ScopeKey {
		t.Fatalf("unexpected scope key: %q", got.ScopeKey)
	}
	if got.TeamKey != want.TeamKey {
		t.Fatalf("unexpected team key: %q", got.TeamKey)
	}
	if len(got.GlobalMemory) != 1 || got.GlobalMemory[0] != want.GlobalMemory[0] {
		t.Fatalf("unexpected global memory: %#v", got.GlobalMemory)
	}
	if len(got.TeamMemory) != 1 || got.TeamMemory[0] != want.TeamMemory[0] {
		t.Fatalf("unexpected team memory: %#v", got.TeamMemory)
	}
	if len(got.ProjectMemory) != 1 || got.ProjectMemory[0] != want.ProjectMemory[0] {
		t.Fatalf("unexpected project memory: %#v", got.ProjectMemory)
	}
	if len(got.TodoItems) != 1 || got.TodoItems[0] != want.TodoItems[0] {
		t.Fatalf("unexpected todo items: %#v", got.TodoItems)
	}
	if got.TaskContract.Objective != want.TaskContract.Objective {
		t.Fatalf("unexpected task contract: %#v", got.TaskContract)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("unexpected messages: %#v", got.Messages)
	}
	if got.SavedAt.IsZero() {
		t.Fatalf("expected saved timestamp")
	}
}

func TestListSessionsSortedBySavedAt(t *testing.T) {
	dir := t.TempDir()
	if err := Save(SessionPath(dir, "older"), State{SessionName: "older"}); err != nil {
		t.Fatalf("save older: %v", err)
	}
	if err := Save(SessionPath(dir, "newer"), State{SessionName: "newer", Model: "gpt", Messages: []model.Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("save newer: %v", err)
	}

	got, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %#v", got)
	}
	if got[0].Name != "newer" || got[1].Name != "older" {
		t.Fatalf("unexpected order: %#v", got)
	}
	if got[0].MessageCount != 1 {
		t.Fatalf("unexpected message count: %#v", got[0])
	}
}
