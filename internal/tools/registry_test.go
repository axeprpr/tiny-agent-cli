package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegistryIncludesTodoTools(t *testing.T) {
	r := NewRegistry(".", "bash", time.Second, nil)
	defs := r.Definitions()
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	if !names["update_todo"] {
		t.Fatalf("update_todo missing from registry")
	}
	if !names["show_todo"] {
		t.Fatalf("show_todo missing from registry")
	}
	if !names["update_task_contract"] {
		t.Fatalf("update_task_contract missing from registry")
	}
	if !names["show_task_contract"] {
		t.Fatalf("show_task_contract missing from registry")
	}
	if !names["edit_file"] {
		t.Fatalf("edit_file missing from registry")
	}
}

func TestRegistryPreviewCompactsTodoItems(t *testing.T) {
	r := NewRegistry(".", "bash", time.Second, nil)
	raw := json.RawMessage(`{"items":[{"text":"inspect repo layout","status":"completed"},{"text":"patch agent loop","status":"in_progress"}]}`)
	got := r.Preview("update_todo", raw)
	if !strings.Contains(got, "inspect repo layout") {
		t.Fatalf("unexpected preview: %q", got)
	}
}

func TestRegistryPreviewCompactsTaskContract(t *testing.T) {
	r := NewRegistry(".", "bash", time.Second, nil)
	raw := json.RawMessage(`{"task_kind":"webapp_with_deploy","objective":"Ship the embedded app","deliverables":[{"text":"go binary","status":"completed"}],"acceptance_checks":[{"text":"GET / returns app html","status":"pending"}]}`)
	got := r.Preview("update_task_contract", raw)
	if !strings.Contains(got, "webapp_with_deploy") || !strings.Contains(got, "Ship the embedded app") {
		t.Fatalf("unexpected preview: %q", got)
	}
}
