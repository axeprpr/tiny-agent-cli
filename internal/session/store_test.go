package session

import (
	"path/filepath"
	"strings"
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
	if got[0].Path == "" || got[1].Path == "" {
		t.Fatalf("expected session paths to be populated: %#v", got)
	}
}

func TestBuildSessionTreeResolvesIDAndLegacyPathParents(t *testing.T) {
	dir := t.TempDir()
	parentPath := SessionPath(dir, "root")
	if err := Save(parentPath, State{
		SessionID:   "root-id",
		SessionName: "root",
	}); err != nil {
		t.Fatalf("save root: %v", err)
	}
	if err := Save(SessionPath(dir, "child-by-id"), State{
		SessionID:     "child-id",
		ParentSession: "root-id",
		SessionName:   "child-by-id",
	}); err != nil {
		t.Fatalf("save child-by-id: %v", err)
	}
	if err := Save(SessionPath(dir, "child-by-path"), State{
		SessionID:     "child-path-id",
		ParentSession: parentPath,
		SessionName:   "child-by-path",
	}); err != nil {
		t.Fatalf("save child-by-path: %v", err)
	}

	roots, err := BuildSessionTree(dir)
	if err != nil {
		t.Fatalf("build session tree: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %#v", roots)
	}
	root := roots[0]
	if root.Summary.Name != "root" {
		t.Fatalf("expected root node to be root, got %#v", root.Summary)
	}
	if len(root.Children) != 2 {
		t.Fatalf("expected two children under root, got %#v", root.Children)
	}
	for _, child := range root.Children {
		if child.ParentName != "root" {
			t.Fatalf("expected child parent to resolve to root, got %#v", child)
		}
	}
}

func TestNewSessionIDHasStablePrefix(t *testing.T) {
	id := NewSessionID()
	if !strings.HasPrefix(id, "sess-") {
		t.Fatalf("unexpected session id format: %q", id)
	}
	if len(id) < len("sess-")+6 {
		t.Fatalf("session id too short: %q", id)
	}
}
