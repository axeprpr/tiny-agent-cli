package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateTaskContractToolPersistsAndShowsContract(t *testing.T) {
	store := newContractStoreWithPath(filepath.Join(t.TempDir(), "contract-v1.json"))
	update := newUpdateTaskContractTool(store)
	show := newShowTaskContractTool(store)

	raw := json.RawMessage(`{
		"task_kind":"webapp_with_deploy",
		"objective":"Ship the embedded app",
		"deliverables":[{"text":"single binary","status":"completed","evidence":"go build ./..."}],
		"acceptance_checks":[{"text":"GET / returns app html","status":"pending"}]
	}`)
	out, err := update.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("update task contract failed: %v", err)
	}
	if !strings.Contains(out, "objective=Ship the embedded app") {
		t.Fatalf("unexpected contract output: %q", out)
	}

	showOut, err := show.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("show task contract failed: %v", err)
	}
	if !strings.Contains(showOut, "GET / returns app html") {
		t.Fatalf("expected acceptance check in show output: %q", showOut)
	}
}

func TestTaskContractRejectsEmptyObjective(t *testing.T) {
	store := newContractStoreWithPath(filepath.Join(t.TempDir(), "contract-v1.json"))
	update := newUpdateTaskContractTool(store)
	_, err := update.Call(context.Background(), json.RawMessage(`{"deliverables":[{"text":"binary","status":"pending"}]}`))
	if err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected objective validation error, got %v", err)
	}
}

func TestTaskContractReplacePersistsLatestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contract-v1.json")
	store := newContractStoreWithPath(path)
	if err := store.Replace(TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		AcceptanceChecks: []ContractItem{
			{Text: "GET / returns app html", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := store.Replace(TaskContract{
		TaskKind:  "webapp_with_deploy",
		Objective: "Ship the embedded app",
		AcceptanceChecks: []ContractItem{
			{Text: "GET / returns app html", Status: "completed", Evidence: "curl -I http://127.0.0.1:4173"},
		},
	}); err != nil {
		t.Fatalf("replace contract: %v", err)
	}

	loaded, err := LoadTaskContract(path)
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	if got := loaded.AcceptanceChecks[0].Status; got != "completed" {
		t.Fatalf("expected persisted completed status, got %q", got)
	}
	if got := loaded.AcceptanceChecks[0].Evidence; got != "curl -I http://127.0.0.1:4173" {
		t.Fatalf("unexpected persisted evidence: %q", got)
	}
}
